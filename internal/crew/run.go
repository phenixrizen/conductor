package crew

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

// A run is one launch of a crew. Every member becomes a server session of
// its own, launched like any other (Launcher) and tagged with the run
// (session.Info.Crew). A member starts when its start condition says: at
// once, once the member it waits for first reports done after its prompt,
// or when asked. Once its session is ready its role prompt is typed into it.
// A handoff a member reports is typed into the member it names, once that one
// does not wait for input. Runs live in memory, as sessions do: a server
// restart forgets them.

// Launcher starts the session of a crew member. The API server implements it
// on the path POST /api/sessions takes, so a member's session is launched
// like any other: its working directory checked, its agent looked up in the
// catalog, its environment built and its hooks injected. env holds variables
// to set in the process (GOAL); ref tags the session and gives its process
// CONDUCTOR_CREW, CONDUCTOR_RUN and CONDUCTOR_MEMBER.
type Launcher interface {
	Launch(ctx context.Context, spec LaunchSpec) (*session.Local, error)
}

// LaunchSpec is a member's session as the engine asks a Launcher for it.
type LaunchSpec struct {
	AgentID, Name, Cwd string
	Args               []string
	// Env holds variables to set in the process: GOAL.
	Env map[string]string
	// Ref tags the session with its run.
	Ref session.CrewRef
	// Yolo is the run's yolo choice, fixed when the run was made: the agent's
	// yolo recipe is applied when it has one.
	Yolo bool
	// Resume, when set, is the agent session the launch resumes with its
	// agent's recipe (ResumeMember); ResumedFrom the session it follows.
	Resume, ResumedFrom string
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
	// diffReaders bounds the DiffStat reads GetWithDiffs runs at once.
	diffReaders = 4
)

// worktreesDir is where a run's worktrees go under the crew's working
// directory, <cwd>/.conductor/worktrees/<run>/<member>, so that they pass
// the check every session's working directory passes.
var worktreesDir = filepath.Join(".conductor", "worktrees")

// Errors of the runs, beside ErrInvalid, ErrNotRepo and ErrNoGit.
var (
	ErrRunNotFound    = errors.New("no such run")
	ErrMemberNotFound = errors.New("no such member in the run")
	ErrMemberStarted  = errors.New("the member has started already")
	ErrRunStopped     = errors.New("the run is stopped")
	ErrMemberRunning  = errors.New("the member is still running")
	ErrRunRunning     = errors.New("the run is still running: stop it first")
	// errNotReady is awaitReady giving up after readyCap.
	errNotReady = errors.New("not ready")
)

// Diff is what a member's worktree adds and removes against the commit it
// began from, in lines: tracked changes against the base, uncommitted
// included (DiffStat).
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
	// NeedsInput is set when the member is starting or running and its
	// session waits on a prompt: a trust question before its prompt, or a
	// question of its agent's.
	NeedsInput bool `json:"needsInput,omitempty"`
	// AgentSession is the agent's own session in the member's latest session,
	// kept when that session has left the server: what ResumeMember resumes.
	AgentSession *session.AgentSession `json:"agentSession,omitempty"`
	// Diff is set by GetWithDiffs for a member with a worktree: tracked
	// changes against the base, uncommitted included.
	Diff *Diff `json:"diff,omitempty"`
}

// Run states (Run.State), derived each time a run is read (refresh).
const (
	RunRunning    = "running"     // a member is pending, starting or running, and none waits on a prompt
	RunNeedsInput = "needs_input" // the session of a starting or running member waits on a prompt
	RunStopped    = "stopped"     // stopped (Stop): its sessions were stopped
	RunFinished   = "finished"    // every member ended, and none is pending
)

// Run is a launch of a crew as the engine reports it.
type Run struct {
	ID     string `json:"id"`
	CrewID string `json:"crewId"`
	Name   string `json:"name"`
	// Label is the run's own name, given at launch and kept by a resume;
	// empty when none: the pages then say the crew and the start.
	Label     string        `json:"label,omitempty"`
	Goal      string        `json:"goal"`
	Cwd       string        `json:"cwd"`
	Isolation string        `json:"isolation"`
	StartedAt time.Time     `json:"startedAt"`
	StoppedAt *time.Time    `json:"stoppedAt,omitempty"`
	Members   []MemberState `json:"members"`
	// Yolo is the run's yolo choice, fixed at launch: every member, one added
	// or started later included, is launched with it.
	Yolo bool `json:"yolo"`
	// ResumedFrom is the stopped run this one resumed (ResumeRun), and
	// ResumedBy the run that resumed this one.
	ResumedFrom string `json:"resumedFrom,omitempty"`
	ResumedBy   string `json:"resumedBy,omitempty"`
	// State is the run's state (RunRunning…), NeedsInput how many members'
	// sessions wait on a prompt. A member's done keeps a run running: a done
	// agent is idle, not gone.
	State      string `json:"state"`
	NeedsInput int    `json:"needsInput"`
	// Log is the run's own log, oldest first: launched, member started,
	// prompt typed, handoff queued, delivered or dropped, stopped, and the
	// rest docs/protocol.md lists. At most maxRunLog entries.
	Log []session.ActivityEntry `json:"log"`
}

