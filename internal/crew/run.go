package crew

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

// A run is one launch of a crew. Every member becomes a server session of
// its own, launched like any other (Launcher) and tagged with the run
// (session.Info.Crew). A member starts when its start condition says: at
// once, once the member it waits for first reports done after its prompt,
// or when asked. Once its session is ready its role prompt is typed into it.
// Runs live in memory, as sessions do: a server restart forgets them.

// Launcher starts the session of a crew member. The API server implements it
// on the path POST /api/sessions takes, so a member's session is launched
// like any other: its working directory checked, its agent looked up in the
// catalog, its environment built and its hooks injected. env holds variables
// to set in the process (GOAL); ref tags the session and gives its process
// CONDUCTOR_CREW, CONDUCTOR_RUN and CONDUCTOR_MEMBER.
type Launcher interface {
	Launch(ctx context.Context, agentID, name, cwd string, args []string, env map[string]string, ref session.CrewRef) (*session.Local, error)
}

// Member states (MemberState.Status).
const (
	MemberPending  = "pending"  // waiting for its start condition, or to be started by hand
	MemberStarting = "starting" // its worktree and session are being made, or it waits to be ready for its prompt
	MemberRunning  = "running"  // its session runs and its prompt has been typed
	MemberEnded    = "ended"    // its session ended, or it could not start (Err says why)
)

// When a member is ready for its prompt (awaitReady).
const (
	readyPoll  = 250 * time.Millisecond // how often it looks
	readyQuiet = time.Second            // output silence after the first output
	readyMin   = 2 * time.Second        // since the wait began, for the silence to count
	readyCap   = 60 * time.Second       // after this the prompt is typed anyway
)

const (
	// maxRunLog bounds a run's log.
	maxRunLog = 200
	// maxRuns is how many runs the engine keeps: a launch past it forgets
	// the oldest run with nothing running.
	maxRuns = 100
	// diffTTL is how long GetWithDiffs keeps a member's diff.
	diffTTL = 10 * time.Second
	// typedBy is the name what a run types into a session is recorded by.
	typedBy = "crew"
	// stopTimeout bounds the stop of what a failed start leaves running.
	stopTimeout = 15 * time.Second
)

// worktreesDir is where a run's worktrees go under the crew's working
// directory, <cwd>/.conductor/worktrees/<run>/<member>, so that they pass
// the check every session's working directory passes.
var worktreesDir = filepath.Join(".conductor", "worktrees")

// Errors of the runs, beside ErrInvalid and ErrNotRepo.
var (
	ErrRunNotFound    = errors.New("no such run")
	ErrMemberNotFound = errors.New("no such member in the run")
	ErrMemberStarted  = errors.New("the member has started already")
	ErrRunStopped     = errors.New("the run is stopped")
	// errNotReady is awaitReady giving up after readyCap.
	errNotReady = errors.New("not ready")
)

// Diff is what a member's branch adds and removes since it began, in lines.
type Diff struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// MemberState is a member of a run as the run reports it.
type MemberState struct {
	Name    string `json:"name"`
	AgentID string `json:"agentId"`
	Start   Start  `json:"start"`
	// SessionID is the member's session once it has started.
	SessionID string `json:"sessionId,omitempty"`
	// Branch and Worktree are set with isolation "worktree" once it starts.
	Branch   string     `json:"branch,omitempty"`
	Worktree string     `json:"worktree,omitempty"`
	Status   string     `json:"status"` // pending | starting | running | ended
	Started  *time.Time `json:"startedAt,omitempty"`
	Ended    *time.Time `json:"endedAt,omitempty"`
	// Err says why a member ended before it ran: it could not start, or its
	// process ended before its prompt was typed.
	Err string `json:"error,omitempty"`
	// Diff is set by GetWithDiffs for a member with a worktree.
	Diff *Diff `json:"diff,omitempty"`
}

// Run is a launch of a crew as the engine reports it.
type Run struct {
	ID        string        `json:"id"`
	CrewID    string        `json:"crewId"`
	Name      string        `json:"name"`
	Goal      string        `json:"goal"`
	Cwd       string        `json:"cwd"`
	Isolation string        `json:"isolation"`
	StartedAt time.Time     `json:"startedAt"`
	StoppedAt *time.Time    `json:"stoppedAt,omitempty"`
	Members   []MemberState `json:"members"`
	// Log is the run's own log, oldest first: launched, member started,
	// prompt typed, stopped. At most maxRunLog entries.
	Log []session.ActivityEntry `json:"log"`
}

