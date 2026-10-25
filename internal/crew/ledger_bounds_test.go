package crew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
)

// sqlEngine is an engine on the real ledger, in a directory of the
// test's: the ledger's own bounds apply, as they do on a server.
func sqlEngine(t *testing.T) (*Engine, *ledger.Ledger) {
	t.Helper()
	e, fl := newEngine(t)
	fl.onLaunch = printOnce
	ld, err := ledger.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	if ro, why := ld.ReadOnly(); ro {
		t.Fatalf("the ledger opened read-only: %s", why)
	}
	e.UseLedger(SQLLedger(ld))
	return e, ld
}

// sqlKinds is the kinds of the run's events in the real ledger, in order.
func sqlKinds(t *testing.T, ld *ledger.Ledger, runID string) []string {
	t.Helper()
	evs, err := ld.Events(context.Background(), runID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Kind)
	}
	return out
}

// A change whose commit fails with no undo keeps its state ahead and its
// events with it: the pause's commit writes both, so the ledger never
// holds a reopened review's rows without the task.reopened that says so.
func TestAFailedCommitsEventsAreWrittenByTheNextCommit(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), review("check", "critic", "build")))
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, b, "built.txt", "b\n", "built")
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	c := waitAttempt(t, e, run.ID, "check", ledger.AttemptRunning)
	l.mu.Lock()
	l.failOn, l.failOnce = "task.reopened", true
	l.mu.Unlock()
	submit(t, e, c.Attempt.SessionID, Result{Verdict: VerdictNeedsChanges, Findings: []Finding{{Severity: "major", Message: "fix it"}}})
	deadline := time.Now().Add(10 * time.Second)
	for {
		kinds := l.kinds(run.ID)
		ro, rp := slices.Index(kinds, "task.reopened"), slices.Index(kinds, "run.paused")
		if ro >= 0 && rp >= 0 {
			if ro > rp {
				t.Fatalf("the reopen's event after the pause's: %v", kinds)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reopen's event was not written with the pause: %v", kinds)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r, _ := e.Get(run.ID); !r.Paused {
		t.Fatal("the run's dispatch is not paused after the refused commit")
	}
	stopRun(t, e, run.ID)
}

// A change undone after its commit failed takes its events with it: they
// are not written by a later commit.
func TestAnUndoneChangesEventsAreNotWritten(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), work("test", "lead", "build")))
	waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	ctx := context.Background()
	if err := e.PauseRun(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	l.failOn, l.failOnce = "run.paused[paused=false]", true
	l.mu.Unlock()
	if err := e.PauseRun(ctx, run.ID, false); err == nil {
		t.Fatal("an unpause the ledger refused was taken")
	}
	stopRun(t, e, run.ID)
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ev := range l.events {
		if p, ok := ev.payload.(map[string]any); ok && ev.runID == run.ID && ev.kind == "run.paused" && p["paused"] == false {
			t.Fatalf("the undone unpause's event was written: %+v", ev)
		}
	}
}

// An event the ledger would refuse for its size becomes a note of it, so
// it cannot fail every commit after it; a path list is bounded by bytes.
func TestAnOversizedEventBecomesANote(t *testing.T) {
	big := strings.Repeat("<", ledger.MaxPayload)
	out := writable([]event{{"check.finished", map[string]any{"task": "build", "attempt": 2, "check": "ok", "untracked": []string{big}}}, {"small", map[string]any{"task": "x"}}})
	note := out[0].payload
	if out[0].kind != "check.finished" || note["oversized"] == nil || note["task"] != "build" || note["attempt"] != 2 || note["check"] != "ok" || note["untracked"] != nil {
		t.Fatalf("the oversized event: %+v", out[0])
	}
	if out[1].payload["task"] != "x" {
		t.Fatalf("the small event: %+v", out[1])
	}
	paths := make([]string, 50)
	for i := range paths {
		paths[i] = fmt.Sprintf("%d/%s", i, strings.Repeat("p", 1450))
	}
	got := bounded(paths)
	size := 0
	for _, p := range got[:len(got)-1] {
		size += len(p)
	}
	if size > maxListedBytes || !strings.HasPrefix(got[len(got)-1], "… and ") || !strings.HasSuffix(got[len(got)-1], " more") {
		t.Fatalf("bounded: %d paths, %d bytes, last %q", len(got), size, got[len(got)-1])
	}
	if b, _ := json.Marshal(map[string]any{"changed": got, "untracked": got}); len(b) > ledger.MaxPayload {
		t.Fatalf("two bounded lists are %d bytes", len(b))
	}
}

