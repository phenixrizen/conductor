package crew

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
	"github.com/phenixrizen/conductor/internal/gitcli"
	"github.com/phenixrizen/conductor/internal/session"
)

// A managed run (a crew with tasks, #334; docs/plans/agent-harness-order.md)
// dispatches its tasks as attempts: a fresh member session in a worktree of
// its own, made at the result the task starts from, with its task in a
// file. The member submits a result (SubmitResult); the controller decides
// it (verify). Every transition is written to the ledger before the side
// effect it precedes.

// Bounds of a managed run.
const (
	maxActiveAttempts  = 4 // attempts in flight per run
	maxAttemptsPerTask = 3
	// maxTaskFile bounds .conductor/task.md and task.json.
	maxTaskFile = 64 << 10
	// taskDir is the directory of an attempt's task files, in its worktree.
	taskDir = ".conductor"
)

// Result bounds.
const (
	maxSummary     = 4000 // characters
	maxIssues      = 20
	maxIssue       = 500
	maxFindings    = 50
	maxFindingText = 1000
	maxFindingPath = 1024
)

// LedgerTx is a transition's writer: ledger.Tx, or a test's fake.
type LedgerTx interface {
	Event(kind string, payload any) error
	PutRun(ledger.RunRow) error
	PutTask(ledger.TaskRow) error
	PutAttempt(ledger.AttemptRow) error
	PutCheck(ledger.CheckRow) error
}

// Ledger is the managed runs' ledger as the engine writes it: one
// transition at a time, committed before the side effect it precedes.
// Available says whether it takes transitions now: a ledger that is
// read-only (another server holds the owner lock, for a while after a
// restart) refuses a managed launch with why.
type Ledger interface {
	Transition(ctx context.Context, runID string, fn func(LedgerTx) error) (revision int64, err error)
	Available() error
}

// SQLLedger is the ledger package's as a Ledger.
func SQLLedger(l *ledger.Ledger) Ledger { return sqlLedger{l} }

type sqlLedger struct{ l *ledger.Ledger }

func (s sqlLedger) Transition(ctx context.Context, runID string, fn func(LedgerTx) error) (int64, error) {
	return s.l.Transition(ctx, runID, func(tx *ledger.Tx) error { return fn(tx) })
}

func (s sqlLedger) Available() error {
	if ro, why := s.l.ReadOnly(); ro {
		return errors.New(why)
	}
	return nil
}

// UseLedger gives the engine the ledger managed runs are written to. Set
// it before the first launch; without one, or with one not available, a
// managed launch is refused (ErrNoLedger).
func (e *Engine) UseLedger(l Ledger) { e.ledger = l }

// Errors of a managed run.
var (
	ErrNoLedger      = errors.New("managed runs are off: the ledger is not available")
	ErrNotAnAttempt  = errors.New("the session is not an attempt at a task")
	ErrStaleAttempt  = errors.New("the attempt is not the task's active one")
	ErrNotRunning    = errors.New("the attempt is not running")
	ErrCommitMissing = errors.New("the worktree has no commit to submit")
	ErrNotFromInput  = errors.New("the result does not descend from the task's inputs")
	ErrReviewChanged = errors.New("a review submits no commit: its tree must be the candidate's")
	ErrReservedPath  = errors.New("the result tree holds a path under .conductor/, which is Conductor's")
	ErrSubmodules    = errors.New("the result tree holds a submodule, which managed runs do not take")
	ErrBadResult     = errors.New("bad result")
	ErrTaskNotFound  = errors.New("no such task in the run")
	ErrTaskState     = errors.New("the task is not in a state for that")
)

// DirtyWorktreeError says the attempt's worktree has changes not committed.
type DirtyWorktreeError struct{ Paths []string }

func (e *DirtyWorktreeError) Error() string {
	return "the worktree has changes not committed: commit them, then submit (" + strings.Join(e.Paths, ", ") + ")"
}

// Result is what an attempt submits: a work task's commit and summary, or
// a review's verdict and findings.
type Result struct {
	Summary  string    `json:"summary,omitempty"`
	Commit   string    `json:"commit,omitempty"` // the result commit; HEAD when empty
	Issues   []string  `json:"issues,omitempty"`
	Verdict  string    `json:"verdict,omitempty"` // a review's: approved | needs_changes
	Findings []Finding `json:"findings,omitempty"`
}

