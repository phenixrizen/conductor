package runtimeevidence

import (
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

// ValidateReceipt is the last independent admission check before source evidence
// becomes an immutable shared fact. Callers cannot supply evaluation conclusions.
func ValidateReceipt(request domain.RuntimeEvidence, r domain.RuntimeReceipt, now time.Time) error {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > domain.MaxRuntimeReceiptBytes || r.CollectedAt.Before(request.CreatedAt) || r.CollectedAt.After(now.Add(time.Second)) || r.CollectedAt.Before(request.Input.End) || r.ProductionOutcome != "not_verified" || len(r.Signals) != len(request.Target.Metrics)+2 {
		return ErrProvider
	}
	digest, err := domain.RuntimeReceiptDigest(r)
	if err != nil || digest != r.Digest {
		return ErrProvider
	}
	seen := map[string]bool{}
	for _, s := range r.Signals {
		key := s.Kind + ":" + s.Metric
		if seen[key] || !domain.IsLowerHex(s.QueryDigest, 64) || s.CollectedAt.Before(request.CreatedAt) || s.CollectedAt.After(r.CollectedAt) || s.ResponseDigest != "" && !domain.IsLowerHex(s.ResponseDigest, 64) {
			return ErrProvider
		}
		seen[key] = true
		if !slices.Contains([]string{"collected", "missing", "unavailable", "truncated", "uncorrelated"}, s.State) || !slices.Contains([]string{"exact", "not_established"}, s.Correlation) || !slices.Contains([]string{"unknown", "empty_query_result", "truncated", "complete_query_grid", "partial_query_grid", "bounded_query_result"}, s.Coverage) {
			return ErrProvider
		}
		if s.State == "collected" && (s.Correlation != "exact" || s.ResponseDigest == "" || len(s.Series)+len(s.Records) == 0) || s.State == "missing" && (len(s.Records) > 0 || len(s.Series) > 0) || s.Correlation != "exact" && (len(s.Records) > 0 || len(s.Series) > 0) {
			return ErrProvider
		}
		if s.Kind == "metrics" {
			if !slices.Contains(request.Target.Metrics, s.Metric) || s.Endpoint != "/api/metrics/query-range" || len(s.Series) > 8 || len(s.Records) > 0 {
				return ErrProvider
			}
			for _, series := range s.Series {
				if !exactLabels(series.Labels, request.Target.MetricFields, request) || series.Labels["__name__"] != s.Metric || len(series.Points) > 241 {
					return ErrProvider
				}
			}
		} else {
			if !(s.Kind == "logs" || s.Kind == "traces") || s.Metric != "" || s.Endpoint != "/api/"+s.Kind+"/v2/search" || len(s.Series) > 0 || len(s.Records) > 50 {
				return ErrProvider
			}
			for _, record := range s.Records {
				var data map[string]json.RawMessage
				if len(record.Data) > 8192 || json.Unmarshal(record.Data, &data) != nil {
					return ErrProvider
				}
				stamp, err := time.Parse(time.RFC3339Nano, recordString(data, "timestamp"))
				if err != nil || !stamp.Equal(record.At) {
					return ErrProvider
				}
				f := request.Target.RecordFields
				if recordString(data, f.Service) != request.Target.Service || recordString(data, f.Environment) != request.Input.Environment || recordString(data, f.Commit) != request.Input.Commit || record.At.Before(request.Input.Start) || record.At.After(request.Input.End) {
					return ErrProvider
				}
			}
		}
	}
	if !reflect.DeepEqual(r.Evaluations, Evaluate(request, r)) {
		return ErrProvider
	}
	return nil
}
