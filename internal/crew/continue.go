package crew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
	"github.com/phenixrizen/conductor/internal/session"
)

// A continuation (docs/plans/agent-harness-order.md, decision 9; row 8 of
// #334): a managed run the server no longer runs (interrupted by the
// server's end, stopped, or finished) goes on as a new run of the same
// crew as launched, from the same base. The results that stood are
// imported by reference, the attempts spent are counted against their
// tasks, a result that was being verified (when the server ended, or the
// run was stopped) is verified again without a session, and the rest is
// dispatched as new. An attempt whose launch was
// never known (uncertain) waits for a person: its task fails with why, and
// a retry allows one more attempt. Nothing is retried on its own.

// Loaded is a run as the ledger has it, for a continuation and for the
// record written after a recovery.
type Loaded struct {
	Run      ledger.RunRow
	Tasks    []ledger.TaskRow
	Attempts []ledger.AttemptRow
	Messages []ledger.MessageRow
}

// candidate is a result a continuation verifies again: the interrupted
// attempt's number, commit and start, and what it submitted.
type candidate struct {
	n      int
	commit string
	start  string
	result Result
}

// ContinueRun makes the run that continues runID, a managed run the ledger
// has: interrupted, stopped or finished, and not continued yet. The new run
// names the old in ResumedFrom (the ledger's ContinuedFrom) and the old
// names it in ResumedBy (ContinuedBy). A run still going is refused
// (ErrRunRunning), one continued already with RunResumedError. The label
// is the old run's when none is given and the engine still holds it.
func (e *Engine) ContinueRun(ctx context.Context, runID, label string) (*Run, error) {
	if e.ledger == nil {
		return nil, ErrNoLedger
	}
	if err := ValidateLabel(label); err != nil {
		return nil, err
	}
	e.mu.Lock()
	old := e.runs[runID]
	if old != nil {
		switch {
		case !old.managed:
			e.mu.Unlock()
			return nil, fmt.Errorf("%w: a legacy run is resumed, not continued", ErrTaskState)
		case old.stopping && !old.stopDone:
			e.mu.Unlock()
			return nil, ErrRunStopped
		case old.stoppedAt == nil && old.outcome == "":
			e.mu.Unlock()
			return nil, ErrRunRunning
		case old.resumedBy != "":
			e.mu.Unlock()
			return nil, &RunResumedError{By: old.resumedBy}
		case old.resuming:
			e.mu.Unlock()
			return nil, &RunResumedError{}
		}
		old.resuming = true
		if label == "" {
			label = old.label
		}
	}
	e.mu.Unlock()
	undo := func() {
		if old != nil {
			e.mu.Lock()
			old.resuming = false
			e.mu.Unlock()
		}
	}
	from, ok, err := e.ledger.Load(ctx, runID)
	if err != nil {
		undo()
		return nil, err
	}
	if !ok {
		undo()
		return nil, ErrRunNotFound
	}
	if from.Run.ContinuedBy != "" {
		undo()
		return nil, &RunResumedError{By: from.Run.ContinuedBy}
	}
	if from.Run.State == ledger.RunRunning {
		undo()
		return nil, ErrRunRunning
	}
	var c Crew
	if err := json.Unmarshal([]byte(from.Run.Crew), &c); err != nil {
		undo()
		return nil, fmt.Errorf("the run's crew as launched cannot be read: %w", err)
	}
	if !c.Managed() {
		undo()
		return nil, fmt.Errorf("%w: the run's crew as launched has no tasks", ErrTaskState)
	}
	prefix, err := e.prepare(ctx, c)
	if err != nil {
		undo()
		return nil, err
	}
	r := e.add(c, prefix, label)
	defer e.launched(r)
	if e.afterAdd != nil {
		e.afterAdd(r.id)
	}
	if err := e.startContinued(ctx, r, from, old); err != nil {
		e.abort(r)
		undo()
		return nil, err
	}
	out, ok := e.Get(r.id)
	if !ok {
		return nil, ErrRunNotFound
	}
	return &out, nil
}

