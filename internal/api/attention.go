package api

import (
	"net/http"

	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/signal"
)

type attentionRequest struct {
	State   string           `json:"state"`
	Message string           `json:"message"`
	Kind    string           `json:"kind"`
	Options []session.Option `json:"options"`
}

// agentTokenOK reports whether the presented token is the session's agent token.
func agentTokenOK(d session.Driver, tok string) bool {
	switch drv := d.(type) {
	case *session.Local:
		return drv.AgentTokenOK(tok)
	case *signal.HostedSession:
		return drv.AgentTokenOK(tok)
	}
	return false
}

// handleAttention lets the agent running in a session (or an admin) report
// whether it is waiting for a human.
func (s *Server) handleAttention(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, ok := s.registry.Get(id)
	if !ok {
		if !s.limiter.allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	tok := presentedToken(r, false)
	p := s.authenticate(r, false)
	source := session.SourceAPI
	if p.admin {
		source = session.SourceAdmin
	} else if !agentTokenOK(d, tok) {
		if !s.limiter.allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "agent or admin token required")
		return
	}
	var req attentionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	state := session.AttentionState(req.State)
	if req.State == "clear" {
		state = session.AttentionNone
	}
	if !state.Valid() || req.State == "" {
		writeError(w, http.StatusBadRequest, "invalid_state", "state must be needs_input, working, done or clear")
		return
	}
	if len(req.Message) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_request", "message too long")
		return
	}
	if !session.ValidKind(req.Kind) {
		writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be permission, prompt, done or empty")
		return
	}
	if len(req.Options) > 32 {
		writeError(w, http.StatusBadRequest, "invalid_request", "too many options")
		return
	}
	if d.Info().Status.Ended() {
		writeError(w, http.StatusConflict, "session_ended", "the session has ended")
		return
	}
	switch drv := d.(type) {
	case *session.Local:
		drv.SetAttentionFull(state, req.Message, source, req.Kind, req.Options)
	case *signal.HostedSession:
		drv.SetAttentionFull(state, req.Message, source, req.Kind, req.Options, true)
	}
	writeJSON(w, http.StatusOK, map[string]any{"attention": d.Info().Attention})
}
