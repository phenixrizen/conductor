package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

type TrackerDispatch struct {
	ID, Binding, LeaseToken, Target, Namespace, RunID string
	PreviouslyAttempted                               bool
	HasReceipt                                        bool
}

func (p *Postgres) ClaimTrackerDispatch(ctx context.Context, target, namespace string) (*TrackerDispatch, error) {
	if !domain.IsLowerHex(target, 64) || !contextNamespace.MatchString(namespace) {
		return nil, domain.ErrInvalidInput
	}
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var d TrackerDispatch
	e = tx.QueryRow(ctx, `SELECT o.sync_id,s.binding,COALESCE(o.target,''),COALESCE(o.namespace,''),COALESCE(o.run_id,''),o.first_attempt_at IS NOT NULL,EXISTS(SELECT 1 FROM tracker_observations r WHERE r.sync_id=o.sync_id) FROM tracker_sync_outbox o JOIN tracker_syncs s ON s.id=o.sync_id WHERE (o.delivered_at IS NULL OR NOT EXISTS(SELECT 1 FROM tracker_observations r WHERE r.sync_id=o.sync_id)) AND o.unresolved_at IS NULL AND o.available_at<=clock_timestamp() AND (o.lease_until IS NULL OR o.lease_until<clock_timestamp()) ORDER BY o.created_at,o.sync_id LIMIT 1 FOR UPDATE OF o SKIP LOCKED`).Scan(&d.ID, &d.Binding, &d.Target, &d.Namespace, &d.RunID, &d.PreviouslyAttempted, &d.HasReceipt)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	d.LeaseToken, e = trackerNonce()
	if e != nil {
		return nil, e
	}
	if d.Target == "" {
		d.Target = target
		d.Namespace = namespace
	}
	if _, e = tx.Exec(ctx, `UPDATE tracker_sync_outbox SET target=$2,namespace=$3,lease_token=$4,lease_until=clock_timestamp()+interval '45 seconds',first_attempt_at=COALESCE(first_attempt_at,clock_timestamp()) WHERE sync_id=$1`, d.ID, d.Target, d.Namespace, d.LeaseToken); e != nil {
		return nil, e
	}
	return &d, tx.Commit(ctx)
}
func (p *Postgres) FinishTrackerDispatch(ctx context.Context, d TrackerDispatch, outcome, runID string) error {
	if outcome != "delivered" && outcome != "retry" && outcome != "unresolved" || !domain.IsLowerHex(d.LeaseToken, 32) || len(runID) > 128 {
		return domain.ErrInvalidInput
	}
	tag, e := p.pool.Exec(ctx, `UPDATE tracker_sync_outbox SET delivered_at=CASE WHEN $3='delivered' THEN clock_timestamp() ELSE delivered_at END,unresolved_at=CASE WHEN $3='unresolved' THEN clock_timestamp() ELSE unresolved_at END,run_id=COALESCE(NULLIF($4,''),run_id),available_at=clock_timestamp()+interval '5 seconds',lease_token=NULL,lease_until=NULL WHERE sync_id=$1 AND lease_token=$2 AND lease_until>=clock_timestamp() AND (run_id IS NULL OR run_id=$4 OR $4='')`, d.ID, d.LeaseToken, outcome, runID)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
