package service

import (
	"context"

	"github.com/phenixrizen/conductor/internal/domain"
)

type changeLister interface {
	List(context.Context, string, string, int) (domain.ChangePage, error)
}

func (s *Service) List(ctx context.Context, repository, before string, limit int) (domain.ChangePage, error) {
	if _, err := domain.ValidateChangeQuery(repository, before, limit); err != nil {
		return domain.ChangePage{}, err
	}
	r, ok := s.repo.(changeLister)
	if !ok {
		return domain.ChangePage{}, domain.ErrUnavailable
	}
	return r.List(ctx, repository, before, limit)
}
