package coordinationworker

import (
	"context"
	"errors"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/coordinationworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
)

type DispatchStore interface {
	CoordinationWork(context.Context, string, string) (store.CoordinationWork, error)
	ClaimCoordinationDispatch(context.Context, string, string) (*store.CoordinationDispatch, error)
	FinishCoordinationDispatch(context.Context, store.CoordinationDispatch, string, string) error
	CoordinationObservationTargets(context.Context) ([]store.CoordinationObservationTarget, error)
	ObserveCoordinationExecution(context.Context, string, string, string, string, string, string, string) error
}

type Runtime interface {
	Start(context.Context, string, contextworkflow.Reference) (contextworkflow.Observation, error)
	Lookup(context.Context, string, string, contextworkflow.Reference) (contextworkflow.Observation, error)
	Cancel(context.Context, string, string, contextworkflow.Reference) error
}

type Dispatcher struct {
	store             DispatchStore
	runtime           Runtime
	namespace, target string
	resolveTarget     func(context.Context) (string, error)
}

// target pins this worker process to one retained Temporal cluster/namespace.
// The production runtime also verifies it before each bounded execution RPC.
func NewDispatcher(db DispatchStore, runtime Runtime, namespace, target string, resolveTarget func(context.Context) (string, error)) (*Dispatcher, error) {
	if db == nil || runtime == nil || namespace == "" || !domain.IsLowerHex(target, 64) || resolveTarget == nil {
		return nil, contextworkflow.ErrInvalidConfiguration
	}
	return &Dispatcher{store: db, runtime: runtime, namespace: namespace, target: target, resolveTarget: resolveTarget}, nil
}

func (d *Dispatcher) checkTarget(ctx context.Context) error {
	actual, err := d.resolveTarget(ctx)
	if err != nil {
		return err
	}
	if actual != d.target {
		return contextworkflow.ErrBindingMismatch
	}
	return nil
}

// Step leases and delivers at most one outbox intent. A failed acknowledgment is
// recoverable through the lease; it never authorizes replacement execution.
func (d *Dispatcher) Step(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if err := d.checkTarget(ctx); err != nil {
		return false, err
	}
	item, err := d.store.ClaimCoordinationDispatch(ctx, d.target, d.namespace)
	if err != nil || item == nil {
		return false, err
	}
	if item.Mismatch {
		return true, d.finish(ctx, *item, "unresolved", "binding_mismatch", "unresolved", runID(item.Work))
	}
	if execution := item.Work.Run.Execution; execution != nil && execution.State == "unresolved" {
		// An acknowledgment can fail after the unresolved observation commits.
		// Reclaiming that lease must not reopen a permanently uncertain handoff.
		return true, d.store.FinishCoordinationDispatch(ctx, *item, "unresolved", "execution_unresolved")
	}
	// A receipt prevents replacement, but lookup is still needed to confirm
	// Temporal termination before releasing write reservations after a lost start
	// acknowledgement. Missing history preserves uncertainty and the receipt.
	ref := contextworkflow.Reference{ID: item.ID, Binding: item.Binding}
	id := coordinationworkflow.WorkflowName + "/" + item.ID
	knownRun := runID(item.Work)
	observation, lookupErr := d.runtime.Lookup(ctx, id, knownRun, ref)
	if errors.Is(lookupErr, contextworkflow.ErrNotFound) {
		if knownRun != "" || item.UncertaintyExpired || item.Work.ReceiptDigest != "" {
			return true, d.finish(ctx, *item, "unresolved", "history_unavailable", "unresolved", knownRun)
		}
		if item.Operation == "cancel" {
			// Cancellation can beat initial dispatch. Keep its durable intent until
			// start is reconciled; absence does not confirm workflow cancellation.
			return true, d.finish(ctx, *item, "retry", "awaiting_start", "unavailable", "")
		}
		observation, lookupErr = d.runtime.Start(ctx, id, ref)
	}
	if lookupErr != nil {
		return true, d.dispatchFailure(ctx, *item, lookupErr, knownRun)
	}
	if item.Operation == "cancel" && observation.State == "running" {
		if err = d.runtime.Cancel(ctx, id, observation.RunID, ref); err != nil {
			return true, d.dispatchFailure(ctx, *item, err, observation.RunID)
		}
	}
	state, err := d.checkedState(ctx, ref, observation)
	if err != nil {
		return true, err
	}
	outcome, code := "delivered", ""
	if state == "unresolved" {
		outcome, code = "unresolved", "receipt_mismatch"
	}
	return true, d.finish(ctx, *item, outcome, code, state, observation.RunID)
}

