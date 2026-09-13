// Package collectionworker connects durable collection facts to Temporal and
// bounded provider reads. It never executes repository-controlled commands.
package collectionworker

import (
	"context"
	"errors"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/repositorycontext/remote"
	"github.com/phenixrizen/conductor/internal/store"
)

type WorkStore interface {
	CollectionWork(context.Context, string, string) (store.CollectionWork, error)
	CheckCollectionWork(context.Context, string, string) error
	CompleteCollection(context.Context, string, string, []domain.ContextArtifact) (domain.CollectionReceipt, error)
}

type Collector interface {
	Collect(context.Context, string, []string, func(context.Context) error) ([]domain.ContextArtifact, error)
}

type CredentialResolver func(context.Context, domain.ContextIntegration) (string, error)
type CollectorFactory func(remote.Config) (Collector, error)

type Activity struct {
	store       WorkStore
	credentials CredentialResolver
	collector   CollectorFactory
	indexer     interface {
		Index(context.Context, []domain.ContextArtifact) (domain.CodeGraphIndex, error)
	}
}

// A custom factory exists for controlled provider acceptance fixtures. The
// executable always uses remote.New and offers no provider-origin override.
func NewActivity(db WorkStore, credentials CredentialResolver, factory CollectorFactory) (*Activity, error) {
	if db == nil || credentials == nil {
		return nil, contextworkflow.ErrInvalidConfiguration
	}
	if factory == nil {
		factory = func(c remote.Config) (Collector, error) { return remote.New(c) }
	}
	return &Activity{store: db, credentials: credentials, collector: factory}, nil
}

// WithCodeGraph enables the operator-pinned, credential-free parser activity.
// Older committed receipts remain recoverable without backfilling mutable data.
func (a *Activity) WithCodeGraph(indexer interface {
	Index(context.Context, []domain.ContextArtifact) (domain.CodeGraphIndex, error)
}) *Activity {
	copy := *a
	copy.indexer = indexer
	return &copy
}

