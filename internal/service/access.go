package service

import (
	"context"

	"github.com/phenixrizen/conductor/internal/domain"
)

// AccessTransaction holds the authorization snapshot and row locks until the
// domain command and its audit facts commit together.
type AccessTransaction interface {
	Repository
	List(context.Context, string, string, int) (domain.ChangePage, error)
	History(context.Context, string, int64, int) (domain.HistoryPage, error)
	Revision(context.Context, string, int64) (domain.RevisionRecord, error)
	Events(context.Context, string, int64, int) (domain.AuditPage, error)
	Principal() domain.Principal
	Commit(context.Context) error
	Rollback(context.Context) error
}
type AccessRepository interface {
	BeginAccess(context.Context, domain.AccessRequest) (AccessTransaction, error)
	Session(context.Context, domain.AccessRequest) (domain.Session, error)
	Repositories(context.Context, domain.AccessRequest) (domain.RepositoryPage, error)
}

// AuthenticatedService preserves the shared command contract while deriving every
// mutation actor from PostgreSQL. The legacy actor argument has no authority.
type AuthenticatedService struct {
	repo         AccessRepository
	collections  bool
	coordination bool
}

func NewAuthenticated(repo AccessRepository) *AuthenticatedService {
	return &AuthenticatedService{repo: repo}
}
func authenticated[T any](ctx context.Context, s *AuthenticatedService, run func(*Service, string) (T, error)) (T, error) {
	var zero T
	access, ok := domain.AccessFromContext(ctx)
	if !ok {
		return zero, domain.ErrUnauthenticated
	}
	tx, err := s.repo.BeginAccess(ctx, access)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	value, err := run(New(tx), tx.Principal().ID)
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return value, nil
}
func (s *AuthenticatedService) Create(ctx context.Context, _ string, c domain.Content) (domain.Package, error) {
	return authenticated(ctx, s, func(v *Service, actor string) (domain.Package, error) { return v.Create(ctx, actor, c) })
}
func (s *AuthenticatedService) Get(ctx context.Context, id string) (domain.Package, error) {
	return authenticated(ctx, s, func(v *Service, _ string) (domain.Package, error) { return v.Get(ctx, id) })
}
func (s *AuthenticatedService) Revise(ctx context.Context, id, _ string, n int64, c domain.Content) (domain.Package, error) {
	return authenticated(ctx, s, func(v *Service, actor string) (domain.Package, error) { return v.Revise(ctx, id, actor, n, c) })
}
func (s *AuthenticatedService) Submit(ctx context.Context, id, _ string, n int64) (domain.Package, error) {
	return authenticated(ctx, s, func(v *Service, actor string) (domain.Package, error) { return v.Submit(ctx, id, actor, n) })
}
func (s *AuthenticatedService) Approve(ctx context.Context, id, _ string, n int64, digest string) (domain.Package, error) {
	return authenticated(ctx, s, func(v *Service, actor string) (domain.Package, error) { return v.Approve(ctx, id, actor, n, digest) })
}
func (s *AuthenticatedService) List(ctx context.Context, repository, before string, limit int) (domain.ChangePage, error) {
	return authenticated(ctx, s, func(v *Service, _ string) (domain.ChangePage, error) { return v.List(ctx, repository, before, limit) })
}
func (s *AuthenticatedService) History(ctx context.Context, id string, before int64, limit int) (domain.HistoryPage, error) {
	return authenticated(ctx, s, func(v *Service, _ string) (domain.HistoryPage, error) { return v.History(ctx, id, before, limit) })
}
func (s *AuthenticatedService) Revision(ctx context.Context, id string, n int64) (domain.RevisionRecord, error) {
	return authenticated(ctx, s, func(v *Service, _ string) (domain.RevisionRecord, error) { return v.Revision(ctx, id, n) })
}
func (s *AuthenticatedService) Events(ctx context.Context, id string, after int64, limit int) (domain.AuditPage, error) {
	return authenticated(ctx, s, func(v *Service, _ string) (domain.AuditPage, error) { return v.Events(ctx, id, after, limit) })
}
func (s *AuthenticatedService) Session(ctx context.Context) (domain.Session, error) {
	a, ok := domain.AccessFromContext(ctx)
	if !ok {
		return domain.Session{}, domain.ErrUnauthenticated
	}
	return s.repo.Session(ctx, a)
}
func (s *AuthenticatedService) Repositories(ctx context.Context) (domain.RepositoryPage, error) {
	a, ok := domain.AccessFromContext(ctx)
	if !ok {
		return domain.RepositoryPage{}, domain.ErrUnauthenticated
	}
	return s.repo.Repositories(ctx, a)
}
