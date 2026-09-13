package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/runtimeevidence"
)

// Validate historical receipts at their collection time. Reading them today must
// not change a retained met result into a new conclusion; window age is separate.
func (m releaseModel) validateRuntime(r domain.RuntimeEvidence) error {
	if !domain.IsLowerHex(r.ID, 32) || r.WorkspaceID != m.access.workspaceID || r.RepositoryID != m.access.repositoryID || r.Target.WorkspaceID != r.WorkspaceID || r.Target.RepositoryID != r.RepositoryID || r.Target.Environment != r.Input.Environment || r.Target.SourceCommit != domain.GroundcoverSourceCommit || r.Target.IntegrationVersion < 1 || domain.ValidateAccessID(r.RequesterID) != nil || domain.ValidateRuntimeInput(r.Input, r.CreatedAt) != nil {
		return errResponseScope
	}
	d, err := domain.RuntimeRequestDigest(r)
	if err != nil || d != r.Digest {
		return errResponseScope
	}
	target := r.Target
	config := domain.RuntimeIntegrationConfig{WorkspaceID: target.WorkspaceID, RepositoryID: target.RepositoryID, Environment: target.Environment, Service: target.Service, BackendID: target.BackendID, CredentialID: "inspection", Profile: target.Profile, MetricFields: target.MetricFields, RecordFields: target.RecordFields, Metrics: target.Metrics, StepSeconds: target.StepSeconds, MaxAgeSeconds: target.MaxAgeSeconds}
	if domain.ValidateRuntimeIntegration(config) != nil || r.Deployment.ID != r.Input.DeploymentID || r.Deployment.RepositoryID != r.RepositoryID || r.Deployment.Commit != r.Input.Commit || r.Deployment.Environment != r.Input.Environment || len(r.Criteria) != len(r.Input.Requirements) {
		return errResponseScope
	}
	seen := map[string]bool{}
	for _, link := range r.Criteria {
		key := link.Requirement.ChangeID + ":" + link.Requirement.CriterionID
		if seen[key] || !slices.Contains(r.Input.Requirements, link.Requirement) || link.Criterion.ID != link.Requirement.CriterionID || !slices.Contains(target.Metrics, link.Criterion.Metric) {
			return errResponseScope
		}
		seen[key] = true
		d, err := domain.JSONDigest(link.Criterion)
		if err != nil || d != link.CriterionDigest {
			return errResponseScope
		}
		if _, err = domain.ParseRuntimeCriteria(domain.Content{"runtimeCriteria": domain.RuntimeCriteria{SchemaVersion: 1, Criteria: []domain.RuntimeCriterion{link.Criterion}}}); err != nil {
			return errResponseScope
		}
	}
	if !slices.Contains([]string{"not_collected", "current", "stale"}, r.Freshness) {
		return errResponseScope
	}
	if r.Receipt == nil {
		if r.Freshness != "not_collected" {
			return errResponseScope
		}
	} else if r.Freshness == "not_collected" || runtimeevidence.ValidateReceipt(r, *r.Receipt, r.Receipt.CollectedAt) != nil {
		return errResponseScope
	}
	if r.Execution != nil && (!slices.Contains([]string{"running", "completed", "cancelled", "failed", "timed_out", "terminated", "unavailable", "unresolved"}, r.Execution.State) || r.Execution.ObservedAt.IsZero()) {
		return errResponseScope
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 2<<20 {
		return errResponseScope
	}
	return nil
}
func runtimeWindow(r domain.RuntimeEvidence, now time.Time) string {
	state := "within configured age bound"
	if now.Sub(r.Input.End) > time.Duration(r.Target.MaxAgeSeconds)*time.Second {
		state = "stale"
	}
	return fmt.Sprintf("Window %s to %s | %s; historical results are not current production proof", r.Input.Start.UTC().Format(time.RFC3339), r.Input.End.UTC().Format(time.RFC3339), state)
}
func runtimeSummary(r domain.RuntimeEvidence) string {
	lines := []string{"Runtime evidence: immutable collection facts; no deployment or approval authority.", "Deployment: " + r.Input.DeploymentID + " | environment " + r.Input.Environment + " | exact commit " + r.Input.Commit}
	if r.Receipt == nil {
		return strings.Join(append(lines, "Receipt: not collected. Criteria and production outcome are not verified."), "\n")
	}
	lines = append(lines, "Historical receipt: "+r.Receipt.Digest+" | collected "+r.Receipt.CollectedAt.UTC().Format(time.RFC3339), "Overall production outcome: "+r.Receipt.ProductionOutcome)
	if len(r.Receipt.Evaluations) == 0 {
		lines = append(lines, "No approved criteria were selected; no threshold or outcome is inferred.")
	}
	for _, evaluation := range r.Receipt.Evaluations {
		label := fmt.Sprintf("Historical criterion %s revision %d / %s: %s (%s)", evaluation.Requirement.ChangeID, evaluation.Requirement.Revision, evaluation.Requirement.CriterionID, evaluation.State, evaluation.Reason)
		if evaluation.Value != nil {
			label += fmt.Sprintf(" | observed value %g", *evaluation.Value)
		}
		lines = append(lines, label)
	}
	for _, signal := range r.Receipt.Signals {
		lines = append(lines, fmt.Sprintf("Signal %s %s: %s | correlation %s | coverage %s | series %d | records %d", signal.Kind, signal.Metric, signal.State, signal.Correlation, signal.Coverage, len(signal.Series), len(signal.Records)))
	}
	lines = append(lines, "Complete bounded request, policies, provenance, metric samples and escaped log/trace records follow.")
	return strings.Join(lines, "\n")
}
