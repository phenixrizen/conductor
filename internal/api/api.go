package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type packageService interface {
	Create(context.Context, string, domain.Content) (domain.Package, error)
	Get(context.Context, string) (domain.Package, error)
	Revise(context.Context, string, string, int64, domain.Content) (domain.Package, error)
	Submit(context.Context, string, string, int64) (domain.Package, error)
	Approve(context.Context, string, string, int64, string) (domain.Package, error)
}

type API struct {
	service        packageService
	historyService historyService
}

func New(s packageService) http.Handler {
	a := &API{service: s}
	a.historyService, _ = s.(historyService)
	mux := routes(a)
	mux.HandleFunc("GET /api/v1/auth/config", authenticationConfig("local", false))
	return requestID(mux)
}

func routes(a *API) *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/changes", a.list)
	m.HandleFunc("POST /api/v1/changes", a.create)
	m.HandleFunc("GET /api/v1/changes/{id}", a.get)
	m.HandleFunc("GET /api/v1/changes/{id}/history", a.history)
	m.HandleFunc("GET /api/v1/changes/{id}/revisions/{revision}", a.revision)
	m.HandleFunc("GET /api/v1/changes/{id}/events", a.events)
	m.HandleFunc("POST /api/v1/changes/{id}/revisions", a.revise)
	m.HandleFunc("POST /api/v1/changes/{id}/review-requests", a.submit)
	m.HandleFunc("POST /api/v1/changes/{id}/approvals", a.approve)
	return m
}

func actor(r *http.Request) (string, error) {
	if _, ok := domain.AccessFromContext(r.Context()); ok {
		// AuthenticatedService derives the durable actor inside its authorization
		// transaction. This marker is never stored as an author or reviewer.
		return "authenticated", nil
	}
	v := strings.TrimSpace(r.Header.Get("X-Conductor-Actor"))
	if v == "" {
		return "", errors.New("X-Conductor-Actor is required (local development only)")
	}
	if len(v) > 128 || strings.ContainsAny(v, "\r\n\x00") {
		return "", errors.New("X-Conductor-Actor is invalid")
	}
	return v, nil
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func reject(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	write(w, status, map[string]any{"error": map[string]string{
		"code": code, "message": message, "correlationId": r.Header.Get("X-Correlation-ID"),
	}})
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	message := err.Error()
	switch {
	case errors.Is(err, domain.ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "authentication_required"
	case errors.Is(err, domain.ErrForbidden):
		status, code = http.StatusForbidden, "permission_denied"
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrStaleApproval):
		status, code = http.StatusConflict, "revision_conflict"
	case errors.Is(err, domain.ErrSelfApproval), errors.Is(err, domain.ErrNotSubmitted):
		status, code = http.StatusUnprocessableEntity, "approval_rejected"
	case errors.Is(err, domain.ErrInvalidInput):
		status, code = http.StatusBadRequest, "invalid_input"
	case errors.Is(err, domain.ErrUnavailable):
		status, code = http.StatusServiceUnavailable, "service_unavailable"
	}
	if status == http.StatusInternalServerError {
		slog.Error("request failed", "error", err, "correlation_id", r.Header.Get("X-Correlation-ID"))
		message = "internal server error"
	}
	reject(w, r, status, code, message)
}

// Package commands and current inspection take their scope from headers and
// consequential inputs from the command body. Reject extra query parameters so
// a caller cannot mistake an ignored URL value for the scope or revision used.
func noQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" {
		fail(w, r, domain.ErrInvalidInput)
		return false
	}
	return true
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	u, err := actor(r)
	if err != nil {
		reject(w, r, http.StatusUnauthorized, "authentication_required", err.Error())
		return
	}
	if !noQuery(w, r) {
		return
	}
	var in struct {
		Content domain.Content `json:"content"`
	}
	if err = decode(w, r, &in); err != nil {
		reject(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	p, err := a.service.Create(r.Context(), u, in.Content)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, p)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	if _, err := actor(r); err != nil {
		reject(w, r, http.StatusUnauthorized, "authentication_required", err.Error())
		return
	}
	if !noQuery(w, r) {
		return
	}
	p, err := a.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, p)
}

func (a *API) revise(w http.ResponseWriter, r *http.Request) {
	u, err := actor(r)
	if err != nil {
		reject(w, r, http.StatusUnauthorized, "authentication_required", err.Error())
		return
	}
	if !noQuery(w, r) {
		return
	}
	var in struct {
		ExpectedRevision int64          `json:"expectedRevision"`
		Content          domain.Content `json:"content"`
	}
	if err = decode(w, r, &in); err != nil {
		reject(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	p, err := a.service.Revise(r.Context(), r.PathValue("id"), u, in.ExpectedRevision, in.Content)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, p)
}

func (a *API) submit(w http.ResponseWriter, r *http.Request) {
	u, err := actor(r)
	if err != nil {
		reject(w, r, http.StatusUnauthorized, "authentication_required", err.Error())
		return
	}
	if !noQuery(w, r) {
		return
	}
	var in struct {
		Revision int64 `json:"revision"`
	}
	if err = decode(w, r, &in); err != nil {
		reject(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	p, err := a.service.Submit(r.Context(), r.PathValue("id"), u, in.Revision)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, p)
}

func (a *API) approve(w http.ResponseWriter, r *http.Request) {
	u, err := actor(r)
	if err != nil {
		reject(w, r, http.StatusUnauthorized, "authentication_required", err.Error())
		return
	}
	if !noQuery(w, r) {
		return
	}
	var in struct {
		Revision int64  `json:"revision"`
		Digest   string `json:"digest"`
	}
	if err = decode(w, r, &in); err != nil {
		reject(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	p, err := a.service.Approve(r.Context(), r.PathValue("id"), u, in.Revision, in.Digest)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusCreated, p)
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Correlation-ID")
		if id == "" {
			var b [16]byte
			if _, err := rand.Read(b[:]); err == nil {
				id = hex.EncodeToString(b[:])
			} else {
				id = strconv.FormatInt(time.Now().UnixNano(), 36)
			}
		}
		r.Header.Set("X-Correlation-ID", id)
		w.Header().Set("X-Correlation-ID", id)
		next.ServeHTTP(w, r)
	})
}
