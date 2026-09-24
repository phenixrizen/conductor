package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
)

var errShuttingDown = errors.New("server shutting down")

type createSessionRequest struct {
	AgentID string   `json:"agentId"`
	Name    string   `json:"name"`
	Cwd     string   `json:"cwd"`
	Args    []string `json:"args"`
	Cols    uint16   `json:"cols"`
	Rows    uint16   `json:"rows"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.registry.List()})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	agent, ok := s.catalog.Get(req.AgentID)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_agent", "unknown agent id")
		return
	}
	if len(req.Args) > 0 && !agent.AllowArgs {
		writeError(w, http.StatusBadRequest, "args_not_allowed", "this agent does not accept extra arguments")
		return
	}
	if len(req.Args) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_request", "too many arguments")
		return
	}
	for _, a := range req.Args {
		if strings.ContainsRune(a, 0) || len(a) > 4096 {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid argument")
			return
		}
	}
	cwdInput := req.Cwd
	if cwdInput == "" {
		cwdInput = agent.Cwd
	}
	if cwdInput == "" {
		cwdInput = s.cfg.DefaultCwd
	}
	cwd, err := s.resolveCwd(cwdInput)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cwd", err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 120 {
		name = name[:120]
	}
	if name == "" {
		name = fmt.Sprintf("%s #%d", agent.Name, s.nextCounter())
	}
	cols, rows := req.Cols, req.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	if !proto.ValidDimension(cols) || !proto.ValidDimension(rows) {
		writeError(w, http.StatusBadRequest, "invalid_request", "terminal size out of range")
		return
	}
	argv := append(append([]string{}, agent.Command...), req.Args...)
	proc, err := pty.Start(pty.Spec{
		Argv: argv,
		Dir:  cwd,
		Env:  pty.BuildEnv(pty.ParentEnv(), s.cfg.EnvPassthrough, agent.Env),
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		s.log.Warn("session start failed", "agent", agent.ID, "err", err)
		writeError(w, http.StatusBadGateway, "start_failed", "could not start the agent process")
		return
	}
	id := session.NewID()
	info := session.Info{
		ID:        id,
		Name:      name,
		Kind:      session.KindServer,
		AgentID:   agent.ID,
		Command:   argv,
		Cwd:       cwd,
		Status:    session.StatusRunning,
		Cols:      cols,
		Rows:      rows,
		CreatedAt: time.Now().UTC(),
	}
	local := session.NewLocal(info, proc, session.Options{
		ScrollbackBytes: s.cfg.ScrollbackBytes,
		MaxViewers:      s.cfg.MaxViewersPerSession,
		FileView:        string(s.cfg.FileView),
		Transport:       proto.TransportWS,
		Log:             s.log,
	})
	if err := s.registry.Add(local); err != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		_ = local.Stop(ctx)
		writeError(w, http.StatusConflict, "too_many_sessions", "session limit reached")
		return
	}
	s.log.Info("session started", "session", id, "agent", agent.ID, "pid", proc.PID())
	writeJSON(w, http.StatusCreated, local.Info())
}

func (s *Server) nextCounter() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	return s.counter
}

// resolveCwd makes cwd absolute, follows symlinks, and requires it to be an
// existing directory under one of the allowed roots.
func (s *Server) resolveCwd(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", errors.New("invalid working directory")
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.New("working directory does not exist")
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.IsDir() {
		return "", errors.New("working directory is not a directory")
	}
	for _, root := range s.cfg.AllowedRoots {
		rootReal, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if real == rootReal || strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
			return real, nil
		}
	}
	return "", errors.New("working directory is outside the allowed roots")
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := s.authenticate(r, true)
	if p.role(id) == "" {
		if !s.limiter.allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "no access to this session")
		return
	}
	d, ok := s.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	out := map[string]any{"session": d.Info(), "role": p.role(id)}
	if p.admin {
		out["links"] = s.links.ListBySession(id)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteSession stops a running session; a second DELETE on an ended
// session removes it from the listing.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, ok := s.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	if d.Info().Status.Ended() {
		s.registry.Remove(id)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
	defer cancel()
	if err := d.Stop(ctx); err != nil {
		s.log.Warn("stop failed", "session", id, "err", err)
		writeError(w, http.StatusInternalServerError, "stop_failed", "could not stop the session")
		return
	}
	writeJSON(w, http.StatusOK, d.Info())
}
