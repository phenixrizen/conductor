package acceptance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/deliveryworker"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/runtimeevidence"
	"github.com/phenixrizen/conductor/internal/runtimeworker"
	"github.com/phenixrizen/conductor/internal/trackerworker"
	"github.com/phenixrizen/conductor/internal/trackerworkflow"
	temporalclient "go.temporal.io/sdk/client"
)

const releaseRuntimeToken = "synthetic-release-runtime-credential"

func releaseTrackerAndRuntime(t *testing.T, f *accessFixture, engine temporalclient.Client, address, provider string, agent *releaseMCP, plan domain.CoordinationPlan, deliveries []domain.Delivery, repositories []*releaseRepository, publisher *deliveryworker.Dispatcher, history *[]string) {
	t.Helper()
	config := trackerConfig(provider)
	if err := f.db.ApplyTrackerConfig(f.ctx, "synthetic-release-operator", config); err != nil {
		t.Fatal(err)
	}
	remote, factory := newTrackerProvider(t, config)
	activity, err := trackerworker.NewActivity(f.db, trackerCredentials, factory)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := trackerworker.NewDispatcher(f.ctx, f.db, engine, address, durableNamespace)
	if err != nil {
		t.Fatal(err)
	}
	stop := releaseWorker(t, f.ctx, func(ctx context.Context) error { return trackerworkflow.RunWorker(ctx, engine, activity.Sync) })
	defer stop()
	input := domain.TrackerLinkInput{IssueID: remote.issue}
	for _, pin := range plan.Packages {
		input.Packages = append(input.Packages, domain.TrackerPackageRef{RepositoryID: pin.RepositoryID, PackageID: pin.ChangeID, Revision: pin.Revision, Digest: pin.Digest})
	}
	for _, d := range deliveries {
		input.Publications = append(input.Publications, domain.TrackerPublicationRef{ID: d.ID, Digest: d.Receipt.Digest})
	}
	var link domain.TrackerLink
	agent.call("conductor_link_tracker_issue", map[string]any{"idempotencyKey": "release-tracker-link", "issueId": input.IssueID, "packages": input.Packages, "publications": input.Publications}, &link)
	runSync := func(id string) {
		t.Helper()
		if claimed, e := dispatcher.Step(f.ctx); e != nil || !claimed {
			t.Fatal("tracker dispatch", e)
		}
		workflow := trackerworkflow.WorkflowName + "/" + id
		*history = append(*history, workflow)
		var result trackerworkflow.Result
		if e := engine.GetWorkflow(f.ctx, workflow, "").Get(f.ctx, &result); e != nil {
			t.Fatal("tracker workflow", e)
		}
	}
	runSync(link.LatestSyncID)
	agent.call("conductor_get_tracker_link", map[string]any{"id": link.ID}, &link)
	if link.Observation == nil || link.Observation.Issue == nil || link.Observation.Issue.StatusName != "Done" || len(link.Publications) != 2 {
		t.Fatal("chosen tracker lost both exact publications")
	}
	remote.mu.Lock()
	remote.dropAfterWrite = true
	remote.mu.Unlock()
	var syncRequest domain.TrackerSync
	syncArgs := map[string]any{"id": link.ID, "idempotencyKey": "release-tracker-publish", "linkDigest": link.Digest, "mode": "publish"}
	if link.Observation.ProjectionDigest != "" {
		syncArgs["expectedProjectionDigest"] = link.Observation.ProjectionDigest
	}
	agent.call("conductor_sync_tracker_link", syncArgs, &syncRequest)
	runSync(syncRequest.ID)
	agent.call("conductor_get_tracker_sync", map[string]any{"id": syncRequest.ID}, &syncRequest)
	if syncRequest.Observation == nil || syncRequest.Observation.State != "synchronized" {
		t.Fatal("tracker lost-write reconciliation did not retain actual card observation")
	}
	remote.mu.Lock()
	writes := remote.writes
	projection := remote.projection
	remote.mu.Unlock()
	if writes != 1 || projection == nil || !strings.Contains(projection.Summary, "do not establish ticket completion") || !strings.Contains(projection.Summary, "github") || !strings.Contains(projection.Summary, "gitlab") {
		t.Fatal("tracker replayed card write or invented completion")
	}
	c := releaseClient(t, f, "reviewer", "application")
	d := deliveries[0]
	stillDraft, err := c.GetDelivery(f.ctx, d.ID)
	if err != nil || stillDraft.Observation.State != "draft" || stillDraft.Observation.ChecksState != "unknown" || stillDraft.Observation.ProductionOutcome != "not_observed" {
		t.Fatal("tracker Done status became execution, merge or production evidence")
	}
	// A fixture deployment is observed through the actual provider adapter; no SQL
	// fixture substitutes for provider receipt admission in this end-to-end path.
	repo := repositories[0]
	repo.mu.Lock()
	repo.deployed = true
	repo.deploymentAt = time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	repo.mu.Unlock()
	var reconciled domain.Delivery
	agent.call("conductor_reconcile_delivery", map[string]any{"id": d.ID, "digest": d.Digest, "idempotencyKey": "release-observe-deployment"}, &reconciled)
	var operation string
	if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM delivery_outbox WHERE delivery_id=$1 AND operation='reconcile'`, d.ID).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	releaseDispatchUntilStarted(t, f.ctx, engine, deliveryworker.WorkflowName+"/"+operation, publisher.Step)
	workflow := deliveryworker.WorkflowName + "/" + operation
	*history = append(*history, workflow)
	var result contextworkflow.Result
	if err = engine.GetWorkflow(f.ctx, workflow, "").Get(f.ctx, &result); err != nil {
		t.Fatal(err)
	}
	d, err = c.GetDelivery(f.ctx, d.ID)
	if err != nil || d.Observation == nil || d.Observation.Deployment != "observed" || len(d.Observation.Deployments) != 1 {
		t.Fatal("exact fixture deployment not observed", err)
	}
	target := domain.RuntimeIntegrationConfig{WorkspaceID: "team", RepositoryID: "application", Environment: "synthetic-test", Service: "synthetic-service", BackendID: "synthetic-backend", CredentialID: "release-runtime", Profile: domain.GroundcoverProfile, MetricFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, RecordFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, Metrics: []string{"synthetic_latency"}, StepSeconds: 15, MaxAgeSeconds: 600, Enabled: true}
	if err = f.db.ApplyRuntimeIntegrations(f.ctx, "synthetic-release-operator", []domain.RuntimeIntegrationConfig{target}); err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC().Add(-5 * time.Second).Truncate(time.Second)
	runtimeInput := domain.RuntimeInput{DeliveryID: d.ID, DeliveryDigest: d.Digest, ObservationSequence: d.Observation.Sequence, DeploymentID: d.Observation.Deployments[0].ID, Commit: d.Observation.Commit, Environment: target.Environment, Start: end.Add(-30 * time.Second), End: end, Requirements: []domain.RuntimeRequirement{{ChangeID: plan.Packages[0].ChangeID, Revision: plan.Packages[0].Revision, Digest: plan.Packages[0].Digest, CriterionID: "synthetic-latency"}}}
	var mu sync.Mutex
	wrongCommit := false
	calls := 0
	telemetry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+releaseRuntimeToken || r.Header.Get("X-Backend-Id") != target.BackendID {
			t.Error("runtime credential/backend changed")
			w.WriteHeader(401)
			return
		}
		var request map[string]any
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		if request["start"] != runtimeInput.Start.Format(time.RFC3339) || request["end"] != runtimeInput.End.Format(time.RFC3339) {
			t.Error("runtime time scope widened")
		}
		commit := runtimeInput.Commit
		if wrongCommit {
			commit = strings.Repeat("f", 40)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/metrics/query-range" {
			expected := "synthetic_latency{commit=\"" + runtimeInput.Commit + "\",env=\"synthetic-test\",service=\"synthetic-service\"}"
			if request["promql"] != expected {
				t.Error("runtime metric scope changed")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"promql": expected, "velocities": []any{map[string]any{"metric": map[string]string{"__name__": "synthetic_latency", "service": target.Service, "env": target.Environment, "commit": commit}, "velocity": [][]any{{runtimeInput.Start.Unix(), "2"}, {runtimeInput.Start.Add(15 * time.Second).Unix(), "4"}, {runtimeInput.End.Unix(), "3"}}}}})
			return
		}
		query, _ := request["query"].(string)
		if !strings.Contains(query, "service:=\"synthetic-service\" AND env:=\"synthetic-test\" AND commit:=\""+runtimeInput.Commit+"\"") || !strings.HasSuffix(query, "| limit 51") {
			t.Error("runtime record scope changed")
		}
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"timestamp": runtimeInput.End.Format(time.RFC3339), "service": target.Service, "env": target.Environment, "string_attributes": map[string]string{"commit": commit}, "content": "Synthetic correlated telemetry; not a production outcome claim"}})
	}))
	defer telemetry.Close()
	runtimeActivity, err := runtimeworker.NewActivity(f.db, func(_ context.Context, t domain.RuntimeTarget, id string) (string, error) {
		if id != "release-runtime" || t.BackendID != target.BackendID {
			return "", domain.ErrForbidden
		}
		return releaseRuntimeToken, nil
	}, func(token string, check func(context.Context) error) (runtimeworker.Collector, error) {
		return runtimeevidence.New(token, check, runtimeevidence.Options{Origin: telemetry.URL, AllowInsecureLoopback: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	rt, rtTarget, rtResolve := releaseRuntime(t, f.ctx, engine, address, runtimeworker.TaskQueue, runtimeworker.WorkflowName)
	rd, err := runtimeworker.NewDispatcher(f.db, rt, durableNamespace, rtTarget, rtResolve)
	if err != nil {
		t.Fatal(err)
	}
	stopRuntime := releaseWorker(t, f.ctx, func(ctx context.Context) error { return runtimeworker.RunWorker(ctx, engine, runtimeActivity.Collect) })
	defer stopRuntime()
	var retained domain.RuntimeEvidence
	for _, mode := range []string{"exact", "foreign"} {
		mu.Lock()
		wrongCommit = mode == "foreign"
		mu.Unlock()
		var evidence domain.RuntimeEvidence
		agent.call("conductor_collect_runtime_evidence", map[string]any{"idempotencyKey": "release-runtime-" + mode, "input": runtimeInput}, &evidence)
		releaseDispatchUntilStarted(t, f.ctx, engine, runtimeworker.WorkflowName+"/"+evidence.ID, rd.Step)
		wf := runtimeworker.WorkflowName + "/" + evidence.ID
		*history = append(*history, wf)
		if err = engine.GetWorkflow(f.ctx, wf, "").Get(f.ctx, &result); err != nil {
			t.Fatal("runtime collection", err)
		}
		agent.call("conductor_get_runtime_evidence", map[string]any{"id": evidence.ID}, &evidence)
		if evidence.Receipt == nil || evidence.Receipt.ProductionOutcome != "not_verified" || len(evidence.Receipt.Evaluations) != 1 {
			t.Fatal("runtime missing explicit criteria/output boundary")
		}
		evaluation := evidence.Receipt.Evaluations[0]
		if mode == "exact" {
			if evaluation.State != "met" || evaluation.Value == nil || *evaluation.Value != 4 {
				t.Fatal("complete exact correlated samples did not meet approved criterion")
			}
			retained = evidence
		} else if evaluation.State != "not_verified" || len(evidence.Receipt.Signals[0].Series) > 0 {
			t.Fatal("foreign commit telemetry established passing outcome")
		}
	}
	mu.Lock()
	count := calls
	mu.Unlock()
	if count != 6 {
		t.Fatal("runtime request bound changed")
	}
	unchanged, err := c.GetRuntimeEvidence(f.ctx, retained.ID)
	if err != nil || unchanged.Receipt.Digest != retained.Receipt.Digest {
		t.Fatal("later evidence replaced earlier immutable samples")
	}
	// Revoking only the related source repository blocks every aggregate view;
	// neither the selected application anchor nor the tracker status widens access.
	if err = f.db.ApplyAccessConfig(f.ctx, "synthetic-release-revocation", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "related", PrincipalID: "person-agent"}}}); err != nil {
		t.Fatal(err)
	}
	agent.denied("conductor_get_graph", map[string]any{"id": plan.GraphID})
	agent.denied("conductor_get_delivery_artifact", map[string]any{"id": d.ID})
	agent.denied("conductor_get_tracker_link", map[string]any{"id": link.ID})
	agent.denied("conductor_get_runtime_evidence", map[string]any{"id": retained.ID})
}
