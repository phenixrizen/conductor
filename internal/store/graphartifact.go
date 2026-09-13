package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

// RepositoryGraphArtifact holds every graph repository grant in the same read
// transaction before querying text. Its SQL never selects private Git bundles.
func (p *Postgres) RepositoryGraphArtifact(ctx context.Context, id string, q domain.GraphArtifactQuery) (domain.GraphArtifactResult, error) {
	var result domain.GraphArtifactResult
	if !domain.IsLowerHex(id, 32) || domain.ValidateGraphArtifactQuery(q) != nil {
		return result, domain.ErrInvalidInput
	}
	graph, err := p.RepositoryGraph(ctx, id)
	if err != nil {
		return result, err
	}
	if graph.Digest != q.GraphDigest {
		return result, domain.ErrConflict
	}
	selected := domain.GraphSource{RepositoryID: q.RepositoryID, CollectionID: q.CollectionID, Digest: q.ReceiptDigest, FullSourceDigest: q.FullSourceDigest}
	found := false
	for _, source := range graph.Snapshot.Sources {
		if source.GraphSource == selected {
			result.Source = source
			found = true
			break
		}
	}
	if !found {
		return result, domain.ErrNotFound
	}
	result.GraphID = graph.ID
	result.Digest = graph.Digest
	result.Coverage = "selected_paths"
	var encoded json.RawMessage
	if selected.FullSourceDigest != "" {
		result.Coverage = "full_source"
		err = p.tx.QueryRow(ctx, `SELECT COALESCE((SELECT a FROM jsonb_array_elements(s.artifacts) a WHERE a->>'path'=$7 LIMIT 1),'null'::jsonb),s.truncated FROM context_source_bundles s WHERE collection_id=$1 AND workspace_id=$2 AND repository_id=$3 AND receipt_digest=$4 AND commit_oid=$5 AND bundle_digest=$6`, selected.CollectionID, graph.WorkspaceID, selected.RepositoryID, selected.Digest, result.Source.Commit, selected.FullSourceDigest, q.Path).Scan(&encoded, &result.CoverageTruncated)
	} else {
		err = p.tx.QueryRow(ctx, `SELECT COALESCE((SELECT a FROM jsonb_array_elements(r.snapshot->'artifacts') a WHERE a->>'path'=$6 LIMIT 1),'null'::jsonb) FROM context_receipts r JOIN context_collections c ON c.id=r.collection_id WHERE r.collection_id=$1 AND c.workspace_id=$2 AND c.repository_id=$3 AND r.digest=$4 AND r.snapshot->>'commit'=$5`, selected.CollectionID, graph.WorkspaceID, selected.RepositoryID, selected.Digest, result.Source.Commit, q.Path).Scan(&encoded)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.GraphArtifactResult{}, domain.ErrUnavailable
	}
	if err != nil {
		return domain.GraphArtifactResult{}, err
	}
	// Return explicit coverage gaps for unretained paths; absence is not proof that
	// the path is missing at the provider. No freshness check or provider I/O occurs.
	var artifact *domain.ContextArtifact
	if len(encoded) > 512<<10 || json.Unmarshal(encoded, &artifact) != nil {
		return domain.GraphArtifactResult{}, domain.ErrUnavailable
	}
	if artifact == nil {
		result.Artifact = domain.ContextArtifact{Path: q.Path, State: "unavailable", Message: "Path not retained in this immutable source snapshot."}
		return result, nil
	}
	if artifact.Path != q.Path || len(artifact.Message) > 1024 {
		return domain.GraphArtifactResult{}, domain.ErrUnavailable
	}
	if artifact.State == "collected" {
		if artifact.Text == nil || len(*artifact.Text) > domain.MaxContextArtifactBytes || !domain.IsContextText([]byte(*artifact.Text)) {
			return domain.GraphArtifactResult{}, domain.ErrUnavailable
		}
		digest := sha256.Sum256([]byte(*artifact.Text))
		if hex.EncodeToString(digest[:]) != artifact.Digest {
			return domain.GraphArtifactResult{}, domain.ErrUnavailable
		}
	} else if artifact.State != "missing" && artifact.State != "unavailable" && artifact.State != "truncated" || artifact.Text != nil || artifact.Digest != "" {
		return domain.GraphArtifactResult{}, domain.ErrUnavailable
	}
	result.Artifact = *artifact
	return result, nil
}
