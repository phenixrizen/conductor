package api

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
	"net/url"
	"strconv"
)

type coordinationService interface {
	ExecutionCapabilities(context.Context) (domain.ExecutionCapabilities, error)
	CreateCoordination(context.Context, string, domain.CoordinationPlan) (domain.CoordinationRun, error)
	GetCoordination(context.Context, string) (domain.CoordinationRun, error)
	ListCoordinations(context.Context, string, int) (domain.CoordinationPage, error)
	AuthorizeCoordination(context.Context, string, string) (domain.CoordinationRun, error)
	CancelCoordination(context.Context, string, string) (domain.CoordinationRun, error)
}

func (a *API) executionCapabilities(w http.ResponseWriter, r *http.Request) {
	s, ok := a.coordination(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	caps, err := s.ExecutionCapabilities(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, caps)
}

func (a *API) coordination(w http.ResponseWriter, r *http.Request) (coordinationService, bool) {
	if _, ok := domain.AccessFromContext(r.Context()); !ok {
		fail(w, r, domain.ErrUnavailable)
		return nil, false
	}
	s, ok := a.service.(coordinationService)
	if !ok {
		fail(w, r, domain.ErrUnavailable)
	}
	return s, ok
}
func (a *API) createCoordination(w http.ResponseWriter, r *http.Request) {
	s, ok := a.coordination(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || domain.ValidateCollectionKey(keys[0]) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	var plan *domain.CoordinationPlan
	if err := decodeStrictCommand(w, r, &plan); err != nil || plan == nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	run, err := s.CreateCoordination(r.Context(), keys[0], *plan)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, run)
}
func (a *API) getCoordination(w http.ResponseWriter, r *http.Request) {
	s, ok := a.coordination(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	run, err := s.GetCoordination(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, run)
}
func (a *API) listCoordinations(w http.ResponseWriter, r *http.Request) {
	s, ok := a.coordination(w, r)
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
	page, err := s.ListCoordinations(r.Context(), query.Get("before"), limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, page)
}
func (a *API) authorizeCoordination(w http.ResponseWriter, r *http.Request) {
	a.mutateCoordination(w, r, false)
}
func (a *API) cancelCoordination(w http.ResponseWriter, r *http.Request) {
	a.mutateCoordination(w, r, true)
}
func (a *API) mutateCoordination(w http.ResponseWriter, r *http.Request, cancel bool) {
	s, ok := a.coordination(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	var input *struct {
		Digest string `json:"digest"`
	}
	if err := decodeStrictCommand(w, r, &input); err != nil || input == nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	var run domain.CoordinationRun
	var err error
	if cancel {
		run, err = s.CancelCoordination(r.Context(), r.PathValue("id"), input.Digest)
	} else {
		run, err = s.AuthorizeCoordination(r.Context(), r.PathValue("id"), input.Digest)
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusAccepted, run)
}
