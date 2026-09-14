package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

type assistanceService interface {
	RequestDesignAssistance(context.Context, string, domain.AssistanceInput) (domain.DesignAssistance, error)
	ListDesignAssistance(context.Context, domain.AssistanceListOptions) (domain.AssistancePage, error)
	GetDesignAssistance(context.Context, string) (domain.DesignAssistance, error)
	ProposeDesignSections(context.Context, string, string, domain.SuggestionInput) (domain.DesignAssistance, error)
	ApplyDesignSuggestion(context.Context, string, string, domain.ApplySuggestionInput) (domain.DesignAssistance, error)
}

func (a *API) designAssistance(w http.ResponseWriter, r *http.Request) (assistanceService, bool) {
	if _, ok := domain.AccessFromContext(r.Context()); !ok {
		fail(w, r, domain.ErrUnavailable)
		return nil, false
	}
	s, ok := a.service.(assistanceService)
	if !ok {
		fail(w, r, domain.ErrUnavailable)
	}
	return s, ok
}
func assistanceKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || domain.ValidateCollectionKey(keys[0]) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return "", false
	}
	return keys[0], true
}
func (a *API) requestDesignAssistance(w http.ResponseWriter, r *http.Request) {
	s, ok := a.designAssistance(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	key, ok := assistanceKey(w, r)
	if !ok {
		return
	}
	var in domain.AssistanceInput
	if decodeStrictCommand(w, r, &in) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	value, err := s.RequestDesignAssistance(r.Context(), key, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, value)
}
func (a *API) getDesignAssistance(w http.ResponseWriter, r *http.Request) {
	s, ok := a.designAssistance(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	value, err := s.GetDesignAssistance(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, value)
}
func (a *API) listDesignAssistance(w http.ResponseWriter, r *http.Request) {
	s, ok := a.designAssistance(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	in := domain.AssistanceListOptions{ChangeID: query.Get("changeId"), Before: query.Get("before"), Limit: domain.DefaultHistoryPageSize}
	for key, values := range query {
		if (key != "changeId" && key != "before" && key != "limit") || len(values) != 1 {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
		if key == "limit" {
			in.Limit, err = strconv.Atoi(values[0])
			if err != nil {
				fail(w, r, domain.ErrInvalidInput)
				return
			}
		}
	}
	page, err := s.ListDesignAssistance(r.Context(), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, page)
}
func (a *API) proposeDesignSections(w http.ResponseWriter, r *http.Request) {
	s, ok := a.designAssistance(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	key, ok := assistanceKey(w, r)
	if !ok {
		return
	}
	var in domain.SuggestionInput
	if decodeStrictCommand(w, r, &in) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	value, err := s.ProposeDesignSections(r.Context(), r.PathValue("id"), key, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, value)
}
func (a *API) applyDesignSuggestion(w http.ResponseWriter, r *http.Request) {
	s, ok := a.designAssistance(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	key, ok := assistanceKey(w, r)
	if !ok {
		return
	}
	var in domain.ApplySuggestionInput
	if decodeStrictCommand(w, r, &in) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	value, err := s.ApplyDesignSuggestion(r.Context(), r.PathValue("id"), key, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, value)
}