// Finding is one thing a review found.
type Finding struct {
	Severity string `json:"severity"` // blocker | major | minor | nit
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

// Verdicts of a review.
const (
	VerdictApproved     = "approved"
	VerdictNeedsChanges = "needs_changes"
)

// validate checks r for a task of the kind.
func (r Result) validate(kind string) error {
	if utf8.RuneCountInString(r.Summary) > maxSummary {
		return fmt.Errorf("%w: the summary is more than %d characters", ErrBadResult, maxSummary)
	}
	if len(r.Issues) > maxIssues {
		return fmt.Errorf("%w: more than %d issues", ErrBadResult, maxIssues)
	}
	for _, s := range r.Issues {
		if utf8.RuneCountInString(s) > maxIssue {
			return fmt.Errorf("%w: an issue is more than %d characters", ErrBadResult, maxIssue)
		}
	}
	if r.Commit != "" && !fullCommit(r.Commit) {
		return fmt.Errorf("%w: the commit is not a full commit id", ErrBadResult)
	}
	switch kind {
	case TaskWork:
		if r.Verdict != "" || len(r.Findings) > 0 {
			return fmt.Errorf("%w: a work task submits a commit, not a verdict", ErrBadResult)
		}
	case TaskReview:
		if r.Verdict != VerdictApproved && r.Verdict != VerdictNeedsChanges {
			return fmt.Errorf("%w: a review submits a verdict, %q or %q", ErrBadResult, VerdictApproved, VerdictNeedsChanges)
		}
		if r.Commit != "" {
			return fmt.Errorf("%w: a review submits no commit", ErrBadResult)
		}
		if len(r.Findings) > maxFindings {
			return fmt.Errorf("%w: more than %d findings", ErrBadResult, maxFindings)
		}
		if r.Verdict == VerdictNeedsChanges && len(r.Findings) == 0 {
			return fmt.Errorf("%w: needs_changes needs at least one finding", ErrBadResult)
		}
		for _, f := range r.Findings {
			switch f.Severity {
			case "blocker", "major", "minor", "nit":
			default:
				return fmt.Errorf("%w: a finding's severity is blocker, major, minor or nit", ErrBadResult)
			}
			if f.Message == "" || utf8.RuneCountInString(f.Message) > maxFindingText || len(f.Path) > maxFindingPath || f.Line < 0 {
				return fmt.Errorf("%w: a finding has a message of at most %d characters, a path of at most %d bytes and a line of 1 or more", ErrBadResult, maxFindingText, maxFindingPath)
			}
		}
	}
	return nil
}

// task is a task of a managed run as the engine runs it.
type task struct {
	def      Task
	state    string // ledger.Task*
	reason   string
	attempts []*attempt
	result   *taskResult
	extra    int // attempts granted past the bound (RetryTask)
}

// taskResult is a task's accepted result.
type taskResult struct {
	attempt  int
	commit   string
	summary  string
	issues   []string
	verdict  string
	findings []Finding
	at       time.Time
}

// attempt is one try at a task: one member session in one worktree.
type attempt struct {
	t      *task
	m      *member
	n      int
	state  string // ledger.Attempt*
	stage  string
	err    string
	ctx    context.Context // ends when the attempt closes
	cancel context.CancelFunc

	sessionID  string
	worktree   string
	branch     string
	start      string // the commit the worktree was made at
	inputs     []input
	result     string // the result commit, once submitted
	submitted  *Result
	reservedAt time.Time
	launchedAt time.Time
	promptedAt time.Time
	closedAt   time.Time
	reminded   bool
}

// input is a result a task starts from or has at hand.
type input struct {
	task    string
	attempt int
	commit  string
	summary string
	branch  string
	verdict string
}

// TaskState is a task as the run reports it.
type TaskState struct {
	ID       string        `json:"id"`
	Member   string        `json:"member"`
	Kind     string        `json:"kind"`
	Needs    []string      `json:"needs,omitempty"`
	State    string        `json:"state"`
	Reason   string        `json:"reason,omitempty"`
	Attempts int           `json:"attempts"`
	Attempt  *AttemptState `json:"attempt,omitempty"` // the latest
	Result   *ResultState  `json:"result,omitempty"`
}

// AttemptState is an attempt as the run reports it.
type AttemptState struct {
	N         int        `json:"n"`
	SessionID string     `json:"sessionId,omitempty"`
	Worktree  string     `json:"worktree,omitempty"`
	Branch    string     `json:"branch,omitempty"`
	Start     string     `json:"start,omitempty"`
	Result    string     `json:"result,omitempty"`
	State     string     `json:"state"`
	Stage     string     `json:"stage,omitempty"`
	Error     string     `json:"error,omitempty"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// ResultState is a task's accepted result as the run reports it.
type ResultState struct {
	Attempt  int       `json:"attempt"`
	Commit   string    `json:"commit,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Verdict  string    `json:"verdict,omitempty"`
	Findings []Finding `json:"findings,omitempty"`
	At       time.Time `json:"at"`
}

// newTasks makes the engine's tasks of a plan: blocked until their needs
// have results, ready at once without needs.
func newTasks(plan []Task) []*task {
	out := make([]*task, 0, len(plan))
	for _, def := range plan {
		st := ledger.TaskReady
		if len(def.Needs) > 0 {
			st = ledger.TaskBlocked
		}
		out = append(out, &task{def: def, state: st})
	}
	return out
}

// task returns r's task with the given id. The caller holds e.mu.
func (r *run) task(id string) *task {
	for _, t := range r.tasks {
		if t.def.ID == id {
			return t
		}
	}
	return nil
}

// taskStates is the run's tasks as reported. The caller holds e.mu.
func (r *run) taskStates() []TaskState {
	out := make([]TaskState, 0, len(r.tasks))
	for _, t := range r.tasks {
		st := TaskState{ID: t.def.ID, Member: t.def.Member, Kind: t.def.Kind, Needs: slices.Clone(t.def.Needs), State: t.state, Reason: t.reason, Attempts: len(t.attempts)}
		if n := len(t.attempts); n > 0 {
			a := t.attempts[n-1]
			as := AttemptState{N: a.n, SessionID: a.sessionID, Worktree: a.worktree, Branch: a.branch, Start: a.start, Result: a.result, State: a.state, Stage: a.stage, Error: a.err}
			if !a.launchedAt.IsZero() {
				at := a.launchedAt
				as.StartedAt = &at
			}
			if !a.closedAt.IsZero() {
				at := a.closedAt
				as.EndedAt = &at
			}
			st.Attempt = &as
		}
		if res := t.result; res != nil {
			st.Result = &ResultState{Attempt: res.attempt, Commit: res.commit, Summary: res.summary, Verdict: res.verdict, Findings: slices.Clone(res.findings), At: res.at}
		}
		out = append(out, st)
	}
	return out
}

// settle derives the run's outcome once every task is terminal: succeeded
// when all succeeded, cancelled when one was cancelled and none failed,
// failed otherwise; "" while a task is not terminal. The caller holds e.mu.
func (r *run) settle() {
	failed, cancelled := false, false
	for _, t := range r.tasks {
		switch t.state {
		case ledger.TaskSucceeded:
		case ledger.TaskFailed:
			failed = true
		case ledger.TaskCancelled:
			cancelled = true
		case ledger.TaskBlocked:
			// Blocked for good behind a failed or cancelled need, or still
			// waiting: told apart by the needs.
			if !r.blockedForGood(t) {
				r.outcome = ""
				return
			}
		default:
			r.outcome = ""
			return
		}
	}
	switch {
	case failed:
		r.outcome = ledger.OutcomeFailed
	case cancelled:
		r.outcome = ledger.OutcomeCancelled
	default:
		r.outcome = ledger.OutcomeSucceeded
	}
}

// blockedForGood reports whether t waits behind a need that failed or was
// cancelled, or behind one blocked for good. The caller holds e.mu.
func (r *run) blockedForGood(t *task) bool {
	for _, n := range t.def.Needs {
		need := r.task(n)
		if need == nil {
			return true
		}
		switch need.state {
		case ledger.TaskFailed, ledger.TaskCancelled:
			return true
		case ledger.TaskBlocked:
			if r.blockedForGood(need) {
				return true
			}
		}
	}
	return false
}

// ready marks the blocked tasks whose needs all have results ready, and
// says what blocks the others. The caller holds e.mu.
func (r *run) ready() {
	for _, t := range r.tasks {
		if t.state != ledger.TaskBlocked {
			continue
		}
		waiting := []string{}
		for _, n := range t.def.Needs {
			if need := r.task(n); need == nil || need.state != ledger.TaskSucceeded {
				waiting = append(waiting, n)
			}
		}
		if len(waiting) == 0 {
			t.state, t.reason = ledger.TaskReady, ""
		} else if r.blockedForGood(t) {
			t.reason = "needs " + strings.Join(waiting, ", ") + ", which will not succeed"
		} else {
			t.reason = "needs " + strings.Join(waiting, ", ")
		}
	}
}

// activeAttempt is a member's attempt in flight, nil for none. The caller
// holds e.mu.
func (r *run) activeAttempt(m *member) *attempt {
	for _, t := range r.tasks {
		if a := t.active(); a != nil && a.m == m {
			return a
		}
	}
	return nil
}

// active is t's attempt in flight, nil for none.
func (t *task) active() *attempt {
	if n := len(t.attempts); n > 0 {
		if a := t.attempts[n-1]; a.closedAt.IsZero() {
			return a
		}
	}
	return nil
}

// inputs are the results t starts from, in the order of its needs: the
// first is the commit its worktree is made at. The caller holds e.mu.
func (r *run) inputs(t *task) []input {
	var out []input
	for _, n := range t.def.Needs {
		need := r.task(n)
		if need == nil || need.result == nil {
			continue
		}
		var branch string
		if need.result.attempt > 0 && need.result.attempt <= len(need.attempts) {
			branch = need.attempts[need.result.attempt-1].branch
		}
		out = append(out, input{task: n, attempt: need.result.attempt, commit: need.result.commit, summary: need.result.summary, branch: branch, verdict: need.result.verdict})
	}
	return out
}

// startCommit is where t's next attempt starts: the result of its last
// attempt when that failed a check (the work is kept; the task file says
// what failed), else its first need's result, else the run's base.
func (r *run) startCommit(t *task) string {
	if n := len(t.attempts); n > 0 {
		if last := t.attempts[n-1]; last.state == ledger.AttemptFailed && last.result != "" {
			return last.result
		}
	}
	for _, in := range r.inputs(t) {
		if in.commit != "" {
			return in.commit
		}
	}
	return r.base
}

// pump dispatches the ready tasks, in plan order, while the run takes
// them: fewer than maxActiveAttempts in flight, the member free, the task
// within its attempts, the run neither stopping nor paused. The caller
// holds e.mu; the dispatches run on goroutines of their own.
func (e *Engine) pump(r *run) {
	if !r.managed || r.stopping || r.paused || r.ctx.Err() != nil {
		return
	}
	r.ready()
	for _, t := range r.tasks {
		if r.active >= maxActiveAttempts {
			break
		}
		if t.state != ledger.TaskReady {
			continue
		}
		m := r.member(t.def.Member)
		if m == nil || r.activeAttempt(m) != nil {
			continue
		}
		if len(t.attempts) >= maxAttemptsPerTask+t.extra {
			t.state, t.reason = ledger.TaskFailed, fmt.Sprintf("%d attempts made; retry it to allow one more", len(t.attempts))
			continue
		}
		ctx, cancel := context.WithCancel(r.ctx)
		a := &attempt{t: t, m: m, n: len(t.attempts) + 1, state: ledger.AttemptReserved, ctx: ctx, cancel: cancel, reservedAt: time.Now().UTC(), inputs: r.inputs(t), start: r.startCommit(t)}
		t.attempts = append(t.attempts, a)
		t.state, t.reason = ledger.TaskRunning, ""
		r.active++
		r.starts.Add(1)
		r.note(session.ActivityStatus, "task %s: attempt %d reserved for %s", t.def.ID, a.n, m.def.Name)
		go e.dispatch(ctx, r, a)
	}
	if r.outcome == "" {
		r.settle()
		if r.outcome != "" {
			r.note(session.ActivityStatus, "every task is done: the run %s", r.outcome)
			e.noteEndLater(r)
		}
	}
}

// noteEndLater reports the run's end (OnEnd) once the caller has let go
// of e.mu.
func (e *Engine) noteEndLater(r *run) {
	go e.noteEnd(r.id)
}

// startManaged begins a managed run: its base is the repository's commit,
// its first transition records the run and its tasks, and the ready tasks
// are dispatched. An error is the launch's.
func (e *Engine) startManaged(ctx context.Context, r *run) error {
	base, err := headCommit(ctx, r.cwd)
	if err != nil {
		return fmt.Errorf("reading the repository's commit: %w", err)
	}
	if !fullCommit(base) {
		return fmt.Errorf("the repository's commit %q is not a commit id", base)
	}
	e.mu.Lock()
	r.base = base
	crew := r.crewJSON
	e.mu.Unlock()
	if err := e.created(ctx, r, crew); err != nil {
		return err
	}
	e.mu.Lock()
	e.pump(r)
	e.mu.Unlock()
	return nil
}

// recordStop writes a managed run's stop: its attempts stopped, its tasks,
// its row. The caller does not hold e.mu.
func (e *Engine) recordStop(r *run) {
	e.mu.Lock()
	var rows []ledger.AttemptRow
	var trows []ledger.TaskRow
	for _, t := range r.tasks {
		trows = append(trows, r.taskRow(t))
		for _, a := range t.attempts {
			if a.state == ledger.AttemptStopped {
				rows = append(rows, r.attemptRow(a))
			}
		}
	}
	runRow := r.runRow(r.crewJSON)
	e.mu.Unlock()
	_ = e.transition(context.Background(), r, func(tx LedgerTx) error {
		for _, ar := range rows {
			if err := tx.PutAttempt(ar); err != nil {
				return err
			}
		}
		for _, tr := range trows {
			if err := tx.PutTask(tr); err != nil {
				return err
			}
		}
		if err := tx.PutRun(runRow); err != nil {
			return err
		}
		return tx.Event("run.stopped", map[string]any{"attempts": len(rows)})
	})
}

// watchAttempt marks a's member ended when its session ends and, when the
// end comes before a result, fails the attempt. It ends with the run.
func (e *Engine) watchAttempt(r *run, a *attempt, local *session.Local) {
	select {
	case <-local.Ended():
	case <-r.ctx.Done():
		return
	}
	now := time.Now().UTC()
	e.mu.Lock()
	m := a.m
	if m.state.SessionID == a.sessionID && m.state.Status != MemberEnded {
		m.state.Status, m.state.Ended = MemberEnded, &now
	}
	r.note(session.ActivityStatus, "task %s: attempt %d's session ended (%s)", a.t.def.ID, a.n, a.state)
	e.mu.Unlock()
	e.managedEnded(r, a)
}

// closeAttempt ends a, with its state, stage and error, lets its member
// and the run's count go, and settles its task: failed with attempts
// left goes back to ready, else the task fails and its dependents are
// blocked for good. The caller holds e.mu.
func (r *run) closeAttempt(a *attempt, state, stage, errText string) {
	if !a.closedAt.IsZero() {
		return
	}
	a.state, a.stage, a.err, a.closedAt = state, stage, errText, time.Now().UTC()
	a.cancel()
	r.active--
	t := a.t
	switch state {
	case ledger.AttemptSucceeded:
		t.state, t.reason = ledger.TaskSucceeded, ""
	case ledger.AttemptCancelled:
		t.state, t.reason = ledger.TaskCancelled, "cancelled"
	case ledger.AttemptStopped:
		// The run stopped under it: not the task's failure. A continuation
		// (row 8) takes it up again.
		t.state, t.reason = ledger.TaskReady, fmt.Sprintf("attempt %d stopped with the run", a.n)
	default:
		if len(t.attempts) < maxAttemptsPerTask+t.extra && state == ledger.AttemptFailed {
			t.state, t.reason = ledger.TaskReady, fmt.Sprintf("attempt %d failed at %s: %s", a.n, stage, errText)
		} else {
			t.state, t.reason = ledger.TaskFailed, fmt.Sprintf("attempt %d %s at %s: %s", a.n, state, stage, errText)
		}
	}
	r.ready()
	r.note(session.ActivityStatus, "task %s: attempt %d %s%s", t.def.ID, a.n, state, suffix(stage, errText))
}

func suffix(stage, errText string) string {
	switch {
	case stage != "" && errText != "":
		return " at " + stage + ": " + errText
	case stage != "":
		return " at " + stage
	case errText != "":
		return ": " + errText
	}
	return ""
}

// stopAttempts ends the attempts in flight as stopped, for a stop. The
// caller holds e.mu.
func (r *run) stopAttempts() {
	for _, t := range r.tasks {
		if a := t.active(); a != nil {
			r.closeAttempt(a, ledger.AttemptStopped, a.state, "the run was stopped")
		}
	}
}

// transition writes one transition of r to the ledger and keeps its
// revision. It never holds e.mu: the caller does not either.
func (e *Engine) transition(ctx context.Context, r *run, fn func(LedgerTx) error) error {
	if e.ledger == nil {
		return ErrNoLedger
	}
	rev, err := e.ledger.Transition(ctx, r.id, fn)
	if err != nil {
		return err
	}
	e.mu.Lock()
	r.revision = rev
	r.touch()
	e.mu.Unlock()
	return nil
}

// attemptRow is a as the ledger keeps it. The caller holds e.mu.
func (r *run) attemptRow(a *attempt) ledger.AttemptRow {
	return ledger.AttemptRow{RunID: r.id, TaskID: a.t.def.ID, N: a.n, Member: a.m.def.Name, SessionID: a.sessionID, Worktree: a.worktree, Branch: a.branch,
		StartCommit: a.start, ResultCommit: a.result, State: a.state, Stage: a.stage, Error: a.err,
		ReservedAt: a.reservedAt, LaunchedAt: a.launchedAt, PromptedAt: a.promptedAt, ClosedAt: a.closedAt}
}

// taskRow is t as the ledger keeps it. The caller holds e.mu.
func (r *run) taskRow(t *task) ledger.TaskRow {
	row := ledger.TaskRow{RunID: r.id, ID: t.def.ID, Member: t.def.Member, Kind: t.def.Kind, State: t.state, Reason: t.reason, Attempts: len(t.attempts)}
	if t.result != nil {
		row.ResultAttempt, row.ResultCommit, row.Verdict = t.result.attempt, t.result.commit, t.result.verdict
	}
	return row
}

// runRow is r as the ledger keeps it. The caller holds e.mu.
func (r *run) runRow(crewJSON string) ledger.RunRow {
	state := ledger.RunRunning
	if r.stoppedAt != nil {
		state = ledger.RunStopped
	} else if r.outcome != "" {
		state = ledger.RunFinished
	}
	row := ledger.RunRow{ID: r.id, CrewID: r.crewID, Crew: crewJSON, Base: r.base, State: state, Outcome: r.outcome, Paused: r.paused, CreatedAt: r.startedAt}
	if state != ledger.RunRunning {
		row.ClosedAt = time.Now().UTC()
	}
	return row
}

// created writes a managed run's first transition: its row, its tasks and
// the run.created event. A failure is the launch's: the run is aborted.
func (e *Engine) created(ctx context.Context, r *run, crewJSON string) error {
	e.mu.Lock()
	row := r.runRow(crewJSON)
	tasks := make([]ledger.TaskRow, 0, len(r.tasks))
	for _, t := range r.tasks {
		tasks = append(tasks, r.taskRow(t))
	}
	e.mu.Unlock()
	return e.transition(ctx, r, func(tx LedgerTx) error {
		if err := tx.PutRun(row); err != nil {
			return err
		}
		for _, t := range tasks {
			if err := tx.PutTask(t); err != nil {
				return err
			}
		}
		return tx.Event("run.created", map[string]any{"base": row.Base, "tasks": len(tasks)})
	})
}

// dispatch makes a's worktree and session and types its task line, each
// step recorded before it is taken: the member's previous session let go,
// the place among the sessions reserved, the attempt reserved in the
// ledger, the worktree at the start commit, the task files, the setup, the
// launch, the line. A failure fails the attempt at its stage; a refusal at
// the reservation spends nothing. It runs on a goroutine of its own, on
// the attempt's context, and ends the start it was reserved under.
func (e *Engine) dispatch(ctx context.Context, r *run, a *attempt) {
	defer r.starts.Done()
	t, m := a.t, a.m
	fail := func(stage string, err error) {
		e.mu.Lock()
		state := ledger.AttemptFailed
		if r.ctx.Err() != nil {
			state = ledger.AttemptStopped
		} else if ctx.Err() != nil {
			state = ledger.AttemptCancelled
		}
		r.closeAttempt(a, state, stage, session.CleanMessage(err.Error()))
		row, trow := r.attemptRow(a), r.taskRow(t)
		e.pump(r)
		e.mu.Unlock()
		_ = e.transition(context.Background(), r, func(tx LedgerTx) error {
			if err := tx.PutAttempt(row); err != nil {
				return err
			}
			if err := tx.PutTask(trow); err != nil {
				return err
			}
			return tx.Event("attempt.failed", map[string]any{"task": t.def.ID, "attempt": a.n, "stage": stage, "error": row.Error})
		})
	}
	// The member's previous session, idle since its task: let go, so that
	// its place is free for this one.
	e.mu.Lock()
	prev := m.state.SessionID
	e.mu.Unlock()
	if prev != "" {
		if l, ok := e.lookup(prev); ok && !l.Info().Status.Ended() {
			_ = e.transition(ctx, r, func(tx LedgerTx) error {
				return tx.Event("session.released", map[string]any{"member": m.def.Name, "session": prev, "for": t.def.ID})
			})
			stopLocal(l)
			select {
			case <-l.Ended():
			case <-time.After(stopTimeout):
			case <-ctx.Done():
			}
		}
	}
	res, err := e.launcher.Reserve(ctx)
	if err != nil {
		// Refused: nothing was made, and the attempt is not spent. The
		// task waits as ready for the next pump.
		e.mu.Lock()
		t.attempts = t.attempts[:len(t.attempts)-1]
		r.active--
		a.cancel()
		t.state, t.reason = ledger.TaskReady, "waiting for a place among the server's sessions: "+session.CleanMessage(err.Error())
		r.note(session.ActivityStatus, "task %s: no place for attempt %d yet: %v", t.def.ID, a.n, err)
		e.mu.Unlock()
		return
	}
	// The place goes to the launch; let go on every other way out.
	defer res.Release()
	e.mu.Lock()
	a.worktree = filepath.Join(r.cwd, worktreesDir, r.id, fmt.Sprintf("%s.%d", t.def.ID, a.n))
	a.branch = fmt.Sprintf("crew/%s/%s.%d", r.id, t.def.ID, a.n)
	row := r.attemptRow(a)
	inputs := slices.Clone(a.inputs)
	e.mu.Unlock()
	if err := e.transition(ctx, r, func(tx LedgerTx) error {
		if err := tx.PutAttempt(row); err != nil {
			return err
		}
		return tx.Event("attempt.reserved", map[string]any{"task": t.def.ID, "attempt": a.n, "member": m.def.Name, "start": a.start, "inputs": len(inputs)})
	}); err != nil {
		// The ledger refused the intent: nothing is made, and the run's
		// dispatch pauses until a person looks.
		e.mu.Lock()
		t.attempts = t.attempts[:len(t.attempts)-1]
		r.active--
		a.cancel()
		t.state, t.reason = ledger.TaskReady, "the ledger could not record the attempt: "+session.CleanMessage(err.Error())
		r.paused = true
		r.note(session.ActivityError, "dispatch paused: the ledger could not record task %s's attempt %d: %v", t.def.ID, a.n, err)
		e.mu.Unlock()
		return
	}
	// The worktree, at the start commit, under the repository's lock.
	if err := checkWorktreesDir(r.cwd); err != nil {
		fail("worktree", err)
		return
	}
	e.gitMu.Lock()
	err = excludeWorktrees(ctx, r.cwd)
	e.gitMu.Unlock()
	if err != nil {
		e.mu.Lock()
		if !r.excludeNoted {
			r.excludeNoted = true
			r.note(session.ActivityError, "could not add %s to the repository's info/exclude: %v", excludeLine, err)
		}
		e.mu.Unlock()
	}
	if tracked, err := treeHasPath(ctx, r.cwd, a.start, path.Join(filepath.ToSlash(r.prefix), taskDir)); err != nil {
		fail("worktree", err)
		return
	} else if tracked {
		fail("worktree", fmt.Errorf("the commit %s tracks %s/, which is Conductor's: the task files cannot be written", short(a.start), taskDir))
		return
	}
	e.gitMu.Lock()
	err = AddWorktreeAt(ctx, r.cwd, a.worktree, a.branch, a.start)
	e.gitMu.Unlock()
	if err != nil {
		fail("worktree", err)
		return
	}
	if head, err := headCommit(ctx, a.worktree); err != nil || head != a.start {
		fail("worktree", fmt.Errorf("the worktree is at %s, not %s", short(head), short(a.start)))
		return
	}
	if err := checkNoLinks(a.worktree, r.prefix); err != nil {
		fail("worktree", err)
		return
	}
	cwd := filepath.Join(a.worktree, r.prefix)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		fail("worktree", err)
		return
	}
	e.mu.Lock()
	m.state.Branch, m.state.Worktree = a.branch, a.worktree
	m.base = a.start
	e.mu.Unlock()
	// The task files, through a root at the worktree: a .conductor the
	// commit made a symbolic link is refused.
	md, js, err := e.taskFile(r, a)
	if err == nil {
		err = writeTaskFiles(a.worktree, r.prefix, md, js)
	}
	if err != nil {
		fail("task file", err)
		return
	}
	_ = e.transition(ctx, r, func(tx LedgerTx) error {
		return tx.Event("attempt.workspace", map[string]any{"task": t.def.ID, "attempt": a.n, "worktree": a.worktree, "branch": a.branch, "start": a.start})
	})
	// The setup, in the worktree, before the member starts.
	for _, ch := range r.setup {
		if err := e.runSetup(ctx, r, a, cwd, ch); err != nil {
			fail("setup "+ch.ID, err)
			return
		}
	}
	// The session, with the attempt in its crew reference.
	local, err := e.launcher.Launch(ctx, LaunchSpec{AgentID: m.def.AgentID, Name: fmt.Sprintf("%s · %s #%d", m.def.Name, t.def.ID, a.n), Cwd: cwd, Args: slices.Clone(m.def.Args),
		Env: map[string]string{"GOAL": r.goal}, Ref: session.CrewRef{RunID: r.id, CrewID: r.crewID, Member: m.def.Name, Task: t.def.ID, Attempt: a.n}, Yolo: r.yolo, Reservation: res})
	if err != nil {
		fail("launch", err)
		return
	}
	id := local.Info().ID
	started := time.Now().UTC()
	e.mu.Lock()
	a.sessionID, a.launchedAt, a.state = id, started, ledger.AttemptLaunched
	m.state.SessionID, m.state.Started, m.state.Status, m.state.Ended, m.state.Err = id, &started, MemberStarting, nil, ""
	m.state.Task, m.state.Attempt = t.def.ID, a.n
	e.bySession.Store(id, sessionMember{r: r, m: m, a: a})
	r.note(session.ActivityStatus, "task %s: attempt %d started as %s", t.def.ID, a.n, m.def.Name)
	row = r.attemptRow(a)
	stopping := r.stopping
	e.mu.Unlock()
	go e.watchAttempt(r, a, local)
	if stopping {
		stopLocal(local)
		fail("launch", ErrRunStopped)
		return
	}
	_ = e.transition(ctx, r, func(tx LedgerTx) error {
		if err := tx.PutAttempt(row); err != nil {
			return err
		}
		return tx.Event("attempt.launched", map[string]any{"task": t.def.ID, "attempt": a.n, "session": id})
	})
	// The line: typed once the session is ready, as a role prompt is.
	if err := e.promptWith(ctx, r, m, local, taskLine(t.def, a.n)); err != nil {
		if r.ctx.Err() != nil {
			err = ErrRunStopped
		}
		stopLocal(local)
		fail("prompt", err)
		return
	}
	e.mu.Lock()
	if a.closedAt.IsZero() {
		a.promptedAt, a.state = time.Now().UTC(), ledger.AttemptRunning
	}
	row = r.attemptRow(a)
	e.mu.Unlock()
	_ = e.transition(ctx, r, func(tx LedgerTx) error {
		if err := tx.PutAttempt(row); err != nil {
			return err
		}
		return tx.Event("attempt.prompted", map[string]any{"task": t.def.ID, "attempt": a.n})
	})
}