// Engine launches crews and runs their runs. It never holds its lock while
// it starts, types into or stops a session.
type Engine struct {
	launcher Launcher
	lookup   func(sessionID string) (*session.Local, bool)
	// await waits for a member's session to be ready, calling held with the
	// question once each time a trust question holds the prompt: awaitReady,
	// but for tests.
	await func(ctx context.Context, l *session.Local, held func(question string)) error
	// now is the clock of the diff cache.
	now func() time.Time
	// excludeMu keeps two launches from writing a repository's info/exclude
	// at once.
	excludeMu sync.Mutex

	// bySession maps the ID of each member's session to its sessionMember.
	// It is read without mu, so that the activity and the changes of the
	// sessions no run has cost the engine no lock; it is written under mu.
	bySession sync.Map

	mu    sync.Mutex
	runs  map[string]*run
	order []*run // oldest first

	// OnForget is called with the ID of each run the engine forgets: past
	// maxRuns, or after a launch that failed. It runs under mu, so it must
	// not call the engine or wait. Set it before the first launch.
	OnForget func(runID string)

	// OnRunChange is called with the ID of a run each time something a read
	// of it shows changes that no session change carries: a member reserved,
	// started, running or ended, an entry in its log, a stop. It runs under
	// mu, so it must not call the engine or wait. Set it before the first
	// launch.
	OnRunChange func(runID string)

	// OnEnd is called with a run each time it ends: stopped (Stop), or
	// finished, every member ended, as the change of the last member's
	// session shows it (OnChange). It runs without the engine's lock, in
	// the goroutine that stopped the run or reported the change, so it
	// must not wait; RecordEnds sets it to save the run. Set it before the
	// first launch.
	OnEnd func(r Run)
	// endMu orders the ends: a snapshot and its OnEnd are one step under it,
	// so the ends reach OnEnd in the order their snapshots were taken.
	endMu sync.Mutex

	// afterAdd, when set, runs once Launch has kept its run and before any
	// member's start is reserved: a test stops the run there.
	afterAdd func(runID string)
	// tried, when set, is called by deliver after each attempt to type a
	// handoff, with the member and whether it was typed: tests wait on it.
	tried func(member string, typed bool)
}

// sessionMember is the member a session is, and its run.
type sessionMember struct {
	r *run
	m *member
}

// run is a launch of a crew. Engine.mu guards what changes after it is made.
type run struct {
	id, crewID, name, label, goal, cwd, isolation string
	// yolo is the run's yolo choice, fixed when it is made.
	yolo bool
	// changed is the engine's OnRunChange, or nil.
	changed func(runID string)
	// prefix is where cwd lies in its repository, with worktrees: a member
	// works in <worktree>/<prefix>.
	prefix    string
	startedAt time.Time
	stoppedAt *time.Time
	members   []*member
	log       []session.ActivityEntry

	// ctx ends when the run stops; every member start runs under it.
	ctx    context.Context
	cancel context.CancelFunc
	// stopping is set once the run begins to stop: no member starts after it;
	// stopDone once the stop completed (every session stopped, every start
	// and delivery drained), which a member's Resume may reopen.
	stopping, stopDone bool
	// resumedFrom and resumedBy link a run to the one it resumed, and to the
	// one that resumed it (ResumeRun).
	resumedFrom, resumedBy string
	// launching is set from add until the launcher releases the run
	// (LaunchHeld): evict leaves the run alone meanwhile, so that a crew whose
	// members all start later, which has nothing running once it is kept, is
	// not forgotten before Launch answers with it.
	launching bool
	// starts counts the member starts in flight; a stop waits for them.
	starts sync.WaitGroup
	// deliveries counts the goroutines typing handoffs (deliver); a stop
	// waits for them.
	deliveries sync.WaitGroup
	// excludeNoted is set once the log says info/exclude could not be written.
	excludeNoted bool
}

type member struct {
	def   Member
	state MemberState
	base  string // the commit its worktree branched from, for DiffStat
	// prompted is set as its prompt is typed (or at once without one), and
	// promptedAt is when: a done whose entry is stamped after it starts the
	// members after it (startAfter). One stamped before it, even if it
	// reaches the engine later, is the idle state the prompt answered.
	prompted   bool
	promptedAt time.Time
	diff       *Diff
	diffAt     time.Time // when diff was read; zero: never
	// handoffs wait to be typed into the member, oldest first, at most
	// maxHandoffs (handoff.go). delivering is set, under e.mu, while a
	// goroutine types them (deliver), and read without it by poke; wake makes
	// that goroutine look again.
	handoffs   []handoff
	delivering atomic.Bool
	wake       chan struct{}
}

// NewEngine returns an engine that starts member sessions with l and finds
// them again with lookup (the server's registry).
func NewEngine(l Launcher, lookup func(sessionID string) (*session.Local, bool)) *Engine {
	return &Engine{launcher: l, lookup: lookup, await: awaitReady, now: time.Now,
		runs: map[string]*run{}}
}

// Launch is LaunchHeld for a caller that needs nothing more of the run once
// it has it.
func (e *Engine) Launch(ctx context.Context, c Crew) (*Run, error) {
	run, release, err := e.LaunchHeld(ctx, c)
	release()
	return run, err
}

// LaunchHeld starts a run of c, whose Cwd the caller has resolved. With
// isolation "worktree" it first checks that git is on the server's PATH
// (ErrNoGit) and that Cwd is in a git repository to make worktrees of
// (ErrNotRepo), before anything is made. It starts the sessions of the
// members that start immediately, each in a worktree of its own when the
// crew has them, and returns once they exist: each member is starting, and
// its prompt is typed once its session is ready (on the run's context, so a
// client that goes away does not stop it). A member whose prompt cannot be
// typed ends alone. When a member's session cannot be started, the ones
// started are stopped, no run is kept, and the error names the member; when
// the run is stopped meanwhile, it stays, stopped, and LaunchHeld returns
// ErrRunStopped, a stop that lands before the first member's start included.
// A crew with no members, one that runs on a host, one without a valid ID
// or, with worktrees, one whose <cwd>/.conductor or <cwd>/.conductor/worktrees
// is a symbolic link is not launched (ErrInvalid), and neither is a member
// whose directory in its worktree lies through a symbolic link.
//
// The run stays exempt from eviction until the caller calls release, which
// is never nil and must be called once, after an error too (a second call
// does nothing): the API mints the run's view link first, so that a launch at
// the cap cannot forget the run between its start and its link.
func (e *Engine) LaunchHeld(ctx context.Context, c Crew) (*Run, func(), error) {
	return e.LaunchNamed(ctx, c, "")
}

