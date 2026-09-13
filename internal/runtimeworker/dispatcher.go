package runtimeworker

import (
	"context"
	"errors"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
)

type DispatchStore interface {
	ClaimRuntimeDispatch(context.Context, string, string) (*store.RuntimeOperation, error)
	FinishRuntimeDispatch(context.Context, store.RuntimeOperation, string, string, string) error
	RuntimeOperationReceipt(context.Context, string, string) (string, error)
}
type Runtime interface {
	Start(context.Context, string, contextworkflow.Reference) (contextworkflow.Observation, error)
	Lookup(context.Context, string, string, contextworkflow.Reference) (contextworkflow.Observation, error)
}
type Dispatcher struct {
	db                DispatchStore
	runtime           Runtime
	namespace, target string
	resolveTarget     func(context.Context) (string, error)
}

func NewDispatcher(db DispatchStore, runtime Runtime, namespace, target string, resolve func(context.Context) (string, error)) (*Dispatcher, error) {
	if db == nil || runtime == nil || namespace == "" || !domain.IsLowerHex(target, 64) || resolve == nil {
		return nil, contextworkflow.ErrInvalidConfiguration
	}
	return &Dispatcher{db, runtime, namespace, target, resolve}, nil
}
func (d *Dispatcher) Step(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	target, err := d.resolveTarget(ctx)
	if err != nil {
		return false, err
	}
	if target != d.target {
		return false, contextworkflow.ErrBindingMismatch
	}
	item, err := d.db.ClaimRuntimeDispatch(ctx, d.target, d.namespace)
	if err != nil || item == nil {
		return false, err
	}
	finish := func(state, run, code string) (bool, error) {
		return true, d.db.FinishRuntimeDispatch(ctx, *item, state, run, code)
	}
	if item.Mismatch || item.State == "unresolved" {
		return finish("unresolved", item.RunID, "binding_mismatch")
	}
	ref := contextworkflow.Reference{ID: item.ID, Binding: item.Work.Binding}
	id := WorkflowName + "/" + ref.ID
	// A receipt prevents replacement execution, but it is not itself a Temporal
	// completion observation. Keep checking the bound run until its actual state
	// is known, including after PostgreSQL committed before the workflow response.
	receipt, err := d.db.RuntimeOperationReceipt(ctx, ref.ID, ref.Binding)
	if err != nil {
		return true, err
	}

	observation, err := d.runtime.Lookup(ctx, id, item.RunID, ref)
	if errors.Is(err, contextworkflow.ErrNotFound) {
		if receipt != "" || item.RunID != "" || item.UncertaintyExpired {
			return finish("unresolved", item.RunID, "history_unavailable")
		}
		observation, err = d.runtime.Start(ctx, id, ref)
	}
	if err != nil {
		if errors.Is(err, contextworkflow.ErrBindingMismatch) || errors.Is(err, contextworkflow.ErrInvalidConfiguration) || errors.Is(err, contextworkflow.ErrNotFound) {
			return finish("unresolved", item.RunID, "binding_mismatch")
		}
		return finish("unavailable", item.RunID, "execution_unavailable")
	}
	if observation.State == "completed" {
		digest, err := d.db.RuntimeOperationReceipt(ctx, ref.ID, ref.Binding)
		if err != nil {
			return true, err
		}
		if digest == "" || observation.Result == nil || observation.Result.ReceiptID != ref.ID || observation.Result.Digest != digest {
			return finish("unresolved", observation.RunID, "receipt_mismatch")
		}
	}
	return finish(observation.State, observation.RunID, "")
}
func (d *Dispatcher) Run(ctx context.Context, report func(string)) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	last := ""
	for {
		_, err := d.Step(ctx)
		status := "ready"
		if err != nil {
			status = "runtime evidence execution unavailable; reconciliation pending"
		}
		if last != status && report != nil {
			report(status)
		}
		last = status
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
