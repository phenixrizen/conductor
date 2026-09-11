package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type reviewService struct{ pkg domain.Package }

func (s *reviewService) Create(_ context.Context, actor string, content domain.Content) (domain.Package, error) {
	digest, _ := domain.Digest(content)
	s.pkg = domain.Package{ID: "CHG-test", Revision: domain.Revision{ChangeID: "CHG-test", Number: 1, SchemaVersion: 1, Digest: digest, Content: content, Author: actor}}
	return s.pkg, nil
}
func (s *reviewService) Get(_ context.Context, id string) (domain.Package, error) {
	if id != s.pkg.ID {
		return domain.Package{}, domain.ErrNotFound
	}
	return s.pkg, nil
}
func (s *reviewService) Revise(_ context.Context, id, actor string, expected int64, content domain.Content) (domain.Package, error) {
	if id != s.pkg.ID {
		return domain.Package{}, domain.ErrNotFound
	}
	if expected != s.pkg.Revision.Number {
		return domain.Package{}, domain.ErrConflict
	}
	digest, _ := domain.Digest(content)
	s.pkg.Revision = domain.Revision{ChangeID: id, Number: expected + 1, SchemaVersion: 1, Digest: digest, Content: content, Author: actor}
	s.pkg.Approval, s.pkg.Approved = nil, false
	return s.pkg, nil
}
func (s *reviewService) Submit(_ context.Context, id, _ string, revision int64) (domain.Package, error) {
	if id != s.pkg.ID {
		return domain.Package{}, domain.ErrNotFound
	}
	if revision != s.pkg.Revision.Number {
		return domain.Package{}, domain.ErrConflict
	}
	now := time.Now().UTC()
	s.pkg.Revision.SubmittedAt = &now
	return s.pkg, nil
}
func (s *reviewService) Approve(_ context.Context, id, reviewer string, revision int64, digest string) (domain.Package, error) {
	if id != s.pkg.ID {
		return domain.Package{}, domain.ErrNotFound
	}
	if err := domain.ValidateApproval(s.pkg.Revision, revision, digest, reviewer); err != nil {
		return domain.Package{}, err
	}
	s.pkg.Approved = true
	s.pkg.Approval = &domain.Approval{ChangeID: id, Revision: revision, Digest: digest, Reviewer: reviewer}
	return s.pkg, nil
}

func perform(t *testing.T, h http.Handler, method, path, actor string, input any) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &body)
	if actor != "" {
		req.Header.Set("X-Conductor-Actor", actor)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Header().Get("X-Correlation-ID") == "" {
		t.Fatal("response omitted correlation ID")
	}
	return res
}

func packageFrom(t *testing.T, res *httptest.ResponseRecorder) domain.Package {
	t.Helper()
	var pkg domain.Package
	if err := json.NewDecoder(res.Body).Decode(&pkg); err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestReviewEditInvalidationThroughHTTP(t *testing.T) {
	h := New(&reviewService{})
	created := perform(t, h, http.MethodPost, "/api/v1/changes", "developer", map[string]any{"content": map[string]any{"intent": "original"}})
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	pkg := packageFrom(t, created)
	submitted := perform(t, h, http.MethodPost, "/api/v1/changes/"+pkg.ID+"/review-requests", "developer", map[string]any{"revision": 1})
	if submitted.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", submitted.Code, submitted.Body.String())
	}
	approved := perform(t, h, http.MethodPost, "/api/v1/changes/"+pkg.ID+"/approvals", "reviewer", map[string]any{"revision": 1, "digest": pkg.Revision.Digest})
	if approved.Code != http.StatusCreated || !packageFrom(t, approved).Approved {
		t.Fatalf("approve: %d %s", approved.Code, approved.Body.String())
	}
	revised := perform(t, h, http.MethodPost, "/api/v1/changes/"+pkg.ID+"/revisions", "developer", map[string]any{"expectedRevision": 1, "content": map[string]any{"intent": "changed"}})
	if revised.Code != http.StatusCreated || packageFrom(t, revised).Approved {
		t.Fatalf("revise: %d %s", revised.Code, revised.Body.String())
	}
	stale := perform(t, h, http.MethodPost, "/api/v1/changes/"+pkg.ID+"/approvals", "reviewer", map[string]any{"revision": 1, "digest": pkg.Revision.Digest})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale approval: %d %s", stale.Code, stale.Body.String())
	}
}

func TestRejectsUnauthenticatedAndMalformedCommands(t *testing.T) {
	h := New(&reviewService{})
	unauthenticated := perform(t, h, http.MethodPost, "/api/v1/changes", "", map[string]any{"content": map[string]any{"intent": "x"}})
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", unauthenticated.Code)
	}
	malformed := perform(t, h, http.MethodPost, "/api/v1/changes", "developer", map[string]any{"content": map[string]any{"intent": "x"}, "unknown": true})
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("got %d", malformed.Code)
	}
}
