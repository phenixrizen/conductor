package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

type CoordinationDispatch struct {
	ID, Binding, Operation, LeaseToken      string
	BoundTarget, BoundNamespace             string
	Mismatch                                bool
	PreviouslyAttempted, UncertaintyExpired bool
	Work                                    CoordinationWork
}

// ClaimCoordinationDispatch commits its short lease before any Temporal call. A
// reclaimed lease gets a fresh token, fencing every later acknowledgment.
func (p *Postgres) ClaimCoordinationDispatch(ctx context.Context, target, namespace string) (*CoordinationDispatch, error) {
	if !domain.IsLowerHex(target, 64) || !contextNamespace.MatchString(namespace) {
		return nil, domain.ErrInvalidInput
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var item CoordinationDispatch
	err = tx.QueryRow(ctx, `SELECT run_id,operation,first_attempt_at IS NOT NULL,COALESCE(first_attempt_at < clock_timestamp()-interval '1 hour',false) FROM coordination_outbox WHERE delivered_at IS NULL AND unresolved_at IS NULL AND next_attempt_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY next_attempt_at,run_id,operation DESC LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&item.ID, &item.Operation, &item.PreviouslyAttempted, &item.UncertaintyExpired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO coordination_dispatch_bindings(run_id,target) VALUES($1,jsonb_build_object('target',$2::text,'namespace',$3::text)) ON CONFLICT(run_id) DO NOTHING`, item.ID, target, namespace); err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `SELECT target->>'target',target->>'namespace' FROM coordination_dispatch_bindings WHERE run_id=$1`, item.ID).Scan(&item.BoundTarget, &item.BoundNamespace); err != nil {
		return nil, err
	}
	// Even a mismatch receives a fenced lease so the caller can mark the handoff
	// unresolved. The caller must perform no Temporal RPC for a mismatched item.
	item.Mismatch = item.BoundTarget != target || item.BoundNamespace != namespace
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	item.LeaseToken = hex.EncodeToString(nonce[:])
	if _, err = tx.Exec(ctx, `UPDATE coordination_outbox SET lease_token=$3,lease_until=clock_timestamp()+interval '45 seconds',first_attempt_at=COALESCE(first_attempt_at,clock_timestamp()) WHERE run_id=$1 AND operation=$2`, item.ID, item.Operation, item.LeaseToken); err != nil {
		return nil, err
	}
	item.Work, err = readCoordinationWork(ctx, tx, item.ID)
	if err != nil {
		return nil, err
	}
	item.Binding = item.Work.Binding
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &item, nil
}

