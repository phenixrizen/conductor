package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/sourcebundle"
)

func (p *Postgres) CompleteCollectionWithSource(ctx context.Context, id, binding string, artifacts []domain.ContextArtifact, source domain.SourceBundleData, index domain.CodeGraphIndex) (domain.CollectionReceipt, error) {
	if sourcebundle.Validate(source) != nil || domain.ValidateCodeGraphIndex(index, source.Artifacts) != nil {
		return domain.CollectionReceipt{}, domain.ErrInvalidInput
	}
	return p.completeCollection(ctx, id, binding, artifacts, nil, &source, &index)
}
func (p *Postgres) persistSourceBundle(ctx context.Context, c domain.Collection, r domain.CollectionReceipt, data domain.SourceBundleData, index *domain.CodeGraphIndex) error {
	if !c.Input.FullSource || data.Commit != c.Input.Commit || index == nil || sourcebundle.Validate(data) != nil || domain.ValidateCodeGraphIndex(*index, data.Artifacts) != nil {
		return domain.ErrInvalidInput
	}
	_, err := p.tx.Exec(ctx, `INSERT INTO context_source_bundles(collection_id,workspace_id,repository_id,receipt_digest,commit_oid,tree_oid,bundle_digest,bundle,artifacts,file_count,truncated,index_data) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, c.ID, c.WorkspaceID, c.RepositoryID, r.Digest, data.Commit, data.Tree, data.Digest, data.Bundle, data.Artifacts, data.FileCount, data.Truncated, index)
	return err
}
func readSourceBundleSummary(ctx context.Context, q queryExecutor, id, digest string) (domain.SourceBundleSummary, error) {
	var s domain.SourceBundleSummary
	err := q.QueryRow(ctx, `SELECT bundle_digest,tree_oid,commit_oid,octet_length(bundle),file_count,jsonb_array_length(index_data->'files'),truncated,created_at FROM context_source_bundles WHERE collection_id=$1 AND receipt_digest=$2`, id, digest).Scan(&s.Digest, &s.Tree, &s.Commit, &s.Size, &s.FileCount, &s.IndexedFiles, &s.Truncated, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, domain.ErrUnavailable
	}
	return s, err
}

// LoadSourceBundle is a trusted integration boundary, not an HTTP endpoint. A
// scoped transaction also holds current permission; unscoped trusted workers must
// authorize their own immutable command before requesting private source bytes.
func (p *Postgres) LoadSourceBundle(ctx context.Context, workspace, repo, id, receiptDigest, commit string) (domain.SourceBundle, error) {
	var s domain.SourceBundle
	if domain.ValidateAccessID(workspace) != nil || domain.ValidateAccessID(repo) != nil || !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(receiptDigest, 64) || !domain.IsLowerHex(commit, 40) {
		return s, domain.ErrInvalidInput
	}
	if p.access != nil {
		if p.access.request.WorkspaceID != workspace || p.access.request.RepositoryID != "" && p.access.request.RepositoryID != repo {
			return s, domain.ErrNotFound
		}
		if err := p.authorizeRepository(ctx, p.queries(), repo, "read"); err != nil {
			return s, err
		}
	}
	err := p.queries().QueryRow(ctx, `SELECT collection_id,workspace_id,repository_id,receipt_digest,commit_oid,tree_oid,bundle_digest,bundle,artifacts,file_count,truncated FROM context_source_bundles WHERE collection_id=$1 AND workspace_id=$2 AND repository_id=$3 AND receipt_digest=$4 AND commit_oid=$5`, id, workspace, repo, receiptDigest, commit).Scan(&s.CollectionID, &s.WorkspaceID, &s.RepositoryID, &s.ReceiptDigest, &s.Commit, &s.Tree, &s.Digest, &s.Bundle, &s.Artifacts, &s.FileCount, &s.Truncated)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, domain.ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if err = sourcebundle.Validate(s.SourceBundleData); err != nil {
		return domain.SourceBundle{}, domain.ErrUnavailable
	}
	return s, nil
}
