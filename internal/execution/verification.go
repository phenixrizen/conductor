package execution

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/phenixrizen/conductor/internal/domain"
)

// ValidateVerificationBindings also runs in the receipt transaction. A worker
// result cannot add, drop or replace the inspected command's criterion links.
// No-link legacy reports retain their existing admission and digest semantics.
func ValidateVerificationBindings(task domain.CoordinationTask, result Result) error {
	if len(result.Producer.Requirements) != 0 {
		return ErrInvalid
	}
	declared := map[string]domain.VerificationCommand{}
	linked := false
	for _, c := range task.Checks {
		declared[c.ID] = c
		linked = linked || len(c.Requirements) > 0
	}
	seen := map[string]bool{}
	for _, e := range result.Checks {
		c, ok := declared[e.ID]
		if len(e.Requirements) == 0 && (!ok || len(c.Requirements) == 0) {
			continue
		}
		if !ok || seen[e.ID] || e.RepositoryID != c.RepositoryID || !reflect.DeepEqual(e.Argv, c.Argv) || !slices.Equal(e.Requirements, c.Requirements) {
			return ErrInvalid
		}
		seen[e.ID] = true
	}
	if linked {
		for _, c := range task.Checks {
			if len(c.Requirements) > 0 && !seen[c.ID] {
				return ErrInvalid
			}
		}
	}
	return nil
}

func AssessVerification(requirement domain.VerificationRequirement, description string, approved bool, task domain.CoordinationTask, result Result) domain.VerificationSupport {
	out := domain.VerificationSupport{Requirement: requirement, Description: description, State: "not_verified", Reason: "unlinked", CheckIDs: []string{}}
	passed := approved && result.CleanupConfirmed && result.Producer.State == "passed" && result.Producer.ExitCode != nil && *result.Producer.ExitCode == 0 && !result.Producer.Truncated && result.ProfileDigest == task.ProfileDigest && result.Image == task.Image
	patchJSON, _ := json.Marshal(result.Patches)
	for _, expected := range task.Checks {
		if !slices.Contains(expected.Requirements, requirement) {
			continue
		}
		out.CheckIDs = append(out.CheckIDs, expected.ID)
		matched := 0
		for _, e := range result.Checks {
			if e.ID != expected.ID {
				continue
			}
			matched++
			passed = passed && e.RepositoryID == expected.RepositoryID && reflect.DeepEqual(e.Argv, expected.Argv) && slices.Equal(e.Requirements, expected.Requirements) && e.State == "passed" && e.ExitCode != nil && *e.ExitCode == 0 && !e.Truncated && e.SourceDigest == Sum(patchJSON) && e.OutputDigest == Sum([]byte(e.Output))
		}
		passed = passed && matched == 1
	}
	if len(out.CheckIDs) > 0 {
		out.Reason = "linked_check_not_verified"
	}
	if !approved {
		out.Reason = "exact_revision_not_approved"
	}
	if len(out.CheckIDs) > 0 && passed {
		out.State, out.Reason = "supported", "declared_checks_passed"
	}
	return out
}
