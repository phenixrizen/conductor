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

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/codegraph"
	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/coordinationworker"
	"github.com/phenixrizen/conductor/internal/coordinationworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/pkg/client"
	enumspb "go.temporal.io/api/enums/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func coordinationAcceptance(t *testing.T, modes ...string) (*accessFixture, *coordinationworker.Activity, store.CoordinationWork) {
	t.Helper()
	if os.Getenv("CONDUCTOR_TEST_EXECUTION") != "1" || os.Getenv("CONDUCTOR_TEST_CODEGRAPH") != "1" {
		t.Skip("set CONDUCTOR_TEST_EXECUTION=1 and CONDUCTOR_TEST_CODEGRAPH=1 for actual Docker coordinated execution")
	}
	image := os.Getenv("CONDUCTOR_TEST_WORKER_IMAGE")
	if !domain.ExecutionImagePinned(image) {
		t.Fatal("immutable CONDUCTOR_TEST_WORKER_IMAGE required")
	}
	f := collectionFixture(t)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination(), f.verifier))
	config := domain.AccessConfig{Repositories: []domain.RepositoryConfig{{ID: "related", WorkspaceID: "team", Provider: "gitlab", Host: "gitlab.com", ProviderID: "101", Name: "synthetic/application"}}, Grants: []domain.GrantConfig{{RepositoryID: "related", PrincipalID: "person-author", CanRead: true, CanAuthor: true, CanApprove: true}, {RepositoryID: "related", PrincipalID: "person-reviewer", CanRead: true, CanAuthor: true, CanApprove: true}, {RepositoryID: "related", PrincipalID: "person-agent", CanRead: true, CanAuthor: true}}, ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanExecute: true}, {RepositoryID: "related", PrincipalID: "person-reviewer", CanExecute: true}}, ContextIntegrations: []domain.ContextIntegrationConfig{{WorkspaceID: "team", RepositoryID: "related", Profile: "gitlab-rest/v4-19.3", CredentialID: "synthetic-only", Enabled: true}}}
	profiles := []coordinationworker.ProfileConfig{
		{WorkspaceID: "team", ID: "first", Image: image, Profile: execution.Profile{Adapter: "command/v1", Command: []string{"sh", "-c", "printf '\\n// first agent\\n' >> fixture.go"}}},
		{WorkspaceID: "team", ID: "second", Image: image, Profile: execution.Profile{Adapter: "command/v1", Command: []string{"sh", "-c", "printf '\\nsecond agent\\n' >> README.md"}}},
		{WorkspaceID: "team", ID: "join", Image: image, Profile: execution.Profile{Adapter: "command/v1", Command: []string{"sh", "-c", "grep -q 'first agent' fixture.go && grep -q 'second agent' " + execution.RepositoryDirectory("related") + "/README.md && printf '\\n// joined related work\\n' >> fixture.go"}}},
	}
	if len(modes) > 0 && (modes[0] == "slow" || modes[0] == "crash") {
		profiles[0].Profile.Command = []string{"sh", "-c", "printf '// started\\n' >> fixture.go; sleep 30"}
	}
	for _, p := range profiles {
		config.ExecutionProfiles = append(config.ExecutionProfiles, domain.ExecutionProfileConfig{WorkspaceID: p.WorkspaceID, ID: p.ID, Image: p.Image, Profile: domain.WorkerProfile{Adapter: p.Profile.Adapter, Command: p.Profile.Command}, Enabled: true})
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", config); err != nil {
		t.Fatalf("provision execution authority: %v", err)
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatalf("resolve Docker: %v", err)
	}
	indexer, err := codegraph.New(docker, os.Getenv("CONDUCTOR_CODEGRAPH_IMAGE"))
	if err != nil {
		t.Fatalf("configure native CodeGraph: %v", err)
	}
	plan := domain.CoordinationPlan{SchemaVersion: 1, MaxParallel: 2}
	input := domain.GraphInput{}
	for _, repo := range []string{"application", "related"} {
		c, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", repo)
		if err != nil {
			t.Fatalf("create author client: %v", err)
		}
		reviewer, err := client.NewAuthenticated(f.server.URL, f.tokens["reviewer"], "team", repo)
		if err != nil {
			t.Fatalf("create reviewer client: %v", err)
		}
		source := bundleFixture(t)
		collection, err := c.CreateCollection(f.ctx, "coordinated-full-source", domain.CollectionInput{Commit: source.Commit, Paths: []string{"README.md"}, FullSource: true})
		if err != nil {
			t.Fatalf("request complete source: %v", err)
		}
		var binding string
		if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, collection.ID).Scan(&binding); err != nil {
			t.Fatalf("load collection binding: %v", err)
		}
		index, err := indexer.Index(f.ctx, source.Artifacts)
		if err != nil {
			t.Fatalf("run native graph extraction: %v", err)
		}
		receipt, err := f.db.CompleteCollectionWithSource(f.ctx, collection.ID, binding, source.Artifacts[:1], source, index)
		if err != nil {
			t.Fatalf("commit complete source: %v", err)
		}
		input.Sources = append(input.Sources, domain.GraphSource{RepositoryID: repo, CollectionID: collection.ID, Digest: receipt.Digest, FullSourceDigest: source.Digest})
		plan.Repositories = append(plan.Repositories, domain.CoordinationRepository{RepositoryID: repo, Commit: source.Commit, CollectionID: collection.ID, ReceiptDigest: receipt.Digest, FullSourceDigest: source.Digest})
		pkg, err := c.Create(f.ctx, domain.Content{"intent": "Synthetic governed cross repository change", "verificationCriteria": domain.VerificationCriteria{SchemaVersion: 1, Criteria: []domain.VerificationCriterion{{ID: "synthetic-output", Description: "The synthetic command observes its declared source change."}, {ID: "unlinked", Description: "A separate synthetic criterion has no declared check."}}}})
		if err != nil {
			t.Fatalf("create work package: %v", err)
		}
		if _, err = c.Submit(f.ctx, pkg.ID, pkg.Revision.Number); err != nil {
			t.Fatalf("submit work package: %v", err)
		}
		if _, err = reviewer.Approve(f.ctx, pkg.ID, pkg.Revision.Number, pkg.Revision.Digest); err != nil {
			t.Fatalf("approve work package: %v", err)
		}
		plan.Packages = append(plan.Packages, domain.PackagePin{ChangeID: pkg.ID, RepositoryID: repo, Revision: pkg.Revision.Number, Digest: pkg.Revision.Digest})
	}
	reviewer, err := client.NewAuthenticated(f.server.URL, f.tokens["reviewer"], "team", "application")
	if err != nil {
		t.Fatalf("create graph reviewer client: %v", err)
	}
	graph, err := reviewer.CreateRepositoryGraph(f.ctx, "coordinated-graph", input)
	if err != nil {
		t.Fatalf("retain inspected graph: %v", err)
	}
	plan.GraphID = graph.ID
	plan.GraphDigest = graph.Digest
	for _, p := range profiles {
		digest, _ := domain.JSONDigest(p.Profile)
		task := domain.CoordinationTask{ID: p.ID, Perspective: "developer", Profile: p.ID, ProfileDigest: digest, Image: p.Image, Prompt: "Synthetic authorized task prompt, private to activities", TimeoutSeconds: 60}
		if len(modes) > 0 && modes[0] == "crash" && p.ID == "first" {
			task.TimeoutSeconds = 15
		}
		switch p.ID {
		case "first":
			task.Scopes = []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{"fixture.go"}}}
			task.Checks = []domain.VerificationCommand{{ID: "first.check", RepositoryID: "application", Argv: []string{"sh", "-c", "grep -q 'first agent' fixture.go"}, TimeoutSeconds: 10}}
		case "second":
			task.Scopes = []domain.TaskScope{{RepositoryID: "related", WritablePaths: []string{"README.md"}}}
			task.Checks = []domain.VerificationCommand{{ID: "second.check", RepositoryID: "related", Argv: []string{"sh", "-c", "grep -q 'second agent' README.md"}, TimeoutSeconds: 10}}
		case "join":
			task.DependsOn = []string{"first", "second"}
			task.Scopes = []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{"fixture.go"}}, {RepositoryID: "related", WritablePaths: []string{}}}
			task.Checks = []domain.VerificationCommand{{ID: "join.application", RepositoryID: "application", Argv: []string{"sh", "-c", "grep -q 'joined related work' fixture.go && test -z \"$GITHUB_TOKEN$CONDUCTOR_TOKEN$ANTHROPIC_API_KEY\""}, TimeoutSeconds: 10}, {ID: "join.related", RepositoryID: "related", Argv: []string{"sh", "-c", "grep -q 'second agent' README.md"}, TimeoutSeconds: 10}}
		}
		for i := range task.Checks {
			for _, pin := range plan.Packages {
				if pin.RepositoryID == task.Checks[i].RepositoryID {
					task.Checks[i].Requirements = []domain.VerificationRequirement{{ChangeID: pin.ChangeID, Revision: pin.Revision, Digest: pin.Digest, CriterionID: "synthetic-output"}}
				}
			}
		}
		plan.Tasks = append(plan.Tasks, task)
	}
	agent, err := client.NewAuthenticated(f.server.URL, f.tokens["agent"], "team", "application")
	if err != nil {
		t.Fatalf("create proposing agent client: %v", err)
	}
	run, err := agent.CreateCoordination(f.ctx, "coordinated-plan", plan)
	if err != nil {
		t.Fatalf("propose exact plan: %v", err)
	}
	if _, err = agent.AuthorizeCoordination(f.ctx, run.ID, run.Digest); err == nil {
		t.Fatal("agent granted execution authority")
	}
	if _, err = reviewer.AuthorizeCoordination(f.ctx, run.ID, run.Digest); err != nil {
		t.Fatalf("authorize exact plan: %v", err)
	}
	var binding string
	if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM coordination_runs WHERE id=$1`, run.ID).Scan(&binding); err != nil {
		t.Fatalf("resolve trusted run binding: %v", err)
	}
	work, err := f.db.CoordinationWork(f.ctx, run.ID, binding)
	if err != nil {
		t.Fatalf("load trusted run: %v", err)
	}
	activity, err := coordinationworker.NewActivity(f.db, profiles, docker)
	if err != nil {
		t.Fatalf("configure activity: %v", err)
	}
	t.Setenv("GITHUB_TOKEN", "synthetic-must-not-cross")
	t.Setenv("CONDUCTOR_TOKEN", "synthetic-must-not-cross")
	t.Setenv("ANTHROPIC_API_KEY", "synthetic-must-not-cross")
	return f, activity, work
}

func TestCoordinatedDockerTasksShareCrossRepositoryArtifacts(t *testing.T) {
	f, activity, w := coordinationAcceptance(t)
	ref := coordinationworkflow.Reference{ID: w.Run.ID, Binding: w.Binding}
	plan, err := activity.Load(f.ctx, ref)
	if err != nil || len(plan.Tasks) != 3 {
		t.Fatalf("load: %v", err)
	}
	for _, key := range []string{"first", "second"} {
		input, err := f.db.CoordinationInput(f.ctx, w.Run.ID, w.Binding, w.TaskIDs[key])
		if err != nil {
			t.Fatalf("trusted input %s: %v", key, err)
		}
		if err = execution.ValidateRequest(input); err != nil {
			t.Fatalf("input %s: %v", key, err)
		}
	}
	done := make(chan error, 2)
	for _, key := range []string{"first", "second"} {
		go func(key string) {
			r, e := activity.Execute(f.ctx, coordinationworkflow.TaskReference{Run: ref, TaskID: w.TaskIDs[key]})
			if e == nil && r.Outcome != "succeeded" {
				e = domain.ErrConflict
			}
			done <- e
		}(key)
	}
	for range 2 {
		if err = <-done; err != nil {
			t.Fatalf("execute: %#v", err)
		}
	}
	joinRef := coordinationworkflow.TaskReference{Run: ref, TaskID: w.TaskIDs["join"]}
	r, err := activity.Execute(f.ctx, joinRef)
	if err != nil || r.Outcome != "succeeded" {
		t.Fatalf("dependent task: %+v %v", r, err)
	}
	artifact, _, err := f.db.CoordinationArtifact(f.ctx, ref.ID, ref.Binding, joinRef.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Patches) != 2 || len(artifact.Checks) != 2 || !artifact.CleanupConfirmed {
		t.Fatal("missing independent evidence or cumulative repository coverage")
	}
	artifactDigest, _ := domain.JSONDigest(artifact)
	viewer, err := client.NewAuthenticated(f.server.URL, f.tokens["reviewer"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := viewer.GetCoordinationArtifact(f.ctx, w.Run.ID, domain.CoordinationArtifactQuery{RunDigest: w.Run.Digest, TaskID: joinRef.TaskID, ArtifactDigest: artifactDigest})
	if err != nil || inspected.Verification == nil || len(inspected.Verification.Criteria) != 4 {
		t.Fatal("actual source-bound criterion review missing", err)
	}
	for _, criterion := range inspected.Verification.Criteria {
		want := "not_verified"
		if criterion.Requirement.CriterionID == "synthetic-output" {
			want = "supported"
		}
		if criterion.State != want {
			t.Fatal("actual check support crossed criterion boundary", criterion)
		}
	}
	for _, patch := range artifact.Patches {
		if patch.RepositoryID == "application" && (!bytes.Contains(patch.Patch, []byte("first agent")) || !bytes.Contains(patch.Patch, []byte("joined related work"))) {
			t.Fatal("descendant lost ancestor work")
		}
		if patch.RepositoryID == "related" && !bytes.Contains(patch.Patch, []byte("second agent")) {
			t.Fatal("related repository patch lost")
		}
	}
	if _, err = activity.Finalize(f.ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err = f.sql.Exec(f.ctx, `UPDATE execution_grants SET can_execute=false`); err != nil {
		t.Fatal(err)
	}
	again, err := activity.Execute(f.ctx, joinRef)
	if err != nil || again != r {
		t.Fatalf("committed redelivery after revocation: %+v %v", again, err)
	}
	var attempts, receipts int
	if err = f.sql.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coordination_task_attempts),(SELECT count(*) FROM coordination_task_receipts)`).Scan(&attempts, &receipts); err != nil || attempts != 3 || receipts != 3 {
		t.Fatalf("attempt duplication %d %d %v", attempts, receipts, err)
	}
}