// runSetup runs one setup command in an attempt's worktree and records
// how it went.
func (e *Engine) runSetup(ctx context.Context, r *run, a *attempt, dir string, ch Check) error {
	res := e.runCommand(ctx, dir, ch, io.Discard, nil)
	_ = e.transition(context.Background(), r, func(tx LedgerTx) error {
		return tx.Event("attempt.setup", map[string]any{"task": a.t.def.ID, "attempt": a.n, "setup": ch.ID, "exit": res.Exit, "timedOut": res.TimedOut, "error": res.Err, "duration": res.Duration.String()})
	})
	switch {
	case res.Err != "" && !res.TimedOut:
		return fmt.Errorf("%s could not run: %s", ch.ID, res.Err)
	case res.TimedOut:
		return fmt.Errorf("%s timed out after %s%s", ch.ID, checkTimeout(ch), tailWords(res.Tail))
	case res.Exit != 0:
		return fmt.Errorf("%s exited %d%s", ch.ID, res.Exit, tailWords(res.Tail))
	}
	return nil
}

func tailWords(tail []byte) string {
	t := strings.TrimSpace(string(tail))
	if t == "" {
		return ""
	}
	if len(t) > 200 {
		t = "…" + t[len(t)-200:]
	}
	return ": " + session.CleanMessage(t)
}

