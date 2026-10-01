package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
)

var errShuttingDown = errors.New("server shutting down")

type createSessionRequest struct {
	AgentID string   `json:"agentId"`
	Name    string   `json:"name"`
	Cwd     string   `json:"cwd"`
	Args    []string `json:"args"`
	Cols    uint16   `json:"cols"`
	Rows    uint16   `json:"rows"`
	// Env is set in the process over the agent's own variables: the GOAL of
	// a crew member. JSON cannot set it; a client sending "env" is refused
	// like one sending any other unknown field.
	Env map[string]string `json:"-"`
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
	local, aerr := s.createLocalSession(req, nil)
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	writeJSON(w, http.StatusCreated, local.Info())
}

// createLocalSession starts a server session: the agent looked up in the
// catalog, its arguments checked, its working directory through resolveCwd,
// its hooks injected and its environment built, then the session registered
// and announced. It is the one path every server session takes, a crew
// member's too: crewRef, when set, tags the session with its run (Info.Crew)
// and gives its process CONDUCTOR_CREW, CONDUCTOR_RUN and CONDUCTOR_MEMBER.
func (s *Server) createLocalSession(req createSessionRequest, crewRef *session.CrewRef) (*session.Local, *apiError) {
	agent, ok := s.Catalog().Get(req.AgentID)
	if !ok {
		return nil, newAPIError(http.StatusBadRequest, "invalid_agent", "unknown agent id")
	}
	if len(req.Args) > 0 && !agent.AllowArgs {
		return nil, newAPIError(http.StatusBadRequest, "args_not_allowed", "this agent does not accept extra arguments")
	}
	if len(req.Args) > 64 {
		return nil, newAPIError(http.StatusBadRequest, "invalid_request", "too many arguments")
	}
	for _, a := range req.Args {
		if strings.ContainsRune(a, 0) || len(a) > 4096 {
			return nil, newAPIError(http.StatusBadRequest, "invalid_request", "invalid argument")
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
		return nil, newAPIError(http.StatusBadRequest, "invalid_cwd", err.Error())
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
		return nil, newAPIError(http.StatusBadRequest, "invalid_request", "terminal size out of range")
	}
	// An agent with a screen pattern gets the detector. The catalog held the
	// pattern to the same rules when it took the agent in.
	sig := agent.EffectiveSignal()
	var pattern *regexp.Regexp
	if sig.Kind == catalog.SignalPattern {
		if pattern, err = catalog.CompilePattern(sig.Pattern); err != nil {
			return nil, newAPIError(http.StatusInternalServerError, "invalid_agent", "the agent's signal pattern is invalid")
		}
	}
	// An agent that reports through hooks gets them at launch: its adapter's
	// flags after the command and the user's arguments, and its adapter's
	// environment under the agent's own, so a variable the agent sets keeps
	// its value. The hooks are the assets `conductor serve` wrote at startup.
	// What the request sets (a crew's GOAL) goes over both.
	extra, adapterEnv := agents.InjectFor(agent.Adapter, agents.HooksDir(s.cfg.DataDir), sig)
	argv := append(append(append([]string{}, agent.Command...), req.Args...), extra...)
	env := agent.Env
	if len(adapterEnv) > 0 || len(req.Env) > 0 {
		env = maps.Clone(adapterEnv)
		if env == nil {
			env = map[string]string{}
		}
		maps.Copy(env, agent.Env)
		maps.Copy(env, req.Env)
	}
	id := session.NewID()
	agentToken, _ := share.NewToken()
	notifyURL := s.cfg.PublicURL + "/api/sessions/" + id + "/attention"
	// The binary the hooks run, for what the agent runs itself (the skill).
	bin, _ := agents.Binary()
	inject := pty.Inject(id, notifyURL, agentToken, bin)
	if crewRef != nil {
		// BuildEnv keeps CONDUCTOR_* out of env: these travel with Inject's.
		inject["CONDUCTOR_CREW"] = crewRef.CrewID
		inject["CONDUCTOR_RUN"] = crewRef.RunID
		inject["CONDUCTOR_MEMBER"] = crewRef.Member
	}
	// The agent's envPassthrough adds to the server-wide list; the clone keeps
	// one launch from growing the list another launch reads.
	passthrough := append(slices.Clone(s.cfg.EnvPassthrough), agent.EnvPassthrough...)
	proc, err := pty.Start(pty.Spec{
		Argv: argv,
		Dir:  cwd,
		Env:  pty.BuildEnv(pty.ParentEnv(), passthrough, env, inject),
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		s.log.Warn("session start failed", "agent", agent.ID, "err", err)
		return nil, newAPIError(http.StatusBadGateway, "start_failed", "could not start the agent process")
	}
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
		Branch:    session.GitBranch(cwd),
		CreatedAt: time.Now().UTC(),
	}
	if crewRef != nil {
		ref := *crewRef
		info.Crew = &ref
	}
	local := session.NewLocal(info, proc, session.Options{
		ScrollbackBytes: s.cfg.ScrollbackBytes,
		MaxViewers:      s.cfg.MaxViewersPerSession,
		FileView:        string(s.cfg.FileView),
		FileDeny:        s.fileDeny,
		Transport:       proto.TransportWS,
		Log:             s.log,
		OnChange:        s.localChange,
		OnActivity:      s.events.activity,
		Pattern:         pattern,
	})
	local.SetAgentToken(agentToken)
	if err := s.registry.Add(local); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = local.Stop(ctx)
		return nil, newAPIError(http.StatusConflict, "too_many_sessions", "session limit reached")
	}
	s.log.Info("session started", "session", id, "agent", agent.ID, "pid", proc.PID())
	s.events.publish(local.Info())
	return local, nil
}

// localChange is the OnChange hook of a server session: the change goes to
// the event hub, and to the runs, whose waiting handoffs a member's change
// may let go (a prompt cleared records no activity entry). The runs' hook
// never waits.
func (s *Server) localChange(info session.Info) {
	s.events.publish(info)
	s.runs.OnChange(info)
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
