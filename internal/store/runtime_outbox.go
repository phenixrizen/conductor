package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

type RuntimeOperation struct {
	Number                                          int64
	ID, LeaseToken, Target, Namespace, RunID, State string
	UncertaintyExpired, Mismatch                    bool
	Work                                            RuntimeWork
}

func (p *Postgres) ClaimRuntimeDispatch(ctx context.Context, target, namespace string) (*RuntimeOperation, error) {
	if !domain.IsLowerHex(target, 64) || !contextNamespace.MatchString(namespace) {
		return nil, domain.ErrInvalidInput
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var item RuntimeOperation
	var runtimeID string
	err = tx.QueryRow(ctx, `SELECT id,evidence_id,COALESCE(first_attempt_at<clock_timestamp()-interval '1 hour',false) FROM runtime_outbox WHERE delivered_at IS NULL AND unresolved_at IS NULL AND next_attempt_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&item.Number, &runtimeID, &item.UncertaintyExpired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO runtime_dispatch_bindings(outbox_id,target) VALUES($1,$2) ON CONFLICT DO NOTHING`, item.Number, map[string]string{"target": target, "namespace": namespace}); err != nil {
		return nil, err
	}
	var bound map[string]string
	if err = tx.QueryRow(ctx, `SELECT target FROM runtime_dispatch_bindings WHERE outbox_id=$1`, item.Number).Scan(&bound); err != nil {
		return nil, err
	}
	item.Target, item.Namespace = bound["target"], bound["namespace"]
	item.Mismatch = item.Target != target || item.Namespace != namespace
	err = tx.QueryRow(ctx, `SELECT temporal_run_id,state FROM runtime_execution_observations WHERE outbox_id=$1`, item.Number).Scan(&item.RunID, &item.State)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	item.LeaseToken, err = opaqueID()
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE runtime_outbox SET lease_token=$2,lease_until=clock_timestamp()+interval '45 seconds',first_attempt_at=COALESCE(first_attempt_at,clock_timestamp()),attempts=attempts+1 WHERE id=$1`, item.Number, item.LeaseToken); err != nil {
		return nil, err
	}
	item.ID = runtimeID
	item.Work, err = readRuntime(ctx, tx, runtimeID)
	if err != nil {
		return nil, err
	}
	return &item, tx.Commit(ctx)
}

// FinishRuntimeDispatch atomically fences transport acknowledgment and its
// execution observation. Running operations remain eligible only for lookup;
// a known missing run or permanently uncertain binding is never replaced.
func (p *Postgres) FinishRuntimeDispatch(ctx context.Context, item RuntimeOperation, state, run, code string) error {
	if !domain.IsLowerHex(item.LeaseToken, 32) || len(run) > 128 || len(code) > 64 {
		return domain.ErrInvalidInput
	}
	switch state {
	case "running", "completed", "cancelled", "failed", "timed_out", "terminated", "unavailable", "unresolved":
	default:
		return domain.ErrInvalidInput
	}
	if run == "" && state != "unavailable" && state != "unresolved" {
		return domain.ErrInvalidInput
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var valid bool
	err = tx.QueryRow(ctx, `SELECT lease_token=$2 AND lease_until>=clock_timestamp() FROM runtime_outbox WHERE id=$1 FOR UPDATE`, item.Number, item.LeaseToken).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return domain.ErrConflict
	}
	var previousRun, previousState string
	err = tx.QueryRow(ctx, `SELECT temporal_run_id,state FROM runtime_execution_observations WHERE outbox_id=$1 FOR UPDATE`, item.Number).Scan(&previousRun, &previousState)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if previousRun != "" && run != "" && previousRun != run {
		return domain.ErrConflict
	}
	if run == "" {
		run = previousRun
	}
	if previousState != "" && previousState != "running" && previousState != "unavailable" {
		state = previousState
	}
	if state == "completed" {
		var receipt bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runtime_receipts r JOIN runtime_outbox o ON o.evidence_id=r.evidence_id WHERE o.id=$1)`, item.Number).Scan(&receipt); err != nil {
			return err
		}
		if !receipt {
			return domain.ErrConflict
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO runtime_execution_observations(outbox_id,namespace,workflow_id,temporal_run_id,state,observed_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()) ON CONFLICT(outbox_id) DO UPDATE SET temporal_run_id=EXCLUDED.temporal_run_id,state=EXCLUDED.state,observed_at=EXCLUDED.observed_at`, item.Number, item.Namespace, "conductor.runtime.v1/"+item.ID, run, state); err != nil {
		return err
	}
	terminal := state != "running" && state != "unavailable"
	if _, err = tx.Exec(ctx, `UPDATE runtime_outbox SET delivered_at=CASE WHEN $2 AND $3<>'unresolved' THEN clock_timestamp() ELSE delivered_at END,unresolved_at=CASE WHEN $3='unresolved' THEN clock_timestamp() ELSE unresolved_at END,next_attempt_at=clock_timestamp()+interval '5 seconds',failure_code=$4,lease_token=NULL,lease_until=NULL WHERE id=$1`, item.Number, terminal, state, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
