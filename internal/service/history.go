package service

import (
	"context"

	"github.com/phenixrizen/conductor/internal/domain"
)

type historyRepository interface {
	History(context.Context, string, int64, int) (domain.HistoryPage, error)
	Revision(context.Context, string, int64) (domain.RevisionRecord, error)
	Events(context.Context, string, int64, int) (domain.AuditPage, error)
}

func (s *Service) History(ctx context.Context, id string, beforeRevision int64, limit int) (domain.HistoryPage, error) {
	if err := domain.ValidateHistoryQuery(id, beforeRevision, limit); err != nil {
		return domain.HistoryPage{}, err
	}
	r, ok := s.repo.(historyRepository)
	if !ok {
		return domain.HistoryPage{}, domain.ErrUnavailable
	}
	return r.History(ctx, id, beforeRevision, limit)
}

func (s *Service) Revision(ctx context.Context, id string, revision int64) (domain.RevisionRecord, error) {
	if id == "" || revision < 1 {
		return domain.RevisionRecord{}, domain.ErrInvalidInput
	}
	r, ok := s.repo.(historyRepository)
	if !ok {
		return domain.RevisionRecord{}, domain.ErrUnavailable
	}
	return r.Revision(ctx, id, revision)
}

func (s *Service) Events(ctx context.Context, id string, afterSequence int64, limit int) (domain.AuditPage, error) {
	if err := domain.ValidateHistoryQuery(id, afterSequence, limit); err != nil {
		return domain.AuditPage{}, err
	}
	r, ok := s.repo.(historyRepository)
	if !ok {
		return domain.AuditPage{}, domain.ErrUnavailable
	}
	return r.Events(ctx, id, afterSequence, limit)
}
