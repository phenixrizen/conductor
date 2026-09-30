package crew

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/session/sessiontest"
)

// quietLog keeps the sessions' logs out of the test output.
var quietLog = slog.New(slog.DiscardHandler)

// launchCall is one call of fakeLauncher.Launch.
type launchCall struct {
	agentID, name, cwd string
	args               []string
	env                map[string]string
	ref                session.CrewRef
}

// fakeLauncher starts member sessions over fake processes, where the API
// server starts them over PTYs, and hands their activity to the engine with
// the attention state, and their changes, as the server's hooks do.
type fakeLauncher struct {
	t      *testing.T
	engine *Engine
	// onLaunch, when set, runs on a goroutine of its own once a member's
	// session has started: what its process does.
	onLaunch func(member string, p *sessiontest.FakeProc, l *session.Local)

	mu     sync.Mutex
	calls  []launchCall
	fail   map[string]error // member name -> the error its launch returns
	block  map[string]bool  // member name -> its launch waits for ctx to end
	procs  map[string]*sessiontest.FakeProc
	locals map[string]*session.Local // by member name, the latest
	byID   map[string]*session.Local // by session ID
}

func newFakeLauncher(t *testing.T) *fakeLauncher {
	return &fakeLauncher{t: t, fail: map[string]error{}, procs: map[string]*sessiontest.FakeProc{},
		locals: map[string]*session.Local{}, byID: map[string]*session.Local{}}
}

// newEngine returns an engine over a new fakeLauncher.
func newEngine(t *testing.T) (*Engine, *fakeLauncher) {
	fl := newFakeLauncher(t)
	e := NewEngine(fl, fl.lookup)
	fl.engine = e
	return e, fl
}

func (f *fakeLauncher) Launch(ctx context.Context, agentID, name, cwd string, args []string, env map[string]string, ref session.CrewRef) (*session.Local, error) {
	f.mu.Lock()
	f.calls = append(f.calls, launchCall{agentID, name, cwd, slices.Clone(args), maps.Clone(env), ref})
	err, block := f.fail[name], f.block[name]
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p := sessiontest.NewFakeProc()
	info := session.Info{ID: session.NewID(), Name: name, AgentID: agentID, Cwd: cwd, Crew: &ref, Cols: 80, Rows: 24}
	l := session.NewLocal(info, p, session.Options{Log: quietLog, OnActivity: f.activity, OnChange: f.change})
	f.mu.Lock()
	f.procs[name], f.locals[name], f.byID[info.ID] = p, l, l
	f.mu.Unlock()
	f.t.Cleanup(func() { p.End(0) })
	if f.onLaunch != nil {
		go f.onLaunch(name, p, l)
	}
	return l, nil
}

// activity is the sessions' OnActivity: the entry goes to the engine with the
// state of an attention entry, read from the session as the server reads it.
func (f *fakeLauncher) activity(sessionID string, e session.ActivityEntry) {
	var state session.AttentionState
	if l, ok := f.lookup(sessionID); ok && e.Type == session.ActivityAttention {
		state = l.Info().Attention.State
	}
	f.engine.OnActivity(sessionID, e, state)
}

// change is the sessions' OnChange: the change goes to the engine, as the
// server's hook sends it.
func (f *fakeLauncher) change(info session.Info) { f.engine.OnChange(info) }

func (f *fakeLauncher) lookup(id string) (*session.Local, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.byID[id]
	return l, ok
}

// member returns the latest session and process launched for a member.
func (f *fakeLauncher) member(name string) (*session.Local, *sessiontest.FakeProc) {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	l, p := f.locals[name], f.procs[name]
	if l == nil {
		f.t.Fatalf("member %q has no session", name)
	}
	return l, p
}

// launched returns the names of the members launched, in order.
func (f *fakeLauncher) launched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, c := range f.calls {
		names = append(names, c.name)
	}
	return names
}

// printOnce makes every process print a prompt and go quiet.
func printOnce(member string, p *sessiontest.FakeProc, _ *session.Local) {
	_ = p.Print(member + "> ")
}

// askAtOnce makes every agent report that it waits for input as soon as it
// starts: ready without the quiet period.
func askAtOnce(_ string, _ *sessiontest.FakeProc, l *session.Local) {
	l.SetAttention(session.AttentionNeedsInput, "what next?", session.SourceAPI)
}

func testCrew(members ...Member) Crew {
	return Crew{ID: "api-sweep", Name: "API sweep", Goal: "ship /v1/users", Cwd: "/work", Where: WhereServer, Isolation: IsolationNone, Members: members}
}

func immediate(name, prompt string) Member {
	return Member{Name: name, AgentID: "claude", Prompt: prompt, Start: Start{When: StartImmediately}}
}

func after(name, prompt, member string) Member {
	return Member{Name: name, AgentID: "shell", Prompt: prompt, Start: Start{When: StartAfter, Member: member}}
}

func manual(name, prompt string) Member {
	return Member{Name: name, AgentID: "shell", Prompt: prompt, Start: Start{When: StartManual}}
}

// typed returns what a process has been written so far, without waiting.
func typed(p *sessiontest.FakeProc) []string {
	var out []string
	for {
		select {
		case b := <-p.Input:
			out = append(out, string(b))
		default:
			return out
		}
	}
}

