package crew

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/crew/ledger"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// The Codex review of rows 6–10 (2026-10-23): each finding's scenario, as
// a test.

// A command the ledger does not take (its cap, another content under the
// same id) changes nothing; a stale revision is kept as a failed receipt
// that a repeat answers again.
func TestACommandTheLedgerDoesNotTakeChangesNothing(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead")))
	ctx := context.Background()
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	l.mu.Lock()
	l.commandCap = 1
	l.mu.Unlock()
	if rc, err := e.Pause(ctx, run.ID, "admin", Command{ID: "p1"}, true); err != nil || rc.Status != ledger.CommandDone {
		t.Fatalf("the first command: %+v %v", rc, err)
	}
	// The cap: a cancel refused by it leaves the attempt running and its session live.
	if _, err := e.Cancel(ctx, run.ID, "admin", Command{ID: "c1"}, "build"); !errors.Is(err, ErrTooManyCommands) {
		t.Fatalf("over the cap: %v", err)
	}
	if got := taskOf(t, e, run.ID, "build"); got.State != ledger.TaskRunning || got.Attempt == nil || got.Attempt.State != ledger.AttemptRunning {
		t.Fatalf("the task after the refused cancel: %+v", got)
	}
	if r, _ := e.Get(run.ID); !r.Paused || r.Outcome != "" {
		t.Fatalf("the run after the refused cancel: paused=%v outcome=%q", r.Paused, r.Outcome)
	}
	if _, ok, _ := l.Receipt(ctx, run.ID, "admin", "c1"); ok {
		t.Fatal("a refused command has a row")
	}
	l.mu.Lock()
	l.commandCap = 0
	l.mu.Unlock()
	// The same id with other content: a conflict, nothing changed.
	if _, err := e.Cancel(ctx, run.ID, "admin", Command{ID: "p1"}, "build"); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("other content: %v", err)
	}
	if got := taskOf(t, e, run.ID, "build"); got.State != ledger.TaskRunning {
		t.Fatalf("the task after the conflict: %+v", got)
	}
	// A stale revision: refused, kept as a failed receipt, answered the same again.
	rc, err := e.Pause(ctx, run.ID, "admin", Command{ID: "p2", ExpectedRevision: 1}, false)
	if !errors.Is(err, ErrStaleRevision) || rc.Status != ledger.CommandFailed || rc.Refusal != RefusalStaleRevision {
		t.Fatalf("stale: %+v %v", rc, err)
	}
	if r, _ := e.Get(run.ID); !r.Paused {
		t.Fatal("a stale unpause acted")
	}
	if again, err := e.Pause(ctx, run.ID, "admin", Command{ID: "p2", ExpectedRevision: 1}, false); err != nil || !again.Repeat || again.Status != ledger.CommandFailed || again.Refusal != RefusalStaleRevision {
		t.Fatalf("the stale receipt again: %+v %v", again, err)
	}
	if kinds := l.kinds(run.ID); !slices.Contains(kinds, "command.accepted") {
		t.Fatalf("no command.accepted in %v", kinds)
	}
	_ = b
	stopRun(t, e, run.ID)
}

