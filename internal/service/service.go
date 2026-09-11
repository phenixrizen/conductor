package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type Repository interface {
	Create(context.Context, string, string, domain.Content, string, time.Time) (domain.Package, error)
	Revise(context.Context, string, string, int64, domain.Content, string, time.Time) (domain.Package, error)
	Submit(context.Context, string, string, int64, time.Time) (domain.Package, error)
	Approve(context.Context, string, string, int64, string, time.Time) (domain.Package, error)
	Get(context.Context, string) (domain.Package, error)
}
type Service struct {
	repo Repository
	now  func() time.Time
}

func New(r Repository) *Service {
	return &Service{repo: r, now: func() time.Time { return time.Now().UTC() }}
}
func (s *Service) Create(ctx context.Context, actor string, c domain.Content) (domain.Package, error) {
	if e := domain.ValidateActor(actor); e != nil {
		return domain.Package{}, e
	}
	if e := domain.ValidateContent(c); e != nil {
		return domain.Package{}, e
	}
	d, e := domain.Digest(c)
	if e != nil {
		return domain.Package{}, e
	}
	b := make([]byte, 8)
	if _, e = rand.Read(b); e != nil {
		return domain.Package{}, e
	}
	return s.repo.Create(ctx, "CHG-"+hex.EncodeToString(b), actor, c, d, s.now())
}
func (s *Service) Revise(ctx context.Context, id, actor string, expected int64, c domain.Content) (domain.Package, error) {
	if e := domain.ValidateActor(actor); e != nil {
		return domain.Package{}, e
	}
	if e := domain.ValidateContent(c); e != nil {
		return domain.Package{}, e
	}
	if id == "" || expected < 1 {
		return domain.Package{}, domain.ErrInvalidInput
	}
	d, e := domain.Digest(c)
	if e != nil {
		return domain.Package{}, e
	}
	return s.repo.Revise(ctx, id, actor, expected, c, d, s.now())
}
func (s *Service) Submit(ctx context.Context, id, actor string, expected int64) (domain.Package, error) {
	if e := domain.ValidateActor(actor); e != nil {
		return domain.Package{}, e
	}
	if id == "" || expected < 1 {
		return domain.Package{}, domain.ErrInvalidInput
	}
	return s.repo.Submit(ctx, id, actor, expected, s.now())
}
func (s *Service) Approve(ctx context.Context, id, actor string, rev int64, digest string) (domain.Package, error) {
	if id == "" || rev < 1 || len(digest) != 64 {
		return domain.Package{}, domain.ErrInvalidInput
	}
	return s.repo.Approve(ctx, id, actor, rev, digest, s.now())
}
func (s *Service) Get(ctx context.Context, id string) (domain.Package, error) {
	return s.repo.Get(ctx, id)
}
