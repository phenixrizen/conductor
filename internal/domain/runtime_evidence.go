package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
)

// GroundcoverProfile pins the inspected public REST and official SDK schema
// snapshot. It is a compatibility profile, not a claim about a hosted release.
const GroundcoverProfile = "groundcover-rest/2026-09-13-sdk1.424.0"
const GroundcoverSourceCommit = "7c4ff02883078abf9266bd7602093e424206a61c"
const MaxRuntimeReceiptBytes = 1536 << 10

var runtimeField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
var runtimeMetric = regexp.MustCompile(`^[A-Za-z_:][A-Za-z0-9_:]{0,127}$`)
var runtimeValue = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._/-]{0,127}$`)

type RuntimeFields struct {
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Commit      string `json:"commit"`
}
type RuntimeIntegrationConfig struct {
	WorkspaceID   string        `json:"workspaceId"`
	RepositoryID  string        `json:"repositoryId"`
	Environment   string        `json:"environment"`
	Service       string        `json:"service"`
	BackendID     string        `json:"backendId"`
	CredentialID  string        `json:"credentialId"`
	Profile       string        `json:"profile"`
	MetricFields  RuntimeFields `json:"metricFields"`
	RecordFields  RuntimeFields `json:"recordFields"`
	Metrics       []string      `json:"metrics"`
	StepSeconds   int           `json:"stepSeconds"`
	MaxAgeSeconds int           `json:"maxAgeSeconds"`
	Enabled       bool          `json:"enabled"`
}
type RuntimeTarget struct {
	WorkspaceID        string        `json:"workspaceId"`
	RepositoryID       string        `json:"repositoryId"`
	Environment        string        `json:"environment"`
	Service            string        `json:"service"`
	BackendID          string        `json:"backendId"`
	Profile            string        `json:"profile"`
	SourceCommit       string        `json:"sourceCommit"`
	IntegrationVersion int64         `json:"integrationVersion"`
	MetricFields       RuntimeFields `json:"metricFields"`
	RecordFields       RuntimeFields `json:"recordFields"`
	Metrics            []string      `json:"metrics"`
	StepSeconds        int           `json:"stepSeconds"`
	MaxAgeSeconds      int           `json:"maxAgeSeconds"`
}
type RuntimeRequirement struct {
	ChangeID    string `json:"changeId"`
	Revision    int64  `json:"revision"`
	Digest      string `json:"digest"`
	CriterionID string `json:"criterionId"`
}
type RuntimeInput struct {
	DeliveryID          string               `json:"deliveryId"`
	DeliveryDigest      string               `json:"deliveryDigest"`
	ObservationSequence int64                `json:"observationSequence"`
	DeploymentID        string               `json:"deploymentId"`
	Commit              string               `json:"commit"`
	Environment         string               `json:"environment"`
	Start               time.Time            `json:"start"`
	End                 time.Time            `json:"end"`
	Requirements        []RuntimeRequirement `json:"requirements"`
}

// RuntimeCriteria is optional approved package content. Numeric policy must be
// supplied by its author and approved in that exact revision; labels confer none.
// Evaluation means the specified provider query samples met these criteria, not
// that the application is healthy or every business requirement was satisfied.
type RuntimeCriteria struct {
	SchemaVersion int                `json:"schemaVersion"`
	Criteria      []RuntimeCriterion `json:"criteria"`
}
type RuntimeCriterion struct {
	ID             string  `json:"id"`
	Metric         string  `json:"metric"`
	Aggregation    string  `json:"aggregation"`
	Operator       string  `json:"operator"`
	Threshold      float64 `json:"threshold"`
	ExpectedSeries int     `json:"expectedSeries"`
	WindowSeconds  int     `json:"windowSeconds"`
	StepSeconds    int     `json:"stepSeconds"`
	MaxAgeSeconds  int     `json:"maxAgeSeconds"`
}
type RuntimeCriterionLink struct {
	Requirement     RuntimeRequirement `json:"requirement"`
	Criterion       RuntimeCriterion   `json:"criterion"`
	CriterionDigest string             `json:"criterionDigest"`
}
type RuntimeEvidence struct {
	ID           string                 `json:"id"`
	WorkspaceID  string                 `json:"workspaceId"`
	RepositoryID string                 `json:"repositoryId"`
	RequesterID  string                 `json:"requesterId"`
	Input        RuntimeInput           `json:"input"`
	Digest       string                 `json:"digest"`
	Target       RuntimeTarget          `json:"target"`
	Deployment   ProviderDeployment     `json:"deployment"`
	Criteria     []RuntimeCriterionLink `json:"criteria"`
	CreatedAt    time.Time              `json:"createdAt"`
	Receipt      *RuntimeReceipt        `json:"receipt,omitempty"`
	Execution    *CollectionExecution   `json:"execution,omitempty"`
	Freshness    string                 `json:"freshness"`
}
type RuntimeSummary struct {
	ID            string    `json:"id"`
	DeliveryID    string    `json:"deliveryId"`
	RepositoryID  string    `json:"repositoryId"`
	Environment   string    `json:"environment"`
	Commit        string    `json:"commit"`
	Digest        string    `json:"digest"`
	CreatedAt     time.Time `json:"createdAt"`
	ReceiptDigest string    `json:"receiptDigest,omitempty"`
}
type RuntimePage struct {
	Evidence   []RuntimeSummary `json:"evidence"`
	NextBefore string           `json:"nextBefore,omitempty"`
}
type RuntimePoint struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}
type RuntimeSeries struct {
	Labels map[string]string `json:"labels"`
	Points []RuntimePoint    `json:"points"`
}
type RuntimeRecord struct {
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}
type RuntimeSignal struct {
	Kind           string          `json:"kind"`
	Metric         string          `json:"metric,omitempty"`
	State          string          `json:"state"`
	Correlation    string          `json:"correlation"`
	Coverage       string          `json:"coverage"`
	QueryDigest    string          `json:"queryDigest"`
	ResponseDigest string          `json:"responseDigest,omitempty"`
	Endpoint       string          `json:"endpoint"`
	CollectedAt    time.Time       `json:"collectedAt"`
	Series         []RuntimeSeries `json:"series"`
	Records        []RuntimeRecord `json:"records"`
}
type RuntimeEvaluation struct {
	Requirement     RuntimeRequirement `json:"requirement"`
	CriterionDigest string             `json:"criterionDigest"`
	State           string             `json:"state"`
	Reason          string             `json:"reason"`
	Value           *float64           `json:"value,omitempty"`
}
type RuntimeReceipt struct {
	Digest      string              `json:"digest"`
	Signals     []RuntimeSignal     `json:"signals"`
	Evaluations []RuntimeEvaluation `json:"evaluations"`
	// No overall production claim is inferred from a selected subset of criteria.
	ProductionOutcome string    `json:"productionOutcome"`
	CollectedAt       time.Time `json:"collectedAt"`
}

func ValidateRuntimeIntegration(c RuntimeIntegrationConfig) error {
	if ValidateAccessID(c.WorkspaceID) != nil || ValidateAccessID(c.RepositoryID) != nil || ValidateAccessID(c.CredentialID) != nil || ValidateAccessID(c.BackendID) != nil || !runtimeValue.MatchString(c.Environment) || !runtimeValue.MatchString(c.Service) || c.Profile != GroundcoverProfile || len(c.Metrics) > 4 || c.StepSeconds < 15 || c.StepSeconds > 300 || c.MaxAgeSeconds < 60 || c.MaxAgeSeconds > 86400 {
		return ErrInvalidInput
	}
	for _, f := range []RuntimeFields{c.MetricFields, c.RecordFields} {
		if !runtimeField.MatchString(f.Service) || !runtimeField.MatchString(f.Environment) || !runtimeField.MatchString(f.Commit) || f.Service == f.Environment || f.Service == f.Commit || f.Environment == f.Commit {
			return ErrInvalidInput
		}
	}
	seen := map[string]bool{}
	for _, m := range c.Metrics {
		if !runtimeMetric.MatchString(m) || seen[m] {
			return ErrInvalidInput
		}
		seen[m] = true
	}
	return nil
}
func ValidateRuntimeInput(i RuntimeInput, now time.Time) error {
	if !IsLowerHex(i.DeliveryID, 32) || !IsLowerHex(i.DeliveryDigest, 64) || i.ObservationSequence < 1 || len(i.DeploymentID) < 1 || len(i.DeploymentID) > 128 || strings.ContainsAny(i.DeploymentID, "\r\n") || !IsLowerHex(i.Commit, 40) || !runtimeValue.MatchString(i.Environment) || len(i.Requirements) > 16 {
		return ErrInvalidInput
	}
	if i.Start.IsZero() || i.End.IsZero() || !i.End.After(i.Start) || i.End.Sub(i.Start) > time.Hour || i.End.After(now) || i.Start.Before(now.Add(-7*24*time.Hour)) || i.Start.Nanosecond() != 0 || i.End.Nanosecond() != 0 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, r := range i.Requirements {
		if ValidateAccessID(r.ChangeID) != nil || r.Revision < 1 || !IsLowerHex(r.Digest, 64) || ValidateAccessID(r.CriterionID) != nil {
			return ErrInvalidInput
		}
		key := r.ChangeID + ":" + r.CriterionID
		if seen[key] {
			return ErrInvalidInput
		}
		seen[key] = true
	}
	return nil
}
func ParseRuntimeCriteria(content Content) (RuntimeCriteria, error) {
	var out RuntimeCriteria
	value, ok := content["runtimeCriteria"]
	if !ok {
		return out, ErrNotFound
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 32<<10 {
		return out, ErrInvalidInput
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF || out.SchemaVersion != 1 || len(out.Criteria) < 1 || len(out.Criteria) > 32 {
		return out, ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, c := range out.Criteria {
		if ValidateAccessID(c.ID) != nil || seen[c.ID] || !runtimeMetric.MatchString(c.Metric) || !(c.Aggregation == "maximum" || c.Aggregation == "minimum" || c.Aggregation == "mean") || !(c.Operator == "lte" || c.Operator == "gte") || math.IsNaN(c.Threshold) || math.IsInf(c.Threshold, 0) || c.ExpectedSeries < 1 || c.ExpectedSeries > 8 || c.StepSeconds < 15 || c.StepSeconds > 300 || c.WindowSeconds < 15 || c.WindowSeconds > 3600 || c.WindowSeconds%c.StepSeconds != 0 || c.MaxAgeSeconds < 60 || c.MaxAgeSeconds > 86400 {
			return out, ErrInvalidInput
		}
		seen[c.ID] = true
	}
	return out, nil
}
func RuntimeRequestDigest(r RuntimeEvidence) (string, error) {
	return JSONDigest(struct {
		ID, WorkspaceID, RepositoryID, RequesterID string
		Input                                      RuntimeInput
		Target                                     RuntimeTarget
		Deployment                                 ProviderDeployment
		Criteria                                   []RuntimeCriterionLink
	}{r.ID, r.WorkspaceID, r.RepositoryID, r.RequesterID, r.Input, r.Target, r.Deployment, r.Criteria})
}
func RuntimeReceiptDigest(r RuntimeReceipt) (string, error) { r.Digest = ""; return JSONDigest(r) }

// UnmarshalJSON requires every policy field, including a possibly zero threshold.
// A missing numeric threshold must never silently invent a domain decision.
func (c *RuntimeCriterion) UnmarshalJSON(raw []byte) error {
	type plain RuntimeCriterion
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalidInput
	}
	fields := map[string]bool{"id": false, "metric": false, "aggregation": false, "operator": false, "threshold": false, "expectedSeries": false, "windowSeconds": false, "stepSeconds": false, "maxAgeSeconds": false}
	for dec.More() {
		token, err = dec.Token()
		key, ok := token.(string)
		present, known := fields[key]
		if err != nil || !ok || !known || present {
			return ErrInvalidInput
		}
		fields[key] = true
		var value json.RawMessage
		if dec.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return ErrInvalidInput
		}
	}
	if _, err = dec.Token(); err != nil || dec.Decode(new(any)) != io.EOF {
		return ErrInvalidInput
	}
	for _, present := range fields {
		if !present {
			return ErrInvalidInput
		}
	}
	var value plain
	if json.Unmarshal(raw, &value) != nil {
		return ErrInvalidInput
	}
	*c = RuntimeCriterion(value)
	return nil
}