// A review saves no checkpoint, and a review's attempt always starts at
// its candidate; a checkpoint too large for the ledger is refused before
// anything changes.
func TestAReviewHasNoCheckpointAndAlwaysStartsAtItsCandidate(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), review("review", "critic", "build")))
	ctx := context.Background()
	first := commitResult(t, e, waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning), "built.txt", "yes\n", "built")
	first = waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	rev := waitAttempt(t, e, run.ID, "review", ledger.AttemptRunning)
	if _, err := e.SaveCheckpoint(ctx, rev.Attempt.SessionID, "read it", "the verdict", ""); !errors.Is(err, ErrReviewNoCheck) {
		t.Fatalf("a review's checkpoint: %v", err)
	}
	// The review asks for changes; build's repair; the review's second attempt is at the repair, not at anything earlier.
	submit(t, e, rev.Attempt.SessionID, Result{Verdict: VerdictNeedsChanges, Summary: "more", Findings: []Finding{{Severity: "minor", Message: "more"}}})
	second := waitTaskWhere(t, e, run.ID, "build", "at attempt 2, running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	// An oversized checkpoint (CJK characters within the field bound, over the row's) is refused before any change.
	big := strings.Repeat("漢", 4000)
	if _, err := e.SaveCheckpoint(ctx, second.Attempt.SessionID, big, big, big); !errors.Is(err, ErrBadCheckpoint) {
		t.Fatalf("an oversized checkpoint: %v", err)
	}
	if got := taskOf(t, e, run.ID, "build"); got.Attempt.Checkpoint != nil {
		t.Fatalf("the oversized checkpoint stayed: %+v", got.Attempt.Checkpoint)
	}
	writeFile(t, filepath.Join(second.Attempt.Worktree, "built.txt"), "yes, more\n")
	commitAll(t, second.Attempt.Worktree, "more")
	repaired := commitResult(t, e, second, "note.txt", "n\n", "noted")
	repaired = waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 2)
	rev2 := waitTaskWhere(t, e, run.ID, "review", "at attempt 2, running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	if rev2.Attempt.Start != repaired.Result.Commit || rev2.Attempt.Start == first.Result.Commit {
		t.Fatalf("the review's second attempt starts at %s; the repair is %s, the first candidate %s", rev2.Attempt.Start, repaired.Result.Commit, first.Result.Commit)
	}
	_ = l
	stopRun(t, e, run.ID)
}

