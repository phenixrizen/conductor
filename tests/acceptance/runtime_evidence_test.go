package acceptance_test

import (
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/mcpserver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/pkg/client"
)

func TestAuthenticatedRuntimeEvidenceAndMCP(t *testing.T) {
	f := collectionFixture(t)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries().WithRuntimeEvidence(), f.verifier))
	profile := domain.ExecutionProfileConfig{WorkspaceID: "team", ID: "synthetic", Image: "sha256:" + strings.Repeat("a", 64), Enabled: true, Profile: domain.WorkerProfile{Adapter: "command/v1", Command: []string{"/bin/true"}}}
	_, profileDigest, err := store.ValidatedExecutionProfile(profile.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{ExecutionProfiles: []domain.ExecutionProfileConfig{profile}, Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanRead: true, CanAuthor: true, CanApprove: true}}, ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanExecute: true, CanPublish: true}, {RepositoryID: "application", PrincipalID: "person-agent", CanExecute: true, CanPublish: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.ApplyDeliveryIntegrations(f.ctx, "synthetic-operator", []domain.DeliveryIntegrationConfig{{WorkspaceID: "team", RepositoryID: "application", Profile: domain.GitHubDeliveryProfile, Locator: "synthetic/application", CredentialID: "fixture-publication", BaseBranches: []string{"main"}, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["reviewer"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	data := bundleFixture(t)
	var collection domain.Collection
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", domain.CollectionInput{Commit: data.Commit, Paths: []string{"README.md"}, FullSource: true}, http.Header{"Idempotency-Key": {"delivery-source"}}, 202, &collection)
	var binding string
	if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, collection.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	// This fixture uses real retained Git bytes but explicitly marks native indexing
	// unexecuted. Native CodeGraph and coding execution have separate acceptance.
	index := domain.CodeGraphIndex{Indexer: domain.CodeGraphIndexer, KernelVersion: "publication-unexecuted", Files: []domain.CodeGraphFile{}, Nodes: []domain.CodeGraphNode{}, Edges: []domain.CodeGraphEdge{}}
	for _, a := range data.Artifacts {
		index.Files = append(index.Files, domain.CodeGraphFile{Path: a.Path, State: "unsupported_or_deferred"})
	}
	receipt, err := f.db.CompleteCollectionWithSource(f.ctx, collection.ID, binding, data.Artifacts[:1], data, index)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := c.CreateRepositoryGraph(f.ctx, "delivery-graph", domain.GraphInput{Sources: []domain.GraphSource{{RepositoryID: "application", CollectionID: collection.ID, Digest: receipt.Digest, FullSourceDigest: data.Digest}}})
	if err != nil {
		t.Fatal(err)
	}
	author, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := author.Create(f.ctx, domain.Content{"intent": "Synthetic publication acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = author.Submit(f.ctx, pkg.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Approve(f.ctx, pkg.ID, 1, pkg.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	check := domain.VerificationCommand{ID: "test", RepositoryID: "application", Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 10}
	plan := domain.CoordinationPlan{SchemaVersion: 1, GraphID: graph.ID, GraphDigest: graph.Digest, Packages: []domain.PackagePin{{ChangeID: pkg.ID, RepositoryID: "application", Revision: 1, Digest: pkg.Revision.Digest}}, Repositories: []domain.CoordinationRepository{{RepositoryID: "application", Commit: collection.Input.Commit, CollectionID: collection.ID, ReceiptDigest: receipt.Digest, FullSourceDigest: data.Digest}}, Tasks: []domain.CoordinationTask{{ID: "code", Perspective: "developer", Profile: profile.ID, ProfileDigest: profileDigest, Image: profile.Image, Prompt: "Fixture", Scopes: []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{"src"}}}, Checks: []domain.VerificationCommand{check}, TimeoutSeconds: 30}}, MaxParallel: 1}
	run, err := c.CreateCoordination(f.ctx, "delivery-run", plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.AuthorizeCoordination(f.ctx, run.ID, run.Digest); err != nil {
		t.Fatal(err)
	}
	var task string
	if err = f.sql.QueryRow(f.ctx, `SELECT id FROM coordination_tasks WHERE run_id=$1`, run.ID).Scan(&task); err != nil {
		t.Fatal(err)
	}
	patchBytes := []byte("synthetic fixture: worker execution is tested separately")
	patches := []execution.Patch{{RepositoryID: "application", BaseCommit: collection.Input.Commit, BaseTree: data.Tree, ResultTree: strings.Repeat("c", 40), Patch: patchBytes, Digest: execution.Sum(patchBytes), Paths: []string{"src/file.go"}}}
	patchJSON, _ := json.Marshal(patches)
	zero := 0
	artifact := execution.Result{CleanupConfirmed: true, ProfileDigest: profileDigest, Image: profile.Image, Adapter: "command/v1", AdapterVersion: "1", InputDigest: strings.Repeat("d", 64), Patches: patches, Producer: execution.Evidence{State: "passed", ExitCode: &zero, SourceDigest: strings.Repeat("d", 64), OutputDigest: execution.Sum(nil)}, Checks: []execution.Evidence{{ID: check.ID, RepositoryID: check.RepositoryID, Argv: check.Argv, State: "passed", ExitCode: &zero, SourceDigest: execution.Sum(patchJSON), OutputDigest: execution.Sum(nil)}}}
	digest, _ := domain.JSONDigest(artifact)
	if _, err = f.sql.Exec(f.ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,'succeeded',$3,$4)`, task, run.ID, digest, artifact); err != nil {
		t.Fatal(err)
	}
	input := domain.DeliveryInput{RunID: run.ID, TaskID: task, ArtifactDigest: digest, BaseBranch: "main", Title: "Synthetic verified change", Description: "Fixture"}

	d, err := c.CreateDelivery(f.ctx, "runtime-publish", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.AuthorizeDelivery(f.ctx, d.ID, d.Digest); err != nil {
		t.Fatal(err)
	}
	op, err := f.db.ClaimDeliveryDispatch(f.ctx, strings.Repeat("a", 64), "default")
	if err != nil || op == nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	commit := strings.Repeat("e", 40)
	deployment := domain.ProviderDeployment{ID: "deployment", RepositoryID: "application", ProviderProfile: domain.GitHubDeliveryProfile, Commit: commit, CommitRelation: "published_head", Environment: "production", State: "success", ProviderState: "success", ProviderUpdatedAt: now.Add(-2 * time.Minute), ObservedAt: now}
	observation := domain.DeliveryObservation{ProviderID: "123", Number: 1, URL: "https://github.com/synthetic/application/pull/1", Commit: commit, Tree: d.ResultTree, State: "draft", Draft: true, Checks: []domain.ProviderCheck{}, ChecksState: "unknown", Deployment: "observed", Deployments: []domain.ProviderDeployment{deployment}, ProductionOutcome: "not_observed", ObservedAt: now}
	if _, err = f.db.CompleteDeliveryOperation(f.ctx, op.ID, op.Work.Binding, observation); err != nil {
		t.Fatal(err)
	}
	d, err = c.GetDelivery(f.ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	config := domain.RuntimeIntegrationConfig{WorkspaceID: "team", RepositoryID: "application", Environment: "production", Service: "synthetic", BackendID: "backend", CredentialID: "runtime", Profile: domain.GroundcoverProfile, MetricFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, RecordFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, Metrics: []string{}, StepSeconds: 15, MaxAgeSeconds: 600, Enabled: true}
	if err = f.db.ApplyRuntimeIntegrations(f.ctx, "synthetic-operator", []domain.RuntimeIntegrationConfig{config}); err != nil {
		t.Fatal(err)
	}
	runtimeInput := domain.RuntimeInput{DeliveryID: d.ID, DeliveryDigest: d.Digest, ObservationSequence: d.Observation.Sequence, DeploymentID: deployment.ID, Commit: commit, Environment: "production", Start: now.Add(-time.Minute), End: now.Add(-30 * time.Second), Requirements: []domain.RuntimeRequirement{}}
	agent, err := client.NewAuthenticated(f.server.URL, f.tokens["agent"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := mcpserver.New(agent)
	if err != nil {
		t.Fatal(err)
	}
	left, right := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- bridge.Run(f.ctx, left) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "runtime-acceptance", Version: "1"}, nil).Connect(f.ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(); <-done })
	call, err := session.CallTool(f.ctx, &mcp.CallToolParams{Name: "conductor_collect_runtime_evidence", Arguments: map[string]any{"idempotencyKey": "captured-runtime", "input": runtimeInput}})
	if err != nil || call.IsError {
		t.Fatalf("MCP request %v %+v", err, call)
	}
	raw, _ := json.Marshal(call.StructuredContent)
	var envelope struct {
		Data domain.RuntimeEvidence `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Data.RequesterID != "person-agent" {
		t.Fatal("lost server identity")
	}
	r := envelope.Data
	got, err := c.GetRuntimeEvidence(f.ctx, r.ID)
	if err != nil || got.Digest != r.Digest || got.Receipt != nil {
		t.Fatal("shared inspection fabricated receipt")
	}
	page, err := c.ListRuntimeEvidence(f.ctx, "", 1)
	if err != nil || len(page.Evidence) != 1 {
		t.Fatal("missing shared request")
	}
	endpoints := []accessEndpoint{{"POST", "/api/v1/runtime-evidence", runtimeInput}, {"GET", "/api/v1/runtime-evidence", nil}, {"GET", "/api/v1/runtime-evidence/" + r.ID, nil}}
	for _, e := range endpoints {
		headers := http.Header{"Idempotency-Key": {"isolation"}}
		f.raw(f.server.URL, "forged.token", "team", "application", e.method, e.path, e.body, headers, 401, nil)
		headers.Set("X-Conductor-Actor", "forged")
		f.raw(f.server.URL, f.tokens["reviewer"], "team", "application", e.method, e.path, e.body, headers, 401, nil)
		f.localRequest("reviewer", e.method, e.path, e.body, 503, nil)
	}
	f.request("reviewer", "team", "private", "GET", "/api/v1/runtime-evidence/"+r.ID, nil, 404, nil)
	forged := map[string]any{}
	raw, _ = json.Marshal(runtimeInput)
	_ = json.Unmarshal(raw, &forged)
	forged["actor"] = "reviewer"
	f.raw(f.server.URL, f.tokens["agent"], "team", "application", "POST", "/api/v1/runtime-evidence", forged, http.Header{"Idempotency-Key": {"forged"}}, 400, nil)
	if err = f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanRead: false}}}); err != nil {
		t.Fatal(err)
	}
	f.request("reviewer", "team", "application", "GET", "/api/v1/runtime-evidence/"+r.ID, nil, 404, nil)
}
