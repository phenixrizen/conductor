package crew

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
)

// A result that makes the crew's setup fail is refused before its checks;
// the next attempt starts where the refused one did, since the setup runs
// in its worktree before the launch and would fail it at once, and its
// task file names the refused result's branch.
func TestAResultThatBreaksTheSetupHandsTheNextAttemptWhereItStarted(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{{ID: "ok", Argv: []string{"true"}}}
	c := repoCrew(dir, build)
	c.Setup = []Check{{ID: "deps", Argv: []string{"sh", "-c", "test ! -e broken.txt"}}}
	run := launchManaged(t, e, c)
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	writeFile(t, filepath.Join(a.Attempt.Worktree, "broken.txt"), "x\n")
	commitAll(t, a.Attempt.Worktree, "breaks the setup")
	submit(t, e, a.Attempt.SessionID, Result{Summary: "broken"})
	a2 := waitTaskWhere(t, e, run.ID, "build", "attempt 2 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	if row := l.attemptRow(t, run.ID, "build", 1); row.State != ledger.AttemptFailed || row.Stage != "setup deps" {
		t.Fatalf("attempt 1: %+v", row)
	}
	if a2.Attempt.Start != a.Attempt.Start {
		t.Fatalf("attempt 2 starts at %s, not where attempt 1 did (%s)", short(a2.Attempt.Start), short(a.Attempt.Start))
	}
	md, f := taskFileOf(t, a2)
	if f.StartFrom != "where attempt 1 started: its result was refused at setup deps" || f.Previous == nil || f.Previous.Branch != a.Attempt.Branch || !strings.Contains(md, "on the branch "+a.Attempt.Branch+", is not where you start") {
		t.Fatalf("attempt 2's task file: starts from %q, the attempt before %+v\n%s", f.StartFrom, f.Previous, md)
	}
	if l.count(run.ID, "check.started") != 0 {
		t.Fatal("a check ran after the setup failed")
	}
	stopRun(t, e, run.ID)
}

// A candidate a continuation verifies again and refuses before its checks
// hands the next attempt the start of the work, not the candidate.
func TestAContinuedCandidateRefusedBeforeItsChecksHandsTheNextAttemptTheWorksStart(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	mark := filepath.Join(t.TempDir(), "refuse")
	build := work("build", "lead")
	build.Checks = []Check{{ID: "ok", Argv: []string{"true"}}}
	c := repoCrew(dir, build)
	// At the base the setup passes. At the result it takes long until the
	// mark exists (the server ends during it), then fails.
	c.Setup = []Check{{ID: "deps", Argv: []string{"sh", "-c", "test -e broken.txt || exit 0; test -e " + mark + " && exit 1; sleep 30"}}}
	run := launchManaged(t, e, c)
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	writeFile(t, filepath.Join(a.Attempt.Worktree, "broken.txt"), "x\n")
	commitAll(t, a.Attempt.Worktree, "breaks the setup")
	submit(t, e, a.Attempt.SessionID, Result{Summary: "broken"})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if row := l.attemptRow(t, run.ID, "build", 1); row.State == ledger.AttemptVerifying && row.Result != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the result was not recorded as verifying")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The first engine is stopped before the ledger is rewritten and the
	// mark made: a verifier still going would record its own outcome over
	// the interrupted row.
	stopRun(t, e, run.ID)
	interrupt(l, run.ID, map[string][2]string{
		"build/1": {ledger.AttemptInterrupted, "verifying: the owner stopped; the candidate is verified again"},
	})
	if err := os.WriteFile(mark, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e2, _ := readyEngine(t)
	e2.UseLedger(l)
	next, err := e2.ContinueRun(ctx, run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	a3 := waitTaskWhere(t, e2, next.ID, "build", "attempt 3 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 3 && ts.Attempt.State == ledger.AttemptRunning
	})
	if row := l.attemptRow(t, next.ID, "build", 2); row.State != ledger.AttemptFailed || row.Stage != "setup deps" || row.StartCommit != a.Attempt.Start {
		t.Fatalf("attempt 2, the candidate verified again: %+v", row)
	}
	if a3.Attempt.Start != a.Attempt.Start {
		t.Fatalf("attempt 3 starts at %s, not where the work did (%s)", short(a3.Attempt.Start), short(a.Attempt.Start))
	}
	stopRun(t, e2, next.ID)
}

// A result that makes the crew's directory a file is refused at checkout:
// no check can run in it and no next attempt can be made through it, so
// the next attempt starts where the refused one did.
func TestAResultThatMakesTheCrewsDirectoryAFileIsRefusedAtCheckout(t *testing.T) {
	dir := newRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "web", "app.txt"), "app\n")
	commitAll(t, dir, "web")
	e, _, l := managedEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{{ID: "ok", Argv: []string{"true"}}}
	c := repoCrew(dir, build)
	c.Cwd = filepath.Join(dir, "web")
	run := launchManaged(t, e, c)
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	if err := os.RemoveAll(filepath.Join(a.Attempt.Worktree, "web")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(a.Attempt.Worktree, "web"), "a file\n")
	commitAll(t, a.Attempt.Worktree, "web is a file now")
	submit(t, e, a.Attempt.SessionID, Result{Summary: "a file"})
	a2 := waitTaskWhere(t, e, run.ID, "build", "attempt 2 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	if row := l.attemptRow(t, run.ID, "build", 1); row.State != ledger.AttemptFailed || row.Stage != "checkout" || !strings.Contains(row.Error, "is a file, not a directory") {
		t.Fatalf("attempt 1: %+v", row)
	}
	if a2.Attempt.Start != a.Attempt.Start {
		t.Fatalf("attempt 2 starts at %s, not where attempt 1 did (%s)", short(a2.Attempt.Start), short(a.Attempt.Start))
	}
	if l.count(run.ID, "check.started") != 0 {
		t.Fatal("a check ran in a file")
	}
	stopRun(t, e, run.ID)
}

