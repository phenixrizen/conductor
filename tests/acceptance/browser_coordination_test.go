package acceptance_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/mcpserver"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/pkg/client"
)

// This combines a real MCP SDK client, signed API identity, shared PostgreSQL and
// Chromium human decisions. Task execution is deliberately not simulated here;
// the coordinator's separate Docker/Temporal suite verifies actual execution.
func TestBrowserCoordinatedRuns(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for coordinated workbench acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newConfiguredBrowserFixture(t, browserFixtureOptions{withUI: true, collections: true, coordination: true})
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination(), f.verifier))
	configureCollectionIntegration(t, f.accessFixture, "team", "application", true)
	profile := domain.ExecutionProfileConfig{WorkspaceID: "team", ID: "synthetic-offline", Image: "sha256:" + strings.Repeat("a", 64), Enabled: true, Profile: domain.WorkerProfile{Adapter: "command/v1", Command: []string{"/bin/true"}}}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{ExecutionProfiles: []domain.ExecutionProfileConfig{profile}, ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanExecute: true}, {RepositoryID: "application", PrincipalID: "person-agent", CanExecute: true}}}); err != nil {
		t.Fatal(err)
	}
	_, profileDigest, err := store.ValidatedExecutionProfile(profile.Profile)
	if err != nil {
		t.Fatal(err)
	}
	data := bundleFixture(t)
	var c domain.Collection
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", domain.CollectionInput{Commit: data.Commit, Paths: []string{"README.md"}, FullSource: true}, http.Header{"Idempotency-Key": {"coordination-browser-source"}}, 202, &c)
	var binding string
	if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, c.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	index := domain.CodeGraphIndex{Indexer: domain.CodeGraphIndexer, KernelVersion: "browser-unexecuted", Files: []domain.CodeGraphFile{}, Nodes: []domain.CodeGraphNode{}, Edges: []domain.CodeGraphEdge{}}
	for _, a := range data.Artifacts {
		index.Files = append(index.Files, domain.CodeGraphFile{Path: a.Path, State: "unsupported_or_deferred"})
	}
	receipt, err := f.db.CompleteCollectionWithSource(f.ctx, c.ID, binding, data.Artifacts[:1], data, index)
	if err != nil {
		t.Fatal(err)
	}
	var graph domain.RepositoryGraph
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/repository-graphs", domain.GraphInput{Sources: []domain.GraphSource{{RepositoryID: "application", CollectionID: c.ID, Digest: receipt.Digest, FullSourceDigest: data.Digest}}}, http.Header{"Idempotency-Key": {"coordination-browser-graph"}}, 201, &graph)
	planFor := func(title string) (domain.CoordinationPlan, domain.Package) {
		pkg := f.create("author", "team", "application", domain.Content{"intent": title})
		path := "/api/v1/changes/" + pkg.ID
		f.request("author", "team", "application", "POST", path+"/review-requests", map[string]any{"revision": 1}, 200, nil)
		f.request("reviewer", "team", "application", "POST", path+"/approvals", map[string]any{"revision": 1, "digest": pkg.Revision.Digest}, 201, nil)
		plan := domain.CoordinationPlan{SchemaVersion: 1, GraphID: graph.ID, GraphDigest: graph.Digest, Packages: []domain.PackagePin{{RepositoryID: "application", ChangeID: pkg.ID, Revision: 1, Digest: pkg.Revision.Digest}}, Repositories: []domain.CoordinationRepository{{RepositoryID: "application", Commit: data.Commit, CollectionID: c.ID, ReceiptDigest: receipt.Digest, FullSourceDigest: data.Digest}}, Tasks: []domain.CoordinationTask{{ID: "inspect", Perspective: "architect", Profile: profile.ID, ProfileDigest: profileDigest, Image: profile.Image, Prompt: "Inspect synthetic source. <script>window.untrustedExecuted=true</script>", DependsOn: []string{}, Scopes: []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{"README.md"}}}, Checks: []domain.VerificationCommand{{ID: "check", RepositoryID: "application", Argv: []string{"/bin/true"}, TimeoutSeconds: 30}}, TimeoutSeconds: 60}}, MaxParallel: 1}
		return plan, pkg
	}
	stalePlan, stalePackage := planFor("Shared MCP proposal inspected in browser")
	apiClient, err := client.NewAuthenticated(f.server.URL, f.tokens["agent"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := mcpserver.New(apiClient)
	if err != nil {
		t.Fatal(err)
	}
	left, right := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- bridge.Run(f.ctx, left) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "browser-shared-coordinator", Version: "1"}, nil).Connect(f.ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(); <-done })
	result, err := session.CallTool(f.ctx, &mcp.CallToolParams{Name: "conductor_propose_run", Arguments: map[string]any{"idempotencyKey": "mcp-browser-plan", "plan": stalePlan}})
	if err != nil || result.IsError {
		t.Fatalf("MCP proposal: %v %+v", err, result)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var envelope struct {
		Data domain.CoordinationRun `json:"data"`
	}
	if json.Unmarshal(encoded, &envelope) != nil || envelope.Data.ProposerID != "person-agent" {
		t.Fatal("MCP proposal lost server agent identity")
	}
	f.request("agent", "team", "application", "POST", "/api/v1/coordination-runs/"+envelope.Data.ID+"/authorization", map[string]any{"digest": envelope.Data.Digest}, 403, nil)
	plan, _ := planFor("Exact browser execution authorization")
	planJSON, _ := json.Marshal(plan)
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if err = os.WriteFile(planFile, planJSON, 0600); err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/coordination.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_RUN_ID="+envelope.Data.ID, "CONDUCTOR_BROWSER_RUN_DIGEST="+envelope.Data.Digest, "CONDUCTOR_BROWSER_STALE_PACKAGE="+stalePackage.ID, "CONDUCTOR_BROWSER_AUTHOR_TOKEN="+f.tokens["author"], "CONDUCTOR_BROWSER_PLAN_FILE="+planFile)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser coordinated acceptance: %v\n%s", err, output)
	}
	t.Log(string(output))
	var runs, authorized, cancelled int
	if err = f.sql.QueryRow(f.ctx, `SELECT count(*),count(a.run_id),count(c.run_id) FROM coordination_runs r LEFT JOIN coordination_authorizations a ON a.run_id=r.id LEFT JOIN coordination_cancellations c ON c.run_id=r.id WHERE r.proposer_id='person-reviewer'`).Scan(&runs, &authorized, &cancelled); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || authorized != 1 || cancelled != 1 {
		t.Fatalf("browser duplicated or lost exact decisions: %d/%d/%d", runs, authorized, cancelled)
	}
}
