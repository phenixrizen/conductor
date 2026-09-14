package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/phenixrizen/conductor/internal/domain"
)

type assistanceRepository interface {
	RequestDesignAssistance(context.Context, string, string, domain.AssistanceInput) (domain.DesignAssistance, error)
	ListDesignAssistance(context.Context, domain.AssistanceListOptions) (domain.AssistancePage, error)
	GetDesignAssistance(context.Context, string) (domain.DesignAssistance, error)
	ProposeDesignSections(context.Context, string, string, domain.SuggestionInput) (domain.DesignAssistance, error)
	ApplyDesignSuggestion(context.Context, string, string, domain.ApplySuggestionInput) (domain.DesignAssistance, error)
}

func assistanceCommand[T any](ctx context.Context, s *AuthenticatedService, run func(assistanceRepository) (T, error)) (T, error) {
	var zero T
	access, ok := domain.AccessFromContext(ctx)
	if !ok {
		return zero, domain.ErrUnauthenticated
	}
	if access.WorkspaceID == "" || access.RepositoryID == "" {
		return zero, domain.ErrInvalidInput
	}
	return authenticated(ctx, s, func(v *Service, _ string) (T, error) {
		repo, ok := v.repo.(assistanceRepository)
		if !ok {
			return zero, domain.ErrUnavailable
		}
		return run(repo)
	})
}
func (s *AuthenticatedService) RequestDesignAssistance(ctx context.Context, key string, in domain.AssistanceInput) (domain.DesignAssistance, error) {
	if domain.ValidateCollectionKey(key) != nil || domain.ValidateAssistanceInput(in) != nil {
		return domain.DesignAssistance{}, domain.ErrInvalidInput
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return domain.DesignAssistance{}, err
	}
	return assistanceCommand(ctx, s, func(r assistanceRepository) (domain.DesignAssistance, error) {
		return r.RequestDesignAssistance(ctx, hex.EncodeToString(random[:]), key, in)
	})
}
func (s *AuthenticatedService) ListDesignAssistance(ctx context.Context, in domain.AssistanceListOptions) (domain.AssistancePage, error) {
	if _, err := domain.ValidateAssistanceListOptions(in); err != nil {
		return domain.AssistancePage{}, err
	}
	return assistanceCommand(ctx, s, func(r assistanceRepository) (domain.AssistancePage, error) { return r.ListDesignAssistance(ctx, in) })
}
func (s *AuthenticatedService) GetDesignAssistance(ctx context.Context, id string) (domain.DesignAssistance, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.DesignAssistance{}, domain.ErrInvalidInput
	}
	return assistanceCommand(ctx, s, func(r assistanceRepository) (domain.DesignAssistance, error) { return r.GetDesignAssistance(ctx, id) })
}
func (s *AuthenticatedService) ProposeDesignSections(ctx context.Context, id, key string, in domain.SuggestionInput) (domain.DesignAssistance, error) {
	if !domain.IsLowerHex(id, 32) || domain.ValidateCollectionKey(key) != nil || domain.ValidateSuggestionInput(in) != nil {
		return domain.DesignAssistance{}, domain.ErrInvalidInput
	}
	return assistanceCommand(ctx, s, func(r assistanceRepository) (domain.DesignAssistance, error) {
		return r.ProposeDesignSections(ctx, id, key, in)
	})
}
func (s *AuthenticatedService) ApplyDesignSuggestion(ctx context.Context, id, key string, in domain.ApplySuggestionInput) (domain.DesignAssistance, error) {
	if !domain.IsLowerHex(id, 32) || domain.ValidateCollectionKey(key) != nil || domain.ValidateApplySuggestionInput(in) != nil {
		return domain.DesignAssistance{}, domain.ErrInvalidInput
	}
	return assistanceCommand(ctx, s, func(r assistanceRepository) (domain.DesignAssistance, error) {
		return r.ApplyDesignSuggestion(ctx, id, key, in)
	})
}
