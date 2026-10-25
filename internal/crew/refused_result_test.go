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
	interrupt(l, run.ID, map[string][2]string{
		"build/1": {ledger.AttemptInterrupted, "verifying: the owner stopped; the candidate is verified again"},
	})
	if err := os.WriteFile(mark, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e2, fl2 := newEngine(t)
	fl2.onLaunch = printOnce
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
	stopRun(t, e, run.ID)
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
			if row := l.attemptRow(t, run.ID, "build", 1); row.State != ledger.AttemptFailed || row.Stage != "checkout" || !strings.Contains(row.Error, "in the result is a") {
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
		if row := l.attemptRow(t, run.ID, "build", 1); row.State != ledger.AttemptFailed || row.Stage != "checkout" || !strings.Contains(row.Error, `":web" in the result is a file`) {
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