// short is a commit id as the log names it.
func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// treeHasPath reports whether the commit's tree holds path.
func treeHasPath(ctx context.Context, repo, commit, path string) (bool, error) {
	out, err := gitcli.Run(ctx, repo, "ls-tree", "-d", "--name-only", commit, "--", path)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// managedActivity takes a member's done or handoff in a managed run: a
// done finishes nothing (a reminder is typed once per attempt when it came
// with no result); a handoff is noted, never typed. The caller holds e.mu.
func (e *Engine) managedActivity(r *run, sm sessionMember, entry session.ActivityEntry, isHandoff bool) {
	a := sm.a
	if isHandoff {
		r.note(session.ActivityStatus, "%s reported a handoff to %s: noted, not typed; a managed run passes work by results", sm.m.def.Name, entry.To)
		return
	}
	if a == nil || !a.closedAt.IsZero() || a.state != ledger.AttemptRunning || a.reminded {
		return
	}
	a.reminded = true
	r.note(session.ActivityStatus, "task %s: %s reports done without a result; reminded once", a.t.def.ID, sm.m.def.Name)
	if l, ok := e.lookup(a.sessionID); ok {
		line := fmt.Sprintf("Conductor: a done does not finish task %q. Commit your work and submit it with the task_result tool or \"conductor task result --summary ...\"; a review submits --verdict.", a.t.def.ID)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
			defer cancel()
			_, _ = l.Submit(ctx, session.Submission{Text: line, ByName: typedBy, UnlessWaiting: true})
		}()
	}
}