// Engine launches crews and runs their runs. It never holds its lock while
// it starts, types into or stops a session.
type Engine struct {
	launcher Launcher
	lookup   func(sessionID string) (*session.Local, bool)
	// await waits for a member's session to be ready: awaitReady, but for tests.
	await func(ctx context.Context, l *session.Local) error
	// now is the clock of the diff cache.
	now func() time.Time

	mu        sync.Mutex
	runs      map[string]*run
	order     []*run               // oldest first
	bySession map[string]memberRef // the member each member session is
}

type memberRef struct{ run, member string }

// run is a launch of a crew. Engine.mu guards what changes after it is made.
type run struct {
	id, crewID, name, goal, cwd, isolation string
	startedAt                              time.Time
	stoppedAt                              *time.Time
	members                                []*member
	log                                    []session.ActivityEntry

	// ctx ends when the run stops; every member start runs under it.
	ctx    context.Context
	cancel context.CancelFunc
	// stopping is set once the run begins to stop: no member starts after it.
	stopping bool
	// starts counts the member starts in flight; a stop waits for them.
	starts sync.WaitGroup
}

type member struct {
	def    Member
	state  MemberState
	base   string // the commit its worktree branched from, for DiffStat
	diff   *Diff
	diffAt time.Time // when diff was read; zero: never
}

// NewEngine returns an engine that starts member sessions with l and finds
// them again with lookup (the server's registry).
func NewEngine(l Launcher, lookup func(sessionID string) (*session.Local, bool)) *Engine {
	return &Engine{launcher: l, lookup: lookup, await: awaitReady, now: time.Now,
		runs: map[string]*run{}, bySession: map[string]memberRef{}}
}

// Launch starts a run of c, whose Cwd the caller has resolved. With isolation
// "worktree" it first checks that Cwd is a repository to make worktrees of
// (ErrNotRepo), before anything is made. It starts the members that start
// immediately, each in a worktree of its own when the crew has them, and
// types their prompts once they are ready. It returns once all of them have
// their prompt, or have ended before it (the run goes on). When one cannot be
// started, the ones started are stopped, no run is kept, and the error names
// the member. A crew with no members or one that runs on a host is not
// launched (ErrInvalid).
func (e *Engine) Launch(ctx context.Context, c Crew) (*Run, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := c.Launchable(); err != nil {
		return nil, err
	}
	if c.Isolation == IsolationWorktree {
		if !filepath.IsAbs(c.Cwd) {
			return nil, invalidf("with worktrees, cwd must be an absolute path")
		}
		if err := CheckRepo(ctx, c.Cwd); err != nil {
			return nil, err
		}
	}
	r := e.add(c)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(r.ctx, cancel)()

	e.mu.Lock()
	var starts []*member
	for _, m := range r.members {
		if m.def.Start.When == StartImmediately && r.reserve(m) {
			starts = append(starts, m)
		}
	}
	e.mu.Unlock()
	results := make(chan error, len(starts))
	for i, m := range starts {
		local, err := e.launch(ctx, r, m)
		if err != nil {
			for range starts[i:] {
				r.starts.Done()
			}
			e.abort(r)
			return nil, fmt.Errorf("member %s: %w", quote(m.def.Name), err)
		}
		go func() {
			defer r.starts.Done()
			if err := e.prompt(ctx, r, m, local); err != nil {
				results <- fmt.Errorf("member %s: %w", quote(m.def.Name), err)
				return
			}
			results <- nil
		}()
	}
	var failed error
	for range starts {
		if err := <-results; err != nil && failed == nil {
			failed = err
		}
	}
	if failed != nil {
		if r.ctx.Err() != nil {
			return nil, ErrRunStopped
		}
		e.abort(r)
		return nil, failed
	}
	out, _ := e.Get(r.id)
	return &out, nil
}

