package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

type historyService interface {
	History(context.Context, string, int64, int) (domain.HistoryPage, error)
	Revision(context.Context, string, int64) (domain.RevisionRecord, error)
	Events(context.Context, string, int64, int) (domain.AuditPage, error)
}

func historyQuery(r *http.Request, cursorName string) (int64, int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, 0, domain.ErrInvalidInput
	}
	cursor, limit := int64(0), domain.DefaultHistoryPageSize
	for key, values := range query {
		if (key != cursorName && key != "limit") || len(values) != 1 || values[0] == "" {
			return 0, 0, domain.ErrInvalidInput
		}
		value, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || value < 0 {
			return 0, 0, domain.ErrInvalidInput
		}
		if key == "limit" {
			if value < 1 || value > domain.MaxHistoryPageSize {
				return 0, 0, domain.ErrInvalidInput
			}
			limit = int(value)
		} else {
			cursor = value
		}
	}
	return cursor, limit, nil
}

func (a *API) historyReady(w http.ResponseWriter, r *http.Request) bool {
	if _, err := actor(r); err != nil {
		reject(w, r, http.StatusUnauthorized, "authentication_required", err.Error())
		return false
	}
	if a.historyService == nil {
		fail(w, r, domain.ErrUnavailable)
		return false
	}
	return true
}

func (a *API) history(w http.ResponseWriter, r *http.Request) {
	if !a.historyReady(w, r) {
		return
	}
	cursor, limit, err := historyQuery(r, "beforeRevision")
	if err != nil {
		fail(w, r, err)
		return
	}
	page, err := a.historyService.History(r.Context(), r.PathValue("id"), cursor, limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, page)
}

func (a *API) revision(w http.ResponseWriter, r *http.Request) {
	if !a.historyReady(w, r) {
		return
	}
	revision, err := strconv.ParseInt(r.PathValue("revision"), 10, 64)
	if err != nil || revision < 1 || r.URL.RawQuery != "" {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	record, err := a.historyService.Revision(r.Context(), r.PathValue("id"), revision)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, record)
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	if !a.historyReady(w, r) {
		return
	}
	cursor, limit, err := historyQuery(r, "afterSequence")
	if err != nil {
		fail(w, r, err)
		return
	}
	page, err := a.historyService.Events(r.Context(), r.PathValue("id"), cursor, limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, page)
}