// managedEnded takes the end of an attempt's session before its result: the
// attempt fails at its stage. The caller does not hold e.mu.
func (e *Engine) managedEnded(r *run, a *attempt) {
	e.mu.Lock()
	if !a.closedAt.IsZero() || a.state == ledger.AttemptVerifying || a.state == ledger.AttemptSubmitting {
		e.mu.Unlock()
		return
	}
	stage := a.state
	r.closeAttempt(a, ledger.AttemptFailed, stage, "the session ended before a result was submitted")
	row, trow := r.attemptRow(a), r.taskRow(a.t)
	e.pump(r)
	e.mu.Unlock()
	_ = e.transition(context.Background(), r, func(tx LedgerTx) error {
		if err := tx.PutAttempt(row); err != nil {
			return err
		}
		if err := tx.PutTask(trow); err != nil {
			return err
		}
		return tx.Event("attempt.failed", map[string]any{"task": a.t.def.ID, "attempt": a.n, "stage": stage, "error": row.Error})
	})
}

// SubmitResult takes the result the session submits for its attempt: the
// attempt must be its task's active, running one; the worktree clean apart
// from the task files; HEAD on the attempt's branch, descending from every
// input (a review's equal to its candidate); the result tree without a
// path under .conductor/ or a submodule. The result is recorded, and the
// task is verified (its checks, in a checkout of their own) and decided by
// the controller: a task with no checks succeeds at once. A second submit
// of the same commit while the first is verified answers as the first;
// another is refused. The attempt and result are returned with the state
// the task is in.
func (e *Engine) SubmitResult(ctx context.Context, sessionID string, res Result) (TaskState, error) {
	v, ok := e.bySession.Load(sessionID)
	if !ok {
		return TaskState{}, ErrNotAnAttempt
	}
	sm := v.(sessionMember)
	r, a := sm.r, sm.a
	if a == nil {
		return TaskState{}, ErrNotAnAttempt
	}
	t := a.t
	if err := res.validate(t.def.Kind); err != nil {
		return TaskState{}, err
	}
	e.mu.Lock()
	if e.runs[r.id] != r || t.active() != a {
		e.mu.Unlock()
		return TaskState{}, ErrStaleAttempt
	}
	switch a.state {
	case ledger.AttemptSubmitting, ledger.AttemptVerifying:
		// The same result again: the first answer stands.
		if a.submitted != nil && (res.Commit == "" || res.Commit == a.result) {
			st := r.taskStates()[slices.Index(r.tasks, t)]
			e.mu.Unlock()
			return st, nil
		}
		e.mu.Unlock()
		return TaskState{}, ErrStaleAttempt
	case ledger.AttemptRunning:
	default:
		e.mu.Unlock()
		return TaskState{}, fmt.Errorf("%w: it is %s", ErrNotRunning, a.state)
	}
	a.state = ledger.AttemptSubmitting
	worktree, start, inputs := a.worktree, a.start, slices.Clone(a.inputs)
	e.mu.Unlock()
	back := func(err error) (TaskState, error) {
		e.mu.Lock()
		if a.state == ledger.AttemptSubmitting {
			a.state = ledger.AttemptRunning
		}
		e.mu.Unlock()
		return TaskState{}, err
	}
	// The tree: clean apart from the task files.
	_, changes, _, err := gitcli.Porcelain(ctx, worktree)
	if err != nil {
		return back(fmt.Errorf("reading the worktree: %w", err))
	}
	var dirty []string
	for _, c := range changes {
		if c.Path == taskDir || strings.HasPrefix(c.Path, taskDir+"/") {
			continue
		}
		dirty = append(dirty, c.Path)
	}
	if len(dirty) > 0 {
		if len(dirty) > 20 {
			dirty = append(dirty[:20], "…")
		}
		return back(&DirtyWorktreeError{Paths: dirty})
	}
	head, err := headCommit(ctx, worktree)
	if err != nil {
		return back(fmt.Errorf("reading the worktree's commit: %w", err))
	}
	if res.Commit != "" && res.Commit != head {
		return back(fmt.Errorf("%w: the worktree is at %s, the result names %s", ErrCommitMissing, short(head), short(res.Commit)))
	}
	if t.def.Kind == TaskReview {
		if head != start {
			return back(fmt.Errorf("%w: it is at %s, the candidate is %s", ErrReviewChanged, short(head), short(start)))
		}
	} else {
		for _, in := range inputs {
			if in.commit == "" {
				continue
			}
			if ok, err := isAncestor(ctx, worktree, in.commit, head); err != nil {
				return back(err)
			} else if !ok {
				return back(fmt.Errorf("%w: %s does not descend from %s's result %s", ErrNotFromInput, short(head), in.task, short(in.commit)))
			}
		}
		if len(inputs) == 0 && start != "" {
			if ok, err := isAncestor(ctx, worktree, start, head); err != nil {
				return back(err)
			} else if !ok {
				return back(fmt.Errorf("%w: %s does not descend from the run's base %s", ErrNotFromInput, short(head), short(start)))
			}
		}
		reserved, links, err := treeReserved(ctx, worktree, head)
		if err != nil {
			return back(err)
		}
		if reserved {
			return back(ErrReservedPath)
		}
		if links {
			return back(ErrSubmodules)
		}
	}
	// Recorded, then decided.
	e.mu.Lock()
	if t.active() != a || a.state != ledger.AttemptSubmitting {
		e.mu.Unlock()
		return TaskState{}, ErrStaleAttempt
	}
	submitted := res
	a.submitted, a.result = &submitted, head
	if t.def.Kind == TaskReview {
		a.result = ""
	}
	a.state = ledger.AttemptVerifying
	t.state = ledger.TaskVerifying
	row, trow := r.attemptRow(a), r.taskRow(t)
	r.note(session.ActivityStatus, "task %s: attempt %d submitted %s", t.def.ID, a.n, resultWords(t.def.Kind, head, res))
	e.mu.Unlock()
	if err := e.transition(ctx, r, func(tx LedgerTx) error {
		if err := tx.PutAttempt(row); err != nil {
			return err
		}
		if err := tx.PutTask(trow); err != nil {
			return err
		}
		return tx.Event("result.submitted", map[string]any{"task": t.def.ID, "attempt": a.n, "commit": a.result, "verdict": res.Verdict, "summary": res.Summary})
	}); err != nil {
		return back(err)
	}
	r.checks.Add(1)
	go e.verify(r, a)
	e.mu.Lock()
	st := r.taskStates()[slices.Index(r.tasks, t)]
	e.mu.Unlock()
	return st, nil
}

