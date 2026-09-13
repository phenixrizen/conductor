package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func contextOutboxFixture(t *testing.T) (context.Context, *Postgres, *service.AuthenticatedService, context.Context, domain.Collection) {
	t.Helper()
	ctx, p, _ := integrationStore(t)
	config := syntheticAccessConfig()
	config.Repositories[0].Host = "github.com"
	config.ContextIntegrations = []domain.ContextIntegrationConfig{{RepositoryID: "repo-one", WorkspaceID: "workspace-one", Profile: "github-rest/2026-03-10", Locator: "synthetic/repository", CredentialID: "fixture", Enabled: true}}
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", config); err != nil {
		t.Fatal(err)
	}
	s := service.NewAuthenticated(p).WithCollections()
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	collection, err := s.CreateCollection(author, "outbox-fixture", domain.CollectionInput{Commit: strings.Repeat("a", 40), Paths: []string{"README.md"}})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, p, s, author, collection
}

func TestContextOutboxBindsTargetBeforeExternalCall(t *testing.T) {
	t.Parallel()
	ctx, p, _, _, collection := contextOutboxFixture(t)
	targetA, targetB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	first, err := p.ClaimContextDispatch(ctx, targetA, "namespace-a")
	if err != nil || first == nil || first.ID != collection.ID || first.Mismatch || first.PreviouslyAttempted || first.BoundTarget != targetA || first.BoundNamespace != "namespace-a" {
		t.Fatalf("first claim: %#v %v", first, err)
	}
	// Model a process death before any RPC/acknowledgment. The committed binding
	// must survive independently of both the lease and execution observations.
	if _, err = p.pool.Exec(ctx, `UPDATE context_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE collection_id=$1`, collection.ID); err != nil {
		t.Fatal(err)
	}
	second, err := p.ClaimContextDispatch(ctx, targetB, "namespace-b")
	if err != nil || second == nil || !second.Mismatch || !second.PreviouslyAttempted || second.BoundTarget != targetA || second.BoundNamespace != "namespace-a" || second.LeaseToken == first.LeaseToken {
		t.Fatalf("reclaimed target mismatch: %#v %v", second, err)
	}
	if err = p.FinishContextDispatch(ctx, *first, "delivered", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expired dispatcher acknowledged replacement lease: %v", err)
	}
	if err = p.ObserveContextExecution(ctx, second.ID, second.Binding, targetB, "namespace-b", "conductor.collect.v1/"+second.ID, "", "unresolved"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("mismatched runtime wrote an observation: %v", err)
	}
	if err = p.ObserveContextExecution(ctx, second.ID, second.Binding, second.BoundTarget, second.BoundNamespace, "conductor.collect.v1/"+second.ID, "", "unresolved"); err != nil {
		t.Fatal("record original target as unresolved:", err)
	}
	if err = p.FinishContextDispatch(ctx, *second, "unresolved", "runtime_target_mismatch"); err != nil {
		t.Fatal(err)
	}
	if item, err := p.ClaimContextDispatch(ctx, targetB, "namespace-b"); err != nil || item != nil {
		t.Fatalf("unresolved handoff was automatically redispatched: %#v %v", item, err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE context_runtime_bindings SET target=$2 WHERE collection_id=$1`, collection.ID, targetB); err == nil {
		t.Fatal("runtime binding was mutable")
	}
	if _, err = p.pool.Exec(ctx, `DELETE FROM context_runtime_bindings WHERE collection_id=$1`, collection.ID); err == nil {
		t.Fatal("runtime binding was deletable")
	}
}

func TestContextCancelSharesOriginalRuntimeBinding(t *testing.T) {
	t.Parallel()
	ctx, p, s, author, collection := contextOutboxFixture(t)
	targetA, targetB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	first, err := p.ClaimContextDispatch(ctx, targetA, "namespace-a")
	if err != nil || first == nil {
		t.Fatalf("start claim: %#v %v", first, err)
	}
	if _, err = s.CancelCollection(author, collection.ID); err != nil {
		t.Fatal(err)
	}
	cancellation, err := p.ClaimContextDispatch(ctx, targetB, "namespace-b")
	if err != nil || cancellation == nil || cancellation.Operation != "cancel" || !cancellation.Mismatch || cancellation.BoundTarget != targetA || cancellation.BoundNamespace != "namespace-a" {
		t.Fatalf("cancel was redirected to another runtime: %#v %v", cancellation, err)
	}
}

func TestContextOutboxClaimsAreExclusiveAndTimeBounded(t *testing.T) {
	t.Parallel()
	ctx, p, _, _, collection := contextOutboxFixture(t)
	target := strings.Repeat("a", 64)
	var wg sync.WaitGroup
	items := make(chan *ContextDispatch, 2)
	errorsCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := p.ClaimContextDispatch(ctx, target, "namespace-a")
			items <- item
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(items)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	claims := 0
	for item := range items {
		if item != nil {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("one outbox row produced %d concurrent claims", claims)
	}
	if _, err := p.pool.Exec(ctx, `UPDATE context_outbox SET lease_until=clock_timestamp()-interval '1 second',first_attempt_at=clock_timestamp()-interval '2 hours' WHERE collection_id=$1`, collection.ID); err != nil {
		t.Fatal(err)
	}
	item, err := p.ClaimContextDispatch(ctx, target, "namespace-a")
	if err != nil || item == nil || !item.UncertaintyExpired || !item.PreviouslyAttempted || item.Mismatch {
		t.Fatalf("uncertainty horizon: %#v %v", item, err)
	}
}

func TestContextObservationsRequireBindingReceiptAndStableRun(t *testing.T) {
	t.Parallel()
	ctx, p, _, _, collection := contextOutboxFixture(t)
	target := strings.Repeat("a", 64)
	var binding string
	if err := p.pool.QueryRow(ctx, `SELECT binding FROM context_collections WHERE id=$1`, collection.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	observe := func(run, state string) error {
		return p.ObserveContextExecution(ctx, collection.ID, binding, target, "namespace-a", "conductor.collect.v1/"+collection.ID, run, state)
	}
	if err := observe("run-one", "running"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("observation preceded durable runtime binding: %v", err)
	}
	item, err := p.ClaimContextDispatch(ctx, target, "namespace-a")
	if err != nil || item == nil {
		t.Fatalf("claim: %#v %v", item, err)
	}
	if err = observe("run-one", "completed"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Temporal completion fabricated a receipt: %v", err)
	}
	if err = observe("run-one", "running"); err != nil {
		t.Fatal(err)
	}
	if err = observe("run-two", "running"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("execution run was silently replaced: %v", err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE context_execution_observations SET observed_at=clock_timestamp()-interval '1 minute' WHERE collection_id=$1`, collection.ID); err != nil {
		t.Fatal(err)
	}
	lookups, err := p.ContextObservationTargets(ctx)
	if err != nil || len(lookups) != 1 || lookups[0].BoundTarget != target || lookups[0].BoundNamespace != "namespace-a" || lookups[0].RunID != "run-one" {
		t.Fatalf("reconciliation lost runtime identity: %#v %v", lookups, err)
	}
	text := "synthetic receipt text"
	textDigest := sha256.Sum256([]byte(text))
	_, err = p.CompleteCollection(ctx, collection.ID, binding, []domain.ContextArtifact{{Path: "README.md", State: "collected", BlobOID: strings.Repeat("c", 40), Digest: hex.EncodeToString(textDigest[:]), Text: &text}})
	if err != nil {
		t.Fatal(err)
	}
	if err = observe("run-one", "completed"); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"running", "unavailable", "unresolved", "cancelled", "failed"} {
		if err = observe("run-one", stale); err != nil {
			t.Fatal(err)
		}
	}
	var state, namespace string
	if err = p.pool.QueryRow(ctx, `SELECT state,namespace FROM context_execution_observations WHERE collection_id=$1`, collection.ID).Scan(&state, &namespace); err != nil || state != "completed" || namespace != "namespace-a" {
		t.Fatalf("terminal observation regressed: state=%s namespace=%s err=%v", state, namespace, err)
	}
}

