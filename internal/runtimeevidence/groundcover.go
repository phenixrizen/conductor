// Package runtimeevidence collects read-only provider observations. Source text is
// data; it cannot alter the request scope, executable commands or outcome policy.
package runtimeevidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

const Host = "api.groundcover.com"
const maxResponse = 1 << 20

var ErrProvider = errors.New("runtime provider unavailable")

type Collector struct {
	client *http.Client
	origin string
	token  string
	check  func(context.Context) error
	now    func() time.Time
}

// Options supports owned literal-loopback fixtures only. Operator executables
// retain the official origin and cannot select arbitrary telemetry destinations.
type Options struct {
	Origin                string
	AllowInsecureLoopback bool
}

func New(token string, check func(context.Context) error, options ...Options) (*Collector, error) {
	origin := "https://" + Host
	if len(options) > 1 {
		return nil, ErrProvider
	}
	if len(options) == 1 && options[0].Origin != "" {
		u, err := url.Parse(options[0].Origin)
		if err != nil || !options[0].AllowInsecureLoopback || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			return nil, ErrProvider
		}
		origin = options[0].Origin
	}
	if len(token) < 16 || len(token) > 8192 || strings.ContainsAny(token, "\r\n") || check == nil {
		return nil, ErrProvider
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second, DisableKeepAlives: true, ForceAttemptHTTP2: false}
	return &Collector{client: &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, token: token, origin: origin, check: check, now: time.Now}, nil
}
func sum(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }
func (c *Collector) request(ctx context.Context, target domain.RuntimeTarget, path string, body any) ([]byte, string, error) {
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > 16384 {
		return nil, "", ErrProvider
	}
	queryDigest := sum(raw)
	if err = c.check(ctx); err != nil {
		return nil, queryDigest, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+path, bytes.NewReader(raw))
	if err != nil {
		return nil, queryDigest, ErrProvider
	}
	// These POSTs are read queries, but keep retry/redirect uncertainty explicit.
	req.GetBody = nil
	req.Close = true
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Backend-Id", target.BackendID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, queryDigest, ctx.Err()
		}
		return nil, queryDigest, ErrProvider
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, queryDigest, ErrProvider
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse || !json.Valid(data) {
		return nil, queryDigest, ErrProvider
	}
	return data, queryDigest, nil
}
func emptySignal(kind, metric, endpoint string) domain.RuntimeSignal {
	return domain.RuntimeSignal{Kind: kind, Metric: metric, Endpoint: endpoint, State: "unavailable", Correlation: "not_established", Coverage: "unknown", Series: []domain.RuntimeSeries{}, Records: []domain.RuntimeRecord{}}
}
func (c *Collector) Collect(ctx context.Context, r domain.RuntimeEvidence) (domain.RuntimeReceipt, error) {
	out := domain.RuntimeReceipt{Signals: []domain.RuntimeSignal{}, Evaluations: []domain.RuntimeEvaluation{}, ProductionOutcome: "not_verified"}
	target := r.Target
	config := domain.RuntimeIntegrationConfig{WorkspaceID: target.WorkspaceID, RepositoryID: target.RepositoryID, Environment: target.Environment, Service: target.Service, BackendID: target.BackendID, CredentialID: "validation", Profile: target.Profile, MetricFields: target.MetricFields, RecordFields: target.RecordFields, Metrics: target.Metrics, StepSeconds: target.StepSeconds, MaxAgeSeconds: target.MaxAgeSeconds}
	if domain.ValidateRuntimeIntegration(config) != nil || target.SourceCommit != domain.GroundcoverSourceCommit || target.Environment != r.Input.Environment || !domain.IsLowerHex(r.Input.Commit, 40) || !r.Input.End.After(r.Input.Start) || r.Input.End.Sub(r.Input.Start) > time.Hour {
		return out, ErrProvider
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for _, metric := range target.Metrics {
		path := "/api/metrics/query-range"
		signal := emptySignal("metrics", metric, path)
		filters := map[string]string{target.MetricFields.Service: target.Service, target.MetricFields.Environment: target.Environment, target.MetricFields.Commit: r.Input.Commit}
		keys := []string{target.MetricFields.Service, target.MetricFields.Environment, target.MetricFields.Commit}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			parts = append(parts, k+"="+strconv.Quote(filters[k]))
		}
		query := metric + "{" + strings.Join(parts, ",") + "}"
		body := map[string]string{"promql": query, "start": r.Input.Start.UTC().Format(time.RFC3339), "end": r.Input.End.UTC().Format(time.RFC3339), "step": strconv.Itoa(target.StepSeconds) + "s"}
		raw, digest, err := c.request(ctx, target, path, body)
		signal.QueryDigest = digest
		signal.CollectedAt = c.now().UTC()
		if err == nil {
			signal.ResponseDigest = sum(raw)
			decodeMetrics(&signal, raw, query, r)
		} else if !errors.Is(err, ErrProvider) {
			return out, err
		}
		out.Signals = append(out.Signals, signal)
	}
	for _, kind := range []string{"logs", "traces"} {
		path := "/api/" + kind + "/v2/search"
		signal := emptySignal(kind, "", path)
		f := target.RecordFields
		// gcQL is case-insensitive by default. := selects exact case and explicit AND
		// avoids gcQL's implicit OR rule when a field is repeated.
		query := f.Service + ":=" + strconv.Quote(target.Service) + " AND " + f.Environment + ":=" + strconv.Quote(target.Environment) + " AND " + f.Commit + ":=" + strconv.Quote(r.Input.Commit) + " | sort by (timestamp desc) | limit 51"
		body := map[string]any{"query": query, "start": r.Input.Start.UTC().Format(time.RFC3339), "end": r.Input.End.UTC().Format(time.RFC3339), "enableStream": false, "sources": []any{}}
		raw, digest, err := c.request(ctx, target, path, body)
		signal.QueryDigest = digest
		signal.CollectedAt = c.now().UTC()
		if err == nil {
			signal.ResponseDigest = sum(raw)
			decodeRecords(&signal, raw, r)
		} else if !errors.Is(err, ErrProvider) {
			return out, err
		}
		out.Signals = append(out.Signals, signal)
	}
	out.CollectedAt = c.now().UTC()
	out.Evaluations = Evaluate(r, out)
	digest, err := domain.RuntimeReceiptDigest(out)
	if err != nil {
		return out, ErrProvider
	}
	out.Digest = digest
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > domain.MaxRuntimeReceiptBytes {
		return domain.RuntimeReceipt{}, ErrProvider
	}
	return out, nil
}
func exactLabels(labels map[string]string, f domain.RuntimeFields, r domain.RuntimeEvidence) bool {
	return labels[f.Service] == r.Target.Service && labels[f.Environment] == r.Input.Environment && labels[f.Commit] == r.Input.Commit
}
func decodeMetrics(signal *domain.RuntimeSignal, raw []byte, query string, r domain.RuntimeEvidence) {
	var result struct {
		Velocities []struct {
			Metric   map[string]string   `json:"metric"`
			Velocity [][]json.RawMessage `json:"velocity"`
		} `json:"velocities"`
		PromQL    string `json:"promql"`
		Warnings  []any  `json:"warnings"`
		Partial   bool   `json:"isPartial"`
		Truncated bool   `json:"truncated"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Velocities == nil || result.PromQL != query || len(result.Warnings) > 0 || result.Partial || result.Truncated {
		return
	}
	if len(result.Velocities) == 0 {
		signal.State = "missing"
		signal.Coverage = "empty_query_result"
		return
	}
	if len(result.Velocities) > 8 {
		signal.State = "truncated"
		signal.Coverage = "truncated"
		return
	}
	series := []domain.RuntimeSeries{}
	seen := map[string]bool{}
	complete := true
	for _, v := range result.Velocities {
		if !exactLabels(v.Metric, r.Target.MetricFields, r) || v.Metric["__name__"] != signal.Metric {
			signal.State = "uncorrelated"
			return
		}
		if len(v.Metric) > 32 || len(v.Velocity) > 241 {
			signal.State = "truncated"
			signal.Coverage = "truncated"
			return
		}
		for k, value := range v.Metric {
			if len(k) > 128 || len(value) > 256 {
				return
			}
		}
		labels, _ := domain.JSONDigest(v.Metric)
		if seen[labels] {
			return
		}
		seen[labels] = true
		s := domain.RuntimeSeries{Labels: v.Metric, Points: []domain.RuntimePoint{}}
		for _, pair := range v.Velocity {
			var stamp float64
			var number string
			if len(pair) != 2 || json.Unmarshal(pair[0], &stamp) != nil || json.Unmarshal(pair[1], &number) != nil || math.IsNaN(stamp) || math.IsInf(stamp, 0) || stamp < 0 || stamp > 253402300799 {
				return
			}
			value, err := strconv.ParseFloat(number, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				return
			}
			at := time.Unix(int64(stamp), int64((stamp-math.Floor(stamp))*1e9)).UTC()
			if at.Before(r.Input.Start) || at.After(r.Input.End) {
				signal.State = "uncorrelated"
				return
			}
			if len(s.Points) > 0 && !at.After(s.Points[len(s.Points)-1].At) {
				return
			}
			s.Points = append(s.Points, domain.RuntimePoint{At: at, Value: value})
		}
		if len(s.Points) == 0 {
			return
		}
		expected := int(r.Input.End.Sub(r.Input.Start)/time.Second)/r.Target.StepSeconds + 1
		if len(s.Points) != expected || r.Input.End.Sub(r.Input.Start)%(time.Duration(r.Target.StepSeconds)*time.Second) != 0 {
			complete = false
		}
		for i, p := range s.Points {
			if !p.At.Equal(r.Input.Start.Add(time.Duration(i*r.Target.StepSeconds) * time.Second)) {
				complete = false
			}
		}
		series = append(series, s)
	}
	signal.State = "collected"
	signal.Correlation = "exact"
	signal.Coverage = "partial_query_grid"
	if complete {
		signal.Coverage = "complete_query_grid"
	}
	signal.Series = series
}
func recordString(row map[string]json.RawMessage, key string) string {
	var direct, nested string
	_ = json.Unmarshal(row[key], &direct)
	var attributes map[string]json.RawMessage
	_ = json.Unmarshal(row["string_attributes"], &attributes)
	_ = json.Unmarshal(attributes[key], &nested)
	// Conflicting representations cannot establish a canonical correlation.
	if direct != "" && nested != "" && direct != nested {
		return ""
	}
	if direct != "" {
		return direct
	}
	return nested
}
func decodeRecords(signal *domain.RuntimeSignal, raw []byte, r domain.RuntimeEvidence) {
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || rows == nil {
		return
	}
	if len(rows) == 0 {
		signal.State = "missing"
		signal.Coverage = "empty_query_result"
		return
	}
	if len(rows) > 51 {
		signal.State = "truncated"
		signal.Coverage = "truncated"
		return
	}
	records := []domain.RuntimeRecord{}
	for _, data := range rows {
		if len(data) > 8192 {
			signal.State = "truncated"
			signal.Coverage = "truncated"
			return
		}
		var row map[string]json.RawMessage
		if json.Unmarshal(data, &row) != nil {
			return
		}
		f := r.Target.RecordFields
		if recordString(row, f.Service) != r.Target.Service || recordString(row, f.Environment) != r.Input.Environment || recordString(row, f.Commit) != r.Input.Commit {
			signal.State = "uncorrelated"
			return
		}
		at, err := time.Parse(time.RFC3339Nano, recordString(row, "timestamp"))
		if err != nil || at.Before(r.Input.Start) || at.After(r.Input.End) {
			signal.State = "uncorrelated"
			return
		}
		records = append(records, domain.RuntimeRecord{At: at.UTC(), Data: data})
	}
	signal.State = "collected"
	signal.Correlation = "exact"
	signal.Coverage = "bounded_query_result"
	if len(records) > 50 {
		records = records[:50]
		signal.State = "truncated"
		signal.Coverage = "truncated"
	}
	signal.Records = records
}