// startContinued sets r up from the run it continues and records it: the
// base and the pause carried, every task as its row and its last attempt
// say (imported, failed, cancelled, verified again, waiting for a person,
// or new), the old run's row naming the new; then the ready tasks are
// dispatched.
func (e *Engine) startContinued(ctx context.Context, r *run, from Loaded, old *run) error {
	rows := map[string]ledger.TaskRow{}
	for _, row := range from.Tasks {
		rows[row.ID] = row
	}
	last := map[string]ledger.AttemptRow{}
	for _, a := range from.Attempts {
		if a.N > last[a.TaskID].N {
			last[a.TaskID] = a
		}
	}
	// The results imported by reference: from this run's rows, or from the
	// run an earlier continuation imported them from (the reference is
	// kept as it was). Read before the transaction: the ledger's one
	// connection serves no reader inside one.
	sources := map[string]Loaded{from.Run.ID: from}
	source := func(ref string) (ledger.AttemptRow, bool) {
		runID, taskID, n, ok := parseImport(ref)
		if !ok {
			return ledger.AttemptRow{}, false
		}
		src, ok := sources[runID]
		if !ok {
			loaded, found, err := e.ledger.Load(ctx, runID)
			if err != nil || !found {
				return ledger.AttemptRow{}, false
			}
			sources[runID] = loaded
			src = loaded
		}
		row := byN(src.Attempts, taskID, n)
		return row, row.N == n
	}
	refs := map[string]ledger.AttemptRow{}
	for _, row := range from.Tasks {
		if row.State != ledger.TaskSucceeded || row.ResultAttempt == 0 {
			continue
		}
		ref := row.ImportedFrom
		if ref == "" {
			ref = from.Run.ID + "/" + row.ID + "/" + strconv.Itoa(row.ResultAttempt)
		}
		if ra, ok := source(ref); ok {
			refs[row.ID] = ra
		}
	}
	var imported, again, uncertain, held int
	err := e.transact(ctx, r, func() tx {
		r.base, r.paused, r.resumedFrom = from.Run.Base, from.Run.Paused, from.Run.ID
		r.markRun()
		for _, row := range from.Messages {
			m := &message{seq: row.Seq, from: row.From, to: row.To, text: row.Text, createdAt: row.CreatedAt, typingAt: row.TypingAt, deliveredAt: row.DeliveredAt, ackedAt: row.AckedAt}
			if !row.TypingAt.IsZero() && row.DeliveredAt.IsZero() {
				// Set out to be typed and not entered before the server
				// ended: whether it reached the terminal cannot be told.
				m.uncertain = true
				held++
				r.note(session.ActivityStatus, "message %d from %s to %s may have been typed before the server ended: not typed again, in %s's inbox", m.seq, m.from, m.to, m.to)
			}
			r.messages = append(r.messages, m)
		}
		for _, t := range r.tasks {
			row, ok := rows[t.def.ID]
			if !ok {
				r.markTask(t)
				continue
			}
			t.prior, t.extra = row.Attempts, row.Extra
			if rp := decodeRepair(row.Repair); rp != nil {
				t.repair = rp
			}
			la, hasLast := last[t.def.ID]
			if hasLast && la.Checkpoint != "" && t.def.Kind == TaskWork {
				t.checkpoint = decodeCheckpoint(la.Checkpoint)
			}
			switch row.State {
			case ledger.TaskSucceeded:
				ra := refs[t.def.ID]
				res := decodeResult(ra.Result)
				at := ra.ClosedAt
				if at.IsZero() {
					at = time.Now().UTC()
				}
				t.result = &taskResult{attempt: row.ResultAttempt, commit: row.ResultCommit, summary: res.Summary, issues: res.Issues, verdict: res.Verdict, findings: res.Findings, at: at, branch: ra.Branch}
				if t.def.Kind == TaskReview {
					// The review's candidate, the source attempt's start: a
					// dependent starts there in the continuation too (#351).
					t.result.reviewed = ra.StartCommit
				}
				if t.result.verdict == "" {
					t.result.verdict = row.Verdict
				}
				t.imported = row.ImportedFrom
				if t.imported == "" {
					t.imported = from.Run.ID + "/" + t.def.ID + "/" + strconv.Itoa(row.ResultAttempt)
				}
				t.state, t.reason = ledger.TaskSucceeded, ""
				imported++
			case ledger.TaskFailed:
				t.state, t.reason = ledger.TaskFailed, row.Reason
			case ledger.TaskCancelled:
				t.state, t.reason = ledger.TaskCancelled, "cancelled"
			default:
				switch {
				case hasLast && la.State == ledger.AttemptUncertain:
					t.state, t.reason = ledger.TaskFailed, fmt.Sprintf("attempt %d's launch is uncertain (the server stopped before it was known): retry it to go on", la.N)
					uncertain++
				case hasLast && (la.State == ledger.AttemptInterrupted || la.State == ledger.AttemptStopped) && t.def.Kind == TaskWork && la.ResultCommit != "" && la.Result != "" && strings.HasPrefix(la.Stage, "verifying"):
					// Its result was being verified when the server ended or
					// the run was stopped: verified again, the work kept.
					t.candidate = &candidate{n: la.N, commit: la.ResultCommit, start: la.StartCommit, result: decodeResult(la.Result)}
					t.state, t.reason = ledger.TaskReady, fmt.Sprintf("attempt %d's result %s is verified again", la.N, short(la.ResultCommit))
					again++
				default:
					t.state, t.reason = ledger.TaskBlocked, ""
					if len(t.def.Needs) == 0 {
						t.state = ledger.TaskReady
					}
				}
			}
			r.markTask(t)
		}
		r.ready()
		e.settleRun(r)
		r.note(session.ActivityStatus, "continues %s: %d results kept, %d verified again, %d waiting for a decision, %d messages carried", from.Run.ID, imported, again, uncertain, len(r.messages))
		w := ev("run.continued", map[string]any{"from": from.Run.ID, "base": r.base, "imported": imported, "reverified": again, "uncertain": uncertain, "messages": len(r.messages), "held": held})
		w.more = func(ltx LedgerTx) error {
			if err := ltx.Continued(from.Run.ID); err != nil {
				return err
			}
			// The messages carried, under this run: a later continuation
			// reads them here.
			for _, m := range r.messages {
				if err := ltx.PutMessage(r.messageRow(m)); err != nil {
					return err
				}
			}
			return nil
		}
		return w
	}, giveUp)
	if errors.Is(err, ledger.ErrContinued) {
		// Continued meanwhile, by another server or request: the one that
		// was is the continuation.
		by := ""
		if again, ok, lerr := e.ledger.Load(ctx, from.Run.ID); lerr == nil && ok {
			by = again.Run.ContinuedBy
		}
		return &RunResumedError{By: by}
	}
	if err != nil {
		return err
	}
	if old != nil {
		e.mu.Lock()
		old.resumedBy = r.id
		old.resuming = false
		old.note(session.ActivityStatus, "continued as %s", r.id)
		old.touch()
		e.mu.Unlock()
	}
	e.pumpLocked(r)
	return nil
}