// FinishCoordinationDispatch records transport delivery only. A timeout remains a
// retryable uncertain handoff; it never assigns a coordination execution outcome.
func (p *Postgres) FinishCoordinationDispatch(ctx context.Context, item CoordinationDispatch, outcome, code string) error {
	if !domain.IsLowerHex(item.LeaseToken, 32) || len(code) > 64 {
		return domain.ErrInvalidInput
	}
	if outcome != "delivered" && outcome != "retry" && outcome != "unresolved" {
		return domain.ErrInvalidInput
	}
	tag, err := p.pool.Exec(ctx, `UPDATE coordination_outbox SET delivered_at=CASE WHEN $4='delivered' THEN clock_timestamp() ELSE delivered_at END,unresolved_at=CASE WHEN $4='unresolved' THEN clock_timestamp() ELSE unresolved_at END,next_attempt_at=clock_timestamp()+interval '5 seconds',error_code=$5,lease_token=NULL,lease_until=NULL WHERE run_id=$1 AND operation=$2 AND lease_token=$3 AND lease_until>=clock_timestamp()`, item.ID, item.Operation, item.LeaseToken, outcome, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

type CoordinationObservationTarget struct{ ID, Binding, RunID, BoundTarget, BoundNamespace string }

func (p *Postgres) CoordinationObservationTargets(ctx context.Context) ([]CoordinationObservationTarget, error) {
	rows, err := p.pool.Query(ctx, `SELECT c.id,c.binding,e.temporal_run_id,b.target->>'target',b.target->>'namespace' FROM coordination_runs c JOIN coordination_execution_observations e ON e.run_id=c.id JOIN coordination_dispatch_bindings b ON b.run_id=c.id WHERE e.state IN ('running','unavailable') AND e.temporal_run_id<>'' AND e.observed_at<clock_timestamp()-interval '5 seconds' ORDER BY e.observed_at LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]CoordinationObservationTarget, 0, 20)
	for rows.Next() {
		var item CoordinationObservationTarget
		if err = rows.Scan(&item.ID, &item.Binding, &item.RunID, &item.BoundTarget, &item.BoundNamespace); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) ObserveCoordinationExecution(ctx context.Context, id, binding, target, namespace, workflowID, runID, state string) error {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(binding, 32) || !domain.IsLowerHex(target, 64) || !contextNamespace.MatchString(namespace) || workflowID != "conductor.coordinate.v1/"+id || len(runID) > 128 {
		return domain.ErrInvalidInput
	}
	switch state {
	case "running", "completed", "cancelled", "failed", "timed_out", "terminated", "unavailable", "unresolved":
	default:
		return domain.ErrInvalidInput
	}
	if runID == "" && state != "unresolved" && state != "unavailable" {
		return domain.ErrInvalidInput
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "coordination:"+id); err != nil {
		return err
	}
	work, err := readCoordinationWork(ctx, tx, id)
	if err != nil {
		return err
	}
	if work.Binding != binding {
		return domain.ErrForbidden
	}
	var boundTarget, boundNamespace string
	err = tx.QueryRow(ctx, `SELECT target->>'target',target->>'namespace' FROM coordination_dispatch_bindings WHERE run_id=$1`, id).Scan(&boundTarget, &boundNamespace)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return err
	}
	if target != boundTarget || namespace != boundNamespace {
		return domain.ErrConflict
	}
	if previous := work.Run.Execution; previous != nil {
		if previous.RunID != "" && runID != "" && previous.RunID != runID {
			return domain.ErrConflict
		}
		// A stale dispatcher must not regress an observed terminal execution to a
		// running/unavailable state. A receipt remains an independent durable fact.
		if previous.State != "running" && previous.State != "unavailable" {
			if err = releaseCoordinationClaims(ctx, tx, id); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		if runID == "" {
			runID = previous.RunID
		}
	}
	if state == "completed" && work.ReceiptDigest == "" {
		return domain.ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO coordination_execution_observations(run_id,namespace,workflow_id,temporal_run_id,state) VALUES($1,$2,$3,$4,$5) ON CONFLICT(run_id) DO UPDATE SET temporal_run_id=excluded.temporal_run_id,state=excluded.state,observed_at=clock_timestamp()`, id, namespace, workflowID, runID, state)
	if err != nil {
		return err
	}
	if state != "running" && state != "unavailable" && state != "unresolved" {
		if err = releaseCoordinationClaims(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func releaseCoordinationClaims(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE coordination_claims SET released_at=clock_timestamp() WHERE run_id=$1 AND released_at IS NULL AND EXISTS(SELECT 1 FROM coordination_execution_observations e WHERE e.run_id=$1 AND e.state IN ('completed','cancelled','failed','timed_out','terminated')) AND NOT EXISTS(SELECT 1 FROM coordination_tasks t LEFT JOIN coordination_task_receipts r ON r.task_id=t.id WHERE t.run_id=$1 AND (r.task_id IS NULL OR r.outcome='unresolved')) AND NOT EXISTS(SELECT 1 FROM coordination_task_attempts a LEFT JOIN coordination_task_cleanup c ON c.task_id=a.task_id AND c.input_digest=a.input_digest WHERE a.run_id=$1 AND c.task_id IS NULL)`, id)
	return err
}

// Temporal observations are timestamped in PostgreSQL. This value is used only
// for client freshness; execution sequencing and cancellation stay with Temporal.
const CoordinationObservationFreshness = 30 * time.Second
