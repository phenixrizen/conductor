package store

import (
	"context"
	"fmt"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

// ValidatedExecutionProfile uses the actual runner adapter contract. The digest
// includes precisely the public profile fields that the producer will receive.
func ValidatedExecutionProfile(profile domain.WorkerProfile) (execution.Profile, string, error) {
	value := execution.Profile{Adapter: profile.Adapter, Model: profile.Model, Command: profile.Command, MaxBudgetUSD: profile.MaxBudgetUSD}
	if err := value.Validate(); err != nil {
		return value, "", fmt.Errorf("%w: unsupported execution profile", domain.ErrInvalidInput)
	}
	digest, err := domain.JSONDigest(value)
	return value, digest, err
}

func (p *Postgres) ExecutionProfiles(ctx context.Context) (domain.ExecutionProfilePage, error) {
	page := domain.ExecutionProfilePage{Profiles: []domain.ExecutionProfile{}}
	if p.access == nil || p.access.request.RepositoryID == "" {
		return page, domain.ErrForbidden
	}
	if err := p.authorizeRepository(ctx, p.tx, p.access.request.RepositoryID, "read"); err != nil {
		return page, err
	}
	rows, err := p.tx.Query(ctx, `SELECT id,profile_digest,image,configuration FROM execution_profiles WHERE workspace_id=$1 AND enabled ORDER BY id LIMIT 101 FOR SHARE`, p.access.request.WorkspaceID)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var profile domain.ExecutionProfile
		if err = rows.Scan(&profile.ID, &profile.ProfileDigest, &profile.Image, &profile.WorkerProfile); err != nil {
			return page, err
		}
		if len(page.Profiles) == 100 {
			page.Truncated = true
			break
		}
		page.Profiles = append(page.Profiles, profile)
	}
	return page, rows.Err()
}
