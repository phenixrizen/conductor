package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

// Called with the exact package revision held through the admission transaction.
// Proposal validates identifiers/content; authorization and all worker boundaries
// also require the existing exact independent package approval checks.
func validatePinnedVerificationCriteria(plan domain.CoordinationPlan, pin domain.PackagePin, content domain.Content) error {
	var criteria *domain.VerificationCriteria
	for _, task := range plan.Tasks {
		for _, check := range task.Checks {
			for _, requirement := range check.Requirements {
				if requirement.ChangeID != pin.ChangeID {
					continue
				}
				if criteria == nil {
					parsed, err := domain.ParseVerificationCriteria(content)
					if err != nil {
						return domain.ErrInvalidInput
					}
					criteria = &parsed
				}
				found := false
				for _, c := range criteria.Criteria {
					if c.ID == requirement.CriterionID {
						found = true
						break
					}
				}
				if !found || requirement.Revision != pin.Revision || requirement.Digest != pin.Digest {
					return domain.ErrInvalidInput
				}
			}
		}
	}
	return nil
}

// Historical display reads only the plan's original package revisions under the
// already-held all-source grants. It never refreshes a package or rewrites the
// retained execution artifact to add inferred requirements.
func (p *Postgres) verificationReview(ctx context.Context, run domain.CoordinationRun, taskKey string, result execution.Result) (*domain.VerificationReview, error) {
	out := &domain.VerificationReview{SchemaVersion: 1, Criteria: []domain.VerificationSupport{}, Gaps: []domain.VerificationGap{}}
	var task domain.CoordinationTask
	for _, candidate := range run.Plan.Tasks {
		if candidate.ID == taskKey {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		return nil, domain.ErrUnavailable
	}
	for _, pin := range run.Plan.Packages {
		var content domain.Content
		var approved bool
		err := p.tx.QueryRow(ctx, `SELECT r.content,EXISTS(SELECT 1 FROM approvals a WHERE a.change_id=r.change_id AND a.revision=r.revision AND a.digest=r.digest) AND r.submitted_at IS NOT NULL FROM work_package_revisions r JOIN changes c ON c.id=r.change_id WHERE r.change_id=$1 AND r.revision=$2 AND r.digest=$3 AND c.workspace_id=$4 AND c.repository_id=$5`, pin.ChangeID, pin.Revision, pin.Digest, run.WorkspaceID, pin.RepositoryID).Scan(&content, &approved)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		gap := "criteria_unavailable"
		if err == nil {
			criteria, parseErr := domain.ParseVerificationCriteria(content)
			if parseErr == nil {
				for _, c := range criteria.Criteria {
					ref := domain.VerificationRequirement{ChangeID: pin.ChangeID, Revision: pin.Revision, Digest: pin.Digest, CriterionID: c.ID}
					out.Criteria = append(out.Criteria, execution.AssessVerification(ref, c.Description, approved, task, result))
				}
				continue
			}
			if errors.Is(parseErr, domain.ErrNotFound) {
				gap = "criteria_absent"
			}
		}
		out.Gaps = append(out.Gaps, domain.VerificationGap{Package: pin, State: gap})
	}
	return out, nil
}
