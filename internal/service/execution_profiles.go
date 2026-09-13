package service

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
)

func (s *AuthenticatedService) ExecutionProfiles(ctx context.Context) (domain.ExecutionProfilePage, error) {
	var zero domain.ExecutionProfilePage
	if !s.coordination {
		return zero, domain.ErrUnavailable
	}
	a, ok := domain.AccessFromContext(ctx)
	if !ok {
		return zero, domain.ErrUnauthenticated
	}
	if a.WorkspaceID == "" || a.RepositoryID == "" {
		return zero, domain.ErrInvalidInput
	}
	return authenticated(ctx, s, func(v *Service, _ string) (domain.ExecutionProfilePage, error) {
		repo, ok := v.repo.(interface {
			ExecutionProfiles(context.Context) (domain.ExecutionProfilePage, error)
		})
		if !ok {
			return zero, domain.ErrUnavailable
		}
		return repo.ExecutionProfiles(ctx)
	})
}
