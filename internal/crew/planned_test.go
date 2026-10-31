package crew

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/crew/ledger"
)

// The ticket, plan revision and launch the tests' packages are of.
const (
	testTicketID = "0123456789abcdef"
	testPlanID   = "fedcba9876543210"
	testLaunchID = "00112233aabbccdd"
)

// testPackage is a launch package of run runID at base, encoded indented
// with a trailing newline: bytes no re-encoding would give, so a row that
// holds them was kept as given.
func testPackage(t *testing.T, runID, base string) []byte {
	t.Helper()
	p := Package{
		Version: PackageVersion,
		Ticket:  PackageTicket{ID: testTicketID, Key: "CND-12", Rev: 7, Title: "Fix the login loop", Acceptance: "It logs in.", Status: "open"},
		Plan: PackagePlan{ID: testPlanID, N: 2, Digest: strings.Repeat("d", 64), Crew: PackageCrew{ID: "api-sweep", Name: "API sweep"}, Base: base,
			Agents: []PackageAgent{{ID: "claude", SHA256: strings.Repeat("e", 64)}}},
		Attachments: []PackageAttachment{{ID: "a1", Name: "Spec.png", File: "01-Spec.png", Size: 3, SHA256: strings.Repeat("f", 64), ContentType: "image/png"}},
		Launch:      PackageLaunch{ID: testLaunchID, Run: runID, Label: "CND-12 Fix the login loop"},
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// planned is a planned launch of run runID at base with testPackage.
func planned(t *testing.T, runID, base string) Planned {
	t.Helper()
	pkg := testPackage(t, runID, base)
	return Planned{RunID: runID, Base: base, Digest: packageDigest(pkg), Package: pkg}
}

// launchPlanned launches c as p plans it and returns its run.
func launchPlanned(t *testing.T, e *Engine, c Crew, p Planned) Run {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, release, err := e.LaunchPlanned(ctx, c, "CND-12 Fix the login loop", p)
	release()
	if err != nil {
		t.Fatal(err)
	}
	return *run
}

// ledgerState is what the ledger holds of a run: its row, its events, its
// tasks and its attempts, compared whole.
type ledgerState struct {
	Row      ledger.RunRow
	Found    bool
	Events   []ledger.EventRow
	Tasks    []ledger.TaskRow
	Attempts []ledger.AttemptRow
}

func ledgerOf(t *testing.T, ld *ledger.Ledger, runID string) ledgerState {
	t.Helper()
	ctx := context.Background()
	var s ledgerState
	var err error
	if s.Row, s.Found, err = ld.Run(ctx, runID); err != nil {
		t.Fatal(err)
	}
	if s.Events, err = ld.Events(ctx, runID, 0, 10000); err != nil {
		t.Fatal(err)
	}
	if s.Tasks, err = ld.Tasks(ctx, runID); err != nil {
		t.Fatal(err)
	}
	if s.Attempts, err = ld.Attempts(ctx, runID); err != nil {
		t.Fatal(err)
	}
	return s
}

// otherRun writes, through the ledger itself (a handle other than the
// engine's), the row of a run that is not the engine's: another launch's.
func otherRun(t *testing.T, ld *ledger.Ledger, runID string) {
	t.Helper()
	if _, err := ld.Transition(context.Background(), runID, func(tx *ledger.Tx) error {
		if err := tx.CreateRun(ledger.RunRow{ID: runID, CrewID: "api-sweep", Crew: `{"id":"api-sweep","name":"another launch"}`, Base: strings.Repeat("c", 40),
			State: ledger.RunRunning, CreatedAt: tx.Now(), Ticket: "ffffffffffffffff", Package: `{"version":1,"another":"launch"}`}); err != nil {
			return err
		}
		return tx.Event("run.created", map[string]any{"by": "another launch"})
	}); err != nil {
		t.Fatal(err)
	}
}

// head is the commit HEAD names in dir.
func head(t *testing.T, dir string) string {
	t.Helper()
	return runGit(t, dir, "rev-parse", "HEAD")
}

// A planned run starts at the revision's base, whatever HEAD became since:
// its row and run.created say the base, its first attempt starts there,
// and run.created names the ticket and the package's digest. A base that
// is no commit of the repository is refused before a run exists.
func TestLaunchPlannedStartsAtTheBase(t *testing.T) {
	dir := newRepo(t)
	base := head(t, dir)
	writeFile(t, dir+"/README", "moved on\n")
	commitAll(t, dir, "after the revision")
	if head(t, dir) == base {
		t.Fatal("HEAD did not move")
	}
	e, ld := sqlEngine(t)
	c := repoCrew(dir, work("build", "lead"))
	p := planned(t, "api-sweep-0000000a", base)
	run := launchPlanned(t, e, c, p)
	if run.ID != p.RunID {
		t.Fatalf("the run is %s, not the planned %s", run.ID, p.RunID)
	}
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	if b.Attempt.Start != base {
		t.Fatalf("the first attempt starts at %s, not the revision's base %s", b.Attempt.Start, base)
	}
	got := ledgerOf(t, ld, run.ID)
	if got.Row.Base != base || got.Row.Ticket != testTicketID || got.Row.Package != string(p.Package) {
		t.Fatalf("the run's row: base %s ticket %s package %d bytes", got.Row.Base, got.Row.Ticket, len(got.Row.Package))
	}
	if len(got.Events) == 0 || got.Events[0].Kind != "run.created" {
		t.Fatalf("the run's first event: %+v", got.Events)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(got.Events[0].Payload), &created); err != nil {
		t.Fatal(err)
	}
	if created["base"] != base || created["ticket"] != "CND-12" || created["digest"] != p.Digest {
		t.Fatalf("run.created: %s", got.Events[0].Payload)
	}
	stopRun(t, e, run.ID)

	missing := planned(t, "api-sweep-0000000b", strings.Repeat("0", 40))
	if _, release, err := e.LaunchPlanned(t.Context(), c, "", missing); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not a commit") {
		release()
		t.Fatalf("a base not in the repository: %v", err)
	} else {
		release()
	}
	if s := ledgerOf(t, ld, missing.RunID); s.Found {
		t.Fatalf("a refused launch left a row: %+v", s.Row)
	}
}

// A requested run id is taken once: one the engine holds, one whose row
// the ledger has, and one whose row another ledger handle writes after the
// engine's own check (afterAdd) and before the run's first transaction are
// each refused ErrRunExists, before any attempt, the row there untouched,
// nothing of the refused run recorded or told as forgotten; an id not of
// the crew's shape is ErrInvalid.
func TestARequestedRunIDIsTakenOnce(t *testing.T) {
	dir := newRepo(t)
	base := head(t, dir)
	c := repoCrew(dir, work("build", "lead"))

	t.Run("held by the engine", func(t *testing.T) {
		e, ld := sqlEngine(t)
		fl := e.launcher.(*fakeLauncher)
		p := planned(t, "api-sweep-0000000a", base)
		first := launchPlanned(t, e, c, p)
		waitAttempt(t, e, first.ID, "build", ledger.AttemptRunning)
		launches := len(fl.launched())
		_, release, err := e.LaunchPlanned(t.Context(), c, "", p)
		release()
		if !errors.Is(err, ErrRunExists) {
			t.Fatalf("an id the engine holds: %v", err)
		}
		if n := len(fl.launched()); n != launches {
			t.Fatalf("the refused launch started %d sessions", n-launches)
		}
		if r, ok := e.Get(first.ID); !ok || !r.StartedAt.Equal(first.StartedAt) || r.Ticket == nil {
			t.Fatalf("the run held: %+v %v", r, ok)
		}
		if runs, _ := ld.Runs(t.Context()); len(runs) != 1 {
			t.Fatalf("the ledger has %d runs", len(runs))
		}
		stopRun(t, e, first.ID)
	})

	refused := func(t *testing.T, e *Engine, ld *ledger.Ledger, p Planned, before ledgerState, forgotten *[]string) {
		t.Helper()
		fl := e.launcher.(*fakeLauncher)
		_, release, err := e.LaunchPlanned(t.Context(), c, "", p)
		release()
		if !errors.Is(err, ErrRunExists) || !errors.Is(err, ledger.ErrRunExists) {
			t.Fatalf("an id the ledger has: %v", err)
		}
		if n := len(fl.launched()); n != 0 {
			t.Fatalf("the refused launch started %d sessions", n)
		}
		if _, ok := e.Get(p.RunID); ok {
			t.Fatal("the engine kept the refused run")
		}
		if after := ledgerOf(t, ld, p.RunID); !reflect.DeepEqual(after, before) {
			t.Fatalf("the row there changed:\n%+v\nwas\n%+v", after, before)
		}
		if len(*forgotten) != 0 {
			t.Fatalf("the refused run was told as forgotten: %v", *forgotten)
		}
	}
	recorded := func(t *testing.T, e *Engine) (*Records, *[]string) {
		rs, _ := newRecords(t)
		e.RecordEnds(rs, func(runID string, err error) { t.Errorf("%s: %v", runID, err) }, nil)
		var mu sync.Mutex
		forgotten := []string{}
		e.OnForget = func(r Run) {
			mu.Lock()
			forgotten = append(forgotten, r.ID)
			mu.Unlock()
		}
		return rs, &forgotten
	}

	t.Run("the ledger has its row", func(t *testing.T) {
		e, ld := sqlEngine(t)
		rs, forgotten := recorded(t, e)
		p := planned(t, "api-sweep-0000000b", base)
		otherRun(t, ld, p.RunID)
		refused(t, e, ld, p, ledgerOf(t, ld, p.RunID), forgotten)
		if !e.WaitRecorded(t.Context()) {
			t.Fatal("the records did not settle")
		}
		if _, ok, _ := rs.Get(p.RunID); ok {
			t.Fatal("the refused run's record was written")
		}
	})

	t.Run("written after the engine's check", func(t *testing.T) {
		e, ld := sqlEngine(t)
		rs, forgotten := recorded(t, e)
		p := planned(t, "api-sweep-0000000c", base)
		var before ledgerState
		e.afterAdd = func(runID string) {
			// The engine holds the run now and has checked its own map;
			// its first transaction is next.
			otherRun(t, ld, runID)
			before = ledgerOf(t, ld, runID)
		}
		_, release, err := e.LaunchPlanned(t.Context(), c, "", p)
		release()
		e.afterAdd = nil
		if !errors.Is(err, ErrRunExists) {
			t.Fatalf("a row written before the first transaction: %v", err)
		}
		if !before.Found {
			t.Fatal("afterAdd did not run")
		}
		if after := ledgerOf(t, ld, p.RunID); !reflect.DeepEqual(after, before) {
			t.Fatalf("the row there changed:\n%+v\nwas\n%+v", after, before)
		}
		if n := len(e.launcher.(*fakeLauncher).launched()); n != 0 {
			t.Fatalf("the refused launch started %d sessions", n)
		}
		if _, ok := e.Get(p.RunID); ok || len(*forgotten) != 0 {
			t.Fatalf("the refused run: held %v, told forgotten %v", ok, *forgotten)
		}
		if !e.WaitRecorded(t.Context()) {
			t.Fatal("the records did not settle")
		}
		if _, ok, _ := rs.Get(p.RunID); ok {
			t.Fatal("the refused run's record was written")
		}
		// The id refused, the same launch is still refused; another is
		// taken.
		refused(t, e, ld, p, before, forgotten)
		run := launchPlanned(t, e, c, planned(t, "api-sweep-0000000d", base))
		waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
		stopRun(t, e, run.ID)
	})

	t.Run("not the crew's", func(t *testing.T) {
		e, ld := sqlEngine(t)
		for _, id := range []string{"api-sweep-xyz", "api-sweep-0000000A", "api-sweep", "other-0000000a", "api-sweep-0000000a0"} {
			p := planned(t, "api-sweep-0000000e", base)
			p.RunID = id
			_, release, err := e.LaunchPlanned(t.Context(), c, "", p)
			release()
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("the id %q: %v", id, err)
			}
		}
		if runs, _ := ld.Runs(t.Context()); len(runs) != 0 {
			t.Fatalf("refused launches left %d runs", len(runs))
		}
	})
}

