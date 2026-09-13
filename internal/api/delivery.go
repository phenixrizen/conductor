package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

type deliveryService interface {
	GetDeliveryArtifact(context.Context, string) (domain.DeliveryArtifact, error)
	CreateDelivery(context.Context, string, domain.DeliveryInput) (domain.Delivery, error)
	GetDelivery(context.Context, string) (domain.Delivery, error)
	ListDeliveries(context.Context, string, int) (domain.DeliveryPage, error)
	AuthorizeDelivery(context.Context, string, string) (domain.Delivery, error)
	ReconcileDelivery(context.Context, string, string, string) (domain.Delivery, error)
}

func (a *API) delivery(w http.ResponseWriter, r *http.Request) (deliveryService, bool) {
	if _, ok := domain.AccessFromContext(r.Context()); !ok {
		fail(w, r, domain.ErrUnavailable)
		return nil, false
	}
	s, ok := a.service.(deliveryService)
	if !ok {
		fail(w, r, domain.ErrUnavailable)
	}
	return s, ok
}
func (a *API) createDelivery(w http.ResponseWriter, r *http.Request) {
	s, ok := a.delivery(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || domain.ValidateCollectionKey(keys[0]) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	var plan *domain.DeliveryInput
	if err := decodeStrictCommand(w, r, &plan); err != nil || plan == nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	run, err := s.CreateDelivery(r.Context(), keys[0], *plan)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, run)
}
func (a *API) getDelivery(w http.ResponseWriter, r *http.Request) {
	s, ok := a.delivery(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	run, err := s.GetDelivery(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, run)
}
func (a *API) listDeliveries(w http.ResponseWriter, r *http.Request) {
	s, ok := a.delivery(w, r)
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
	page, err := s.ListDeliveries(r.Context(), query.Get("before"), limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, page)
}
func (a *API) authorizeDelivery(w http.ResponseWriter, r *http.Request) {
	a.mutateDelivery(w, r, false)
}
func (a *API) reconcileDelivery(w http.ResponseWriter, r *http.Request) {
	a.mutateDelivery(w, r, true)
}
func (a *API) mutateDelivery(w http.ResponseWriter, r *http.Request, reconcile bool) {
	s, ok := a.delivery(w, r)
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
	var run domain.Delivery
	var err error
	if reconcile {
		if len(r.Header.Values("Idempotency-Key")) != 1 || domain.ValidateCollectionKey(r.Header.Get("Idempotency-Key")) != nil {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
		run, err = s.ReconcileDelivery(r.Context(), r.PathValue("id"), input.Digest, r.Header.Get("Idempotency-Key"))
	} else {
		run, err = s.AuthorizeDelivery(r.Context(), r.PathValue("id"), input.Digest)
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusAccepted, run)
}

func (a *API) getDeliveryArtifact(w http.ResponseWriter, r *http.Request) {
	s, ok := a.delivery(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	value, err := s.GetDeliveryArtifact(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, http.StatusOK, value)
}
