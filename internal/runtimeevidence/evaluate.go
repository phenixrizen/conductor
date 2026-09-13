package runtimeevidence

import (
	"math"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

// Evaluate applies only approved explicit policy to a complete provider query
// grid. Logs, trace samples and a deployment label never prove numeric criteria.
func Evaluate(request domain.RuntimeEvidence, receipt domain.RuntimeReceipt) []domain.RuntimeEvaluation {
	out := []domain.RuntimeEvaluation{}
	for _, link := range request.Criteria {
		e := domain.RuntimeEvaluation{Requirement: link.Requirement, CriterionDigest: link.CriterionDigest, State: "not_verified", Reason: "complete_correlated_metrics_required"}
		c := link.Criterion
		if request.Deployment.State != "success" || request.Deployment.Commit != request.Input.Commit || request.Deployment.Environment != request.Input.Environment || request.Input.Start.Before(request.Deployment.ProviderUpdatedAt) {
			e.Reason = "successful_deployment_window_required"
			out = append(out, e)
			continue
		}
		if receipt.CollectedAt.Sub(request.Input.End) > time.Duration(c.MaxAgeSeconds)*time.Second || receipt.CollectedAt.Sub(request.Input.End) > time.Duration(request.Target.MaxAgeSeconds)*time.Second {
			e.Reason = "stale_window"
			out = append(out, e)
			continue
		}
		if int(request.Input.End.Sub(request.Input.Start)/time.Second) != c.WindowSeconds || request.Target.StepSeconds != c.StepSeconds {
			e.Reason = "approved_sampling_window_mismatch"
			out = append(out, e)
			continue
		}
		for _, signal := range receipt.Signals {
			if signal.Kind != "metrics" || signal.Metric != c.Metric {
				continue
			}
			if signal.State != "collected" || signal.Correlation != "exact" || signal.Coverage != "complete_query_grid" || len(signal.Series) != c.ExpectedSeries {
				break
			}
			value := 0.0
			count := 0
			valid := true
			for _, s := range signal.Series {
				if !exactLabels(s.Labels, request.Target.MetricFields, request) || len(s.Points) != c.WindowSeconds/c.StepSeconds+1 {
					valid = false
					break
				}
				for i, p := range s.Points {
					if !p.At.Equal(request.Input.Start.Add(time.Duration(i*c.StepSeconds)*time.Second)) || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
						valid = false
						break
					}
					if count == 0 {
						value = p.Value
					} else {
						switch c.Aggregation {
						case "maximum":
							value = math.Max(value, p.Value)
						case "minimum":
							value = math.Min(value, p.Value)
						case "mean":
							value += p.Value
						default:
							valid = false
						}
					}
					count++
				}
			}
			if !valid || count == 0 {
				break
			}
			if c.Aggregation == "mean" {
				value /= float64(count)
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				break
			}
			e.Value = &value
			e.State = "not_met"
			e.Reason = "approved_threshold_not_met"
			if c.Operator == "lte" && value <= c.Threshold || c.Operator == "gte" && value >= c.Threshold {
				e.State = "met"
				e.Reason = "approved_threshold_met"
			}
			break
		}
		out = append(out, e)
	}
	return out
}