// The package is the one its digest names, as given: bytes whose SHA-256
// is not the digest, a package that is not the launch of the run, one of
// another version, one that is not JSON, one naming an attachment file not
// of the derived shape and one past the bound are each ErrInvalid with no
// run made; the package taken is kept byte for byte.
func TestThePackageIsCheckedAgainstItsDigest(t *testing.T) {
	dir := newRepo(t)
	base := head(t, dir)
	c := repoCrew(dir, work("build", "lead"))
	e, ld := sqlEngine(t)
	const id = "api-sweep-0000000a"
	edit := func(f func(*Package)) []byte {
		var p Package
		if err := json.Unmarshal(testPackage(t, id, base), &p); err != nil {
			t.Fatal(err)
		}
		f(&p)
		b, _ := json.Marshal(p)
		return b
	}
	good := planned(t, id, base)
	for _, tc := range []struct {
		name  string
		p     Planned
		words string
	}{
		{"another digest", Planned{RunID: id, Base: base, Digest: packageDigest([]byte("other")), Package: good.Package}, "SHA-256"},
		{"no digest", Planned{RunID: id, Base: base, Package: good.Package}, "SHA-256"},
		{"another run's launch", planned(t, "api-sweep-0000000b", base), "not of"},
		{"another version", withPackage(id, base, edit(func(p *Package) { p.Version = 2 })), "version 2"},
		{"not JSON", withPackage(id, base, []byte("{")), "cannot be read"},
		{"no ticket", withPackage(id, base, edit(func(p *Package) { p.Ticket.ID = "" })), "ticket.id"},
		{"a display name as a file", withPackage(id, base, edit(func(p *Package) { p.Attachments[0].File = "Spec.png" })), "derived name"},
		{"a file ending in a dot", withPackage(id, base, edit(func(p *Package) { p.Attachments[0].File = "01-spec." })), "derived name"},
		{"past the bound", withPackage(id, base, append(good.Package, []byte(strings.Repeat(" ", ledger.MaxPackage))...)), "more than"},
	} {
		if tc.name == "another run's launch" {
			tc.p.RunID = id
		}
		_, release, err := e.LaunchPlanned(t.Context(), c, "", tc.p)
		release()
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.words) {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if s := ledgerOf(t, ld, id); s.Found {
			t.Fatalf("%s: a row was made", tc.name)
		}
	}
	run := launchPlanned(t, e, c, good)
	if row := ledgerOf(t, ld, id).Row; row.Package != string(good.Package) || packageDigest([]byte(row.Package)) != good.Digest {
		t.Fatalf("the package as kept:\n%s\nas given:\n%s", row.Package, good.Package)
	}
	if run.Ticket == nil || run.Ticket.Digest != good.Digest {
		t.Fatalf("the run's ticket: %+v", run.Ticket)
	}
	stopRun(t, e, run.ID)
}