// A work task with no checks is refused at checkout too when its result
// makes the crew's directory a file or a link: a dependent made at that
// result would fail at its worktree, attempt after attempt, before its
// agent could act. The next attempt starts where the refused one did, and
// the dependent waits for a result it can start in.
func TestAResultWithNoChecksThatBreaksTheCrewsDirectoryIsRefused(t *testing.T) {
	for _, how := range []string{"file", "link"} {
		t.Run(how, func(t *testing.T) {
			dir := newRepo(t)
			if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, "web", "app.txt"), "app\n")
			commitAll(t, dir, "web")
			e, _, l := managedEngine(t)
			c := repoCrew(dir, work("build", "lead"), work("docs", "writer", "build"))
			c.Cwd = filepath.Join(dir, "web")
			run := launchManaged(t, e, c)
			a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
			web := filepath.Join(a.Attempt.Worktree, "web")
			if err := os.RemoveAll(web); err != nil {
				t.Fatal(err)
			}
			if how == "file" {
				writeFile(t, web, "a file\n")
			} else if err := os.Symlink("..", web); err != nil {
				t.Fatal(err)
			}
			commitAll(t, a.Attempt.Worktree, "web is a "+how+" now")
			submit(t, e, a.Attempt.SessionID, Result{Summary: "broken"})
			a2 := waitTaskWhere(t, e, run.ID, "build", "attempt 2 running", func(ts TaskState) bool {
				return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
			})
			if row := l.attemptRow(t, run.ID, "build", 1); row.State != ledger.AttemptFailed || row.Stage != "checkout" || !strings.Contains(row.Error, "in the member's worktree is a") {
				t.Fatalf("attempt 1: %+v", row)
			}
			if a2.Attempt.Start != a.Attempt.Start {
				t.Fatalf("attempt 2 starts at %s, not where attempt 1 did (%s)", short(a2.Attempt.Start), short(a.Attempt.Start))
			}
			if d := taskOf(t, e, run.ID, "docs"); d.State != ledger.TaskBlocked || d.Attempts != 0 {
				t.Fatalf("docs: %+v", d)
			}
			stopRun(t, e, run.ID)
		})
	}
}

