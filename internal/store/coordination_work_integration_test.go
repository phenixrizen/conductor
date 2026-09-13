package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

func authorizedCoordination(t *testing.T) (context.Context, *Postgres, CoordinationWork) {
	t.Helper()
	ctx, p, s, plan := coordinationFixture(t)
	run, err := s.CreateCoordination(accessContext(ctx, "worker", "workspace-one", "repo-one"), "runtime-plan", plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeCoordination(accessContext(ctx, "reviewer", "workspace-one", "repo-one"), run.ID, run.Digest); err != nil {
		t.Fatal(err)
	}
	var binding string
	if err = p.pool.QueryRow(ctx, `SELECT binding FROM coordination_runs WHERE id=$1`, run.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	w, err := p.CoordinationWork(ctx, run.ID, binding)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, p, w
}
func TestCoordinationAttemptAdmissionIsDurableAndSingleProducer(t *testing.T) {
	ctx, p, w := authorizedCoordination(t)
	id := w.TaskIDs["service"]
	input, err := p.CoordinationInput(ctx, w.Run.ID, w.Binding, id)
	if err != nil {
		t.Fatal(err)
	}
	if input.PlanDigest != w.Run.Digest || len(input.Repositories) != 1 || input.Repositories[0].ID != "repo-one" || input.Repositories[0].Commit != w.Run.Plan.Repositories[0].Commit {
		t.Fatal("trusted input lost inspected bindings")
	}
	var created atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attempt, new, err := p.BeginCoordinationAttempt(ctx, w.Run.ID, w.Binding, id, execution.InputDigest(input))
			if err != nil {
				failures <- err
				return
			}
			if attempt.InputDigest != execution.InputDigest(input) {
				failures <- domain.ErrConflict
			}
			if new {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if created.Load() != 1 {
		t.Fatalf("producer admitted %d times", created.Load())
	}
	if _, err = p.pool.Exec(ctx, `UPDATE coordination_task_attempts SET input_digest=$1`, strings.Repeat("f", 64)); err == nil {
		t.Fatal("attempt was mutable")
	}
	if _, err = p.CoordinationInput(ctx, w.Run.ID, strings.Repeat("f", 32), id); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("forged worker binding: %v", err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE execution_grants SET can_execute=false WHERE repository_id='repo-two' AND principal_id='human-reviewer'`); err != nil {
		t.Fatal(err)
	}
	if err = p.CheckCoordinationWork(ctx, w.Run.ID, w.Binding); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("related repo revocation ignored: %v", err)
	}
}
func TestCoordinationReceiptAndAttemptAuditRollback(t *testing.T) {
	ctx, p, w := authorizedCoordination(t)
	id := w.TaskIDs["service"]
	input, err := p.CoordinationInput(ctx, w.Run.ID, w.Binding, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION reject_coordination_attempt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='task.attempt_admitted' THEN RAISE EXCEPTION 'synthetic rejection'; END IF;RETURN NEW;END $$; CREATE TRIGGER reject_coordination_attempt BEFORE INSERT ON coordination_audit_events FOR EACH ROW EXECUTE FUNCTION reject_coordination_attempt()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.BeginCoordinationAttempt(ctx, w.Run.ID, w.Binding, id, execution.InputDigest(input)); err == nil {
		t.Fatal("ignored admission audit failure")
	}
	a, err := p.CoordinationAttempt(ctx, w.Run.ID, w.Binding, id)
	if err != nil || a != nil {
		t.Fatalf("attempt survived rollback: %+v %v", a, err)
	}
	if _, err = p.pool.Exec(ctx, `DROP TRIGGER reject_coordination_attempt ON coordination_audit_events`); err != nil {
		t.Fatal(err)
	}
	a, created, err := p.BeginCoordinationAttempt(ctx, w.Run.ID, w.Binding, id, execution.InputDigest(input))
	if err != nil || !created {
		t.Fatal(err)
	}
	// This fixture proves atomic stopped-fact persistence only; it supplies no
	// patch and never claims a successful producer or verification run.
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION reject_coordination_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='task.receipt_recorded' THEN RAISE EXCEPTION 'synthetic rejection'; END IF;RETURN NEW;END $$; CREATE TRIGGER reject_coordination_receipt BEFORE INSERT ON coordination_audit_events FOR EACH ROW EXECUTE FUNCTION reject_coordination_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, err = p.StopCoordinationTask(ctx, w.Run.ID, w.Binding, id, "cancelled", a.InputDigest, true); err == nil {
		t.Fatal("ignored receipt audit failure")
	}
	var count int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM coordination_task_cleanup`).Scan(&count); err != nil || count != 0 {
		t.Fatal("cleanup survived receipt rollback")
	}
	if _, err = p.pool.Exec(ctx, `DROP TRIGGER reject_coordination_receipt ON coordination_audit_events`); err != nil {
		t.Fatal(err)
	}
	receipt, err := p.StopCoordinationTask(ctx, w.Run.ID, w.Binding, id, "cancelled", a.InputDigest, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE access_principals SET active=false WHERE id='human-reviewer'`); err != nil {
		t.Fatal(err)
	}
	recovered, err := p.CompleteCoordinationTask(ctx, w.Run.ID, w.Binding, id, "invalid-new-output", execution.Result{})
	if err != nil || recovered.Digest != receipt.Digest {
		t.Fatalf("lost acknowledgement after revocation: %+v %v", recovered, err)
	}
}
func TestCoordinationDispatchFencesTargetAndReleasesOnlyKnownCleanTerminalWork(t *testing.T) {
	ctx, p, w := authorizedCoordination(t)
	target := strings.Repeat("a", 64)
	item, err := p.ClaimCoordinationDispatch(ctx, target, "synthetic")
	if err != nil || item == nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE coordination_outbox SET lease_until=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	other, err := p.ClaimCoordinationDispatch(ctx, strings.Repeat("b", 64), "other")
	if err != nil || other == nil || !other.Mismatch || other.BoundTarget != target {
		t.Fatalf("runtime identity changed: %+v %v", other, err)
	}
	if err = p.FinishCoordinationDispatch(ctx, *item, "delivered", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale lease accepted: %v", err)
	}
	if err = p.ObserveCoordinationExecution(ctx, w.Run.ID, w.Binding, target, "synthetic", "conductor.coordinate.v1/"+w.Run.ID, "run", "completed"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("completion without receipts: %v", err)
	}
	if err = p.ObserveCoordinationExecution(ctx, w.Run.ID, w.Binding, target, "synthetic", "conductor.coordinate.v1/"+w.Run.ID, "run", "cancelled"); err != nil {
		t.Fatal(err)
	}
	var claims int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM coordination_claims WHERE released_at IS NULL`).Scan(&claims); err != nil || claims != 2 {
		t.Fatal("claims released before terminal task facts")
	}
	for _, id := range w.TaskIDs {
		if _, err = p.StopCoordinationTask(ctx, w.Run.ID, w.Binding, id, "cancelled", "", true); err != nil {
			t.Fatal(err)
		}
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM coordination_claims WHERE released_at IS NULL`).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("confirmed terminal no-producer claims retained: %d %v", claims, err)
	}
	if err = p.ObserveCoordinationExecution(ctx, w.Run.ID, w.Binding, target, "synthetic", "conductor.coordinate.v1/"+w.Run.ID, "run", "running"); err != nil {
		t.Fatal(err)
	}
	latest, err := p.CoordinationWork(ctx, w.Run.ID, w.Binding)
	if err != nil || latest.Run.Execution.State != "cancelled" {
		t.Fatal("stale observation regressed terminal state")
	}
}
func TestCoordinationProfileRotationAndWholeSourceRemainInspected(t *testing.T) {
	ctx, p, w := authorizedCoordination(t)
	if _, err := p.pool.Exec(ctx, `UPDATE execution_profiles SET image=$1 WHERE workspace_id='workspace-one'`, "sha256:"+strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckCoordinationWork(ctx, w.Run.ID, w.Binding); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("changed profile image dispatched: %v", err)
	}
}
