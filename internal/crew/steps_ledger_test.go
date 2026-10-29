package crew

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/crew/ledger"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// twoMiB is a line of sh writing 2 MiB of the letter, then the end mark.
func twoMiB(letter, end string) string {
	return `head -c 2097152 /dev/zero | tr '\000' ` + letter + `; echo; echo ` + end
}

// A setup step and a check that each write 2 MiB are rows the ledger takes
// (EN1 PR-2; on main the check's log passed MaxLog and its attempt failed
// with "the ledger could not record the check's outcome"): each finished
// with its own exit, its log at most MaxLog, holding the dropped line and
// the output's last bytes, its hash the SHA-256 of the bytes kept.
func TestAStepsAndAChecksLargeOutputAreRowsTheLedgerTakes(t *testing.T) {
	dir := newRepo(t)
	e, ld := sqlEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{shStep("unit", twoMiB("c", "CHECK-END"))}
	run := launchManaged(t, e, repoManifestCrew(dir, Manifest{Setup: []Check{shStep("deps", twoMiB("s", "SETUP-END"))}}, build))
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, a, "built.txt", "yes\n", "built")
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	rows, err := ld.Checks(context.Background(), run.ID, "build", 1, true)
	if err != nil || len(rows) != 3 {
		t.Fatalf("the rows: %d %v", len(rows), err)
	}
	for _, r := range rows {
		end := "SETUP-END\n"
		if r.Phase == ledger.PhaseCheck {
			end = "CHECK-END\n"
		}
		if r.State != ledger.CheckFinished || r.Exit != 0 || r.Error != "" || r.LogDropped || len(r.Log) > ledger.MaxLog || int64(len(r.Log)) != r.LogBytes ||
			!bytes.Contains(r.Log, []byte(" bytes dropped …\n")) || !bytes.HasSuffix(r.Log, []byte(end)) || r.LogSHA256 != sha(r.Log) || r.Total != 2097152+1+int64(len(end)) {
			t.Fatalf("%s %s k%d: %s exit %d error %q, a log of %d bytes (%d), total %d, dropped line %v, ends %q, sha ok %v", r.Phase, r.ID, r.K, r.State, r.Exit, r.Error, len(r.Log), r.LogBytes, r.Total,
				bytes.Contains(r.Log, []byte(" bytes dropped …\n")), r.Log[max(0, len(r.Log)-20):], r.LogSHA256 == sha(r.Log))
		}
	}
	stopRun(t, e, run.ID)
}