// A work task with no checks still has its result set up when the crew has
// a setup: a result the setup refuses is refused, the next attempt starts
// where the refused one did, and the dependent is not made at it (its
// setup would fail it, attempt after attempt, before its agent could act).
func TestAResultWithNoChecksThatBreaksTheSetupIsRefused(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	c := repoCrew(dir, work("build", "lead"), work("docs", "writer", "build"))
	c.Setup = []Check{{ID: "deps", Argv: []string{"sh", "-c", "test ! -e broken.txt"}}}
	run := launchManaged(t, e, c)
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	writeFile(t, filepath.Join(a.Attempt.Worktree, "broken.txt"), "x\n")
	commitAll(t, a.Attempt.Worktree, "breaks the setup")
	submit(t, e, a.Attempt.SessionID, Result{Summary: "broken"})
	a2 := waitTaskWhere(t, e, run.ID, "build", "attempt 2 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	if row := l.attemptRow(t, run.ID, "build", 1); row.State != ledger.AttemptFailed || row.Stage != "setup deps" {
		t.Fatalf("attempt 1: %+v", row)
	}
	if a2.Attempt.Start != a.Attempt.Start {
		t.Fatalf("attempt 2 starts at %s, not where attempt 1 did (%s)", short(a2.Attempt.Start), short(a.Attempt.Start))
	}
	if d := taskOf(t, e, run.ID, "docs"); d.State != ledger.TaskBlocked || d.Attempts != 0 {
		t.Fatalf("docs: %+v", d)
	}
	stopRun(t, e, run.ID)
}

// The crew's directory is looked up in a tree as a literal path: ":web" is
// not git's pathspec magic for "web".
func TestTheCrewsDirectoryIsLookedUpLiterally(t *testing.T) {
	t.Run("a result that makes it a file", func(t *testing.T) {
		dir := newRepo(t)
		if err := os.MkdirAll(filepath.Join(dir, ":web"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, ":web", "app.txt"), "app\n")
		commitAll(t, dir, ":web")
		e, _, l := managedEngine(t)
		c := repoCrew(dir, work("build", "lead"))
		c.Cwd = filepath.Join(dir, ":web")
		run := launchManaged(t, e, c)
		a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
		web := filepath.Join(a.Attempt.Worktree, ":web")
		if err := os.RemoveAll(web); err != nil {
			t.Fatal(err)
		}
		writeFile(t, web, "a file\n")
		commitAll(t, a.Attempt.Worktree, ":web is a file now")
		submit(t, e, a.Attempt.SessionID, Result{Summary: "a file"})
		waitTaskWhere(t, e, run.ID, "build", "attempt 2 running", func(ts TaskState) bool {
			return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
		})
		if row := l.attemptRow(t, run.ID, "build", 1); row.State != ledger.AttemptFailed || row.Stage != "checkout" || !strings.Contains(row.Error, `":web" in the member's worktree is a file`) {
			t.Fatalf("attempt 1: %+v", row)
		}
		stopRun(t, e, run.ID)
	})
	t.Run("a commit that tracks its .conductor", func(t *testing.T) {
		dir := newRepo(t)
		if err := os.MkdirAll(filepath.Join(dir, ":web", taskDir), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, ":web", taskDir, "task.md"), "not Conductor's\n")
		commitAll(t, dir, ":web/.conductor")
		e, _, l := managedEngine(t)
		c := repoCrew(dir, work("build", "lead"))
		c.Cwd = filepath.Join(dir, ":web")
		run := launchManaged(t, e, c)
		// The base itself is refused: every attempt fails at its worktree,
		// and the task with them.
		waitTaskWhere(t, e, run.ID, "build", "failed", func(ts TaskState) bool { return ts.State == ledger.TaskFailed })
		if row := l.attemptRow(t, run.ID, "build", 1); row.Stage != "worktree" || !strings.Contains(row.Error, "tracks .conductor/") {
			t.Fatalf("attempt 1: %+v", row)
		}
		stopRun(t, e, run.ID)
	})
}