// parseImport reads an import reference, "<run>/<task>/<n>".
func parseImport(ref string) (runID, taskID string, n int, ok bool) {
	parts := strings.Split(ref, "/")
	if len(parts) != 3 {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil || n < 1 || !ValidRunID(parts[0]) || !ValidTaskID(parts[1]) {
		return "", "", 0, false
	}
	return parts[0], parts[1], n, true
}

// decodeRepair reads a task row's repair; nil for an empty or unreadable
// one.
func decodeRepair(raw string) *repair {
	var rp TaskRepair
	if raw == "" || json.Unmarshal([]byte(raw), &rp) != nil {
		return nil
	}
	return &repair{review: rp.Review, attempt: rp.Attempt, commit: rp.Commit, summary: rp.Summary, findings: rp.Findings}
}

// byN is the attempt row of the task numbered n, or a zero row.
func byN(attempts []ledger.AttemptRow, taskID string, n int) ledger.AttemptRow {
	for _, a := range attempts {
		if a.TaskID == taskID && a.N == n {
			return a
		}
	}
	return ledger.AttemptRow{}
}

// decodeResult reads an attempt row's result; an empty or unreadable one
// is empty.
func decodeResult(raw string) Result {
	var res Result
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &res)
	}
	return res
}

// reverify makes the attempt that verifies t's candidate again: no
// session, the candidate's commit its result and the start of the work
// its start (where a next attempt goes back to when the candidate is
// refused before its checks, refusedBeforeChecks), not counted against
// the task; the verification follows its record
// (dispatchCandidate). The caller holds e.mu, in the pump.
func (e *Engine) reverify(r *run, t *task, m *member) {
	c := *t.candidate
	t.candidate = nil
	ctx, cancel := context.WithCancel(r.ctx)
	n := len(t.attempts) + t.prior + 1
	start := c.start
	if start == "" {
		start = c.commit
	}
	a := &attempt{t: t, m: m, n: n, state: ledger.AttemptReserved, ctx: ctx, cancel: cancel, reservedAt: time.Now().UTC(), inputs: r.inputs(t), start: start, provisional: true,
		worktree: filepath.Join(r.cwd, worktreesDir, r.id, fmt.Sprintf("%s.%d", t.def.ID, n))}
	t.attempts = append(t.attempts, a)
	t.extra++
	t.state, t.reason = ledger.TaskRunning, ""
	r.active++
	r.markTask(t)
	r.markAttempt(a)
	r.starts.Add(1)
	r.note(session.ActivityStatus, "task %s: attempt %d verifies again the result %s of attempt %d", t.def.ID, n, short(c.commit), c.n)
	go e.dispatchCandidate(ctx, r, a, c)
}

