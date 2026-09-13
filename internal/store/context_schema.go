package store

import (
	"context"
	"fmt"

	"github.com/phenixrizen/conductor/internal/domain"
)

// CheckContextSchema verifies the required context tables and columns without
// reading source text or changing data. Only the opted-in API and trusted worker
// require this readiness check; ordinary review can keep using an older schema.
// It does not apply migrations or substitute for migration integrity checks.
func (p *Postgres) CheckContextSchema(ctx context.Context) error {
	rows, err := p.pool.Query(ctx, `SELECT
 i.repository_id,i.workspace_id,i.version,i.configuration,i.enabled,
 c.id,c.binding,c.workspace_id,c.repository_id,c.requester_id,c.idempotency_key,c.input,c.input_digest,c.integration,c.binding_digest,c.created_at,c.cancel_requested_at,
 r.collection_id,r.digest,r.snapshot,r.created_at,
 a.sequence,a.collection_id,a.event_type,a.actor,a.data,a.created_at,
 o.collection_id,o.operation,o.available_at,o.lease_token,o.lease_until,o.first_attempt_at,o.delivered_at,o.unresolved_at,o.error_code,
 b.collection_id,b.target,b.namespace,b.created_at,
 e.collection_id,e.namespace,e.workflow_id,e.run_id,e.state,e.observed_at
 FROM context_integrations i,context_collections c,context_receipts r,
 context_audit_events a,context_outbox o,context_runtime_bindings b,context_execution_observations e
 LIMIT 0`)
	if err == nil {
		rows.Close()
		err = rows.Err()
	}
	if err != nil {
		if interrupted := ctx.Err(); interrupted != nil {
			return fmt.Errorf("%w: context schema check: %w", domain.ErrUnavailable, interrupted)
		}
		// PostgreSQL diagnostics can contain role names, connection details, or
		// SQL text. Startup needs a fixed actionable explanation, not that data.
		return fmt.Errorf("%w: context schema is unavailable; apply migrations through 004_context_collections.sql", domain.ErrUnavailable)
	}
	return nil
}
