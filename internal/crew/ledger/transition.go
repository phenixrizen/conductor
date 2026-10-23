package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors a transition can end in.
var (
	// ErrCommandConflict says a command id was reused with other content.
	ErrCommandConflict = errors.New("ledger: command_conflict")
	// ErrTooManyCommands says the run has maxCommands receipts already.
	ErrTooManyCommands = errors.New("ledger: too_many_commands")
	// ErrTooLarge says a payload, a log or a crew is past its bound.
	ErrTooLarge = errors.New("ledger: too large")
	// ErrNoRun says the run has no row.
	ErrNoRun = errors.New("ledger: no such run")
)

// Tx is one transition: the rows it puts, the events it records and the
// command it answers are committed together, or not at all, and the run's
// revision is bumped once with them.
type Tx struct {
	ctx   context.Context
	tx    *sql.Tx
	runID string
	now   time.Time
}

// Now is the transaction's time, one for all it writes.
func (t *Tx) Now() time.Time { return t.now }

// Run reads the run's row within the transaction. The ledger's readers
// (Run, Runs, Tasks…) must not be called inside a transition: there is one
// connection, and it is the transaction's until it commits.
func (t *Tx) Run() (RunRow, bool, error) {
	row := t.tx.QueryRowContext(t.ctx, `SELECT id, crew_id, crew, base, state, outcome, paused, revision, continued_from, continued_by, created_at, closed_at FROM runs WHERE id = ?`, t.runID)
	var r RunRow
	var paused int
	var created, closed string
	switch err := row.Scan(&r.ID, &r.CrewID, &r.Crew, &r.Base, &r.State, &r.Outcome, &paused, &r.Revision, &r.ContinuedFrom, &r.ContinuedBy, &created, &closed); {
	case errors.Is(err, sql.ErrNoRows):
		return RunRow{}, false, nil
	case err != nil:
		return RunRow{}, false, err
	}
	r.Paused, r.CreatedAt, r.ClosedAt = paused != 0, unstamp(created), unstamp(closed)
	return r, true, nil
}

// Transition runs fn in one transaction on the run with the given ID and,
// when fn returns nil, bumps the run's revision and commits; the new
// revision is returned. The run's row must exist by the end (fn may put
// it). fn's error, or the commit's, undoes everything. ErrReadOnly when the
// ledger takes no writes.
func (l *Ledger) Transition(ctx context.Context, runID string, fn func(*Tx) error) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.readOnly {
		return 0, fmt.Errorf("%w: %s", ErrReadOnly, l.why)
	}
	if l.closed {
		return 0, ErrClosed
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	t := &Tx{ctx: ctx, tx: tx, runID: runID, now: time.Now().UTC()}
	if err := fn(t); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, "UPDATE runs SET revision = revision + 1 WHERE id = ?", runID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrNoRun
	}
	var rev int64
	if err := tx.QueryRowContext(ctx, "SELECT revision FROM runs WHERE id = ?", runID).Scan(&rev); err != nil {
		return 0, err
	}
	if l.beforeCommit != nil {
		if err := l.beforeCommit(); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rev, nil
}

