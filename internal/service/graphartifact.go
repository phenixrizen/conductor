package service

import (
	"context"

	"github.com/phenixrizen/conductor/internal/domain"
)

type graphArtifactRepository interface {
	RepositoryGraphArtifact(context.Context, string, domain.GraphArtifactQuery) (domain.GraphArtifactResult, error)
}

func (s *AuthenticatedService) GetRepositoryGraphArtifact(ctx context.Context, id string, q domain.GraphArtifactQuery) (domain.GraphArtifactResult, error) {
	if !domain.IsLowerHex(id, 32) || domain.ValidateGraphArtifactQuery(q) != nil {
		return domain.GraphArtifactResult{}, domain.ErrInvalidInput
	}
	return graphCommand(ctx, s, func(repo graphRepository) (domain.GraphArtifactResult, error) {
		source, ok := repo.(graphArtifactRepository)
		if !ok {
			return domain.GraphArtifactResult{}, domain.ErrUnavailable
		}
		return source.RepositoryGraphArtifact(ctx, id, q)
	})
}
