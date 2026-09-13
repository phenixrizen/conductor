// Package deliveryworker owns trusted publication activities. It receives only
// opaque references through Temporal; artifacts and credentials stay in process.
package deliveryworker

import (
	"context"
	"errors"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/delivery"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
)

type ActivityStore interface {
	LoadDeliveryOperation(context.Context, string, string) (store.DeliveryOperation, error)
	DeliveryOperationReceipt(context.Context, string, string) (string, error)
	CheckDeliveryWork(context.Context, string, string) (store.DeliveryWork, error)
	CompleteDeliveryOperation(context.Context, string, string, domain.DeliveryObservation) (string, error)
}
type Publisher interface {
	Publish(context.Context, domain.Delivery, delivery.Prepared, func(context.Context) error) (domain.DeliveryObservation, error)
	Observe(context.Context, domain.Delivery, func(context.Context) error) (domain.DeliveryObservation, error)
}

// Factory is a trusted constructor seam for controlled provider fixtures.
// The executable always uses the fixed-origin production adapter.
type Factory func(domain.DeliveryTarget, string) (Publisher, error)

type SourceLoader func(context.Context, string, string) ([]byte, error)
type CredentialResolver func(context.Context, domain.DeliveryTarget, string) (string, error)
type Activity struct {
	db          ActivityStore
	source      SourceLoader
	credentials CredentialResolver
	provider    func(domain.DeliveryTarget, string) (Publisher, error)
}

func NewActivity(db ActivityStore, source SourceLoader, credentials CredentialResolver, factories ...Factory) (*Activity, error) {
	if db == nil || source == nil || credentials == nil || len(factories) > 1 {
		return nil, domain.ErrInvalidInput
	}
	factory := Factory(func(t domain.DeliveryTarget, s string) (Publisher, error) { return delivery.NewProvider(t, s) })
	if len(factories) == 1 && factories[0] != nil {
		factory = factories[0]
	}
	return &Activity{db: db, source: source, credentials: credentials, provider: factory}, nil
}
func (a *Activity) Publish(ctx context.Context, ref contextworkflow.Reference) (contextworkflow.Result, error) {
	if !domain.IsLowerHex(ref.ID, 32) || !domain.IsLowerHex(ref.Binding, 32) {
		return contextworkflow.Result{}, domain.ErrInvalidInput
	}
	if digest, err := a.db.DeliveryOperationReceipt(ctx, ref.ID, ref.Binding); err != nil || digest != "" {
		return contextworkflow.Result{ReceiptID: ref.ID, Digest: digest}, err
	}
	operation, err := a.db.LoadDeliveryOperation(ctx, ref.ID, ref.Binding)
	if err != nil {
		return contextworkflow.Result{}, err
	}
	work, err := a.db.CheckDeliveryWork(ctx, operation.Work.Delivery.ID, ref.Binding)
	if err != nil {
		return contextworkflow.Result{}, err
	}
	check := func(ctx context.Context) error {
		_, err := a.db.CheckDeliveryWork(ctx, work.Delivery.ID, ref.Binding)
		return err
	}
	var prepared delivery.Prepared
	if operation.Operation == "publish" {
		bundle, err := a.source(ctx, work.Delivery.ID, ref.Binding)
		if err != nil {
			return contextworkflow.Result{}, err
		}
		prepared, err = delivery.Prepare(ctx, bundle, work.Patch)
		if err != nil {
			return contextworkflow.Result{}, err
		}
	} else if operation.Operation != "reconcile" {
		return contextworkflow.Result{}, domain.ErrInvalidInput
	}
	token, err := a.credentials(ctx, work.Delivery.Target, work.CredentialID)
	if err != nil {
		return contextworkflow.Result{}, err
	}
	provider, err := a.provider(work.Delivery.Target, token)
	if err != nil {
		return contextworkflow.Result{}, err
	}
	var observed domain.DeliveryObservation
	if operation.Operation == "publish" {
		observed, err = provider.Publish(ctx, work.Delivery, prepared, check)
	} else {
		observed, err = provider.Observe(ctx, work.Delivery, check)
	}
	if err != nil {
		return contextworkflow.Result{}, err
	}
	digest, err := a.db.CompleteDeliveryOperation(ctx, ref.ID, ref.Binding, observed)
	return contextworkflow.Result{ReceiptID: ref.ID, Digest: digest}, err
}
func safeError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	// Only bounded categories cross into Temporal. Unknown outcomes may retry
	// because every adapter reconciles its deterministic branch and marker first.
	if errors.Is(err, delivery.ErrUnknown) {
		return retryableFailure()
	}
	return permanentFailure()
}
