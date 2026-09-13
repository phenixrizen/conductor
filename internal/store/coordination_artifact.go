package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

// InspectCoordinationArtifact uses the caller's existing authorization
// transaction. Coordination locks every included repository read grant; those
// locks remain held until the service commits the complete artifact read.
// Current execution/publication permission or current approval is unnecessary:
// historic failed checks and design-only reports must remain inspectable.
func (p *Postgres) InspectCoordinationArtifact(ctx context.Context, id string, q domain.CoordinationArtifactQuery) (domain.CoordinationArtifact, error) {
	var out domain.CoordinationArtifact
	if !domain.IsLowerHex(id, 32) || domain.ValidateCoordinationArtifactQuery(q) != nil {
		return out, domain.ErrInvalidInput
	}
	run, err := p.Coordination(ctx, id)
	if err != nil {
		return out, err
	}
	if run.Digest != q.RunDigest {
		return out, domain.ErrConflict
	}
	found := false
	taskKey := ""
	for _, receipt := range run.Receipts {
		if receipt.TaskID == q.TaskID {
			if receipt.ArtifactDigest == "" {
				return out, domain.ErrNotFound
			}
			if receipt.ArtifactDigest != q.ArtifactDigest {
				return out, domain.ErrConflict
			}
			found = true
			taskKey = receipt.TaskKey
			break
		}
	}
	if !found {
		return out, domain.ErrNotFound
	}
	var size int
	// Refuse oversized JSON before bringing it into the application. JSONB's
	// canonical whitespace can differ from the original producer document.
	err = p.tx.QueryRow(ctx, `SELECT octet_length(artifact::text),CASE WHEN octet_length(artifact::text)<=16777216 THEN artifact ELSE NULL END FROM coordination_task_receipts WHERE run_id=$1 AND task_id=$2 AND artifact_digest=$3`, id, q.TaskID, q.ArtifactDigest).Scan(&size, &out.Artifact)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CoordinationArtifact{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CoordinationArtifact{}, err
	}
	if size > 16<<20 || len(out.Artifact) == 0 {
		return domain.CoordinationArtifact{}, domain.ErrUnavailable
	}
	var result execution.Result
	decoder := json.NewDecoder(bytes.NewReader(out.Artifact))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return domain.CoordinationArtifact{}, domain.ErrUnavailable
	}
	digest, err := domain.JSONDigest(result)
	if err != nil || digest != q.ArtifactDigest {
		return domain.CoordinationArtifact{}, domain.ErrUnavailable
	}
	out.RunID, out.RunDigest, out.TaskID, out.ArtifactDigest = id, run.Digest, q.TaskID, digest
	out.Verification, err = p.verificationReview(ctx, run, taskKey, result)
	if err != nil {
		return domain.CoordinationArtifact{}, err
	}
	return out, nil
}
