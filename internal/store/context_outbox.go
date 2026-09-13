package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

type ContextDispatch struct {
	ID, Binding, Operation, LeaseToken      string
	BoundTarget, BoundNamespace             string
	Mismatch                                bool
	PreviouslyAttempted, UncertaintyExpired bool
	Work                                    CollectionWork
}

// ClaimContextDispatch commits its short lease before any Temporal call. A
// reclaimed lease gets a fresh token, fencing every later acknowledgment.
func (p *Postgres) ClaimContextDispatch(ctx context.Context, target, namespace string) (*ContextDispatch, error) {
	if !domain.IsLowerHex(target, 64) || !contextNamespace.MatchString(namespace) {
		return nil, domain.ErrInvalidInput
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var item ContextDispatch
	err = tx.QueryRow(ctx, `SELECT collection_id,operation,first_attempt_at IS NOT NULL,COALESCE(first_attempt_at < clock_timestamp()-interval '1 hour',false) FROM context_outbox WHERE delivered_at IS NULL AND unresolved_at IS NULL AND available_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY available_at,collection_id,operation DESC LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&item.ID, &item.Operation, &item.PreviouslyAttempted, &item.UncertaintyExpired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO context_runtime_bindings(collection_id,target,namespace) VALUES($1,$2,$3) ON CONFLICT(collection_id) DO NOTHING`, item.ID, target, namespace); err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `SELECT target,namespace FROM context_runtime_bindings WHERE collection_id=$1`, item.ID).Scan(&item.BoundTarget, &item.BoundNamespace); err != nil {
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
	if _, err = tx.Exec(ctx, `UPDATE context_outbox SET lease_token=$3,lease_until=clock_timestamp()+interval '45 seconds',first_attempt_at=COALESCE(first_attempt_at,clock_timestamp()) WHERE collection_id=$1 AND operation=$2`, item.ID, item.Operation, item.LeaseToken); err != nil {
		return nil, err
	}
	item.Work, err = readCollection(ctx, tx, item.ID, false)
	if err != nil {
		return nil, err
	}
	item.Binding = item.Work.Binding
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &item, nil
}

// FinishContextDispatch records transport delivery only. A timeout remains a
// retryable uncertain handoff; it never assigns a collection execution outcome.
func (p *Postgres) FinishContextDispatch(ctx context.Context, item ContextDispatch, outcome, code string) error {
	if !domain.IsLowerHex(item.LeaseToken, 32) || len(code) > 64 {
		return domain.ErrInvalidInput
	}
	if outcome != "delivered" && outcome != "retry" && outcome != "unresolved" {
		return domain.ErrInvalidInput
	}
	tag, err := p.pool.Exec(ctx, `UPDATE context_outbox SET delivered_at=CASE WHEN $4='delivered' THEN clock_timestamp() ELSE delivered_at END,unresolved_at=CASE WHEN $4='unresolved' THEN clock_timestamp() ELSE unresolved_at END,available_at=clock_timestamp()+interval '5 seconds',error_code=$5,lease_token=NULL,lease_until=NULL WHERE collection_id=$1 AND operation=$2 AND lease_token=$3 AND lease_until>=clock_timestamp()`, item.ID, item.Operation, item.LeaseToken, outcome, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

type ContextObservationTarget struct{ ID, Binding, RunID, BoundTarget, BoundNamespace string }

var contextNamespace = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func (p *Postgres) ContextObservationTargets(ctx context.Context) ([]ContextObservationTarget, error) {
	rows, err := p.pool.Query(ctx, `SELECT c.id,c.binding,e.run_id,b.target,b.namespace FROM context_collections c JOIN context_execution_observations e ON e.collection_id=c.id JOIN context_runtime_bindings b ON b.collection_id=c.id WHERE e.state IN ('running','unavailable') AND e.run_id<>'' AND e.observed_at<clock_timestamp()-interval '5 seconds' ORDER BY e.observed_at LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ContextObservationTarget, 0, 20)
	for rows.Next() {
		var item ContextObservationTarget
		if err = rows.Scan(&item.ID, &item.Binding, &item.RunID, &item.BoundTarget, &item.BoundNamespace); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) ObserveContextExecution(ctx context.Context, id, binding, target, namespace, workflowID, runID, state string) error {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(binding, 32) || !domain.IsLowerHex(target, 64) || !contextNamespace.MatchString(namespace) || workflowID != "conductor.collect.v1/"+id || len(runID) > 128 {
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
	work, err := readCollection(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if work.Binding != binding {
		return domain.ErrForbidden
	}
	var boundTarget, boundNamespace string
	err = tx.QueryRow(ctx, `SELECT target,namespace FROM context_runtime_bindings WHERE collection_id=$1`, id).Scan(&boundTarget, &boundNamespace)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return err
	}
	if target != boundTarget || namespace != boundNamespace {
		return domain.ErrConflict
	}
	if previous := work.Collection.Execution; previous != nil {
		if previous.RunID != "" && runID != "" && previous.RunID != runID {
			return domain.ErrConflict
		}
		// A stale dispatcher must not regress an observed terminal execution to a
		// running/unavailable state. A receipt remains an independent durable fact.
		if previous.State != "running" && previous.State != "unavailable" {
			return nil
		}
		if runID == "" {
			runID = previous.RunID
		}
	}
	if state == "completed" && work.Collection.Receipt == nil {
		return domain.ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO context_execution_observations(collection_id,namespace,workflow_id,run_id,state) VALUES($1,$2,$3,$4,$5) ON CONFLICT(collection_id) DO UPDATE SET run_id=excluded.run_id,state=excluded.state,observed_at=clock_timestamp()`, id, namespace, workflowID, runID, state)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Temporal observations are timestamped in PostgreSQL. This value is used only
// for client freshness; execution sequencing and cancellation stay with Temporal.
const ContextObservationFreshness = 30 * time.Second