// LaunchNamed is LaunchHeld with the run's own name (Run.Label), checked
// as a crew name is (ValidateLabel, ErrInvalid); empty gives it none.
func (e *Engine) LaunchNamed(ctx context.Context, c Crew, label string) (*Run, func(), error) {
	noop := func() {}
	if err := ValidateLabel(label); err != nil {
		return nil, noop, err
	}
	prefix, err := e.prepare(ctx, c)
	if err != nil {
		return nil, noop, err
	}
	r := e.add(c, prefix, label)
	release := sync.OnceFunc(func() { e.launched(r) })
	if e.afterAdd != nil {
		e.afterAdd(r.id)
	}
	run, err := e.startImmediate(ctx, r)
	return run, release, err
}

// LaunchAdopting is LaunchHeld for a crew an agent forms around its own
// session: the member named self is not started but adopted, its session
// the one given (running already, its prompt its own), so the members after
// it start on its next done and handoffs reach it; the rest start as at
// launch. The session is tagged with the run by the caller (SetCrew).
func (e *Engine) LaunchAdopting(ctx context.Context, c Crew, self, sessionID string) (*Run, func(), error) {
	noop := func() {}
	if c.member(self) == nil {
		return nil, noop, invalidf("self %s is not a member of the crew", quote(self))
	}
	if sessionID == "" {
		return nil, noop, invalidf("no session to adopt")
	}
	if _, _, ok := e.MemberOf(sessionID); ok {
		return nil, noop, invalidf("the session is a member of a run already")
	}
	prefix, err := e.prepare(ctx, c)
	if err != nil {
		return nil, noop, err
	}
	r := e.add(c, prefix, "")
	release := sync.OnceFunc(func() { e.launched(r) })
	if e.afterAdd != nil {
		e.afterAdd(r.id)
	}
	now := time.Now().UTC()
	e.mu.Lock()
	m := r.member(self)
	m.state.SessionID, m.state.Started, m.state.Status = sessionID, &now, MemberRunning
	m.prompted, m.promptedAt = true, now
	e.bySession.Store(sessionID, sessionMember{r, m})
	r.note(session.ActivityStatus, "%s formed the crew from its own session and joined it", self)
	r.touch()
	e.mu.Unlock()
	run, err := e.startImmediate(ctx, r)
	return run, release, err
}

// member returns the member of c named name, or nil.
func (c Crew) member(name string) *Member {
	for i := range c.Members {
		if c.Members[i].Name == name {
			return &c.Members[i]
		}
	}
	return nil
}

