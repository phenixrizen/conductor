package service

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
)

func (s *AuthenticatedService) GetCoordinationArtifact(ctx context.Context, id string, q domain.CoordinationArtifactQuery) (domain.CoordinationArtifact, error) {
	if !domain.IsLowerHex(id, 32) || domain.ValidateCoordinationArtifactQuery(q) != nil {
		return domain.CoordinationArtifact{}, domain.ErrInvalidInput
	}
	return coordinationCommand(ctx, s, func(repo coordinationRepository) (domain.CoordinationArtifact, error) {
		reader, ok := repo.(interface {
			InspectCoordinationArtifact(context.Context, string, domain.CoordinationArtifactQuery) (domain.CoordinationArtifact, error)
		})
		if !ok {
			return domain.CoordinationArtifact{}, domain.ErrUnavailable
		}
		return reader.InspectCoordinationArtifact(ctx, id, q)
	})
}
