package collectionworker

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/repositorycontext/remote"
	"github.com/phenixrizen/conductor/internal/store"
)

const workerTokenCanary = "synthetic-provider-token-canary"
const workerSourceCanary = "synthetic-private-source-canary"

type activityStore struct {
	load     func(context.Context, string, string) (store.CollectionWork, error)
	check    func(context.Context, string, string) error
	complete func(context.Context, string, string, []domain.ContextArtifact) (domain.CollectionReceipt, error)
}

func (s activityStore) CollectionWork(ctx context.Context, id, binding string) (store.CollectionWork, error) {
	return s.load(ctx, id, binding)
}
func (s activityStore) CheckCollectionWork(ctx context.Context, id, binding string) error {
	return s.check(ctx, id, binding)
}
func (s activityStore) CompleteCollection(ctx context.Context, id, binding string, a []domain.ContextArtifact) (domain.CollectionReceipt, error) {
	return s.complete(ctx, id, binding, a)
}

type collectFunc func(context.Context, string, []string, func(context.Context) error) ([]domain.ContextArtifact, error)

func (f collectFunc) Collect(ctx context.Context, commit string, paths []string, authorize func(context.Context) error) ([]domain.ContextArtifact, error) {
	return f(ctx, commit, paths, authorize)
}

func activityFixture() (contextworkflow.Reference, store.CollectionWork, domain.CollectionReceipt) {
	ref := contextworkflow.Reference{ID: strings.Repeat("1", 32), Binding: strings.Repeat("2", 32)}
	source := domain.ContextSource{WorkspaceID: "workspace-one", RepositoryID: "repo-one", Provider: "github", Host: "github.com", ProviderID: "42", Locator: "synthetic/context", Profile: remote.GitHubProfile, IntegrationVersion: 1}
	work := store.CollectionWork{Binding: ref.Binding, BindingDigest: strings.Repeat("3", 64), Collection: domain.Collection{ID: ref.ID, WorkspaceID: source.WorkspaceID, RepositoryID: source.RepositoryID, RequesterID: "synthetic-author", Input: domain.CollectionInput{Commit: strings.Repeat("4", 40), Paths: []string{"source.txt"}}, Source: source}, Integration: domain.ContextIntegration{Source: source, CredentialID: "synthetic-credential", Enabled: true}}
	text := workerSourceCanary
	git := sha1.Sum(append([]byte(fmt.Sprintf("blob %d%c", len(text), 0)), []byte(text)...))
	digest := sha256.Sum256([]byte(text))
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	work.Collection.CreatedAt = now
	work.Collection.InputDigest, _ = domain.JSONDigest(work.Collection.Input)
	work.BindingDigest, _ = domain.ContextBindingDigest(work.Integration)
	receipt := domain.CollectionReceipt{ID: ref.ID, CreatedAt: now, Snapshot: domain.RepositoryContext{SchemaVersion: 2, Repository: "github:github.com:42", RequestedRef: work.Collection.Input.Commit, Commit: work.Collection.Input.Commit, Collector: domain.RemoteContextCollector, CollectedAt: now, CollectionID: ref.ID, Source: &source, Artifacts: []domain.ContextArtifact{{Path: "source.txt", State: "collected", BlobOID: hex.EncodeToString(git[:]), Digest: hex.EncodeToString(digest[:]), Text: &text}}}}
	receipt.Digest, _ = domain.JSONDigest(receipt.Snapshot)
	return ref, work, receipt
}

func assertSafeActivityFailure(t *testing.T, err error, code string, retryable bool) contextworkflow.Failure {
	t.Helper()
	var f contextworkflow.Failure
	if !errors.As(err, &f) || f.Code != code || f.Retryable != retryable {
		t.Fatalf("activity failure=%v (%+v), want %s retryable=%v", err, f, code, retryable)
	}
	data, _ := json.Marshal(f)
	for _, canary := range []string{workerTokenCanary, workerSourceCanary} {
		if strings.Contains(err.Error(), canary) || strings.Contains(string(data), canary) {
			t.Fatal("failure retained sensitive diagnostics")
		}
	}
	if errors.Unwrap(err) != nil {
		t.Fatal("safe failure retained a raw cause")
	}
	return f
}

