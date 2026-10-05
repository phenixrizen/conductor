package crew

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/phenixrizen/conductor/internal/store"
)

// Records keeps runs that ended, one JSON document each under runs/ in the
// data directory, so the Crews page can chart a crew's last runs after the
// engine, or the server, has forgotten them: the run as GET /api/runs/{run}
// answers it (members, their times, states, diffs and agent sessions, the
// log), never a terminal's contents. At most maxRecords are kept; a save
// past that drops the oldest by start time.
type Records struct {
	st     *store.Store
	mu     sync.Mutex
	closed bool
}

// Close stops the records: a save after it is dropped (the server is
// shutting down, or a test's directories are going).
func (rs *Records) Close() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.closed = true
}

// maxRecords bounds the records kept.
const maxRecords = 500

// maxRecordSize bounds one record read back: a run with 200 log lines is
// well under it.
const maxRecordSize = 4 << 20

// NewRecords opens runs/ under st.
func NewRecords(st *store.Store) (*Records, error) {
	sub, err := st.Sub("runs")
	if err != nil {
		return nil, err
	}
	return &Records{st: sub}, nil
}

// Save writes r's record, replacing an earlier one of the same run (a
// stopped run that was reopened and ended again), and drops the oldest past
// maxRecords.
func (rs *Records) Save(r Run) error {
	if !ValidRunID(r.ID) {
		return fmt.Errorf("records: bad run id %q", r.ID)
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.closed {
		return nil
	}
	if err := rs.st.Save(r.ID+".json", r); err != nil {
		return err
	}
	return rs.prune()
}

// prune drops the oldest records past maxRecords. The caller holds rs.mu.
func (rs *Records) prune() error {
	entries, err := rs.st.List()
	if err != nil {
		return err
	}
	if len(entries) <= maxRecords {
		return nil
	}
	all, err := rs.load(entries)
	if err != nil {
		return err
	}
	// Oldest first by start time; a record that cannot be read goes first.
	slices.SortFunc(all, func(a, b recordFile) int {
		return cmp.Or(cmp.Compare(boolInt(a.ok), boolInt(b.ok)), a.run.StartedAt.Compare(b.run.StartedAt), cmp.Compare(a.name, b.name))
	})
	var errs []error
	for _, f := range all[:len(all)-maxRecords] {
		errs = append(errs, rs.st.Delete(f.name))
	}
	return errors.Join(errs...)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type recordFile struct {
	name string
	run  Run
	ok   bool
}

// load reads the records named, keeping one that cannot be read as not ok.
func (rs *Records) load(entries []store.Entry) ([]recordFile, error) {
	out := make([]recordFile, 0, len(entries))
	for _, en := range entries {
		var r Run
		ok, err := rs.st.LoadLimit(en.Name, &r, maxRecordSize)
		if err != nil || !ok || r.ID+".json" != en.Name {
			out = append(out, recordFile{name: en.Name})
			continue
		}
		out = append(out, recordFile{name: en.Name, run: r, ok: true})
	}
	return out, nil
}

// List returns the records, newest first by start time; those of one crew
// when crewID is not empty. A record that cannot be read is left out.
func (rs *Records) List(crewID string) ([]Run, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	entries, err := rs.st.List()
	if err != nil {
		return nil, err
	}
	files, err := rs.load(entries)
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(files))
	for _, f := range files {
		if f.ok && (crewID == "" || f.run.CrewID == crewID) {
			out = append(out, f.run)
		}
	}
	slices.SortFunc(out, func(a, b Run) int { return cmp.Or(b.StartedAt.Compare(a.StartedAt), strings.Compare(b.ID, a.ID)) })
	return out, nil
}

// Get returns one record.
func (rs *Records) Get(runID string) (Run, bool, error) {
	if !ValidRunID(runID) {
		return Run{}, false, nil
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var r Run
	ok, err := rs.st.LoadLimit(runID+".json", &r, maxRecordSize)
	if err != nil || !ok || r.ID != runID {
		return Run{}, false, err
	}
	return r, true, nil
}

// ValidRunID reports whether id has a run id's shape: a crew id, a dash and
// eight hex digits (newRunID).
func ValidRunID(id string) bool {
	i := strings.LastIndexByte(id, '-')
	if i <= 0 || len(id)-i-1 != 8 {
		return false
	}
	for _, c := range id[i+1:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return ValidID(id[:i])
}

// noteEnd tells OnEnd about r when it has ended: stopped, or every member
// ended with none pending (runState). It takes the engine's lock itself, so
// the caller must not hold it; OnEnd runs without it. A run being stopped is
// told about by its stop, once stopped: its members' sessions ending on the
// way do not make it finished. The ends reach OnEnd in the order their
// snapshots were taken (endMu).
func (e *Engine) noteEnd(runID string) {
	if e.OnEnd == nil {
		return
	}
	e.endMu.Lock()
	defer e.endMu.Unlock()
	e.mu.Lock()
	cur, ok := e.runs[runID]
	midStop := ok && cur.stopping && cur.stoppedAt == nil
	e.mu.Unlock()
	if !ok || midStop {
		return
	}
	r, ok := e.Get(runID)
	if !ok || (r.State != RunStopped && r.State != RunFinished) {
		return
	}
	e.OnEnd(r)
}

// RecordEnds wires OnEnd to rs: each run that ends is saved on a goroutine
// of its own, started through run (the server's tracked goroutines, which
// its shutdown waits for; a plain go when nil), and a save that fails is
// logged by warn.
//
// The saves run on goroutines of their own, so a later end of a run (a run
// reopened and ended again) could be saved before an earlier one: each end
// is numbered as OnEnd gets it (in the order noteEnd took the snapshots),
// and a save older than the one already written for its run is dropped.
func (e *Engine) RecordEnds(rs *Records, warn func(runID string, err error), run func(func())) {
	if run == nil {
		run = func(f func()) { go f() }
	}
	type pending struct {
		saved    uint64 // the newest end written
		inflight int    // ends not yet saved or dropped; the entry goes at zero
	}
	var (
		mu    sync.Mutex
		seq   uint64
		byRun = map[string]*pending{}
	)
	e.OnEnd = func(r Run) {
		mu.Lock()
		seq++
		n := seq
		p := byRun[r.ID]
		if p == nil {
			p = &pending{}
			byRun[r.ID] = p
		}
		p.inflight++
		mu.Unlock()
		run(func() {
			mu.Lock()
			defer mu.Unlock()
			if n > p.saved {
				if err := rs.Save(r); err != nil && warn != nil {
					warn(r.ID, err)
				}
				p.saved = n
			}
			if p.inflight--; p.inflight == 0 {
				delete(byRun, r.ID)
			}
		})
	}
}
