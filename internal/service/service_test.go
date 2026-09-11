package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type memory struct {
	mu sync.Mutex
	p  domain.Package
}

func (m *memory) Create(_ context.Context, id, a string, c domain.Content, d string, n time.Time) (domain.Package, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.p = domain.Package{ID: id, Revision: domain.Revision{ChangeID: id, Number: 1, SchemaVersion: 1, Digest: d, Content: c, Author: a, CreatedAt: n}}
	return m.p, nil
}
func (m *memory) Revise(_ context.Context, id, a string, e int64, c domain.Content, d string, n time.Time) (domain.Package, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.p.Revision.Number != e {
		return domain.Package{}, domain.ErrConflict
	}
	m.p.Revision = domain.Revision{ChangeID: id, Number: e + 1, SchemaVersion: 1, Digest: d, Content: c, Author: a, CreatedAt: n}
	m.p.Approval = nil
	m.p.Approved = false
	return m.p, nil
}
func (m *memory) Submit(_ context.Context, _, _ string, e int64, n time.Time) (domain.Package, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.p.Revision.Number != e {
		return domain.Package{}, domain.ErrConflict
	}
	m.p.Revision.SubmittedAt = &n
	return m.p, nil
}
func (m *memory) Approve(_ context.Context, id, a string, r int64, d string, n time.Time) (domain.Package, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := domain.ValidateApproval(m.p.Revision, r, d, a); e != nil {
		return domain.Package{}, e
	}
	x := domain.Approval{ChangeID: id, Revision: r, Digest: d, Reviewer: a, CreatedAt: n}
	m.p.Approval = &x
	m.p.Approved = true
	return m.p, nil
}
func (m *memory) Get(_ context.Context, _ string) (domain.Package, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.p, nil
}

func TestReviewThenEditInvalidatesEffectiveApproval(t *testing.T) {
	ctx := context.Background()
	s := New(&memory{})
	p, e := s.Create(ctx, "developer", domain.Content{"intent": "bounded change"})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.Submit(ctx, p.ID, "developer", 1)
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.Approve(ctx, p.ID, "reviewer", 1, p.Revision.Digest)
	if e != nil {
		t.Fatal(e)
	}
	if !p.Approved {
		t.Fatal("expected approval")
	}
	p, e = s.Revise(ctx, p.ID, "developer", 1, domain.Content{"intent": "materially changed"})
	if e != nil {
		t.Fatal(e)
	}
	if p.Approved || p.Approval != nil {
		t.Fatal("old approval remained effective")
	}
}
func TestRejectsStaleAndSelfApproval(t *testing.T) {
	ctx := context.Background()
	s := New(&memory{})
	p, _ := s.Create(ctx, "developer", domain.Content{"intent": "change"})
	p, _ = s.Submit(ctx, p.ID, "developer", 1)
	if _, e := s.Approve(ctx, p.ID, "reviewer", 1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); e != domain.ErrStaleApproval {
		t.Fatalf("got %v", e)
	}
	if _, e := s.Approve(ctx, p.ID, "developer", 1, p.Revision.Digest); e != domain.ErrSelfApproval {
		t.Fatalf("got %v", e)
	}
}
func TestConcurrentRevisionUsesExpectedVersion(t *testing.T) {
	ctx := context.Background()
	s := New(&memory{})
	p, _ := s.Create(ctx, "developer", domain.Content{"v": 1})
	if _, e := s.Revise(ctx, p.ID, "developer", 1, domain.Content{"v": 2}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Revise(ctx, p.ID, "developer", 1, domain.Content{"v": 3}); e != domain.ErrConflict {
		t.Fatalf("got %v", e)
	}
}