// prepare checks c as LaunchHeld describes and, with worktrees, finds where
// its cwd lies in its repository (the prefix).
func (e *Engine) prepare(ctx context.Context, c Crew) (prefix string, err error) {
	if err := c.validateWithID(); err != nil {
		return "", err
	}
	if err := c.Launchable(); err != nil {
		return "", err
	}
	if c.Isolation != IsolationWorktree {
		return "", nil
	}
	if err := checkGit(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(c.Cwd) {
		return "", invalidf("with worktrees, cwd must be an absolute path")
	}
	if err := checkWorktreesDir(c.Cwd); err != nil {
		return "", err
	}
	if err := CheckRepo(ctx, c.Cwd); err != nil {
		return "", err
	}
	return repoPrefix(ctx, c.Cwd)
}

// ResumeRun starts a new run of a stopped (or finished) run's crew in which
// every member whose last agent session is resumable continues its
// conversation in its kept worktree and branch, without a prompt, and the
// others start afresh under their start rules; the new run names the old
// in ResumedFrom and the old names it in ResumedBy. A run still going, or
// whose stop was cut short, is refused (ErrRunRunning, ErrRunStopped). A
// member whose session cannot be made ends with the reason, as at launch.
func (e *Engine) ResumeRun(ctx context.Context, runID string) (*Run, error) {
	cur, ok := e.Get(runID)
	if !ok {
		return nil, ErrRunNotFound
	}
	e.mu.Lock()
	old := e.runs[runID]
	if old == nil {
		e.mu.Unlock()
		return nil, ErrRunNotFound
	}
	switch {
	case old.stopping && !old.stopDone:
		e.mu.Unlock()
		return nil, ErrRunStopped
	case cur.State != RunStopped && cur.State != RunFinished:
		e.mu.Unlock()
		return nil, ErrRunRunning
	}
	yolo := old.yolo
	c := Crew{ID: old.crewID, Name: old.name, Goal: old.goal, Cwd: old.cwd, Where: WhereServer, Isolation: old.isolation, Yolo: &yolo}
	type kept struct{ agentSession, sessionID, worktree, branch, base string }
	keeps := map[string]kept{}
	for _, m := range old.members {
		c.Members = append(c.Members, m.def)
	}
	for _, st := range cur.Members {
		if st.AgentSession != nil && st.AgentSession.Resumable && st.AgentSession.ID != "" {
			k := kept{agentSession: st.AgentSession.ID, sessionID: st.SessionID, worktree: st.Worktree, branch: st.Branch}
			if m := old.member(st.Name); m != nil {
				k.base = m.base
			}
			keeps[st.Name] = k
		}
	}
	e.mu.Unlock()
	prefix, err := e.prepare(ctx, c)
	if err != nil {
		return nil, err
	}
	r := e.add(c, prefix, old.label)
	defer e.launched(r)
	e.mu.Lock()
	r.resumedFrom = old.id
	old.resumedBy = r.id
	old.note(session.ActivityStatus, "resumed as %s", r.id)
	r.note(session.ActivityStatus, "resumes %s: %d conversations continue", old.id, len(keeps))
	var resumes []*member
	for _, m := range r.members {
		k, ok := keeps[m.def.Name]
		if !ok {
			continue
		}
		if r.isolation == IsolationWorktree && k.worktree != "" {
			m.state.Branch, m.state.Worktree, m.base = k.branch, k.worktree, k.base
		}
		m.state.Status = MemberStarting
		r.starts.Add(1)
		resumes = append(resumes, m)
	}
	r.touch()
	e.mu.Unlock()
	if e.afterAdd != nil {
		e.afterAdd(r.id)
	}
	for _, m := range resumes {
		k := keeps[m.def.Name]
		name := m.def.Name
		cwd := r.cwd
		if r.isolation == IsolationWorktree && k.worktree != "" {
			cwd = filepath.Join(k.worktree, r.prefix)
		}
		local, err := e.launcher.Launch(ctx, LaunchSpec{AgentID: m.def.AgentID, Name: name, Cwd: cwd, Args: slices.Clone(m.def.Args),
			Env: map[string]string{"GOAL": r.goal}, Ref: session.CrewRef{RunID: r.id, CrewID: r.crewID, Member: name}, Yolo: r.yolo,
			Resume: k.agentSession, ResumedFrom: k.sessionID})
		if err != nil {
			r.starts.Done()
			e.fail(r, m, err)
			continue
		}
		id := local.Info().ID
		started := time.Now().UTC()
		e.mu.Lock()
		m.state.SessionID, m.state.Started, m.state.Status = id, &started, MemberRunning
		// Its conversation has its prompt: a done from now on starts the
		// members after it.
		m.prompted, m.promptedAt = true, started
		e.bySession.Store(id, sessionMember{r, m})
		r.note(session.ActivityStatus, "%s resumed its conversation", name)
		e.mu.Unlock()
		r.starts.Done()
		m.poke()
	}
	run, err := e.startImmediate(ctx, r)
	if err != nil {
		return nil, err
	}
	return run, nil
}

// startImmediate starts the members of r that start immediately and returns
// r as it then is. r is launching (held) until its launcher releases it, so
// no other launch forgets it meanwhile. (Engine.start, which starts one
// member, is another function and stays as it is.)
func (e *Engine) startImmediate(ctx context.Context, r *run) (*Run, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(r.ctx, cancel)()

	e.mu.Lock()
	if r.stopping {
		// Stopped before any start was reserved: the run stays, stopped.
		e.mu.Unlock()
		return nil, ErrRunStopped
	}
	var starts []*member
	for _, m := range r.members {
		if m.def.Start.When == StartImmediately && r.reserve(m) {
			starts = append(starts, m)
		}
	}
	e.mu.Unlock()
	for i, m := range starts {
		local, err := e.launch(ctx, r, m)
		if err == nil {
			go e.finish(r, m, local) // takes over the start
			continue
		}
		if errors.Is(err, ErrRunStopped) || r.ctx.Err() != nil {
			// Stopped meanwhile: the run stays, stopped, and so does what
			// had started. The members not begun were never started.
			e.fail(r, m, ErrRunStopped)
			e.mu.Lock()
			for _, rest := range starts[i+1:] {
				rest.state.Status = MemberPending
			}
			e.mu.Unlock()
			for range starts[i:] {
				r.starts.Done()
			}
			return nil, ErrRunStopped
		}
		for range starts[i:] {
			r.starts.Done()
		}
		e.abort(r)
		return nil, fmt.Errorf("member %s: %w", quote(m.def.Name), err)
	}
	// The check stays, should something else ever forget a run.
	out, ok := e.Get(r.id)
	if !ok {
		return nil, ErrRunNotFound
	}
	return &out, nil
}

// add makes and keeps a run of c with every member pending, launching until
// the caller calls launched.
func (e *Engine) add(c Crew, prefix, label string) *run {
	ctx, cancel := context.WithCancel(context.Background())
	r := &run{crewID: c.ID, name: c.Name, label: label, goal: c.Goal, cwd: c.Cwd, isolation: c.Isolation, prefix: prefix,
		yolo: c.Yolo != nil && *c.Yolo, changed: e.OnRunChange, startedAt: time.Now().UTC(), ctx: ctx, cancel: cancel, launching: true}
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
		r.id = c.ID + "-" + session.NewID()[:8]
	}
	e.evict()
	e.runs[r.id] = r
	e.order = append(e.order, r)
	if label != "" {
		r.note(session.ActivityStatus, "launched %s as %q: %d members, %d starting now", c.Name, label, len(c.Members), immediate)
	} else {
		r.note(session.ActivityStatus, "launched %s: %d members, %d starting now", c.Name, len(c.Members), immediate)
	}
	return r
}

// launched marks r launched: from now on evict may forget it.
func (e *Engine) launched(r *run) {
	e.mu.Lock()
	r.launching = false
	e.mu.Unlock()
}

func newMember(m Member) *member {
	m.Args = slices.Clone(m.Args)
	return &member{def: m, state: MemberState{Name: m.Name, AgentID: m.AgentID, Start: m.Start, Status: MemberPending},
		wake: make(chan struct{}, 1)}
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
	r.touch()
	return true
}

