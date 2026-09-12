package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

type changeLister interface {
	List(context.Context, string, string, int) (domain.ChangePage, error)
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	if _, err := actor(r); err != nil {
		reject(w, r, http.StatusUnauthorized, "authentication_required", err.Error())
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	limit := domain.DefaultHistoryPageSize
	for key, values := range query {
		if (key != "repository" && key != "before" && key != "limit") || len(values) != 1 {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
		if key == "limit" {
			value, err := strconv.Atoi(values[0])
			if err != nil {
				fail(w, r, domain.ErrInvalidInput)
				return
			}
			limit = value
		}
	}
	repository, before := query.Get("repository"), query.Get("before")
	if _, err := domain.ValidateChangeQuery(repository, before, limit); err != nil {
		fail(w, r, err)
		return
	}
	lister, ok := a.service.(changeLister)
	if !ok {
		fail(w, r, domain.ErrUnavailable)
		return
	}
	page, err := lister.List(r.Context(), repository, before, limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, page)
}