// waitTyped waits up to d for the next write to a process.
func waitTyped(t *testing.T, p *sessiontest.FakeProc, d time.Duration) string {
	t.Helper()
	select {
	case b := <-p.Input:
		return string(b)
	case <-time.After(d):
		t.Fatalf("nothing typed within %v", d)
		return ""
	}
}

// crewEntries returns the input entries a session records as typed by the crew.
func crewEntries(l *session.Local) []session.ActivityEntry {
	var out []session.ActivityEntry
	for _, e := range l.Activity() {
		if e.Type == session.ActivityInput && e.ByName == "crew" {
			out = append(out, e)
		}
	}
	return out
}

// memberState returns a member of a run as Get reports it.
func memberState(t *testing.T, e *Engine, runID, name string) MemberState {
	t.Helper()
	r, ok := e.Get(runID)
	if !ok {
		t.Fatalf("run %s not found", runID)
	}
	for _, m := range r.Members {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("run %s has no member %q", runID, name)
	return MemberState{}
}

// waitStatus waits until a member of a run has the status.
func waitStatus(t *testing.T, e *Engine, runID, name, status string, d time.Duration) MemberState {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		m := memberState(t, e, runID, name)
		if m.Status == status {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("member %q is %q after %v, want %q (%+v)", name, m.Status, d, status, m)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// logged reports whether a run's log has an entry whose message holds text.
func logged(r Run, text string) bool {
	return slices.ContainsFunc(r.Log, func(e session.ActivityEntry) bool { return strings.Contains(e.Message, text) })
}

// Two members start at once. Launch returns once their sessions exist; each
// process prints and goes quiet, and once it has been quiet for a second, and
// two have passed, the member's prompt is typed with the goal in it and an
// Enter, recorded as typed by the crew, and the member is running.
func TestLaunchTypesPromptsWhenReady(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = printOnce
	lead := immediate("lead", "Own the plan for $GOAL.")
	lead.Args = []string{"--model", "opus"}
	core := immediate("core", "Build ${GOAL} in Go.")
	core.AgentID = "codex"
	began := time.Now()
	run, err := e.Launch(t.Context(), testCrew(lead, core))
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took >= readyMin {
		t.Fatalf("Launch waited %v, for the prompts", took)
	}
	for _, m := range run.Members {
		if m.Status != MemberStarting || m.SessionID == "" {
			t.Fatalf("at launch: %+v", m)
		}
	}
	want := map[string]string{"lead": "Own the plan for ship /v1/users.\r", "core": "Build ship /v1/users in Go.\r"}
	for name, prompt := range want {
		l, p := fl.member(name)
		if got := waitTyped(t, p, 10*time.Second); got != prompt {
			t.Errorf("%s was typed %q, want %q", name, got, prompt)
		}
		if took := time.Since(began); took < readyMin {
			t.Errorf("%s was typed after %v, before the %v a quiet member is given", name, took, readyMin)
		}
		waitStatus(t, e, run.ID, name, MemberRunning, 5*time.Second)
		entries := crewEntries(l)
		if len(entries) != 1 || entries[0].Message != strings.TrimSuffix(prompt, "\r") {
			t.Errorf("%s records %+v", name, entries)
		}
	}

	if !strings.HasPrefix(run.ID, "api-sweep-") || run.CrewID != "api-sweep" || run.Name != "API sweep" || run.Goal != "ship /v1/users" ||
		run.Cwd != "/work" || run.Isolation != IsolationNone || run.StartedAt.IsZero() || run.StoppedAt != nil || len(run.Members) != 2 {
		t.Fatalf("run %+v", run)
	}
	for _, m := range memberStates(t, e, run.ID) {
		l, _ := fl.member(m.Name)
		if m.Status != MemberRunning || m.SessionID != l.Info().ID || m.Started == nil || m.Ended != nil || m.Err != "" || m.Branch != "" || m.Worktree != "" {
			t.Errorf("member %+v", m)
		}
		if runID, name, ok := e.MemberOf(m.SessionID); !ok || runID != run.ID || name != m.Name {
			t.Errorf("MemberOf(%s) = %q %q %v", m.SessionID, runID, name, ok)
		}
	}
	if _, _, ok := e.MemberOf("nope"); ok {
		t.Error("MemberOf found a session outside the run")
	}
	for _, c := range fl.calls {
		wantArgs := map[string][]string{"lead": {"--model", "opus"}, "core": nil}[c.name]
		if c.cwd != "/work" || !slices.Equal(c.args, wantArgs) || !maps.Equal(c.env, map[string]string{"GOAL": "ship /v1/users"}) ||
			c.ref != (session.CrewRef{RunID: run.ID, CrewID: "api-sweep", Member: c.name}) || c.agentID != map[string]string{"lead": "claude", "core": "codex"}[c.name] {
			t.Errorf("launch %+v", c)
		}
	}
	got, ok := e.Get(run.ID)
	if !ok || got.ID != run.ID || !logged(got, "launched") || !logged(got, "lead started") || !logged(got, "typed lead's prompt") || !logged(got, "typed core's prompt") {
		t.Fatalf("Get: %v %+v", ok, got.Log)
	}
	if list := e.List(); len(list) != 1 || list[0].ID != run.ID {
		t.Fatalf("List: %+v", list)
	}
}

// A member that starts after another starts when that one first reports
// done once its prompt is typed, once: a done before its prompt, or a second
// one, starts nothing.
func TestAfterConditionStartsOnFirstDone(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = func(member string, p *sessiontest.FakeProc, l *session.Local) {
		if member == "core" {
			// Idle at start: done, before any prompt.
			l.SetAttention(session.AttentionDone, "idle", session.SourceAPI)
			return
		}
		printOnce(member, p, l)
	}
	run, err := e.Launch(t.Context(), testCrew(immediate("core", "Build it."), after("tests", "Test $GOAL.", "core")))
	if err != nil {
		t.Fatal(err)
	}
	if names := fl.launched(); !slices.Equal(names, []string{"core"}) {
		t.Fatalf("launched %v", names)
	}
	waitStatus(t, e, run.ID, "core", MemberRunning, 5*time.Second)
	if m := memberState(t, e, run.ID, "tests"); m.Status != MemberPending || m.SessionID != "" || m.Started != nil {
		t.Fatalf("tests before core is done: %+v", m)
	}
	core, _ := fl.member("core")
	core.SetAttention(session.AttentionWorking, "", session.SourceAPI)
	core.SetAttention(session.AttentionDone, "turn finished", session.SourceAPI)
	m := waitStatus(t, e, run.ID, "tests", MemberRunning, 10*time.Second)
	_, p := fl.member("tests")
	if got := typed(p); !slices.Equal(got, []string{"Test ship /v1/users.\r"}) {
		t.Fatalf("tests was typed %q", got)
	}
	if m.SessionID == "" || m.Started == nil {
		t.Fatalf("tests %+v", m)
	}

	// A second done starts nothing more.
	core.SetAttention(session.AttentionWorking, "", session.SourceAPI)
	core.SetAttention(session.AttentionDone, "finished again", session.SourceAPI)
	time.Sleep(300 * time.Millisecond)
	if names := fl.launched(); !slices.Equal(names, []string{"core", "tests"}) {
		t.Fatalf("launched %v", names)
	}
	if got, _ := e.Get(run.ID); !logged(got, "core is done") {
		t.Fatalf("log %+v", got.Log)
	}
}

// A member whose process ends before it is ready gets no prompt: it shows
// ended, with how its process ended, and the rest of the run carries on.
func TestEarlyExitDropsPrompt(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = func(member string, p *sessiontest.FakeProc, l *session.Local) {
		_ = p.Print("starting...\r\n")
		if member == "lead" {
			p.End(7)
		}
	}
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it.")))
	if err != nil {
		t.Fatalf("an early exit failed the launch: %v", err)
	}
	waitStatus(t, e, run.ID, "core", MemberRunning, 10*time.Second)
	_, lead := fl.member("lead")
	if got := typed(lead); len(got) != 0 {
		t.Fatalf("lead was typed %q", got)
	}
	m := waitStatus(t, e, run.ID, "lead", MemberEnded, 5*time.Second)
	if m.Status != MemberEnded || m.Ended == nil || !strings.Contains(m.Err, "exited (exit 7)") || !strings.Contains(m.Err, "prompt") {
		t.Fatalf("lead %+v", m)
	}
	_, core := fl.member("core")
	if got := typed(core); !slices.Equal(got, []string{"Build it.\r"}) {
		t.Fatalf("core was typed %q", got)
	}
	if m := memberState(t, e, run.ID, "core"); m.Status != MemberRunning {
		t.Fatalf("core %+v", m)
	}
	if got, _ := e.Get(run.ID); !logged(got, "lead ended before its prompt") || got.StoppedAt != nil {
		t.Fatalf("run %+v", got)
	}
}

// When a member cannot be launched, the members launched before it are
// stopped, no other is launched, and the launch fails with the reason. No
// run is left behind.
func TestLaunchFailureStopsStartedMembers(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = printOnce
	var forgotten []string
	e.OnForget = func(runID string) { forgotten = append(forgotten, runID) }
	limit := errors.New("session limit reached")
	fl.fail["core"] = limit
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it."), immediate("web", "Style it."), manual("tests", "")))
	if run != nil || !errors.Is(err, limit) || !strings.Contains(err.Error(), `"core"`) {
		t.Fatalf("Launch: %+v %v", run, err)
	}
	lead, p := fl.member("lead")
	if !p.Stopped() || lead.Info().Status != session.StatusStopped {
		t.Fatalf("lead was left running: %+v", lead.Info())
	}
	if got := typed(p); len(got) != 0 {
		t.Fatalf("lead was typed %q", got)
	}
	if names := fl.launched(); !slices.Equal(names, []string{"lead", "core"}) {
		t.Fatalf("launched %v", names)
	}
	if list := e.List(); len(list) != 0 {
		t.Fatalf("runs %+v", list)
	}
	if _, _, ok := e.MemberOf(lead.Info().ID); ok {
		t.Fatal("the stopped session still belongs to a run")
	}
	if len(forgotten) != 1 || !strings.HasPrefix(forgotten[0], "api-sweep-") {
		t.Fatalf("OnForget saw %v", forgotten)
	}
}

// A crew with no members, one that runs on a host, or an invalid one does not
// launch; nothing starts.
func TestLaunchRefusesCrewsThatCannotRun(t *testing.T) {
	e, fl := newEngine(t)
	hosted := testCrew(immediate("lead", ""))
	hosted.Where = WhereHost
	invalid := testCrew(immediate("Lead!", ""))
	badID := testCrew(immediate("lead", ""))
	badID.ID = "../escape"
	for name, c := range map[string]Crew{"no members": testCrew(), "on a host": hosted, "invalid": invalid, "an id that is none": badID} {
		if _, err := e.Launch(t.Context(), c); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(fl.launched()) != 0 || len(e.List()) != 0 {
		t.Fatalf("launched %v, runs %v", fl.launched(), e.List())
	}
}

// With isolation "worktree" the working directory must be a git repository;
// otherwise the launch fails before anything is made or started.
func TestLaunchNeedsARepositoryForWorktrees(t *testing.T) {
	newRepo(t) // skips without git
	e, fl := newEngine(t)
	c := testCrew(immediate("lead", "Plan it."))
	c.Isolation, c.Cwd = IsolationWorktree, t.TempDir()
	if _, err := e.Launch(t.Context(), c); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("Launch: %v", err)
	}
	if entries, _ := os.ReadDir(c.Cwd); len(entries) != 0 || len(fl.launched()) != 0 || len(e.List()) != 0 {
		t.Fatalf("made %v, launched %v, runs %v", entries, fl.launched(), e.List())
	}
}

// With isolation "worktree" each member works on a branch of its own in a
// worktree under <cwd>/.conductor/worktrees/<run>/<member>, made when it
// starts. A run reports what each branch adds and removes, read again at most
// every 10 s.
func TestLaunchMakesAWorktreePerMember(t *testing.T) {
	repo := newRepo(t)
	e, fl := newEngine(t)
	clock := time.Now()
	var clockMu sync.Mutex
	e.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	tick := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }
	fl.onLaunch = askAtOnce
	c := testCrew(immediate("lead", "Plan it."), manual("core", "Build it."))
	c.Isolation, c.Cwd = IsolationWorktree, repo
	run, err := e.Launch(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".conductor", "worktrees", run.ID, "lead")
	lead := run.Members[0]
	if lead.Worktree != path || lead.Branch != "crew/"+run.ID+"/lead" || fl.calls[0].cwd != path {
		t.Fatalf("lead %+v, launched in %s", lead, fl.calls[0].cwd)
	}
	if branch := runGit(t, path, "rev-parse", "--abbrev-ref", "HEAD"); branch != lead.Branch {
		t.Fatalf("the worktree is on %q", branch)
	}
	if core := run.Members[1]; core.Worktree != "" || core.Branch != "" {
		t.Fatalf("a member that has not started has a worktree: %+v", core)
	}
	diff := func() *Diff {
		t.Helper()
		got, ok := e.GetWithDiffs(t.Context(), run.ID)
		if !ok {
			t.Fatal("run not found")
		}
		if got.Members[1].Diff != nil {
			t.Fatalf("core has a diff: %+v", got.Members[1])
		}
		return got.Members[0].Diff
	}
	// A read cut short by its request is read again by the next.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, ok := e.GetWithDiffs(cancelled, run.ID); !ok || got.Members[0].Diff != nil {
		t.Fatalf("a cancelled read: %v %+v", ok, got.Members[0])
	}
	if d := diff(); d == nil || *d != (Diff{}) {
		t.Fatalf("diff before any commit: %+v", d)
	}
	writeFile(t, filepath.Join(path, "plan.md"), "a\nb\n")
	commitAll(t, path, "plan")
	if d := diff(); d == nil || *d != (Diff{}) {
		t.Fatalf("diff read again within 10 s: %+v", d)
	}
	tick(diffTTL)
	if d := diff(); d == nil || *d != (Diff{Added: 2}) {
		t.Fatalf("diff after 10 s: %+v", d)
	}
	if r, _ := e.Get(run.ID); r.Members[0].Diff != nil {
		t.Fatalf("Get carries a diff: %+v", r.Members[0])
	}
	// A read that fails keeps what was read before.
	moved := path + ".away"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	tick(diffTTL)
	if d := diff(); d == nil || *d != (Diff{Added: 2}) {
		t.Fatalf("diff after a failed read: %+v", d)
	}
	if err := os.Rename(moved, path); err != nil {
		t.Fatal(err)
	}
	// The worktrees are kept out of the repository's own status.
	if status := runGit(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("git status in the main checkout:\n%s", status)
	}

	// A member started later gets its worktree then.
	if err := e.StartMember(t.Context(), run.ID, "core"); err != nil {
		t.Fatal(err)
	}
	core := memberState(t, e, run.ID, "core")
	if core.Worktree != filepath.Join(repo, ".conductor", "worktrees", run.ID, "core") || core.Branch != "crew/"+run.ID+"/core" {
		t.Fatalf("core %+v", core)
	}
	// Stopping the run keeps the worktrees.
	if err := e.Stop(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, core.Worktree} {
		if _, err := os.Stat(filepath.Join(p, "README")); err != nil {
			t.Fatalf("worktree %s: %v", p, err)
		}
	}
	// A second launch excludes .conductor/ no second time.
	again, err := e.Launch(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, again.ID, "lead", MemberRunning, 5*time.Second)
	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	lines := strings.Split(string(exclude), "\n")
	if n := len(slices.DeleteFunc(lines, func(l string) bool { return l != ".conductor/" })); err != nil || n != 1 {
		t.Fatalf("info/exclude holds .conductor/ %d times (%v):\n%s", n, err, exclude)
	}
	if status := runGit(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("git status in the main checkout:\n%s", status)
	}
}

// A member that starts by hand starts when asked, once; asking for a member
// or a run that does not exist fails.
func TestStartMemberStartsAPendingMember(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), manual("tests", "Test it."), after("docs", "Write it up.", "tests")))
	if err != nil {
		t.Fatal(err)
	}
	if m := run.Members[1]; m.Status != MemberPending || m.SessionID != "" {
		t.Fatalf("tests before its start: %+v", m)
	}
	if err := e.StartMember(t.Context(), run.ID, "tests"); err != nil {
		t.Fatal(err)
	}
	m := waitStatus(t, e, run.ID, "tests", MemberRunning, 5*time.Second)
	_, p := fl.member("tests")
	if m.Status != MemberRunning || m.SessionID == "" || !slices.Equal(typed(p), []string{"Test it.\r"}) {
		t.Fatalf("tests %+v", m)
	}
	if err := e.StartMember(t.Context(), run.ID, "tests"); !errors.Is(err, ErrMemberStarted) {
		t.Fatalf("starting it again: %v", err)
	}
	// A member that starts after another can be started early.
	if err := e.StartMember(t.Context(), run.ID, "docs"); err != nil {
		t.Fatal(err)
	}
	if err := e.StartMember(t.Context(), run.ID, "nobody"); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("an unknown member: %v", err)
	}
	if err := e.StartMember(t.Context(), "nope", "tests"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("an unknown run: %v", err)
	}
	if names := fl.launched(); !slices.Equal(names, []string{"lead", "tests", "docs"}) {
		t.Fatalf("launched %v", names)
	}
}