// add makes and keeps a run of c with every member pending.
func (e *Engine) add(c Crew) *run {
	ctx, cancel := context.WithCancel(context.Background())
	r := &run{crewID: c.ID, name: c.Name, goal: c.Goal, cwd: c.Cwd, isolation: c.Isolation,
		startedAt: time.Now().UTC(), ctx: ctx, cancel: cancel}
	immediate := 0
	for _, m := range c.Members {
		r.members = append(r.members, newMember(m))
		if m.Start.When == StartImmediately {
			immediate++
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for r.id == "" || e.runs[r.id] != nil {
		r.id = cmp.Or(c.ID, "run") + "-" + session.NewID()[:8]
	}
	e.evict()
	e.runs[r.id] = r
	e.order = append(e.order, r)
	r.note(session.ActivityStatus, "launched %s: %d members, %d starting now", c.Name, len(c.Members), immediate)
	return r
}

func newMember(m Member) *member {
	m.Args = slices.Clone(m.Args)
	return &member{def: m, state: MemberState{Name: m.Name, AgentID: m.AgentID, Start: m.Start, Status: MemberPending}}
}

// member returns the member of r with the given name. The caller holds e.mu.
func (r *run) member(name string) *member {
	for _, m := range r.members {
		if m.def.Name == name {
			return m
		}
	}
	return nil
}

// reserve marks m starting when it is pending and r takes starts, and counts
// the start in r.starts, which the caller ends with r.starts.Done. The caller
// holds e.mu, so no start is counted once a stop waits for them.
func (r *run) reserve(m *member) bool {
	if r.stopping || m.state.Status != MemberPending {
		return false
	}
	m.state.Status = MemberStarting
	r.starts.Add(1)
	return true
}

// note appends an entry to r's log, cleaned like a session's entries, and
// drops the oldest past maxRunLog. The caller holds e.mu.
func (r *run) note(typ, format string, args ...any) {
	entry := session.CleanEntry(session.ActivityEntry{At: time.Now().UTC(), Type: typ, Message: fmt.Sprintf(format, args...)})
	if len(r.log) >= maxRunLog {
		r.log = slices.Delete(r.log, 0, len(r.log)-maxRunLog+1)
	}
	r.log = append(r.log, entry)
}

// launch makes m's worktree, when r has them, and starts m's session.
func (e *Engine) launch(ctx context.Context, r *run, m *member) (*session.Local, error) {
	name := m.def.Name
	cwd := r.cwd
	if r.isolation == IsolationWorktree {
		path := filepath.Join(r.cwd, worktreesDir, r.id, name)
		branch := "crew/" + r.id + "/" + name
		if err := AddWorktree(ctx, r.cwd, path, branch); err != nil {
			return nil, err
		}
		base, err := headCommit(ctx, path)
		e.mu.Lock()
		m.state.Branch, m.state.Worktree = branch, path
		if err == nil {
			m.base = base
		}
		e.mu.Unlock()
		cwd = path
	}
	ref := session.CrewRef{RunID: r.id, CrewID: r.crewID, Member: name}
	local, err := e.launcher.Launch(ctx, m.def.AgentID, name, cwd, slices.Clone(m.def.Args), map[string]string{"GOAL": r.goal}, ref)
	if err != nil {
		return nil, err
	}
	id := local.Info().ID
	started := time.Now().UTC()
	e.mu.Lock()
	m.state.SessionID, m.state.Started = id, &started
	e.bySession[id] = memberRef{run: r.id, member: name}
	r.note(session.ActivityStatus, "%s started", name)
	stopping := r.stopping
	e.mu.Unlock()
	if stopping {
		// The run began to stop while the session started: the stop may
		// have looked for the member's session before there was one.
		stopLocal(local)
		return nil, ErrRunStopped
	}
	return local, nil
}

// prompt waits for m's session to be ready and types m's prompt, the goal in
// it, and m is running; a member without a prompt runs at once. A session
// that ends first gets no prompt: m ends, with how its process ended. An
// error means the start failed: ctx ended or the prompt could not be written.
func (e *Engine) prompt(ctx context.Context, r *run, m *member, local *session.Local) error {
	name := m.def.Name
	text := ExpandPrompt(m.def.Prompt, r.goal)
	hasPrompt := strings.TrimSpace(text) != ""
	if hasPrompt {
		err := e.await(ctx, local)
		switch {
		case errors.Is(err, errNotReady):
			e.mu.Lock()
			r.note(session.ActivityStatus, "%s was not ready after %v: typing its prompt anyway", name, readyCap)
			e.mu.Unlock()
		case errors.Is(err, session.ErrSessionEnded):
			e.endedEarly(r, m, local)
			return nil
		case err != nil:
			return err
		}
		if err := local.Type(text+"\r", typedBy); err != nil {
			if errors.Is(err, session.ErrSessionEnded) {
				e.endedEarly(r, m, local)
				return nil
			}
			return fmt.Errorf("typing its prompt: %w", err)
		}
	}
	e.mu.Lock()
	m.state.Status = MemberRunning
	if hasPrompt {
		r.note(session.ActivityStatus, "typed %s's prompt", name)
	}
	e.mu.Unlock()
	return nil
}

// endedEarly ends m, whose process ended before its prompt was typed.
func (e *Engine) endedEarly(r *run, m *member, local *session.Local) {
	info := local.Info()
	how := string(info.Status)
	if info.ExitCode != nil {
		how = fmt.Sprintf("%s (exit %d)", info.Status, *info.ExitCode)
	}
	ended := info.EndedAt
	if ended == nil {
		t := time.Now().UTC()
		ended = &t
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	m.state.Status, m.state.Ended = MemberEnded, ended
	m.state.Err = how + " before its prompt was typed"
	r.note(session.ActivityStatus, "%s ended before its prompt was typed: %s", m.def.Name, how)
}

// start runs m's start, which the caller has reserved: its worktree and
// session, then its prompt. It ends the start in r.starts. When it fails m
// ends with the reason, and a session it had is stopped.
func (e *Engine) start(ctx context.Context, r *run, m *member) error {
	defer r.starts.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(r.ctx, cancel)()
	local, err := e.launch(ctx, r, m)
	if err == nil {
		if err = e.prompt(ctx, r, m, local); err != nil {
			stopLocal(local)
		}
	}
	if err == nil {
		return nil
	}
	if r.ctx.Err() != nil {
		err = ErrRunStopped
	}
	e.fail(r, m, err)
	return fmt.Errorf("member %s: %w", quote(m.def.Name), err)
}

// fail ends m, which could not start.
func (e *Engine) fail(r *run, m *member, err error) {
	now := time.Now().UTC()
	e.mu.Lock()
	defer e.mu.Unlock()
	m.state.Status, m.state.Ended = MemberEnded, &now
	m.state.Err = session.CleanMessage(err.Error())
	r.note(session.ActivityError, "%s could not start: %v", m.def.Name, err)
}

// stopLocal stops a session a failed start leaves behind.
func stopLocal(l *session.Local) {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	_ = l.Stop(ctx)
}

// OnActivity takes the activity of every session, as the server's activity
// fan-out hands it with, for an attention entry, the state it records. The
// first done a running member reports starts the pending members that start
// after it; a done before its prompt was typed does not count. It never
// waits: a start runs on a goroutine of its own.
func (e *Engine) OnActivity(sessionID string, entry session.ActivityEntry, state session.AttentionState) {
	if entry.Type != session.ActivityAttention || state != session.AttentionDone {
		return
	}
	e.mu.Lock()
	ref, ok := e.bySession[sessionID]
	r := e.runs[ref.run]
	if !ok || r == nil {
		e.mu.Unlock()
		return
	}
	if done := r.member(ref.member); done == nil || done.state.Status != MemberRunning {
		e.mu.Unlock()
		return
	}
	var next []*member
	var names []string
	for _, m := range r.members {
		if m.def.Start.When == StartAfter && m.def.Start.Member == ref.member && r.reserve(m) {
			next = append(next, m)
			names = append(names, m.def.Name)
		}
	}
	if len(next) > 0 {
		r.note(session.ActivityStatus, "%s is done: starting %s", ref.member, strings.Join(names, ", "))
	}
	e.mu.Unlock()
	for _, m := range next {
		// The outcome is in m's state and the run log.
		go func() { _ = e.start(r.ctx, r, m) }()
	}
}

// StartMember starts a pending member of a run by hand, whatever its start
// condition, and returns once its prompt is typed or it failed.
func (e *Engine) StartMember(ctx context.Context, runID, name string) error {
	e.mu.Lock()
	r, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return ErrRunNotFound
	}
	m := r.member(name)
	var err error
	switch {
	case m == nil:
		err = ErrMemberNotFound
	case r.stopping:
		err = ErrRunStopped
	case !r.reserve(m):
		err = ErrMemberStarted
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	return e.start(ctx, r, m)
}

// AddMember adds m to a run, held to the rules of a crew: a valid member, a
// name no member has, an after condition naming a member of the run, at most
// 12 members (ErrInvalid). A member that starts immediately starts, and
// AddMember returns once its prompt is typed or it failed (it stays in the
// run, ended); one that starts after another waits for that one's next done.
func (e *Engine) AddMember(ctx context.Context, runID string, m Member) error {
	if err := m.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	r, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return ErrRunNotFound
	}
	if err := r.admit(m); err != nil {
		e.mu.Unlock()
		return err
	}
	nm := newMember(m)
	r.members = append(r.members, nm)
	r.note(session.ActivityStatus, "%s joined the run", m.Name)
	start := m.Start.When == StartImmediately && r.reserve(nm)
	e.mu.Unlock()
	if !start {
		return nil
	}
	return e.start(ctx, r, nm)
}

// admit checks that m can join r. The caller holds e.mu.
func (r *run) admit(m Member) error {
	switch {
	case r.stopping:
		return ErrRunStopped
	case len(r.members) >= maxMembers:
		return invalidf("too many members (at most %d)", maxMembers)
	case r.member(m.Name) != nil:
		return invalidf("member %s: the name is used twice", quote(m.Name))
	case m.Start.When != StartAfter:
		return nil
	case m.Start.Member == m.Name:
		return invalidf("member %s: cannot start after itself", quote(m.Name))
	case r.member(m.Start.Member) == nil:
		return invalidf("member %s: starts after %s, which is not a member of this run", quote(m.Name), quote(m.Start.Member))
	}
	return nil
}

// Stop stops a run: no member starts any more, the starts in flight end,
// every member's session is stopped and the run is marked stopped. The
// worktrees stay. Stopping a stopped run again changes nothing.
func (e *Engine) Stop(ctx context.Context, runID string) error {
	e.mu.Lock()
	r, ok := e.runs[runID]
	e.mu.Unlock()
	if !ok {
		return ErrRunNotFound
	}
	return e.stop(ctx, r)
}

func (e *Engine) stop(ctx context.Context, r *run) error {
	e.mu.Lock()
	r.stopping = true
	e.mu.Unlock()
	r.cancel()
	waitStarts(ctx, &r.starts)
	e.mu.Lock()
	var ids []string
	for _, m := range r.members {
		if m.state.SessionID != "" {
			ids = append(ids, m.state.SessionID)
		}
	}
	e.mu.Unlock()
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		l, ok := e.lookup(id)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = l.Stop(ctx)
		}()
	}
	wg.Wait()
	e.mu.Lock()
	if r.stoppedAt == nil {
		now := time.Now().UTC()
		r.stoppedAt = &now
		r.note(session.ActivityStatus, "stopped")
	}
	e.mu.Unlock()
	return errors.Join(errs...)
}