func resultWords(kind, commit string, res Result) string {
	if kind == TaskReview {
		return fmt.Sprintf("the verdict %s with %d findings", res.Verdict, len(res.Findings))
	}
	return "commit " + short(commit)
}

// isAncestor reports whether a is an ancestor of b, or b itself.
func isAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	if a == b {
		return true, nil
	}
	_, err := gitcli.Run(ctx, dir, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "exit status 1") {
		return false, nil
	}
	return false, err
}

// treeReserved reports whether the commit's tree holds a path under
// .conductor/, and whether it holds a gitlink (a submodule).
func treeReserved(ctx context.Context, dir, commit string) (reserved, links bool, err error) {
	out, err := gitcli.Run(ctx, dir, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return false, false, err
	}
	for _, line := range strings.Split(out, "\x00") {
		if line == "" {
			continue
		}
		meta, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if strings.HasPrefix(meta, "160000 ") {
			links = true
		}
		if path == taskDir || strings.HasPrefix(path, taskDir+"/") {
			reserved = true
		}
	}
	return reserved, links, nil
}

// accept records the decision on a's result: succeeded with the result,
// or failed at a stage. The caller does not hold e.mu.
func (e *Engine) accept(r *run, a *attempt, ok bool, stage, errText string) {
	t := a.t
	e.mu.Lock()
	if !a.closedAt.IsZero() {
		// Closed meanwhile (cancelled, stopped): what closed it wrote it.
		e.mu.Unlock()
		return
	}
	{
		if ok {
			res := a.submitted
			t.result = &taskResult{attempt: a.n, commit: a.result, summary: res.Summary, issues: slices.Clone(res.Issues), verdict: res.Verdict, findings: slices.Clone(res.Findings), at: time.Now().UTC()}
			r.closeAttempt(a, ledger.AttemptSucceeded, "", "")
		} else {
			r.closeAttempt(a, ledger.AttemptFailed, stage, errText)
		}
	}
	rows := []ledger.TaskRow{r.taskRow(t)}
	for _, other := range r.tasks {
		if other != t {
			rows = append(rows, r.taskRow(other))
		}
	}
	row := r.attemptRow(a)
	e.pump(r)
	runRow := r.runRow(r.crewJSON)
	outcome := r.outcome
	e.mu.Unlock()
	kind := "task.succeeded"
	if !ok {
		kind = "attempt.failed"
	}
	_ = e.transition(context.Background(), r, func(tx LedgerTx) error {
		if err := tx.PutAttempt(row); err != nil {
			return err
		}
		for _, tr := range rows {
			if err := tx.PutTask(tr); err != nil {
				return err
			}
		}
		if outcome != "" {
			if err := tx.PutRun(runRow); err != nil {
				return err
			}
			if err := tx.Event("run.finished", map[string]any{"outcome": outcome}); err != nil {
				return err
			}
		}
		return tx.Event(kind, map[string]any{"task": t.def.ID, "attempt": a.n, "stage": stage, "error": errText, "commit": a.result})
	})
}