// A member can join a running run: one that starts immediately starts, one
// that starts after another waits for it. The run holds members to the rules
// of a crew.
func TestAddMemberJoinsARun(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it.")))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AddMember(t.Context(), run.ID, immediate("core", "Build $GOAL.")); err != nil {
		t.Fatal(err)
	}
	_, p := fl.member("core")
	if m := waitStatus(t, e, run.ID, "core", MemberRunning, 5*time.Second); !slices.Equal(typed(p), []string{"Build ship /v1/users.\r"}) {
		t.Fatalf("core %+v", m)
	}
	if err := e.AddMember(t.Context(), run.ID, after("tests", "Test it.", "core")); err != nil {
		t.Fatal(err)
	}
	if m := memberState(t, e, run.ID, "tests"); m.Status != MemberPending {
		t.Fatalf("tests %+v", m)
	}
	core, _ := fl.member("core")
	core.SetAttention(session.AttentionDone, "done", session.SourceAPI)
	waitStatus(t, e, run.ID, "tests", MemberRunning, 5*time.Second)

	for name, m := range map[string]Member{
		"a name used twice":      immediate("core", ""),
		"an invalid name":        immediate("Core!", ""),
		"after a missing member": after("docs", "", "ghost"),
		"after itself":           after("docs", "", "docs"),
		"an unknown start":       {Name: "docs", AgentID: "shell", Start: Start{When: "later"}},
	} {
		if err := e.AddMember(t.Context(), run.ID, m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for i := len(memberStates(t, e, run.ID)); i < maxMembers; i++ {
		if err := e.AddMember(t.Context(), run.ID, manual(fmt.Sprintf("m%d", i), "")); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.AddMember(t.Context(), run.ID, manual("one-too-many", "")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a 13th member: %v", err)
	}
	if err := e.AddMember(t.Context(), "nope", manual("x", "")); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("an unknown run: %v", err)
	}
	if got, _ := e.Get(run.ID); len(got.Members) != maxMembers || !logged(got, "core joined") {
		t.Fatalf("run %+v", got)
	}
}

// A member that joins a run is held to the size of what is typed as its
// prompt, with the run's goal in it.
func TestAddMemberRefusesAPromptTooLongToType(t *testing.T) {
	e, _ := newEngine(t)
	c := testCrew(manual("lead", ""))
	c.Goal = strings.Repeat("é", 2000)
	run, err := e.Launch(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AddMember(t.Context(), run.ID, manual("tests", strings.Repeat("$GOAL ", 9))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("9 references: %v", err)
	}
	if err := e.AddMember(t.Context(), run.ID, manual("tests", strings.Repeat("$GOAL ", 8))); err != nil {
		t.Fatalf("8 references: %v", err)
	}
}

func memberStates(t *testing.T, e *Engine, runID string) []MemberState {
	t.Helper()
	r, ok := e.Get(runID)
	if !ok {
		t.Fatalf("run %s not found", runID)
	}
	return r.Members
}

// Stop stops every member's session and marks the run stopped; nothing starts
// in a stopped run afterwards, and stopping it again changes nothing.
func TestStopStopsEveryMember(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it."), after("tests", "", "core"), manual("docs", "")))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Stop(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lead", "core"} {
		l, p := fl.member(name)
		if !p.Stopped() || l.Info().Status != session.StatusStopped {
			t.Errorf("%s: %+v", name, l.Info())
		}
		if m := memberState(t, e, run.ID, name); m.Status != MemberEnded || m.Ended == nil {
			t.Errorf("%s: %+v", name, m)
		}
	}
	got, _ := e.Get(run.ID)
	if got.StoppedAt == nil || !logged(got, "stopped") {
		t.Fatalf("run %+v", got)
	}
	stoppedAt := *got.StoppedAt
	core, _ := fl.member("core")
	e.OnActivity(core.Info().ID, session.ActivityEntry{Type: session.ActivityAttention, Message: "done"}, session.AttentionDone)
	if err := e.StartMember(t.Context(), run.ID, "docs"); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("starting a member of a stopped run: %v", err)
	}
	if err := e.AddMember(t.Context(), run.ID, manual("late", "")); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("adding a member to a stopped run: %v", err)
	}
	if err := e.Stop(t.Context(), run.ID); err != nil {
		t.Fatalf("stopping again: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if names := fl.launched(); !slices.Equal(names, []string{"lead", "core"}) {
		t.Fatalf("launched %v", names)
	}
	if got, _ := e.Get(run.ID); !got.StoppedAt.Equal(stoppedAt) {
		t.Fatalf("stopped at %v, then %v", stoppedAt, got.StoppedAt)
	}
	if err := e.Stop(t.Context(), "nope"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("stopping an unknown run: %v", err)
	}
}

