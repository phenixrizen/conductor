package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/runtimeevidence"
	"github.com/phenixrizen/conductor/internal/service"
)

func runtimeFixture(t *testing.T) (context.Context, *Postgres, *service.AuthenticatedService, domain.RuntimeInput, domain.RuntimeIntegrationConfig) {
	t.Helper()
	ctx, p, s, plan := coordinationFixture(t)
	s = s.WithDeliveries().WithRuntimeEvidence()
	criterion := domain.RuntimeCriterion{ID: "latency", Metric: "synthetic_latency", Aggregation: "maximum", Operator: "lte", Threshold: 5, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 600}
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	pkg, err := s.Revise(author, plan.Packages[0].ChangeID, "ignored", 1, domain.Content{"intent": "Synthetic runtime criteria", "runtimeCriteria": domain.RuntimeCriteria{SchemaVersion: 1, Criteria: []domain.RuntimeCriterion{criterion}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(author, pkg.ID, "ignored", pkg.Revision.Number); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Approve(human, pkg.ID, "ignored", pkg.Revision.Number, pkg.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	plan.Packages[0].Revision = pkg.Revision.Number
	plan.Packages[0].Digest = pkg.Revision.Digest
	plan.Tasks[0].Checks = []domain.VerificationCommand{{ID: "test", RepositoryID: "repo-one", Argv: []string{"true"}, TimeoutSeconds: 10}}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-reviewer", CanExecute: true, CanPublish: true}}}); err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyDeliveryIntegrations(ctx, "synthetic-operator", []domain.DeliveryIntegrationConfig{{WorkspaceID: "workspace-one", RepositoryID: "repo-one", Profile: domain.GitHubDeliveryProfile, Locator: "synthetic/repo", CredentialID: "publish", BaseBranches: []string{"main"}, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCoordination(human, "runtime-run", plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeCoordination(human, run.ID, run.Digest); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err = p.pool.QueryRow(ctx, `SELECT id FROM coordination_tasks WHERE run_id=$1 AND task_key=$2`, run.ID, plan.Tasks[0].ID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	bytes := []byte("Synthetic SQL receipt fixture; actual coding/Git validation is separate")
	patches := []execution.Patch{{RepositoryID: "repo-one", BaseCommit: plan.Repositories[0].Commit, BaseTree: strings.Repeat("a", 40), ResultTree: strings.Repeat("b", 40), Patch: bytes, Digest: execution.Sum(bytes), Paths: []string{"src/app.txt"}}}
	patchJSON, _ := json.Marshal(patches)
	zero := 0
	result := execution.Result{CleanupConfirmed: true, ProfileDigest: plan.Tasks[0].ProfileDigest, Image: plan.Tasks[0].Image, Adapter: "command/v1", AdapterVersion: "1", InputDigest: strings.Repeat("a", 64), Patches: patches, Producer: execution.Evidence{State: "passed", ExitCode: &zero, SourceDigest: strings.Repeat("a", 64), OutputDigest: execution.Sum(nil)}, Checks: []execution.Evidence{{ID: "test", RepositoryID: "repo-one", Argv: []string{"true"}, State: "passed", ExitCode: &zero, SourceDigest: execution.Sum(patchJSON), OutputDigest: execution.Sum(nil)}}}
	digest, _ := domain.JSONDigest(result)
	if _, err = p.pool.Exec(ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,'succeeded',$3,$4)`, taskID, run.ID, digest, result); err != nil {
		t.Fatal(err)
	}
	delivery, err := s.CreateDelivery(human, "runtime-delivery", domain.DeliveryInput{RunID: run.ID, TaskID: taskID, ArtifactDigest: digest, BaseBranch: "main", Title: "Synthetic runtime deployment"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeDelivery(human, delivery.ID, delivery.Digest); err != nil {
		t.Fatal(err)
	}
	op, err := p.ClaimDeliveryDispatch(ctx, strings.Repeat("d", 64), "default")
	if err != nil || op == nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	commit := strings.Repeat("e", 40)
	deployment := domain.ProviderDeployment{ID: "deployment", RepositoryID: "repo-one", ProviderProfile: domain.GitHubDeliveryProfile, Commit: commit, CommitRelation: "published_head", Environment: "production", State: "success", ProviderState: "success", ProviderUpdatedAt: now.Add(-2 * time.Minute), ObservedAt: now}
	observation := domain.DeliveryObservation{ProviderID: "123", Number: 1, URL: "https://github.com/synthetic/repo/pull/1", Commit: commit, Tree: delivery.ResultTree, State: "draft", Draft: true, Checks: []domain.ProviderCheck{}, ChecksState: "unknown", Deployment: "observed", Deployments: []domain.ProviderDeployment{deployment}, ProductionOutcome: "not_observed", ObservedAt: now}
	if _, err = p.CompleteDeliveryOperation(ctx, op.ID, op.Work.Binding, observation); err != nil {
		t.Fatal(err)
	}
	delivery, err = s.GetDelivery(human, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	config := domain.RuntimeIntegrationConfig{WorkspaceID: "workspace-one", RepositoryID: "repo-one", Environment: "production", Service: "synthetic", BackendID: "backend", CredentialID: "runtime", Profile: domain.GroundcoverProfile, MetricFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, RecordFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, Metrics: []string{"synthetic_latency"}, StepSeconds: 15, MaxAgeSeconds: 600, Enabled: true}
	if err = p.ApplyRuntimeIntegrations(ctx, "synthetic-operator", []domain.RuntimeIntegrationConfig{config}); err != nil {
		t.Fatal(err)
	}
	return ctx, p, s, domain.RuntimeInput{DeliveryID: delivery.ID, DeliveryDigest: delivery.Digest, ObservationSequence: delivery.Observation.Sequence, DeploymentID: "deployment", Commit: commit, Environment: "production", Start: now.Add(-60 * time.Second), End: now.Add(-30 * time.Second), Requirements: []domain.RuntimeRequirement{{ChangeID: pkg.ID, Revision: pkg.Revision.Number, Digest: pkg.Revision.Digest, CriterionID: "latency"}}}, config
}
func runtimeReceiptFixture(r domain.RuntimeEvidence) domain.RuntimeReceipt {
	now := time.Now().UTC()
	out := domain.RuntimeReceipt{Signals: []domain.RuntimeSignal{}, Evaluations: []domain.RuntimeEvaluation{}, ProductionOutcome: "not_verified", CollectedAt: now}
	for _, kind := range []string{"metrics", "logs", "traces"} {
		s := domain.RuntimeSignal{Kind: kind, State: "missing", Correlation: "not_established", Coverage: "empty_query_result", QueryDigest: strings.Repeat("a", 64), ResponseDigest: strings.Repeat("b", 64), Endpoint: "/api/" + kind + "/v2/search", CollectedAt: now, Series: []domain.RuntimeSeries{}, Records: []domain.RuntimeRecord{}}
		if kind == "metrics" {
			s.Metric = "synthetic_latency"
			s.Endpoint = "/api/metrics/query-range"
			s.State = "collected"
			s.Correlation = "exact"
			s.Coverage = "complete_query_grid"
			s.Series = []domain.RuntimeSeries{{Labels: map[string]string{"__name__": "synthetic_latency", "service": r.Target.Service, "env": r.Input.Environment, "commit": r.Input.Commit}, Points: []domain.RuntimePoint{{At: r.Input.Start, Value: 2}, {At: r.Input.Start.Add(15 * time.Second), Value: 3}, {At: r.Input.End, Value: 4}}}}
		}
		out.Signals = append(out.Signals, s)
	}
	out.Evaluations = runtimeevidence.Evaluate(r, out)
	out.Digest, _ = domain.RuntimeReceiptDigest(out)
	return out
}
func TestRuntimeSharedEvidenceCriteriaAndRevocation(t *testing.T) {
	ctx, p, s, input, _ := runtimeFixture(t)
	if _, err := p.RuntimeEvidence(ctx, strings.Repeat("a", 32)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("unscoped runtime read: %v", err)
	}
	agent := accessContext(ctx, "worker", "workspace-one", "repo-one")
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	r, err := s.CreateRuntimeEvidence(agent, "request", input)
	if err != nil {
		t.Fatal(err)
	}
	if r.RequesterID != "agent-worker" || len(r.Criteria) != 1 || r.Receipt != nil {
		t.Fatal("incorrect request authority")
	}
	same, err := s.CreateRuntimeEvidence(agent, "request", input)
	if err != nil || same.ID != r.ID {
		t.Fatal("idempotency failed")
	}
	changed := input
	changed.Requirements = nil
	if _, err = s.CreateRuntimeEvidence(agent, "request", changed); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("changed input %v", err)
	}
	op, err := p.ClaimRuntimeDispatch(ctx, strings.Repeat("a", 64), "default")
	if err != nil || op == nil || op.Work.Evidence.ID != r.ID {
		t.Fatalf("dispatch %v", err)
	}
	if _, err = p.CheckRuntimeWork(ctx, r.ID, op.Work.Binding); err != nil {
		t.Fatal(err)
	}
	receipt := runtimeReceiptFixture(r)
	if receipt.Evaluations[0].State != "met" {
		t.Fatal("explicit criteria not evaluated")
	}
	if _, err = p.CompleteRuntimeEvidence(ctx, r.ID, op.Work.Binding, receipt); err != nil {
		t.Fatal(err)
	}
	if err = p.FinishRuntimeDispatch(ctx, *op, "completed", "fixture-run", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRuntimeEvidence(human, r.ID)
	if err != nil || got.Receipt == nil || got.Receipt.Digest != receipt.Digest || got.Receipt.ProductionOutcome != "not_verified" {
		t.Fatalf("shared evidence %v", err)
	}
	page, err := s.ListRuntimeEvidence(human, "", 1)
	if err != nil || len(page.Evidence) != 1 {
		t.Fatal("missing shared discovery")
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-two", PrincipalID: "agent-worker", CanRead: false}, {RepositoryID: "repo-two", PrincipalID: "human-reviewer", CanRead: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.CheckRuntimeWork(ctx, r.ID, op.Work.Binding); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revocation permitted I/O %v", err)
	}
	if digest, err := p.CompleteRuntimeEvidence(ctx, r.ID, op.Work.Binding, receipt); err != nil || digest != receipt.Digest {
		t.Fatal("committed receipt lost after revocation")
	}
	if _, err = s.GetRuntimeEvidence(human, r.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross repository data exposed %v", err)
	}
	page, err = s.ListRuntimeEvidence(human, "", 1)
	if err != nil || len(page.Evidence) != 0 {
		t.Fatal("pagination leaked revoked source")
	}
}
func TestRuntimeFailsClosedForChangedPinsAndAuditRollback(t *testing.T) {
	ctx, p, s, input, config := runtimeFixture(t)
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	wrong := input
	wrong.Commit = strings.Repeat("f", 40)
	if _, err := s.CreateRuntimeEvidence(human, "wrong", wrong); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("wrong deployment commit %v", err)
	}
	r, err := s.CreateRuntimeEvidence(human, "request", input)
	if err != nil {
		t.Fatal(err)
	}
	op, err := p.ClaimRuntimeDispatch(ctx, strings.Repeat("a", 64), "default")
	if err != nil || op == nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION reject_runtime_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic'; END $$; CREATE TRIGGER reject_runtime_audit BEFORE INSERT ON runtime_audit_events FOR EACH ROW EXECUTE FUNCTION reject_runtime_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRuntimeEvidence(human, "rollback", input); err == nil {
		t.Fatal("request audit failure accepted")
	}
	var requests, outboxes int
	if err = p.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM runtime_evidence),(SELECT count(*) FROM runtime_outbox)`).Scan(&requests, &outboxes); err != nil || requests != 1 || outboxes != 1 {
		t.Fatal("request/outbox survived audit rollback")
	}
	if _, err = p.CompleteRuntimeEvidence(ctx, r.ID, op.Work.Binding, runtimeReceiptFixture(r)); err == nil {
		t.Fatal("audit failure accepted")
	}
	if digest, err := p.RuntimeOperationReceipt(ctx, r.ID, op.Work.Binding); err != nil || digest != "" {
		t.Fatal("receipt survived rollback")
	}
	if _, err = p.pool.Exec(ctx, `DROP TRIGGER reject_runtime_audit ON runtime_audit_events`); err != nil {
		t.Fatal(err)
	}
	config.Enabled = false
	if err = p.ApplyRuntimeIntegrations(ctx, "synthetic-operator", []domain.RuntimeIntegrationConfig{config}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.CheckRuntimeWork(ctx, r.ID, op.Work.Binding); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("disabled integration allowed %v", err)
	}
}

func TestRuntimeCriterionEditStopsQueuedCollection(t *testing.T) {
	ctx, p, s, input, _ := runtimeFixture(t)
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	r, err := s.CreateRuntimeEvidence(human, "request", input)
	if err != nil {
		t.Fatal(err)
	}
	op, err := p.ClaimRuntimeDispatch(ctx, strings.Repeat("a", 64), "default")
	if err != nil || op == nil {
		t.Fatal(err)
	}
	pin := input.Requirements[0]
	if _, err = s.Revise(accessContext(ctx, "author", "workspace-one", "repo-one"), pin.ChangeID, "ignored", pin.Revision, domain.Content{"intent": "Changed synthetic criterion"}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.CheckRuntimeWork(ctx, r.ID, op.Work.Binding); !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatalf("stale criterion permitted telemetry collection %v", err)
	}
	if _, err = s.GetRuntimeEvidence(human, r.ID); err != nil {
		t.Fatal("historical request lost after edit")
	}
}
