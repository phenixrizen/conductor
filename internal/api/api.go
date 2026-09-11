package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
)

type API struct{ service *service.Service }

func New(s *service.Service) http.Handler {
	a := &API{service: s}
	m := http.NewServeMux()
	m.HandleFunc("POST /api/v1/changes", a.create)
	m.HandleFunc("GET /api/v1/changes/{id}", a.get)
	m.HandleFunc("POST /api/v1/changes/{id}/revisions", a.revise)
	m.HandleFunc("POST /api/v1/changes/{id}/review-requests", a.submit)
	m.HandleFunc("POST /api/v1/changes/{id}/approvals", a.approve)
	return requestID(m)
}
func actor(r *http.Request) (string, error) {
	v := strings.TrimSpace(r.Header.Get("X-Conductor-Actor"))
	if v == "" {
		return "", errors.New("X-Conductor-Actor is required (local development only)")
	}
	return v, nil
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func write(w http.Response, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.Response, r *http.Request, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
		code = "not_found"
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrStaleApproval):
		status = http.StatusConflict
		code = "revision_conflict"
	case errors.Is(err, domain.ErrSelfApproval), errors.Is(err, domain.ErrNotSubmitted):
		status = http.StatusUnprocessableEntity
		code = "approval_rejected"
	}
	if status == 500 {
		slog.Error("request failed", "error", err, "correlation_id", r.Header.Get("X-Correlation-ID"))
	}
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": err.Error(), "correlationId": r.Header.Get("X-Correlation-ID")}})
}
func (a *API) create(w http.ResponseWriter, r *http.Request) {
	u, e := actor(r)
	if e != nil {
		write(w, 401, map[string]string{"error": e.Error()})
		return
	}
	var in struct {
		Content domain.Content `json:"content"`
	}
	if e = decode(r, &in); e != nil {
		write(w, 400, map[string]string{"error": e.Error()})
		return
	}
	p, e := a.service.Create(r.Context(), u, in.Content)
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 201, p)
}
func (a *API) get(w http.ResponseWriter, r *http.Request) {
	if _, e := actor(r); e != nil {
		write(w, 401, map[string]string{"error": e.Error()})
		return
	}
	p, e := a.service.Get(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 200, p)
}
func (a *API) revise(w http.ResponseWriter, r *http.Request) {
	u, e := actor(r)
	if e != nil {
		write(w, 401, map[string]string{"error": e.Error()})
		return
	}
	var in struct {
		ExpectedRevision int64          `json:"expectedRevision"`
		Content          domain.Content `json:"content"`
	}
	if e = decode(r, &in); e != nil {
		write(w, 400, map[string]string{"error": e.Error()})
		return
	}
	p, e := a.service.Revise(r.Context(), r.PathValue("id"), u, in.ExpectedRevision, in.Content)
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 201, p)
}
func (a *API) submit(w http.ResponseWriter, r *http.Request) {
	u, e := actor(r)
	if e != nil {
		write(w, 401, map[string]string{"error": e.Error()})
		return
	}
	var in struct {
		Revision int64 `json:"revision"`
	}
	if e = decode(r, &in); e != nil {
		write(w, 400, map[string]string{"error": e.Error()})
		return
	}
	p, e := a.service.Submit(r.Context(), r.PathValue("id"), u, in.Revision)
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 200, p)
}
func (a *API) approve(w http.ResponseWriter, r *http.Request) {
	u, e := actor(r)
	if e != nil {
		write(w, 401, map[string]string{"error": e.Error()})
		return
	}
	var in struct {
		Revision int64  `json:"revision"`
		Digest   string `json:"digest"`
	}
	if e = decode(r, &in); e != nil {
		write(w, 400, map[string]string{"error": e.Error()})
		return
	}
	p, e := a.service.Approve(r.Context(), r.PathValue("id"), u, in.Revision, in.Digest)
	if e != nil {
		fail(w, r, e)
		return
	}
	write(w, 201, p)
}
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Correlation-ID")
		if id == "" {
			id = strconv.FormatInt(int64(len(r.URL.Path))+r.ContentLength, 36)
		}
		r.Header.Set("X-Correlation-ID", id)
		w.Header().Set("X-Correlation-ID", id)
		next.ServeHTTP(w, r)
	})
}