// Stopping a run while a member waits to be ready ends the wait at once: the
// start fails, its session is stopped and no prompt is typed.
func TestStopEndsAStartInProgress(t *testing.T) {
	e, fl := newEngine(t) // processes that never print: never ready
	run, err := e.Launch(t.Context(), testCrew(manual("lead", "Plan it.")))
	if err != nil {
		t.Fatal(err)
	}
	// StartMember returns once the session exists; the member then waits.
	if err := e.StartMember(t.Context(), run.ID, "lead"); err != nil {
		t.Fatal(err)
	}
	if m := memberState(t, e, run.ID, "lead"); m.Status != MemberStarting || m.SessionID == "" {
		t.Fatalf("lead %+v", m)
	}
	began := time.Now()
	if err := e.Stop(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("Stop took %v", took)
	}
	l, p := fl.member("lead")
	if !p.Stopped() || l.Info().Status != session.StatusStopped || len(typed(p)) != 0 {
		t.Fatalf("lead %+v, typed %q", l.Info(), typed(p))
	}
	if m := memberState(t, e, run.ID, "lead"); m.Status != MemberEnded {
		t.Fatalf("lead %+v", m)
	}
}

// When a member is not ready after readyCap, its prompt is typed anyway and
// the run log says so.
func TestNotReadyAfterTheCapTypesAnyway(t *testing.T) {
	e, fl := newEngine(t)
	e.await = func(context.Context, *session.Local) error { return errNotReady }
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	_, p := fl.member("lead")
	if got := typed(p); !slices.Equal(got, []string{"Plan it.\r"}) {
		t.Fatalf("lead was typed %q", got)
	}
	if got, _ := e.Get(run.ID); !logged(got, "not ready after 1m0s") {
		t.Fatalf("log %+v", got.Log)
	}
}

