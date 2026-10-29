package crew

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/crew/ledger"
)

// stepRows is the attempt's receipts as the fake ledger last has them,
// one per id and k, in the order each was first put.
func (l *fakeLedger) stepRows(runID, taskID string, n int) []ledger.CheckRow {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []ledger.CheckRow
	for _, c := range l.checks {
		if c.RunID != runID || c.TaskID != taskID || c.N != n {
			continue
		}
		if i := slices.IndexFunc(out, func(o ledger.CheckRow) bool { return o.ID == c.ID && o.K == c.K }); i >= 0 {
			out[i] = c
			continue
		}
		out = append(out, c)
	}
	return out
}

// prereqRows is the run's prerequisites' receipts as the fake ledger last
// has them, one per id and k, in the order each was first put.
func (l *fakeLedger) prereqRows(runID string) []ledger.PrerequisiteRow {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []ledger.PrerequisiteRow
	for _, c := range l.prerequisites {
		if c.RunID != runID {
			continue
		}
		if i := slices.IndexFunc(out, func(o ledger.PrerequisiteRow) bool { return o.ID == c.ID && o.K == c.K }); i >= 0 {
			out[i] = c
			continue
		}
		out = append(out, c)
	}
	return out
}

// treeRow is a tree's row as the fake ledger last has it.
func (l *fakeLedger) treeRow(runID, taskID string, n int, kind string) (ledger.TreeRow, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	row, ok := l.trees[fmt.Sprintf("%s/%s/%d/%s", runID, taskID, n, kind)]
	return row, ok
}

// payloads is the payloads of the run's events of the kind, in order.
func (l *fakeLedger) payloads(runID, kind string) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, ev := range l.events {
		if ev.runID == runID && ev.kind == kind {
			p, _ := ev.payload.(map[string]any)
			out = append(out, p)
		}
	}
	return out
}