// withPackage is a planned launch of id at base with the package given and
// its own digest.
func withPackage(id, base string, pkg []byte) Planned {
	return Planned{RunID: id, Base: base, Digest: packageDigest(pkg), Package: pkg}
}

// A planned run's read, its place in the list, its JSON and its record say
// the ticket and plan revision it was launched from; so does the record
// rebuilt from the ledger when the run's own is missing. A run no ticket
// launched has none, and its JSON no ticket key.
func TestRunTicketOnTheRunAndItsRecord(t *testing.T) {
	dir := newRepo(t)
	base := head(t, dir)
	c := repoCrew(dir, work("build", "lead"))
	e, ld := sqlEngine(t)
	rs, _ := newRecords(t)
	e.RecordEnds(rs, func(runID string, err error) { t.Errorf("%s: %v", runID, err) }, nil)
	p := planned(t, "api-sweep-0000000a", base)
	run := launchPlanned(t, e, c, p)
	want := &RunTicket{ID: testTicketID, Key: "CND-12", Rev: 7, Plan: testPlanID, PlanN: 2, Launch: testLaunchID, Digest: p.Digest}
	if got, _ := e.Get(run.ID); !reflect.DeepEqual(got.Ticket, want) {
		t.Fatalf("the run's read: %+v", got.Ticket)
	}
	if list := e.List(); len(list) != 1 || !reflect.DeepEqual(list[0].Ticket, want) {
		t.Fatalf("the run in the list: %+v", list)
	}
	b, _ := json.Marshal(run)
	if wantJSON := `"ticket":{"id":"` + testTicketID + `","key":"CND-12","rev":7,"plan":"` + testPlanID + `","planN":2,"launch":"` + testLaunchID + `","digest":"` + p.Digest + `"}`; !strings.Contains(string(b), wantJSON) {
		t.Fatalf("the run's JSON has no %s: %s", wantJSON, b)
	}
	waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	stopRun(t, e, run.ID)
	if rec := waitRecord(t, rs, run.ID, RunStopped); !reflect.DeepEqual(rec.Ticket, want) {
		t.Fatalf("the run's record: %+v", rec.Ticket)
	}
	from, ok, err := SQLLedger(ld).Load(t.Context(), run.ID)
	if err != nil || !ok {
		t.Fatalf("load: %v %v", ok, err)
	}
	if rebuilt := Recovered(Run{}, from); !reflect.DeepEqual(rebuilt.Ticket, want) {
		t.Fatalf("the record rebuilt from the ledger: %+v", rebuilt.Ticket)
	}
	if rec, _, _ := rs.Get(run.ID); !reflect.DeepEqual(Recovered(rec, from).Ticket, want) {
		t.Fatalf("the record recovered: %+v", Recovered(rec, from).Ticket)
	}

	plain := launchManaged(t, e, c)
	if plain.Ticket != nil {
		t.Fatalf("a run no ticket launched: %+v", plain.Ticket)
	}
	if b, _ := json.Marshal(plain); strings.Contains(string(b), `"ticket"`) {
		t.Fatalf("a run no ticket launched has a ticket key: %s", b)
	}
	if row := ledgerOf(t, ld, plain.ID).Row; row.Ticket != "" || row.Package != "" {
		t.Fatalf("a run no ticket launched: its row %+v", row)
	}
	waitAttempt(t, e, plain.ID, "build", ledger.AttemptRunning)
	stopRun(t, e, plain.ID)
}