// Event records an event of the run, next in its sequence, with payload as
// JSON (at most MaxPayload bytes).
func (t *Tx) Event(kind string, payload any) error {
	body, err := encode(payload, MaxPayload)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO events (run_id, seq, at, kind, payload)
		VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE run_id = ?), ?, ?, ?)`,
		t.runID, t.runID, stamp(t.now), kind, body)
	return err
}

// Command answers a command: the receipt it has already, when actor sent
// the same id with the same content before (found); ErrCommandConflict
// when with other content; else a new receipt, accepted, which Settle
// finishes. ErrTooManyCommands past the run's bound.
func (t *Tx) Command(actor, id, intent, fingerprint string) (prior CommandRow, found bool, err error) {
	row := t.tx.QueryRowContext(t.ctx, `SELECT intent, fingerprint, status, receipt, created_at FROM commands
		WHERE run_id = ? AND actor = ? AND id = ?`, t.runID, actor, id)
	var at string
	prior = CommandRow{RunID: t.runID, Actor: actor, ID: id}
	switch err := row.Scan(&prior.Intent, &prior.Fingerprint, &prior.Status, &prior.Receipt, &at); {
	case err == nil:
		prior.CreatedAt = unstamp(at)
		if prior.Fingerprint != fingerprint || prior.Intent != intent {
			return CommandRow{}, false, ErrCommandConflict
		}
		return prior, true, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return CommandRow{}, false, err
	}
	var n int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM commands WHERE run_id = ?", t.runID).Scan(&n); err != nil {
		return CommandRow{}, false, err
	}
	if n >= maxCommands {
		return CommandRow{}, false, ErrTooManyCommands
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO commands (run_id, actor, id, intent, fingerprint, status, receipt, created_at)
		VALUES (?, ?, ?, ?, ?, ?, '', ?)`, t.runID, actor, id, intent, fingerprint, CommandAccepted, stamp(t.now))
	return CommandRow{}, false, err
}

// Settle gives a command its outcome and receipt (JSON, at most MaxPayload
// bytes): done, failed or uncertain.
func (t *Tx) Settle(actor, id, status string, receipt any) error {
	body, err := encode(receipt, MaxPayload)
	if err != nil {
		return err
	}
	res, err := t.tx.ExecContext(t.ctx, "UPDATE commands SET status = ?, receipt = ? WHERE run_id = ? AND actor = ? AND id = ?",
		status, body, t.runID, actor, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("ledger: no command %s of %s", id, actor)
	}
	return nil
}

