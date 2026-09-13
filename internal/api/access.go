package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/authn"
	"github.com/phenixrizen/conductor/internal/domain"
)

type authenticatedService interface {
	packageService
	historyService
	Session(context.Context) (domain.Session, error)
	Repositories(context.Context) (domain.RepositoryPage, error)
}

type tokenVerifier interface {
	Verify(context.Context, string) (authn.Identity, error)
}

// NewAuthenticated has no local-header fallback. Its service derives permissions
// and the durable principal ID from PostgreSQL inside the command transaction.
func NewAuthenticated(s authenticatedService, verifier tokenVerifier) http.Handler {
	a := &API{service: s, historyService: s}
	mux := routes(a)
	mux.HandleFunc("GET /api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
		value, err := s.Session(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, http.StatusOK, value)
	})
	mux.HandleFunc("GET /api/v1/repositories", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
		value, err := s.Repositories(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, http.StatusOK, value)
	})
	return requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		// Reject mixed modes rather than letting a caller select a durable actor
		// independently of the signed identity used for access control.
		if len(r.Header.Values("X-Conductor-Actor")) != 0 || len(r.Header.Values("Authorization")) != 1 {
			reject(w, r, http.StatusUnauthorized, "authentication_required", "a bearer access token is required; local actor headers are not accepted")
			return
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 16<<10 || verifier == nil {
			reject(w, r, http.StatusUnauthorized, "authentication_required", "a valid bearer access token is required")
			return
		}
		identity, err := verifier.Verify(r.Context(), parts[1])
		if err != nil {
			// Verification details may contain issuer responses. Never return token
			// contents or upstream diagnostics to a requesting client.
			reject(w, r, http.StatusUnauthorized, "authentication_required", "the access token could not be verified")
			return
		}
		workspace, okWorkspace := scopeHeader(r, "X-Conductor-Workspace")
		repository, okRepository := scopeHeader(r, "X-Conductor-Repository")
		if !okWorkspace || !okRepository {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
		access := domain.AccessRequest{Identity: domain.AccessIdentity{Issuer: identity.Issuer, Subject: identity.Subject},
			WorkspaceID: workspace, RepositoryID: repository}
		mux.ServeHTTP(w, r.WithContext(domain.WithAccess(r.Context(), access)))
	}))
}

func scopeHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) > 1 {
		return "", false
	}
	value := r.Header.Get(name)
	if len(value) > 128 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return "", false
	}
	return value, true
}
