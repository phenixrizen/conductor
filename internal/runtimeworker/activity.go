package runtimeworker

import (
	"context"
	"errors"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/runtimeevidence"
	"github.com/phenixrizen/conductor/internal/store"
)

type Store interface {
	RuntimeOperationReceipt(context.Context, string, string) (string, error)
	CheckRuntimeWork(context.Context, string, string) (store.RuntimeWork, error)
	CompleteRuntimeEvidence(context.Context, string, string, domain.RuntimeReceipt) (string, error)
}
type CredentialResolver func(context.Context, domain.RuntimeTarget, string) (string, error)
type Collector interface {
	Collect(context.Context, domain.RuntimeEvidence) (domain.RuntimeReceipt, error)
}
type Activity struct {
	db           Store
	credentials  CredentialResolver
	newCollector func(string, func(context.Context) error) (Collector, error)
}

func NewActivity(db Store, credentials CredentialResolver) (*Activity, error) {
	if db == nil || credentials == nil {
		return nil, domain.ErrInvalidInput
	}
	return &Activity{db: db, credentials: credentials, newCollector: func(token string, check func(context.Context) error) (Collector, error) {
		return runtimeevidence.New(token, check)
	}}, nil
}
func (a *Activity) Collect(ctx context.Context, ref contextworkflow.Reference) (contextworkflow.Result, error) {
	if !domain.IsLowerHex(ref.ID, 32) || !domain.IsLowerHex(ref.Binding, 32) {
		return contextworkflow.Result{}, domain.ErrInvalidInput
	}
	if digest, err := a.db.RuntimeOperationReceipt(ctx, ref.ID, ref.Binding); err != nil || digest != "" {
		return contextworkflow.Result{ReceiptID: ref.ID, Digest: digest}, err
	}
	w, err := a.db.CheckRuntimeWork(ctx, ref.ID, ref.Binding)
	if err != nil {
		return contextworkflow.Result{}, err
	}
	token, err := a.credentials(ctx, w.Evidence.Target, w.CredentialID)
	if err != nil {
		return contextworkflow.Result{}, err
	}
	collector, err := a.newCollector(token, func(ctx context.Context) error { _, err := a.db.CheckRuntimeWork(ctx, ref.ID, ref.Binding); return err })
	if err != nil {
		return contextworkflow.Result{}, err
	}
	receipt, err := collector.Collect(ctx, w.Evidence)
	if err != nil {
		return contextworkflow.Result{}, err
	}
	digest, err := a.db.CompleteRuntimeEvidence(ctx, ref.ID, ref.Binding, receipt)
	if err != nil {
		return contextworkflow.Result{}, err
	}
	return contextworkflow.Result{ReceiptID: ref.ID, Digest: digest}, nil
}
func safeError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrStaleApproval) || errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrUnauthenticated) {
		return permanentFailure()
	}
	return retryableFailure()
}