// A member without a prompt runs as soon as its session starts.
func TestAMemberWithoutAPromptRunsAtOnce(t *testing.T) {
	e, fl := newEngine(t)
	e.await = func(context.Context, *session.Local) error {
		t.Error("waited for a member with no prompt to be ready")
		return nil
	}
	run, err := e.Launch(t.Context(), testCrew(immediate("logs", "  \n")))
	if err != nil {
		t.Fatal(err)
	}
	_, p := fl.member("logs")
	if m := waitStatus(t, e, run.ID, "logs", MemberRunning, time.Second); len(typed(p)) != 0 {
		t.Fatalf("logs %+v", m)
	}
}

// The readiness rule: a member is ready when its agent says it waits or is
// done, or once its output has been quiet for a second after the first output
// and two seconds have passed; after a minute the wait gives up.
func TestReadiness(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return start.Add(d) }
	s := time.Second
	for _, tc := range []struct {
		name          string
		state         session.AttentionState
		lastOutput    time.Time
		now           time.Time
		ready, capped bool
	}{
		{"waiting for input at once", session.AttentionNeedsInput, time.Time{}, at(0), true, false},
		{"done at once", session.AttentionDone, time.Time{}, at(s / 2), true, false},
		{"working, output still coming", session.AttentionWorking, at(4500 * time.Millisecond), at(5 * s), false, false},
		{"working, but quiet", session.AttentionWorking, at(0), at(5 * s), true, false},
		{"no output yet", session.AttentionNone, time.Time{}, at(5 * s), false, false},
		{"output still coming", session.AttentionNone, at(4500 * time.Millisecond), at(5 * s), false, false},
		{"quiet, but too early", session.AttentionNone, at(0), at(1500 * time.Millisecond), false, false},
		{"quiet for a second at two seconds", session.AttentionNone, at(s), at(2 * s), true, false},
		{"never quiet", session.AttentionNone, at(60 * s), at(60 * s), false, true},
		{"no output ever", session.AttentionNone, time.Time{}, at(61 * s), false, true},
	} {
		ready, capped := readyAt(tc.state, tc.lastOutput, start, tc.now)
		if ready != tc.ready || capped != tc.capped {
			t.Errorf("%s: ready %v capped %v, want %v %v", tc.name, ready, capped, tc.ready, tc.capped)
		}
	}
}