// A continuation of a planned run carries its package: its row's package
// and ticket are the source's byte for byte, and its read's ticket is the
// source's, the digest the same; so is a continuation of the continuation.
func TestAContinuationCarriesThePackage(t *testing.T) {
	dir := newRepo(t)
	base := head(t, dir)
	c := repoCrew(dir, work("build", "lead"))
	e, ld := sqlEngine(t)
	p := planned(t, "api-sweep-0000000a", base)
	run := launchPlanned(t, e, c, p)
	waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	stopRun(t, e, run.ID)
	src := ledgerOf(t, ld, run.ID).Row
	from := run.ID
	for range 2 {
		next, err := e.ContinueRun(t.Context(), from, "")
		if err != nil {
			t.Fatal(err)
		}
		row := ledgerOf(t, ld, next.ID).Row
		if row.Package != src.Package || row.Package != string(p.Package) || row.Ticket != src.Ticket || row.ContinuedFrom != from || row.Base != base {
			t.Fatalf("the continuation's row: ticket %s, continued from %s, base %s, package %d bytes", row.Ticket, row.ContinuedFrom, row.Base, len(row.Package))
		}
		if !reflect.DeepEqual(next.Ticket, run.Ticket) || next.Ticket.Digest != p.Digest {
			t.Fatalf("the continuation's ticket: %+v, the source's %+v", next.Ticket, run.Ticket)
		}
		waitAttempt(t, e, next.ID, "build", ledger.AttemptRunning)
		stopRun(t, e, next.ID)
		from = next.ID
	}
}