// dispatchCandidate records a as verifying the candidate's result, then
// verifies it (verify), as a submitted result is after its record.
func (e *Engine) dispatchCandidate(ctx context.Context, r *run, a *attempt, c candidate) {
	defer r.starts.Done()
	t := a.t
	res := c.result
	err := e.transact(ctx, r, func() tx {
		if !a.closedAt.IsZero() {
			return tx{}
		}
		a.provisional = false
		a.launchedAt = time.Now().UTC()
		a.result, a.submitted, a.state = c.commit, &res, ledger.AttemptVerifying
		t.state = ledger.TaskVerifying
		r.markAttempt(a)
		r.markTask(t)
		return ev("result.reverified", map[string]any{"task": t.def.ID, "attempt": a.n, "commit": c.commit, "of": c.n})
	}, func() {
		a.provisional, a.result, a.submitted, a.state = true, "", nil, ledger.AttemptReserved
		t.state = ledger.TaskRunning
	})
	if err != nil {
		_ = e.transact(context.Background(), r, func() tx {
			r.unreserve(a, "the ledger could not record the attempt: "+session.CleanMessage(err.Error()))
			return pausedTx(e.pauseDispatch(r, err), err)
		}, nil)
		return
	}
	e.mu.Lock()
	closed := !a.closedAt.IsZero() || r.stopping
	if !closed {
		r.checks.Add(1)
	}
	e.mu.Unlock()
	if closed {
		return // cancelled, or the stop closes it
	}
	e.verify(r, a)
}