// A tree the owner left between its attempt's close and its recorded end
// (the engine ended at the seam, the run finished, the tree's row not
// ended) is ended by the recovery at the next start: ended and
// interrupted, with its tree.ended, counted in Recovered.Trees; the run
// read after has nothing cleaning.
func TestATreeTheOwnerLeftEndingIsEndedByTheRecovery(t *testing.T) {
	dir := newRepo(t)
	e, _ := readyEngine(t)
	ldir := t.TempDir()
	ld, err := ledger.Open(ldir)
	if err != nil {
		t.Fatal(err)
	}
	e.UseLedger(SQLLedger(ld))
	held, release := make(chan string, 2), make(chan struct{})
	e.beforeTreeEnd = func(kind string) {
		held <- kind
		<-release
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	run := launchManaged(t, e, repoManifestCrew(dir, Manifest{Setup: []Check{shStep("deps", "true")}}, work("build", "lead")))
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	commitResult(t, e, a, "built.txt", "yes\n", "built")
	waitOutcome(t, e, run.ID, ledger.OutcomeSucceeded)
	kinds := []string{<-held, <-held}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{ledger.TreeCheckout, ledger.TreeWorktree}) {
		t.Fatalf("the trees at the seam: %v", kinds)
	}
	snap, _ := e.Get(run.ID)
	if snap.Cleaning != 2 {
		t.Fatalf("the run while its trees end: cleaning %d", snap.Cleaning)
	}
	// The owner ends here: its ledger closed before the trees' ends.
	if err := ld.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := ledger.Open(ldir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	ctx := context.Background()
	rec, err := again.Recover(ctx)
	if err != nil || len(rec) != 1 || rec[0].RunID != run.ID || rec[0].Trees != 2 || rec[0].Interrupted {
		t.Fatalf("the recovery: %+v %v", rec, err)
	}
	trees, _ := again.Trees(ctx, run.ID)
	for _, tr := range trees {
		if tr.State != ledger.TreeEnded || !tr.Interrupted || !strings.Contains(tr.Why, "the owner stopped") {
			t.Fatalf("a tree after the recovery: %+v", tr)
		}
	}
	if r, _, _ := again.Run(ctx, run.ID); r.State != ledger.RunFinished {
		t.Fatalf("the finished run's state: %+v", r)
	}
	from, ok, err := SQLLedger(again).Load(ctx, run.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if read := Recovered(snap, from); read.Cleaning != 0 || read.State != RunFinished {
		t.Fatalf("the run read after: cleaning %d, %s", read.Cleaning, read.State)
	}
	close(release)
}

// A cancel during a setup step kills the step with its group and records
// it interrupted; the worktree's end follows, once, after the step's row.
func TestACancelDuringASetupStepEndsItsTreeOnce(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	gate, pidFile := filepath.Join(t.TempDir(), "gate"), filepath.Join(t.TempDir(), "pid")
	run := launchManaged(t, e, repoManifestCrew(dir, Manifest{Setup: []Check{shStep("deps", `trap "" TERM; echo $$ > "`+pidFile+`"; `+gated(gate))}}, work("build", "lead")))
	pid := pidIn(t, pidFile)
	if err := e.CancelTask(t.Context(), run.ID, "build"); err != nil {
		t.Fatal(err)
	}
	waitFake(t, "the worktree's end", func() bool {
		tr, _ := l.treeRow(run.ID, "build", 1, ledger.TreeWorktree)
		return tr.State == ledger.TreeEnded
	})
	if alive(pid) {
		t.Fatalf("the setup step's process %d outlived the cancel", pid)
	}
	rows := l.stepRows(run.ID, "build", 1)
	if len(rows) != 1 || rows[0].State != ledger.CheckInterrupted || rows[0].Error != "deps was interrupted: the attempt was cancelled" {
		t.Fatalf("the step:\n%s", rowWords(rows))
	}
	kinds := l.kinds(run.ID)
	if n := l.count(run.ID, "tree.ended"); n != 1 {
		t.Fatalf("%d tree.ended events: %v", n, kinds)
	}
	finished := -1
	for i, k := range kinds {
		if k == "step.finished" {
			finished = i
		}
	}
	if ended := slices.Index(kinds, "tree.ended"); finished < 0 || ended < finished {
		t.Fatalf("the tree ended before its step's row: %v", kinds)
	}
	if ts := taskOf(t, e, run.ID, "build"); ts.State != ledger.TaskCancelled {
		t.Fatalf("the task: %+v", ts)
	}
	waitTreesEnded(t, e, run.ID)
	if r, _ := e.Get(run.ID); r.Cleaning != 0 {
		t.Fatalf("cleaning %d after the tree ended", r.Cleaning)
	}
	open(t, gate)
	stopRun(t, e, run.ID)
}

// A cancel that commits while the attempt's session is being launched
// (Server.Launch looks at its context only at its start) ends the worktree
// only once the launch has returned and the session it made, stopped for
// the attempt the cancel closed, has ended: the launch holds the tree, and
// the tree's end waits for the session the launch names (Codex's review of
// #422, round 5). The launch is held after its process is made, until the
// tree's end has begun.
func TestACancelDuringTheLaunchEndsTheTreeAfterItsSession(t *testing.T) {
	dir := newRepo(t)
	e, fl, l := managedEngine(t)
	hold, held := make(chan struct{}), make(chan *session.Local, 1)
	fl.mu.Lock()
	fl.launchHold, fl.launchHeld = hold, held
	fl.mu.Unlock()
	// At the worktree's end, whether the session the launch made had ended.
	var mu sync.Mutex
	var sess *session.Local
	var seen, endedFirst bool
	e.beforeTreeEnd = func(kind string) {
		if kind != ledger.TreeWorktree {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		seen = true
		if sess == nil {
			return
		}
		select {
		case <-sess.Ended():
			endedFirst = true
		default:
		}
	}
	run := launchManaged(t, e, repoManifestCrew(dir, Manifest{Setup: []Check{shStep("deps", "true")}}, work("build", "lead")))
	var s *session.Local
	select {
	case s = <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("no launch was held")
	}
	t.Cleanup(func() {
		select {
		case <-hold:
		default:
			close(hold)
		}
	})
	mu.Lock()
	sess = s
	mu.Unlock()
	r := runOf(t, e, run.ID)
	e.mu.Lock()
	a := r.task("build").attempts[0]
	if len(a.trees) != 1 || a.trees[0].kind != ledger.TreeWorktree || a.sessionID != "" {
		e.mu.Unlock()
		t.Fatalf("the attempt while its launch is held: %d trees, session %q", len(a.trees), a.sessionID)
	}
	tr := a.trees[0]
	e.mu.Unlock()
	if err := e.CancelTask(t.Context(), run.ID, "build"); err != nil {
		t.Fatal(err)
	}
	// The tree's end has begun with the launch still held: the session
	// cannot end before the launch returns.
	waitFake(t, "the worktree's end to begin", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return tr.ending
	})
	close(hold)
	waitFake(t, "the worktree's end", func() bool {
		row, _ := l.treeRow(run.ID, "build", 1, ledger.TreeWorktree)
		return row.State == ledger.TreeEnded
	})
	waitTreesEnded(t, e, run.ID)
	mu.Lock()
	saw, ok := seen, endedFirst
	mu.Unlock()
	if !saw || !ok {
		t.Fatalf("the worktree ended before the session its launch made had ended (its end seen: %v)", saw)
	}
	select {
	case <-s.Ended():
	default:
		t.Fatal("the session the launch made is still running")
	}
	kinds := l.kinds(run.ID)
	launched, ended := slices.Index(kinds, "attempt.launched"), slices.Index(kinds, "tree.ended")
	if launched < 0 || ended < launched || l.count(run.ID, "tree.ended") != 1 {
		t.Fatalf("the tree's end and the launch, in the ledger: %v", kinds)
	}
	if ts := taskOf(t, e, run.ID, "build"); ts.State != ledger.TaskCancelled || ts.Attempt.State != ledger.AttemptCancelled {
		t.Fatalf("the task: %+v %+v", ts, ts.Attempt)
	}
	if got, _ := e.Get(run.ID); got.Cleaning != 0 {
		t.Fatalf("cleaning %d after the tree ended", got.Cleaning)
	}
	stopRun(t, e, run.ID)
}
