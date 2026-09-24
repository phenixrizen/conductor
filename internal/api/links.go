package api

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
)

type createLinkRequest struct {
	Role       session.Role `json:"role"`
	Label      string       `json:"label"`
	TTLSeconds int64        `json:"ttlSeconds"`
}

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.registry.Get(id); !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": s.links.ListBySession(id)})
}

func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.registry.Get(id); !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	var req createLinkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !req.Role.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_role", "role must be view or control")
		return
	}
	if req.TTLSeconds < 0 || req.TTLSeconds > 365*24*3600 {
		writeError(w, http.StatusBadRequest, "invalid_request", "ttlSeconds out of range")
		return
	}
	link, token, err := s.links.Create(id, req.Role, req.Label, time.Duration(req.TTLSeconds)*time.Second)
	if err != nil {
		if errors.Is(err, share.ErrTooManyLinks) {
			writeError(w, http.StatusConflict, "too_many_links", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "could not create link")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"link":  link,
		"token": token,
		"url":   s.cfg.PublicURL + "/join/" + url.PathEscape(token),
	})
}

func (s *Server) handleRevokeLink(w http.ResponseWriter, r *http.Request) {
	id, linkID := r.PathValue("id"), r.PathValue("linkId")
	if !s.links.Revoke(id, linkID) {
		writeError(w, http.StatusNotFound, "not_found", "no such link")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleJoin resolves a share token for the join page.
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	link, err := s.links.Resolve(r.PathValue("token"))
	if err != nil {
		code := "invalid_link"
		switch {
		case errors.Is(err, share.ErrRevoked):
			code = "revoked"
		case errors.Is(err, share.ErrExpired):
			code = "expired"
		}
		writeError(w, http.StatusNotFound, code, "this link is not valid")
		return
	}
	d, ok := s.registry.Get(link.SessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "session_gone", "the shared session no longer exists")
		return
	}
	info := d.Info()
	writeJSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"id":       info.ID,
			"name":     info.Name,
			"agentId":  info.AgentID,
			"kind":     info.Kind,
			"status":   info.Status,
			"cols":     info.Cols,
			"rows":     info.Rows,
			"hostName": info.HostName,
		},
		"role":  link.Role,
		"label": link.Label,
	})
}