// Recovered is rec, a managed run's record, as the ledger has the run:
// the run's state (interrupted when the recovery found it running), each
// task as its row and its last attempt say (an attempt interrupted or
// uncertain leaves its task ready or failed with why), the members'
// sessions ended with the server. The server writes it in place of the
// record the run last saved, which may lag the ledger (a snapshot cut
// short by the server's end) or be missing (rec empty): then the run is
// built from the crew as launched, its log empty.
func Recovered(rec Run, from Loaded) Run {
	if rec.ID == "" {
		rec = Run{ID: from.Run.ID, CrewID: from.Run.CrewID, StartedAt: from.Run.CreatedAt, State: RunRunning}
		var c Crew
		if err := json.Unmarshal([]byte(from.Run.Crew), &c); err == nil {
			rec.Name, rec.Goal, rec.Cwd, rec.Isolation, rec.Yolo = c.Name, c.Goal, c.Cwd, c.Isolation, c.Yolo != nil && *c.Yolo
			for _, m := range c.Members {
				rec.Members = append(rec.Members, MemberState{Name: m.Name, AgentID: m.AgentID, Start: m.Start, Status: MemberPending})
			}
			for _, t := range c.Tasks {
				rec.Tasks = append(rec.Tasks, TaskState{ID: t.ID, Member: t.Member, Kind: t.Kind, Needs: slices.Clone(t.Needs), State: ledger.TaskBlocked})
			}
		}
		if !from.Run.ClosedAt.IsZero() && from.Run.State == ledger.RunStopped {
			at := from.Run.ClosedAt
			rec.StoppedAt = &at
		}
	}
	rec.ResumedBy = from.Run.ContinuedBy
	if from.Run.ContinuedFrom != "" {
		rec.ResumedFrom = from.Run.ContinuedFrom
	}
	switch from.Run.State {
	case ledger.RunInterrupted:
		rec.State = RunInterrupted
	case ledger.RunStopped:
		rec.State = RunStopped
	case ledger.RunFinished:
		rec.State = RunFinished
	}
	rec.Managed = &Managed{Revision: from.Run.Revision}
	rec.Paused, rec.Outcome, rec.NeedsInput = from.Run.Paused, from.Run.Outcome, 0
	rows := map[string]ledger.TaskRow{}
	for _, row := range from.Tasks {
		rows[row.ID] = row
	}
	last := map[string]ledger.AttemptRow{}
	for _, a := range from.Attempts {
		if a.N > last[a.TaskID].N {
			last[a.TaskID] = a
		}
	}
	for i := range rec.Tasks {
		t := &rec.Tasks[i]
		if row, ok := rows[t.ID]; ok {
			t.State, t.Reason, t.Attempts, t.ImportedFrom = row.State, row.Reason, row.Attempts, row.ImportedFrom
			switch {
			case row.ResultAttempt == 0:
				// Withdrawn, or never made: the snapshot's result goes.
				t.Result = nil
			case t.Result == nil || t.Result.Attempt != row.ResultAttempt:
				res := decodeResult(byN(from.Attempts, t.ID, row.ResultAttempt).Result)
				t.Result = &ResultState{Attempt: row.ResultAttempt, Commit: row.ResultCommit, Summary: res.Summary, Issues: res.Issues, Verdict: row.Verdict, Findings: res.Findings, At: byN(from.Attempts, t.ID, row.ResultAttempt).ClosedAt}
			}
		}
		la, ok := last[t.ID]
		if !ok {
			continue
		}
		as := AttemptState{N: la.N, SessionID: la.SessionID, Worktree: la.Worktree, Branch: la.Branch, Start: la.StartCommit, Result: la.ResultCommit, State: la.State, Stage: la.Stage, Error: la.Error}
		if !la.LaunchedAt.IsZero() {
			at := la.LaunchedAt
			as.StartedAt = &at
		}
		if !la.ClosedAt.IsZero() {
			at := la.ClosedAt
			as.EndedAt = &at
		}
		t.Attempt = &as
		switch {
		case la.State == ledger.AttemptUncertain && (t.State == ledger.TaskRunning || t.State == ledger.TaskVerifying):
			t.State, t.Reason = ledger.TaskFailed, fmt.Sprintf("attempt %d's launch is uncertain (the server stopped before it was known): retry it once the run is continued", la.N)
		case la.State == ledger.AttemptInterrupted && (t.State == ledger.TaskRunning || t.State == ledger.TaskVerifying):
			t.State, t.Reason = ledger.TaskReady, fmt.Sprintf("attempt %d was interrupted by the server's end: the run continues it", la.N)
		}
	}
	now := time.Now().UTC()
	for i := range rec.Members {
		m := &rec.Members[i]
		if m.Status == MemberRunning || m.Status == MemberStarting {
			m.Status, m.NeedsInput = MemberEnded, false
			if m.Ended == nil {
				m.Ended = &now
			}
		}
	}
	return rec
}
