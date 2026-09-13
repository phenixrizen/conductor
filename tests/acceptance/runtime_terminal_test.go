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
	"github.com/phenixrizen/conductor/internal/runtimeevidence"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/pkg/client"
)

// Use the same immutable delivery/package fixture as browser acceptance. The
// source and telemetry remain synthetic; this test qualifies terminal transport.
func runtimeEvidenceFixture(t *testing.T) (*accessFixture, domain.RuntimeInput) {
	t.Helper()
	f := newAccessFixtureWithIssuer(t, nil, nil, 3*time.Minute)
	configureCollectionIntegration(t, f, "team", "application", true)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries().WithRuntimeEvidence(), f.verifier))
	criteria := []domain.RuntimeCriterion{
		{ID: "latency", Metric: "synthetic_latency", Aggregation: "maximum", Operator: "lte", Threshold: 5, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 60},
		{ID: "minimum", Metric: "synthetic_latency", Aggregation: "minimum", Operator: "gte", Threshold: 10, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 60},
		{ID: "missing", Metric: "missing_metric", Aggregation: "maximum", Operator: "lte", Threshold: 5, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 60},
	}
	input, c := seedRuntimeInput(t, f, domain.Content{"intent": "Synthetic approved terminal runtime criteria", "runtimeCriteria": domain.RuntimeCriteria{SchemaVersion: 1, Criteria: criteria}})
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
	input.Start = input.Start.Add(-20 * time.Second)
	input.End = input.End.Add(-20 * time.Second)
	fields := domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}
	config := domain.RuntimeIntegrationConfig{WorkspaceID: "team", RepositoryID: "application", Environment: "production", Service: "synthetic", BackendID: "backend", CredentialID: "runtime", Profile: domain.GroundcoverProfile, MetricFields: fields, RecordFields: fields, Metrics: []string{"synthetic_latency", "missing_metric"}, StepSeconds: 15, MaxAgeSeconds: 60, Enabled: true}
	if err := f.db.ApplyRuntimeIntegrations(f.ctx, "synthetic-operator", []domain.RuntimeIntegrationConfig{config}); err != nil {
		t.Fatal(err)
	}
	return f, input
}

func TestAuthenticatedRuntimeTerminal(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TERMINAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TERMINAL=1 for actual runtime CLI/PTY acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in runtime terminal acceptance requires PostgreSQL")
	}
	f, input := runtimeEvidenceFixture(t)
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["reviewer"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.CreateRuntimeEvidence(f.ctx, "retained-terminal-runtime", input)
	if err != nil {
		t.Fatal(err)
	}
	receipt := domain.RuntimeReceipt{CollectedAt: time.Now().UTC(), ProductionOutcome: "not_verified", Signals: []domain.RuntimeSignal{}, Evaluations: []domain.RuntimeEvaluation{}}
	for _, metric := range r.Target.Metrics {
		s := domain.RuntimeSignal{Kind: "metrics", Metric: metric, State: "unavailable", Correlation: "not_established", Coverage: "unknown", QueryDigest: strings.Repeat("a", 64), Endpoint: "/api/metrics/query-range", CollectedAt: receipt.CollectedAt, Series: []domain.RuntimeSeries{}, Records: []domain.RuntimeRecord{}}
		if metric == "synthetic_latency" {
			s.State = "collected"
			s.Correlation = "exact"
			s.Coverage = "complete_query_grid"
			s.ResponseDigest = strings.Repeat("b", 64)
			s.Series = []domain.RuntimeSeries{{Labels: map[string]string{"__name__": metric, "service": "synthetic", "env": "production", "commit": input.Commit}, Points: []domain.RuntimePoint{{At: input.Start, Value: 1}, {At: input.Start.Add(15 * time.Second), Value: 2}, {At: input.End, Value: 3}}}}
		}
		receipt.Signals = append(receipt.Signals, s)
	}
	for _, kind := range []string{"logs", "traces"} {
		data, _ := json.Marshal(map[string]any{"timestamp": input.End.Format(time.RFC3339Nano), "service": "synthetic", "env": "production", "commit": input.Commit, "message": "Synthetic retained runtime source \x1b]52;c;YQ==\a " + strings.Repeat("bounded fixture ", 20) + "runtime-source-tail"})
		state, coverage := "collected", "bounded_query_result"
		if kind == "logs" {
			state = "truncated"
			coverage = "truncated"
		}
		receipt.Signals = append(receipt.Signals, domain.RuntimeSignal{Kind: kind, State: state, Correlation: "exact", Coverage: coverage, QueryDigest: strings.Repeat("a", 64), ResponseDigest: strings.Repeat("b", 64), Endpoint: "/api/" + kind + "/v2/search", CollectedAt: receipt.CollectedAt, Series: []domain.RuntimeSeries{}, Records: []domain.RuntimeRecord{{At: input.End, Data: data}}})
	}
	receipt.Evaluations = runtimeevidence.Evaluate(r, receipt)
	receipt.Digest, _ = domain.RuntimeReceiptDigest(receipt)
	seen := map[string]bool{}
	for _, e := range receipt.Evaluations {
		seen[e.State] = true
	}
	if !seen["met"] || !seen["not_met"] || !seen["not_verified"] {
		t.Fatal("fixture failed to retain distinct historical states", receipt.Evaluations)
	}
	var binding string
	if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM runtime_evidence WHERE id=$1`, r.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.CompleteRuntimeEvidence(f.ctx, r.ID, binding, receipt); err != nil {
		t.Fatal(err)
	}
	r, err = c.GetRuntimeEvidence(f.ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, e := domain.RuntimeReceiptDigest(*r.Receipt); e != nil || got != r.Receipt.Digest {
		t.Fatalf("retained receipt digest changed after PostgreSQL: %s != %s (%v)", got, r.Receipt.Digest, e)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "conductor")
	buildCtx, cancel := context.WithTimeout(f.ctx, time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-race", "-o", binary, "./cmd/conductor")
	build.Dir = "../.."
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("CLI build: %v %s", e, out)
	}
	paths := map[string]string{}
	for name, token := range f.tokens {
		path := filepath.Join(dir, name+".token")
		if err = os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}
	credentials, _ := json.Marshal(paths)
	setup, _ := json.Marshal(map[string]any{"record": r, "input": input})
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != "POST" || (request.URL.Path != "/revoke" && request.URL.Path != "/restore") {
			http.NotFound(w, request)
			return
		}
		enabled := request.URL.Path == "/restore"
		if err := f.db.ApplyAccessConfig(request.Context(), "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanRead: enabled, CanAuthor: enabled, CanApprove: enabled}}}); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer control.Close()
	python := os.Getenv("CONDUCTOR_TERMINAL_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../terminal/runtime_review.py")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "CONDUCTOR_API_URL="+f.server.URL, "CONDUCTOR_BIN="+binary, "CONDUCTOR_TERMINAL_CREDENTIAL_FILES="+string(credentials), "CONDUCTOR_TERMINAL_RUNTIME_SETUP="+string(setup), "CONDUCTOR_TERMINAL_CONTROL_URL="+control.URL)
	out, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("runtime CLI/PTY: %v\n%s", e, out)
	}
	t.Log(string(out))
}