// touch tells OnRunChange that r changed. The caller holds e.mu.
func (r *run) touch() {
	if r.changed != nil {
		r.changed(r.id)
	}
}

// note appends an entry to r's log, cleaned like a session's entries, and
// drops the oldest past maxRunLog. The caller holds e.mu.
func (r *run) note(typ, format string, args ...any) {
	r.record(session.ActivityEntry{Type: typ, Message: fmt.Sprintf(format, args...)})
}

// noteHandoff notes a handoff delivered from one member to another, with
// the two as fields (byName, to) beside the words, so a client draws it
// without reading the text. The caller holds e.mu.
func (r *run) noteHandoff(from, to string) {
	r.record(session.ActivityEntry{Type: session.ActivityStatus, Message: fmt.Sprintf("handoff delivered from %s to %s", from, to), ByName: from, To: to})
}

// record is note with the entry given: its time is now. The caller holds e.mu.
func (r *run) record(entry session.ActivityEntry) {
	entry.At = time.Now().UTC()
	entry = session.CleanEntry(entry)
	if len(r.log) >= maxRunLog {
		r.log = slices.Delete(r.log, 0, len(r.log)-maxRunLog+1)
	}
	r.log = append(r.log, entry)
	r.touch()
}

// launch makes m's worktree, when r has them, and starts m's session, in
// the worktree's directory that r's cwd is of its repository, which it makes
// when no commit has a file there.
func (e *Engine) launch(ctx context.Context, r *run, m *member) (*session.Local, error) {
	name := m.def.Name
	cwd := r.cwd
	if r.isolation == IsolationWorktree {
		path := filepath.Join(r.cwd, worktreesDir, r.id, name)
		branch := "crew/" + r.id + "/" + name
		if err := checkWorktreesDir(r.cwd); err != nil {
			return nil, err
		}
		e.excludeMu.Lock()
		err := excludeWorktrees(ctx, r.cwd)
		e.excludeMu.Unlock()
		if err != nil {
			// The worktrees work without it; git status shows them. The run
			// log says so once.
			e.mu.Lock()
			if !r.excludeNoted {
				r.excludeNoted = true
				r.note(session.ActivityError, "could not add %s to the repository's info/exclude: %v", excludeLine, err)
			}
			e.mu.Unlock()
		}
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
		cwd = filepath.Join(path, r.prefix)
		// A directory no commit has a file in is not in the worktree; one
		// that a commit makes a symbolic link would be made where it points.
		if err := checkNoLinks(path, r.prefix); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			return nil, err
		}
	}
	local, err := e.launcher.Launch(ctx, LaunchSpec{AgentID: m.def.AgentID, Name: name, Cwd: cwd, Args: slices.Clone(m.def.Args),
		Env: map[string]string{"GOAL": r.goal}, Ref: session.CrewRef{RunID: r.id, CrewID: r.crewID, Member: name}, Yolo: r.yolo})
	if err != nil {
		return nil, err
	}
	id := local.Info().ID
	started := time.Now().UTC()
	e.mu.Lock()
	m.state.SessionID, m.state.Started = id, &started
	e.bySession.Store(id, sessionMember{r, m})
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

// prompt waits for m's session to be ready and submits m's prompt, the goal
// in it, as one line (typedPrompt), and m is running; a member without a
// prompt runs at once. A trust question on the member's screen holds the
// prompt until a person answers it (awaitReady), noted once in the run log
// each time. m is prompted as the Enter is written, so a done the agent
// reported before it never starts the members after m. A session that ends
// first gets no prompt: m ends, with how its process ended. An error means
// the start failed: ctx ended or the prompt could not be written.
func (e *Engine) prompt(ctx context.Context, r *run, m *member, local *session.Local) error {
	name := m.def.Name
	text := typedPrompt(m.def.Prompt, r.goal)
	hasPrompt := strings.TrimSpace(text) != ""
	if hasPrompt {
		err := e.await(ctx, local, func(question string) {
			e.mu.Lock()
			r.note(session.ActivityStatus, "%s asks %s: answer it in %s's terminal; its prompt waits", name, quote(question), name)
			e.mu.Unlock()
		})
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
		res, err := local.Submit(ctx, session.Submission{Text: text, ByName: typedBy, Confirm: true, BeforeEnter: func() {
			e.mu.Lock()
			m.markPrompted()
			e.mu.Unlock()
		}})
		if err != nil {
			if errors.Is(err, session.ErrSessionEnded) || endsSoon(local) {
				e.endedEarly(r, m, local)
				return nil
			}
			return fmt.Errorf("typing its prompt: %w", err)
		}
		e.mu.Lock()
		switch {
		case !res.Entered:
			// A question came up during the pause: the prompt waits in the
			// agent's input, and is never typed again.
			m.markPrompted()
			r.note(session.ActivityError, "typed %s's prompt without its Enter: %s waits on a question; answer it, then press Enter in its terminal", name, name)
		case res.Reentered:
			r.note(session.ActivityStatus, "pressed Enter again for %s: it did not report taking its prompt within %v", name, session.ConfirmWait)
		}
		e.mu.Unlock()
	}
	e.mu.Lock()
	m.markPrompted()
	m.state.Status = MemberRunning
	if hasPrompt {
		r.note(session.ActivityStatus, "typed %s's prompt", name)
	} else {
		r.touch()
	}
	e.mu.Unlock()
	m.poke() // the handoffs waiting for its prompt may go
	return nil
}

// markPrompted marks m prompted, now, unless it is already. The caller holds
// e.mu.
func (m *member) markPrompted() {
	if !m.prompted {
		m.prompted, m.promptedAt = true, time.Now().UTC()
	}
}

