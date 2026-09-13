package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/authn"
	"github.com/phenixrizen/conductor/internal/domain"
)

type queryGuardService struct {
	authenticatedService
	calls int
}

func (s *queryGuardService) Create(context.Context, string, domain.Content) (domain.Package, error) {
	s.calls++
	return domain.Package{}, nil
}
func (s *queryGuardService) Get(context.Context, string) (domain.Package, error) {
	s.calls++
	return domain.Package{}, nil
}
func (s *queryGuardService) Revise(context.Context, string, string, int64, domain.Content) (domain.Package, error) {
	s.calls++
	return domain.Package{}, nil
}
func (s *queryGuardService) Submit(context.Context, string, string, int64) (domain.Package, error) {
	s.calls++
	return domain.Package{}, nil
}
func (s *queryGuardService) Approve(context.Context, string, string, int64, string) (domain.Package, error) {
	s.calls++
	return domain.Package{}, nil
}

type queryGuardVerifier struct{}

func (queryGuardVerifier) Verify(context.Context, string) (authn.Identity, error) {
	return authn.Identity{Issuer: "https://identity.example.test", Subject: "synthetic-reviewer"}, nil
}

func TestPackageOperationsRejectIgnoredQueryInputs(t *testing.T) {
	operations := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/v1/changes", map[string]any{"content": map[string]any{"intent": "synthetic design"}}},
		{http.MethodGet, "/api/v1/changes/CHG-test", nil},
		{http.MethodPost, "/api/v1/changes/CHG-test/revisions", map[string]any{"expectedRevision": 1, "content": map[string]any{"intent": "synthetic revision"}}},
		{http.MethodPost, "/api/v1/changes/CHG-test/review-requests", map[string]any{"revision": 1}},
		{http.MethodPost, "/api/v1/changes/CHG-test/approvals", map[string]any{"revision": 1, "digest": strings.Repeat("a", 64)}},
	}
	for _, mode := range []string{"local", "authenticated"} {
		for _, operation := range operations {
			for _, query := range []string{"?repositoryId=another-repository", "?revision=999&revision=1", "?malformed=%ZZ"} {
				t.Run(mode+"/"+operation.path+query, func(t *testing.T) {
					service := &queryGuardService{}
					var handler http.Handler = New(service)
					if mode == "authenticated" {
						handler = NewAuthenticated(service, queryGuardVerifier{})
					}
					var body bytes.Buffer
					if operation.body != nil {
						if err := json.NewEncoder(&body).Encode(operation.body); err != nil {
							t.Fatal(err)
						}
					}
					request := httptest.NewRequest(operation.method, operation.path+query, &body)
					if mode == "authenticated" {
						request.Header.Set("Authorization", "Bearer synthetic.token")
						request.Header.Set("X-Conductor-Workspace", "workspace-one")
						request.Header.Set("X-Conductor-Repository", "repo-one")
					} else {
						request.Header.Set("X-Conductor-Actor", "synthetic-reviewer")
					}
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					var envelope struct {
						Error struct {
							Code string `json:"code"`
						} `json:"error"`
					}
					if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if response.Code != http.StatusBadRequest || envelope.Error.Code != "invalid_input" || service.calls != 0 {
						t.Fatalf("query reached inspection/command boundary: status=%d code=%s calls=%d", response.Code, envelope.Error.Code, service.calls)
					}
				})
			}
		}
	}
}