// A run keeps the newest maxRunLog entries of its log.
func TestRunLogKeepsTheNewest(t *testing.T) {
	r := &run{}
	for i := range maxRunLog + 50 {
		r.note(session.ActivityStatus, "entry %d", i)
	}
	if len(r.log) != maxRunLog || r.log[0].Message != "entry 50" || r.log[maxRunLog-1].Message != fmt.Sprintf("entry %d", maxRunLog+49) {
		t.Fatalf("%d entries, from %q to %q", len(r.log), r.log[0].Message, r.log[len(r.log)-1].Message)
	}
	long := strings.Repeat("x", 2*session.MaxAttentionMessage)
	r.note(session.ActivityError, "%s", long)
	if got := r.log[len(r.log)-1]; len(got.Message) > session.MaxAttentionMessage || got.At.IsZero() {
		t.Fatalf("entry of %d bytes at %v", len(got.Message), got.At)
	}
}

// The engine keeps at most maxRuns runs: a launch past it forgets the oldest
// run that has nothing running.
func TestOldRunsAreForgotten(t *testing.T) {
	e, _ := newEngine(t)
	var forgotten []string
	e.OnForget = func(runID string) { forgotten = append(forgotten, runID) }
	var first, second string
	for i := range maxRuns + 1 {
		run, err := e.Launch(t.Context(), testCrew(manual("lead", "")))
		if err != nil {
			t.Fatal(err)
		}
		switch i {
		case 0:
			first = run.ID
		case 1:
			second = run.ID
		}
	}
	if _, ok := e.Get(first); ok {
		t.Fatal("the oldest run is still kept")
	}
	if _, ok := e.Get(second); !ok || len(e.List()) != maxRuns {
		t.Fatalf("%d runs kept", len(e.List()))
	}
	if !slices.Equal(forgotten, []string{first}) {
		t.Fatalf("OnForget saw %v, want [%s]", forgotten, first)
	}
}