// A check that leaves fifty untracked paths of 1,453 bytes, on the real
// ledger: its check.finished commits (the lists cut short), and the task
// succeeds; before, the event was refused and so was the result.
func TestACheckThatLeavesManyLongPathsIsRecorded(t *testing.T) {
	dir := newRepo(t)
	e, ld := sqlEngine(t)
	seg := strings.Repeat("d", 200)
	long := filepath.Join(seg, seg, seg, seg, seg, seg, strings.Repeat("f", 200)) // 1,406 bytes, and the number
	build := work("build", "lead")
	build.Checks = []Check{{ID: "ok", Argv: []string{"sh", "-c", `for i in $(seq 10 59); do mkdir -p "$(dirname "$i$1")" && : > "$i$1"; done`, "sh", "/" + long}}}
	run := launchManaged(t, e, repoCrew(dir, build))
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, a, "built.txt", "b\n", "built")
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	if !slices.Contains(sqlKinds(t, ld, run.ID), "check.finished") {
		t.Fatalf("events: %v", sqlKinds(t, ld, run.ID))
	}
	stopRun(t, e, run.ID)
}

// What an agent sends is measured as the ledger keeps it: a review's
// findings of 1,000 "<" each pass their character bounds and are six
// times that as JSON; the result is refused at submission with nothing
// changed, a smaller one is taken, and the run's commits go on after.
func TestAResultTooLargeAsTheLedgerKeepsItIsRefusedAtSubmission(t *testing.T) {
	dir := newRepo(t)
	e, ld := sqlEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), review("check", "critic", "build")))
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, b, "built.txt", "b\n", "built")
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	c := waitAttempt(t, e, run.ID, "check", ledger.AttemptRunning)
	var findings []Finding
	for range 20 {
		findings = append(findings, Finding{Severity: "major", Message: strings.Repeat("<", maxFindingText)})
	}
	ctx := context.Background()
	if _, err := e.SubmitResult(ctx, c.Attempt.SessionID, Result{Verdict: VerdictNeedsChanges, Findings: findings}); !errors.Is(err, ErrBadResult) || !strings.Contains(err.Error(), "as the ledger keeps them") {
		t.Fatalf("the oversized review: %v", err)
	}
	if ts := taskOf(t, e, run.ID, "check"); ts.Attempt == nil || ts.Attempt.State != ledger.AttemptRunning {
		t.Fatalf("the review after the refusal: %+v", ts)
	}
	// Fifty findings of 1,000 "<" with 1,024-byte paths: more than the
	// attempt's row keeps, whatever the repair.
	var fifty []Finding
	for range maxFindings {
		fifty = append(fifty, Finding{Severity: "nit", Path: strings.Repeat("<", maxFindingPath), Message: strings.Repeat("<", maxFindingText)})
	}
	if _, err := e.SubmitResult(ctx, c.Attempt.SessionID, Result{Verdict: VerdictApproved, Findings: fifty}); !errors.Is(err, ErrBadResult) || !strings.Contains(err.Error(), "as the ledger keeps it") {
		t.Fatalf("the oversized approval: %v", err)
	}
	submit(t, e, c.Attempt.SessionID, Result{Verdict: VerdictNeedsChanges, Findings: findings[:2]})
	waitTaskWhere(t, e, run.ID, "build", "reopened", func(ts TaskState) bool { return ts.Attempts == 2 })
	if err := e.PauseRun(ctx, run.ID, true); err != nil {
		t.Fatalf("a pause after: %v", err)
	}
	if !slices.Contains(sqlKinds(t, ld, run.ID), "task.reopened") {
		t.Fatalf("events: %v", sqlKinds(t, ld, run.ID))
	}
	stopRun(t, e, run.ID)
}

