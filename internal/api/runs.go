package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/crew"
	"github.com/phenixrizen/conductor/internal/session"
)

const (
	// runTimeout bounds a launch or a member start: making the worktrees and
	// starting the sessions. It runs to its end when the client goes away:
	// the run is the server's, and GET /api/runs finds it. The prompts are
	// typed afterwards, on the run's own context.
	runTimeout = 2 * time.Minute
	// maxMemberBody bounds the body of POST /api/runs/{run}/members: a member
	// at the crew limits, a prompt of 4000 characters and 8 KiB of arguments,
	// takes up to about 72 KiB of JSON.
	maxMemberBody = 128 << 10
)

// Launch starts the session of a crew member through createLocalSession, the
// path POST /api/sessions takes (crew.Launcher). An error is an *apiError.
func (s *Server) Launch(ctx context.Context, agentID, name, cwd string, args []string, env map[string]string, ref session.CrewRef) (*session.Local, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	local, aerr := s.createLocalSession(createSessionRequest{AgentID: agentID, Name: name, Cwd: cwd, Args: args, Env: env}, &ref)
	if aerr != nil {
		return nil, aerr
	}
	return local, nil
}

// lookupLocal finds a server session in the registry, for the run engine.
func (s *Server) lookupLocal(id string) (*session.Local, bool) {
	d, ok := s.registry.Get(id)
	if !ok {
		return nil, false
	}
	l, ok := d.(*session.Local)
	return l, ok
}

// checkLaunch reports the first member whose agent the catalog lacks, or who
// is given arguments its agent does not take, so that a launch refuses it
// before any session starts. The error matches crew.ErrInvalid.
func checkLaunch(members []crew.Member, cat catalog.Catalog) error {
	if err := (crew.Crew{Members: members}).CheckAgents(cat); err != nil {
		return err
	}
	for _, m := range members {
		if a, _ := cat.Get(m.AgentID); len(m.Args) > 0 && !a.AllowArgs {
			return fmt.Errorf("%w: member %q: agent %q takes no extra arguments", crew.ErrInvalid, m.Name, m.AgentID)
		}
	}
	return nil
}

// runContext is the context of a launch or a start: the request's values,
// not its end, bounded by runTimeout.
func runContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), runTimeout)
}

// handleLaunchCrew launches a saved crew as a run: 201 {run} once the session
// of every member that starts immediately exists; each prompt is typed once
// its session is ready.
func (s *Server) handleLaunchCrew(w http.ResponseWriter, r *http.Request) {
	if s.crews == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	c, ok := s.crews.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such crew")
		return
	}
	if err := c.Launchable(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
		return
	}
	if err := checkLaunch(c.Members, s.Catalog()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
		return
	}
	cwd, err := s.resolveCwd(cmp.Or(c.Cwd, s.cfg.DefaultCwd))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cwd", err.Error())
		return
	}
	c.Cwd = cwd
	ctx, cancel := runContext(r)
	defer cancel()
	run, err := s.runs.Launch(ctx, c)
	if err != nil {
		s.runError(w, "launch", c.ID, err)
		return
	}
	s.log.Info("crew launched", "crew", c.ID, "run", run.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"run": run})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"runs": s.runs.List()})
}

// handleGetRun answers one run with the diff of every member's branch.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runs.GetWithDiffs(r.Context(), r.PathValue("run"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

// handleAddRunMember adds a member to a run: 201 {run}. A member that starts
// immediately has its session by then.
func (s *Server) handleAddRunMember(w http.ResponseWriter, r *http.Request) {
	var m crew.Member
	if err := decodeJSONLimit(w, r, &m, maxMemberBody); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := m.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
		return
	}
	if err := checkLaunch([]crew.Member{m}, s.Catalog()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
		return
	}
	id := r.PathValue("run")
	ctx, cancel := runContext(r)
	defer cancel()
	if err := s.runs.AddMember(ctx, id, m); err != nil {
		s.runError(w, "add member", id, err)
		return
	}
	s.writeRun(w, http.StatusCreated, id)
}

// handleStartRunMember starts a pending member by hand: 200 {run}.
func (s *Server) handleStartRunMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run")
	ctx, cancel := runContext(r)
	defer cancel()
	if err := s.runs.StartMember(ctx, id, r.PathValue("name")); err != nil {
		s.runError(w, "start member", id, err)
		return
	}
	s.writeRun(w, http.StatusOK, id)
}

// handleStopRun stops every member of a run: 200 {run}, marked stopped. The
// worktrees stay. A session that did not stop cleanly is logged; the run is
// stopped all the same.
func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run")
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	switch err := s.runs.Stop(ctx, id); {
	case errors.Is(err, crew.ErrRunNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	case err != nil:
		s.log.Warn("run stop: a session did not stop cleanly", "run", id, "err", err)
	}
	s.log.Info("run stopped", "run", id)
	s.writeRun(w, http.StatusOK, id)
}

// writeRun answers the run with the given ID, or 404 when it is gone.
func (s *Server) writeRun(w http.ResponseWriter, status int, id string) {
	run, ok := s.runs.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	}
	writeJSON(w, status, map[string]any{"run": run})
}

// runError answers an error of the run engine. A member's session that could
// not be created answers as POST /api/sessions would, its message naming the
// member; anything else unforeseen is 500 launch_failed.
func (s *Server) runError(w http.ResponseWriter, what, id string, err error) {
	var aerr *apiError
	switch {
	case errors.Is(err, crew.ErrRunNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such run")
	case errors.Is(err, crew.ErrMemberNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such member in the run")
	case errors.Is(err, crew.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
	case errors.Is(err, crew.ErrNotRepo):
		writeError(w, http.StatusConflict, "not_a_repo", err.Error())
	case errors.Is(err, crew.ErrMemberStarted):
		writeError(w, http.StatusConflict, "member_started", err.Error())
	case errors.Is(err, crew.ErrRunStopped):
		writeError(w, http.StatusConflict, "run_stopped", err.Error())
	case errors.As(err, &aerr):
		writeError(w, aerr.status, aerr.Code, err.Error())
	default:
		s.log.Warn("crew "+what+" failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "launch_failed", err.Error())
	}
}
