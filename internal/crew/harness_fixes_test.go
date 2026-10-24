package crew

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
	"github.com/phenixrizen/conductor/internal/session"
)

// The five defects the harness's live check found (#351–#355), each pinned
// where the engine decides it.

// A work task that needs a review starts at the commit the review
// reviewed, its candidate: a review submits no commit, and its dependent
// fell through to the run's base with an empty input commit (#351).
func TestATaskThatNeedsAReviewStartsAtTheReviewedCommit(t *testing.T) {
	dir := newRepo(t)
	e, _, _ := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), review("check", "reviewer", "build"), work("docs", "writer", "check")))
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, b, "built.txt", "yes\n", "built")
	built := waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	commit := built.Result.Commit
	rev := waitAttempt(t, e, run.ID, "check", ledger.AttemptRunning)
	submit(t, e, rev.Attempt.SessionID, Result{Verdict: VerdictApproved, Summary: "fine"})
	d := waitAttempt(t, e, run.ID, "docs", ledger.AttemptRunning)
	if d.Attempt.Start != commit {
		t.Fatalf("docs starts at %s, the reviewed commit is %s", d.Attempt.Start, commit)
	}
	md, f := taskFileOf(t, d)
	if len(f.Inputs) != 1 || f.Inputs[0].Commit != commit || f.Inputs[0].Verdict != VerdictApproved {
		t.Fatalf("docs' input: %+v", f.Inputs)
	}
	if !strings.Contains(f.StartFrom, `review "check" reviewed`) || !strings.Contains(md, "commit "+commit) {
		t.Fatalf("the task file does not say where docs starts: %q\n%s", f.StartFrom, md)
	}
	if strings.Contains(md, "(the run's base)") {
		t.Fatalf("the task file calls the reviewed commit the run's base:\n%s", md)
	}
	stopRun(t, e, run.ID)
}

// A message to a member whose task has succeeded is typed into its session,
// alive and idle for inspection: it waited for a next session that never
// came (#352).
func TestAMessageToAMemberWhoseTaskSucceededIsTypedIntoItsIdleSession(t *testing.T) {
	dir := newRepo(t)
	e, fl, _ := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), work("docs", "writer", "build")))
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	_, leadProc := fl.attemptSession(t, b)
	commitResult(t, e, b, "built.txt", "yes\n", "built")
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	d := waitAttempt(t, e, run.ID, "docs", ledger.AttemptRunning)
	typed(leadProc) // what was typed before: the task line
	if _, err := e.SendMessage(context.Background(), d.Attempt.SessionID, "lead", "is internal/api final?"); err != nil {
		t.Fatal(err)
	}
	want := "Message from writer (#1): is internal/api final?"
	deadline := time.Now().Add(10 * time.Second)
	var lines []string
	for !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, want) }) {
		if time.Now().After(deadline) {
			t.Fatalf("the message was not typed into lead's idle session: %q", lines)
		}
		lines = append(lines, typed(leadProc)...)
		time.Sleep(20 * time.Millisecond)
	}
	waitMessages(t, e, run.ID, "delivered", func(ms []Message) bool { return len(ms) == 1 && ms[0].DeliveredAt != nil })
	stopRun(t, e, run.ID)
}

// A blocker's question leaves the session when the blocker clears: by the
// attempt's result, its task done, nothing is left asking (#353).
func TestABlockersQuestionLeavesWithTheBlocker(t *testing.T) {
	dir := newRepo(t)
	e, fl, _ := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead")))
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	l, _ := fl.attemptSession(t, b)
	if _, err := e.Blocked(context.Background(), b.Attempt.SessionID, "style", "Which heading style?"); err != nil {
		t.Fatal(err)
	}
	asked := waitAttention(t, l, "the blocker's question", func(a session.Attention) bool {
		return a.State == session.AttentionNeedsInput && a.Message == "Which heading style?"
	})
	commitResult(t, e, b, "built.txt", "yes\n", "built")
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	waitAttention(t, l, "the question withdrawn", func(a session.Attention) bool {
		return a.State != session.AttentionNeedsInput || a.ID != asked.ID
	})
	stopRun(t, e, run.ID)
}