// The task file is cut to fit: with every part at its largest, task.md
// and task.json keep within maxTaskFile, everything cut is in
// task-more.json in full, and the crew's own text is never cut.
func TestTheTaskFileIsCutToFit(t *testing.T) {
	lt := strings.Repeat("<", 1)
	var findings []Finding
	for i := range maxFindings {
		findings = append(findings, Finding{Severity: "major", Path: fmt.Sprintf("p%d/%s", i, strings.Repeat("x", 1000)), Message: strings.Repeat(lt, maxFindingText)})
	}
	var inputs []TaskInput
	for i := range 8 {
		inputs = append(inputs, TaskInput{Task: fmt.Sprintf("in%d", i), Attempt: 1, Commit: strings.Repeat("a", 40), Summary: strings.Repeat("s", maxSummary)})
	}
	f := TaskFile{Task: "build", Attempt: 2, MaxAttempts: 3, Member: "lead", Kind: TaskWork,
		Goal: strings.Repeat("<", maxGoal), Role: strings.Repeat("<", maxPrompt), Instructions: strings.Repeat("<", maxInstructions),
		Start: strings.Repeat("b", 40), Inputs: inputs,
		Repair:     &TaskRepair{Review: "check", Attempt: 1, Commit: strings.Repeat("c", 40), Summary: strings.Repeat("r", maxSummary), Findings: findings},
		Checkpoint: &Checkpoint{Done: strings.Repeat("d", maxCheckpointField), Remaining: strings.Repeat("m", maxCheckpointField), Commit: strings.Repeat("e", 40)}}
	md, js, more, err := fitTaskFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(md) > maxTaskFile || len(js) > maxTaskFile || more == nil {
		t.Fatalf("task.md %d, task.json %d bytes, task-more.json %v", len(md), len(js), more != nil)
	}
	var kept TaskFile
	var cut TaskMore
	if err := json.Unmarshal(js, &kept); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(more, &cut); err != nil {
		t.Fatal(err)
	}
	if kept.More != taskMoreFile || len(kept.Cut) == 0 || !strings.Contains(string(md), "## Cut to fit") || !strings.Contains(string(md), taskMoreFile) {
		t.Fatalf("what task.json and task.md say of the cut: %q %v", kept.More, kept.Cut)
	}
	if kept.Goal != f.Goal || kept.Role != f.Role || kept.Instructions != f.Instructions || !strings.Contains(string(md), f.Instructions) {
		t.Fatal("the crew's own text was cut")
	}
	keptFindings := 0
	if kept.Repair != nil {
		keptFindings = len(kept.Repair.Findings)
	}
	if keptFindings+len(cut.Findings) != maxFindings || cut.Checkpoint == nil || len(cut.Inputs) != len(inputs) {
		t.Fatalf("findings %d kept and %d cut; checkpoint cut %v; summaries cut %d", keptFindings, len(cut.Findings), cut.Checkpoint != nil, len(cut.Inputs))
	}
	if strings.Contains(string(js), "\\"+"u003c") {
		t.Fatal("task.json escapes <")
	}
	// A task file that fits is written whole, with no task-more.json.
	small := TaskFile{Task: "build", Attempt: 1, MaxAttempts: 3, Member: "lead", Kind: TaskWork, Instructions: "Do it.", Inputs: inputs[:1]}
	if _, js, more, err := fitTaskFile(small); err != nil || more != nil || strings.Contains(string(js), `"more"`) {
		t.Fatalf("a small task file: more %v, %v", more != nil, err)
	}
}