func TestActivityRecoversReceiptBeforeCredentialsOrPermissionChecks(t *testing.T) {
	ref, work, receipt := activityFixture()
	work.Collection.Receipt = &receipt
	db := activityStore{load: func(ctx context.Context, id, binding string) (store.CollectionWork, error) {
		if id != ref.ID || binding != ref.Binding {
			t.Fatal("request binding changed")
		}
		return work, nil
	}, check: func(context.Context, string, string) error {
		t.Fatal("recovered receipt rechecked revoked author authority")
		return nil
	}, complete: func(context.Context, string, string, []domain.ContextArtifact) (domain.CollectionReceipt, error) {
		t.Fatal("recovered receipt republished source")
		return domain.CollectionReceipt{}, nil
	}}
	a, err := NewActivity(db, func(context.Context, domain.ContextIntegration) (string, error) {
		t.Fatal("recovered receipt opened revoked/missing credentials")
		return "", nil
	}, func(remote.Config) (Collector, error) {
		t.Fatal("recovered receipt contacted provider")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Collect(context.Background(), ref)
	if err != nil || got != (contextworkflow.Result{ReceiptID: receipt.ID, Digest: receipt.Digest}) {
		t.Fatalf("recover committed receipt: %+v %v", got, err)
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), workerSourceCanary) || strings.Contains(string(data), workerTokenCanary) || len(data) > 256 {
		t.Fatal("workflow result retained source/credentials")
	}
}

func TestActivityChecksEachProviderOperationThenPersists(t *testing.T) {
	ref, work, receipt := activityFixture()
	events := []string{}
	text := workerSourceCanary
	artifacts := []domain.ContextArtifact{{Path: "source.txt", State: "collected", Text: &text}}
	db := activityStore{
		load: func(context.Context, string, string) (store.CollectionWork, error) {
			events = append(events, "load")
			return work, nil
		},
		check: func(_ context.Context, id, binding string) error {
			if id != ref.ID || binding != ref.Binding {
				t.Fatal("authorizer used another request")
			}
			events = append(events, "authorize")
			return nil
		},
		complete: func(_ context.Context, id, binding string, got []domain.ContextArtifact) (domain.CollectionReceipt, error) {
			if id != ref.ID || binding != ref.Binding || !reflect.DeepEqual(got, artifacts) {
				t.Fatal("completion changed immutable request/artifacts")
			}
			events = append(events, "persist")
			return receipt, nil
		},
	}
	a, err := NewActivity(db, func(_ context.Context, in domain.ContextIntegration) (string, error) {
		if !reflect.DeepEqual(in, work.Integration) {
			t.Fatal("credentials resolved under another binding")
		}
		events = append(events, "credentials")
		return workerTokenCanary, nil
	}, func(c remote.Config) (Collector, error) {
		if c.Binding != (remote.Binding{Provider: "github", Host: "github.com", ProviderID: "42", Locator: "synthetic/context", Profile: remote.GitHubProfile}) || c.Token != workerTokenCanary || c.APIOrigin != "" || c.AllowInsecureLoopback {
			t.Fatal("collector binding or production origin changed")
		}
		return collectFunc(func(ctx context.Context, commit string, paths []string, authorize func(context.Context) error) ([]domain.ContextArtifact, error) {
			if commit != work.Collection.Input.Commit || !reflect.DeepEqual(paths, work.Collection.Input.Paths) {
				t.Fatal("pinned collection inputs changed")
			}
			for range 3 {
				if err := authorize(ctx); err != nil {
					return nil, err
				}
				events = append(events, "provider")
			}
			return artifacts, nil
		}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Collect(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	want := []string{"load", "authorize", "credentials", "authorize", "provider", "authorize", "provider", "authorize", "provider", "persist"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("operation order=%v", events)
	}
}

func TestActivityRevocationStopsLaterReadsAndPublication(t *testing.T) {
	for _, failureAt := range []int{1, 3} {
		t.Run(fmt.Sprint(failureAt), func(t *testing.T) {
			ref, work, _ := activityFixture()
			checks, reads, credentials := 0, 0, 0
			db := activityStore{load: func(context.Context, string, string) (store.CollectionWork, error) { return work, nil }, check: func(context.Context, string, string) error {
				checks++
				if checks == failureAt {
					return fmt.Errorf("%s: %w", workerTokenCanary, domain.ErrForbidden)
				}
				return nil
			}, complete: func(context.Context, string, string, []domain.ContextArtifact) (domain.CollectionReceipt, error) {
				t.Fatal("revoked collection published source")
				return domain.CollectionReceipt{}, nil
			}}
			a, err := NewActivity(db, func(context.Context, domain.ContextIntegration) (string, error) {
				credentials++
				return workerTokenCanary, nil
			}, func(remote.Config) (Collector, error) {
				return collectFunc(func(ctx context.Context, _ string, _ []string, authorize func(context.Context) error) ([]domain.ContextArtifact, error) {
					for range 3 {
						if err := authorize(ctx); err != nil {
							return nil, &remote.Error{Code: "access_revoked"}
						}
						reads++
					}
					return nil, nil
				}), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.Collect(context.Background(), ref)
			assertSafeActivityFailure(t, err, "permission_revoked", false)
			if checks != failureAt || reads != max(0, failureAt-2) || failureAt == 1 && credentials != 0 {
				t.Fatalf("revocation checks=%d reads=%d credentials=%d", checks, reads, credentials)
			}
		})
	}
}

func TestActivitySanitizesBoundaryFailures(t *testing.T) {
	for _, stage := range []string{"load", "initial_check", "credentials", "factory", "provider", "commit"} {
		t.Run(stage, func(t *testing.T) {
			ref, work, receipt := activityFixture()
			raw := errors.New(workerTokenCanary + " " + workerSourceCanary)
			db := activityStore{load: func(context.Context, string, string) (store.CollectionWork, error) {
				if stage == "load" {
					return store.CollectionWork{}, raw
				}
				return work, nil
			}, check: func(context.Context, string, string) error {
				if stage == "initial_check" {
					return raw
				}
				return nil
			}, complete: func(context.Context, string, string, []domain.ContextArtifact) (domain.CollectionReceipt, error) {
				if stage == "commit" {
					return domain.CollectionReceipt{}, raw
				}
				return receipt, nil
			}}
			a, err := NewActivity(db, func(context.Context, domain.ContextIntegration) (string, error) {
				if stage == "credentials" {
					return "", raw
				}
				return workerTokenCanary, nil
			}, func(remote.Config) (Collector, error) {
				if stage == "factory" {
					return nil, raw
				}
				return collectFunc(func(context.Context, string, []string, func(context.Context) error) ([]domain.ContextArtifact, error) {
					if stage == "provider" {
						return nil, raw
					}
					return nil, nil
				}), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.Collect(context.Background(), ref)
			code, retryable := "database_unavailable", true
			if stage == "credentials" || stage == "factory" {
				code, retryable = "invalid_configuration", false
			}
			if stage == "provider" {
				code, retryable = "internal", false
			}
			assertSafeActivityFailure(t, err, code, retryable)
		})
	}
}

func TestActivityFailureClassification(t *testing.T) {
	for _, row := range []struct {
		err       error
		code      string
		retryable bool
	}{{domain.ErrCollectionStopped, "cancelled", false}, {domain.ErrForbidden, "permission_revoked", false}, {domain.ErrUnauthenticated, "permission_revoked", false}, {domain.ErrNotFound, "not_found", false}, {domain.ErrInvalidInput, "invalid_request", false}, {domain.ErrUnavailable, "integration_disabled", false}, {errors.New(workerTokenCanary), "database_unavailable", true}} {
		assertSafeActivityFailure(t, databaseFailure(fmt.Errorf("%s: %w", workerSourceCanary, row.err)), row.code, row.retryable)
	}
	if err := databaseFailure(fmt.Errorf("%s: %w", workerTokenCanary, context.Canceled)); !errors.Is(err, context.Canceled) {
		t.Fatalf("database cancellation=%v", err)
	}
	for _, row := range []struct {
		code, want string
		retryable  bool
	}{{"access_revoked", "permission_revoked", false}, {"identity_mismatch", "binding_mismatch", false}, {"invalid_config", "invalid_configuration", false}, {"invalid_input", "invalid_request", false}, {"transport", "unavailable", true}, {"authorization_unavailable", "unavailable", true}, {"rate_limited", "rate_limited", true}, {"object_mismatch", "unavailable", false}} {
		err := providerFailure(context.Background(), fmt.Errorf("%s: %w", workerTokenCanary, &remote.Error{Code: row.code, Retryable: row.retryable, RetryAfter: 2 * time.Second}))
		got := assertSafeActivityFailure(t, err, row.want, row.retryable)
		if row.code == "rate_limited" && got.RetryAfter != 2*time.Second {
			t.Fatal("provider retry delay lost")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := providerFailure(ctx, errors.New(workerTokenCanary)); !errors.Is(err, context.Canceled) {
		t.Fatalf("provider cancellation=%v", err)
	}
}

func TestActivityCredentialCancellationRemainsCancellation(t *testing.T) {
	ref, work, _ := activityFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := activityStore{load: func(context.Context, string, string) (store.CollectionWork, error) { return work, nil }, check: func(context.Context, string, string) error { return nil }, complete: func(context.Context, string, string, []domain.ContextArtifact) (domain.CollectionReceipt, error) {
		t.Fatal("cancelled activity committed")
		return domain.CollectionReceipt{}, nil
	}}
	a, err := NewActivity(db, func(context.Context, domain.ContextIntegration) (string, error) { cancel(); return "", ctx.Err() }, func(remote.Config) (Collector, error) {
		t.Fatal("cancelled credential resolution created collector")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Collect(ctx, ref)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("credential cancellation became another terminal failure: %v", err)
	}
}

func TestActivityRequiresStoreAndCredentials(t *testing.T) {
	if _, err := NewActivity(nil, func(context.Context, domain.ContextIntegration) (string, error) { return "", nil }, nil); !errors.Is(err, contextworkflow.ErrInvalidConfiguration) {
		t.Fatalf("nil store accepted: %v", err)
	}
	if _, err := NewActivity(activityStore{}, nil, nil); !errors.Is(err, contextworkflow.ErrInvalidConfiguration) {
		t.Fatalf("nil credentials accepted: %v", err)
	}
}
