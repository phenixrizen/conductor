package api

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
)

func (a *API) executionProfiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := domain.AccessFromContext(r.Context()); !ok {
		fail(w, r, domain.ErrUnavailable)
		return
	}
	s, ok := a.service.(interface {
		ExecutionProfiles(context.Context) (domain.ExecutionProfilePage, error)
	})
	if !ok {
		fail(w, r, domain.ErrUnavailable)
		return
	}
	if !noQuery(w, r) {
		return
	}
	page, err := s.ExecutionProfiles(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, page)
}
