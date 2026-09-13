package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

type runtimeEvidenceService interface {
	CreateRuntimeEvidence(context.Context, string, domain.RuntimeInput) (domain.RuntimeEvidence, error)
	GetRuntimeEvidence(context.Context, string) (domain.RuntimeEvidence, error)
	ListRuntimeEvidence(context.Context, string, int) (domain.RuntimePage, error)
}

func (a *API) runtimeEvidence(w http.ResponseWriter, r *http.Request) (runtimeEvidenceService, bool) {
	if _, ok := domain.AccessFromContext(r.Context()); !ok {
		fail(w, r, domain.ErrUnavailable)
		return nil, false
	}
	s, ok := a.service.(runtimeEvidenceService)
	if !ok {
		fail(w, r, domain.ErrUnavailable)
	}
	return s, ok
}
func (a *API) createRuntimeEvidence(w http.ResponseWriter, r *http.Request) {
	s, ok := a.runtimeEvidence(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || domain.ValidateCollectionKey(keys[0]) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	var plan *domain.RuntimeInput
	if err := decodeStrictCommand(w, r, &plan); err != nil || plan == nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	run, err := s.CreateRuntimeEvidence(r.Context(), keys[0], *plan)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusAccepted, run)
}
func (a *API) getRuntimeEvidence(w http.ResponseWriter, r *http.Request) {
	s, ok := a.runtimeEvidence(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	run, err := s.GetRuntimeEvidence(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, run)
}
func (a *API) listRuntimeEvidence(w http.ResponseWriter, r *http.Request) {
	s, ok := a.runtimeEvidence(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	limit := domain.DefaultHistoryPageSize
	for key, values := range query {
		if (key != "before" && key != "limit") || len(values) != 1 {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
		if key == "limit" {
			limit, err = strconv.Atoi(values[0])
			if err != nil {
				fail(w, r, domain.ErrInvalidInput)
				return
			}
		}
	}
	page, err := s.ListRuntimeEvidence(r.Context(), query.Get("before"), limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, page)
}
