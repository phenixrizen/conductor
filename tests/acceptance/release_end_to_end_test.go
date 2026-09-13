package acceptance_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/codegraph"
	"github.com/phenixrizen/conductor/internal/collectionworker"
	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/coordinationworker"
	"github.com/phenixrizen/conductor/internal/coordinationworkflow"
	"github.com/phenixrizen/conductor/internal/delivery"
	"github.com/phenixrizen/conductor/internal/deliveryworker"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/repositorycontext/remote"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/pkg/client"
	enumspb "go.temporal.io/api/enums/v1"
	temporalclient "go.temporal.io/sdk/client"
	"google.golang.org/protobuf/encoding/protojson"
)

// The release gate joins actual authenticated processes, PostgreSQL, Git,
// CodeGraph, Docker producers/checks and Temporal. Provider HTTP data and the
// command/v1 producers are explicitly synthetic: no paid model or live external
// write is performed, and a fixture deployment never claims this app deployed.
func TestFullReleaseCrossRepositoryWorkflow(t *testing.T) {

	if os.Getenv("CONDUCTOR_TEST_RELEASE") != "1" {
		t.Skip("set CONDUCTOR_TEST_RELEASE=1 for the owned full release gate")
	}
	for _, name := range []string{"CONDUCTOR_TEST_EXECUTION", "CONDUCTOR_TEST_CODEGRAPH", "CONDUCTOR_TEST_TEMPORAL"} {
		if os.Getenv(name) != "1" {
			t.Fatalf("opted-in release gate requires %s=1", name)
		}
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" || !domain.ExecutionImagePinned(os.Getenv("CONDUCTOR_TEST_WORKER_IMAGE")) {
		t.Fatal("release gate requires explicit test PostgreSQL and immutable worker image")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binaries := map[string]string{}
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelBuild()
	for _, name := range []string{"conductor-mcp", "conductor-executor"} {
		binary := filepath.Join(t.TempDir(), name)
		build := exec.CommandContext(buildCtx, "go", "build", "-race", "-o", binary, "./cmd/"+name)
		build.Dir = root
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v %s", name, err, out)
		}
		binaries[name] = binary
	}
	for _, tracker := range []string{"linear", "jira"} {
		t.Run(tracker, func(t *testing.T) { fullReleaseWorkflow(t, binaries, tracker) })
	}
}

type releaseMCP struct {
	t       *testing.T
	ctx     context.Context
	session *mcp.ClientSession
	stderr  *boundedRestartOutput
}

