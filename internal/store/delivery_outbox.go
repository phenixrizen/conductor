package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

type DeliveryOperation struct {
	Number                                                     int64
	ID, Operation, LeaseToken, Target, Namespace, RunID, State string
	DeduplicationKey                                           string
	UncertaintyExpired, Mismatch                               bool
	Work                                                       DeliveryWork
}

func (p *Postgres) LoadDeliveryOperation(ctx context.Context, id, binding string) (DeliveryOperation, error) {
	var item DeliveryOperation
	var deliveryID string
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(binding, 32) {
		return item, domain.ErrInvalidInput
	}
	err := p.queries().QueryRow(ctx, `SELECT o.id,o.binding,o.operation,o.delivery_id,o.deduplication_key FROM delivery_outbox o JOIN repository_deliveries d ON d.id=o.delivery_id WHERE o.binding=$1 AND d.binding=$2`, id, binding).Scan(&item.Number, &item.ID, &item.Operation, &deliveryID, &item.DeduplicationKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, domain.ErrNotFound
	}
	if err != nil {
		return item, err
	}
	item.Work, err = readDelivery(ctx, p.queries(), deliveryID)
	return item, err
}
func (p *Postgres) ClaimDeliveryDispatch(ctx context.Context, target, namespace string) (*DeliveryOperation, error) {
	if !domain.IsLowerHex(target, 64) || !contextNamespace.MatchString(namespace) {
		return nil, domain.ErrInvalidInput
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var item DeliveryOperation
	var deliveryID string
	err = tx.QueryRow(ctx, `SELECT id,binding,operation,delivery_id,COALESCE(first_attempt_at<clock_timestamp()-interval '1 hour',false) FROM delivery_outbox WHERE delivered_at IS NULL AND unresolved_at IS NULL AND next_attempt_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&item.Number, &item.ID, &item.Operation, &deliveryID, &item.UncertaintyExpired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO delivery_dispatch_bindings(outbox_id,target) VALUES($1,$2) ON CONFLICT DO NOTHING`, item.Number, map[string]string{"target": target, "namespace": namespace}); err != nil {
		return nil, err
	}
	var bound map[string]string
	if err = tx.QueryRow(ctx, `SELECT target FROM delivery_dispatch_bindings WHERE outbox_id=$1`, item.Number).Scan(&bound); err != nil {
		return nil, err
	}
	item.Target, item.Namespace = bound["target"], bound["namespace"]
	item.Mismatch = item.Target != target || item.Namespace != namespace
	err = tx.QueryRow(ctx, `SELECT temporal_run_id,state FROM delivery_execution_observations WHERE outbox_id=$1`, item.Number).Scan(&item.RunID, &item.State)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	item.LeaseToken, err = opaqueID()
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE delivery_outbox SET lease_token=$2,lease_until=clock_timestamp()+interval '45 seconds',first_attempt_at=COALESCE(first_attempt_at,clock_timestamp()),attempts=attempts+1 WHERE id=$1`, item.Number, item.LeaseToken); err != nil {
		return nil, err
	}
	item.Work, err = readDelivery(ctx, tx, deliveryID)
	if err != nil {
		return nil, err
	}
	return &item, tx.Commit(ctx)
}

// FinishDeliveryDispatch atomically fences transport acknowledgment and its
// execution observation. Running operations remain eligible only for lookup;
// a known missing run or permanently uncertain binding is never replaced.
func (p *Postgres) FinishDeliveryDispatch(ctx context.Context, item DeliveryOperation, state, run, code string) error {
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
	err = tx.QueryRow(ctx, `SELECT lease_token=$2 AND lease_until>=clock_timestamp() FROM delivery_outbox WHERE id=$1 FOR UPDATE`, item.Number, item.LeaseToken).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return domain.ErrConflict
	}
	var previousRun, previousState string
	err = tx.QueryRow(ctx, `SELECT temporal_run_id,state FROM delivery_execution_observations WHERE outbox_id=$1 FOR UPDATE`, item.Number).Scan(&previousRun, &previousState)
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
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM delivery_operation_receipts WHERE outbox_id=$1)`, item.Number).Scan(&receipt); err != nil {
			return err
		}
		if !receipt {
			return domain.ErrConflict
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO delivery_execution_observations(outbox_id,namespace,workflow_id,temporal_run_id,state,observed_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()) ON CONFLICT(outbox_id) DO UPDATE SET temporal_run_id=EXCLUDED.temporal_run_id,state=EXCLUDED.state,observed_at=EXCLUDED.observed_at`, item.Number, item.Namespace, "conductor.publish.v1/"+item.ID, run, state); err != nil {
		return err
	}
	terminal := state != "running" && state != "unavailable"
	if _, err = tx.Exec(ctx, `UPDATE delivery_outbox SET delivered_at=CASE WHEN $2 AND $3<>'unresolved' THEN clock_timestamp() ELSE delivered_at END,unresolved_at=CASE WHEN $3='unresolved' THEN clock_timestamp() ELSE unresolved_at END,next_attempt_at=clock_timestamp()+interval '5 seconds',failure_code=$4,lease_token=NULL,lease_until=NULL WHERE id=$1`, item.Number, terminal, state, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) FinishDeliveryReceiptDispatch(ctx context.Context, item DeliveryOperation) error {
	tag, err := p.pool.Exec(ctx, `UPDATE delivery_outbox o SET delivered_at=clock_timestamp(),failure_code='receipt_recorded',lease_token=NULL,lease_until=NULL WHERE o.id=$1 AND lease_token=$2 AND lease_until>=clock_timestamp() AND EXISTS(SELECT 1 FROM delivery_operation_receipts r WHERE r.outbox_id=o.id)`, item.Number, item.LeaseToken)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