// forcedIDs is a newRunID seam that gives the ids listed, in turn, and the
// last of them from then on, counting the ids it gave.
type forcedIDs struct {
	mu    sync.Mutex
	ids   []string
	given int
}

func (f *forcedIDs) next(string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.given++
	id := f.ids[0]
	if len(f.ids) > 1 {
		f.ids = f.ids[1:]
	}
	return id
}

func (f *forcedIDs) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.given
}

// A planned run's row in the ledger and a new engine, as after a restart,
// that does not hold it: an ordinary launch and a continuation whose minted
// id is the planned run's each take another, and the planned row stays as
// it was, its crew, base, ticket and package with it. Given that id every
// time, each is refused ErrRunExists after three ids, no attempt made, the
// row untouched, and the continued run continued by nothing.
func TestAnOrdinaryLaunchNeverOverwritesAPlannedRun(t *testing.T) {
	dir := newRepo(t)
	base := head(t, dir)
	c := repoCrew(dir, work("build", "lead"))
	e1, ld := sqlEngine(t)
	p := planned(t, "api-sweep-0000000a", base)
	run := launchPlanned(t, e1, c, p)
	waitAttempt(t, e1, run.ID, "build", ledger.AttemptRunning)
	stopRun(t, e1, run.ID)
	before := ledgerOf(t, ld, run.ID)

	e2, fl2 := readyEngine(t)
	e2.UseLedger(SQLLedger(ld))
	if _, ok := e2.Get(run.ID); ok {
		t.Fatal("the new engine holds the planned run")
	}
	untouched := func(what string) {
		t.Helper()
		if after := ledgerOf(t, ld, run.ID); !reflect.DeepEqual(after, before) {
			t.Fatalf("%s changed the planned run's rows:\n%+v\nwas\n%+v", what, after, before)
		}
	}

	ids := &forcedIDs{ids: []string{run.ID, "api-sweep-0000000b"}}
	e2.newRunID = ids.next
	ordinary := launchManaged(t, e2, c)
	if ordinary.ID != "api-sweep-0000000b" || ids.count() != 2 {
		t.Fatalf("the ordinary launch took %s after %d ids", ordinary.ID, ids.count())
	}
	untouched("an ordinary launch")
	if row := ledgerOf(t, ld, ordinary.ID).Row; row.Ticket != "" || row.Package != "" || row.ContinuedFrom != "" {
		t.Fatalf("the ordinary run's row: %+v", row)
	}
	waitAttempt(t, e2, ordinary.ID, "build", ledger.AttemptRunning)
	stopRun(t, e2, ordinary.ID)

	ids = &forcedIDs{ids: []string{run.ID, "api-sweep-0000000c"}}
	e2.newRunID = ids.next
	next, err := e2.ContinueRun(t.Context(), ordinary.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != "api-sweep-0000000c" || ids.count() != 2 || next.ResumedFrom != ordinary.ID {
		t.Fatalf("the continuation took %s after %d ids, from %s", next.ID, ids.count(), next.ResumedFrom)
	}
	untouched("a continuation")
	if row := ledgerOf(t, ld, ordinary.ID).Row; row.ContinuedBy != next.ID {
		t.Fatalf("the continued run is continued by %q", row.ContinuedBy)
	}
	waitAttempt(t, e2, next.ID, "build", ledger.AttemptRunning)
	stopRun(t, e2, next.ID)

	// The planned run's id every time.
	launches := len(fl2.launched())
	ids = &forcedIDs{ids: []string{run.ID}}
	e2.newRunID = ids.next
	if _, release, err := e2.LaunchNamed(t.Context(), c, ""); !errors.Is(err, ErrRunExists) || ids.count() != maxMints || maxMints != 3 {
		release()
		t.Fatalf("an ordinary launch given the planned id every time: %v after %d ids", err, ids.count())
	} else {
		release()
	}
	untouched("a refused ordinary launch")
	ids = &forcedIDs{ids: []string{run.ID}}
	e2.newRunID = ids.next
	if _, err := e2.ContinueRun(t.Context(), next.ID, ""); !errors.Is(err, ErrRunExists) || ids.count() != maxMints {
		t.Fatalf("a continuation given the planned id every time: %v after %d ids", err, ids.count())
	}
	untouched("a refused continuation")
	if n := len(fl2.launched()); n != launches {
		t.Fatalf("the refused launch and continuation started %d sessions", n-launches)
	}
	if row := ledgerOf(t, ld, next.ID).Row; row.ContinuedBy != "" {
		t.Fatalf("the refused continuation marked its run continued by %q", row.ContinuedBy)
	}
	if _, ok := e2.Get(run.ID); ok {
		t.Fatal("the engine kept a refused run under the planned id")
	}
	// The refusal let go of the run: it is continued once the ids are its
	// own again.
	e2.newRunID = nil
	again, err := e2.ContinueRun(t.Context(), next.ID, "")
	if err != nil {
		t.Fatalf("the continuation after the refusal: %v", err)
	}
	waitAttempt(t, e2, again.ID, "build", ledger.AttemptRunning)
	stopRun(t, e2, again.ID)
	untouched("the continuation after the refusal")
}