func TestCoordinatedTemporalDockerExecutionAndRetainedHistory(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL=1 for owned coordinator workflow acceptance")
	}
	f, activity, w := coordinationAcceptance(t)
	retrying := &coordinationRetryActivities{Activity: activity, firstTask: w.TaskIDs["first"]}
	address := durableAddress(t)
	root := t.TempDir()
	process := durableStartTemporal(t, root, address, "coordination-temporal")
	defer func() { process.stop() }()
	ctx, cancel := context.WithTimeout(f.ctx, 120*time.Second)
	defer cancel()
	engine, err := durableEngine(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { engine.Close() }()
	resolve := func(ctx context.Context) (string, error) {
		return contextworkflow.RuntimeTarget(ctx, engine, durableNamespace, address, coordinationworkflow.TaskQueue)
	}
	target, err := resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contextworkflow.NewBoundRuntime(engine, durableNamespace, address, coordinationworkflow.TaskQueue, target)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = runtime.ForWorkflow(coordinationworkflow.WorkflowName)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := coordinationworker.NewDispatcher(f.db, runtime, durableNamespace, target, resolve)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- coordinationworkflow.RunWorker(workerCtx, engine, retrying) }()
	workerStopped := false
	defer func() {
		if !workerStopped {
			stopWorker()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	if claimed, err := dispatcher.Step(ctx); err != nil || !claimed {
		t.Fatalf("dispatch %v %v", claimed, err)
	}
	var result contextworkflow.Result
	if err = engine.GetWorkflow(ctx, coordinationworkflow.WorkflowName+"/"+w.Run.ID, "").Get(ctx, &result); err != nil {
		t.Fatal(err)
	}
	if result.ReceiptID != w.Run.ID || !domain.IsLowerHex(result.Digest, 64) {
		t.Fatal("aggregate receipt mismatch")
	}
	retrying.assertRecovered(t)
	if _, err = f.sql.Exec(ctx, `UPDATE coordination_execution_observations SET observed_at=clock_timestamp()-interval '10 seconds'`); err != nil {
		t.Fatal(err)
	}
	if err = dispatcher.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err = f.sql.QueryRow(ctx, `SELECT count(*) FROM coordination_claims WHERE released_at IS NULL`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("terminal confirmed tasks retained claims: %d %v", retained, err)
	}
	iterator := engine.GetWorkflowHistory(ctx, coordinationworkflow.WorkflowName+"/"+w.Run.ID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			t.Fatal(err)
		}
		data, err := protojson.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if json.Unmarshal(data, &decoded) != nil {
			t.Fatal("history JSON")
		}
		all := string(data)
		var inspect func(any)
		inspect = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, v := range x {
					if k == "data" {
						if text, ok := v.(string); ok {
							if b, err := base64.StdEncoding.DecodeString(text); err == nil {
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
		inspect(decoded)
		for _, secret := range []string{"Synthetic authorized task prompt", "first agent", "synthetic-must-not-cross", "fixture.go", "bundle", "private-retry-cause"} {
			if strings.Contains(all, secret) {
				t.Fatalf("source or commands entered history: %s", secret)
			}
		}
	}
	// Restart the actual owned Temporal process with retained SQLite state. A
	// completed run and PostgreSQL receipt remain the same, with no new producer.
	stopWorker()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	workerStopped = true
	engine.Close()
	process.stop()
	process = durableStartTemporal(t, root, address, "coordination-temporal-restarted")
	engine, err = durableEngine(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	var recovered contextworkflow.Result
	if err = engine.GetWorkflow(ctx, coordinationworkflow.WorkflowName+"/"+w.Run.ID, "").Get(ctx, &recovered); err != nil || recovered != result {
		t.Fatalf("actual Temporal restart receipt: %+v %v", recovered, err)
	}
	var count int
	if err = f.sql.QueryRow(ctx, `SELECT count(*) FROM coordination_task_attempts`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("restart repeated production: %d %v", count, err)
	}
}

func TestCoordinatedLostAttemptRecoveryCleansWithoutRerunning(t *testing.T) {
	f, activity, w := coordinationAcceptance(t)
	taskID := w.TaskIDs["first"]
	request, err := f.db.CoordinationInput(f.ctx, w.Run.ID, w.Binding, taskID)
	if err != nil {
		t.Fatal(err)
	}
	digest := execution.InputDigest(request)
	profile := w.Run.Plan.Tasks[0]
	if _, err = f.sql.Exec(f.ctx, `INSERT INTO coordination_task_attempts(task_id,run_id,input_digest,profile_digest,image,deadline) VALUES($1,$2,$3,$4,$5,clock_timestamp()-interval '1 second')`, taskID, w.Run.ID, digest, profile.ProfileDigest, profile.Image); err != nil {
		t.Fatal(err)
	}
	name := "conductor-task-" + digest[:32] + "-produce"
	command := exec.Command("docker", "run", "--detach", "--name", name, "--network", "none", "--entrypoint", "/bin/sleep", profile.Image, "60")
	if b, err := command.CombinedOutput(); err != nil {
		t.Fatalf("owned orphan: %v %s", err, b)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", name).Run() })
	target := strings.Repeat("a", 64)
	if _, err = f.db.ClaimCoordinationDispatch(f.ctx, target, "synthetic"); err != nil {
		t.Fatal(err)
	}
	if err = f.db.ObserveCoordinationExecution(f.ctx, w.Run.ID, w.Binding, target, "synthetic", coordinationworkflow.WorkflowName+"/"+w.Run.ID, "lost-worker-run", "failed"); err != nil {
		t.Fatal(err)
	}
	if err = activity.Recover(f.ctx); err != nil {
		t.Fatal(err)
	}
	latest, err := f.db.CoordinationWork(f.ctx, w.Run.ID, w.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Run.Receipts) != 3 {
		t.Fatal("missing recovery facts")
	}
	for _, r := range latest.Run.Receipts {
		if r.TaskID == taskID && r.Outcome != "unresolved" {
			t.Fatal("lost producer outcome was invented")
		}
		if r.ArtifactDigest != "" {
			t.Fatal("recovery invented a patch")
		}
	}
	var attempts, claims, cleanup int
	if err = f.sql.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coordination_task_attempts),(SELECT count(*) FROM coordination_claims WHERE released_at IS NULL),(SELECT count(*) FROM coordination_task_cleanup)`).Scan(&attempts, &claims, &cleanup); err != nil || attempts != 1 || claims != 2 || cleanup != 1 {
		t.Fatalf("recovery repeated producer or released unknown write: %d %d %d %v", attempts, claims, cleanup, err)
	}
	if b, err := exec.Command("docker", "ps", "--all", "--filter", "name=^/"+name+"$", "--format", "{{.ID}}").CombinedOutput(); err != nil || strings.TrimSpace(string(b)) != "" {
		t.Fatal("owned orphan still exists")
	}
	if err = activity.Recover(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatedRunningProducerStopsOnRelatedRepositoryRevocation(t *testing.T) {
	f, activity, w := coordinationAcceptance(t, "slow")
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	ref := coordinationworkflow.TaskReference{Run: coordinationworkflow.Reference{ID: w.Run.ID, Binding: w.Binding}, TaskID: w.TaskIDs["first"]}
	done := make(chan coordinationworkflow.TaskResult, 1)
	failures := make(chan error, 1)
	go func() {
		r, err := activity.Execute(ctx, ref)
		if err != nil {
			failures <- err
		} else {
			done <- r
		}
	}()
	var attempt *store.CoordinationAttempt
	for attempt == nil {
		var err error
		attempt, err = f.db.CoordinationAttempt(ctx, w.Run.ID, w.Binding, ref.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == nil {
			select {
			case <-ctx.Done():
				t.Fatal("producer was not admitted")
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	name := "conductor-task-" + attempt.InputDigest[:32] + "-produce"
	for {
		b, err := exec.CommandContext(ctx, "docker", "ps", "--filter", "name=^/"+name+"$", "--format", "{{.ID}}").Output()
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(b)) != "" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("producer never started")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if _, err := f.sql.Exec(ctx, `UPDATE execution_grants SET can_execute=false WHERE repository_id='related' AND principal_id='person-reviewer'`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failures:
		t.Fatalf("revoked execution: %#v", err)
	case r := <-done:
		if r.Outcome != "cancelled" {
			t.Fatalf("revoked outcome: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal("running producer ignored revocation")
	}
	var artifacts, cleanup int
	if err := f.sql.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coordination_task_receipts WHERE artifact_digest IS NOT NULL),(SELECT count(*) FROM coordination_task_cleanup)`).Scan(&artifacts, &cleanup); err != nil || artifacts != 0 || cleanup != 1 {
		t.Fatalf("revocation retained output or lost cleanup: %d %d %v", artifacts, cleanup, err)
	}
}

// This test kills and restarts the compiled executor while a real producer is
// running. It does not substitute an in-memory worker or a fabricated receipt.
func TestCoordinatedExecutorProcessCrashNeverRepeatsProducer(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL") != "1" || os.Getenv("CONDUCTOR_TEST_PROCESS_RESTART") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL=1 and CONDUCTOR_TEST_PROCESS_RESTART=1 for executor crash acceptance")
	}
	f, _, w := coordinationAcceptance(t, "crash")
	ctx, cancel := context.WithTimeout(f.ctx, 150*time.Second)
	defer cancel()
	address := durableAddress(t)
	temporalProcess := durableStartTemporal(t, t.TempDir(), address, "executor-crash-temporal")
	defer temporalProcess.stop()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "conductor-executor")
	runRestartCommand(t, ctx, 90*time.Second, root, nil, "go", "build", "-race", "-o", binary, "./cmd/conductor-executor")
	profiles := []coordinationworker.ProfileConfig{}
	for _, task := range w.Run.Plan.Tasks {
		var profile execution.Profile
		if err = f.sql.QueryRow(ctx, `SELECT configuration FROM execution_profiles WHERE workspace_id='team' AND id=$1`, task.Profile).Scan(&profile); err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, coordinationworker.ProfileConfig{WorkspaceID: "team", ID: task.Profile, Image: task.Image, Profile: profile})
	}
	data, err := json.Marshal(map[string]any{"profiles": profiles})
	if err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(t.TempDir(), "profiles.json")
	if err = os.WriteFile(catalog, data, 0600); err != nil {
		t.Fatal(err)
	}
	start := func() *exec.Cmd {
		command := exec.CommandContext(ctx, binary)
		command.Env = append(os.Environ(), "DATABASE_URL="+f.databaseURL, "CONDUCTOR_TEMPORAL_MODE=local", "CONDUCTOR_TEMPORAL_ADDRESS="+address, "CONDUCTOR_TEMPORAL_NAMESPACE="+durableNamespace, "CONDUCTOR_EXECUTION_PROFILES_FILE="+catalog)
		logFile, err := os.CreateTemp(t.TempDir(), "executor-*.log")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = logFile.Close() })
		command.Stdout = logFile
		command.Stderr = logFile
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		return command
	}
	command := start()
	defer func() {
		if command != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	var digest string
	for {
		err = f.sql.QueryRow(ctx, `SELECT a.input_digest FROM coordination_task_attempts a WHERE a.task_id=$1 AND EXISTS(SELECT 1 FROM coordination_task_receipts r WHERE r.task_id=$2 AND outcome='succeeded')`, w.TaskIDs["first"], w.TaskIDs["second"]).Scan(&digest)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("executor did not start durable producers")
		case <-time.After(30 * time.Millisecond):
		}
	}
	name := "conductor-task-" + digest[:32] + "-produce"
	if b, err := exec.CommandContext(ctx, "docker", "ps", "--filter", "name=^/"+name+"$", "--format", "{{.ID}}").Output(); err != nil || strings.TrimSpace(string(b)) == "" {
		t.Fatal("no running producer at crash boundary")
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	command = nil
	var attemptsBefore int
	if err = f.sql.QueryRow(ctx, `SELECT count(*) FROM coordination_task_attempts`).Scan(&attemptsBefore); err != nil {
		t.Fatal(err)
	}
	command = start()
	for {
		var outcome, observed string
		err = f.sql.QueryRow(ctx, `SELECT r.outcome,e.state FROM coordination_task_receipts r JOIN coordination_execution_observations e ON e.run_id=r.run_id WHERE r.task_id=$1`, w.TaskIDs["first"]).Scan(&outcome, &observed)
		if err == nil && outcome == "unresolved" && observed == "failed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("executor did not reconcile lost attempt: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	var attempts, cleanup, claims int
	if err = f.sql.QueryRow(ctx, `SELECT (SELECT count(*) FROM coordination_task_attempts),(SELECT count(*) FROM coordination_task_cleanup WHERE task_id=$1),(SELECT count(*) FROM coordination_claims WHERE released_at IS NULL)`, w.TaskIDs["first"]).Scan(&attempts, &cleanup, &claims); err != nil || attempts != attemptsBefore || cleanup != 1 || claims != 2 {
		t.Fatalf("process crash repeated producer or invented certainty: %d/%d %d %d %v", attempts, attemptsBefore, cleanup, claims, err)
	}
	if b, err := exec.CommandContext(ctx, "docker", "ps", "--all", "--filter", "name=^/"+name+"$", "--format", "{{.ID}}").Output(); err != nil || strings.TrimSpace(string(b)) != "" {
		t.Fatal("lost producer container survived recovery")
	}
}