// PauseRun pauses a managed run's dispatch: attempts in flight go on,
// none starts. UnpauseRun dispatches what is ready.
func (e *Engine) PauseRun(ctx context.Context, runID string, paused bool) error {
	e.mu.Lock()
	r, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return ErrRunNotFound
	}
	if !r.managed {
		e.mu.Unlock()
		return fmt.Errorf("%w: a legacy run is not paused", ErrTaskState)
	}
	if r.stoppedAt != nil {
		e.mu.Unlock()
		return ErrRunStopped
	}
	r.paused = paused
	if paused {
		r.note(session.ActivityStatus, "dispatch paused")
	} else {
		r.note(session.ActivityStatus, "dispatch resumed")
		e.pump(r)
	}
	e.mu.Unlock()
	return e.transition(ctx, r, func(tx LedgerTx) error {
		return tx.Event("run.paused", map[string]any{"paused": paused})
	})
}

// CancelTask cancels a task: its attempt in flight is ended (its session
// stopped), the task is cancelled, and the tasks that need it are blocked
// for good.
func (e *Engine) CancelTask(ctx context.Context, runID, taskID string) error {
	e.mu.Lock()
	r, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return ErrRunNotFound
	}
	t := r.task(taskID)
	if t == nil {
		e.mu.Unlock()
		return ErrTaskNotFound
	}
	switch t.state {
	case ledger.TaskSucceeded, ledger.TaskFailed, ledger.TaskCancelled:
		e.mu.Unlock()
		return fmt.Errorf("%w: it is %s", ErrTaskState, t.state)
	}
	a := t.active()
	var sessionID string
	if a != nil {
		sessionID = a.sessionID
		r.closeAttempt(a, ledger.AttemptCancelled, a.state, "cancelled")
	} else {
		t.state, t.reason = ledger.TaskCancelled, "cancelled"
		r.ready()
	}
	rows := []ledger.TaskRow{}
	for _, other := range r.tasks {
		rows = append(rows, r.taskRow(other))
	}
	var row *ledger.AttemptRow
	if a != nil {
		ar := r.attemptRow(a)
		row = &ar
	}
	r.note(session.ActivityStatus, "task %s cancelled", taskID)
	e.pump(r)
	e.mu.Unlock()
	if sessionID != "" {
		if l, ok := e.lookup(sessionID); ok {
			stopLocal(l)
		}
	}
	return e.transition(ctx, r, func(tx LedgerTx) error {
		if row != nil {
			if err := tx.PutAttempt(*row); err != nil {
				return err
			}
		}
		for _, tr := range rows {
			if err := tx.PutTask(tr); err != nil {
				return err
			}
		}
		return tx.Event("task.cancelled", map[string]any{"task": taskID})
	})
}