// A check that fails with one short line keeps that line: in the attempt's
// error, in the ledger's tail and in the next attempt's task file, which
// says where that attempt starts (#355).
func TestAShortCheckFailureReachesTheNextAttempt(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{{ID: "strict", Argv: []string{"sh", "-c", "test -f fixed.txt || { echo 'fixed.txt must exist first'; exit 1; }"}}}
	run := launchManaged(t, e, repoCrew(dir, build))
	a1 := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	if _, f := taskFileOf(t, a1); f.StartFrom != "the run's base" {
		t.Fatalf("attempt 1 starts at %q", f.StartFrom)
	}
	commitResult(t, e, a1, "built.txt", "yes\n", "built")
	a2 := waitTaskWhere(t, e, run.ID, "build", "attempt 2 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	md, f := taskFileOf(t, a2)
	if f.Previous == nil || !strings.Contains(f.Previous.Error, "fixed.txt must exist first") || !strings.Contains(md, "fixed.txt must exist first") {
		t.Fatalf("the next attempt is not told why: %+v\n%s", f.Previous, md)
	}
	if f.StartFrom != "attempt 1's result" || strings.Contains(md, "(the run's base)") {
		t.Fatalf("attempt 2 starts at %q:\n%s", f.StartFrom, md)
	}
	l.mu.Lock()
	rows := slices.Clone(l.checks)
	l.mu.Unlock()
	tailed := false
	for _, c := range rows {
		if c.ID == "strict" && strings.Contains(c.Tail, "fixed.txt must exist first") {
			tailed = true
		}
	}
	if !tailed {
		t.Fatalf("the ledger's check row has no tail: %+v", rows)
	}
	stopRun(t, e, run.ID)
}

// waitAttention waits for the session's attention to be as ok says.
func waitAttention(t *testing.T, l *session.Local, what string, ok func(session.Attention) bool) session.Attention {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		a := l.Info().Attention
		if ok(a) {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the session's attention is %+v", what, a)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A run stopped after its review approved, continued: the review's result
// is imported, and the task needing it starts at the reviewed commit in
// the continuation too, its result held to descend from it (Codex's review
// of #351: an imported review had no attempt to take its start from).
func TestAContinuationStartsAReviewsDependentAtTheReviewedCommit(t *testing.T) {
	dir := newRepo(t)
	e, _, _ := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), review("check", "reviewer", "build"), work("docs", "writer", "check")))
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, b, "built.txt", "yes\n", "built")
	commit := waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1).Result.Commit
	rev := waitAttempt(t, e, run.ID, "check", ledger.AttemptRunning)
	submit(t, e, rev.Attempt.SessionID, Result{Verdict: VerdictApproved, Summary: "fine"})
	waitAttempt(t, e, run.ID, "docs", ledger.AttemptRunning)
	if err := e.Stop(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	next, err := e.ContinueRun(context.Background(), run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	d := waitTaskWhere(t, e, next.ID, "docs", "at attempt 2, running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	if d.Attempt.Start != commit {
		t.Fatalf("docs starts at %s in the continuation, the reviewed commit is %s", d.Attempt.Start, commit)
	}
	if _, f := taskFileOf(t, d); len(f.Inputs) != 1 || f.Inputs[0].Commit != commit {
		t.Fatalf("docs' input in the continuation: %+v", f.Inputs)
	}
	stopRun(t, e, next.ID)
}

// A blocker reported twice with the same words keeps its question's id, so
// the attempt's result still takes the question away (Codex's review of
// #353).
func TestABlockerReportedTwiceStillLeavesWithTheResult(t *testing.T) {
	dir := newRepo(t)
	e, fl, _ := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead")))
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	l, _ := fl.attemptSession(t, b)
	for range 2 {
		if _, err := e.Blocked(context.Background(), b.Attempt.SessionID, "style", "Which heading style?"); err != nil {
			t.Fatal(err)
		}
	}
	asked := waitAttention(t, l, "the blocker's question", func(a session.Attention) bool {
		return a.State == session.AttentionNeedsInput && a.Message == "Which heading style?"
	})
	commitResult(t, e, b, "built.txt", "yes\n", "built")
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	waitAttention(t, l, "the question withdrawn", func(a session.Attention) bool {
		return a.State != session.AttentionNeedsInput || a.ID != asked.ID
	})
	stopRun(t, e, run.ID)
}
