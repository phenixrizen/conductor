package ledger

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openLedger(t *testing.T, dir string) *Ledger {
	t.Helper()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func newRun(t *testing.T, l *Ledger, id string) {
	t.Helper()
	ctx := context.Background()
	rev, err := l.Transition(ctx, id, func(tx *Tx) error {
		if err := tx.PutRun(RunRow{ID: id, CrewID: "team", Crew: `{"id":"team"}`, Base: "abc123", State: RunRunning, CreatedAt: tx.Now()}); err != nil {
			return err
		}
		return tx.Event("run.created", map[string]any{"base": "abc123"})
	})
	if err != nil || rev != 1 {
		t.Fatalf("new run %s: rev %d err %v", id, rev, err)
	}
}

func TestOpenMakesTheSchema(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	l := openLedger(t, dir)
	if ro, why := l.ReadOnly(); ro {
		t.Fatalf("read-only: %s", why)
	}
	for _, name := range []string{File, Lock} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == File && fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %o", name, fi.Mode().Perm())
		}
	}
	rows, err := l.db.Query("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	if want := "attempts checks commands events runs schema_version tasks"; strings.Join(tables, " ") != want {
		t.Fatalf("tables %v", tables)
	}
	newRun(t, l, "team-00000001")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	// Closed with its write-ahead log checkpointed, and opened again whole.
	if fi, err := os.Stat(filepath.Join(dir, File+"-wal")); err == nil && fi.Size() != 0 {
		t.Fatalf("the WAL holds %d bytes after Close", fi.Size())
	}
	again := openLedger(t, dir)
	if r, ok, err := again.Run(context.Background(), "team-00000001"); err != nil || !ok || r.Revision != 1 || r.State != RunRunning || r.Base != "abc123" {
		t.Fatalf("after reopen: %+v %v %v", r, ok, err)
	}
}

func TestANewerSchemaOpensReadOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	l := openLedger(t, dir)
	newRun(t, l, "team-00000001")
	if _, err := l.db.Exec("UPDATE schema_version SET version = ?", Version+1); err != nil {
		t.Fatal(err)
	}
	l.Close()
	again := openLedger(t, dir)
	ro, why := again.ReadOnly()
	if !ro || !strings.Contains(why, "newer") {
		t.Fatalf("read-only %v: %s", ro, why)
	}
	if _, err := again.Transition(context.Background(), "team-00000001", func(*Tx) error { return nil }); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("a write: %v", err)
	}
	if _, err := again.Recover(context.Background()); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("a recover: %v", err)
	}
	if runs, err := again.Runs(context.Background()); err != nil || len(runs) != 1 {
		t.Fatalf("a read: %v %v", runs, err)
	}
}

func TestADamagedDatabaseIsNeverWritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	l := openLedger(t, dir)
	for i := range 40 {
		newRun(t, l, fmt.Sprintf("team-%08x", i))
	}
	l.Close()
	path := filepath.Join(dir, File)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Garbage over the third page, a table's root: the file header stays,
	// the content does not.
	if _, err := f.WriteAt([]byte(strings.Repeat("\xff", 4096)), 2*4096); err != nil {
		t.Fatal(err)
	}
	f.Close()
	again, err := Open(dir)
	if err != nil {
		// Refused outright is as good: nothing is written to it.
		return
	}
	defer again.Close()
	if ro, _ := again.ReadOnly(); !ro {
		t.Fatal("a damaged database opened for writing")
	}
}

// The helper holds the owner lock of the directory LEDGER_HOLD until its
// stdin closes.
func TestHelperHoldsTheOwnerLock(t *testing.T) {
	dir := os.Getenv("LEDGER_HOLD")
	if dir == "" {
		t.Skip("a helper")
	}
	l, err := Open(dir)
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(2)
	}
	fmt.Println("held")
	bufio.NewReader(os.Stdin).ReadString('\n')
	l.Close()
}

func TestTheOwnerLockHoldsAcrossProcesses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperHoldsTheOwnerLock$", "-test.v")
	helper.Env = append(os.Environ(), "LEDGER_HOLD="+dir)
	stdin, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		helper.Wait()
	}()
	sc := bufio.NewScanner(stdout)
	held := false
	for sc.Scan() {
		if sc.Text() == "held" {
			held = true
			break
		}
		if strings.HasPrefix(sc.Text(), "error:") {
			t.Fatal(sc.Text())
		}
	}
	if !held {
		t.Fatal("the helper did not take the lock")
	}
	old := LockWait
	LockWait = 300 * time.Millisecond
	t.Cleanup(func() { LockWait = old })
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ro, why := l.ReadOnly()
	if !ro || !strings.Contains(why, "another server holds") || !strings.Contains(why, "pid") {
		t.Fatalf("read-only %v: %s", ro, why)
	}
	l.Close()
	// The helper lets go: the lock is ours.
	stdin.Close()
	helper.Wait()
	l = openLedger(t, dir)
	if ro, why := l.ReadOnly(); ro {
		t.Fatalf("still read-only: %s", why)
	}
}

