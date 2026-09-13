package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/repositorygraph"
)

func (p *Postgres) CreateRepositoryGraph(ctx context.Context, id, key string, input domain.GraphInput) (domain.RepositoryGraph, error) {
	if p.access == nil || p.access.request.RepositoryID == "" {
		return domain.RepositoryGraph{}, domain.ErrForbidden
	}
	if !domain.IsLowerHex(id, 32) || domain.ValidateCollectionKey(key) != nil {
		return domain.RepositoryGraph{}, domain.ErrInvalidInput
	}
	in, err := domain.NormalizeGraphInput(input)
	if err != nil {
		return domain.RepositoryGraph{}, err
	}
	anchor := false
	// Sorted repository locks keep access revocation atomic with graph publication
	// and avoid lock order inversions between overlapping cross-repository requests.
	for _, source := range in.Sources {
		if source.RepositoryID == p.access.request.RepositoryID {
			anchor = true
		}
		if err = p.authorizeRepository(ctx, p.tx, source.RepositoryID, "author"); err != nil {
			return domain.RepositoryGraph{}, err
		}
	}
	if !anchor {
		return domain.RepositoryGraph{}, domain.ErrNotFound
	}
	inputDigest, err := domain.JSONDigest(in)
	if err != nil {
		return domain.RepositoryGraph{}, err
	}
	if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "graph-create:"+p.access.request.WorkspaceID+":"+p.access.principal.ID+":"+key); err != nil {
		return domain.RepositoryGraph{}, err
	}
	var oldID, oldDigest string
	err = p.tx.QueryRow(ctx, `SELECT id,input_digest FROM repository_graphs WHERE workspace_id=$1 AND creator_id=$2 AND idempotency_key=$3`, p.access.request.WorkspaceID, p.access.principal.ID, key).Scan(&oldID, &oldDigest)
	if err == nil {
		if oldDigest != inputDigest {
			return domain.RepositoryGraph{}, domain.ErrIdempotencyConflict
		}
		return p.RepositoryGraph(ctx, oldID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.RepositoryGraph{}, err
	}
	receipts := make([]repositorygraph.Receipt, 0, len(in.Sources))
	for _, source := range in.Sources {
		work, err := readCollection(ctx, p.tx, source.CollectionID, false)
		if err != nil {
			return domain.RepositoryGraph{}, err
		}
		c := work.Collection
		if c.WorkspaceID != p.access.request.WorkspaceID || c.RepositoryID != source.RepositoryID {
			return domain.RepositoryGraph{}, domain.ErrNotFound
		}
		if c.Receipt == nil || c.Receipt.Digest != source.Digest {
			return domain.RepositoryGraph{}, domain.ErrInvalidInput
		}
		var index domain.CodeGraphIndex
		var indexDigest string
		err = p.tx.QueryRow(ctx, `SELECT index_digest,index_data FROM context_codegraph_indexes WHERE collection_id=$1 AND receipt_digest=$2`, source.CollectionID, source.Digest).Scan(&indexDigest, &index)
		r := repositorygraph.Receipt{Source: source, Receipt: *c.Receipt}
		if err == nil {
			digest, e := domain.JSONDigest(index)
			if e != nil || digest != indexDigest {
				return domain.RepositoryGraph{}, domain.ErrUnavailable
			}
			r.Index = &index
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return domain.RepositoryGraph{}, err
		}
		if source.FullSourceDigest != "" {
			var fullIndex domain.CodeGraphIndex
			e := p.tx.QueryRow(ctx, `SELECT artifacts,truncated,index_data FROM context_source_bundles WHERE collection_id=$1 AND workspace_id=$2 AND repository_id=$3 AND receipt_digest=$4 AND commit_oid=$5 AND bundle_digest=$6`, source.CollectionID, c.WorkspaceID, source.RepositoryID, source.Digest, c.Input.Commit, source.FullSourceDigest).Scan(&r.FullArtifacts, &r.FullTruncated, &fullIndex)
			if errors.Is(e, pgx.ErrNoRows) {
				return domain.RepositoryGraph{}, domain.ErrInvalidInput
			}
			if e != nil {
				return domain.RepositoryGraph{}, e
			}
			r.Index = &fullIndex
		}
		receipts = append(receipts, r)
	}
	snapshot, err := repositorygraph.Build(ctx, receipts)
	if err != nil {
		return domain.RepositoryGraph{}, err
	}
	digest, err := domain.JSONDigest(snapshot)
	if err != nil {
		return domain.RepositoryGraph{}, err
	}
	g := domain.RepositoryGraph{ID: id, WorkspaceID: p.access.request.WorkspaceID, CreatorID: p.access.principal.ID, Digest: digest, Snapshot: snapshot}
	err = p.tx.QueryRow(ctx, `INSERT INTO repository_graphs(id,workspace_id,creator_id,idempotency_key,input_digest,digest,snapshot) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, id, g.WorkspaceID, g.CreatorID, key, inputDigest, digest, snapshot).Scan(&g.CreatedAt)
	if err != nil {
		return domain.RepositoryGraph{}, fmt.Errorf("persist repository graph: %w", err)
	}
	for _, source := range in.Sources {
		if _, err = p.tx.Exec(ctx, `INSERT INTO repository_graph_sources(graph_id,workspace_id,repository_id,collection_id,receipt_digest) VALUES($1,$2,$3,$4,$5)`, id, g.WorkspaceID, source.RepositoryID, source.CollectionID, source.Digest); err != nil {
			return domain.RepositoryGraph{}, err
		}
	}
	if _, err = p.tx.Exec(ctx, `INSERT INTO repository_graph_audit_events(graph_id,actor,event_type,data) VALUES($1,$2,'graph.created',$3)`, id, g.CreatorID, map[string]any{"digest": digest, "inputDigest": inputDigest}); err != nil {
		return domain.RepositoryGraph{}, fmt.Errorf("audit repository graph: %w", err)
	}
	return g, nil
}

// RepositoryGraph checks all source repositories before decoding any graph
// content. Denial is indistinguishable from an unknown snapshot, including when
// only one endpoint of a dependency has been revoked.
func (p *Postgres) RepositoryGraph(ctx context.Context, id string) (domain.RepositoryGraph, error) {
	var g domain.RepositoryGraph
	if p.access == nil || p.access.request.RepositoryID == "" {
		return g, domain.ErrForbidden
	}
	rows, err := p.tx.Query(ctx, `SELECT repository_id FROM repository_graph_sources WHERE graph_id=$1 AND workspace_id=$2 ORDER BY repository_id LIMIT $3`, id, p.access.request.WorkspaceID, domain.MaxGraphSources+1)
	if err != nil {
		return g, err
	}
	var repos []string
	for rows.Next() {
		var repo string
		if err = rows.Scan(&repo); err != nil {
			rows.Close()
			return g, err
		}
		repos = append(repos, repo)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return g, err
	}
	if len(repos) < 1 || len(repos) > domain.MaxGraphSources {
		return g, domain.ErrNotFound
	}
	anchor := false
	for _, repo := range repos {
		if repo == p.access.request.RepositoryID {
			anchor = true
		}
		if err = p.authorizeRepository(ctx, p.tx, repo, "read"); err != nil {
			return g, err
		}
	}
	if !anchor {
		return g, domain.ErrNotFound
	}
	err = p.tx.QueryRow(ctx, `SELECT id,workspace_id,creator_id,digest,snapshot,created_at FROM repository_graphs WHERE id=$1 AND workspace_id=$2`, id, p.access.request.WorkspaceID).Scan(&g.ID, &g.WorkspaceID, &g.CreatorID, &g.Digest, &g.Snapshot, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, domain.ErrNotFound
	}
	return g, err
}
func (p *Postgres) RepositoryGraphs(ctx context.Context, before string, limit int) (domain.RepositoryGraphPage, error) {
	page := domain.RepositoryGraphPage{Graphs: []domain.RepositoryGraphSummary{}}
	if p.access == nil || p.access.request.RepositoryID == "" {
		return page, domain.ErrForbidden
	}
	cursor, err := domain.ValidateChangeQuery("", before, limit)
	if err != nil {
		return page, err
	}
	if err = p.authorizeRepository(ctx, p.tx, p.access.request.RepositoryID, "read"); err != nil {
		return page, err
	}
	args := []any{p.access.request.WorkspaceID, p.access.request.RepositoryID, p.access.principal.ID, limit + 1}
	query := `SELECT g.id,g.digest,g.created_at FROM repository_graphs g WHERE g.workspace_id=$1 AND EXISTS(SELECT 1 FROM repository_graph_sources s WHERE s.graph_id=g.id AND s.repository_id=$2) AND NOT EXISTS(SELECT 1 FROM repository_graph_sources s LEFT JOIN repository_grants p ON p.repository_id=s.repository_id AND p.principal_id=$3 WHERE s.graph_id=g.id AND (p.can_read IS DISTINCT FROM true))`
	if cursor != nil {
		query += ` AND (g.created_at,g.id)<($5,$6)`
		args = append(args, cursor.CreatedAt, cursor.ID)
	}
	query += ` ORDER BY g.created_at DESC,g.id DESC LIMIT $4`
	rows, err := p.tx.Query(ctx, query, args...)
	if err != nil {
		return page, err
	}
	var values []domain.RepositoryGraphSummary
	for rows.Next() {
		var v domain.RepositoryGraphSummary
		if err = rows.Scan(&v.ID, &v.Digest, &v.CreatedAt); err != nil {
			rows.Close()
			return page, err
		}
		values = append(values, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return page, err
	}
	// Lock and recheck every visible row after filtering. A concurrent revocation
	// cannot race a response containing a graph (or its pagination boundary).
	for _, v := range values {
		if _, err = p.RepositoryGraph(ctx, v.ID); err != nil {
			return domain.RepositoryGraphPage{}, err
		}
	}
	if len(values) > limit {
		values = values[:limit]
		last := values[len(values)-1]
		page.NextBefore, err = domain.EncodeChangeCursor(last.CreatedAt, last.ID)
		if err != nil {
			return page, err
		}
	}
	page.Graphs = append(page.Graphs, values...)
	return page, nil
}
