package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/delivery"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/service"
)

func deliveryFixture(t *testing.T) (context.Context, *Postgres, *service.AuthenticatedService, domain.DeliveryInput) {
	t.Helper()
	ctx, p, s, plan := coordinationFixture(t)
	s = s.WithDeliveries()
	plan.Tasks[0].Checks = []domain.VerificationCommand{{ID: "test", RepositoryID: "repo-one", Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 10}}
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-reviewer", CanExecute: true, CanPublish: true}, {RepositoryID: "repo-one", PrincipalID: "agent-worker", CanExecute: true, CanPublish: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyDeliveryIntegrations(ctx, "synthetic-operator", []domain.DeliveryIntegrationConfig{{WorkspaceID: "workspace-one", RepositoryID: "repo-one", Profile: domain.GitHubDeliveryProfile, Locator: "synthetic/repo", CredentialID: "publish", BaseBranches: []string{"main"}, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCoordination(reviewer, "delivery-execution", plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeCoordination(reviewer, run.ID, run.Digest); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err = p.pool.QueryRow(ctx, `SELECT id FROM coordination_tasks WHERE run_id=$1 AND task_key=$2`, run.ID, plan.Tasks[0].ID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	zero := 0
	patchBytes := []byte("synthetic patch fixture; actual Git verification has separate acceptance")
	patches := []execution.Patch{{RepositoryID: "repo-one", BaseCommit: run.Plan.Repositories[0].Commit, BaseTree: strings.Repeat("a", 40), ResultTree: strings.Repeat("b", 40), Patch: patchBytes, Digest: execution.Sum(patchBytes), Paths: []string{"src/source.go"}}}
	source, _ := json.Marshal(patches)
	artifact := execution.Result{CleanupConfirmed: true, ProfileDigest: plan.Tasks[0].ProfileDigest, Image: plan.Tasks[0].Image, Adapter: "command/v1", AdapterVersion: "1", InputDigest: strings.Repeat("c", 64), Patches: patches, Producer: execution.Evidence{State: "passed", ExitCode: &zero, SourceDigest: strings.Repeat("c", 64), OutputDigest: execution.Sum(nil)}, Checks: []execution.Evidence{{ID: "test", RepositoryID: "repo-one", Argv: plan.Tasks[0].Checks[0].Argv, State: "passed", ExitCode: &zero, SourceDigest: execution.Sum(source), OutputDigest: execution.Sum(nil)}}}
	digest, _ := domain.JSONDigest(artifact)
	// This SQL fixture establishes permission/transaction behavior only. It is not
	// evidence that a coding worker or live provider executed the synthetic patch.
	if _, err = p.pool.Exec(ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,'succeeded',$3,$4)`, taskID, run.ID, digest, artifact); err != nil {
		t.Fatal(err)
	}
	return ctx, p, s, domain.DeliveryInput{RunID: run.ID, TaskID: taskID, ArtifactDigest: digest, BaseBranch: "main", Title: "Synthetic verified change", Description: "Fixture"}
}
func TestDeliveryAuthorizationIsolationAndDurableReceipts(t *testing.T) {
	ctx, p, s, input := deliveryFixture(t)
	agent := accessContext(ctx, "worker", "workspace-one", "repo-one")
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	proposed, err := s.CreateDelivery(agent, "proposal", input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateDelivery(agent, "proposal", input)
	if err != nil || again.ID != proposed.ID {
		t.Fatalf("idempotency: %v", err)
	}
	changed := input
	changed.Title = "Changed"
	if _, err = s.CreateDelivery(agent, "proposal", changed); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("changed input: %v", err)
	}
	if _, err = s.AuthorizeDelivery(agent, proposed.ID, proposed.Digest); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agent published: %v", err)
	}
	if _, err = s.AuthorizeDelivery(human, proposed.ID, strings.Repeat("f", 64)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("wrong digest: %v", err)
	}
	authorized, err := s.AuthorizeDelivery(human, proposed.ID, proposed.Digest)
	if err != nil || authorized.Authorization == nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, err = s.AuthorizeDelivery(human, proposed.ID, proposed.Digest); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM delivery_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate outbox %d %v", count, err)
	}
	target := strings.Repeat("d", 64)
	item, err := p.ClaimDeliveryDispatch(ctx, target, "default")
	if err != nil || item == nil {
		t.Fatalf("claim %v", err)
	}
	if _, err = p.CheckDeliveryWork(ctx, proposed.ID, item.Work.Binding); err != nil {
		t.Fatal(err)
	}
	o := domain.DeliveryObservation{ProviderID: "99", Number: 1, URL: "https://github.com/synthetic/repo/pull/1", Commit: strings.Repeat("e", 40), Tree: proposed.ResultTree, State: "draft", Draft: true, Checks: []domain.ProviderCheck{}, ChecksState: "unknown", Deployment: "not_observed", ProductionOutcome: "not_observed", ObservedAt: time.Now().UTC()}
	receipt, err := p.CompleteDeliveryOperation(ctx, item.ID, item.Work.Binding, o)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.FinishDeliveryDispatch(ctx, *item, "completed", "fixture-run", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDelivery(human, proposed.ID)
	if err != nil || got.Observation == nil || got.Observation.ChecksState != "unknown" {
		t.Fatalf("read observation %+v %v", got, err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-reviewer", CanExecute: true, CanPublish: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.CheckDeliveryWork(ctx, proposed.ID, item.Work.Binding); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("revoked provider I/O permitted: %v", err)
	}
	againReceipt, err := p.CompleteDeliveryOperation(ctx, item.ID, item.Work.Binding, o)
	if err != nil || againReceipt != receipt {
		t.Fatalf("lost persisted receipt %v", err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-two", PrincipalID: "human-reviewer", CanRead: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetDelivery(human, proposed.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross repo hidden %v", err)
	}
	page, err := s.ListDeliveries(human, "", 1)
	if err != nil || len(page.Deliveries) != 0 {
		t.Fatalf("leaked page %+v %v", page, err)
	}
}
func TestDeliveryRejectsChangedApprovalAndRollsBackAudit(t *testing.T) {
	ctx, p, s, input := deliveryFixture(t)
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	d, err := s.CreateDelivery(human, "proposal", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION fail_delivery_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='delivery.authorized' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_delivery_audit BEFORE INSERT ON delivery_audit_events FOR EACH ROW EXECUTE FUNCTION fail_delivery_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeDelivery(human, d.ID, d.Digest); err == nil {
		t.Fatal("audit failure committed")
	}
	var count int
	if err = p.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM delivery_authorizations)+(SELECT count(*) FROM delivery_outbox)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("non atomic %d %v", count, err)
	}
	if _, err = p.pool.Exec(ctx, `DROP TRIGGER fail_delivery_audit ON delivery_audit_events`); err != nil {
		t.Fatal(err)
	}
	run, err := s.GetCoordination(human, input.RunID)
	if err != nil {
		t.Fatal(err)
	}
	pin := run.Plan.Packages[0]
	if _, err = s.Revise(human, pin.ChangeID, "ignored", 1, domain.Content{"intent": "changed approved revision"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeDelivery(human, d.ID, d.Digest); !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatalf("stale approval accepted %v", err)
	}
}

func TestDeliveryWebhookInboxAndLeaseFencing(t *testing.T) {
	ctx, p, s, input := deliveryFixture(t)
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	d, err := s.CreateDelivery(human, "proposal", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeDelivery(human, d.ID, d.Digest); err != nil {
		t.Fatal(err)
	}
	target := strings.Repeat("d", 64)
	first, err := p.ClaimDeliveryDispatch(ctx, target, "default")
	if err != nil || first == nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE delivery_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, first.Number); err != nil {
		t.Fatal(err)
	}
	second, err := p.ClaimDeliveryDispatch(ctx, target, "default")
	if err != nil || second == nil {
		t.Fatal(err)
	}
	if err = p.FinishDeliveryDispatch(ctx, *first, "running", "old", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale acknowledgment accepted %v", err)
	}
	o := domain.DeliveryObservation{ProviderID: "99", Number: 1, URL: "https://github.com/synthetic/repo/pull/1", Commit: strings.Repeat("e", 40), Tree: d.ResultTree, State: "draft", Draft: true, Checks: []domain.ProviderCheck{}, ChecksState: "unknown", Deployment: "not_observed", ProductionOutcome: "not_observed", ObservedAt: time.Now().UTC()}
	if _, err = p.CompleteDeliveryOperation(ctx, second.ID, second.Work.Binding, o); err != nil {
		t.Fatal(err)
	}
	if err = p.FinishDeliveryReceiptDispatch(ctx, *second); err != nil {
		t.Fatal(err)
	}
	event := delivery.WebhookEvent{Key: "event-fixture", PayloadDigest: strings.Repeat("f", 64), Type: "status", Commit: o.Commit}
	if err = p.RecordDeliveryWebhook(ctx, d.Target, event); err != nil {
		t.Fatal(err)
	}
	if err = p.RecordDeliveryWebhook(ctx, d.Target, event); err != nil {
		t.Fatal(err)
	}
	changed := event
	changed.PayloadDigest = strings.Repeat("a", 64)
	if err = p.RecordDeliveryWebhook(ctx, d.Target, changed); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("changed event replay %v", err)
	}
	var inbox, outbox int
	if err = p.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM delivery_event_inbox),(SELECT count(*) FROM delivery_outbox WHERE operation='reconcile')`).Scan(&inbox, &outbox); err != nil || inbox != 1 || outbox != 1 {
		t.Fatalf("duplicate webhook %d %d %v", inbox, outbox, err)
	}
}
