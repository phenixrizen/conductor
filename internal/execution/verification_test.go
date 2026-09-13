package execution

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
)

func TestCriterionSupportRequiresEveryExactDeclaredCheck(t *testing.T) {
	ref := domain.VerificationRequirement{ChangeID: "design", Revision: 1, Digest: strings.Repeat("a", 64), CriterionID: "synthetic-output"}
	task := domain.CoordinationTask{ProfileDigest: strings.Repeat("b", 64), Image: "sha256:" + strings.Repeat("c", 64), Checks: []domain.VerificationCommand{{ID: "check", RepositoryID: "repo", Argv: []string{"true"}, Requirements: []domain.VerificationRequirement{ref}}}}
	zero := 0
	r := Result{InputDigest: strings.Repeat("d", 64), ProfileDigest: task.ProfileDigest, Image: task.Image, CleanupConfirmed: true, Producer: Evidence{State: "passed", ExitCode: &zero}, Patches: []Patch{}, Checks: []Evidence{{ID: "check", RepositoryID: "repo", Argv: []string{"true"}, Requirements: []domain.VerificationRequirement{ref}, State: "passed", ExitCode: &zero, SourceDigest: Sum([]byte("[]")), OutputDigest: Sum(nil)}}}
	if got := AssessVerification(ref, "Synthetic criterion", true, task, r); got.State != "supported" {
		t.Fatalf("complete linked check: %+v", got)
	}
	for _, mutate := range []func(*Result){
		func(r *Result) { r.Checks = nil }, func(r *Result) { r.Checks[0].State = "failed" }, func(r *Result) { r.Checks[0].State = "unexecuted" }, func(r *Result) { r.Checks[0].Truncated = true }, func(r *Result) { r.Checks[0].SourceDigest = Sum(nil) }, func(r *Result) { r.Checks[0].Requirements = nil }, func(r *Result) { r.Checks[0].Requirements[0].CriterionID = "other" }, func(r *Result) { r.Checks[0].Requirements[0].Revision++ }, func(r *Result) { r.Checks[0].Output = "changed" }, func(r *Result) { r.Checks = append(r.Checks, r.Checks[0]) }, func(r *Result) { r.CleanupConfirmed = false },
	} {
		raw, _ := json.Marshal(r)
		var changed Result
		_ = json.Unmarshal(raw, &changed)
		mutate(&changed)
		if got := AssessVerification(ref, "Synthetic criterion", true, task, changed); got.State != "not_verified" {
			t.Fatal("missing/failed/substituted evidence supported a criterion", got)
		}
	}
	if got := AssessVerification(ref, "Synthetic criterion", false, task, r); got.State != "not_verified" {
		t.Fatal("unapproved revision gained support")
	}
	unlinked := ref
	unlinked.CriterionID = "unlinked"
	if got := AssessVerification(unlinked, "Another criterion", true, task, r); got.State != "not_verified" || got.Reason != "unlinked" {
		t.Fatal("one command verified another criterion")
	}
	// When two declared commands support one criterion, one passing result is
	// insufficient even if the other result is omitted or failed.
	task.Checks = append(task.Checks, task.Checks[0])
	task.Checks[1].ID = "second"
	if got := AssessVerification(ref, "Synthetic criterion", true, task, r); got.State != "not_verified" {
		t.Fatal("missing second declared check was ignored")
	}
}

func TestLegacyCheckAndEvidenceJSONKeepTheirDigests(t *testing.T) {
	for _, raw := range []string{
		`{"id":"check","repositoryId":"repo","argv":["true"],"timeoutSeconds":5}`,
		`{"id":"check","repositoryId":"repo","argv":["true"],"state":"unexecuted","outputDigest":"abc","truncated":false,"startedAt":"0001-01-01T00:00:00Z","finishedAt":"0001-01-01T00:00:00Z","sourceDigest":"def"}`,
	} {
		var value any
		if strings.Contains(raw, "timeoutSeconds") {
			value = &Check{}
		} else {
			value = &Evidence{}
		}
		if json.Unmarshal([]byte(raw), value) != nil {
			t.Fatal("legacy fixture")
		}
		encoded, _ := json.Marshal(value)
		if string(encoded) != raw || Sum(encoded) != Sum([]byte(raw)) {
			t.Fatal("omitted requirement extension changed old input/artifact digest")
		}
	}
}

func TestDockerCriterionLinksSurviveActualVerification(t *testing.T) {
	runner, request := dockerFixture(t)
	ref := domain.VerificationRequirement{ChangeID: request.ChangeID, Revision: request.Revision, Digest: request.Digest, CriterionID: "synthetic-output"}
	request.Checks[0].Requirements = []domain.VerificationRequirement{ref}
	r, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := ValidateResult(request, runner.Profile, runner.Image, r); err != nil || outcome != "succeeded" || !slices.Equal(r.Checks[0].Requirements, request.Checks[0].Requirements) {
		t.Fatal("native sandbox lost exact criterion binding", outcome, err)
	}
	r.Checks[0].Requirements = nil
	if _, err := ValidateResult(request, runner.Profile, runner.Image, r); err == nil {
		t.Fatal("omitted retained requirement accepted")
	}
}