// RetryTask gives a failed or cancelled task one more attempt: it is ready
// again, dispatched as the run takes it.
func (e *Engine) RetryTask(ctx context.Context, runID, taskID string) error {
	e.mu.Lock()
	r, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return ErrRunNotFound
	}
	t := r.task(taskID)
	if t == nil {
		e.mu.Unlock()
		return ErrTaskNotFound
	}
	if t.state != ledger.TaskFailed && t.state != ledger.TaskCancelled {
		e.mu.Unlock()
		return fmt.Errorf("%w: it is %s", ErrTaskState, t.state)
	}
	if len(t.attempts) >= maxAttemptsPerTask+t.extra {
		t.extra++
	}
	t.state, t.reason = ledger.TaskBlocked, ""
	r.outcome = ""
	r.ready()
	r.note(session.ActivityStatus, "task %s: retried, one more attempt allowed", taskID)
	e.pump(r)
	rows := []ledger.TaskRow{}
	for _, other := range r.tasks {
		rows = append(rows, r.taskRow(other))
	}
	e.mu.Unlock()
	return e.transition(ctx, r, func(tx LedgerTx) error {
		for _, tr := range rows {
			if err := tx.PutTask(tr); err != nil {
				return err
			}
		}
		return tx.Event("task.retried", map[string]any{"task": taskID})
	})
}

// AttemptOf reports the task and attempt a session is, in a managed run.
func (e *Engine) AttemptOf(sessionID string) (runID, taskID string, n int, ok bool) {
	v, found := e.bySession.Load(sessionID)
	if !found {
		return "", "", 0, false
	}
	sm := v.(sessionMember)
	if sm.a == nil {
		return "", "", 0, false
	}
	return sm.r.id, sm.a.t.def.ID, sm.a.n, true
}

// taskLine is the line typed into an attempt's session: where its task is,
// how to submit, and what a done is not.
func taskLine(t Task, n int) string {
	if t.Kind == TaskReview {
		return fmt.Sprintf("Conductor review %q, attempt %d: read %s/task.md here, or call the task_context tool, and review the candidate it names. Do not commit. Submit your verdict once with the task_result tool, or \"conductor task result --verdict approved|needs_changes --finding ...\". A done or a handoff does not finish a task.", t.ID, n, taskDir)
	}
	return fmt.Sprintf("Conductor task %q, attempt %d: read %s/task.md here, or call the task_context tool, and do it. Commit your work, then submit it once with the task_result tool, or \"conductor task result --summary ...\". A done or a handoff does not finish a task.", t.ID, n, taskDir)
}
