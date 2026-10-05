package crew

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

// stopRun stops the run and waits for its stop to complete.
func stopRun(t *testing.T, e *Engine, runID string) {
	t.Helper()
	if err := e.Stop(t.Context(), runID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Get(runID)
	if got.StoppedAt == nil || got.State != RunStopped {
		t.Fatalf("not stopped: %+v", got)
	}
}

func TestResumeMemberReopensACompletedStop(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	waitStatus(t, e, run.ID, "core", MemberRunning, 5*time.Second)
	stopRun(t, e, run.ID)
	id, err := e.ResumeMember(t.Context(), run.ID, "lead", "3f80c8bd-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatalf("resume on a stopped run: %v", err)
	}
	got, _ := e.Get(run.ID)
	// Reopened: no longer stopped (the fake sessions ask at once, so the
	// state is needs_input rather than running).
	if got.StoppedAt != nil || got.State == RunStopped || got.State == RunFinished {
		t.Fatalf("not reopened: %+v", got)
	}
	if !logged(got, "reopened: lead resumes") || !logged(got, "lead resumed its conversation") {
		t.Fatalf("log %+v", got.Log)
	}
	lead := memberState(t, e, run.ID, "lead")
	core := memberState(t, e, run.ID, "core")
	if lead.SessionID != id || lead.Status != MemberRunning || core.Status != MemberEnded {
		t.Fatalf("lead %+v core %+v", lead, core)
	}
	// The other ended member is resumed on its own, afresh.
	if _, err := e.ResumeMember(t.Context(), run.ID, "core", ""); err != nil {
		t.Fatal(err)
	}
	_, p := fl.member("core")
	if got := waitTyped(t, p, 5*time.Second); got != "Build it.\r" {
		t.Fatalf("core anew: typed %q", got)
	}
	// A reopened run stops again.
	stopRun(t, e, run.ID)
	if st := memberState(t, e, run.ID, "lead"); st.Status != MemberEnded {
		t.Fatalf("lead after the second stop: %+v", st)
	}
	if got, _ := e.Get(run.ID); strings.Count(entriesText(got), "stopped") < 2 {
		t.Fatalf("log %+v", got.Log)
	}
}

func entriesText(r Run) string {
	var b strings.Builder
	for _, e := range r.Log {
		b.WriteString(e.Message + "\n")
	}
	return b.String()
}

func TestResumeMemberRefusesAStopCutShort(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	fl.block = map[string]bool{"slow": true}
	run, _, err := e.LaunchHeld(t.Context(), testCrew(immediate("lead", "Plan it."), manual("slow", "Later.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	// A start that never ends: the stop, given no time, is cut short.
	go func() { _ = e.StartMember(context.Background(), run.ID, "slow") }()
	waitStatus(t, e, run.ID, "slow", MemberStarting, 5*time.Second)
	cut, cancel := context.WithCancel(t.Context())
	cancel()
	_ = e.Stop(cut, run.ID)
	if _, err := e.ResumeMember(t.Context(), run.ID, "lead", ""); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("a stop cut short reopened: %v", err)
	}
	if _, err := e.ResumeRun(t.Context(), run.ID); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("a stop cut short resumed as a new run: %v", err)
	}
}

func TestResumeRunResumesEveryConversationInItsWorktree(t *testing.T) {
	repo := newRepo(t)
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	c := testCrew(immediate("lead", "Plan it."), after("core", "Build it.", "lead"), immediate("docs", "Write it."), manual("qa", "Check it."))
	c.Isolation, c.Cwd = IsolationWorktree, repo
	run, err := e.Launch(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	waitStatus(t, e, run.ID, "docs", MemberRunning, 5*time.Second)
	lead, _ := fl.member("lead")
	lead.SetAgentSession(session.AgentSession{ID: "3f80c8bd-0000-4000-8000-00000000lead", Resumable: true, Source: session.AgentSessionHook})
	leadWorktree := memberState(t, e, run.ID, "lead").Worktree
	stopRun(t, e, run.ID)
	worktrees := runGit(t, repo, "worktree", "list", "--porcelain")
	if _, err := e.ResumeRun(t.Context(), "nope"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	next, err := e.ResumeRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == run.ID || next.CrewID != run.CrewID || next.ResumedFrom != run.ID || next.Goal != run.Goal || next.Isolation != IsolationWorktree {
		t.Fatalf("next %+v", next)
	}
	old, _ := e.Get(run.ID)
	if old.ResumedBy != next.ID || old.State != RunStopped || !logged(old, "resumed as "+next.ID) {
		t.Fatalf("old %+v", old)
	}
	// lead continues in its kept worktree and branch, without a prompt.
	nl := memberState(t, e, next.ID, "lead")
	if nl.Status != MemberRunning || nl.Worktree != leadWorktree || nl.Branch != "crew/"+run.ID+"/lead" || nl.SessionID == "" {
		t.Fatalf("lead %+v", nl)
	}
	fl.mu.Lock()
	var leadCall launchCall
	for _, c := range fl.calls {
		if c.ref.RunID == next.ID && c.name == "lead" {
			leadCall = c
		}
	}
	fl.mu.Unlock()
	if leadCall.resume != "3f80c8bd-0000-4000-8000-00000000lead" || leadCall.cwd != leadWorktree || leadCall.ref.RunID != next.ID {
		t.Fatalf("lead's launch %+v", leadCall)
	}
	_, lp := fl.member("lead")
	time.Sleep(100 * time.Millisecond)
	if w := typed(lp); len(w) != 0 {
		t.Fatalf("a resumed lead was typed %q", w)
	}
	if got := runGit(t, repo, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != strings.Count(worktrees, "worktree ")+1 {
		t.Fatalf("worktrees: only docs (afresh) makes a new one:\n%s", got)
	}
	// docs had no resumable conversation: it starts afresh, with its prompt, in a worktree of the new run.
	waitStatus(t, e, next.ID, "docs", MemberRunning, 5*time.Second)
	nd := memberState(t, e, next.ID, "docs")
	if nd.Worktree != filepath.Join(repo, ".conductor", "worktrees", next.ID, "docs") {
		t.Fatalf("docs %+v", nd)
	}
	_, dp := fl.member("docs")
	if got := waitTyped(t, dp, 5*time.Second); got != "Write it.\r" {
		t.Fatalf("docs: typed %q", got)
	}
	// core waits for lead's next done; qa for a person.
	if st := memberState(t, e, next.ID, "core"); st.Status != MemberPending {
		t.Fatalf("core %+v", st)
	}
	if st := memberState(t, e, next.ID, "qa"); st.Status != MemberPending {
		t.Fatalf("qa %+v", st)
	}
	nlead, _ := fl.member("lead")
	nlead.SetAttention(session.AttentionDone, "planned", session.SourceAPI)
	waitStatus(t, e, next.ID, "core", MemberRunning, 5*time.Second)
	if got, _ := e.Get(next.ID); !logged(got, "lead is done: starting core") || !logged(got, "resumes "+run.ID) {
		t.Fatalf("log %+v", got.Log)
	}
	if _, err := e.ResumeRun(t.Context(), next.ID); !errors.Is(err, ErrRunRunning) {
		t.Fatalf("a running run resumed: %v", err)
	}
}

func TestResumeRunOfAFinishedRunStartsTheOthersAnew(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	waitStatus(t, e, run.ID, "core", MemberRunning, 5*time.Second)
	lead, lp := fl.member("lead")
	lead.SetAgentSession(session.AgentSession{ID: "3f80c8bd-0000-4000-8000-00000000lead", Resumable: true, Source: session.AgentSessionHook})
	core, cp := fl.member("core")
	lp.End(0)
	cp.End(0)
	<-lead.Ended()
	<-core.Ended()
	if got, _ := e.Get(run.ID); got.State != RunFinished {
		t.Fatalf("not finished: %+v", got)
	}
	next, err := e.ResumeRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st := memberState(t, e, next.ID, "lead"); st.Status != MemberRunning {
		t.Fatalf("lead %+v", st)
	}
	waitStatus(t, e, next.ID, "core", MemberRunning, 5*time.Second)
	_, ncp := fl.member("core")
	if got := waitTyped(t, ncp, 5*time.Second); got != "Build it.\r" {
		t.Fatalf("core anew: typed %q", got)
	}
	if st := memberState(t, e, next.ID, "core"); st.SessionID == "" || st.Status != MemberRunning {
		t.Fatalf("core %+v", st)
	}
}