func newReleaseMCP(t *testing.T, f *accessFixture, binary, repo string) *releaseMCP {
	tokenFile := filepath.Join(t.TempDir(), "agent.token")
	if err := os.WriteFile(tokenFile, []byte(f.tokens["agent"]), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(f.ctx, binary)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CONDUCTOR_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "CONDUCTOR_URL="+f.server.URL, "CONDUCTOR_TOKEN_FILE="+tokenFile, "CONDUCTOR_WORKSPACE=team", "CONDUCTOR_REPOSITORY_ID="+repo)
	stderr := new(boundedRestartOutput)
	command.Stderr = stderr
	session, err := mcp.NewClient(&mcp.Implementation{Name: "conductor-full-release-fixture", Version: "1"}, nil).Connect(f.ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		if stderr.String() != "" {
			t.Error("stdio MCP leaked diagnostics")
		}
	})
	return &releaseMCP{t, f.ctx, session, stderr}
}
func (m *releaseMCP) call(name string, args any, target any) {
	m.t.Helper()
	r, err := m.session.CallTool(m.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil || r.IsError {
		m.t.Fatalf("MCP %s: %v %+v", name, err, r)
	}
	raw, _ := json.Marshal(r.StructuredContent)
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(envelope.Data, target) != nil {
		m.t.Fatal("MCP lost typed complete response")
	}
}
func (m *releaseMCP) denied(name string, args any) {
	m.t.Helper()
	r, err := m.session.CallTool(m.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err == nil && !r.IsError {
		m.t.Fatalf("MCP %s ignored scope denial", name)
	}
}
func releaseRuntime(t *testing.T, ctx context.Context, engine temporalclient.Client, address, queue, name string) (*contextworkflow.Runtime, string, func(context.Context) (string, error)) {
	t.Helper()
	resolve := func(ctx context.Context) (string, error) {
		return contextworkflow.RuntimeTarget(ctx, engine, durableNamespace, address, queue)
	}
	target, err := resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contextworkflow.NewBoundRuntime(engine, durableNamespace, address, queue, target)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = runtime.ForWorkflow(name)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, target, resolve
}
func releaseWorker(t *testing.T, ctx context.Context, run func(context.Context) error) func() {
	t.Helper()
	active, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- run(active) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(stop)
	return stop
}
func releaseClient(t *testing.T, f *accessFixture, actor, repo string) *client.Client {
	t.Helper()
	c, err := client.NewAuthenticated(f.server.URL, f.tokens[actor], "team", repo)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fullReleaseWorkflow(t *testing.T, binaries map[string]string, trackerProvider string) {
	f := newAccessFixtureWithIssuer(t, nil, nil, 6*time.Minute)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries().WithRuntimeEvidence(), f.verifier))
	image := os.Getenv("CONDUCTOR_TEST_WORKER_IMAGE")
	profiles := []coordinationworker.ProfileConfig{
		{WorkspaceID: "team", ID: "first", Image: image, Profile: execution.Profile{Adapter: "command/v1", Command: []string{"sh", "-c", "printf '\\n// first agent\\n' >> fixture.go"}}},
		{WorkspaceID: "team", ID: "second", Image: image, Profile: execution.Profile{Adapter: "command/v1", Command: []string{"sh", "-c", "printf '\\nsecond agent\\n' >> README.md"}}},
		{WorkspaceID: "team", ID: "join", Image: image, Profile: execution.Profile{Adapter: "command/v1", Command: []string{"sh", "-c", "grep -q 'first agent' fixture.go && grep -q 'second agent' " + execution.RepositoryDirectory("related") + "/README.md && printf '\\n// joined related work\\n' >> fixture.go"}}},
	}
	config := domain.AccessConfig{Repositories: []domain.RepositoryConfig{{ID: "related", WorkspaceID: "team", Provider: "gitlab", Host: "gitlab.com", ProviderID: "202", Name: "synthetic/related"}}}
	for _, repo := range []string{"application", "related"} {
		for _, actor := range []string{"author", "reviewer", "agent"} {
			config.Grants = append(config.Grants, domain.GrantConfig{RepositoryID: repo, PrincipalID: "person-" + actor, CanRead: true, CanAuthor: true, CanApprove: actor != "agent"})
		}
		config.ExecutionGrants = append(config.ExecutionGrants, domain.ExecutionGrantConfig{RepositoryID: repo, PrincipalID: "person-reviewer", CanExecute: true, CanPublish: true})
	}
	for _, profile := range profiles {
		config.ExecutionProfiles = append(config.ExecutionProfiles, domain.ExecutionProfileConfig{WorkspaceID: "team", ID: profile.ID, Image: profile.Image, Profile: domain.WorkerProfile{Adapter: profile.Profile.Adapter, Command: profile.Profile.Command}, Enabled: true})
	}
	config.ContextIntegrations = []domain.ContextIntegrationConfig{{WorkspaceID: "team", RepositoryID: "application", Profile: remote.GitHubProfile, Locator: "synthetic/application", CredentialID: "release-read", Enabled: true}, {WorkspaceID: "team", RepositoryID: "related", Profile: remote.GitLabProfile, CredentialID: "release-read", Enabled: true}}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-release-operator", config); err != nil {
		t.Fatal(err)
	}
	if err := f.db.ApplyDeliveryIntegrations(f.ctx, "synthetic-release-operator", []domain.DeliveryIntegrationConfig{{WorkspaceID: "team", RepositoryID: "application", Profile: domain.GitHubDeliveryProfile, Locator: "synthetic/application", CredentialID: "release-publication", BaseBranches: []string{"main"}, Enabled: true}, {WorkspaceID: "team", RepositoryID: "related", Profile: domain.GitLabDeliveryProfile, CredentialID: "release-publication", BaseBranches: []string{"main"}, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	repositories := []*releaseRepository{newReleaseRepository(t, f.ctx, "github", "application", "101"), newReleaseRepository(t, f.ctx, "gitlab", "related", "202")}
	origins := map[string]string{}
	for _, p := range repositories {
		origins[p.provider] = p.server.URL
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := codegraph.New(docker, os.Getenv("CONDUCTOR_CODEGRAPH_IMAGE"))
	if err != nil {
		t.Fatal(err)
	}
	collect, err := collectionworker.NewActivity(f.db, func(_ context.Context, i domain.ContextIntegration) (string, error) {
		if i.CredentialID != "release-read" {
			return "", domain.ErrForbidden
		}
		return releaseReadToken, nil
	}, func(c remote.Config) (collectionworker.Collector, error) {
		c.APIOrigin = origins[c.Binding.Provider]
		c.GitOrigin = c.APIOrigin
		c.AllowInsecureLoopback = true
		return remote.New(c)
	})
	if err != nil {
		t.Fatal(err)
	}
	collect = collect.WithCodeGraph(indexer)
	address := durableAddress(t)
	temporalDir := t.TempDir()
	process := durableStartTemporal(t, temporalDir, address, "release-temporal")
	defer func() { process.stop() }()
	engine, err := durableEngine(f.ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { engine.Close() }()
	runtime, target, resolve := releaseRuntime(t, f.ctx, engine, address, contextworkflow.TaskQueue, contextworkflow.WorkflowName)
	dispatcher, err := collectionworker.NewDispatcher(f.db, runtime, durableNamespace, target, resolve)
	if err != nil {
		t.Fatal(err)
	}
	stopCollection := releaseWorker(t, f.ctx, func(ctx context.Context) error {
		return contextworkflow.RunWorker(ctx, engine, contextworkflow.TaskQueue, collect.Collect)
	})
	graphInput := domain.GraphInput{}
	plan := domain.CoordinationPlan{SchemaVersion: 1, MaxParallel: 2}
	agents := map[string]*releaseMCP{}
	workflowIDs := []string{}
	criterion := domain.RuntimeCriterion{ID: "synthetic-latency", Metric: "synthetic_latency", Aggregation: "maximum", Operator: "lte", Threshold: 10, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 600}
	for _, repo := range repositories {
		agent := newReleaseMCP(t, f, binaries["conductor-mcp"], repo.repository)
		agents[repo.repository] = agent
		var collection domain.Collection
		agent.call("conductor_request_collection", map[string]any{"idempotencyKey": "release-full-source", "commit": repo.source.Commit, "paths": []string{"README.md"}, "fullSource": true}, &collection)
		if claimed, err := dispatcher.Step(f.ctx); err != nil || !claimed {
			t.Fatalf("source dispatch %v %v", claimed, err)
		}
		id := contextworkflow.WorkflowName + "/" + collection.ID
		workflowIDs = append(workflowIDs, id)
		var receipt contextworkflow.Result
		if err = engine.GetWorkflow(f.ctx, id, "").Get(f.ctx, &receipt); err != nil {
			t.Fatal(err)
		}
		agent.call("conductor_get_collection", map[string]any{"id": collection.ID}, &collection)
		if collection.Receipt == nil || collection.FullSource == nil || collection.FullSource.Commit != repo.source.Commit || collection.FullSource.Tree != repo.source.Tree {
			t.Fatal("complete source was not acquired")
		}
		repo.mu.Lock()
		requests := repo.gitRequests
		repo.mu.Unlock()
		if requests < 2 {
			t.Fatal("full source bypassed actual smart HTTP Git")
		}
		graphInput.Sources = append(graphInput.Sources, domain.GraphSource{RepositoryID: repo.repository, CollectionID: collection.ID, Digest: receipt.Digest, FullSourceDigest: collection.FullSource.Digest})
		plan.Repositories = append(plan.Repositories, domain.CoordinationRepository{RepositoryID: repo.repository, Commit: repo.source.Commit, CollectionID: collection.ID, ReceiptDigest: receipt.Digest, FullSourceDigest: collection.FullSource.Digest})
		var pkg domain.Package
		agent.call("conductor_create_package", map[string]any{"content": domain.Content{"intent": "Synthetic cross-repository release requirement", "runtimeCriteria": domain.RuntimeCriteria{SchemaVersion: 1, Criteria: []domain.RuntimeCriterion{criterion}}, "verificationCriteria": domain.VerificationCriteria{SchemaVersion: 1, Criteria: []domain.VerificationCriterion{{ID: "synthetic-agent-output", Description: "The synthetic repository retains its separately verified agent output."}, {ID: "unlinked", Description: "This separate synthetic criterion requires its own verification."}}}}}, &pkg)
		human := releaseClient(t, f, "reviewer", repo.repository)
		if _, err = human.Submit(f.ctx, pkg.ID, pkg.Revision.Number); err != nil {
			t.Fatal(err)
		}
		inspected, err := human.Get(f.ctx, pkg.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = human.Approve(f.ctx, pkg.ID, inspected.Revision.Number, inspected.Revision.Digest); err != nil {
			t.Fatal(err)
		}
		plan.Packages = append(plan.Packages, domain.PackagePin{ChangeID: pkg.ID, RepositoryID: repo.repository, Revision: pkg.Revision.Number, Digest: pkg.Revision.Digest})
	}
	stopCollection()
	var graph domain.RepositoryGraph
	agents["application"].call("conductor_create_graph", map[string]any{"idempotencyKey": "release-graph", "sources": graphInput.Sources}, &graph)
	found := map[string]bool{}
	for _, node := range graph.Snapshot.Nodes {
		if node.Name == "Caller" && node.Path == "fixture.go" {
			found[node.RepositoryID] = true
		}
	}
	if !found["application"] || !found["related"] {
		t.Fatal("native CodeGraph symbols absent in cross-repository graph")
	}
	nodeRepos := map[string]string{}
	for _, node := range graph.Snapshot.Nodes {
		nodeRepos[node.ID] = node.RepositoryID
	}
	depends := false
	for _, edge := range graph.Snapshot.Edges {
		if edge.Kind == "depends_on" && nodeRepos[edge.From] == "application" && nodeRepos[edge.To] == "related" {
			depends = true
		}
	}
	if !depends {
		t.Fatal("declared cross-repository dependency missing from complete-source graph")
	}

	source := graphInput.Sources[1]
	var sourceText domain.GraphArtifactResult
	agents["application"].call("conductor_read_graph_source", map[string]any{"id": graph.ID, "source": domain.GraphArtifactQuery{GraphDigest: graph.Digest, RepositoryID: source.RepositoryID, CollectionID: source.CollectionID, ReceiptDigest: source.Digest, FullSourceDigest: source.FullSourceDigest, Path: "fixture.go"}}, &sourceText)
	if sourceText.Artifact.Text == nil || !strings.Contains(*sourceText.Artifact.Text, "Caller") {
		t.Fatal("agent did not inspect related whole-source code")
	}
	plan.GraphID, plan.GraphDigest = graph.ID, graph.Digest
	for _, profile := range profiles {
		pd, _ := domain.JSONDigest(profile.Profile)
		task := domain.CoordinationTask{ID: profile.ID, Perspective: "developer", Profile: profile.ID, ProfileDigest: pd, Image: image, Prompt: "Synthetic private authorized release task", TimeoutSeconds: 60}
		switch profile.ID {
		case "first":
			task.Scopes = []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{"fixture.go"}}}
			task.Checks = []domain.VerificationCommand{{ID: "first", RepositoryID: "application", Argv: []string{"sh", "-c", "grep -q 'first agent' fixture.go"}, TimeoutSeconds: 10}}
		case "second":
			task.Scopes = []domain.TaskScope{{RepositoryID: "related", WritablePaths: []string{"README.md"}}}
			task.Checks = []domain.VerificationCommand{{ID: "second", RepositoryID: "related", Argv: []string{"sh", "-c", "grep -q 'second agent' README.md"}, TimeoutSeconds: 10}}
		case "join":
			task.DependsOn = []string{"first", "second"}
			task.Perspective = "qc"
			task.Scopes = []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{"fixture.go"}}, {RepositoryID: "related", WritablePaths: []string{}}}
			task.Checks = []domain.VerificationCommand{{ID: "joined", RepositoryID: "application", Argv: []string{"sh", "-c", "grep -q 'first agent' fixture.go && grep -q 'joined related work' fixture.go && test -z \"$GITHUB_TOKEN$CONDUCTOR_TOKEN$ANTHROPIC_API_KEY\""}, TimeoutSeconds: 10}, {ID: "related", RepositoryID: "related", Argv: []string{"sh", "-c", "grep -q 'second agent' README.md"}, TimeoutSeconds: 10}}
		}
		for i := range task.Checks {
			for _, pin := range plan.Packages {
				if pin.RepositoryID == task.Checks[i].RepositoryID {
					task.Checks[i].Requirements = []domain.VerificationRequirement{{ChangeID: pin.ChangeID, Revision: pin.Revision, Digest: pin.Digest, CriterionID: "synthetic-agent-output"}}
				}
			}
		}
		plan.Tasks = append(plan.Tasks, task)
	}
	var run, retry domain.CoordinationRun
	proposal := map[string]any{"idempotencyKey": "release-run", "plan": plan}
	agents["application"].call("conductor_propose_run", proposal, &run)
	agents["application"].call("conductor_propose_run", proposal, &retry)
	if run.ID != retry.ID || run.ProposerID != "person-agent" {
		t.Fatal("MCP proposal lost identity/idempotency")
	}
	agents["application"].denied("conductor_authorize_run", map[string]any{"id": run.ID, "digest": run.Digest})
	human := releaseClient(t, f, "reviewer", "application")
	inspectedRun, err := human.GetCoordination(f.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = human.AuthorizeCoordination(f.ctx, run.ID, inspectedRun.Digest); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(t.TempDir(), "profiles.json")
	raw, _ := json.Marshal(map[string]any{"profiles": profiles})
	if err = os.WriteFile(catalog, raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(f.ctx, binaries["conductor-executor"])
	command.Env = append(os.Environ(), "DATABASE_URL="+f.databaseURL, "CONDUCTOR_TEMPORAL_MODE=local", "CONDUCTOR_TEMPORAL_ADDRESS="+address, "CONDUCTOR_TEMPORAL_NAMESPACE="+durableNamespace, "CONDUCTOR_EXECUTION_PROFILES_FILE="+catalog, "GITHUB_TOKEN=synthetic-release-host-secret", "CONDUCTOR_TOKEN=synthetic-release-host-secret", "ANTHROPIC_API_KEY=synthetic-release-host-secret")
	executor := durableStartProcess(t, command, t.TempDir(), "release-executor")
	defer executor.stop()
	runWorkflow := coordinationworkflow.WorkflowName + "/" + run.ID
	workflowIDs = append(workflowIDs, runWorkflow)
	for {
		var started int
		err = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM coordination_task_attempts WHERE run_id=$1`, run.ID).Scan(&started)
		if err != nil {
			t.Fatal(err)
		}
		if started > 0 {
			break
		}
		select {
		case <-f.ctx.Done():
			t.Fatal("executor did not admit work")
		case <-time.After(40 * time.Millisecond):
		}
	}
	var aggregate contextworkflow.Result
	if err = engine.GetWorkflow(f.ctx, runWorkflow, "").Get(f.ctx, &aggregate); err != nil {
		t.Fatal(err)
	}
	agents["application"].call("conductor_get_run", map[string]any{"id": run.ID}, &run)
	if len(run.Receipts) != 3 {
		t.Fatal("actual task receipts incomplete")
	}
	var joined domain.TaskReceipt
	for _, receipt := range run.Receipts {
		if receipt.Outcome != "succeeded" {
			t.Fatal("worker did not pass its actual checks")
		}
		if receipt.TaskKey == "join" {
			joined = receipt
		}
	}
	var artifact domain.CoordinationArtifact
	agents["application"].call("conductor_get_task_artifact", map[string]any{"id": run.ID, "runDigest": run.Digest, "taskId": joined.TaskID, "artifactDigest": joined.ArtifactDigest}, &artifact)
	var result execution.Result
	if json.Unmarshal(artifact.Artifact, &result) != nil || len(result.Patches) != 2 || len(result.Checks) != 2 || !result.CleanupConfirmed {
		t.Fatal("actual complete source-bound worker evidence missing")
	}
	if artifact.Verification == nil || len(artifact.Verification.Criteria) != 4 || len(artifact.Verification.Gaps) != 0 {
		t.Fatal("MCP lost exact cross-repository criterion coverage")
	}
	for _, support := range artifact.Verification.Criteria {
		want := "not_verified"
		if support.Requirement.CriterionID == "synthetic-agent-output" {
			want = "supported"
		}
		if support.State != want {
			t.Fatal("full release artifact widened criterion support", support)
		}
	}
	for _, patch := range result.Patches {
		if patch.RepositoryID == "application" && (!bytes.Contains(patch.Patch, []byte("first agent")) || !bytes.Contains(patch.Patch, []byte("joined related work"))) {
			t.Fatal("descendant lost predecessor patch")
		}
	}
	for _, secret := range []string{releaseReadToken, releasePublishToken, trackerToken, releaseRuntimeToken, "synthetic-release-host-secret", f.tokens["agent"]} {
		if bytes.Contains(artifact.Artifact, []byte(secret)) {
			t.Fatal("credential crossed worker artifact boundary")
		}
	}
	// The compiled dispatcher must observe terminal Temporal history before
	// releasing claims; actual cleanup in the receipt alone is insufficient.
	releaseUntil := time.Now().Add(15 * time.Second)
	for {
		var claims int
		if err = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM coordination_claims WHERE run_id=$1 AND released_at IS NULL`, run.ID).Scan(&claims); err != nil {
			t.Fatal(err)
		}
		if claims == 0 {
			break
		}
		if time.Now().After(releaseUntil) {
			t.Fatal("executor did not reconcile terminal claims")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Stop the actual executor and restart the owned Temporal process with retained
	// SQLite. Continuing publication must preserve all original task receipts.
	executor.stop()
	engine.Close()
	process.stop()
	process = durableStartTemporal(t, temporalDir, address, "release-temporal-restarted")
	engine, err = durableEngine(f.ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	var recovered contextworkflow.Result
	if err = engine.GetWorkflow(f.ctx, runWorkflow, "").Get(f.ctx, &recovered); err != nil || recovered != aggregate {
		t.Fatal("Temporal process restart lost immutable receipt", err)
	}
	var attempts int
	if err = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM coordination_task_attempts WHERE run_id=$1`, run.ID).Scan(&attempts); err != nil || attempts != 3 {
		t.Fatal("restart repeated producers")
	}
	deliveries := []domain.Delivery{}
	publish, err := deliveryworker.NewActivity(f.db, f.db.LoadDeliverySource, func(_ context.Context, target domain.DeliveryTarget, id string) (string, error) {
		if id != "release-publication" {
			return "", domain.ErrForbidden
		}
		return releasePublishToken, nil
	}, func(target domain.DeliveryTarget, token string) (deliveryworker.Publisher, error) {
		return delivery.NewProvider(target, token, delivery.Options{Origin: origins[target.Provider], AllowInsecureLoopback: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	pubRuntime, pubTarget, pubResolve := releaseRuntime(t, f.ctx, engine, address, deliveryworker.TaskQueue, deliveryworker.WorkflowName)
	pubDispatcher, err := deliveryworker.NewDispatcher(f.db, pubRuntime, durableNamespace, pubTarget, pubResolve)
	if err != nil {
		t.Fatal(err)
	}
	stopPublication := releaseWorker(t, f.ctx, func(ctx context.Context) error { return deliveryworker.RunWorker(ctx, engine, publish.Publish) })
	defer stopPublication()
	for _, repo := range repositories {
		input := domain.DeliveryInput{RunID: run.ID, TaskID: joined.TaskID, ArtifactDigest: joined.ArtifactDigest, BaseBranch: "main", Title: "Synthetic governed cross-repository release", Description: "Controlled acceptance fixture; no live deployment claim."}
		var d domain.Delivery
		agents[repo.repository].call("conductor_propose_delivery", map[string]any{"idempotencyKey": "release-publication", "input": input}, &d)
		c := releaseClient(t, f, "reviewer", repo.repository)
		inspected, err := c.GetDeliveryArtifact(f.ctx, d.ID)
		if err != nil || inspected.ArtifactDigest != artifact.ArtifactDigest {
			t.Fatal("human publication did not inspect actual producer artifact", err)
		}
		repo.mu.Lock()
		repo.delivery = d
		repo.mu.Unlock()
		if _, err = c.AuthorizeDelivery(f.ctx, d.ID, d.Digest); err != nil {
			t.Fatal(err)
		}
		var operation string
		if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM delivery_outbox WHERE delivery_id=$1 AND operation='publish'`, d.ID).Scan(&operation); err != nil {
			t.Fatal(err)
		}
		if claimed, err := pubDispatcher.Step(f.ctx); err != nil || !claimed {
			t.Fatal("publication dispatch", err)
		}
		workflow := deliveryworker.WorkflowName + "/" + operation
		workflowIDs = append(workflowIDs, workflow)
		var receipt contextworkflow.Result
		if err = engine.GetWorkflow(f.ctx, workflow, "").Get(f.ctx, &receipt); err != nil {
			t.Fatal("publication recovery", err)
		}
		d, err = c.GetDelivery(f.ctx, d.ID)
		if err != nil || d.Receipt == nil || d.Observation == nil || d.Observation.State != "draft" || d.Observation.ChecksState != "unknown" || d.Observation.Deployment != "not_observed" {
			t.Fatalf("draft provider facts changed: %+v %v", d.Observation, err)
		}
		repo.mu.Lock()
		if repo.branches != 1 || repo.reviews != 1 || repo.dropReview || repo.posts != repo.postsAtDrop {
			t.Error("lost write acknowledgment duplicated provider branch/review")
		}
		actualTree := repo.git("", "rev-parse", repo.published+"^{tree}")
		repo.mu.Unlock()
		if actualTree != d.ResultTree {
			t.Fatal("published tree differs from verified cumulative artifact")
		}
		deliveries = append(deliveries, d)
	}
	releaseTrackerAndRuntime(t, f, engine, address, trackerProvider, agents["application"], plan, deliveries, repositories, pubDispatcher, &workflowIDs)
	for _, id := range workflowIDs {
		releasePrivateHistory(t, f.ctx, engine, id, f.tokens["agent"], releaseReadToken, releasePublishToken, trackerToken, releaseRuntimeToken, "Synthetic private authorized release task", "fixture.go", "go.mod", "first agent", "synthetic-release-host-secret")
	}
	t.Logf("PASS %s workspace: 2 actual smart-HTTP sources/native indexes, MCP-authored approved plan, 3 Docker producers/4 independent checks, retained Temporal restart, exact GitHub/GitLab draft trees with lost write acknowledgments, chosen tracker and correlated runtime evidence", trackerProvider)
}

func releasePrivateHistory(t *testing.T, ctx context.Context, engine temporalclient.Client, id string, forbidden ...string) {
	t.Helper()
	iterator := engine.GetWorkflowHistory(ctx, id, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	events := 0
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			t.Fatal(err)
		}
		events++
		if events > 512 {
			t.Fatal("release workflow history exceeded bound")
		}
		raw, err := protojson.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var data any
		if json.Unmarshal(raw, &data) != nil {
			t.Fatal("history JSON")
		}
		all := string(raw)
		var inspect func(any)
		inspect = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, v := range x {
					if k == "data" {
						if s, ok := v.(string); ok {
							if b, e := base64.StdEncoding.DecodeString(s); e == nil {
								all += "\n" + string(b)
							}
						}
					}
					inspect(v)
				}
			case []any:
				for _, v := range x {
					inspect(v)
				}
			}
		}
		inspect(data)
		for _, secret := range forbidden {
			if strings.Contains(all, secret) {
				t.Fatalf("private source or credential entered %s history", id)
			}
		}
	}
	if events == 0 {
		t.Fatal("missing retained workflow history")
	}
}

// An earlier operation may still need its terminal observation acknowledged.
// Drain the actual fenced dispatcher until this operation exists; never assume
// that one Step necessarily selected the newest requested operation.
func releaseDispatchUntilStarted(t *testing.T, ctx context.Context, engine temporalclient.Client, id string, step func(context.Context) (bool, error)) {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		if _, err := step(bounded); err != nil {
			t.Fatal("durable dispatch", err)
		}
		if _, err := engine.DescribeWorkflowExecution(bounded, id, ""); err == nil {
			return
		}
		select {
		case <-bounded.Done():
			t.Fatal("requested operation did not start")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
