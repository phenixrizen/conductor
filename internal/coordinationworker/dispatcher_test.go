package coordinationworker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
)

type dispatchDB struct {
	item                                                         *store.CoordinationDispatch
	work                                                         store.CoordinationWork
	targets                                                      []store.CoordinationObservationTarget
	outcome, code, state, run, observedTarget, observedNamespace string
	finishErr, observeErr                                        error
	claims                                                       int
}

func (f *dispatchDB) CoordinationWork(context.Context, string, string) (store.CoordinationWork, error) {
	return f.work, nil
}
func (f *dispatchDB) ClaimCoordinationDispatch(context.Context, string, string) (*store.CoordinationDispatch, error) {
	f.claims++
	return f.item, nil
}
func (f *dispatchDB) FinishCoordinationDispatch(_ context.Context, _ store.CoordinationDispatch, outcome, code string) error {
	f.outcome, f.code = outcome, code
	return f.finishErr
}
func (f *dispatchDB) CoordinationObservationTargets(context.Context) ([]store.CoordinationObservationTarget, error) {
	return f.targets, nil
}
func (f *dispatchDB) ObserveCoordinationExecution(_ context.Context, _, _, target, namespace, _, run, state string) error {
	f.observedTarget, f.observedNamespace, f.run, f.state = target, namespace, run, state
	return f.observeErr
}

type dispatchRuntime struct {
	lookup                         contextworkflow.Observation
	lookupErr, startErr, cancelErr error
	lookups, starts, cancels       int
}

func (f *dispatchRuntime) Lookup(context.Context, string, string, contextworkflow.Reference) (contextworkflow.Observation, error) {
	f.lookups++
	return f.lookup, f.lookupErr
}
func (f *dispatchRuntime) Start(context.Context, string, contextworkflow.Reference) (contextworkflow.Observation, error) {
	f.starts++
	return contextworkflow.Observation{RunID: "retained-run", State: "running"}, f.startErr
}
func (f *dispatchRuntime) Cancel(context.Context, string, string, contextworkflow.Reference) error {
	f.cancels++
	return f.cancelErr
}

func dispatcherFixture(t *testing.T) (*Dispatcher, *dispatchDB, *dispatchRuntime) {
	t.Helper()
	target := strings.Repeat("a", 64)
	id, binding := strings.Repeat("b", 32), strings.Repeat("c", 32)
	db := &dispatchDB{item: &store.CoordinationDispatch{ID: id, Binding: binding, Operation: "start", BoundTarget: target, BoundNamespace: "synthetic", Work: store.CoordinationWork{Binding: binding, Run: domain.CoordinationRun{ID: id}}}}
	db.work = db.item.Work
	runtime := &dispatchRuntime{lookupErr: contextworkflow.ErrNotFound}
	d, err := NewDispatcher(db, runtime, "synthetic", target, func(context.Context) (string, error) { return target, nil })
	if err != nil {
		t.Fatal(err)
	}
	return d, db, runtime
}

func TestDispatcherLostStartAcknowledgmentReconcilesSameRun(t *testing.T) {
	d, db, runtime := dispatcherFixture(t)
	runtime.startErr = contextworkflow.ErrUnavailable
	if claimed, err := d.Step(context.Background()); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if runtime.starts != 1 || db.state != "unavailable" || db.outcome != "retry" || db.run != "" {
		t.Fatal("ambiguous start was guessed", db.state, db.outcome)
	}
	db.item.PreviouslyAttempted = true
	runtime.lookupErr = nil
	runtime.lookup = contextworkflow.Observation{RunID: "original-run", State: "running"}
	if _, err := d.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 1 || db.run != "original-run" || db.outcome != "delivered" {
		t.Fatal("lost acknowledgment restarted workflow")
	}
}

func TestDispatcherRefusesReplacementOrChangedRuntime(t *testing.T) {
	for _, name := range []string{"known history missing", "unknown history expired", "changed binding", "unresolved acknowledgment lost", "receipt survives retention", "process target changed"} {
		t.Run(name, func(t *testing.T) {
			d, db, runtime := dispatcherFixture(t)
			switch name {
			case "known history missing":
				db.item.Work.Run.Execution = &domain.CollectionExecution{RunID: "old-run", State: "running"}
			case "unknown history expired":
				db.item.PreviouslyAttempted, db.item.UncertaintyExpired = true, true
			case "changed binding":
				db.item.Mismatch = true
				db.item.BoundTarget = strings.Repeat("d", 64)
				db.item.BoundNamespace = "original"
			case "unresolved acknowledgment lost":
				db.item.Work.Run.Execution = &domain.CollectionExecution{State: "unresolved"}
			case "receipt survives retention":
				db.item.Work.ReceiptDigest = strings.Repeat("f", 64)
			case "process target changed":
				d.resolveTarget = func(context.Context) (string, error) { return strings.Repeat("e", 64), nil }
			}
			_, err := d.Step(context.Background())
			if name == "process target changed" {
				if !errors.Is(err, contextworkflow.ErrBindingMismatch) || db.claims != 0 {
					t.Fatal("changed runtime claimed work", err)
				}
				return
			}
			if err != nil || runtime.starts != 0 {
				t.Fatal("unsafe replacement", runtime.starts, err)
			}
			if name == "receipt survives retention" {
				if runtime.lookups != 1 || db.outcome != "unresolved" {
					t.Fatal("receipt caused redispatch")
				}
				return
			}
			if db.outcome != "unresolved" {
				t.Fatal("missing explicit uncertainty", db.outcome)
			}
			if name == "changed binding" && (runtime.lookups != 0 || db.observedNamespace != "original" || db.observedTarget != db.item.BoundTarget) {
				t.Fatal("mismatched target used")
			}
		})
	}
}