// waitStarts waits for the starts in flight, or for ctx.
func waitStarts(ctx context.Context, wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// abort stops r, which failed to launch, and forgets it. Its worktrees stay.
func (e *Engine) abort(r *run) {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	_ = e.stop(ctx, r)
	e.mu.Lock()
	e.forget(r)
	e.mu.Unlock()
}

// evict forgets the oldest runs with nothing running until there is room for
// one more. A run with a member starting or a live session stays, so the
// engine may hold more than maxRuns while they run. The caller holds e.mu.
func (e *Engine) evict() {
	for len(e.order) >= maxRuns {
		i := slices.IndexFunc(e.order, e.idle)
		if i < 0 {
			return
		}
		e.forget(e.order[i])
	}
}

// idle reports whether nothing runs in r. The caller holds e.mu; it reads the
// sessions, which never call into the engine while they hold their locks.
func (e *Engine) idle(r *run) bool {
	for _, m := range r.members {
		if m.state.Status == MemberStarting {
			return false
		}
		if m.state.SessionID == "" {
			continue
		}
		if l, ok := e.lookup(m.state.SessionID); ok && !l.Info().Status.Ended() {
			return false
		}
	}
	return true
}

// forget drops r and the index of its sessions. The caller holds e.mu.
func (e *Engine) forget(r *run) {
	delete(e.runs, r.id)
	e.order = slices.DeleteFunc(e.order, func(o *run) bool { return o == r })
	for _, m := range r.members {
		if m.state.SessionID != "" {
			delete(e.bySession, m.state.SessionID)
		}
	}
	r.cancel()
}

// MemberOf reports the run and the member a session belongs to.
func (e *Engine) MemberOf(sessionID string) (runID, member string, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ref, ok := e.bySession[sessionID]
	return ref.run, ref.member, ok
}

// Get returns the run with the given ID.
func (e *Engine) Get(runID string) (Run, bool) {
	e.mu.Lock()
	r, ok := e.runs[runID]
	var out Run
	if ok {
		out = r.snapshot(false)
	}
	e.mu.Unlock()
	if !ok {
		return Run{}, false
	}
	e.refresh(&out)
	return out, true
}

// List returns every run, the newest first.
func (e *Engine) List() []Run {
	e.mu.Lock()
	out := make([]Run, 0, len(e.order))
	for i := len(e.order) - 1; i >= 0; i-- {
		out = append(out, e.order[i].snapshot(false))
	}
	e.mu.Unlock()
	for i := range out {
		e.refresh(&out[i])
	}
	return out
}

// GetWithDiffs is Get with the diff of every member that has a worktree: the
// lines its branch adds and removes since it began (DiffStat), read again
// once what was read is diffTTL old.
func (e *Engine) GetWithDiffs(ctx context.Context, runID string) (Run, bool) {
	type read struct {
		m              *member
		worktree, base string
	}
	e.mu.Lock()
	r, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return Run{}, false
	}
	now := e.now()
	var reads []read
	for _, m := range r.members {
		if m.base != "" && (m.diffAt.IsZero() || now.Sub(m.diffAt) >= diffTTL) {
			m.diffAt = now // one reader at a time
			reads = append(reads, read{m, m.state.Worktree, m.base})
		}
	}
	e.mu.Unlock()
	for _, rd := range reads {
		added, removed, err := DiffStat(ctx, rd.worktree, rd.base)
		e.mu.Lock()
		rd.m.diff = nil
		if err == nil {
			rd.m.diff = &Diff{Added: added, Removed: removed}
		}
		e.mu.Unlock()
	}
	e.mu.Lock()
	out := r.snapshot(true)
	e.mu.Unlock()
	e.refresh(&out)
	return out, true
}