// PutRun puts the run's row, new or changed. The revision is the ledger's:
// what r says is ignored.
func (t *Tx) PutRun(r RunRow) error {
	if len(r.Crew) > MaxCrew {
		return fmt.Errorf("%w: the crew is %d bytes", ErrTooLarge, len(r.Crew))
	}
	if r.ID != t.runID {
		return fmt.Errorf("ledger: run %s put in %s's transition", r.ID, t.runID)
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO runs (id, crew_id, crew, base, state, outcome, paused, revision, continued_from, continued_by, created_at, closed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET crew_id = excluded.crew_id, crew = excluded.crew, base = excluded.base, state = excluded.state,
			outcome = excluded.outcome, paused = excluded.paused, continued_from = excluded.continued_from, continued_by = excluded.continued_by,
			closed_at = excluded.closed_at`,
		r.ID, r.CrewID, r.Crew, r.Base, r.State, r.Outcome, b2i(r.Paused), r.ContinuedFrom, r.ContinuedBy, stamp(r.CreatedAt), stamp(r.ClosedAt))
	return err
}

// PutTask puts a task's row, new or changed.
func (t *Tx) PutTask(r TaskRow) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO tasks (run_id, id, member, kind, state, reason, attempts, result_attempt, result_commit, verdict, imported_from)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, id) DO UPDATE SET member = excluded.member, kind = excluded.kind, state = excluded.state, reason = excluded.reason,
			attempts = excluded.attempts, result_attempt = excluded.result_attempt, result_commit = excluded.result_commit, verdict = excluded.verdict,
			imported_from = excluded.imported_from`,
		t.runID, r.ID, r.Member, r.Kind, r.State, r.Reason, r.Attempts, r.ResultAttempt, r.ResultCommit, r.Verdict, r.ImportedFrom)
	return err
}

// PutAttempt puts an attempt's row, new or changed.
func (t *Tx) PutAttempt(r AttemptRow) error {
	if len(r.Result) > MaxResult {
		return fmt.Errorf("ledger: the result of attempt %s/%d is more than %d bytes", r.TaskID, r.N, MaxResult)
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO attempts (run_id, task_id, n, member, session_id, worktree, branch, start_commit, result_commit, state, stage, error, reserved_at, launched_at, prompted_at, closed_at, result)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, task_id, n) DO UPDATE SET member = excluded.member, session_id = excluded.session_id, worktree = excluded.worktree,
			branch = excluded.branch, start_commit = excluded.start_commit, result_commit = excluded.result_commit, state = excluded.state,
			stage = excluded.stage, error = excluded.error, reserved_at = excluded.reserved_at, launched_at = excluded.launched_at,
			prompted_at = excluded.prompted_at, closed_at = excluded.closed_at, result = excluded.result`,
		t.runID, r.TaskID, r.N, r.Member, r.SessionID, r.Worktree, r.Branch, r.StartCommit, r.ResultCommit, r.State, r.Stage, r.Error,
		stamp(r.ReservedAt), stamp(r.LaunchedAt), stamp(r.PromptedAt), stamp(r.ClosedAt), r.Result)
	return err
}

// PutCheck puts a check's row, new or changed; its log at most MaxLog, and
// the run's logs at most MaxRunLogs in all: a log that would pass that is
// not kept (LogDropped), its hash and tail are. The row's own log, when it
// is replaced, counts no more.
func (t *Tx) PutCheck(r CheckRow) error {
	if len(r.Log) > MaxLog {
		return fmt.Errorf("%w: the log is %d bytes", ErrTooLarge, len(r.Log))
	}
	argv, err := encode(r.Argv, MaxPayload)
	if err != nil {
		return err
	}
	if r.LogDropped {
		r.Log = nil // dropped is dropped: no bytes behind the mark
	}
	if len(r.Log) > 0 {
		var kept int64
		if err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(SUM(LENGTH(log)), 0) FROM checks WHERE run_id = ? AND NOT (task_id = ? AND n = ? AND id = ? AND k = ?)`,
			t.runID, r.TaskID, r.N, r.ID, r.K).Scan(&kept); err != nil {
			return err
		}
		if kept+int64(len(r.Log)) > maxRunLogs {
			r.Log, r.LogDropped = nil, true
		}
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO checks (run_id, task_id, n, id, k, argv, dir, subject, state, pid, pgroup, process_started_at, started_at, finished_at,
			exit, signal, timed_out, killed, stable, truncated, total, log, log_dropped, log_sha256, tail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, task_id, n, id, k) DO UPDATE SET argv = excluded.argv, dir = excluded.dir, subject = excluded.subject, state = excluded.state,
			pid = excluded.pid, pgroup = excluded.pgroup, process_started_at = excluded.process_started_at, started_at = excluded.started_at,
			finished_at = excluded.finished_at, exit = excluded.exit, signal = excluded.signal, timed_out = excluded.timed_out, killed = excluded.killed,
			stable = excluded.stable, truncated = excluded.truncated, total = excluded.total, log = excluded.log, log_dropped = excluded.log_dropped,
			log_sha256 = excluded.log_sha256, tail = excluded.tail`,
		t.runID, r.TaskID, r.N, r.ID, r.K, argv, r.Dir, r.Subject, r.State, r.PID, r.Group, stamp(r.ProcessStartedAt), stamp(r.StartedAt), stamp(r.FinishedAt),
		r.Exit, r.Signal, b2i(r.TimedOut), b2i(r.Killed), b2i(r.Stable), b2i(r.Truncated), r.Total, r.Log, b2i(r.LogDropped), r.LogSHA256, r.Tail)
	return err
}

// encode marshals v as JSON within max bytes; nil is "".
func encode(v any, max int) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if len(b) > max {
		return "", fmt.Errorf("%w: %d bytes of JSON", ErrTooLarge, len(b))
	}
	return string(b), nil
}

// Readers. Each is a snapshot on the one connection, between transitions.

// Run reads a run's row.
func (l *Ledger) Run(ctx context.Context, id string) (RunRow, bool, error) {
	rows, err := l.runs(ctx, "WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return RunRow{}, false, err
	}
	return rows[0], true, nil
}

// Runs reads every run's row, oldest first.
func (l *Ledger) Runs(ctx context.Context) ([]RunRow, error) {
	return l.runs(ctx, "ORDER BY created_at, id")
}

func (l *Ledger) runs(ctx context.Context, where string, args ...any) ([]RunRow, error) {
	db, err := l.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, crew_id, crew, base, state, outcome, paused, revision, continued_from, continued_by, created_at, closed_at FROM runs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRow
	for rows.Next() {
		var r RunRow
		var paused int
		var created, closed string
		if err := rows.Scan(&r.ID, &r.CrewID, &r.Crew, &r.Base, &r.State, &r.Outcome, &paused, &r.Revision, &r.ContinuedFrom, &r.ContinuedBy, &created, &closed); err != nil {
			return nil, err
		}
		r.Paused, r.CreatedAt, r.ClosedAt = paused != 0, unstamp(created), unstamp(closed)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Tasks reads a run's tasks, in id order.
func (l *Ledger) Tasks(ctx context.Context, runID string) ([]TaskRow, error) {
	db, err := l.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, member, kind, state, reason, attempts, result_attempt, result_commit, verdict, imported_from FROM tasks WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRow
	for rows.Next() {
		r := TaskRow{RunID: runID}
		if err := rows.Scan(&r.ID, &r.Member, &r.Kind, &r.State, &r.Reason, &r.Attempts, &r.ResultAttempt, &r.ResultCommit, &r.Verdict, &r.ImportedFrom); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Attempts reads a run's attempts, by task and number.
func (l *Ledger) Attempts(ctx context.Context, runID string) ([]AttemptRow, error) {
	db, err := l.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT task_id, n, member, session_id, worktree, branch, start_commit, result_commit, state, stage, error, reserved_at, launched_at, prompted_at, closed_at, result
		FROM attempts WHERE run_id = ? ORDER BY task_id, n`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttemptRow
	for rows.Next() {
		r := AttemptRow{RunID: runID}
		var reserved, launched, prompted, closed string
		if err := rows.Scan(&r.TaskID, &r.N, &r.Member, &r.SessionID, &r.Worktree, &r.Branch, &r.StartCommit, &r.ResultCommit, &r.State, &r.Stage, &r.Error, &reserved, &launched, &prompted, &closed, &r.Result); err != nil {
			return nil, err
		}
		r.ReservedAt, r.LaunchedAt, r.PromptedAt, r.ClosedAt = unstamp(reserved), unstamp(launched), unstamp(prompted), unstamp(closed)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Checks reads the checks of a run, or of one attempt when taskID is set
// (n then too); withLog says whether the log blobs come along.
func (l *Ledger) Checks(ctx context.Context, runID, taskID string, n int, withLog bool) ([]CheckRow, error) {
	where, args := "WHERE run_id = ?", []any{runID}
	if taskID != "" {
		where, args = where+" AND task_id = ? AND n = ?", append(args, taskID, n)
	}
	logCol := "NULL"
	if withLog {
		logCol = "log"
	}
	db, err := l.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT task_id, n, id, k, argv, dir, subject, state, pid, pgroup, process_started_at, started_at, finished_at, exit, signal, timed_out, killed, stable, truncated, total, `+logCol+`, log_dropped, log_sha256, tail
		FROM checks `+where+` ORDER BY task_id, n, id, k`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckRow
	for rows.Next() {
		r := CheckRow{RunID: runID}
		var argv, pstarted, started, finished string
		var timedOut, killed, stable, truncated, dropped int
		var log []byte
		if err := rows.Scan(&r.TaskID, &r.N, &r.ID, &r.K, &argv, &r.Dir, &r.Subject, &r.State, &r.PID, &r.Group, &pstarted, &started, &finished, &r.Exit, &r.Signal, &timedOut, &killed, &stable, &truncated, &r.Total, &log, &dropped, &r.LogSHA256, &r.Tail); err != nil {
			return nil, err
		}
		r.LogDropped = dropped != 0
		if argv != "" {
			if err := json.Unmarshal([]byte(argv), &r.Argv); err != nil {
				return nil, err
			}
		}
		r.ProcessStartedAt, r.StartedAt, r.FinishedAt = unstamp(pstarted), unstamp(started), unstamp(finished)
		r.TimedOut, r.Killed, r.Stable, r.Truncated = timedOut != 0, killed != 0, stable != 0, truncated != 0
		r.Log = log
		out = append(out, r)
	}
	return out, rows.Err()
}

// Receipt reads a command's receipt.
func (l *Ledger) Receipt(ctx context.Context, runID, actor, id string) (CommandRow, bool, error) {
	db, err := l.conn()
	if err != nil {
		return CommandRow{}, false, err
	}
	r := CommandRow{RunID: runID, Actor: actor, ID: id}
	var at string
	err = db.QueryRowContext(ctx, "SELECT intent, fingerprint, status, receipt, created_at FROM commands WHERE run_id = ? AND actor = ? AND id = ?", runID, actor, id).
		Scan(&r.Intent, &r.Fingerprint, &r.Status, &r.Receipt, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return CommandRow{}, false, nil
	}
	if err != nil {
		return CommandRow{}, false, err
	}
	r.CreatedAt = unstamp(at)
	return r, true, nil
}

// Commands reads a run's commands, oldest first.
func (l *Ledger) Commands(ctx context.Context, runID string) ([]CommandRow, error) {
	db, err := l.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT actor, id, intent, fingerprint, status, receipt, created_at FROM commands WHERE run_id = ? ORDER BY created_at, actor, id", runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CommandRow
	for rows.Next() {
		r := CommandRow{RunID: runID}
		var at string
		if err := rows.Scan(&r.Actor, &r.ID, &r.Intent, &r.Fingerprint, &r.Status, &r.Receipt, &at); err != nil {
			return nil, err
		}
		r.CreatedAt = unstamp(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Events reads a run's events after seq, at most limit of them, in order.
func (l *Ledger) Events(ctx context.Context, runID string, after int64, limit int) ([]EventRow, error) {
	if limit <= 0 {
		limit = 1000
	}
	db, err := l.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT seq, at, kind, payload FROM events WHERE run_id = ? AND seq > ? ORDER BY seq LIMIT ?", runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		r := EventRow{RunID: runID}
		var at string
		if err := rows.Scan(&r.Seq, &at, &r.Kind, &r.Payload); err != nil {
			return nil, err
		}
		r.At = unstamp(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Recovered is what Recover concluded for one run.
type Recovered struct {
	RunID       string
	Attempts    map[string]int // attempts concluded, by their new state
	Checks      int            // checks interrupted
	Commands    int            // commands left uncertain
	Interrupted bool           // the run itself was running
}

// Recover concludes what the owner left open when it last stopped, for
// every run that was running or has a row left open (a stopped run's
// command accepted and never settled, say): an attempt reserved and not
// launched is uncertain (a worktree and a process may exist); one
// launched, running, submitting or verifying is interrupted (its session
// went with the server; a candidate verifying is verified again in a
// fresh checkout); a check started or running is interrupted (its process
// may be alive still, so it is reported, never killed); a command accepted
// is uncertain; a run running is interrupted, a run already stopped or
// finished keeps its state. One `recovered` event per run touched says
// so. A second call finds nothing open and writes nothing.
func (l *Ledger) Recover(ctx context.Context) ([]Recovered, error) {
	if ro, why := l.ReadOnly(); ro {
		return nil, fmt.Errorf("%w: %s", ErrReadOnly, why)
	}
	runs, err := l.runs(ctx, `WHERE state = ?
		OR EXISTS (SELECT 1 FROM attempts a WHERE a.run_id = runs.id AND a.state IN (?, ?, ?, ?, ?))
		OR EXISTS (SELECT 1 FROM checks c WHERE c.run_id = runs.id AND c.state IN (?, ?))
		OR EXISTS (SELECT 1 FROM commands m WHERE m.run_id = runs.id AND m.status = ?)
		ORDER BY created_at, id`,
		RunRunning, AttemptReserved, AttemptLaunched, AttemptRunning, AttemptSubmitting, AttemptVerifying, CheckStarted, CheckRunning, CommandAccepted)
	if err != nil {
		return nil, err
	}
	var out []Recovered
	for _, run := range runs {
		rec := Recovered{RunID: run.ID, Attempts: map[string]int{}, Interrupted: run.State == RunRunning}
		_, err := l.Transition(ctx, run.ID, func(t *Tx) error {
			now := stamp(t.now)
			for _, c := range []struct{ from, to, stage string }{
				{AttemptReserved, AttemptUncertain, "reserved: the owner stopped before the launch was known"},
				{AttemptLaunched, AttemptInterrupted, "launched: the owner stopped before the prompt was typed"},
				{AttemptRunning, AttemptInterrupted, "running: the owner stopped"},
				{AttemptSubmitting, AttemptInterrupted, "submitting: the owner stopped before the result was taken"},
				{AttemptVerifying, AttemptInterrupted, "verifying: the owner stopped; the candidate is verified again"},
			} {
				res, err := t.tx.ExecContext(ctx, "UPDATE attempts SET state = ?, stage = ?, closed_at = ? WHERE run_id = ? AND state = ?", c.to, c.stage, now, run.ID, c.from)
				if err != nil {
					return err
				}
				n, _ := res.RowsAffected()
				rec.Attempts[c.to] += int(n)
			}
			res, err := t.tx.ExecContext(ctx, "UPDATE checks SET state = ?, finished_at = ? WHERE run_id = ? AND state IN (?, ?)", CheckInterrupted, now, run.ID, CheckStarted, CheckRunning)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			rec.Checks = int(n)
			res, err = t.tx.ExecContext(ctx, "UPDATE commands SET status = ? WHERE run_id = ? AND status = ?", CommandUncertain, run.ID, CommandAccepted)
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			rec.Commands = int(n)
			if rec.Interrupted {
				if _, err := t.tx.ExecContext(ctx, "UPDATE runs SET state = ? WHERE id = ?", RunInterrupted, run.ID); err != nil {
					return err
				}
			}
			return t.Event("recovered", rec)
		})
		if err != nil {
			return out, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// ErrNotPrunable says a run's rows must stay: the run is open, a
// continuation names it as its source, or another run imports one of its
// results.
var ErrNotPrunable = errors.New("ledger: the run is still needed")

// Prune deletes a run's rows, its tasks', attempts', checks', commands'
// and events' with them, once the run is closed and nothing cites it.
func (l *Ledger) Prune(ctx context.Context, runID string) error {
	if l.readOnly {
		return fmt.Errorf("%w: %s", ErrReadOnly, l.why)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state, closed string
	switch err := tx.QueryRowContext(ctx, "SELECT state, closed_at FROM runs WHERE id = ?", runID).Scan(&state, &closed); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	}
	if closed == "" || state == RunRunning {
		return fmt.Errorf("%w: it is %s", ErrNotPrunable, state)
	}
	var cited int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM runs WHERE continued_from = ?) + (SELECT COUNT(*) FROM tasks WHERE imported_from LIKE ?)`,
		runID, strings.ReplaceAll(strings.ReplaceAll(runID, "%", `\%`), "_", `\_`)+"/%").Scan(&cited); err != nil {
		return err
	}
	if cited > 0 {
		return fmt.Errorf("%w: %d runs cite it", ErrNotPrunable, cited)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM runs WHERE id = ?", runID); err != nil {
		return err
	}
	return tx.Commit()
}
