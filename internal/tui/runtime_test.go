package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/reviewinput"
	"github.com/phenixrizen/conductor/internal/runtimeevidence"
	"github.com/phenixrizen/conductor/pkg/client"
)

func runtimeTerminalFixture(t *testing.T) (releaseModel, domain.RuntimeEvidence, *[]releaseRequest) {
	m, _, calls := releaseFixture(t)
	m.view = "runtime"
	m.access.principal = domain.Principal{ID: "agent", Kind: "agent"}
	now := time.Now().UTC().Truncate(time.Second)
	input := domain.RuntimeInput{DeliveryID: strings.Repeat("a", 32), DeliveryDigest: strings.Repeat("b", 64), ObservationSequence: 1, DeploymentID: "deployment", Commit: strings.Repeat("c", 40), Environment: "production", Start: now.Add(-80 * time.Second), End: now.Add(-50 * time.Second), Requirements: []domain.RuntimeRequirement{}}
	fields := domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}
	r := domain.RuntimeEvidence{ID: strings.Repeat("d", 32), WorkspaceID: "team", RepositoryID: "application", RequesterID: "agent", Input: input, Target: domain.RuntimeTarget{WorkspaceID: "team", RepositoryID: "application", Environment: "production", Service: "synthetic", BackendID: "backend", Profile: domain.GroundcoverProfile, SourceCommit: domain.GroundcoverSourceCommit, IntegrationVersion: 1, MetricFields: fields, RecordFields: fields, Metrics: []string{"metric"}, StepSeconds: 15, MaxAgeSeconds: 60}, Deployment: domain.ProviderDeployment{ID: "deployment", RepositoryID: "application", Environment: "production", Commit: input.Commit, State: "success", ProviderUpdatedAt: input.Start.Add(-time.Minute)}, Criteria: []domain.RuntimeCriterionLink{}, CreatedAt: now, Freshness: "not_collected"}
	r.Digest, _ = domain.RuntimeRequestDigest(r)
	m.record = r
	m.inspectID = r.ID
	m.rebuildRelease()
	return m, r, calls
}
func TestRuntimeTerminalAgentCollectRetainsExactRetryAndClearsOnRevocation(t *testing.T) {
	m, r, calls := runtimeTerminalFixture(t)
	m.record = nil
	raw, _ := json.Marshal(map[string]any{"idempotencyKey": "captured-runtime", "input": r.Input})
	draft, err := reviewinput.Decode(raw, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	m.draft = &draft
	m, _ = releaseKey(m, "s")
	if m.prompt != "collect-runtime" {
		t.Fatal("agent read collection required human approval")
	}
	m, _ = releaseKey(m, "collect-runtime")
	m, _ = releaseKey(m, "enter")
	req := (*calls)[0]
	next, _ := m.Update(releaseResult{releaseRequest: req, err: context.DeadlineExceeded})
	m = next.(releaseModel)
	if !m.uncertain || m.draft == nil {
		t.Fatal("uncertain request lost")
	}
	m, _ = releaseKey(m, "s")
	m, _ = releaseKey(m, "collect-runtime")
	m, _ = releaseKey(m, "enter")
	retry := (*calls)[1]
	if req.draft.Digest != retry.draft.Digest || req.draft.IdempotencyKey != retry.draft.IdempotencyKey || string(req.draft.Input) != string(retry.draft.Input) {
		t.Fatal("retry refreshed captured pins")
	}
	next, _ = m.Update(releaseResult{releaseRequest: retry, err: &client.APIError{StatusCode: http.StatusForbidden}})
	m = next.(releaseModel)
	if m.access.ready || m.draft != nil || m.record != nil || m.presentation != nil || len(m.lines) > 0 {
		t.Fatal("revocation retained runtime source")
	}
}
func TestRuntimeTerminalHistoricalResultIsSeparateFromWindowAge(t *testing.T) {
	m, r, _ := runtimeTerminalFixture(t)
	requirement := domain.RuntimeRequirement{ChangeID: "package", Revision: 1, Digest: strings.Repeat("f", 64), CriterionID: "latency"}
	criterion := domain.RuntimeCriterion{ID: "latency", Metric: "metric", Aggregation: "maximum", Operator: "lte", Threshold: 5, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 60}
	digest, _ := domain.JSONDigest(criterion)
	r.Input.Requirements = []domain.RuntimeRequirement{requirement}
	r.Criteria = []domain.RuntimeCriterionLink{{Requirement: requirement, Criterion: criterion, CriterionDigest: digest}}
	r.Digest, _ = domain.RuntimeRequestDigest(r)
	receipt := domain.RuntimeReceipt{CollectedAt: r.CreatedAt, ProductionOutcome: "not_verified", Signals: []domain.RuntimeSignal{}, Evaluations: []domain.RuntimeEvaluation{}}
	for _, kind := range []string{"metrics", "logs", "traces"} {
		s := domain.RuntimeSignal{Kind: kind, State: "missing", Correlation: "not_established", Coverage: "empty_query_result", QueryDigest: strings.Repeat("a", 64), ResponseDigest: strings.Repeat("b", 64), Endpoint: "/api/" + kind + "/v2/search", CollectedAt: r.CreatedAt, Series: []domain.RuntimeSeries{}, Records: []domain.RuntimeRecord{}}
		if kind == "metrics" {
			s.Endpoint = "/api/metrics/query-range"
			s.Metric = "metric"
			s.State = "collected"
			s.Correlation = "exact"
			s.Coverage = "complete_query_grid"
			s.Series = []domain.RuntimeSeries{{Labels: map[string]string{"__name__": "metric", "service": "synthetic", "env": "production", "commit": r.Input.Commit}, Points: []domain.RuntimePoint{{At: r.Input.Start, Value: 1}, {At: r.Input.Start.Add(15 * time.Second), Value: 2}, {At: r.Input.End, Value: 3}}}}
		}
		receipt.Signals = append(receipt.Signals, s)
	}
	receipt.Evaluations = runtimeevidence.Evaluate(r, receipt)
	receipt.Digest, _ = domain.RuntimeReceiptDigest(receipt)
	r.Receipt = &receipt
	r.Freshness = "current"
	if m.validateRuntime(r) != nil || receipt.Evaluations[0].State != "met" {
		t.Fatal("valid historical evidence rejected")
	}
	if !strings.Contains(runtimeWindow(r, r.Input.End.Add(time.Minute+time.Second)), "stale") || !strings.Contains(runtimeSummary(r), "latency: met") {
		t.Fatal("window age overwrote historical criterion")
	}
	m.record = r
	m.rebuildRelease()
	if !strings.Contains(strings.Join(m.lines, "\n"), "Complete bounded request") || !strings.Contains(strings.Join(m.lines, "\n"), "maximum") {
		t.Fatal("complete policy/source absent")
	}
	r.RepositoryID = "other"
	r.Digest, _ = domain.RuntimeRequestDigest(r)
	if m.validateRuntime(r) == nil {
		t.Fatal("other repository accepted")
	}
}