// Note adds an entry to a run's log, cleaned and bounded as the run's own;
// a run the engine does not have takes none.
func TestNoteAddsToTheRunLog(t *testing.T) {
	e, _ := newEngine(t)
	run, err := e.Launch(t.Context(), testCrew(manual("lead", "")))
	if err != nil {
		t.Fatal(err)
	}
	e.Note(run.ID, session.ActivityLink, "link created: standup\x07 (view)")
	e.Note("nope", session.ActivityLink, "lost")
	got, _ := e.Get(run.ID)
	last := got.Log[len(got.Log)-1]
	if last.Type != session.ActivityLink || last.Message != "link created: standup (view)" || last.At.IsZero() {
		t.Fatalf("last entry %+v", last)
	}
	for range maxRunLog {
		e.Note(run.ID, session.ActivityLink, "again")
	}
	if got, _ := e.Get(run.ID); len(got.Log) != maxRunLog {
		t.Fatalf("%d entries", len(got.Log))
	}
}

// A crew whose working directory is inside a repository works in that
// directory of its worktrees, which live under it.
func TestLaunchFromADirectoryInARepository(t *testing.T) {
	repo := newRepo(t)
	sub := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, "main.go"), "package main\n")
	commitAll(t, repo, "api")
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	c := testCrew(immediate("lead", "Plan it."))
	c.Isolation, c.Cwd = IsolationWorktree, sub
	run, err := e.Launch(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(sub, ".conductor", "worktrees", run.ID, "lead")
	if m := run.Members[0]; m.Worktree != worktree || fl.calls[0].cwd != filepath.Join(worktree, "services", "api") {
		t.Fatalf("lead %+v, launched in %s", m, fl.calls[0].cwd)
	}
	if _, err := os.Stat(filepath.Join(fl.calls[0].cwd, "main.go")); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	if status := runGit(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("git status in the main checkout:\n%s", status)
	}

	// A directory no commit has a file in is made in the worktree.
	empty := filepath.Join(repo, "scratch", "notes")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	c.Cwd = empty
	run, err = e.Launch(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(empty, ".conductor", "worktrees", run.ID, "lead", "scratch", "notes")
	if cwd := fl.calls[1].cwd; cwd != want {
		t.Fatalf("launched in %s, want %s", cwd, want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("the member's directory: %v", err)
	}
}

// Worktrees stay inside the working directory: a .conductor or
// .conductor/worktrees that is a symbolic link is refused (ErrInvalid) before
// git runs.
func TestLaunchRefusesWorktreesThroughASymlink(t *testing.T) {
	for _, link := range []string{".conductor", filepath.Join(".conductor", "worktrees")} {
		repo := newRepo(t)
		outside := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, link)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(repo, link)); err != nil {
			t.Fatal(err)
		}
		e, fl := newEngine(t)
		c := testCrew(immediate("lead", "Plan it."))
		c.Isolation, c.Cwd = IsolationWorktree, repo
		if _, err := e.Launch(t.Context(), c); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("%s: %v", link, err)
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 || len(fl.launched()) != 0 || len(e.List()) != 0 {
			t.Fatalf("%s: made %v, launched %v", link, entries, fl.launched())
		}
	}
}

