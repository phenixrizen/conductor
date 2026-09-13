package acceptance_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/runtimeevidence"
	"github.com/phenixrizen/conductor/internal/service"
)

// Chromium and signed sessions read actual PostgreSQL facts. Retained telemetry
// here is deliberately synthetic; real Groundcover HTTP decoding and Temporal
// sequencing are exercised by the provider and runtime-worker suites.
func TestBrowserRuntimeEvidence(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for runtime browser acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newConfiguredBrowserFixture(t, browserFixtureOptions{withUI: true, collections: true, coordination: true, deliveries: true, runtime: true})
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries().WithRuntimeEvidence(), f.verifier))
	configureCollectionIntegration(t, f.accessFixture, "team", "application", true)
	criteria := []domain.RuntimeCriterion{
		{ID: "latency", Metric: "synthetic_latency", Aggregation: "maximum", Operator: "lte", Threshold: 4, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 600},
		{ID: "strict-latency", Metric: "synthetic_latency", Aggregation: "maximum", Operator: "lte", Threshold: 1, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 600},
		{ID: "two-series", Metric: "synthetic_latency", Aggregation: "maximum", Operator: "lte", Threshold: 4, ExpectedSeries: 2, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 600},
	}
	input, c := seedRuntimeInput(t, f.accessFixture, domain.Content{"intent": "Synthetic approved runtime comparisons", "runtimeCriteria": domain.RuntimeCriteria{SchemaVersion: 1, Criteria: criteria}})
	delivery, err := c.GetDelivery(f.ctx, input.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.GetCoordination(f.ctx, delivery.Input.RunID)
	if err != nil {
		t.Fatal(err)
	}
	pin := run.Plan.Packages[0]
	for _, criterion := range criteria {
		input.Requirements = append(input.Requirements, domain.RuntimeRequirement{ChangeID: pin.ChangeID, Revision: pin.Revision, Digest: pin.Digest, CriterionID: criterion.ID})
	}
	r, err := c.CreateRuntimeEvidence(f.ctx, "browser-runtime-shared", input)
	if err != nil {
		t.Fatal(err)
	}
	op, err := f.db.ClaimRuntimeDispatch(f.ctx, strings.Repeat("a", 64), "default")
	if err != nil || op == nil {
		t.Fatalf("claim runtime: %v", err)
	}
	now := time.Now().UTC()
	receipt := domain.RuntimeReceipt{ProductionOutcome: "not_verified", CollectedAt: now}
	for _, kind := range []string{"metrics", "logs", "traces"} {
		s := domain.RuntimeSignal{Kind: kind, State: "truncated", Correlation: "not_established", Coverage: "truncated", QueryDigest: strings.Repeat("a", 64), ResponseDigest: strings.Repeat("b", 64), Endpoint: "/api/" + kind + "/v2/search", CollectedAt: now, Series: []domain.RuntimeSeries{}, Records: []domain.RuntimeRecord{}}
		if kind == "metrics" {
			s.Metric, s.Endpoint, s.State, s.Correlation, s.Coverage = "synthetic_latency", "/api/metrics/query-range", "collected", "exact", "complete_query_grid"
			s.Series = []domain.RuntimeSeries{{Labels: map[string]string{"__name__": s.Metric, "service": r.Target.Service, "env": input.Environment, "commit": input.Commit}, Points: []domain.RuntimePoint{{At: input.Start, Value: 2}, {At: input.Start.Add(15 * time.Second), Value: 3}, {At: input.End, Value: 4}}}}
		}
		if kind == "logs" {
			s.State, s.Correlation, s.Coverage = "collected", "exact", "bounded_query_result"
			data, _ := json.Marshal(map[string]string{"timestamp": input.Start.Format(time.RFC3339), "service": r.Target.Service, "env": input.Environment, "commit": input.Commit, "message": "Synthetic log <script>window.runtimeInjected=true</script> \x1b[2J \u202e"})
			s.Records = []domain.RuntimeRecord{{At: input.Start, Data: data}}
		}
		receipt.Signals = append(receipt.Signals, s)
	}
	receipt.Evaluations = runtimeevidence.Evaluate(r, receipt)
	receipt.Digest, err = domain.RuntimeReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.CompleteRuntimeEvidence(f.ctx, r.ID, op.Work.Binding, receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Evaluations[0].State != "met" || receipt.Evaluations[1].State != "not_met" || receipt.Evaluations[2].State != "not_verified" {
		t.Fatalf("unexpected fixture evaluations: %+v", receipt.Evaluations)
	}
	encoded, _ := json.Marshal(input)
	file := filepath.Join(t.TempDir(), "runtime-request.json")
	if err = os.WriteFile(file, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/runtime.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_RUNTIME_ID="+r.ID, "CONDUCTOR_BROWSER_RUNTIME_DIGEST="+r.Digest, "CONDUCTOR_BROWSER_RUNTIME_FILE="+file)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime browser: %v\n%s", err, output)
	}
	t.Log(string(output))
	var requests int
	if err = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM runtime_evidence`).Scan(&requests); err != nil || requests != 2 {
		t.Fatalf("uncertain request duplicated: %d %v", requests, err)
	}
}
