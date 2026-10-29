package ledger

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// version5Added is what version 5 added, dropped: a version 5 database
// made a version 4 one, for a migration's fixture.
var version5Added = []string{
	"ALTER TABLE checks DROP COLUMN phase",
	"ALTER TABLE checks DROP COLUMN manifest",
	"ALTER TABLE checks DROP COLUMN ports",
	"ALTER TABLE checks DROP COLUMN tries",
	"ALTER TABLE checks DROP COLUMN error",
	"ALTER TABLE checks DROP COLUMN url",
	"ALTER TABLE runs DROP COLUMN paused_why",
	"DROP TABLE prerequisites",
	"DROP TABLE trees",
}

// attemptOf puts a run's task and its attempt, for the rows that hang on
// them.
func attemptOf(t *testing.T, l *Ledger, runID, taskID string, n int, state string) {
	t.Helper()
	if _, err := l.Transition(context.Background(), runID, func(tx *Tx) error {
		if err := tx.PutTask(TaskRow{RunID: runID, ID: taskID, Member: "lead", Kind: "work", State: TaskRunning}); err != nil {
			return err
		}
		return tx.PutAttempt(AttemptRow{RunID: runID, TaskID: taskID, N: n, Member: "lead", State: state, ReservedAt: tx.Now()})
	}); err != nil {
		t.Fatal(err)
	}
}

// finish closes a run as finished, its outcome given.
func finish(t *testing.T, l *Ledger, runID string) {
	t.Helper()
	if _, err := l.Transition(context.Background(), runID, func(tx *Tx) error {
		r, _, err := tx.Run()
		if err != nil {
			return err
		}
		r.State, r.Outcome, r.ClosedAt = RunFinished, OutcomeSucceeded, tx.Now()
		return tx.PutRun(r)
	}); err != nil {
		t.Fatal(err)
	}
}