// A result that leaves no tracked file in the crew's directory is still a
// tree to start in: the checks' checkout makes the directory, as an
// attempt's worktree does, and the setup and the checks run in it.
func TestAResultThatEmptiesTheCrewsDirectorySucceeds(t *testing.T) {
	for _, with := range []string{"setup", "check"} {
		t.Run(with, func(t *testing.T) {
			dir := newRepo(t)
			if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, "web", "app.txt"), "app\n")
			commitAll(t, dir, "web")
			e, _, _ := managedEngine(t)
			build := work("build", "lead")
			c := repoCrew(dir, build)
			if with == "setup" {
				c.Setup = []Check{{ID: "deps", Argv: []string{"true"}}}
			} else {
				c.Tasks[0].Checks = []Check{{ID: "ok", Argv: []string{"true"}}}
			}
			c.Cwd = filepath.Join(dir, "web")
			run := launchManaged(t, e, c)
			a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
			if err := os.Remove(filepath.Join(a.Attempt.Worktree, "web", "app.txt")); err != nil {
				t.Fatal(err)
			}
			commitAll(t, a.Attempt.Worktree, "web has no tracked file now")
			submit(t, e, a.Attempt.SessionID, Result{Summary: "emptied"})
			waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
			stopRun(t, e, run.ID)
		})
	}
}

// On a filesystem that does not tell case apart (macOS by default) a
// result that replaces web/ with a file WEB leaves "web" a file there: the
// checks' checkout, on this machine's filesystem, refuses it, though git's
// tree has no "web".
func TestAResultThatAliasesTheCrewsDirectoryIsRefusedWhereCaseFolds(t *testing.T) {
	dir := newRepo(t)
	probe := filepath.Join(dir, "case-probe")
	writeFile(t, probe, "x\n")
	_, err := os.Stat(filepath.Join(dir, "CASE-PROBE"))
	if rmErr := os.Remove(probe); rmErr != nil {
		t.Fatal(rmErr)
	}
	if err != nil {
		t.Skip("this filesystem tells case apart")
	}
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "web", "app.txt"), "app\n")
	commitAll(t, dir, "web")
	e, _, l := managedEngine(t)
	c := repoCrew(dir, work("build", "lead"), work("docs", "writer", "build"))
	c.Cwd = filepath.Join(dir, "web")
	run := launchManaged(t, e, c)
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	if err := os.RemoveAll(filepath.Join(a.Attempt.Worktree, "web")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(a.Attempt.Worktree, "WEB"), "a file\n")
	commitAll(t, a.Attempt.Worktree, "web is a file WEB now")
	submit(t, e, a.Attempt.SessionID, Result{Summary: "aliased"})
	waitTaskWhere(t, e, run.ID, "build", "attempt 2 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	if row := l.attemptRow(t, run.ID, "build", 1); row.State != ledger.AttemptFailed || row.Stage != "checkout" {
		t.Fatalf("attempt 1: %+v", row)
	}
	if d := taskOf(t, e, run.ID, "docs"); d.State != ledger.TaskBlocked || d.Attempts != 0 {
		t.Fatalf("docs: %+v", d)
	}
	stopRun(t, e, run.ID)
}

// A continuation keeps a refusal: the last attempt saved a checkpoint at
// the result the setup then refused, the run was paused and stopped, and
// the continuation's first attempt starts where the refused one did, not
// at that checkpoint, with the refused result named in its task file.
func TestAContinuationAfterARefusedResultStartsWhereItStarted(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	c := repoCrew(dir, work("build", "lead"))
	c.Setup = []Check{{ID: "deps", Argv: []string{"sh", "-c", "test ! -e broken.txt"}}}
	run := launchManaged(t, e, c)
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	ctx := context.Background()
	if err := e.PauseRun(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(a.Attempt.Worktree, "broken.txt"), "x\n")
	commitAll(t, a.Attempt.Worktree, "breaks the setup")
	if _, err := e.SaveCheckpoint(ctx, a.Attempt.SessionID, "broke it", "nothing", ""); err != nil {
		t.Fatal(err)
	}
	submit(t, e, a.Attempt.SessionID, Result{Summary: "broken"})
	// Paused: the refusal starts no attempt, so the failed attempt holds.
	waitTaskWhere(t, e, run.ID, "build", "attempt 1 failed", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 1 && ts.Attempt.State == ledger.AttemptFailed
	})
	if row := l.attemptRow(t, run.ID, "build", 1); row.Stage != "setup deps" || row.Checkpoint == "" {
		t.Fatalf("attempt 1: %+v", row)
	}
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
	a2 := waitTaskWhere(t, e2, next.ID, "build", "attempt 2 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	if a2.Attempt.Start != a.Attempt.Start {
		t.Fatalf("attempt 2 starts at %s, not where attempt 1 did (%s)", short(a2.Attempt.Start), short(a.Attempt.Start))
	}
	_, f := taskFileOf(t, a2)
	if f.StartFrom != "where attempt 1 started: its result was refused at setup deps" || f.Previous == nil || f.Previous.Branch != a.Attempt.Branch || f.Previous.Result == "" {
		t.Fatalf("attempt 2's task file: starts from %q, the attempt before %+v", f.StartFrom, f.Previous)
	}
	stopRun(t, e2, next.ID)
}

