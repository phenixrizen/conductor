package crew

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/store"
)

func newRecords(t *testing.T) (*Records, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := NewRecords(st)
	if err != nil {
		t.Fatal(err)
	}
	// Closed before the directory goes (cleanups run last in, first out, and
	// TempDir's was registered first): a record write still in flight ends,
	// and a later one is a no-op, so the directory is empty when removed.
	t.Cleanup(rs.Close)
	return rs, filepath.Join(dir, "runs")
}

// waitRecord waits for the record of runID to be there with the state.
func waitRecord(t *testing.T, rs *Records, runID, state string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, ok, err := rs.Get(runID)
		if err != nil {
			t.Fatal(err)
		}
		if ok && r.State == state {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("no record of %s as %s (%v %+v)", runID, state, ok, r)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A run that is stopped, and one whose last member's session ends, are
// recorded as GET /api/runs/{run} answers them; a stop of a reopened run
// records it again.
func TestRunRecordIsWrittenWhenARunEnds(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	rs, dir := newRecords(t)
	e.RecordEnds(rs, func(runID string, err error) { t.Errorf("%s: %v", runID, err) }, nil)
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	waitStatus(t, e, run.ID, "core", MemberRunning, 5*time.Second)
	if _, ok, _ := rs.Get(run.ID); ok {
		t.Fatal("recorded before it ended")
	}
	stopRun(t, e, run.ID)
	rec := waitRecord(t, rs, run.ID, RunStopped)
	if rec.ID != run.ID || rec.CrewID != run.CrewID || len(rec.Members) != 2 || rec.StoppedAt == nil || !logged(rec, "stopped") {
		t.Fatalf("record %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(dir, run.ID+".json")); err != nil {
		t.Fatal(err)
	}

	// Finished: the last member's session ending ends the run.
	run2, err := e.Launch(t.Context(), testCrew(immediate("solo", "Do it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run2.ID, "solo", MemberRunning, 5*time.Second)
	l, p := fl.member("solo")
	p.End(0)
	<-l.Ended()
	fl.change(l.Info())
	rec2 := waitRecord(t, rs, run2.ID, RunFinished)
	if rec2.StoppedAt != nil || rec2.Members[0].Status != MemberEnded {
		t.Fatalf("record %+v", rec2)
	}
	if list, err := rs.List(run.CrewID); err != nil || len(list) != 2 || list[0].ID != run2.ID || list[1].ID != run.ID {
		t.Fatalf("list %v %v", list, err)
	}
	if list, _ := rs.List("other"); len(list) != 0 {
		t.Fatalf("another crew's list %v", list)
	}
}

// Past 500 records the oldest by start time go.
func TestRunRecordsAreBounded(t *testing.T) {
	rs, dir := newRecords(t)
	base := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	for i := range maxRecords + 2 {
		r := Run{ID: fmt.Sprintf("team-%08x", i), CrewID: "team", Name: "team", StartedAt: base.Add(time.Duration(i) * time.Minute), State: RunFinished, Members: []MemberState{}, Log: []session.ActivityEntry{}}
		if err := rs.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != maxRecords {
		t.Fatalf("%d records kept", len(entries))
	}
	for _, i := range []int{0, 1} {
		if _, ok, _ := rs.Get(fmt.Sprintf("team-%08x", i)); ok {
			t.Fatalf("record %d, the oldest, kept", i)
		}
	}
	if _, ok, _ := rs.Get("team-00000002"); !ok {
		t.Fatal("record 2 gone")
	}
	list, err := rs.List("")
	if err != nil || len(list) != maxRecords || list[0].ID != fmt.Sprintf("team-%08x", maxRecords+1) {
		t.Fatalf("list %d %v", len(list), err)
	}
	// Bad names are refused; a foreign file is left out.
	if err := rs.Save(Run{ID: "../x"}); err == nil {
		t.Fatal("a bad id was saved")
	}
	for _, id := range []string{"team-1234567", "team-1234567g", "Team-12345678", "-12345678", ""} {
		if ValidRunID(id) {
			t.Errorf("%q passes as a run id", id)
		}
	}
}

// A record holds the run as the API answers it and nothing else: no field
// beyond the Run shape, so never a terminal's output.
func TestRunRecordsNeverHoldTerminalOutput(t *testing.T) {
	rs, dir := newRecords(t)
	r := Run{ID: "team-0badf00d", CrewID: "team", Name: "team", StartedAt: time.Now().UTC(), State: RunStopped, Yolo: true,
		Members: []MemberState{{Name: "lead", AgentID: "claude", SessionID: "s1", Status: MemberEnded}},
		Log:     []session.ActivityEntry{{At: time.Now().UTC(), Type: session.ActivityStatus, Message: "stopped"}}}
	if err := rs.Save(r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "team-0badf00d.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	rt := reflect.TypeOf(Run{})
	for i := range rt.NumField() {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			want[tag] = true
		}
	}
	for k := range doc {
		if !want[k] {
			t.Errorf("record holds %q, which the Run shape does not", k)
		}
	}
	for _, word := range []string{"output", "scrollback", "screen", "\\u001b"} {
		if strings.Contains(string(raw), word) {
			t.Errorf("record holds %q", word)
		}
	}
	got, ok, err := rs.Get("team-0badf00d")
	if err != nil || !ok || got.ID != r.ID || got.Members[0].SessionID != "s1" || len(got.Log) != 1 {
		t.Fatalf("read back %v %v %+v", ok, err, got)
	}
}

// A run being stopped is recorded by its stop: its members' sessions ending
// on the way never record it as finished first (that record, saved on a
// goroutine of its own, could land after the stop's and win).
func TestAStoppedRunIsNeverRecordedAsFinished(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	var mu sync.Mutex
	var ends []string
	e.OnEnd = func(r Run) {
		mu.Lock()
		ends = append(ends, r.ID+" "+r.State)
		mu.Unlock()
	}
	for range 25 {
		run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it.")))
		if err != nil {
			t.Fatal(err)
		}
		waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
		waitStatus(t, e, run.ID, "core", MemberRunning, 5*time.Second)
		stopRun(t, e, run.ID)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, end := range ends {
		if strings.HasSuffix(end, " "+RunFinished) {
			t.Fatalf("a stopped run was told about as finished: %v", ends)
		}
	}
	if len(ends) < 25 {
		t.Fatalf("%d ends told for 25 stops: %v", len(ends), ends)
	}
}

// The ends of one run are saved in the order they came, whatever order
// their goroutines run in: a late save of an older end is dropped.
func TestAnOlderEndNeverOverwritesANewerRecord(t *testing.T) {
	e, _ := newEngine(t)
	rs, _ := newRecords(t)
	var queued []func()
	e.RecordEnds(rs, func(runID string, err error) { t.Errorf("%s: %v", runID, err) }, func(f func()) { queued = append(queued, f) })
	base := Run{ID: "team-0000000a", CrewID: "team", Name: "team", StartedAt: time.Now().UTC(), Members: []MemberState{}, Log: []session.ActivityEntry{}}
	finished, stopped := base, base
	finished.State = RunFinished
	stopped.State = RunStopped
	e.OnEnd(finished)
	e.OnEnd(stopped)
	// The newer first, then the older.
	queued[1]()
	queued[0]()
	if r, ok, err := rs.Get(base.ID); err != nil || !ok || r.State != RunStopped {
		t.Fatalf("record %+v %v %v", r, ok, err)
	}
	// The run ended again later: that end is saved.
	e.OnEnd(finished)
	queued[2]()
	if r, _, _ := rs.Get(base.ID); r.State != RunFinished {
		t.Fatalf("the later end was dropped: %+v", r)
	}
}