// A version 4 database (every checks row a task's check, no pause's
// words, no prerequisites or trees) is brought to 5 as it opens: its rows
// read back as checks of phase check, with no manifest, no ports, one try
// and no error or URL, the run with no pause's words, and the events it
// had, the recovery's among them, as they were.
func TestAVersion4DatabaseIsBroughtToVersion5(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	l := openLedger(t, dir)
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	attemptOf(t, l, "team-00000001", "build", 1, AttemptRunning)
	if _, err := l.db.Exec("UPDATE runs SET paused = 1 WHERE id = 'team-00000001'"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		return tx.PutCheck(CheckRow{RunID: "team-00000001", TaskID: "build", N: 1, ID: "unit", K: 1, Argv: []string{"go", "test"}, State: CheckRunning, StartedAt: tx.Now(), Log: []byte("ok"), LogSHA256: "h"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := l.Events(ctx, "team-00000001", 0, 100)
	for _, stmt := range append(version5Added, "UPDATE schema_version SET version = 4") {
		if _, err := l.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	l.Close()
	again := openLedger(t, dir)
	if ro, why := again.ReadOnly(); ro {
		t.Fatalf("read-only after the migration: %s", why)
	}
	var version int
	if err := again.db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != 5 || Version != 5 {
		t.Fatalf("version %d (%v), Version %d", version, err, Version)
	}
	checks, err := again.Checks(ctx, "team-00000001", "build", 1, true)
	if err != nil || len(checks) != 1 {
		t.Fatalf("checks after the migration: %+v %v", checks, err)
	}
	if c := checks[0]; c.Phase != PhaseCheck || c.Manifest != "" || c.Ports != nil || c.Tries != 1 || c.Error != "" || c.URL != "" || c.State != CheckInterrupted || string(c.Log) != "ok" || c.LogBytes != 2 {
		t.Fatalf("the check after the migration: %+v", c)
	}
	if r, ok, err := again.Run(ctx, "team-00000001"); err != nil || !ok || !r.Paused || r.PausedWhy != "" {
		t.Fatalf("the run after the migration: %+v %v %v", r, ok, err)
	}
	after, _ := again.Events(ctx, "team-00000001", 0, 100)
	if len(after) != len(before) || after[len(after)-1].Kind != "recovered" || after[len(after)-1].Payload != before[len(before)-1].Payload {
		t.Fatalf("the events after the migration: %+v, before %+v", after, before)
	}
	if prereqs, err := again.Prerequisites(ctx, "team-00000001", false); err != nil || len(prereqs) != 0 {
		t.Fatalf("prerequisites after the migration: %v %v", prereqs, err)
	}
	if trees, err := again.Trees(ctx, "team-00000001"); err != nil || len(trees) != 0 {
		t.Fatalf("trees after the migration: %v %v", trees, err)
	}
}

// A step's receipt is a checks row with its phase, the manifest that ran
// it, the tree's ports, its tries, its words and a probe's URL; a check's
// row keeps phase check. Check reads one row with its log, Checks every
// row without it, LogBytes either way.
func TestAStepsRowCarriesItsPhaseAndWords(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	attemptOf(t, l, "team-00000001", "build", 1, AttemptRunning)
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		for _, r := range []CheckRow{
			{ID: "deps", K: 1, Phase: PhaseSetup, Manifest: "crew:3", Ports: map[string]int{"web": 41234}, Error: "deps exited 1: no registry", Argv: []string{"npm", "ci"}, State: CheckFinished, Exit: 1, Log: []byte("npm ERR!")},
			{ID: "web", K: 2, Phase: PhaseReady, Manifest: "file:abc", Tries: 3, URL: "http://127.0.0.1:41234/", State: CheckFinished, Exit: 0},
			{ID: "unit", K: 3, Argv: []string{"go", "test"}, State: CheckFinished},
		} {
			r.RunID, r.TaskID, r.N, r.StartedAt = "team-00000001", "build", 1, tx.Now()
			if err := tx.PutCheck(r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checks, err := l.Checks(ctx, "team-00000001", "build", 1, false)
	if err != nil || len(checks) != 3 {
		t.Fatalf("%+v %v", checks, err)
	}
	by := map[string]CheckRow{}
	for _, c := range checks {
		by[c.ID] = c
	}
	if c := by["deps"]; c.Phase != PhaseSetup || c.Manifest != "crew:3" || c.Ports["web"] != 41234 || c.Tries != 1 || c.Error != "deps exited 1: no registry" || c.Log != nil || c.LogBytes != 8 {
		t.Fatalf("the setup step: %+v", c)
	}
	if c := by["web"]; c.Phase != PhaseReady || c.Tries != 3 || c.URL != "http://127.0.0.1:41234/" || c.Argv != nil {
		t.Fatalf("the probe: %+v", c)
	}
	if c := by["unit"]; c.Phase != PhaseCheck || c.Manifest != "" || c.Tries != 1 {
		t.Fatalf("the check: %+v", c)
	}
	if c, ok, err := l.Check(ctx, "team-00000001", "build", 1, "deps", 1); err != nil || !ok || string(c.Log) != "npm ERR!" || c.Error != "deps exited 1: no registry" {
		t.Fatalf("one row with its log: %+v %v %v", c, ok, err)
	}
	if _, ok, err := l.Check(ctx, "team-00000001", "build", 1, "deps", 9); err != nil || ok {
		t.Fatalf("a row that is not there: %v %v", ok, err)
	}
	// A URL past its bound, or words past an event's, are refused.
	for name, r := range map[string]CheckRow{
		"url":   {ID: "web", K: 4, URL: "http://127.0.0.1/" + strings.Repeat("x", MaxURL)},
		"error": {ID: "web", K: 5, Error: strings.Repeat("x", MaxPayload+1)},
	} {
		if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
			r.RunID, r.TaskID, r.N, r.State, r.StartedAt = "team-00000001", "build", 1, CheckStarted, tx.Now()
			return tx.PutCheck(r)
		}); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("%s past its bound: %v", name, err)
		}
	}
}

// A run's pause has its words on its row, written beside paused and read
// back by Run and Runs; an unpause clears them.
func TestARunsPauseKeepsItsWords(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	set := func(paused bool, why string) {
		if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
			r, _, err := tx.Run()
			if err != nil {
				return err
			}
			r.Paused, r.PausedWhy = paused, why
			return tx.PutRun(r)
		}); err != nil {
			t.Fatal(err)
		}
	}
	why := "attempt 2 could not be made usable at setup deps: deps exited 1"
	set(true, why)
	if r, _, _ := l.Run(ctx, "team-00000001"); !r.Paused || r.PausedWhy != why {
		t.Fatalf("Run: %+v", r)
	}
	if runs, _ := l.Runs(ctx); len(runs) != 1 || runs[0].PausedWhy != why {
		t.Fatalf("Runs: %+v", runs)
	}
	set(false, "")
	if r, _, _ := l.Run(ctx, "team-00000001"); r.Paused || r.PausedWhy != "" {
		t.Fatalf("after the unpause: %+v", r)
	}
}

// A prerequisite's receipt is a row of its own, keyed by its run, id and
// k, its logs counted with the checks' toward the run's bound. Recover
// concludes one started or running as it does a check, and selects a
// stopped run whose only open row is a prerequisite; Prune refuses a run
// with one running and deletes it with the run once finished.
func TestAPrerequisitesReceiptIsARowOfItsOwn(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	put := func(r PrerequisiteRow) error {
		_, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
			r.RunID = "team-00000001"
			if r.StartedAt.IsZero() {
				r.StartedAt = tx.Now()
			}
			return tx.PutPrerequisite(r)
		})
		return err
	}
	if err := put(PrerequisiteRow{ID: "node", K: 1, Argv: []string{"node", "--version"}, Dir: "/work", Subject: "abc", Manifest: "crew:1", State: CheckFinished, Exit: 0, Log: []byte("v22\n"), LogSHA256: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := put(PrerequisiteRow{ID: "node", K: 2, Argv: []string{"node", "--version"}, Manifest: "crew:1", State: CheckRunning, PID: 99}); err != nil {
		t.Fatal(err)
	}
	rows, err := l.Prerequisites(ctx, "team-00000001", true)
	if err != nil || len(rows) != 2 || rows[0].K != 1 || string(rows[0].Log) != "v22\n" || rows[0].LogBytes != 4 || rows[0].Manifest != "crew:1" || rows[0].Subject != "abc" || rows[1].State != CheckRunning {
		t.Fatalf("prerequisites: %+v %v", rows, err)
	}
	if r, ok, err := l.Prerequisite(ctx, "team-00000001", "node", 1); err != nil || !ok || string(r.Log) != "v22\n" {
		t.Fatalf("one prerequisite with its log: %+v %v %v", r, ok, err)
	}
	// The run is stopped; its only open row is the prerequisite running.
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		r, _, _ := tx.Run()
		r.State, r.ClosedAt = RunStopped, tx.Now()
		return tx.PutRun(r)
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Prune(ctx, "team-00000001"); !errors.Is(err, ErrNotPrunable) || !strings.Contains(err.Error(), "a prerequisite of it is still running") {
		t.Fatalf("pruned with a prerequisite running: %v", err)
	}
	rec, err := l.Recover(ctx)
	if err != nil || len(rec) != 1 || rec[0].Prerequisites != 1 || rec[0].Interrupted {
		t.Fatalf("recovered %+v %v", rec, err)
	}
	if r, _, _ := l.Prerequisite(ctx, "team-00000001", "node", 2); r.State != CheckInterrupted || r.FinishedAt.IsZero() {
		t.Fatalf("the prerequisite after the recovery: %+v", r)
	}
	if r, _, _ := l.Run(ctx, "team-00000001"); r.State != RunStopped {
		t.Fatalf("the stopped run's state moved: %+v", r)
	}
	evs, _ := l.Events(ctx, "team-00000001", 0, 100)
	if n := countKind(evs, "recovered"); n != 1 {
		t.Fatalf("%d recovered events", n)
	}
	if rec, err := l.Recover(ctx); err != nil || len(rec) != 0 {
		t.Fatalf("a second recovery: %+v %v", rec, err)
	}
	if err := l.Prune(ctx, "team-00000001"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := l.db.QueryRow("SELECT COUNT(*) FROM prerequisites").Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d prerequisites rows after the prune (%v)", n, err)
	}
}

// The prerequisites' logs count toward the run's bound with the checks':
// a prerequisite's log past what the checks left is dropped, its hash
// kept, and a check's past what the prerequisites left.
func TestAPrerequisitesLogCountsTowardTheRunsBound(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	attemptOf(t, l, "team-00000001", "build", 1, AttemptVerifying)
	const unit = 1 << 10
	old := maxRunLogs
	maxRunLogs = 2 * unit
	t.Cleanup(func() { maxRunLogs = old })
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		if err := tx.PutCheck(CheckRow{RunID: "team-00000001", TaskID: "build", N: 1, ID: "unit", K: 1, State: CheckFinished, StartedAt: tx.Now(), Log: make([]byte, unit)}); err != nil {
			return err
		}
		if err := tx.PutPrerequisite(PrerequisiteRow{RunID: "team-00000001", ID: "node", K: 1, State: CheckFinished, StartedAt: tx.Now(), Log: make([]byte, unit), LogSHA256: "h"}); err != nil {
			return err
		}
		return tx.PutPrerequisite(PrerequisiteRow{RunID: "team-00000001", ID: "node", K: 2, State: CheckFinished, StartedAt: tx.Now(), Log: make([]byte, 1), LogSHA256: "h2"})
	}); err != nil {
		t.Fatal(err)
	}
	rows, _ := l.Prerequisites(ctx, "team-00000001", true)
	if len(rows) != 2 || rows[0].LogDropped || len(rows[0].Log) != unit || !rows[1].LogDropped || rows[1].Log != nil || rows[1].LogSHA256 != "h2" {
		t.Fatalf("prerequisites: %+v", rows)
	}
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		return tx.PutCheck(CheckRow{RunID: "team-00000001", TaskID: "build", N: 1, ID: "lint", K: 1, State: CheckFinished, StartedAt: tx.Now(), Log: make([]byte, 1)})
	}); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := l.Check(ctx, "team-00000001", "build", 1, "lint", 1); !c.LogDropped {
		t.Fatalf("a check's log past what the prerequisites left: %+v", c)
	}
}

