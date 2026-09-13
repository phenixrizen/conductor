package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/domain"
)

type collectionService interface {
	CreateCollection(context.Context, string, domain.CollectionInput) (domain.Collection, error)
	GetCollection(context.Context, string) (domain.Collection, error)
	ListCollections(context.Context, string, int) (domain.CollectionPage, error)
	CancelCollection(context.Context, string) (domain.Collection, error)
	AttachCollection(context.Context, string, int64, string, string) (domain.Package, error)
}

// Consequential collection inputs have one interpretation. Reject duplicate
// top-level keys before typed decoding rather than silently choosing the last
// commit, receipt digest, or expected revision supplied by a caller.
func decodeCollectionCommand(w http.ResponseWriter, r *http.Request, value any, fields ...string) error {
	var raw json.RawMessage
	if err := decode(w, r, &raw); err != nil {
		return err
	}
	if !utf8.Valid(raw) {
		return errors.New("collection command must be UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("collection command must be a JSON object")
	}
	seen := make(map[string]bool)
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[field] = true
	}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] || !allowed[key] {
			return errors.New("collection command fields must be known and occur once")
		}
		seen[key] = true
		var field json.RawMessage
		if err := d.Decode(&field); err != nil {
			return err
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(value)
}

func (a *API) collections(w http.ResponseWriter, r *http.Request) (collectionService, bool) {
	// A local actor is not authority to use operator-held provider access. Keep
	// the unavailable mode explicit even when a service implements this interface.
	if _, ok := domain.AccessFromContext(r.Context()); !ok {
		fail(w, r, domain.ErrUnavailable)
		return nil, false
	}
	s, ok := a.service.(collectionService)
	if !ok {
		fail(w, r, domain.ErrUnavailable)
	}
	return s, ok
}

func (a *API) createCollection(w http.ResponseWriter, r *http.Request) {
	s, ok := a.collections(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || domain.ValidateCollectionKey(keys[0]) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	var input *domain.CollectionInput
	if err := decodeCollectionCommand(w, r, &input, "commit", "paths"); err != nil || input == nil {
		reject(w, r, http.StatusBadRequest, "invalid_request", "a collection object with commit and paths is required")
		return
	}
	value, err := s.CreateCollection(r.Context(), keys[0], *input)
	if err != nil {
		fail(w, r, err)
		return
	}
	// Acceptance proves a committed request, not a completed provider operation.
	write(w, http.StatusAccepted, value)
}

func (a *API) listCollections(w http.ResponseWriter, r *http.Request) {
	s, ok := a.collections(w, r)
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
	if limit < 1 || limit > domain.MaxHistoryPageSize || len(query.Get("before")) > 1024 {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	value, err := s.ListCollections(r.Context(), query.Get("before"), limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, value)
}

func (a *API) getCollection(w http.ResponseWriter, r *http.Request) {
	s, ok := a.collections(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	value, err := s.GetCollection(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, value)
}

func (a *API) cancelCollection(w http.ResponseWriter, r *http.Request) {
	s, ok := a.collections(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	var input *struct{}
	if err := decodeCollectionCommand(w, r, &input); err != nil || input == nil {
		reject(w, r, http.StatusBadRequest, "invalid_request", "cancellation requires an empty JSON object")
		return
	}
	value, err := s.CancelCollection(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusAccepted, value)
}

func (a *API) attachCollection(w http.ResponseWriter, r *http.Request) {
	s, ok := a.collections(w, r)
	if !ok || !noQuery(w, r) {
		return
	}
	var input *struct {
		ExpectedRevision int64  `json:"expectedRevision"`
		CollectionID     string `json:"collectionId"`
		Digest           string `json:"digest"`
	}
	if err := decodeCollectionCommand(w, r, &input, "expectedRevision", "collectionId", "digest"); err != nil || input == nil {
		reject(w, r, http.StatusBadRequest, "invalid_request", "an attachment object with expectedRevision, collectionId, and digest is required")
		return
	}
	// The service resolves the already-inspected receipt inside the revision
	// transaction. This command must never collect or refresh source implicitly.
	value, err := s.AttachCollection(r.Context(), r.PathValue("id"), input.ExpectedRevision, input.CollectionID, input.Digest)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, value)
}