// endsSoon reports whether l ends within a second: a write that failed as
// its process exited, before the session saw it end.
func endsSoon(l *session.Local) bool {
	select {
	case <-l.Ended():
		return true
	case <-time.After(time.Second):
		return false
	}
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
// session, and returns once the session exists; its prompt is typed on the
// run's context once the session is ready (finish). When the session cannot
// be started, m ends with the reason.
func (e *Engine) start(ctx context.Context, r *run, m *member) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(r.ctx, cancel)()
	local, err := e.launch(ctx, r, m)
	if err == nil {
		go e.finish(r, m, local) // takes over the start
		return nil
	}
	r.starts.Done()
	if r.ctx.Err() != nil {
		err = ErrRunStopped
	}
	e.fail(r, m, err)
	return fmt.Errorf("member %s: %w", quote(m.def.Name), err)
}

// finish types m's prompt once its session is ready, on the run's context,
// and ends m's start. When that fails m ends alone, with the reason, and then
// its session is stopped: a session seen ended has its member's reason.
func (e *Engine) finish(r *run, m *member, local *session.Local) {
	defer r.starts.Done()
	if err := e.prompt(r.ctx, r, m, local); err != nil {
		if r.ctx.Err() != nil {
			err = ErrRunStopped
		}
		e.fail(r, m, err)
		stopLocal(local)
	}
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
// entry of a session that is no member of a run costs no lock. An entry of a
// member's session wakes the handoffs waiting for that member. The first done
// a running member reports starts the pending members that start after it; a
// done whose entry is stamped no later than its prompt was typed does not
// count, however late it arrives: the session stamps an attention entry as
// the state changes, so that done is the idle state the prompt answered. A handoff a member reports
// goes to the member it names (handoff). It never waits: a start, and the
// typing of a handoff, run on goroutines of their own.
func (e *Engine) OnActivity(sessionID string, entry session.ActivityEntry, state session.AttentionState) {
	v, ok := e.bySession.Load(sessionID)
	if !ok {
		return
	}
	sm := v.(sessionMember)
	r, m := sm.r, sm.m
	m.poke()
	isHandoff := entry.Type == session.ActivityHandoff
	if !isHandoff && (entry.Type != session.ActivityAttention || state != session.AttentionDone) {
		return
	}
	e.mu.Lock()
	if e.runs[r.id] != r { // forgotten meanwhile
		e.mu.Unlock()
		return
	}
	var next []*member
	if isHandoff {
		e.handoff(r, m, entry)
	} else {
		next = r.startAfter(m, entry.At)
	}
	e.mu.Unlock()
	for _, m := range next {
		// The outcome is in m's state and the run log.
		go func() { _ = e.start(r.ctx, r, m) }()
	}
}

// startAfter reserves the start of the pending members that start after
// done, which reports done in an entry stamped at, when done has had its
// prompt before at and has not ended, and returns them. The caller holds e.mu
// and starts them.
func (r *run) startAfter(done *member, at time.Time) []*member {
	if !done.prompted || !at.After(done.promptedAt) || done.state.Status == MemberEnded {
		return nil
	}
	var next []*member
	var names []string
	for _, m := range r.members {
		if m.def.Start.When == StartAfter && m.def.Start.Member == done.def.Name && r.reserve(m) {
			next = append(next, m)
			names = append(names, m.def.Name)
		}
	}
	if len(next) > 0 {
		r.note(session.ActivityStatus, "%s is done: starting %s", done.def.Name, strings.Join(names, ", "))
	}
	return next
}

// StartMember starts a pending member of a run by hand, whatever its start
// condition, and returns once its session exists or could not be started;
// its prompt is typed once the session is ready.
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
// name no member has, a prompt that fits a session with the run's goal in it,
// an after condition naming a member of the run, at most 12 members
// (ErrInvalid). A member that starts immediately starts, and
// AddMember returns once its session exists or could not be started (the
// member stays in the run, ended, its name taken); one that starts after
// another waits for that one's next done.
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
	}
	if err := m.checkTypedPrompt(r.goal); err != nil {
		return err
	}
	switch {
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
// every member's session is stopped, the handoffs waiting are dropped and the
// run is marked stopped. The worktrees stay. Stopping a stopped run again
// changes nothing.
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
	waitFor(ctx, &r.starts)
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
	// The handoffs left are dropped; one being typed ends with its session.
	waitFor(ctx, &r.deliveries)
	e.mu.Lock()
	if r.stoppedAt == nil {
		now := time.Now().UTC()
		r.stoppedAt = &now
		r.note(session.ActivityStatus, "stopped")
	}
	// A stop cut short by its context may have left a start or a delivery
	// going: such a run is not reopened.
	r.stopDone = ctx.Err() == nil
	e.mu.Unlock()
	e.noteEnd(r.id)
	return errors.Join(errs...)
}

