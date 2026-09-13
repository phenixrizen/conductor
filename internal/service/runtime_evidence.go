package service

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
	"time"
)

type runtimeRepository interface {
	CreateRuntimeEvidence(context.Context, string, domain.RuntimeInput) (domain.RuntimeEvidence, error)
	RuntimeEvidence(context.Context, string) (domain.RuntimeEvidence, error)
	RuntimeEvidencePage(context.Context, string, int) (domain.RuntimePage, error)
}

// WithRuntimeEvidence enables scoped read collection. Operator bindings, author
// permission and current access to every source repository remain transactional.
func (s *AuthenticatedService) WithRuntimeEvidence() *AuthenticatedService {
	copy := *s
	copy.runtimeEvidence = true
	return &copy
}
func runtimeCommand[T any](ctx context.Context, s *AuthenticatedService, run func(runtimeRepository) (T, error)) (T, error) {
	var zero T
	if !s.runtimeEvidence {
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
		repo, ok := v.repo.(runtimeRepository)
		if !ok {
			return zero, domain.ErrUnavailable
		}
		return run(repo)
	})
}

func (s *AuthenticatedService) CreateRuntimeEvidence(ctx context.Context, key string, input domain.RuntimeInput) (domain.RuntimeEvidence, error) {
	if domain.ValidateCollectionKey(key) != nil || domain.ValidateRuntimeInput(input, time.Now().UTC()) != nil {
		return domain.RuntimeEvidence{}, domain.ErrInvalidInput
	}
	return runtimeCommand(ctx, s, func(r runtimeRepository) (domain.RuntimeEvidence, error) {
		return r.CreateRuntimeEvidence(ctx, key, input)
	})
}
func (s *AuthenticatedService) GetRuntimeEvidence(ctx context.Context, id string) (domain.RuntimeEvidence, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.RuntimeEvidence{}, domain.ErrInvalidInput
	}
	return runtimeCommand(ctx, s, func(r runtimeRepository) (domain.RuntimeEvidence, error) { return r.RuntimeEvidence(ctx, id) })
}
func (s *AuthenticatedService) ListRuntimeEvidence(ctx context.Context, before string, limit int) (domain.RuntimePage, error) {
	if _, err := domain.ValidateChangeQuery("", before, limit); err != nil {
		return domain.RuntimePage{}, err
	}
	return runtimeCommand(ctx, s, func(r runtimeRepository) (domain.RuntimePage, error) {
		return r.RuntimeEvidencePage(ctx, before, limit)
	})
}
