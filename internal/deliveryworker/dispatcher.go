package deliveryworker

import (
	"context"
	"errors"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
)

type DispatchStore interface {
	ClaimDeliveryDispatch(context.Context, string, string) (*store.DeliveryOperation, error)
	FinishDeliveryDispatch(context.Context, store.DeliveryOperation, string, string, string) error
	DeliveryOperationReceipt(context.Context, string, string) (string, error)
	FinishDeliveryReceiptDispatch(context.Context, store.DeliveryOperation) error
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
	item, err := d.db.ClaimDeliveryDispatch(ctx, d.target, d.namespace)
	if err != nil || item == nil {
		return false, err
	}
	finish := func(state, run, code string) (bool, error) {
		return true, d.db.FinishDeliveryDispatch(ctx, *item, state, run, code)
	}
	if item.Mismatch || item.State == "unresolved" {
		return finish("unresolved", item.RunID, "binding_mismatch")
	}
	ref := contextworkflow.Reference{ID: item.ID, Binding: item.Work.Binding}
	id := WorkflowName + "/" + ref.ID
	if receipt, err := d.db.DeliveryOperationReceipt(ctx, ref.ID, ref.Binding); err != nil {
		return true, err
	} else if receipt != "" {
		return true, d.db.FinishDeliveryReceiptDispatch(ctx, *item)
	}

	observation, err := d.runtime.Lookup(ctx, id, item.RunID, ref)
	if errors.Is(err, contextworkflow.ErrNotFound) {
		if item.RunID != "" || item.UncertaintyExpired {
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
		digest, err := d.db.DeliveryOperationReceipt(ctx, ref.ID, ref.Binding)
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
			status = "publication execution unavailable; reconciliation pending"
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