// A tree's row runs resolved → ready → ended. One the owner left not
// ended, under a finished run, is selected by Recover and concluded ended
// and interrupted with a tree.ended event of its own; one ended is left
// alone. Prune refuses a finished run whose tree is not ended, and
// deletes the trees with the run once they are.
func TestATreeLeftOpenIsEndedByTheRecovery(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	attemptOf(t, l, "team-00000001", "build", 1, AttemptSucceeded)
	putTree := func(r TreeRow) {
		if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
			r.RunID, r.TaskID, r.N = "team-00000001", "build", 1
			return tx.PutTree(r)
		}); err != nil {
			t.Fatal(err)
		}
	}
	putTree(TreeRow{Kind: TreeWorktree, Dir: "/w/build.1", Commit: "abc", Manifest: "crew:1", State: TreeResolved})
	putTree(TreeRow{Kind: TreeWorktree, Dir: "/w/build.1", Commit: "abc", Manifest: "crew:1", Ports: map[string]int{"web": 4000}, Preview: "http://127.0.0.1:4000/", State: TreeReady})
	putTree(TreeRow{Kind: TreeCheckout, Dir: "/w/build.1.check", Commit: "def", Manifest: "crew:1", State: TreeEnded, Why: "attempt 1 succeeded"})
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		return tx.PutCheck(CheckRow{RunID: "team-00000001", TaskID: "build", N: 1, ID: "deps", K: 1, Phase: PhaseSetup, Manifest: "crew:1", State: CheckRunning, StartedAt: tx.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	finish(t, l, "team-00000001")
	trees, err := l.Trees(ctx, "team-00000001")
	if err != nil || len(trees) != 2 || trees[1].Kind != TreeWorktree || trees[1].State != TreeReady || trees[1].Ports["web"] != 4000 || trees[1].Preview != "http://127.0.0.1:4000/" {
		t.Fatalf("trees: %+v %v", trees, err)
	}
	if err := l.Prune(ctx, "team-00000001"); !errors.Is(err, ErrNotPrunable) || !strings.Contains(err.Error(), "a tree of it is still ending") {
		t.Fatalf("pruned with a tree ending: %v", err)
	}
	rec, err := l.Recover(ctx)
	if err != nil || len(rec) != 1 || rec[0].Trees != 1 || rec[0].Checks != 1 || rec[0].Interrupted {
		t.Fatalf("recovered %+v %v", rec, err)
	}
	trees, _ = l.Trees(ctx, "team-00000001")
	for _, tr := range trees {
		switch tr.Kind {
		case TreeWorktree:
			if tr.State != TreeEnded || !tr.Interrupted || tr.Why != treeLeftOpen || tr.EndedAt.IsZero() {
				t.Fatalf("the worktree after the recovery: %+v", tr)
			}
		case TreeCheckout:
			if tr.Interrupted || tr.Why != "attempt 1 succeeded" {
				t.Fatalf("the checkout ended before was touched: %+v", tr)
			}
		}
	}
	if r, _, _ := l.Run(ctx, "team-00000001"); r.State != RunFinished {
		t.Fatalf("the finished run's state moved: %+v", r)
	}
	evs, _ := l.Events(ctx, "team-00000001", 0, 100)
	var ended []string
	for _, ev := range evs {
		if ev.Kind == "tree.ended" {
			ended = append(ended, ev.Payload)
		}
	}
	if len(ended) != 1 || !strings.Contains(ended[0], `"tree":"worktree"`) || !strings.Contains(ended[0], `"recovered":true`) || !strings.Contains(ended[0], `"interrupted":true`) {
		t.Fatalf("tree.ended events: %v", ended)
	}
	if rec, err := l.Recover(ctx); err != nil || len(rec) != 0 {
		t.Fatalf("a second recovery: %+v %v", rec, err)
	}
	if err := l.Prune(ctx, "team-00000001"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := l.db.QueryRow("SELECT COUNT(*) FROM trees").Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d trees rows after the prune (%v)", n, err)
	}
}

// The recovery's payloads are bounded by their shape: many trees left
// open, each a tree.ended of its own, the recovered event its counts.
func TestTheRecoveryOfManyTreesFitsItsPayloads(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	const tasks = 32
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		for i := range tasks {
			id := fmt.Sprintf("t%02d-%s", i, strings.Repeat("x", 30))
			if err := tx.PutTask(TaskRow{RunID: "team-00000001", ID: id, Member: "lead", Kind: "work", State: TaskRunning}); err != nil {
				return err
			}
			if err := tx.PutAttempt(AttemptRow{RunID: "team-00000001", TaskID: id, N: 1, Member: "lead", State: AttemptRunning, ReservedAt: tx.Now()}); err != nil {
				return err
			}
			for _, kind := range []string{TreeWorktree, TreeCheckout} {
				if err := tx.PutTree(TreeRow{RunID: "team-00000001", TaskID: id, N: 1, Kind: kind, Dir: strings.Repeat("d", 4000), State: TreeReady}); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := l.Recover(ctx)
	if err != nil || len(rec) != 1 || rec[0].Trees != 2*tasks {
		t.Fatalf("recovered %+v %v", rec, err)
	}
	evs, _ := l.Events(ctx, "team-00000001", 0, 1000)
	if n := countKind(evs, "tree.ended"); n != 2*tasks {
		t.Fatalf("%d tree.ended events", n)
	}
	for _, ev := range evs {
		if len(ev.Payload) > 512 {
			t.Fatalf("a %s payload of %d bytes", ev.Kind, len(ev.Payload))
		}
	}
}

func countKind(evs []EventRow, kind string) int {
	n := 0
	for _, ev := range evs {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}
