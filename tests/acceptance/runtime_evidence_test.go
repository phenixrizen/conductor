package acceptance_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/mcpserver"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/pkg/client"
)

func TestAuthenticatedRuntimeEvidenceAndMCP(t *testing.T) {
	f := collectionFixture(t)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries().WithRuntimeEvidence(), f.verifier))
	runtimeInput, c := seedRuntimeInput(t, f, domain.Content{"intent": "Synthetic runtime evidence acceptance"})
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

func seedRuntimeInput(t *testing.T, f *accessFixture, content domain.Content) (domain.RuntimeInput, *client.Client) {
	t.Helper()
	input, c := seedDeliveryInputWithContent(t, f, []byte("Synthetic runtime browser fixture"), content)
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
	config := domain.RuntimeIntegrationConfig{WorkspaceID: "team", RepositoryID: "application", Environment: "production", Service: "synthetic", BackendID: "backend", CredentialID: "runtime", Profile: domain.GroundcoverProfile, MetricFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, RecordFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, Metrics: []string{"synthetic_latency"}, StepSeconds: 15, MaxAgeSeconds: 600, Enabled: true}
	if err = f.db.ApplyRuntimeIntegrations(f.ctx, "synthetic-operator", []domain.RuntimeIntegrationConfig{config}); err != nil {
		t.Fatal(err)
	}
	runtimeInput := domain.RuntimeInput{DeliveryID: d.ID, DeliveryDigest: d.Digest, ObservationSequence: d.Observation.Sequence, DeploymentID: deployment.ID, Commit: commit, Environment: "production", Start: now.Add(-time.Minute), End: now.Add(-30 * time.Second), Requirements: []domain.RuntimeRequirement{}}
	return runtimeInput, c
}
