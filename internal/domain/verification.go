package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// VerificationCriteria is optional source content. Only an exact approved
// revision can authorize its use; a description never supplies business policy
// beyond what the package author and reviewer actually wrote and inspected.
type VerificationCriteria struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Criteria      []VerificationCriterion `json:"criteria"`
}
type VerificationCriterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}
type VerificationRequirement struct {
	ChangeID    string `json:"changeId"`
	Revision    int64  `json:"revision"`
	Digest      string `json:"digest"`
	CriterionID string `json:"criterionId"`
}

// VerificationSupport is a derived view of one retained task artifact. Supported
// means every declared check for that criterion passed on the exact artifact;
// it never establishes all business requirements or grants any authority.
type VerificationSupport struct {
	Requirement VerificationRequirement `json:"requirement"`
	Description string                  `json:"description"`
	State       string                  `json:"state"`
	Reason      string                  `json:"reason"`
	CheckIDs    []string                `json:"checkIds"`
}
type VerificationGap struct {
	Package PackagePin `json:"package"`
	State   string     `json:"state"`
}
type VerificationReview struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Criteria      []VerificationSupport `json:"criteria"`
	Gaps          []VerificationGap     `json:"gaps"`
}

func ParseVerificationCriteria(content Content) (VerificationCriteria, error) {
	var out VerificationCriteria
	value, ok := content["verificationCriteria"]
	if !ok {
		return out, ErrNotFound
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 32<<10 {
		return out, ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF || out.SchemaVersion != 1 || len(out.Criteria) < 1 || len(out.Criteria) > 32 {
		return out, ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, c := range out.Criteria {
		if !planKey(c.ID) || seen[c.ID] || strings.TrimSpace(c.Description) == "" || len(c.Description) > 4096 || !utf8.ValidString(c.Description) || strings.ContainsRune(c.Description, 0) {
			return VerificationCriteria{}, ErrInvalidInput
		}
		seen[c.ID] = true
	}
	return out, nil
}
func ValidateVerificationRequirements(refs []VerificationRequirement) error {
	if len(refs) > 16 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, r := range refs {
		key := r.ChangeID + "\x00" + r.CriterionID
		if ValidateAccessID(r.ChangeID) != nil || r.Revision < 1 || !IsLowerHex(r.Digest, 64) || !planKey(r.CriterionID) || seen[key] {
			return ErrInvalidInput
		}
		seen[key] = true
	}
	return nil
}