func TestATransitionIsAllOrNothing(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	boom := errors.New("boom")
	_, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		if err := tx.PutRun(RunRow{ID: "team-00000001", CrewID: "team", State: RunRunning, CreatedAt: tx.Now()}); err != nil {
			return err
		}
		if err := tx.Event("run.created", nil); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if _, ok, _ := l.Run(ctx, "team-00000001"); ok {
		t.Fatal("the run row survived the failed transition")
	}
	if ev, _ := l.Events(ctx, "team-00000001", 0, 10); len(ev) != 0 {
		t.Fatalf("events %v", ev)
	}
	// A transition on a run with no row fails too: an event of it breaks
	// the foreign key, and a transition writing nothing finds no row.
	if _, err := l.Transition(ctx, "team-00000002", func(tx *Tx) error { return tx.Event("x", nil) }); err == nil {
		t.Fatal("an event of a run with no row")
	}
	if _, err := l.Transition(ctx, "team-00000002", func(tx *Tx) error { return nil }); !errors.Is(err, ErrNoRun) {
		t.Fatalf("no run: %v", err)
	}
	newRun(t, l, "team-00000001")
	// A commit that fails leaves the intent unwritten, and later ones land.
	l.beforeCommit = func() error { return errors.New("disk full") }
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error { return tx.Event("dispatch", nil) }); err == nil {
		t.Fatal("a failed commit succeeded")
	}
	l.beforeCommit = nil
	if ev, _ := l.Events(ctx, "team-00000001", 0, 10); len(ev) != 1 {
		t.Fatalf("events after the failed commit: %v", ev)
	}
	if rev, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error { return tx.Event("launched", nil) }); err != nil || rev != 2 {
		t.Fatalf("after the failed commit: rev %d err %v", rev, err)
	}
	if ev, _ := l.Events(ctx, "team-00000001", 0, 10); len(ev) != 2 || ev[1].Seq != 2 || ev[1].Kind != "launched" {
		t.Fatalf("events %v", ev)
	}
}

func TestEventsAreSequencedWithoutGaps(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error { return tx.Event("tick", map[string]int{"i": i}) }); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	ev, err := l.Events(ctx, "team-00000001", 0, 100)
	if err != nil || len(ev) != 51 {
		t.Fatalf("%d events %v", len(ev), err)
	}
	for i, e := range ev {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i, e.Seq)
		}
	}
	if r, _, _ := l.Run(ctx, "team-00000001"); r.Revision != 51 {
		t.Fatalf("revision %d", r.Revision)
	}
	if after, _ := l.Events(ctx, "team-00000001", 49, 100); len(after) != 2 || after[0].Seq != 50 {
		t.Fatalf("after 49: %v", after)
	}
}

func TestReceipts(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	old := maxCommands
	maxCommands = 3
	t.Cleanup(func() { maxCommands = old })
	command := func(actor, id, intent, fp string, settle string) (CommandRow, bool, error) {
		var prior CommandRow
		var found bool
		_, err := l.Transition(ctx, "team-00000001", func(tx *Tx) (err error) {
			prior, found, err = tx.Command(actor, id, intent, fp)
			if err != nil || found {
				return err
			}
			return tx.Settle(actor, id, settle, map[string]string{"task": "build"})
		})
		return prior, found, err
	}
	if _, found, err := command("alice", "c1", "task.retry", "fp1", CommandDone); err != nil || found {
		t.Fatalf("new: %v %v", found, err)
	}
	prior, found, err := command("alice", "c1", "task.retry", "fp1", CommandDone)
	if err != nil || !found || prior.Status != CommandDone || prior.Receipt != `{"task":"build"}` {
		t.Fatalf("repeat: %+v %v %v", prior, found, err)
	}
	if _, _, err := command("alice", "c1", "task.retry", "fp2", CommandDone); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("other content: %v", err)
	}
	if _, _, err := command("alice", "c1", "task.cancel", "fp1", CommandDone); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("other intent: %v", err)
	}
	if _, found, err := command("bob", "c1", "task.retry", "fp1", CommandUncertain); err != nil || found {
		t.Fatalf("another actor: %v %v", found, err)
	}
	if _, found, err := command("alice", "c2", "run.pause", "fp3", CommandFailed); err != nil || found {
		t.Fatalf("c2: %v %v", found, err)
	}
	if _, _, err := command("alice", "c3", "run.pause", "fp4", CommandDone); !errors.Is(err, ErrTooManyCommands) {
		t.Fatalf("past the bound: %v", err)
	}
	if r, ok, err := l.Receipt(ctx, "team-00000001", "bob", "c1"); err != nil || !ok || r.Status != CommandUncertain || r.Intent != "task.retry" {
		t.Fatalf("bob's receipt: %+v %v %v", r, ok, err)
	}
	if _, ok, _ := l.Receipt(ctx, "team-00000001", "carol", "c1"); ok {
		t.Fatal("carol has a receipt")
	}
	if all, _ := l.Commands(ctx, "team-00000001"); len(all) != 3 {
		t.Fatalf("commands %v", all)
	}
	// A refused transition writes nothing: the conflict left no receipt.
	if all, _ := l.Commands(ctx, "team-00000001"); len(all) != 3 {
		t.Fatalf("commands %v", all)
	}
}