func TestDispatcherCancellationIsIntentUntilObserved(t *testing.T) {
	d, db, runtime := dispatcherFixture(t)
	db.item.Operation = "cancel"
	if _, err := d.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 0 || runtime.cancels != 0 || db.state != "unavailable" || db.outcome != "retry" {
		t.Fatal("cancellation guessed before start")
	}
	runtime.lookupErr = nil
	runtime.lookup = contextworkflow.Observation{RunID: "run", State: "running"}
	if _, err := d.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.cancels != 1 || db.state != "running" || db.outcome != "delivered" {
		t.Fatal("cancel RPC incorrectly confirmed cancellation")
	}
	runtime.lookup.State = "cancelled"
	if _, err := d.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.cancels != 1 || db.state != "cancelled" {
		t.Fatal("observed cancellation lost")
	}
}

func TestDispatcherChecksReceiptAndKeepsUnknownProgressExplicit(t *testing.T) {
	for _, name := range []string{"matching receipt", "missing receipt", "wrong digest", "wrong receipt", "unavailable", "lost history", "wrong target"} {
		t.Run(name, func(t *testing.T) {
			d, db, runtime := dispatcherFixture(t)
			digest := strings.Repeat("d", 64)
			db.work.ReceiptDigest = digest
			db.targets = []store.CoordinationObservationTarget{{ID: db.item.ID, Binding: db.item.Binding, RunID: "known-run", BoundTarget: d.target, BoundNamespace: d.namespace}}
			runtime.lookupErr = nil
			runtime.lookup = contextworkflow.Observation{RunID: "known-run", State: "completed", Result: &contextworkflow.Result{ReceiptID: db.item.ID, Digest: digest}}
			want := "unresolved"
			switch name {
			case "matching receipt":
				want = "completed"
			case "missing receipt":
				db.work.ReceiptDigest = ""
			case "wrong digest":
				runtime.lookup.Result.Digest = strings.Repeat("e", 64)
			case "wrong receipt":
				runtime.lookup.Result.ReceiptID = strings.Repeat("e", 32)
			case "unavailable":
				runtime.lookupErr = contextworkflow.ErrUnavailable
				want = "unavailable"
			case "lost history":
				runtime.lookupErr = contextworkflow.ErrNotFound
			case "wrong target":
				db.targets[0].BoundTarget = strings.Repeat("f", 64)
			}
			if err := d.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if db.state != want || runtime.starts != 0 {
				t.Fatal("unverified execution", db.state, runtime.starts)
			}
			if name == "wrong target" && runtime.lookups != 0 {
				t.Fatal("looked up changed target")
			}
		})
	}
}

func TestDispatcherObservationFailureDoesNotAcknowledgeOutbox(t *testing.T) {
	d, db, runtime := dispatcherFixture(t)
	db.observeErr = errors.New("synthetic lost database connection")
	if _, err := d.Step(context.Background()); err == nil {
		t.Fatal("ignored observation commit failure")
	}
	if db.outcome != "" || runtime.starts != 1 {
		t.Fatal("acknowledged before observation commit")
	}
}

func TestDispatcherReceiptReconcilesLostStartBeforeReleasingClaims(t *testing.T) {
	d, db, runtime := dispatcherFixture(t)
	digest := strings.Repeat("f", 64)
	db.item.Work.ReceiptDigest = digest
	db.work = db.item.Work
	runtime.lookupErr = nil
	runtime.lookup = contextworkflow.Observation{RunID: "retained-run", State: "completed", Result: &contextworkflow.Result{ReceiptID: db.item.ID, Digest: digest}}
	if _, err := d.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 0 || db.state != "completed" || db.run != "retained-run" || db.outcome != "delivered" {
		t.Fatal("receipt was not reconciled to exact terminal run")
	}
}
