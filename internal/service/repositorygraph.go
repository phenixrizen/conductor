package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/phenixrizen/conductor/internal/domain"
)

type graphRepository interface {
	CreateRepositoryGraph(context.Context, string, string, domain.GraphInput) (domain.RepositoryGraph, error)
	RepositoryGraph(context.Context, string) (domain.RepositoryGraph, error)
	RepositoryGraphs(context.Context, string, int) (domain.RepositoryGraphPage, error)
}

func graphCommand[T any](ctx context.Context, s *AuthenticatedService, run func(graphRepository) (T, error)) (T, error) {
	var zero T
	a, ok := domain.AccessFromContext(ctx)
	if !ok {
		return zero, domain.ErrUnauthenticated
	}
	if a.WorkspaceID == "" || a.RepositoryID == "" {
		return zero, domain.ErrInvalidInput
	}
	return authenticated(ctx, s, func(v *Service, _ string) (T, error) {
		r, ok := v.repo.(graphRepository)
		if !ok {
			return zero, domain.ErrUnavailable
		}
		return run(r)
	})
}
func (s *AuthenticatedService) CreateRepositoryGraph(ctx context.Context, key string, input domain.GraphInput) (domain.RepositoryGraph, error) {
	if domain.ValidateCollectionKey(key) != nil {
		return domain.RepositoryGraph{}, domain.ErrInvalidInput
	}
	normalized, err := domain.NormalizeGraphInput(input)
	if err != nil {
		return domain.RepositoryGraph{}, err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return domain.RepositoryGraph{}, err
	}
	return graphCommand(ctx, s, func(r graphRepository) (domain.RepositoryGraph, error) {
		return r.CreateRepositoryGraph(ctx, hex.EncodeToString(random[:]), key, normalized)
	})
}
func (s *AuthenticatedService) GetRepositoryGraph(ctx context.Context, id string) (domain.RepositoryGraph, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.RepositoryGraph{}, domain.ErrInvalidInput
	}
	return graphCommand(ctx, s, func(r graphRepository) (domain.RepositoryGraph, error) { return r.RepositoryGraph(ctx, id) })
}
func (s *AuthenticatedService) ListRepositoryGraphs(ctx context.Context, before string, limit int) (domain.RepositoryGraphPage, error) {
	if _, err := domain.ValidateChangeQuery("", before, limit); err != nil {
		return domain.RepositoryGraphPage{}, err
	}
	return graphCommand(ctx, s, func(r graphRepository) (domain.RepositoryGraphPage, error) {
		return r.RepositoryGraphs(ctx, before, limit)
	})
}
func (s *AuthenticatedService) QueryRepositoryGraph(ctx context.Context, id string, q domain.GraphQuery) (domain.GraphQueryResult, error) {
	if err := domain.ValidateGraphQuery(q); err != nil {
		return domain.GraphQueryResult{}, err
	}
	g, err := s.GetRepositoryGraph(ctx, id)
	if err != nil {
		return domain.GraphQueryResult{}, err
	}
	return domain.QueryGraph(g, q)
}
