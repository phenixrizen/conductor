package service

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
)

type coordinationRepository interface {
	ExecutionCapabilities(context.Context) (domain.ExecutionCapabilities, error)
	CreateCoordination(context.Context, string, domain.CoordinationPlan) (domain.CoordinationRun, error)
	Coordination(context.Context, string) (domain.CoordinationRun, error)
	Coordinations(context.Context, string, int) (domain.CoordinationPage, error)
	AuthorizeCoordination(context.Context, string, string) (domain.CoordinationRun, error)
	CancelCoordination(context.Context, string, string) (domain.CoordinationRun, error)
}

func (s *AuthenticatedService) ExecutionCapabilities(ctx context.Context) (domain.ExecutionCapabilities, error) {
	return coordinationCommand(ctx, s, func(repo coordinationRepository) (domain.ExecutionCapabilities, error) {
		return repo.ExecutionCapabilities(ctx)
	})
}

// WithCoordination enables commands only after the operator has configured the
// execution worker. Each authorization still requires a separate human grant.
func (s *AuthenticatedService) WithCoordination() *AuthenticatedService {
	copy := *s
	copy.coordination = true
	return &copy
}
func coordinationCommand[T any](ctx context.Context, s *AuthenticatedService, run func(coordinationRepository) (T, error)) (T, error) {
	var zero T
	if !s.coordination {
		return zero, domain.ErrUnavailable
	}
	access, ok := domain.AccessFromContext(ctx)
	if !ok {
		return zero, domain.ErrUnauthenticated
	}
	if access.WorkspaceID == "" || access.RepositoryID == "" {
		return zero, domain.ErrInvalidInput
	}
	return authenticated(ctx, s, func(v *Service, _ string) (T, error) {
		repo, ok := v.repo.(coordinationRepository)
		if !ok {
			return zero, domain.ErrUnavailable
		}
		return run(repo)
	})
}
func (s *AuthenticatedService) CreateCoordination(ctx context.Context, key string, plan domain.CoordinationPlan) (domain.CoordinationRun, error) {
	if domain.ValidateCollectionKey(key) != nil || domain.ValidateCoordinationPlan(plan) != nil {
		return domain.CoordinationRun{}, domain.ErrInvalidInput
	}
	return coordinationCommand(ctx, s, func(repo coordinationRepository) (domain.CoordinationRun, error) {
		return repo.CreateCoordination(ctx, key, plan)
	})
}
func (s *AuthenticatedService) GetCoordination(ctx context.Context, id string) (domain.CoordinationRun, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.CoordinationRun{}, domain.ErrInvalidInput
	}
	return coordinationCommand(ctx, s, func(repo coordinationRepository) (domain.CoordinationRun, error) { return repo.Coordination(ctx, id) })
}
func (s *AuthenticatedService) ListCoordinations(ctx context.Context, before string, limit int) (domain.CoordinationPage, error) {
	if _, err := domain.ValidateChangeQuery("", before, limit); err != nil {
		return domain.CoordinationPage{}, err
	}
	return coordinationCommand(ctx, s, func(repo coordinationRepository) (domain.CoordinationPage, error) {
		return repo.Coordinations(ctx, before, limit)
	})
}
func (s *AuthenticatedService) AuthorizeCoordination(ctx context.Context, id, digest string) (domain.CoordinationRun, error) {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(digest, 64) {
		return domain.CoordinationRun{}, domain.ErrInvalidInput
	}
	return coordinationCommand(ctx, s, func(repo coordinationRepository) (domain.CoordinationRun, error) {
		return repo.AuthorizeCoordination(ctx, id, digest)
	})
}
func (s *AuthenticatedService) CancelCoordination(ctx context.Context, id, digest string) (domain.CoordinationRun, error) {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(digest, 64) {
		return domain.CoordinationRun{}, domain.ErrInvalidInput
	}
	return coordinationCommand(ctx, s, func(repo coordinationRepository) (domain.CoordinationRun, error) {
		return repo.CancelCoordination(ctx, id, digest)
	})
}