func TestRecoverConcludesOnce(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	newRun(t, l, "team-00000002")
	states := []string{AttemptReserved, AttemptLaunched, AttemptRunning, AttemptSubmitting, AttemptVerifying, AttemptSucceeded, AttemptFailed}
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		for i, st := range states {
			id := fmt.Sprintf("t%d", i)
			if err := tx.PutTask(TaskRow{RunID: "team-00000001", ID: id, Member: "lead", Kind: "work", State: TaskRunning}); err != nil {
				return err
			}
			if err := tx.PutAttempt(AttemptRow{RunID: "team-00000001", TaskID: id, N: 1, Member: "lead", State: st, ReservedAt: tx.Now()}); err != nil {
				return err
			}
		}
		for k, st := range []string{CheckStarted, CheckRunning, CheckFinished} {
			if err := tx.PutCheck(CheckRow{RunID: "team-00000001", TaskID: "t4", N: 1, ID: "unit", K: k, Argv: []string{"go", "test"}, State: st, StartedAt: tx.Now()}); err != nil {
				return err
			}
		}
		if _, _, err := tx.Command("alice", "c1", "task.retry", "fp"); err != nil {
			return err
		}
		if _, _, err := tx.Command("alice", "c2", "task.retry", "fp"); err != nil {
			return err
		}
		return tx.Settle("alice", "c2", CommandDone, nil)
	}); err != nil {
		t.Fatal(err)
	}
	// The second run was stopped: nothing to conclude there.
	if _, err := l.Transition(ctx, "team-00000002", func(tx *Tx) error {
		return tx.PutRun(RunRow{ID: "team-00000002", CrewID: "team", State: RunStopped, CreatedAt: tx.Now(), ClosedAt: tx.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := l.Recover(ctx)
	if err != nil || len(rec) != 1 || rec[0].RunID != "team-00000001" || !rec[0].Interrupted {
		t.Fatalf("%+v %v", rec, err)
	}
	if rec[0].Attempts[AttemptUncertain] != 1 || rec[0].Attempts[AttemptInterrupted] != 4 || rec[0].Checks != 2 || rec[0].Commands != 1 {
		t.Fatalf("%+v", rec[0])
	}
	attempts, _ := l.Attempts(ctx, "team-00000001")
	want := map[string]string{"t0": AttemptUncertain, "t1": AttemptInterrupted, "t2": AttemptInterrupted, "t3": AttemptInterrupted, "t4": AttemptInterrupted, "t5": AttemptSucceeded, "t6": AttemptFailed}
	for _, a := range attempts {
		if a.State != want[a.TaskID] || (a.State != AttemptSucceeded && a.State != AttemptFailed && a.ClosedAt.IsZero()) {
			t.Fatalf("attempt %s: %+v", a.TaskID, a)
		}
	}
	checks, _ := l.Checks(ctx, "team-00000001", "t4", 1, false)
	if len(checks) != 3 || checks[0].State != CheckInterrupted || checks[1].State != CheckInterrupted || checks[2].State != CheckFinished || checks[0].Argv[0] != "go" {
		t.Fatalf("checks %+v", checks)
	}
	if c, _, _ := l.Receipt(ctx, "team-00000001", "alice", "c1"); c.Status != CommandUncertain {
		t.Fatalf("c1 %+v", c)
	}
	if c, _, _ := l.Receipt(ctx, "team-00000001", "alice", "c2"); c.Status != CommandDone {
		t.Fatalf("c2 %+v", c)
	}
	r, _, _ := l.Run(ctx, "team-00000001")
	ev, _ := l.Events(ctx, "team-00000001", 0, 100)
	if r.State != RunInterrupted || len(ev) != 2 || ev[1].Kind != "recovered" || !strings.Contains(ev[1].Payload, `"Checks":2`) {
		t.Fatalf("run %+v events %+v", r, ev)
	}
	// Again: nothing open, nothing written.
	if rec, err := l.Recover(ctx); err != nil || len(rec) != 0 {
		t.Fatalf("second recover: %+v %v", rec, err)
	}
	if ev2, _ := l.Events(ctx, "team-00000001", 0, 100); len(ev2) != 2 {
		t.Fatalf("events after the second recover: %d", len(ev2))
	}
	if r2, _, _ := l.Run(ctx, "team-00000001"); r2.Revision != r.Revision {
		t.Fatalf("revision moved from %d to %d", r.Revision, r2.Revision)
	}
}

func TestPrune(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	close := func(id string) {
		if _, err := l.Transition(ctx, id, func(tx *Tx) error {
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
	newRun(t, l, "team-00000001")
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		if err := tx.PutTask(TaskRow{RunID: "team-00000001", ID: "build", Member: "lead", Kind: "work", State: TaskSucceeded}); err != nil {
			return err
		}
		if err := tx.PutAttempt(AttemptRow{RunID: "team-00000001", TaskID: "build", N: 1, Member: "lead", State: AttemptSucceeded, ReservedAt: tx.Now()}); err != nil {
			return err
		}
		if err := tx.PutCheck(CheckRow{RunID: "team-00000001", TaskID: "build", N: 1, ID: "unit", K: 1, Argv: []string{"true"}, State: CheckFinished, StartedAt: tx.Now(), Log: []byte("ok")}); err != nil {
			return err
		}
		_, _, err := tx.Command("alice", "c1", "task.retry", "fp")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Prune(ctx, "team-00000001"); !errors.Is(err, ErrNotPrunable) {
		t.Fatalf("an open run pruned: %v", err)
	}
	close("team-00000001")
	// A continuation names it: it stays.
	newRun(t, l, "team-00000002")
	if _, err := l.Transition(ctx, "team-00000002", func(tx *Tx) error {
		r, _, _ := tx.Run()
		r.ContinuedFrom = "team-00000001"
		return tx.PutRun(r)
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Prune(ctx, "team-00000001"); !errors.Is(err, ErrNotPrunable) {
		t.Fatalf("a cited run pruned: %v", err)
	}
	// Another run imports its result: it stays.
	if _, err := l.Transition(ctx, "team-00000002", func(tx *Tx) error {
		r, _, _ := tx.Run()
		r.ContinuedFrom = ""
		if err := tx.PutRun(r); err != nil {
			return err
		}
		return tx.PutTask(TaskRow{RunID: "team-00000002", ID: "build", Member: "lead", Kind: "work", State: TaskSucceeded, ImportedFrom: "team-00000001/build/1"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Prune(ctx, "team-00000001"); !errors.Is(err, ErrNotPrunable) {
		t.Fatalf("an imported run pruned: %v", err)
	}
	// Nothing cites it: its rows go, every table's.
	if _, err := l.Transition(ctx, "team-00000002", func(tx *Tx) error {
		return tx.PutTask(TaskRow{RunID: "team-00000002", ID: "build", Member: "lead", Kind: "work", State: TaskSucceeded})
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Prune(ctx, "team-00000001"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := l.Run(ctx, "team-00000001"); ok {
		t.Fatal("the run row stayed")
	}
	for table, want := range map[string]int{"tasks": 1, "attempts": 0, "checks": 0, "commands": 0, "events": 1} {
		var n int
		if err := l.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: %d rows (%v), want %d", table, n, err, want)
		}
	}
	if err := l.Prune(ctx, "team-00000001"); err != nil {
		t.Fatalf("pruning a run that is gone: %v", err)
	}
}

func TestBounds(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "runs"))
	ctx := context.Background()
	newRun(t, l, "team-00000001")
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		if err := tx.PutTask(TaskRow{RunID: "team-00000001", ID: "build", Member: "lead", Kind: "work", State: TaskRunning}); err != nil {
			return err
		}
		if err := tx.PutAttempt(AttemptRow{RunID: "team-00000001", TaskID: "build", N: 1, Member: "lead", State: AttemptVerifying, ReservedAt: tx.Now()}); err != nil {
			return err
		}
		return tx.PutCheck(CheckRow{RunID: "team-00000001", TaskID: "build", N: 1, ID: "unit", K: 1, State: CheckFinished, StartedAt: tx.Now(), Log: make([]byte, MaxLog+1)})
	}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a log past the bound: %v", err)
	}
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		return tx.Event("big", strings.Repeat("x", MaxPayload))
	}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a payload past the bound: %v", err)
	}
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		r, _, _ := tx.Run()
		r.Crew = strings.Repeat("x", MaxCrew+1)
		return tx.PutRun(r)
	}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a crew past the bound: %v", err)
	}
	if _, err := l.Transition(ctx, "team-00000001", func(tx *Tx) error {
		return tx.PutRun(RunRow{ID: "team-00000009", State: RunRunning, CreatedAt: tx.Now()})
	}); err == nil {
		t.Fatal("another run's row put in this run's transition")
	}
}