// waitFor waits for wg, the starts or the deliveries in flight, or for ctx.
func waitFor(ctx context.Context, wg *sync.WaitGroup) {
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
// one more. A run with a member starting or a live session stays, and so does
// one being launched, so the engine may hold more than maxRuns while they run.
// The caller holds e.mu.
func (e *Engine) evict() {
	for len(e.order) >= maxRuns {
		i := slices.IndexFunc(e.order, func(r *run) bool { return !r.launching && e.idle(r) })
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

// ForgetForTest drops a run as evict would, so a test sees the engine
// without it (its record stays).
func (e *Engine) ForgetForTest(runID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r, ok := e.runs[runID]; ok {
		e.forget(r)
	}
}

// forget drops r and the index of its sessions, and tells OnForget. The
// caller holds e.mu.
func (e *Engine) forget(r *run) {
	delete(e.runs, r.id)
	e.order = slices.DeleteFunc(e.order, func(o *run) bool { return o == r })
	for _, m := range r.members {
		if m.state.SessionID != "" {
			e.bySession.Delete(m.state.SessionID)
		}
	}
	r.cancel()
	if e.OnForget != nil {
		e.OnForget(r.id)
	}
}

// IfKept runs f while the engine keeps the run with the given ID, under the
// engine's lock, and reports whether it ran. A run is forgotten under that
// lock too (OnForget), so what f makes for the run, such as a link, cannot
// outlive a forgotten run unseen. f must not call the engine or wait.
func (e *Engine) IfKept(runID string, f func()) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.runs[runID]; !ok {
		return false
	}
	f()
	return true
}

// Note adds an entry of type typ with msg to the log of the run with the
// given ID, cleaned and bounded as the run's own entries; a run the engine
// does not have takes none.
func (e *Engine) Note(runID, typ, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r, ok := e.runs[runID]; ok {
		r.note(typ, "%s", msg)
	}
}

// MemberOf reports the run and the member a session belongs to.
func (e *Engine) MemberOf(sessionID string) (runID, member string, ok bool) {
	v, ok := e.bySession.Load(sessionID)
	if !ok {
		return "", "", false
	}
	sm := v.(sessionMember)
	return sm.r.id, sm.m.def.Name, true
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
// lines its worktree adds and removes since it began (DiffStat), read again,
// at most diffReaders at once, once what was read is diffTTL old. A read that
// fails keeps the diff read before; one that ctx cuts short is not waited
// out: the next call reads again.
func (e *Engine) GetWithDiffs(ctx context.Context, runID string) (Run, bool) {
	type read struct {
		m              *member
		worktree, base string
		prevAt         time.Time
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
			reads = append(reads, read{m, m.state.Worktree, m.base, m.diffAt})
			m.diffAt = now // one reader at a time
		}
	}
	e.mu.Unlock()
	// A read that fails keeps what was read before; one that its request cut
	// short leaves the next request to read again.
	var wg sync.WaitGroup
	slots := make(chan struct{}, diffReaders)
	for _, rd := range reads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			added, removed, err := DiffStat(ctx, rd.worktree, rd.base)
			e.mu.Lock()
			defer e.mu.Unlock()
			switch {
			case err == nil:
				rd.m.diff = &Diff{Added: added, Removed: removed}
			case ctx.Err() != nil && rd.m.diffAt.Equal(now):
				rd.m.diffAt = rd.prevAt
			}
		}()
	}
	wg.Wait()
	e.mu.Lock()
	out := r.snapshot(true)
	e.mu.Unlock()
	e.refresh(&out)
	return out, true
}