func TestContextUnknownStartAndUnresolvedObservations(t *testing.T) {
	t.Parallel()
	ctx, p, _, _, collection := contextOutboxFixture(t)
	target := strings.Repeat("a", 64)
	item, err := p.ClaimContextDispatch(ctx, target, "namespace-a")
	if err != nil || item == nil {
		t.Fatalf("claim: %#v %v", item, err)
	}
	observe := func(run, state string) error {
		return p.ObserveContextExecution(ctx, collection.ID, item.Binding, target, "namespace-a", "conductor.collect.v1/"+collection.ID, run, state)
	}
	if err = observe("", "unavailable"); err != nil {
		t.Fatal("record lost start acknowledgment:", err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE context_execution_observations SET observed_at=clock_timestamp()-interval '1 minute' WHERE collection_id=$1`, collection.ID); err != nil {
		t.Fatal(err)
	}
	if targets, err := p.ContextObservationTargets(ctx); err != nil || len(targets) != 0 {
		t.Fatalf("unknown initial run bypassed dispatch reconciliation horizon: %#v %v", targets, err)
	}
	if err = observe("run-one", "running"); err != nil {
		t.Fatal(err)
	}
	if err = observe("", "unresolved"); err != nil {
		t.Fatal(err)
	}
	for _, late := range []string{"running", "unavailable", "failed", "cancelled"} {
		if err = observe("run-one", late); err != nil {
			t.Fatal(err)
		}
	}
	var state, run string
	if err = p.pool.QueryRow(ctx, `SELECT state,run_id FROM context_execution_observations WHERE collection_id=$1`, collection.ID).Scan(&state, &run); err != nil || state != "unresolved" || run != "run-one" {
		t.Fatalf("late dispatcher revived unresolved execution: state=%s run=%s err=%v", state, run, err)
	}
	if targets, err := p.ContextObservationTargets(ctx); err != nil || len(targets) != 0 {
		t.Fatalf("unresolved execution was automatically reconciled: %#v %v", targets, err)
	}
}
