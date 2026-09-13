package store

import (
	"context"
	"fmt"
	"github.com/phenixrizen/conductor/internal/domain"
)

// Readiness is explicit and read-only; starting an executor never migrates data.
func (p *Postgres) CheckCoordinationSchema(ctx context.Context) error {
	rows, err := p.pool.Query(ctx, `SELECT a.task_id,a.input_digest,a.profile_digest,a.image,a.deadline,c.input_digest,r.digest,b.target,o.first_attempt_at,o.unresolved_at,e.temporal_run_id,p.configuration,s.bundle_digest FROM coordination_task_attempts a,coordination_task_cleanup c,coordination_run_receipts r,coordination_dispatch_bindings b,coordination_outbox o,coordination_execution_observations e,execution_profiles p,context_source_bundles s LIMIT 0`)
	if err == nil {
		rows.Close()
		err = rows.Err()
	}
	if err != nil {
		return fmt.Errorf("%w: coordinated execution schema unavailable; apply documented migrations", domain.ErrUnavailable)
	}
	return nil
}