// snapshot copies r, with the members' diffs when withDiffs. The caller holds e.mu.
func (r *run) snapshot(withDiffs bool) Run {
	out := Run{ID: r.id, CrewID: r.crewID, Name: r.name, Label: r.label, Goal: r.goal, Cwd: r.cwd, Isolation: r.isolation, Yolo: r.yolo,
		StartedAt: r.startedAt, StoppedAt: r.stoppedAt, Members: make([]MemberState, 0, len(r.members)),
		ResumedFrom: r.resumedFrom, ResumedBy: r.resumedBy,
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

// refresh shows as ended the running members of out whose session has ended
// or is gone from the server, marks the starting and running members whose
// session waits on a prompt, and derives the run's state (runState). A member
// still starting is left to its start, which ends it with the reason. It
// looks the sessions up: the caller does not hold e.mu.
func (e *Engine) refresh(out *Run) {
	for i := range out.Members {
		m := &out.Members[i]
		if m.SessionID == "" || (m.Status != MemberRunning && m.Status != MemberStarting) {
			continue
		}
		l, ok := e.lookup(m.SessionID)
		if !ok {
			if m.Status == MemberRunning {
				m.Status = MemberEnded
			}
			continue
		}
		info := l.Info()
		if info.Status.Ended() {
			if m.Status == MemberRunning {
				m.Status, m.Ended = MemberEnded, info.EndedAt
			}
			continue
		}
		m.NeedsInput = info.Attention.State == session.AttentionNeedsInput
	}
	out.State, out.NeedsInput = runState(*out)
}

// runState is a run's state and how many of its members wait on a prompt:
// stopped once stopped; finished when every member has ended and none is
// pending; needs_input while the session of a starting or running member
// waits on a prompt; running otherwise. web/app/utils/runs.ts derives the
// same from the run and the live sessions.
func runState(r Run) (string, int) {
	needs, ended := 0, 0
	for _, m := range r.Members {
		switch {
		case m.Status == MemberEnded:
			ended++
		case m.NeedsInput:
			needs++
		}
	}
	switch {
	case r.StoppedAt != nil:
		return RunStopped, needs
	case ended == len(r.Members):
		return RunFinished, 0
	case needs > 0:
		return RunNeedsInput, needs
	}
	return RunRunning, 0
}

// awaitReady waits until l is ready for its prompt (readyAt), looking every
// readyPoll. While a trust question shows it calls held with the question,
// once until the question goes, and the wait starts over when it goes: the
// cap does not run meanwhile, so a prompt is never typed into the question.
// After readyCap it returns errNotReady, and the prompt is typed anyway; once
// the session has ended it returns session.ErrSessionEnded.
func awaitReady(ctx context.Context, l *session.Local, held func(question string)) error {
	start := time.Now()
	tick := time.NewTicker(readyPoll)
	defer tick.Stop()
	holding := false
	for {
		select {
		case <-l.Ended():
			return session.ErrSessionEnded
		default:
		}
		att := l.Info().Attention
		seen, on := l.BracketedPaste()
		now := time.Now()
		ready, capped, hold := readyAt(readiness{state: att.State, source: att.Source, lastOutput: l.LastOutputAt(), pasteSeen: seen, pasteOn: on}, start, now)
		switch {
		case hold:
			if !holding {
				held(att.Message)
			}
			holding = true
			start = now // once it is answered, the wait starts over
		case ready:
			return nil
		case capped:
			return errNotReady
		default:
			holding = false
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

// readiness is what readyAt looks at of a session.
type readiness struct {
	state  session.AttentionState
	source string
	// lastOutput is when the output last moved (title updates aside), zero
	// before any.
	lastOutput time.Time
	// pasteSeen and pasteOn are the program's bracketed-paste mode
	// (session.Local.BracketedPaste).
	pasteSeen, pasteOn bool
}

// readyAt says whether a session is ready for its prompt at now, in a wait
// that began at start. A trust question on its screen holds the prompt (hold).
// It is ready when its agent reports that it waits for input or is done, or
// when its output has been quiet for readyQuiet and readyMin has passed, but
// never while the program has turned bracketed paste on and then off again:
// it is between screens (Claude Code while it starts), and what is typed then
// loses its Enter. capped is the wait reaching readyCap.
func readyAt(rd readiness, start, now time.Time) (ready, capped, hold bool) {
	if rd.state == session.AttentionNeedsInput && rd.source == session.SourceTrust {
		return false, false, true
	}
	if rd.state == session.AttentionNeedsInput || rd.state == session.AttentionDone {
		return true, false, false
	}
	capped = now.Sub(start) >= readyCap
	if rd.pasteSeen && !rd.pasteOn {
		return false, capped, false
	}
	if !rd.lastOutput.IsZero() && now.Sub(rd.lastOutput) >= readyQuiet && now.Sub(start) >= readyMin {
		return true, false, false
	}
	return false, capped, false
}

// ResumeMember starts an ended member of a run again, in its working
// directory (its worktree and branch, which stay), as a session that resumes
// the agent session resume names with the agent's recipe, or, with resume
// "", as a fresh one: then its role prompt is typed again once it is ready,
// as at its first start; a resumed agent has its conversation, and gets no
// prompt. It returns the new session's ID once the session exists. A member
// still starting or running is refused (ErrMemberRunning), and so is one of a
// stopped run (ErrRunStopped).
func (e *Engine) ResumeMember(ctx context.Context, runID, name, resume string) (string, error) {
	e.mu.Lock()
	r, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return "", ErrRunNotFound
	}
	m := r.member(name)
	var err error
	switch {
	case m == nil:
		err = ErrMemberNotFound
	case r.stopping && !r.stopDone:
		err = ErrRunStopped
	case m.state.Status == MemberPending:
		err = ErrMemberRunning
	case m.state.Status != MemberEnded && !e.ended(m):
		err = ErrMemberRunning
	}
	if err != nil {
		e.mu.Unlock()
		return "", err
	}
	if r.stopping {
		// A stopped run whose stop completed is reopened in place: a context
		// of its own again, no longer stopping, the other members as they
		// are (ended ones are resumed one by one); a later Stop stops it
		// again.
		r.ctx, r.cancel = context.WithCancel(context.Background())
		r.stopping, r.stopDone, r.stoppedAt = false, false, nil
		r.note(session.ActivityStatus, "reopened: %s resumes", name)
		r.touch()
	}
	old := m.state.SessionID
	cwd := r.cwd
	if r.isolation == IsolationWorktree && m.state.Worktree != "" {
		cwd = filepath.Join(m.state.Worktree, r.prefix)
	}
	m.state.Status = MemberStarting
	r.starts.Add(1)
	r.touch()
	e.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(r.ctx, cancel)()
	local, err := e.launcher.Launch(ctx, LaunchSpec{AgentID: m.def.AgentID, Name: name, Cwd: cwd, Args: slices.Clone(m.def.Args),
		Env: map[string]string{"GOAL": r.goal}, Ref: session.CrewRef{RunID: r.id, CrewID: r.crewID, Member: name}, Yolo: r.yolo,
		Resume: resume, ResumedFrom: old})
	if err != nil {
		r.starts.Done()
		e.fail(r, m, err)
		return "", fmt.Errorf("member %s: %w", quote(name), err)
	}
	id := local.Info().ID
	started := time.Now().UTC()
	e.mu.Lock()
	if old != "" {
		e.bySession.Delete(old)
	}
	m.state.SessionID, m.state.Started, m.state.Ended, m.state.Err = id, &started, nil, ""
	e.bySession.Store(id, sessionMember{r, m})
	if resume != "" {
		m.state.Status = MemberRunning
		r.note(session.ActivityStatus, "%s resumed its conversation", name)
	} else {
		// A new conversation: its prompt is typed again, and only a done
		// after that prompt counts.
		m.prompted, m.promptedAt = false, time.Time{}
		r.note(session.ActivityStatus, "%s started anew", name)
	}
	e.mu.Unlock()
	if resume != "" {
		r.starts.Done()
		m.poke()
		return id, nil
	}
	go e.finish(r, m, local) // types its prompt again, and ends the start
	return id, nil
}

// ended reports whether m's session has ended or is gone, as refresh shows
// it. The caller holds e.mu; it reads the session, which never calls into the
// engine while it holds its lock.
func (e *Engine) ended(m *member) bool {
	if m.state.SessionID == "" {
		return false
	}
	l, ok := e.lookup(m.state.SessionID)
	return !ok || l.Info().Status.Ended()
}