// A refusal outlives a continuation that made no attempt: attempt 1's
// result fails a check, attempt 2 starts there and its result is refused
// by the setup while the run is paused; the run is continued, stopped
// with no attempt, and continued again, and attempt 3 starts where
// attempt 2 did, attempt 1's result, not the run's base.
func TestARefusalOutlivesAContinuationThatMadeNoAttempt(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{{ID: "ok", Argv: []string{"sh", "-c", "test -e ok.txt"}}}
	c := repoCrew(dir, build)
	c.Setup = []Check{{ID: "deps", Argv: []string{"sh", "-c", "test ! -e broken.txt"}}}
	run := launchManaged(t, e, c)
	a1 := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, a1, "half.txt", "half\n", "half")
	a2 := waitTaskWhere(t, e, run.ID, "build", "attempt 2 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptRunning
	})
	p := a2.Attempt.Start
	if row := l.attemptRow(t, run.ID, "build", 1); p != row.ResultCommit || p == a1.Attempt.Start {
		t.Fatalf("attempt 2 starts at %s, attempt 1's result is %s", short(p), short(row.ResultCommit))
	}
	ctx := context.Background()
	if err := e.PauseRun(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(a2.Attempt.Worktree, "broken.txt"), "x\n")
	commitAll(t, a2.Attempt.Worktree, "breaks the setup")
	submit(t, e, a2.Attempt.SessionID, Result{Summary: "broken"})
	waitTaskWhere(t, e, run.ID, "build", "attempt 2 failed", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 2 && ts.Attempt.State == ledger.AttemptFailed
	})
	stopRun(t, e, run.ID)
	// Continued paused, and stopped with no attempt made.
	e2, _ := readyEngine(t)
	e2.UseLedger(l)
	mid, err := e2.ContinueRun(ctx, run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ts := taskOf(t, e2, mid.ID, "build"); ts.Attempt != nil {
		t.Fatalf("the paused continuation made an attempt: %+v", ts.Attempt)
	}
	stopRun(t, e2, mid.ID)
	e3, _ := readyEngine(t)
	e3.UseLedger(l)
	next, err := e3.ContinueRun(ctx, mid.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e3.PauseRun(ctx, next.ID, false); err != nil {
		t.Fatal(err)
	}
	a3 := waitTaskWhere(t, e3, next.ID, "build", "attempt 3 running", func(ts TaskState) bool {
		return ts.Attempt != nil && ts.Attempt.N == 3 && ts.Attempt.State == ledger.AttemptRunning
	})
	if a3.Attempt.Start != p {
		t.Fatalf("attempt 3 starts at %s, not where attempt 2 did (%s)", short(a3.Attempt.Start), short(p))
	}
	if _, f := taskFileOf(t, a3); f.StartFrom != "where attempt 2 started: its result was refused at setup deps" || f.Previous == nil || f.Previous.Branch != a2.Attempt.Branch {
		t.Fatalf("attempt 3's task file: starts from %q, the attempt before %+v", f.StartFrom, f.Previous)
	}
	stopRun(t, e3, next.ID)
}