// A dispatch whose repair would pass the task file's bound writes the cut
// files and launches: no attempt fails at "task file" for its size.
func TestARepairPastTheTaskFilesBoundStillLaunches(t *testing.T) {
	dir := newRepo(t)
	e, _, _ := managedEngine(t)
	// Four bytes a character, which JSON keeps as they are: a repair the
	// ledger keeps (about 40 KB) beside 32 KB of instructions.
	clef := "\U0001D11E"
	build := work("build", "lead")
	build.Instructions = strings.Repeat(clef, maxInstructions)
	run := launchManaged(t, e, repoCrew(dir, build, review("check", "critic", "build")))
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, b, "built.txt", "b\n", "built")
	c := waitAttempt(t, e, run.ID, "check", ledger.AttemptRunning)
	var findings []Finding
	for i := range 10 {
		findings = append(findings, Finding{Severity: "major", Path: fmt.Sprintf("f%d.go", i), Message: strings.Repeat(clef, maxFindingText)})
	}
	submit(t, e, c.Attempt.SessionID, Result{Verdict: VerdictNeedsChanges, Findings: findings})
	b2 := waitTaskWhere(t, e, run.ID, "build", "attempt 2 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	for _, name := range []string{"task.md", "task.json", taskMoreFile} {
		fi, err := os.Stat(filepath.Join(b2.Attempt.Worktree, taskDir, name))
		if err != nil || (name != taskMoreFile && fi.Size() > maxTaskFile) {
			t.Fatalf("%s: %v %v", name, fi, err)
		}
	}
	stopRun(t, e, run.ID)
}

// A command's receipt keeps what idempotency needs, not the whole task: an
// Accept anyway of a result whose issues and summary are near their
// bounds, beside a checkpoint near its own, settles on the real ledger,
// and the same command again answers with the same receipt.
func TestAReceiptFitsWhateverTheTaskHolds(t *testing.T) {
	dir := newRepo(t)
	e, _ := sqlEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{{ID: "never", Argv: []string{"false"}}}
	run := launchManaged(t, e, repoCrew(dir, build))
	ctx := context.Background()
	var issues []string
	for range maxIssues {
		issues = append(issues, strings.Repeat("<", maxIssue))
	}
	big := Result{Summary: strings.Repeat("<", maxSummary), Issues: issues}
	for n := 1; n <= maxAttemptsPerTask; n++ {
		a := waitTaskWhere(t, e, run.ID, "build", "running", func(ts TaskState) bool {
			return ts.Attempts == n && ts.Attempt != nil && ts.Attempt.State == ledger.AttemptRunning
		})
		writeFile(t, filepath.Join(a.Attempt.Worktree, "built.txt"), strings.Repeat("try\n", n))
		commitAll(t, a.Attempt.Worktree, "try")
		if n == maxAttemptsPerTask {
			if _, err := e.SaveCheckpoint(ctx, a.Attempt.SessionID, strings.Repeat("<", 1000), strings.Repeat("<", 1000), ""); err != nil {
				t.Fatal(err)
			}
		}
		submit(t, e, a.Attempt.SessionID, big)
	}
	waitTask(t, e, run.ID, "build", ledger.TaskFailed, 3)
	rc, err := e.Accept(ctx, run.ID, "admin", Command{ID: "big"}, "build")
	if err != nil || rc.Status != ledger.CommandDone || rc.Task == nil || rc.Task.State != ledger.TaskSucceeded || rc.Task.Result == nil || rc.Task.Result.Attempt != 3 {
		t.Fatalf("accept: %+v %v", rc, err)
	}
	if b, _ := json.Marshal(rc); len(b) > 4<<10 {
		t.Fatalf("the receipt is %d bytes", len(b))
	}
	if again, err := e.Accept(ctx, run.ID, "admin", Command{ID: "big"}, "build"); err != nil || !again.Repeat || again.Task == nil || again.Task.Result == nil || again.Task.Result.Commit != rc.Task.Result.Commit {
		t.Fatalf("the same command again: %+v %v", again, err)
	}
	stopRun(t, e, run.ID)
}