// A directory on the way to a member's directory in its worktree that is a
// symbolic link, as a commit may hold one, is refused (ErrInvalid) before
// anything is made through it.
func TestLaunchRefusesASymlinkOnTheWayToTheMembersDirectory(t *testing.T) {
	repo := newRepo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, "scratch")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "a link out")
	// The main checkout has a directory where the commit has the link.
	if err := os.Remove(filepath.Join(repo, "scratch")); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(repo, "scratch", "notes")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	e, fl := newEngine(t)
	c := testCrew(immediate("lead", "Plan it."))
	c.Isolation, c.Cwd = IsolationWorktree, cwd
	if _, err := e.Launch(t.Context(), c); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Launch: %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 || len(fl.launched()) != 0 || len(e.List()) != 0 {
		t.Fatalf("made %v outside, launched %v", entries, fl.launched())
	}
}

// When the repository's info/exclude cannot be written, the worktrees work
// without it and the run log says so once, not once per member.
func TestAnExcludeFailureIsNotedOncePerRun(t *testing.T) {
	repo := newRepo(t)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.RemoveAll(exclude); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(exclude, 0o755); err != nil { // not a file: it cannot be read or written
		t.Fatal(err)
	}
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	c := testCrew(immediate("lead", "Plan it."), immediate("core", "Build it."), manual("tests", ""))
	c.Isolation, c.Cwd = IsolationWorktree, repo
	run, err := e.Launch(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.StartMember(t.Context(), run.ID, "tests"); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Get(run.ID)
	n := 0
	for _, entry := range got.Log {
		if strings.Contains(entry.Message, "info/exclude") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d notes of the exclude failure: %+v", n, got.Log)
	}
}

// A member whose prompt cannot be typed ends alone; the run and the other
// members go on.
func TestAPromptFailureEndsOnlyThatMember(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	boom := errors.New("boom")
	e.await = func(ctx context.Context, l *session.Local) error {
		if l.Info().Crew.Member == "lead" {
			return boom
		}
		return awaitReady(ctx, l)
	}
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it.")))
	if err != nil {
		t.Fatal(err)
	}
	m := waitStatus(t, e, run.ID, "lead", MemberEnded, 5*time.Second)
	if !strings.Contains(m.Err, "boom") {
		t.Fatalf("lead %+v", m)
	}
	lead, p := fl.member("lead")
	select {
	case <-lead.Ended():
	case <-time.After(5 * time.Second):
		t.Fatal("lead's session still runs")
	}
	if !p.Stopped() || lead.Info().Status != session.StatusStopped || len(typed(p)) != 0 {
		t.Fatalf("lead %+v", lead.Info())
	}
	waitStatus(t, e, run.ID, "core", MemberRunning, 5*time.Second)
	if got, _ := e.Get(run.ID); got.StoppedAt != nil || !logged(got, "lead could not start") {
		t.Fatalf("run %+v", got)
	}
}

// A process that exits as its prompt is written ends its member as an early
// exit, not as a failure to write.
func TestAnExitAsThePromptIsTypedIsAnEarlyExit(t *testing.T) {
	e, fl := newEngine(t)
	e.await = func(_ context.Context, l *session.Local) error {
		_, p := fl.member(l.Info().Crew.Member)
		p.End(3)
		return nil
	}
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it.")))
	if err != nil {
		t.Fatal(err)
	}
	m := waitStatus(t, e, run.ID, "lead", MemberEnded, 5*time.Second)
	if !strings.Contains(m.Err, "exited (exit 3) before its prompt") {
		t.Fatalf("lead %+v", m)
	}
}

// A done the agent reports as its prompt is being written counts.
func TestADoneAsThePromptIsTypedCounts(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = func(member string, p *sessiontest.FakeProc, l *session.Local) {
		if member != "core" {
			return
		}
		l.SetAttention(session.AttentionNeedsInput, "what next?", session.SourceAPI)
		<-p.Input // the prompt: done at once
		l.SetAttention(session.AttentionDone, "finished", session.SourceAPI)
	}
	run, err := e.Launch(t.Context(), testCrew(immediate("core", "Build it."), after("tests", "", "core")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "tests", MemberRunning, 5*time.Second)
}

// A Stop while Launch starts the sessions keeps the run, stopped: Launch
// says the run is stopped, and nothing is left running.
func TestStopDuringLaunchKeepsTheRun(t *testing.T) {
	e, fl := newEngine(t)
	fl.block = map[string]bool{"core": true}
	done := make(chan error, 1)
	go func() {
		_, err := e.Launch(context.Background(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it."), immediate("web", "Style it.")))
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(fl.launched(), "core") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	runs := e.List()
	if len(runs) != 1 {
		t.Fatalf("runs %+v", runs)
	}
	if err := e.Stop(t.Context(), runs[0].ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrRunStopped) {
			t.Fatalf("Launch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Launch still runs after Stop")
	}
	got, ok := e.Get(runs[0].ID)
	if !ok || got.StoppedAt == nil {
		t.Fatalf("run %v %+v", ok, got)
	}
	lead, p := fl.member("lead")
	if !p.Stopped() || lead.Info().Status != session.StatusStopped {
		t.Fatalf("lead %+v", lead.Info())
	}
	if m := memberState(t, e, got.ID, "lead"); m.Status != MemberEnded {
		t.Fatalf("lead %+v", m)
	}
	if m := memberState(t, e, got.ID, "web"); m.Status != MemberPending {
		t.Fatalf("web %+v", m)
	}
	if names := fl.launched(); !slices.Equal(names, []string{"lead", "core"}) {
		t.Fatalf("launched %v", names)
	}
}
