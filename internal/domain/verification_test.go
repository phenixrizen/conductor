package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestVerificationCriteriaAreBoundedSourceContent(t *testing.T) {
	valid := VerificationCriteria{SchemaVersion: 1, Criteria: []VerificationCriterion{{ID: "related-contract", Description: "The synthetic application reads the related contract."}}}
	content := Content{"verificationCriteria": valid, "unknownExtension": map[string]any{"unchanged": true}}
	before, _ := Digest(content)
	parsed, err := ParseVerificationCriteria(content)
	if err != nil || !reflect.DeepEqual(valid, parsed) {
		t.Fatal("valid criterion content lost", err)
	}
	after, _ := Digest(content)
	if before != after {
		t.Fatal("parsing changed immutable content")
	}
	for _, bad := range []any{
		VerificationCriteria{SchemaVersion: 2, Criteria: valid.Criteria},
		VerificationCriteria{SchemaVersion: 1, Criteria: append(append([]VerificationCriterion{}, valid.Criteria...), valid.Criteria[0])},
		VerificationCriteria{SchemaVersion: 1, Criteria: []VerificationCriterion{{ID: "criterion", Description: " "}}},
		VerificationCriteria{SchemaVersion: 1, Criteria: []VerificationCriterion{{ID: "criterion", Description: strings.Repeat("x", 4097)}}},
		map[string]any{"schemaVersion": 1, "criteria": valid.Criteria, "approved": true},
	} {
		if _, err := ParseVerificationCriteria(Content{"verificationCriteria": bad}); err == nil {
			t.Fatal("ambiguous criterion schema accepted")
		}
	}
}

func TestLegacyVerificationCommandDigestIsUnchanged(t *testing.T) {
	// This is the exact command JSON emitted before requirement links existed.
	raw := []byte(`{"id":"check","repositoryId":"repo","argv":["true"],"timeoutSeconds":5}`)
	var c VerificationCommand
	if json.Unmarshal(raw, &c) != nil {
		t.Fatal("legacy fixture")
	}
	for _, refs := range [][]VerificationRequirement{nil, {}} {
		c.Requirements = refs
		encoded, _ := json.Marshal(c)
		if string(encoded) != string(raw) {
			t.Fatal("omitted extension changed old command/plan digest")
		}
	}
}
