package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

type trackerService interface {
	GetTracker(context.Context) (domain.TrackerSettings, error)
	CreateTrackerLink(context.Context, string, domain.TrackerLinkInput) (domain.TrackerLink, error)
	GetTrackerLink(context.Context, string) (domain.TrackerLink, error)
	ListTrackerLinks(context.Context, string, int) (domain.TrackerLinkPage, error)
	RequestTrackerSync(context.Context, string, string, domain.TrackerSyncInput) (domain.TrackerSync, error)
	GetTrackerSync(context.Context, string) (domain.TrackerSync, error)
}

func (a *API) tracker(w http.ResponseWriter, r *http.Request) (trackerService, bool) {
	if _, ok := domain.AccessFromContext(r.Context()); !ok {
		fail(w, r, domain.ErrUnavailable)
		return nil, false
	}
	s, ok := a.service.(trackerService)
	if !ok {
		fail(w, r, domain.ErrUnavailable)
	}
	return s, ok
}
func trackerKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || domain.ValidateCollectionKey(keys[0]) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return "", false
	}
	return keys[0], true
}
func (a *API) getTracker(w http.ResponseWriter, r *http.Request) {
	s, ok := a.tracker(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	v, e := s.GetTracker(r.Context())
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 200, v)
}
func (a *API) createTrackerLink(w http.ResponseWriter, r *http.Request) {
	s, ok := a.tracker(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	key, ok := trackerKey(w, r)
	if !ok {
		return
	}
	var in *domain.TrackerLinkInput
	if decodeStrictCommand(w, r, &in) != nil || in == nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	v, e := s.CreateTrackerLink(r.Context(), key, *in)
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 201, v)
}
func (a *API) getTrackerLink(w http.ResponseWriter, r *http.Request) {
	s, ok := a.tracker(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	v, e := s.GetTrackerLink(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 200, v)
}
func (a *API) listTrackerLinks(w http.ResponseWriter, r *http.Request) {
	s, ok := a.tracker(w, r)
	if !ok {
		return
	}
	q, e := graphQueryValues(r, "before", "limit")
	if e != nil {
		fail(w, r, e)
		return
	}
	limit := 20
	if q.Has("limit") {
		limit, e = strconv.Atoi(q.Get("limit"))
		if e != nil {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
	}
	v, e := s.ListTrackerLinks(r.Context(), q.Get("before"), limit)
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 200, v)
}
func (a *API) requestTrackerSync(w http.ResponseWriter, r *http.Request) {
	s, ok := a.tracker(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	key, ok := trackerKey(w, r)
	if !ok {
		return
	}
	var in *domain.TrackerSyncInput
	if decodeStrictCommand(w, r, &in) != nil || in == nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	v, e := s.RequestTrackerSync(r.Context(), r.PathValue("id"), key, *in)
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 202, v)
}
func (a *API) getTrackerSync(w http.ResponseWriter, r *http.Request) {
	s, ok := a.tracker(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	v, e := s.GetTrackerSync(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 200, v)
}
