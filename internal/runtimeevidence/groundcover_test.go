package runtimeevidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func requestFixture() domain.RuntimeEvidence {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-60 * time.Second)
	end := now.Add(-30 * time.Second)
	target := domain.RuntimeTarget{WorkspaceID: "workspace", RepositoryID: "repo", Environment: "production", Service: "synthetic-service", BackendID: "backend", Profile: domain.GroundcoverProfile, SourceCommit: domain.GroundcoverSourceCommit, IntegrationVersion: 1, MetricFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, RecordFields: domain.RuntimeFields{Service: "service", Environment: "env", Commit: "commit"}, Metrics: []string{"synthetic_latency"}, StepSeconds: 15, MaxAgeSeconds: 600}
	input := domain.RuntimeInput{Commit: strings.Repeat("a", 40), Environment: "production", Start: start, End: end}
	criterion := domain.RuntimeCriterion{ID: "latency", Metric: "synthetic_latency", Aggregation: "maximum", Operator: "lte", Threshold: 10, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 600}
	digest, _ := domain.JSONDigest(criterion)
	return domain.RuntimeEvidence{ID: strings.Repeat("b", 32), CreatedAt: now.Add(-2 * time.Second), Input: input, Target: target, Deployment: domain.ProviderDeployment{State: "success", Commit: input.Commit, Environment: input.Environment, ProviderUpdatedAt: start.Add(-time.Second)}, Criteria: []domain.RuntimeCriterionLink{{Requirement: domain.RuntimeRequirement{ChangeID: strings.Repeat("c", 32), Revision: 1, Digest: strings.Repeat("d", 64), CriterionID: "latency"}, Criterion: criterion, CriterionDigest: digest}}}
}
func fixtureCollector(t *testing.T, handler http.Handler, check func(context.Context) error) *Collector {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(strings.Repeat("s", 32), check)
	if err != nil {
		t.Fatal(err)
	}
	transport := srv.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = srv.Certificate().DNSNames[0]
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}
	transport.DisableKeepAlives = true
	c.client.Transport = transport
	return c
}
func TestGroundcoverScopedQueriesAndExplicitCriteria(t *testing.T) {
	r := requestFixture()
	calls := 0
	collector := fixtureCollector(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method != "POST" || req.Host != Host || req.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) || req.Header.Get("X-Backend-Id") != "backend" {
			t.Error("credential or endpoint binding changed")
		}
		var body map[string]any
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			t.Error("invalid query")
		}
		if body["start"] != r.Input.Start.Format(time.RFC3339) || body["end"] != r.Input.End.Format(time.RFC3339) {
			t.Error("query window changed")
		}
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/api/metrics/query-range" {
			expected := "synthetic_latency{commit=\"" + r.Input.Commit + "\",env=\"production\",service=\"synthetic-service\"}"
			if body["promql"] != expected {
				t.Error("metric filters widened")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"promql": expected, "velocities": []any{map[string]any{"metric": map[string]string{"__name__": "synthetic_latency", "service": "synthetic-service", "env": "production", "commit": r.Input.Commit}, "velocity": [][]any{{r.Input.Start.Unix(), "2"}, {r.Input.Start.Add(15 * time.Second).Unix(), "4"}, {r.Input.End.Unix(), "3"}}}}})
			return
		}
		query, _ := body["query"].(string)
		if !strings.Contains(query, "service:=\"synthetic-service\" AND env:=\"production\" AND commit:=\"") || !strings.HasSuffix(query, "| limit 51") {
			t.Error("gcQL filter or limit changed")
		}
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"timestamp": r.Input.End.Format(time.RFC3339), "service": "synthetic-service", "env": "production", "string_attributes": map[string]string{"commit": r.Input.Commit}, "content": "untrusted <script> and \u001b[31m data"}})
	}), func(context.Context) error { return nil })
	receipt, err := collector.Collect(context.Background(), r)
	if err != nil || calls != 3 {
		t.Fatalf("collect calls=%d err=%v", calls, err)
	}
	if len(receipt.Signals) != 3 || receipt.Evaluations[0].State != "met" || receipt.ProductionOutcome != "not_verified" {
		t.Fatalf("evidence %+v", receipt)
	}
	if err = ValidateReceipt(r, receipt, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, s := range receipt.Signals {
		if s.State != "collected" || s.Correlation != "exact" || s.ResponseDigest == "" {
			t.Fatal("missing correlation provenance")
		}
	}
}
func TestGroundcoverMissingTruncatedUncorrelatedAndUnavailableNeverPass(t *testing.T) {
	for _, mode := range []string{"empty", "wrong_commit", "missing_commit", "partial_grid", "truncated", "provider_error", "nan", "wrong_time"} {
		t.Run(mode, func(t *testing.T) {
			r := requestFixture()
			c := fixtureCollector(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(req.Body).Decode(&body)
				if req.URL.Path != "/api/metrics/query-range" {
					fmt.Fprint(w, "[]")
					return
				}
				if mode == "provider_error" {
					w.WriteHeader(503)
					fmt.Fprint(w, "secret provider diagnostic")
					return
				}
				labels := map[string]string{"__name__": "synthetic_latency", "service": r.Target.Service, "env": r.Input.Environment, "commit": r.Input.Commit}
				if mode == "wrong_commit" {
					labels["commit"] = strings.Repeat("b", 40)
				}
				if mode == "missing_commit" {
					delete(labels, "commit")
				}
				points := [][]any{{r.Input.Start.Unix(), "2"}, {r.Input.Start.Add(15 * time.Second).Unix(), "4"}, {r.Input.End.Unix(), "3"}}
				if mode == "partial_grid" {
					points = points[:2]
				}
				if mode == "nan" {
					points[0][1] = "NaN"
				}
				if mode == "wrong_time" {
					points[0][0] = r.Input.Start.Add(-time.Second).Unix()
				}
				series := []any{map[string]any{"metric": labels, "velocity": points}}
				if mode == "empty" {
					series = []any{}
				}
				if mode == "truncated" {
					for len(series) < 9 {
						series = append(series, series[0])
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"promql": body["promql"], "velocities": series})
			}), func(context.Context) error { return nil })
			receipt, err := c.Collect(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Evaluations[0].State != "not_verified" {
				t.Fatal("incomplete evidence passed")
			}
			if mode == "wrong_commit" && len(receipt.Signals[0].Series) > 0 {
				t.Fatal("foreign telemetry leaked")
			}
		})
	}
}
func TestGroundcoverRevocationCancellationAndRedirect(t *testing.T) {
	r := requestFixture()
	calls, checks := 0, 0
	c := fixtureCollector(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "https://other.invalid/secret")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}), func(context.Context) error {
		checks++
		if checks > 1 {
			return domain.ErrForbidden
		}
		return nil
	})
	_, err := c.Collect(context.Background(), r)
	if !errors.Is(err, domain.ErrForbidden) || calls != 1 {
		t.Fatalf("authority/redirect calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Collect(ctx, r)
	if err == nil {
		t.Fatal("ignored cancelled request")
	}
}
func TestEvaluationRequiresApprovedWindowDeploymentAndCurrentEvidence(t *testing.T) {
	r := requestFixture()
	s := domain.RuntimeSignal{Kind: "metrics", Metric: "synthetic_latency", State: "collected", Correlation: "exact", Coverage: "complete_query_grid", Series: []domain.RuntimeSeries{{Labels: map[string]string{"service": r.Target.Service, "env": r.Input.Environment, "commit": r.Input.Commit}, Points: []domain.RuntimePoint{{At: r.Input.Start, Value: 2}, {At: r.Input.Start.Add(15 * time.Second), Value: 12}, {At: r.Input.End, Value: 3}}}}}
	receipt := domain.RuntimeReceipt{CollectedAt: time.Now().UTC(), Signals: []domain.RuntimeSignal{s}}
	if got := Evaluate(r, receipt); got[0].State != "not_met" {
		t.Fatal("failed threshold passed")
	}
	for _, mode := range []string{"stale", "deployment", "window", "count"} {
		q := r
		v := receipt
		switch mode {
		case "stale":
			v.CollectedAt = r.Input.End.Add(time.Hour)
		case "deployment":
			q.Deployment.State = "pending"
		case "window":
			q.Input.End = q.Input.End.Add(time.Second)
		case "count":
			v.Signals = nil
		}
		if Evaluate(q, v)[0].State != "not_verified" {
			t.Fatalf("%s passed", mode)
		}
	}
}