func runID(work store.CoordinationWork) string {
	if work.Run.Execution != nil {
		return work.Run.Execution.RunID
	}
	return ""
}

func (d *Dispatcher) dispatchFailure(ctx context.Context, item store.CoordinationDispatch, err error, run string) error {
	if errors.Is(err, contextworkflow.ErrBindingMismatch) || errors.Is(err, contextworkflow.ErrInvalidConfiguration) || errors.Is(err, contextworkflow.ErrNotFound) {
		return d.finish(ctx, item, "unresolved", "execution_unresolved", "unresolved", run)
	}
	return d.finish(ctx, item, "retry", "execution_unavailable", "unavailable", run)
}

func (d *Dispatcher) finish(ctx context.Context, item store.CoordinationDispatch, outcome, code, state, run string) error {
	// Use the stored target even on a mismatch: this records uncertainty about
	// that original execution, never an observation of a substituted server.
	if err := d.store.ObserveCoordinationExecution(ctx, item.ID, item.Binding, item.BoundTarget, item.BoundNamespace, coordinationworkflow.WorkflowName+"/"+item.ID, run, state); err != nil {
		return err
	}
	return d.store.FinishCoordinationDispatch(ctx, item, outcome, code)
}

func (d *Dispatcher) checkedState(ctx context.Context, ref contextworkflow.Reference, observation contextworkflow.Observation) (string, error) {
	if observation.State != "completed" {
		return observation.State, nil
	}
	work, err := d.store.CoordinationWork(ctx, ref.ID, ref.Binding)
	if err != nil {
		return "", err
	}
	if work.ReceiptDigest == "" || observation.Result == nil || observation.Result.ReceiptID != ref.ID || observation.Result.Digest != work.ReceiptDigest {
		return "unresolved", nil
	}
	return "completed", nil
}

// Reconcile refreshes bounded observations of known runs. It neither schedules
// activity retries nor assigns execution outcomes from database intent alone.
func (d *Dispatcher) Reconcile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := d.checkTarget(ctx); err != nil {
		return err
	}
	items, err := d.store.CoordinationObservationTargets(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		ref := contextworkflow.Reference{ID: item.ID, Binding: item.Binding}
		id := coordinationworkflow.WorkflowName + "/" + item.ID
		state, run := "unresolved", item.RunID
		if item.BoundTarget == d.target && item.BoundNamespace == d.namespace {
			observation, lookupErr := d.runtime.Lookup(ctx, id, item.RunID, ref)
			switch {
			case lookupErr == nil:
				state, err = d.checkedState(ctx, ref, observation)
				if err != nil {
					return err
				}
				run = observation.RunID
			case errors.Is(lookupErr, contextworkflow.ErrNotFound), errors.Is(lookupErr, contextworkflow.ErrBindingMismatch), errors.Is(lookupErr, contextworkflow.ErrInvalidConfiguration):
				state = "unresolved"
			default:
				state = "unavailable"
			}
		}
		if err = d.store.ObserveCoordinationExecution(ctx, item.ID, item.Binding, item.BoundTarget, item.BoundNamespace, id, run, state); err != nil {
			return err
		}
	}
	return nil
}

// Run retries transport delivery at a bounded polling interval. Logs receive
// fixed status labels only; source, database errors and SDK payloads stay out.
func (d *Dispatcher) Run(ctx context.Context, report func(string)) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastStatus := ""
	for {
		_, err := d.Step(ctx)
		if err == nil {
			err = d.Reconcile(ctx)
		}
		status := "ready"
		if err != nil {
			status = "execution or database unavailable; reconciliation pending"
		}
		if errors.Is(err, contextworkflow.ErrBindingMismatch) {
			status = "runtime identity changed; worker restart required"
		}
		if status != lastStatus && report != nil {
			report(status)
		}
		lastStatus = status
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