func (a *Activity) Collect(ctx context.Context, ref contextworkflow.Reference) (contextworkflow.Result, error) {
	work, err := a.store.CollectionWork(ctx, ref.ID, ref.Binding)
	if err != nil {
		return contextworkflow.Result{}, databaseFailure(err)
	}
	if receipt := work.Collection.Receipt; receipt != nil {
		// A lost activity acknowledgment must recover the committed receipt even
		// after later revocation. Public reads still require current permission.
		return receiptResult(*receipt), nil
	}
	if err = a.store.CheckCollectionWork(ctx, ref.ID, ref.Binding); err != nil {
		return contextworkflow.Result{}, databaseFailure(err)
	}
	token, err := a.credentials(ctx, work.Integration)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return contextworkflow.Result{}, context.Canceled
		}
		return contextworkflow.Result{}, contextworkflow.Failure{Code: "invalid_configuration"}
	}
	source := work.Collection.Source
	collector, err := a.collector(remote.Config{Binding: remote.Binding{
		Provider: source.Provider, Host: source.Host, ProviderID: source.ProviderID,
		Locator: source.Locator, Profile: source.Profile,
	}, Token: token})
	if err != nil {
		return contextworkflow.Result{}, contextworkflow.Failure{Code: "invalid_configuration"}
	}
	artifacts, err := collector.Collect(ctx, work.Collection.Input.Commit, work.Collection.Input.Paths, func(ctx context.Context) error {
		return a.store.CheckCollectionWork(ctx, ref.ID, ref.Binding)
	})
	if err != nil {
		return contextworkflow.Result{}, providerFailure(ctx, err)
	}
	var receipt domain.CollectionReceipt
	if work.Collection.Input.FullSource {
		fullCollector, ok := collector.(interface {
			FullSource(context.Context, string, func(context.Context) error) (domain.SourceBundleData, error)
		})
		sink, sinkOK := a.store.(interface {
			CompleteCollectionWithSource(context.Context, string, string, []domain.ContextArtifact, domain.SourceBundleData, domain.CodeGraphIndex) (domain.CollectionReceipt, error)
		})
		if !ok || !sinkOK || a.indexer == nil {
			return contextworkflow.Result{}, contextworkflow.Failure{Code: "invalid_configuration"}
		}
		full, e := fullCollector.FullSource(ctx, work.Collection.Input.Commit, func(ctx context.Context) error { return a.store.CheckCollectionWork(ctx, ref.ID, ref.Binding) })
		if e != nil {
			return contextworkflow.Result{}, providerFailure(ctx, e)
		}
		if e = a.store.CheckCollectionWork(ctx, ref.ID, ref.Binding); e != nil {
			return contextworkflow.Result{}, databaseFailure(e)
		}
		index, e := a.indexer.Index(ctx, full.Artifacts)
		if e != nil {
			if ctx.Err() != nil {
				return contextworkflow.Result{}, ctx.Err()
			}
			return contextworkflow.Result{}, contextworkflow.Failure{Code: "unavailable", Retryable: true}
		}
		receipt, err = sink.CompleteCollectionWithSource(ctx, ref.ID, ref.Binding, artifacts, full, index)
	} else if a.indexer != nil {
		if err = a.store.CheckCollectionWork(ctx, ref.ID, ref.Binding); err != nil {
			return contextworkflow.Result{}, databaseFailure(err)
		}
		index, indexErr := a.indexer.Index(ctx, artifacts)
		if indexErr != nil {
			if ctx.Err() != nil {
				return contextworkflow.Result{}, ctx.Err()
			}
			return contextworkflow.Result{}, contextworkflow.Failure{Code: "unavailable", Retryable: true}
		}
		sink, ok := a.store.(interface {
			CompleteCollectionWithCodeGraph(context.Context, string, string, []domain.ContextArtifact, domain.CodeGraphIndex) (domain.CollectionReceipt, error)
		})
		if !ok {
			return contextworkflow.Result{}, contextworkflow.Failure{Code: "invalid_configuration"}
		}
		receipt, err = sink.CompleteCollectionWithCodeGraph(ctx, ref.ID, ref.Binding, artifacts, index)
	} else {
		receipt, err = a.store.CompleteCollection(ctx, ref.ID, ref.Binding, artifacts)
	}
	if err != nil {
		return contextworkflow.Result{}, databaseFailure(err)
	}
	return receiptResult(receipt), nil
}

func receiptResult(receipt domain.CollectionReceipt) contextworkflow.Result {
	return contextworkflow.Result{ReceiptID: receipt.ID, Digest: receipt.Digest}
}

func databaseFailure(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, domain.ErrCollectionStopped):
		return contextworkflow.Failure{Code: "cancelled"}
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrUnauthenticated):
		return contextworkflow.Failure{Code: "permission_revoked"}
	case errors.Is(err, domain.ErrNotFound):
		return contextworkflow.Failure{Code: "not_found"}
	case errors.Is(err, domain.ErrInvalidInput):
		return contextworkflow.Failure{Code: "invalid_request"}
	case errors.Is(err, domain.ErrUnavailable):
		return contextworkflow.Failure{Code: "integration_disabled"}
	default:
		// Raw SQL and connection errors can contain credentials and source values.
		return contextworkflow.Failure{Code: "database_unavailable", Retryable: true}
	}
}

func providerFailure(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var remoteErr *remote.Error
	if !errors.As(err, &remoteErr) {
		return contextworkflow.Failure{Code: "internal"}
	}
	switch remoteErr.Code {
	case "cancelled":
		return context.Canceled
	case "access_revoked":
		return contextworkflow.Failure{Code: "permission_revoked"}
	case "invalid_config":
		return contextworkflow.Failure{Code: "invalid_configuration"}
	case "invalid_input":
		return contextworkflow.Failure{Code: "invalid_request"}
	case "identity_mismatch":
		return contextworkflow.Failure{Code: "binding_mismatch"}
	case "rate_limited":
		return contextworkflow.Failure{Code: "rate_limited", Retryable: remoteErr.Retryable, RetryAfter: remoteErr.RetryAfter}
	default:
		return contextworkflow.Failure{Code: "unavailable", Retryable: remoteErr.Retryable, RetryAfter: remoteErr.RetryAfter}
	}
}