// A continuation carries a pending repair and a task's allowance, and
// keeps an imported result's source and summary through a second
// continuation; a message set out to be typed before the server ended is
// not typed again; carried messages survive a second continuation.
func TestAContinuationCarriesRepairsAllowancesImportsAndMessages(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), review("review", "critic", "build"), work("docs", "writer", "build")))
	ctx := context.Background()
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	if _, err := e.SendMessage(ctx, b.Attempt.SessionID, "writer", "for the docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SendMessage(ctx, b.Attempt.SessionID, "critic", "for the critic"); err != nil {
		t.Fatal(err)
	}
	first := commitResult(t, e, b, "built.txt", "yes\n", "built")
	first = waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	// docs starts at build's result and the writer's message is typed into it: delivered, recorded so, before anything withdraws it.
	waitMessages(t, e, run.ID, "the writer's message delivered", func(ms []Message) bool {
		return slices.ContainsFunc(ms, func(m Message) bool { return m.To == "writer" && m.DeliveredAt != nil })
	})
	// The run pauses; the review asks for changes; the repair waits unlaunched; the server ends.
	if err := e.PauseRun(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	rev := waitAttempt(t, e, run.ID, "review", ledger.AttemptRunning)
	submit(t, e, rev.Attempt.SessionID, Result{Verdict: VerdictNeedsChanges, Summary: "one thing", Findings: []Finding{{Severity: "major", Path: "built.txt", Message: "say why"}}})
	waitTaskWhere(t, e, run.ID, "build", "reopened", func(ts TaskState) bool { return ts.State == ledger.TaskReady && ts.Result == nil })
	// The critic's message was being typed as the server ended: typing set, delivered not.
	l.mu.Lock()
	for k, m := range l.messages {
		if m.To == "critic" {
			m.TypingAt, m.DeliveredAt = time.Now().UTC(), time.Time{}
			l.messages[k] = m
		}
	}
	l.mu.Unlock()
	interrupt(l, run.ID, nil)
	e2, fl2 := readyEngine(t)
	e2.UseLedger(l)
	next, err := e2.ContinueRun(ctx, run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Paused still; unpaused, build's repair starts at the reviewed commit with the findings.
	if r, _ := e2.Get(next.ID); !r.Paused {
		t.Fatal("the continuation is not paused")
	}
	if err := e2.PauseRun(ctx, next.ID, false); err != nil {
		t.Fatal(err)
	}
	b2 := waitAttempt(t, e2, next.ID, "build", ledger.AttemptRunning)
	if b2.Attempt.Start != first.Result.Commit {
		t.Fatalf("the repair starts at %s, the reviewed commit is %s", b2.Attempt.Start, first.Result.Commit)
	}
	if md, f := taskFileOf(t, b2); f.Repair == nil || f.Repair.Review != "review" || !strings.Contains(md, "say why") {
		t.Fatalf("the repair's task file: %+v\n%s", f.Repair, md)
	}
	// The messages carried: the critic's is uncertain and not typed again; the writer's waits.
	msgs, _ := e2.MessagesOf(next.ID)
	if len(msgs) != 2 {
		t.Fatalf("messages: %+v", msgs)
	}
	for _, m := range msgs {
		if m.To == "critic" && !m.Uncertain {
			t.Fatalf("the critic's message is not uncertain: %+v", m)
		}
	}
	if !slices.ContainsFunc(l.kinds(next.ID), func(k string) bool { return k == "run.continued" }) {
		t.Fatal("no run.continued")
	}
	if from, _, _ := l.Load(ctx, next.ID); len(from.Messages) != 2 {
		t.Fatalf("the continuation's own message rows: %+v", from.Messages)
	}
	// build's repair succeeds; the review runs again; the critic's session gets no second typing of the uncertain message.
	commitResult(t, e2, b2, "built.txt", "yes, because\n", "explained")
	waitTask(t, e2, next.ID, "build", ledger.TaskSucceeded, 2)
	rev2 := waitAttempt(t, e2, next.ID, "review", ledger.AttemptRunning)
	_, cp := fl2.attemptSession(t, rev2)
	var criticLines []string
	for range 10 {
		time.Sleep(50 * time.Millisecond)
		criticLines = append(criticLines, typed(cp)...)
	}
	if slices.ContainsFunc(criticLines, func(s string) bool { return strings.Contains(s, "for the critic") }) {
		t.Fatalf("the uncertain message was typed again: %q", criticLines)
	}
	submit(t, e2, rev2.Attempt.SessionID, Result{Verdict: VerdictApproved, Summary: "fine"})
	waitTask(t, e2, next.ID, "review", ledger.TaskSucceeded, 2)
	// docs starts again: the writer's message was typed into its first attempt before the server ended (delivered), so it is not
	// typed again; the writer reads it in its inbox.
	d := waitAttempt(t, e2, next.ID, "docs", ledger.AttemptRunning)
	_, dp := fl2.attemptSession(t, d)
	var docsLines []string
	for range 10 {
		time.Sleep(50 * time.Millisecond)
		docsLines = append(docsLines, typed(dp)...)
	}
	if slices.ContainsFunc(docsLines, func(s string) bool { return strings.Contains(s, "for the docs") }) {
		t.Fatalf("a delivered message was typed again: %q", docsLines)
	}
	if inbox, err := e2.Inbox(d.Attempt.SessionID, 0); err != nil || len(inbox) != 1 || inbox[0].Text != "for the docs" || inbox[0].DeliveredAt == nil {
		t.Fatalf("the writer's inbox in the continuation: %+v %v", inbox, err)
	}
	commitResult(t, e2, d, "docs.txt", "d\n", "docs")
	waitOutcome(t, e2, next.ID, "succeeded")
	// A second continuation: the imported results keep their source and summary; the messages are still there.
	docsResult := taskOf(t, e2, next.ID, "docs").Result.Attempt
	stopRun(t, e2, next.ID)
	e3, _ := readyEngine(t)
	e3.UseLedger(l)
	third, err := e3.ContinueRun(ctx, next.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if b3 := taskOf(t, e3, third.ID, "build"); b3.ImportedFrom != next.ID+"/build/2" || b3.Result == nil || b3.Result.Summary != "explained" {
		t.Fatalf("build twice continued: %+v %+v", b3, b3.Result)
	}
	if d3 := taskOf(t, e3, third.ID, "docs"); d3.ImportedFrom != fmt.Sprintf("%s/docs/%d", next.ID, docsResult) || d3.Result == nil || d3.Result.Summary != "docs" {
		t.Fatalf("docs twice continued: %+v %+v", d3, d3.Result)
	}
	if m3, _ := e3.MessagesOf(third.ID); len(m3) != 2 {
		t.Fatalf("messages twice continued: %+v", m3)
	}
	// The allowance: a task failed for lack of attempts, retried before the stop, keeps its extra attempt.
	stopRun(t, e3, third.ID)
	_ = fmt.Sprint
}

// A retry's allowance survives a continuation: the retried task gets its
// attempt rather than failing again at once.
func TestAContinuationKeepsARetrysAllowance(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{{ID: "never", Argv: []string{"sh", "-c", "exit 1"}}}
	run := launchManaged(t, e, repoCrew(dir, build))
	ctx := context.Background()
	for n := 1; n <= 3; n++ {
		a := waitTaskWhere(t, e, run.ID, "build", "running", func(ts TaskState) bool {
			return ts.Attempts == n && ts.Attempt != nil && ts.Attempt.State == ledger.AttemptRunning
		})
		commitResult(t, e, a, "built.txt", strings.Repeat("try\n", n), "try")
	}
	waitTask(t, e, run.ID, "build", ledger.TaskFailed, 3)
	if err := e.PauseRun(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.RetryTask(ctx, run.ID, "build"); err != nil {
		t.Fatal(err)
	}
	waitTaskWhere(t, e, run.ID, "build", "ready again", func(ts TaskState) bool { return ts.State == ledger.TaskReady })
	stopRun(t, e, run.ID)
	e2, _ := readyEngine(t)
	e2.UseLedger(l)
	next, err := e2.ContinueRun(ctx, run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e2.PauseRun(ctx, next.ID, false); err != nil {
		t.Fatal(err)
	}
	if b := waitAttempt(t, e2, next.ID, "build", ledger.AttemptRunning); b.Attempt.N != 4 || b.Attempts != 4 {
		t.Fatalf("the granted attempt: %+v", b)
	}
	stopRun(t, e2, next.ID)
}

// Delivery goes on past the queue's room: a member with more messages
// than the queue holds gets every one.
func TestDeliveryGoesOnPastTheQueuesRoom(t *testing.T) {
	dir := newRepo(t)
	e, fl, _ := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), work("docs", "writer", "build")))
	ctx := context.Background()
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	for i := 1; i <= 2*maxHandoffs+3; i++ {
		if _, err := e.SendMessage(ctx, b.Attempt.SessionID, "writer", fmt.Sprintf("message \x1b[31m%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	commitResult(t, e, b, "built.txt", "yes\n", "built")
	d := waitAttempt(t, e, run.ID, "docs", ledger.AttemptRunning)
	_, p := fl.attemptSession(t, d)
	deadline := time.Now().Add(30 * time.Second)
	var lines []string
	for {
		lines = append(lines, typed(p)...)
		n := 0
		for _, line := range lines {
			if strings.Contains(line, "Message from lead") {
				n++
				if strings.Contains(line, "\x1b") {
					t.Fatalf("an escape sequence was typed: %q", line)
				}
			}
		}
		if n == 2*maxHandoffs+3 {
			break
		}
		if time.Now().After(deadline) {
			r, _ := e.Get(run.ID)
			var log []string
			for _, l := range r.Log {
				log = append(log, l.Message)
			}
			t.Fatalf("%d of %d messages typed: %q\nlog: %s", n, 2*maxHandoffs+3, lines, strings.Join(log, " | "))
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitMessages(t, e, run.ID, "all delivered", func(ms []Message) bool {
		return len(ms) == 2*maxHandoffs+3 && !slices.ContainsFunc(ms, func(m Message) bool { return m.DeliveredAt == nil })
	})
	stopRun(t, e, run.ID)
}

// A withdrawn input clears a dependent's checkpoint; a recovered record
// drops a result the ledger withdrew.
func TestAWithdrawalClearsTheCheckpointAndTheRecoveredRecord(t *testing.T) {
	dir := newRepo(t)
	e, _, _ := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), review("review", "critic", "build"), work("docs", "writer", "build")))
	ctx := context.Background()
	first := commitResult(t, e, waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning), "built.txt", "yes\n", "built")
	first = waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	d := waitAttempt(t, e, run.ID, "docs", ledger.AttemptRunning)
	writeFile(t, filepath.Join(d.Attempt.Worktree, "docs.txt"), "half\n")
	commitAll(t, d.Attempt.Worktree, "half")
	if _, err := e.SaveCheckpoint(ctx, d.Attempt.SessionID, "half", "the rest", ""); err != nil {
		t.Fatal(err)
	}
	rev := waitAttempt(t, e, run.ID, "review", ledger.AttemptRunning)
	submit(t, e, rev.Attempt.SessionID, Result{Verdict: VerdictNeedsChanges, Summary: "more", Findings: []Finding{{Severity: "minor", Message: "more"}}})
	withdrawn := waitTaskWhere(t, e, run.ID, "docs", "withdrawn", func(ts TaskState) bool { return ts.State == ledger.TaskBlocked })
	// The recovered record of this moment drops docs' snapshot result and build's; the ledger's checkpoint is gone from the next start.
	rec := Run{ID: run.ID, State: RunRunning, Tasks: []TaskState{{ID: "build", State: ledger.TaskSucceeded, Result: &ResultState{Attempt: 1, Commit: first.Result.Commit}}, {ID: "docs", State: ledger.TaskRunning}}}
	e.mu.Lock()
	r := e.runs[run.ID]
	loaded := Loaded{Run: ledger.RunRow{ID: run.ID, State: ledger.RunInterrupted}, Tasks: []ledger.TaskRow{r.taskRow(r.task("build")), r.taskRow(r.task("docs"))}}
	e.mu.Unlock()
	if out := Recovered(rec, loaded); out.Tasks[0].Result != nil {
		t.Fatalf("the recovered record keeps the withdrawn result: %+v", out.Tasks[0])
	}
	// build's repair, then docs again: its attempt starts at the new result, not at its old checkpoint.
	second := waitTaskWhere(t, e, run.ID, "build", "at attempt 2, running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	repaired := commitResult(t, e, second, "built.txt", "yes, more\n", "more")
	repaired = waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 2)
	d2 := waitTaskWhere(t, e, run.ID, "docs", "at attempt 2, running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	if d2.Attempt.Start != repaired.Result.Commit {
		t.Fatalf("docs' second attempt starts at %s, the repair is %s (the old checkpoint %+v)", d2.Attempt.Start, repaired.Result.Commit, withdrawn.Attempt)
	}
	_ = session.ActivityStatus
	stopRun(t, e, run.ID)
}

// A message's line that the recipient's session refuses as it ends, the
// typing recorded and the line never in, is recorded as not typed and
// waits for the recipient's next session, typed there after its task
// line; nothing holds it uncertain (seen in CI's run of the review's
// continuation test).
func TestAMessageWhoseLineWasNotTypedWaitsForTheRecipientsNextSession(t *testing.T) {
	dir := newRepo(t)
	e, fl, l := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), work("docs", "writer")))
	ctx := context.Background()
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	d := waitAttempt(t, e, run.ID, "docs", ledger.AttemptRunning)
	dl, dp := fl.attemptSession(t, d)
	if line := waitTyped(t, dp, 10*time.Second); !strings.Contains(line, "Conductor task") {
		t.Fatalf("docs' first line: %q", line)
	}
	// The typing record held in its commit; docs' session ends under it.
	l.mu.Lock()
	l.blockOn, l.blocked, l.release = "message.typing", make(chan struct{}, 1), make(chan struct{})
	l.mu.Unlock()
	if _, err := e.SendMessage(ctx, b.Attempt.SessionID, "writer", "for the writer"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-l.blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("the typing did not reach its commit")
	}
	dp.End(0)
	select {
	case <-dl.Ended():
	case <-time.After(10 * time.Second):
		t.Fatal("docs' session did not end")
	}
	l.mu.Lock()
	l.blockOn = ""
	l.mu.Unlock()
	close(l.release)
	// Recorded as not typed, then typed into docs' second attempt after its task line, and delivered.
	d2 := waitTaskWhere(t, e, run.ID, "docs", "at its second attempt", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	_, dp2 := fl.attemptSession(t, d2)
	deadline := time.Now().Add(10 * time.Second)
	var lines []string
	for {
		lines = append(lines, typed(dp2)...)
		if slices.ContainsFunc(lines, func(s string) bool { return strings.Contains(s, "for the writer") }) {
			if !strings.Contains(lines[0], "Conductor task") {
				t.Fatalf("the message came before the task line: %q", lines)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the message was not typed into the second attempt: %q", lines)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitMessages(t, e, run.ID, "delivered", func(ms []Message) bool { return len(ms) == 1 && ms[0].DeliveredAt != nil && !ms[0].Uncertain })
	kinds := l.kinds(run.ID)
	untyped, last := slices.Index(kinds, "message.untyped"), 0
	for i, k := range kinds {
		if k == "message.typing" {
			last = i
		}
	}
	if untyped < 0 || untyped > last || slices.Index(kinds, "message.typing") > untyped {
		t.Fatalf("the records: %q", kinds)
	}
	stopRun(t, e, run.ID)
}
