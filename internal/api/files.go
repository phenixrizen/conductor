package api

import (
	"net/http"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/store"
)

// fileDeny returns the directories no file read may enter, even inside a
// session's working directory: the data directory, whose catalog.json holds
// the agents' env secrets. It names both the configured directory and the one
// st writes to, should the two ever differ.
func fileDeny(cfg *config.Config, st *store.Store) []string {
	var dirs []string
	if cfg.DataDir != "" {
		dirs = append(dirs, cfg.DataDir)
	}
	if st != nil && st.Dir() != cfg.DataDir {
		dirs = append(dirs, st.Dir())
	}
	return dirs
}

// handleGetFile reads a file from a server-hosted session's working directory
// under the same rules as the in-band file_get message.
func (s *Server) handleGetFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := s.authenticate(r, true)
	role := p.role(id)
	if role == "" {
		if !s.limiter.allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "no access to this session")
		return
	}
	switch s.cfg.FileView {
	case config.FileViewOff:
		writeError(w, http.StatusForbidden, "file_denied", "file viewing is disabled")
		return
	case config.FileViewControl:
		if role != session.RoleControl {
			writeError(w, http.StatusForbidden, "file_denied", "file viewing requires the control role")
			return
		}
	}
	d, ok := s.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	if d.Info().Kind != session.KindServer {
		writeError(w, http.StatusBadRequest, "unsupported", "files of hosted sessions are read through the terminal connection")
		return
	}
	path := r.URL.Query().Get("path")
	if len(path) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_request", "path too long")
		return
	}
	h, body := session.ReadPath(d.Info().Cwd, path, r.URL.Query().Get("stat") == "1", s.fileDeny)
	status := http.StatusOK
	if h.Kind == "error" {
		switch h.Error.Code {
		case "not_found":
			status = http.StatusNotFound
		case "denied":
			status = http.StatusForbidden
		default:
			status = http.StatusBadRequest
		}
	}
	if r.URL.Query().Get("raw") == "1" && h.Kind == "file" && !h.Binary {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "inline")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return
	}
	writeJSON(w, status, map[string]any{"file": h, "content": string(body)})
}