// snapshot copies r, with the members' diffs when withDiffs. The caller holds e.mu.
func (r *run) snapshot(withDiffs bool) Run {
	out := Run{ID: r.id, CrewID: r.crewID, Name: r.name, Goal: r.goal, Cwd: r.cwd, Isolation: r.isolation,
		StartedAt: r.startedAt, StoppedAt: r.stoppedAt, Members: make([]MemberState, 0, len(r.members)),
		Log: append([]session.ActivityEntry{}, r.log...)}
	for _, m := range r.members {
		st := m.state
		st.Diff = nil
		if withDiffs && m.diff != nil {
			d := *m.diff
			st.Diff = &d
		}
		out.Members = append(out.Members, st)
	}
	return out
}

// refresh shows as ended the members of out whose session has ended or is
// gone from the server. It looks the sessions up: the caller does not hold e.mu.
func (e *Engine) refresh(out *Run) {
	for i := range out.Members {
		m := &out.Members[i]
		if m.SessionID == "" || m.Status == MemberEnded {
			continue
		}
		l, ok := e.lookup(m.SessionID)
		if !ok {
			m.Status = MemberEnded
			continue
		}
		if info := l.Info(); info.Status.Ended() {
			m.Status, m.Ended = MemberEnded, info.EndedAt
		}
	}
}