// waitFake waits up to 10 s for ok, holding nothing, and fails with what.
func waitFake(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("not %s after 10 s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// rowWords is a receipt in words, for a failure's message.
func rowWords(rows []ledger.CheckRow) string {
	var out []string
	for _, c := range rows {
		out = append(out, fmt.Sprintf("%s/%s k%d %s exit %d dir %s manifest %q error %q", c.Phase, c.ID, c.K, c.State, c.Exit, c.Dir, c.Manifest, c.Error))
	}
	return strings.Join(out, "\n")
}

// repoManifestCrew is a managed crew in dir with the manifest m, its
// tasks given.
func repoManifestCrew(dir string, m Manifest, tasks ...Task) Crew {
	c := repoCrew(dir, tasks...)
	c.Manifest = &m
	return c
}

// shStep is a step running a line of sh.
func shStep(id, line string) Check {
	return Check{ID: id, Argv: []string{"sh", "-c", line}}
}

// gated is a line of sh that waits for the file gate to exist.
func gated(gate string) string {
	return `while [ ! -e "` + gate + `" ]; do sleep 0.05; done`
}

// open makes the file at path.
func open(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, "open\n")
}

// sha is the hex SHA-256 of b.
func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Every step of a tree is a receipt (EN1 PR-2): a row of its phase with
// the manifest that ran it, its directory and commit, its exit and log
// and the log's hash, put started, running and finished; the worktree's
// and the checkout's are told apart by k and dir. The tree's row and its
// events say when it was resolved and when it ended, after the attempt's
// close; the task file names the manifest.
func TestEveryStepOfATreeIsAReceipt(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{shStep("unit", "test -f dep.txt")}
	c := repoManifestCrew(dir, Manifest{Setup: []Check{shStep("deps", "echo installing; echo dep > dep.txt"), shStep("seed", "echo seeded")}}, build)
	c.ManifestRevision = 3
	run := launchManaged(t, e, c)
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	rows := l.stepRows(run.ID, "build", 1)
	if len(rows) != 2 {
		t.Fatalf("the worktree's receipts:\n%s", rowWords(rows))
	}
	for i, want := range []struct{ id, log string }{{"deps", "installing\n"}, {"seed", "seeded\n"}} {
		r := rows[i]
		if r.ID != want.id || r.K != 1 || r.Phase != ledger.PhaseSetup || r.Manifest != "crew:3" || r.Dir != a.Attempt.Worktree || r.Subject != a.Attempt.Start ||
			r.State != ledger.CheckFinished || r.Exit != 0 || r.Error != "" || string(r.Log) != want.log || r.LogSHA256 != sha(r.Log) || r.Tries != 1 || r.PID == 0 {
			t.Fatalf("receipt %d: %+v", i, r)
		}
	}
	kinds := l.kinds(run.ID)
	order := []string{"attempt.reserved", "manifest.resolved", "attempt.workspace", "step.started", "step.running", "step.finished", "attempt.launched"}
	for i := 1; i < len(order); i++ {
		if slices.Index(kinds, order[i-1]) < 0 || slices.Index(kinds, order[i-1]) > slices.Index(kinds, order[i]) {
			t.Fatalf("%s before %s: %v", order[i-1], order[i], kinds)
		}
	}
	if p := l.payloads(run.ID, "manifest.resolved")[0]; p["tree"] != ledger.TreeWorktree || p["source"] != "crew" || p["revision"] != int64(3) {
		t.Fatalf("manifest.resolved: %v", p)
	}
	if tr, ok := l.treeRow(run.ID, "build", 1, ledger.TreeWorktree); !ok || tr.State != ledger.TreeResolved || tr.Dir != a.Attempt.Worktree || tr.Commit != a.Attempt.Start || tr.Manifest != "crew:3" {
		t.Fatalf("the worktree's row while it runs: %+v", tr)
	}
	md, f := taskFileOf(t, a)
	if f.Manifest == nil || f.Manifest.Source != "crew" || f.Manifest.Revision != 3 || !strings.Contains(md, "The crew's project manifest (revision 3) made the tree usable") {
		t.Fatalf("the task file's manifest: %+v\n%s", f.Manifest, md)
	}
	// The result: the checkout's setup, then the check, each the next k of
	// its id on the attempt; then both trees end, after the decision.
	commitResult(t, e, a, "built.txt", "yes\n", "built")
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	waitFake(t, "both trees ended", func() bool {
		w, _ := l.treeRow(run.ID, "build", 1, ledger.TreeWorktree)
		c, _ := l.treeRow(run.ID, "build", 1, ledger.TreeCheckout)
		return w.State == ledger.TreeEnded && c.State == ledger.TreeEnded
	})
	rows = l.stepRows(run.ID, "build", 1)
	if len(rows) != 5 || rows[2].ID != "deps" || rows[2].K != 2 || rows[2].Dir != a.Attempt.Worktree+".check" || rows[3].ID != "seed" || rows[3].K != 2 || rows[4].Phase != ledger.PhaseCheck || rows[4].ID != "unit" || rows[4].K != 1 || rows[4].Manifest != "" {
		t.Fatalf("the attempt's receipts:\n%s", rowWords(rows))
	}
	kinds = l.kinds(run.ID)
	succeeded := slices.Index(kinds, "task.succeeded")
	ended := 0
	for i, k := range kinds {
		if k == "tree.ended" {
			ended++
			if i < succeeded {
				t.Fatalf("a tree ended before the decision: %v", kinds)
			}
		}
	}
	if ended != 2 {
		t.Fatalf("%d tree.ended events: %v", ended, kinds)
	}
	if w, _ := l.treeRow(run.ID, "build", 1, ledger.TreeWorktree); w.Why != "attempt 1 succeeded" || w.EndedAt.IsZero() {
		t.Fatalf("the worktree's end: %+v", w)
	}
	if r, _ := e.Get(run.ID); r.Cleaning != 0 || r.Manifest == nil || r.Manifest.Source != "crew" || r.Manifest.Revision != 3 {
		t.Fatalf("the run: cleaning %d, manifest %+v", r.Cleaning, r.Manifest)
	}
	stopRun(t, e, run.ID)
}

// A crew without a project manifest makes no tree and keeps no step
// receipt: it runs as before manifests.
func TestARunWithoutAManifestMakesNoTree(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{shStep("unit", "true")}
	run := launchManaged(t, e, repoCrew(dir, build))
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, a, "built.txt", "yes\n", "built")
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	for _, k := range []string{"manifest.resolved", "tree.ended", "step.started", "prerequisites.passed"} {
		if n := l.count(run.ID, k); n != 0 {
			t.Fatalf("%d %s events in a run without a manifest", n, k)
		}
	}
	if rows := l.stepRows(run.ID, "build", 1); len(rows) != 1 || rows[0].Phase != ledger.PhaseCheck || rows[0].K != 1 {
		t.Fatalf("the receipts:\n%s", rowWords(rows))
	}
	if r, _ := e.Get(run.ID); r.Manifest != nil {
		t.Fatalf("a manifest on a run without one: %+v", r.Manifest)
	}
	stopRun(t, e, run.ID)
}

