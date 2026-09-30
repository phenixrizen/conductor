package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/crew"
	"github.com/phenixrizen/conductor/internal/proto"
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
	// maxBroadcast bounds the text of a broadcast, in bytes, once made one
	// line (broadcastLine).
	maxBroadcast = 4096
)

// Why a broadcast skips a member (broadcastSkip.Reason).
const (
	skipNeedsInput = "needs_input" // its session waits on a prompt, which the text must not answer
	skipNotRunning = "not_running" // it is not running: no session yet, its prompt not typed yet, or ended
	skipUnknown    = "unknown"     // no member of the run has the name
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

// broadcastRequest is the body of POST /api/runs/{run}/broadcast: the text to
// type, the members to type it into (every member when empty) and the display
// name of the admin who sends it.
type broadcastRequest struct {
	Text    string   `json:"text"`
	Members []string `json:"members"`
	ByName  string   `json:"byName"`
}

// broadcastSkip is a member a broadcast was not typed into, and why.
type broadcastSkip struct {
	Member string `json:"member"`
	Reason string `json:"reason"`
}

// broadcastLine is the line a broadcast types, less its carriage return: the
// text with its line breaks and tabs made spaces, as a handoff's message is,
// so that it is one line, without its other control characters, trimmed. A
// handoff's message has had its control characters dropped by the session
// that recorded it; a broadcast's text comes as the client sent it.
func broadcastLine(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, text))
}

// handleBroadcast types a line into the members of a run a request names, or
// into every member: 200 {sent, skipped}, both in the order asked, a name
// given twice typed once. It is typed with a carriage return, as
// Local.TypeUnlessWaiting types, recorded as input by the admin's display
// name. A member whose session waits on a prompt is skipped (needs_input), as
// is one not running (not_running) and a name no member has (unknown).
func (s *Server) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	var req broadcastRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	line := broadcastLine(req.Text)
	switch {
	case line == "":
		writeError(w, http.StatusBadRequest, "invalid_request", "text is empty")
		return
	case len(line) > maxBroadcast || len(line)+1 > proto.MaxInput:
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("text is longer than %d bytes", maxBroadcast))
		return
	}
	run, ok := s.runs.Get(r.PathValue("run"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	}
	names := req.Members
	if len(names) == 0 {
		for _, m := range run.Members {
			names = append(names, m.Name)
		}
	}
	byName := session.CleanName(req.ByName)
	sent, skipped := []string{}, []broadcastSkip{}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if reason := s.broadcastTo(run, name, line+"\r", byName); reason != "" {
			skipped = append(skipped, broadcastSkip{Member: name, Reason: reason})
		} else {
			sent = append(sent, name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "skipped": skipped})
}

// broadcastTo types text into the member of run with the given name, unless
// its session waits on a prompt, and returns why it did not, or "".
func (s *Server) broadcastTo(run crew.Run, name, text, byName string) string {
	i := slices.IndexFunc(run.Members, func(m crew.MemberState) bool { return m.Name == name })
	if i < 0 {
		return skipUnknown
	}
	m := run.Members[i]
	if m.Status != crew.MemberRunning {
		return skipNotRunning
	}
	local, ok := s.lookupLocal(m.SessionID)
	if !ok {
		return skipNotRunning
	}
	typed, err := local.TypeUnlessWaiting(text, byName)
	switch {
	case err != nil:
		// It ended, or the write failed as its process went.
		if !errors.Is(err, session.ErrSessionEnded) {
			s.log.Warn("broadcast: could not type into a member", "run", run.ID, "member", name, "err", err)
		}
		return skipNotRunning
	case !typed:
		return skipNeedsInput
	}
	return ""
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