// awaitReady waits until l is ready for its prompt (readyAt), looking every
// readyPoll. After readyCap it returns errNotReady, and the prompt is typed
// anyway; once the session has ended it returns session.ErrSessionEnded.
func awaitReady(ctx context.Context, l *session.Local) error {
	start := time.Now()
	tick := time.NewTicker(readyPoll)
	defer tick.Stop()
	for {
		select {
		case <-l.Ended():
			return session.ErrSessionEnded
		default:
		}
		ready, capped := readyAt(l.Info().Attention.State, l.LastOutputAt(), start, time.Now())
		if ready {
			return nil
		}
		if capped {
			return errNotReady
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.Ended():
			return session.ErrSessionEnded
		case <-tick.C:
		}
	}
}

// readyAt says whether a session is ready for its prompt at now, a wait that
// began at start: its agent reports that it waits for input or is done, or
// its output (last at lastOutput, zero before any) has been quiet for
// readyQuiet and readyMin has passed. capped is the wait reaching readyCap.
func readyAt(state session.AttentionState, lastOutput, start, now time.Time) (ready, capped bool) {
	if state == session.AttentionNeedsInput || state == session.AttentionDone {
		return true, false
	}
	if !lastOutput.IsZero() && now.Sub(lastOutput) >= readyQuiet && now.Sub(start) >= readyMin {
		return true, false
	}
	return false, now.Sub(start) >= readyCap
}