// failsInTheWorktree is a setup step that passes in a checkout and fails
// in a worktree while mark exists.
func failsInTheWorktree(mark string) Check {
	return shStep("deps", `case "$(pwd)" in *.check) exit 0;; esac; if [ -e "`+mark+`" ]; then echo the registry is down >&2; exit 1; fi`)
}

// A setup step that fails in an attempt's worktree is the host's or the
// manifest's, not the agent's: the attempt fails at setup deps, uncounted
// (the task ready with why, one more attempt allowed), the run's dispatch
// pauses with the same words, and nothing is dispatched until a person
// unpauses, which runs the prerequisites again (k 2). The next attempt is
// numbered after it and starts where it started: attempt 1's result B,
// refused at a check, attempt 2 at B failing at setup deps, attempt 3 at
// B. A continuation made while it is paused does the same.
func TestASetupStepThatFailsInTheWorktreeIsNotCounted(t *testing.T) {
	for _, continued := range []bool{false, true} {
		t.Run(map[bool]string{false: "in the run", true: "after a continuation"}[continued], func(t *testing.T) {
			dir := newRepo(t)
			e, _, l := managedEngine(t)
			mark := filepath.Join(t.TempDir(), "registry-down")
			build := work("build", "lead")
			build.Checks = []Check{shStep("ok", "test -e ok.txt")}
			c := repoManifestCrew(dir, Manifest{Prerequisites: []Check{{ID: "tools", Argv: []string{"true"}}}, Setup: []Check{failsInTheWorktree(mark)}}, build)
			run := launchManaged(t, e, c)
			a1 := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
			open(t, mark)
			commitResult(t, e, a1, "half.txt", "half\n", "half")
			attemptFailed(t, e, run.ID, "build", 2)
			row1, row2 := l.attemptRow(t, run.ID, "build", 1), l.attemptRow(t, run.ID, "build", 2)
			b := row1.ResultCommit
			if row1.Stage != "check ok" || b == "" || row2.StartCommit != b || row2.Stage != "setup deps" || !strings.Contains(row2.Error, "deps exited 1: the registry is down") || row2.ResultCommit != "" {
				t.Fatalf("attempt 1 %+v\nattempt 2 %+v", row1, row2)
			}
			why := "attempt 2 could not be made usable at setup deps: " + row2.Error
			ts := taskOf(t, e, run.ID, "build")
			if ts.State != ledger.TaskReady || ts.Reason != why || ts.Attempts != 2 {
				t.Fatalf("the task: %+v", ts)
			}
			l.mu.Lock()
			extra := l.tasks[run.ID+"/build"].Extra
			l.mu.Unlock()
			if extra != 1 {
				t.Fatalf("extra %d", extra)
			}
			failed := l.payloads(run.ID, "attempt.failed")
			if p := failed[len(failed)-1]; p["attempt"] != 2 || p["uncounted"] != true || p["stage"] != "setup deps" {
				t.Fatalf("attempt.failed: %v", p)
			}
			waitFake(t, "the pause recorded", func() bool {
				l.mu.Lock()
				defer l.mu.Unlock()
				return l.runs[run.ID].Paused && l.runs[run.ID].PausedWhy == why
			})
			if r, _ := e.Get(run.ID); !r.Paused || r.PausedWhy != why {
				t.Fatalf("the run: paused %v %q", r.Paused, r.PausedWhy)
			}
			// Pumped, the paused run dispatches nothing.
			e.mu.Lock()
			rr := e.runs[run.ID]
			e.mu.Unlock()
			e.pumpLocked(rr)
			if ts := taskOf(t, e, run.ID, "build"); ts.Attempts != 2 {
				t.Fatalf("an attempt dispatched while paused: %+v", ts)
			}
			if p := l.prereqRows(run.ID); len(p) != 1 || p[0].K != 1 || p[0].State != ledger.CheckFinished {
				t.Fatalf("the prerequisites before the pause: %+v", p)
			}
			runID, eng := run.ID, e
			if continued {
				stopRun(t, e, run.ID)
				e2, _ := readyEngine(t)
				e2.UseLedger(l)
				next, err := e2.ContinueRun(t.Context(), run.ID, "")
				if err != nil {
					t.Fatal(err)
				}
				if !next.Paused || next.PausedWhy != why {
					t.Fatalf("the continuation's pause: %v %q", next.Paused, next.PausedWhy)
				}
				// Its prerequisites run again, at its base: their generation
				// ends before the unpause, which then runs them a second time.
				waitFake(t, "the continuation's prerequisites passed", func() bool { return l.count(next.ID, "prerequisites.passed") == 1 })
				runID, eng = next.ID, e2
			}
			if err := os.Remove(mark); err != nil {
				t.Fatal(err)
			}
			pauseRun(t, eng, runID, false)
			a3 := attemptN(t, eng, runID, "build", 3)
			if a3.Attempt.Start != b {
				t.Fatalf("attempt 3 starts at %s, not at attempt 2's start %s", short(a3.Attempt.Start), short(b))
			}
			prereqs := l.prereqRows(runID)
			if want := map[bool]int{false: 2, true: 2}[continued]; len(prereqs) != want || prereqs[len(prereqs)-1].K != want {
				t.Fatalf("the prerequisites after the unpause: %+v", prereqs)
			}
			md, f := taskFileOf(t, a3)
			if f.Previous == nil || f.Previous.Attempt != 1 || f.Previous.Stage != "check ok" || f.Unusable == nil || f.Unusable.Attempt != 2 || f.Unusable.Stage != "setup deps" ||
				f.StartFrom != "attempt 1's result" || !strings.Contains(md, "Attempt 2 could not be made usable: it failed at setup deps") {
				t.Fatalf("attempt 3's task file: previous %+v unusable %+v from %q\n%s", f.Previous, f.Unusable, f.StartFrom, md)
			}
			stopRun(t, eng, runID)
		})
	}
}

