package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

func TestVerificationLinksAdmissionReceiptAndHistoricalScope(t *testing.T) {
	ctx, p, s, plan := coordinationFixture(t, true)
	plan.Tasks = plan.Tasks[:1]
	plan.Tasks[0].Scopes = append(plan.Tasks[0].Scopes, domain.TaskScope{RepositoryID: "repo-two", WritablePaths: []string{}})
	refs := []domain.VerificationRequirement{}
	for _, pin := range plan.Packages {
		refs = append(refs, domain.VerificationRequirement{ChangeID: pin.ChangeID, Revision: pin.Revision, Digest: pin.Digest, CriterionID: "synthetic-output"})
	}
	plan.Tasks[0].Checks = []domain.VerificationCommand{{ID: "contract", RepositoryID: "repo-one", Argv: []string{"true"}, TimeoutSeconds: 10, Requirements: refs}}
	author := accessContext(ctx, "worker", "workspace-one", "repo-one")
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	for _, kind := range []string{"unknown criterion", "foreign package", "stale digest", "stale revision", "duplicate reference", "outside task scope"} {
		raw, _ := json.Marshal(plan)
		var bad domain.CoordinationPlan
		_ = json.Unmarshal(raw, &bad)
		r := &bad.Tasks[0].Checks[0].Requirements[0]
		switch kind {
		case "unknown criterion":
			r.CriterionID = "missing"
		case "foreign package":
			r.ChangeID = "foreign"
		case "stale digest":
			r.Digest = strings.Repeat("f", 64)
		case "stale revision":
			r.Revision++
		case "duplicate reference":
			bad.Tasks[0].Checks[0].Requirements = append(bad.Tasks[0].Checks[0].Requirements, *r)
		case "outside task scope":
			bad.Tasks[0].Scopes = bad.Tasks[0].Scopes[:1]
		}
		if _, err := s.CreateCoordination(author, "bad-link", bad); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("%s accepted: %v", kind, err)
		}
	}
	run, err := s.CreateCoordination(author, "linked-plan", plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeCoordination(reviewer, run.ID, run.Digest); err != nil {
		t.Fatal(err)
	}
	var binding string
	if err = p.pool.QueryRow(ctx, `SELECT binding FROM coordination_runs WHERE id=$1`, run.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	w, err := p.CoordinationWork(ctx, run.ID, binding)
	if err != nil {
		t.Fatal(err)
	}
	taskID := w.TaskIDs[plan.Tasks[0].ID]
	input, err := p.CoordinationInput(ctx, run.ID, binding, taskID)
	if err != nil || len(input.Checks[0].Requirements) != 2 {
		t.Fatal("worker input lost related criterion", err)
	}
	if _, _, err = p.BeginCoordinationAttempt(ctx, run.ID, binding, taskID, execution.InputDigest(input)); err != nil {
		t.Fatal(err)
	}
	zero := 0
	artifact := execution.Result{CleanupConfirmed: true, InputDigest: execution.InputDigest(input), ProfileDigest: plan.Tasks[0].ProfileDigest, Image: plan.Tasks[0].Image, Producer: execution.Evidence{State: "passed", ExitCode: &zero}, Patches: []execution.Patch{}, Checks: []execution.Evidence{{ID: "contract", RepositoryID: "repo-one", Argv: []string{"true"}, State: "passed", ExitCode: &zero, OutputDigest: execution.Sum(nil), SourceDigest: execution.Sum([]byte("[]")), Requirements: refs}}}
	bad := artifact
	bad.Checks = append([]execution.Evidence{}, artifact.Checks...)
	bad.Checks[0].Requirements = nil
	if _, err = p.CompleteCoordinationTask(ctx, run.ID, binding, taskID, "succeeded", bad); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("receipt lost criterion links", err)
	}
	receipt, err := p.CompleteCoordinationTask(ctx, run.ID, binding, taskID, "succeeded", artifact)
	if err != nil {
		t.Fatal(err)
	}
	query := domain.CoordinationArtifactQuery{RunDigest: run.Digest, TaskID: taskID, ArtifactDigest: receipt.ArtifactDigest}
	inspected, err := s.GetCoordinationArtifact(reviewer, run.ID, query)
	if err != nil || inspected.Verification == nil || len(inspected.Verification.Criteria) != 4 {
		t.Fatal("linked criterion review", err)
	}
	for _, c := range inspected.Verification.Criteria {
		want := "not_verified"
		if c.Requirement.CriterionID == "synthetic-output" {
			want = "supported"
		}
		if c.State != want {
			t.Fatal("criterion support widened", c)
		}
	}
	// Editing a related package invalidates future execution but the exact
	// original criterion and its historical evidence remain readable.
	pin := plan.Packages[1]
	if _, err = s.Revise(accessContext(ctx, "author", "workspace-one", pin.RepositoryID), pin.ChangeID, "", pin.Revision, domain.Content{"intent": "New revision with no inherited criterion approval"}); err != nil {
		t.Fatal(err)
	}
	if err = p.CheckCoordinationWork(ctx, run.ID, binding); !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatal("package edit did not stop execution", err)
	}
	again, err := s.GetCoordinationArtifact(reviewer, run.ID, query)
	if err != nil || again.ArtifactDigest != inspected.ArtifactDigest {
		t.Fatal("historical evidence changed", err)
	}
	a, _ := domain.JSONDigest(again.Verification)
	b, _ := domain.JSONDigest(inspected.Verification)
	if a != b {
		t.Fatal("artifact inspection fetched newer criteria")
	}
	if _, err = p.pool.Exec(ctx, `UPDATE repository_grants SET can_read=false,can_author=false,can_approve=false WHERE repository_id='repo-two' AND principal_id='human-reviewer'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetCoordinationArtifact(reviewer, run.ID, query); err == nil {
		t.Fatal("related criterion descriptions leaked after revocation")
	}
}
