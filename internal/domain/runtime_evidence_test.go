package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRuntimeCriteriaRequireExplicitBoundedPolicy(t *testing.T) {
	valid := RuntimeCriteria{SchemaVersion: 1, Criteria: []RuntimeCriterion{{ID: "latency", Metric: "synthetic_latency", Aggregation: "maximum", Operator: "lte", Threshold: 5, ExpectedSeries: 1, WindowSeconds: 30, StepSeconds: 15, MaxAgeSeconds: 600}}}
	if _, err := ParseRuntimeCriteria(Content{"runtimeCriteria": valid}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing_policy", "unknown", "negative_series", "unbounded_window"} {
		c := valid
		c.Criteria = append([]RuntimeCriterion(nil), valid.Criteria...)
		var value any = c
		switch mode {
		case "missing_policy":
			c.Criteria[0].Operator = ""
			value = c
		case "unknown":
			value = map[string]any{"schemaVersion": 1, "criteria": c.Criteria, "approval": "agent"}
		case "negative_series":
			c.Criteria[0].ExpectedSeries = 0
			value = c
		case "unbounded_window":
			c.Criteria[0].WindowSeconds = 86400
			value = c
		}
		if _, err := ParseRuntimeCriteria(Content{"runtimeCriteria": value}); err == nil {
			t.Fatalf("%s accepted", mode)
		}
	}

	missing := map[string]any{}
	raw, _ := json.Marshal(valid.Criteria[0])
	_ = json.Unmarshal(raw, &missing)
	delete(missing, "threshold")
	if _, err := ParseRuntimeCriteria(Content{"runtimeCriteria": map[string]any{"schemaVersion": 1, "criteria": []any{missing}}}); err == nil {
		t.Fatal("missing threshold invented zero policy")
	}
	now := time.Now().UTC().Truncate(time.Second)
	input := RuntimeInput{DeliveryID: strings.Repeat("a", 32), DeliveryDigest: strings.Repeat("b", 64), ObservationSequence: 1, DeploymentID: "1", Commit: strings.Repeat("c", 40), Environment: "production", Start: now.Add(-time.Minute), End: now, Requirements: []RuntimeRequirement{}}
	if ValidateRuntimeInput(input, now) != nil {
		t.Fatal("valid past window rejected")
	}
	input.End = now.Add(time.Second)
	if ValidateRuntimeInput(input, now) == nil {
		t.Fatal("future observations permitted")
	}
}