// checkpointAndEnd commits broken.txt in the attempt's worktree, saves a
// checkpoint at that commit and ends the attempt's session: the attempt
// fails without a result, its checkpoint the commit C it returns.
func checkpointAndEnd(t *testing.T, e *Engine, fl *fakeLauncher, ts TaskState) string {
	t.Helper()
	writeFile(t, filepath.Join(ts.Attempt.Worktree, "broken.txt"), "x\n")
	commitAll(t, ts.Attempt.Worktree, "breaks the setup")
	cp, err := e.SaveCheckpoint(t.Context(), ts.Attempt.SessionID, "broke it", "fix it", "")
	if err != nil {
		t.Fatal(err)
	}
	_, p := fl.attemptSession(t, ts)
	p.End(0)
	return cp.Checkpoint.Commit
}

// A worktree made at a checkpoint the agent saved (a commit of its own
// that nothing verified) that cannot be made usable refuses the checkpoint
// as a refused result is refused: attempt 1 saves C and its session ends,
// attempt 2 at C fails at setup deps, uncounted, the run not paused, its
// attempt.failed naming C, and attempt 3 is dispatched at once at attempt
// 1's start, its task file naming the checkpoint as not usable and having
// no checkpoint section. A second failure there, at a commit the agent did
// not make, pauses as any other. A continuation made between attempts 2
// and 3 reads the refusal from the rows: its task's checkpoint is none.
func TestAWorktreeAtACheckpointThatCannotBeMadeUsableRefusesIt(t *testing.T) {
	t.Run("in the run", func(t *testing.T) {
		dir := newRepo(t)
		e, fl, l := managedEngine(t)
		c := repoManifestCrew(dir, Manifest{Setup: []Check{shStep("deps", "if [ -e broken.txt ]; then echo broken >&2; exit 1; fi")}}, work("build", "lead"))
		run := launchManaged(t, e, c)
		a1 := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
		cp := checkpointAndEnd(t, e, fl, a1)
		a3 := attemptN(t, e, run.ID, "build", 3)
		row2 := l.attemptRow(t, run.ID, "build", 2)
		if row2.StartCommit != cp || row2.State != ledger.AttemptFailed || row2.Stage != "setup deps" {
			t.Fatalf("attempt 2: %+v", row2)
		}
		if a3.Attempt.Start != a1.Attempt.Start {
			t.Fatalf("attempt 3 starts at %s, not at attempt 1's start %s", short(a3.Attempt.Start), short(a1.Attempt.Start))
		}
		failed := l.payloads(run.ID, "attempt.failed")
		if p := failed[len(failed)-1]; p["attempt"] != 2 || p["uncounted"] != true || p["checkpoint"] != cp {
			t.Fatalf("attempt.failed: %v", p)
		}
		if r, _ := e.Get(run.ID); r.Paused || l.count(run.ID, "run.paused") != 0 {
			t.Fatalf("the run paused for a refused checkpoint: %+v", r.Paused)
		}
		md, f := taskFileOf(t, a3)
		if f.Checkpoint != nil || f.Previous == nil || f.Previous.Attempt != 1 || f.Previous.Checkpoint != cp || f.Previous.Branch != a1.Attempt.Branch || f.Unusable == nil || f.Unusable.Attempt != 2 ||
			!strings.Contains(md, fmt.Sprintf("Attempt 1's checkpoint, the commit %s on the branch %s, could not be made usable: attempt 2 failed at setup deps", cp, a1.Attempt.Branch)) ||
			strings.Contains(md, "## Where the last attempt got to") || f.StartFrom != "where attempt 1 started: its checkpoint "+short(cp)+" could not be made usable" {
			t.Fatalf("attempt 3's task file: checkpoint %+v previous %+v unusable %+v from %q\n%s", f.Checkpoint, f.Previous, f.Unusable, f.StartFrom, md)
		}
		stopRun(t, e, run.ID)
	})
	t.Run("a second failure pauses", func(t *testing.T) {
		dir := newRepo(t)
		e, fl, l := managedEngine(t)
		mark := filepath.Join(t.TempDir(), "down")
		c := repoManifestCrew(dir, Manifest{Setup: []Check{shStep("deps", `if [ -e broken.txt ] || [ -e "`+mark+`" ]; then exit 1; fi`)}}, work("build", "lead"))
		run := launchManaged(t, e, c)
		a1 := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
		open(t, mark)
		checkpointAndEnd(t, e, fl, a1)
		attemptFailed(t, e, run.ID, "build", 3)
		if row3 := l.attemptRow(t, run.ID, "build", 3); row3.StartCommit != a1.Attempt.Start || row3.Stage != "setup deps" {
			t.Fatalf("attempt 3: %+v", row3)
		}
		waitTrue(t, "the run paused", func() bool {
			r, _ := e.Get(run.ID)
			return r.Paused && strings.HasPrefix(r.PausedWhy, "attempt 3 could not be made usable at setup deps")
		})
		if err := os.Remove(mark); err != nil {
			t.Fatal(err)
		}
		pauseRun(t, e, run.ID, false)
		if a4 := attemptN(t, e, run.ID, "build", 4); a4.Attempt.Start != a1.Attempt.Start {
			t.Fatalf("attempt 4 starts at %s, not at attempt 1's start", short(a4.Attempt.Start))
		}
		stopRun(t, e, run.ID)
	})
	t.Run("after a continuation", func(t *testing.T) {
		dir := newRepo(t)
		e, fl, l := managedEngine(t)
		gate := filepath.Join(t.TempDir(), "gate")
		c := repoManifestCrew(dir, Manifest{Setup: []Check{shStep("deps", `if [ -e broken.txt ]; then `+gated(gate)+`; exit 1; fi`)}}, work("build", "lead"))
		run := launchManaged(t, e, c)
		a1 := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
		cp := checkpointAndEnd(t, e, fl, a1)
		// Attempt 2's setup holds at the gate; the run is paused, then the
		// step fails: the checkpoint refused, nothing dispatched.
		waitFake(t, "attempt 2's setup running", func() bool {
			rows := l.stepRows(run.ID, "build", 2)
			return len(rows) == 1 && rows[0].State == ledger.CheckRunning
		})
		pauseRun(t, e, run.ID, true)
		open(t, gate)
		attemptFailed(t, e, run.ID, "build", 2)
		stopRun(t, e, run.ID)
		e2, _ := readyEngine(t)
		e2.UseLedger(l)
		next, err := e2.ContinueRun(t.Context(), run.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		e2.mu.Lock()
		task := e2.runs[next.ID].task("build")
		cpGone, u := task.checkpoint == nil, task.unusable
		e2.mu.Unlock()
		if !cpGone || u == nil || u.n != 2 || u.checkpoint != cp || u.next != a1.Attempt.Start {
			t.Fatalf("the continuation's task: checkpoint gone %v, unusable %+v", cpGone, u)
		}
		pauseRun(t, e2, next.ID, false)
		a3 := attemptN(t, e2, next.ID, "build", 3)
		if a3.Attempt.Start != a1.Attempt.Start {
			t.Fatalf("attempt 3 starts at %s, not at attempt 1's start", short(a3.Attempt.Start))
		}
		md, f := taskFileOf(t, a3)
		if f.Checkpoint != nil || f.Previous == nil || f.Previous.Attempt != 1 || f.Previous.Checkpoint != cp || f.Unusable == nil || f.Unusable.Attempt != 2 || strings.Contains(md, "## Where the last attempt got to") {
			t.Fatalf("attempt 3's task file: checkpoint %+v previous %+v unusable %+v\n%s", f.Checkpoint, f.Previous, f.Unusable, md)
		}
		stopRun(t, e2, next.ID)
	})
}
