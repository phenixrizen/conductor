package acceptance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/trackerworker"
	"github.com/phenixrizen/conductor/pkg/client"
)

// Retained Git bytes and immutable receipt fixtures supply review data. Actual
// Docker execution and provider writes are verified in their separate suites.
func releaseTerminalFixture(t *testing.T) (*accessFixture, map[string]any) {
	f := newAccessFixtureWithIssuer(t, nil, nil, 4*time.Minute)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries(), f.verifier))
	configureCollectionIntegration(t, f, "team", "application", true)
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
	agent, err := client.NewAuthenticated(f.server.URL, f.tokens["agent"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	d, err := agent.CreateDelivery(f.ctx, "publication", input)
	if err != nil {
		t.Fatal(err)
	}
	planFor := func(title string) (domain.CoordinationPlan, domain.Package) {
		p := f.create("author", "team", "application", domain.Content{"intent": title})
		f.request("author", "team", "application", "POST", "/api/v1/changes/"+p.ID+"/review-requests", map[string]any{"revision": 1}, 200, nil)
		f.request("reviewer", "team", "application", "POST", "/api/v1/changes/"+p.ID+"/approvals", map[string]any{"revision": 1, "digest": p.Revision.Digest}, 201, nil)
		plan.Packages = []domain.PackagePin{{ChangeID: p.ID, RepositoryID: "application", Revision: 1, Digest: p.Revision.Digest}}
		plan.Tasks = []domain.CoordinationTask{{ID: "inspect", Perspective: "architect", Profile: profile.ID, ProfileDigest: profileDigest, Image: profile.Image, Prompt: "Inspect synthetic terminal source. \u001b]52;c;YQ==\u0007", Scopes: []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{"README.md"}}}, Checks: []domain.VerificationCommand{}, TimeoutSeconds: 30}}
		normalized, e := domain.NormalizeCoordinationPlan(plan)
		if e != nil {
			t.Fatal(e)
		}
		return normalized, p
	}
	stalePlan, stalePackage := planFor("Stale terminal execution plan")
	staleRun, err := agent.CreateCoordination(f.ctx, "terminal-stale-run", stalePlan)
	if err != nil {
		t.Fatal(err)
	}
	goodPlan, _ := planFor("Exact terminal execution plan")
	reportRun, err := agent.CreateCoordination(f.ctx, "terminal-failed-report", goodPlan)
	if err != nil {
		t.Fatal(err)
	}
	var reportTask string
	if err = f.sql.QueryRow(f.ctx, `SELECT id FROM coordination_tasks WHERE run_id=$1`, reportRun.ID).Scan(&reportTask); err != nil {
		t.Fatal(err)
	}
	report := execution.Result{InputDigest: strings.Repeat("d", 64), ProfileDigest: profileDigest, Image: profile.Image, Adapter: "command/v1", AdapterVersion: "1", Producer: execution.Evidence{State: "failed", Output: "Synthetic failed design report: inspect before revising. \u001b]52;c;YQ==\u0007"}, Patches: []execution.Patch{}, Checks: []execution.Evidence{}}
	reportDigest, _ := domain.JSONDigest(report)
	if _, err = f.sql.Exec(f.ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,'failed',$3,$4)`, reportTask, reportRun.ID, reportDigest, report); err != nil {
		t.Fatal(err)
	}
	reportRun, err = agent.GetCoordination(f.ctx, reportRun.ID)
	if err != nil {
		t.Fatal(err)
	}

	config := trackerConfig("linear")
	if err = f.db.ApplyTrackerConfig(f.ctx, "synthetic-operator", config); err != nil {
		t.Fatal(err)
	}
	trackerInput := domain.TrackerLinkInput{IssueID: trackerIssueID, Packages: []domain.TrackerPackageRef{{RepositoryID: "application", PackageID: pkg.ID, Revision: 1, Digest: pkg.Revision.Digest}}}
	link, err := c.CreateTrackerLink(f.ctx, "terminal-linked-ticket", trackerInput)
	if err != nil {
		t.Fatal(err)
	}
	_, factory := newTrackerProvider(t, config)
	activity, err := trackerworker.NewActivity(f.db, trackerCredentials, factory)
	if err != nil {
		t.Fatal(err)
	}
	runTrackerActivity(t, f, activity, link.LatestSyncID)
	link, err = c.GetTrackerLink(f.ctx, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, map[string]any{"reportRun": reportRun, "graph": graph, "graphInput": domain.GraphInput{Sources: []domain.GraphSource{{RepositoryID: "application", CollectionID: collection.ID, Digest: receipt.Digest, FullSourceDigest: data.Digest}}}, "staleRun": staleRun, "stalePackage": stalePackage, "plan": goodPlan, "delivery": d, "deliveryInput": input, "trackerLink": link, "trackerInput": trackerInput}
}

func TestAuthenticatedTerminalReleaseWorkflows(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TERMINAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TERMINAL=1 for full release PTY acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in PTY acceptance requires PostgreSQL")
	}
	f, setup := releaseTerminalFixture(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "conductor")
	buildCtx, cancel := context.WithTimeout(f.ctx, time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-race", "-o", binary, "./cmd/conductor")
	build.Dir = "../.."
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build CLI: %v\n%s", e, out)
	}
	credentials := map[string]string{}
	for name, token := range f.tokens {
		path := filepath.Join(dir, name+".token")
		if e := os.WriteFile(path, []byte(token+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
		credentials[name] = path
	}
	paths, _ := json.Marshal(credentials)
	data, _ := json.Marshal(setup)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || (r.URL.Path != "/revoke" && r.URL.Path != "/restore" && r.URL.Path != "/observe") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/observe" {
			d := setup["delivery"].(domain.Delivery)
			observation := domain.DeliveryObservation{ProviderID: "synthetic-publication", Number: 1, URL: "https://github.com/synthetic/application/pull/1", Commit: strings.Repeat("c", 40), Tree: d.ResultTree, State: "open", Draft: true, Checks: []domain.ProviderCheck{}, ChecksState: "unknown", Deployment: "unknown", ProductionOutcome: "unknown", ObservedAt: time.Now().UTC()}
			_, e := f.sql.Exec(r.Context(), `INSERT INTO delivery_observations(delivery_id,observation) VALUES($1,$2)`, d.ID, observation)
			if e != nil {
				t.Error(e)
				w.WriteHeader(500)
				return
			}
			_, _ = w.Write([]byte(`{}`))
			return
		}
		e := f.db.ApplyAccessConfig(r.Context(), "synthetic-terminal-operator", domain.AccessConfig{Memberships: []domain.MembershipConfig{{WorkspaceID: "team", PrincipalID: "person-reviewer", Active: r.URL.Path == "/restore"}}})
		if e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer control.Close()
	python := os.Getenv("CONDUCTOR_TERMINAL_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../terminal/release_review.py")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "CONDUCTOR_API_URL="+f.server.URL, "CONDUCTOR_BIN="+binary, "CONDUCTOR_TERMINAL_CREDENTIAL_FILES="+string(paths), "CONDUCTOR_TERMINAL_RELEASE_SETUP="+string(data), "CONDUCTOR_TERMINAL_CONTROL_URL="+control.URL)
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("release PTY acceptance: %v\n%s", e, output)
	}
	t.Log(string(output))
}

// The release selector leaves the existing local package workbench intact. This
// regression uses a separate local-mode API and disposable schema, never dev data.
func TestLocalTerminalReleaseRegression(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TERMINAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TERMINAL=1 for local PTY regression")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in terminal regression requires PostgreSQL")
	}
	f := newAccessFixtureWithIssuer(t, nil, nil, 2*time.Minute)
	binary := filepath.Join(t.TempDir(), "conductor")
	build := exec.CommandContext(f.ctx, "go", "build", "-race", "-o", binary, "./cmd/conductor")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build local CLI: %v\n%s", err, out)
	}
	python := os.Getenv("CONDUCTOR_TERMINAL_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../terminal/review.py")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CONDUCTOR_TOKEN=") && !strings.HasPrefix(entry, "CONDUCTOR_TOKEN_FILE=") && !strings.HasPrefix(entry, "CONDUCTOR_WORKSPACE=") && !strings.HasPrefix(entry, "CONDUCTOR_REPOSITORY_ID=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "PYTHONDONTWRITEBYTECODE=1", "CONDUCTOR_API_URL="+f.local.URL, "CONDUCTOR_BIN="+binary)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("local terminal regression: %v\n%s", err, out)
	}
	t.Log(string(out))
}
