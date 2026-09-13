package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

type graphService interface {
	CreateRepositoryGraph(context.Context, string, domain.GraphInput) (domain.RepositoryGraph, error)
	GetRepositoryGraph(context.Context, string) (domain.RepositoryGraph, error)
	ListRepositoryGraphs(context.Context, string, int) (domain.RepositoryGraphPage, error)
	QueryRepositoryGraph(context.Context, string, domain.GraphQuery) (domain.GraphQueryResult, error)
}

func (a *API) graphs(w http.ResponseWriter, r *http.Request) (graphService, bool) {
	if _, ok := domain.AccessFromContext(r.Context()); !ok {
		fail(w, r, domain.ErrUnavailable)
		return nil, false
	}
	s, ok := a.service.(graphService)
	if !ok {
		fail(w, r, domain.ErrUnavailable)
	}
	return s, ok
}
func (a *API) createRepositoryGraph(w http.ResponseWriter, r *http.Request) {
	s, ok := a.graphs(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || domain.ValidateCollectionKey(keys[0]) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	var in *domain.GraphInput
	if err := decodeCollectionCommand(w, r, &in, "sources"); err != nil || in == nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	v, err := s.CreateRepositoryGraph(r.Context(), keys[0], *in)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, v)
}
func (a *API) getRepositoryGraph(w http.ResponseWriter, r *http.Request) {
	s, ok := a.graphs(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	v, err := s.GetRepositoryGraph(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, v)
}
func graphQueryValues(r *http.Request, allowed ...string) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, domain.ErrInvalidInput
	}
	for k, v := range q {
		found := false
		for _, a := range allowed {
			if k == a {
				found = true
			}
		}
		if !found || len(v) != 1 {
			return nil, domain.ErrInvalidInput
		}
	}
	return q, nil
}
func (a *API) listRepositoryGraphs(w http.ResponseWriter, r *http.Request) {
	s, ok := a.graphs(w, r)
	if !ok {
		return
	}
	q, err := graphQueryValues(r, "before", "limit")
	if err != nil {
		fail(w, r, err)
		return
	}
	limit := domain.DefaultHistoryPageSize
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
	}
	v, err := s.ListRepositoryGraphs(r.Context(), q.Get("before"), limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, v)
}
func (a *API) queryRepositoryGraph(w http.ResponseWriter, r *http.Request) {
	s, ok := a.graphs(w, r)
	if !ok {
		return
	}
	q, err := graphQueryValues(r, "search", "nodeId", "depth", "limit")
	if err != nil {
		fail(w, r, err)
		return
	}
	input := domain.GraphQuery{Search: q.Get("search"), NodeID: q.Get("nodeId"), Depth: 1, Limit: 100}
	if q.Has("depth") {
		input.Depth, err = strconv.Atoi(q.Get("depth"))
		if err != nil {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
	}
	if q.Has("limit") {
		input.Limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
	}
	v, err := s.QueryRepositoryGraph(r.Context(), r.PathValue("id"), input)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, v)
}
