package crew

import (
	"context"
	"encoding/json"
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
// it (verify). Every change of state is made and written to the ledger in
// one transaction of the run's (transact), and the side effect that rests
// on it follows the commit: a dispatch, a session, a typed line or a check
// follows its row, never the other way round. While a transaction commits,
// the run as read is one change ahead of the ledger; a commit that fails
// is undone, or, where it cannot be, pauses the run's dispatch and says so.

// Bounds of a managed run.
const (
	maxActiveAttempts  = 4 // attempts in flight per run
	maxAttemptsPerTask = 3
	// maxTaskFile bounds .conductor/task.md and task.json.
	maxTaskFile = 64 << 10
	// taskDir is the directory of an attempt's task files, in its working
	// directory. A path with that component anywhere is Conductor's.
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

// LedgerTx is a transaction's writer: ledger.Tx, or a test's fake.
type LedgerTx interface {
	Event(kind string, payload any) error
	PutRun(ledger.RunRow) error
	PutTask(ledger.TaskRow) error
	PutAttempt(ledger.AttemptRow) error
	PutCheck(ledger.CheckRow) error
	// Command and Settle keep a command's receipt (commands.go).
	Command(actor, id, intent, fingerprint string) (prior ledger.CommandRow, found bool, err error)
	Settle(actor, id, status string, receipt any) error
	// Continued marks the run from as continued by this transaction's run,
	// once (continue.go): ledger.ErrContinued when it was already.
	Continued(from string) error
	// PutMessage keeps a message a member sent another (messages.go).
	PutMessage(ledger.MessageRow) error
}

// Ledger is the managed runs' ledger as the engine writes it: one
// transition at a time, committed before the side effect it precedes.
// Available says whether it takes transitions now: a ledger that is
// read-only (another server holds the owner lock, for a while after a
// restart) refuses a managed launch with why.
type Ledger interface {
	Transition(ctx context.Context, runID string, fn func(LedgerTx) error) (revision int64, err error)
	Available() error
	// Receipt reads a command's receipt (commands.go).
	Receipt(ctx context.Context, runID, actor, id string) (ledger.CommandRow, bool, error)
	// Load reads a run with its tasks and attempts, for a continuation
	// (continue.go); ok is false for a run the ledger has no row of.
	Load(ctx context.Context, runID string) (from Loaded, ok bool, err error)
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

func (s sqlLedger) Receipt(ctx context.Context, runID, actor, id string) (ledger.CommandRow, bool, error) {
	return s.l.Receipt(ctx, runID, actor, id)
}

func (s sqlLedger) Load(ctx context.Context, runID string) (Loaded, bool, error) {
	row, ok, err := s.l.Run(ctx, runID)
	if err != nil || !ok {
		return Loaded{}, ok, err
	}
	tasks, err := s.l.Tasks(ctx, runID)
	if err != nil {
		return Loaded{}, true, err
	}
	attempts, err := s.l.Attempts(ctx, runID)
	if err != nil {
		return Loaded{}, true, err
	}
	messages, err := s.l.Messages(ctx, runID)
	if err != nil {
		return Loaded{}, true, err
	}
	return Loaded{Run: row, Tasks: tasks, Attempts: attempts, Messages: messages}, true, nil
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
	ErrOffBranch     = errors.New("the worktree is not on the attempt's branch")
	ErrNotFromInput  = errors.New("the result does not descend from the task's inputs")
	ErrReviewChanged = errors.New("a review submits no commit: its tree must be the candidate's")
	ErrReservedPath  = errors.New("the result tree holds a path with a .conductor directory, which is Conductor's")
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
	// repair is set when a review of the task said needs_changes: the next
	// attempt starts at the commit reviewed, with the findings in its task
	// file; cleared once the task has a result again.
	repair *repair
	// prior counts the attempts spent in the runs this one continues
	// (continue.go): they number the attempts and count against the task.
	prior int
	// imported names the result a continuation took by reference, as
	// "<run>/<task>/<n>"; "" for a result made in this run.
	imported string
	// candidate is a result a continuation verifies again, before any
	// attempt of its own (reverify).
	candidate *candidate
	// blocker is what the attempt in flight says stops it (checkpoints.go),
	// nil once it works on or the attempt ends.
	blocker *blocker
	// checkpoint is the task's latest, from any attempt, this run's or the
	// run's it continues: the next attempt's task file says where the
	// last one got to, and an attempt after one that ended without a
	// result starts there.
	checkpoint *checkpoint
}

// repair is what a review asks of the task it reviewed.
type repair struct {
	review   string
	attempt  int // the review's attempt
	commit   string
	summary  string
	findings []Finding
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
	// branch is the result attempt's, when the attempt is not this run's
	// (imported): a dependent's task file names it.
	branch string
}

// attempt is one try at a task: one member session in one worktree.
type attempt struct {
	t *task
	m *member
	// checkpoint is the attempt's last checkpoint (checkpoints.go).
	checkpoint *checkpoint
	n          int
	state      string // ledger.Attempt*
	stage      string
	err        string
	ctx        context.Context // ends when the attempt closes
	cancel     context.CancelFunc

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
	// provisional says the attempt has no place yet (dispatch has not
	// reserved its row): the ledger does not see it, nor count it, until
	// the reservation is committed, so one refused leaves no row behind.
	provisional bool
	// sessionEnded says the session ended while a result was being taken
	// (submitting or verifying): a submission refused then fails the
	// attempt instead of leaving it running with no session to watch.
	sessionEnded bool
	// submitSeq numbers the submissions taken, so a refused one puts back
	// its own state and never a later one's.
	submitSeq int
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
	// ImportedFrom names the result a continuation took by reference, as
	// "<run>/<task>/<n>" (continue.go).
	ImportedFrom string `json:"importedFrom,omitempty"`
	// Blocker is what the attempt in flight says stops it (checkpoints.go).
	Blocker *Blocker `json:"blocker,omitempty"`
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
	// Submitted is what the attempt submitted, in a task's own read
	// (Engine.TaskOf); the run's read leaves it out.
	Submitted *Result `json:"submitted,omitempty"`
	// Checkpoint is the attempt's last checkpoint (checkpoints.go).
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
}

// ResultState is a task's accepted result as the run reports it.
type ResultState struct {
	Attempt  int       `json:"attempt"`
	Commit   string    `json:"commit,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Issues   []string  `json:"issues,omitempty"`
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
		st := TaskState{ID: t.def.ID, Member: t.def.Member, Kind: t.def.Kind, Needs: slices.Clone(t.def.Needs), State: t.state, Reason: t.reason, Attempts: len(t.attempts) + t.prior, ImportedFrom: t.imported}
		if b := t.blocker; b != nil {
			st.Blocker = &Blocker{Reason: b.reason, Message: b.message, By: b.by, At: b.at}
		}
		if n := len(t.attempts); n > 0 {
			as := attemptStateOf(t.attempts[n-1])
			st.Attempt = &as
		}
		if res := t.result; res != nil {
			st.Result = &ResultState{Attempt: res.attempt, Commit: res.commit, Summary: res.summary, Issues: slices.Clone(res.issues), Verdict: res.verdict, Findings: slices.Clone(res.findings), At: res.at}
		}
		out = append(out, st)
	}
	return out
}

// taskStateOf is t as reported. The caller holds e.mu.
func (r *run) taskStateOf(t *task) TaskState {
	return r.taskStates()[slices.Index(r.tasks, t)]
}

// attemptStateOf is a as reported. The caller holds e.mu.
func attemptStateOf(a *attempt) AttemptState {
	as := AttemptState{N: a.n, SessionID: a.sessionID, Worktree: a.worktree, Branch: a.branch, Start: a.start, Result: a.result, State: a.state, Stage: a.stage, Error: a.err}
	if !a.launchedAt.IsZero() {
		at := a.launchedAt
		as.StartedAt = &at
	}
	if !a.closedAt.IsZero() {
		at := a.closedAt
		as.EndedAt = &at
	}
	if c := a.checkpoint; c != nil {
		as.Checkpoint = c.api()
	}
	return as
}

// The rows changed since the last transition, marked by every change of
// state and drained by the next transition's snapshot (transition). The
// caller holds e.mu.

func (r *run) markTask(t *task) {
	if r.changedTasks == nil {
		r.changedTasks = map[*task]struct{}{}
	}
	r.changedTasks[t] = struct{}{}
}

func (r *run) markAttempt(a *attempt) {
	if r.changedAttempts == nil {
		r.changedAttempts = map[*attempt]struct{}{}
	}
	r.changedAttempts[a] = struct{}{}
}

func (r *run) markRun() { r.runChanged = true }

// dirty reports whether a change waits for a commit. The caller holds e.mu.
func (r *run) dirty() bool {
	return r.runChanged || len(r.changedTasks) > 0 || len(r.changedAttempts) > 0
}

// snapshot is what one transition writes: the rows of what changed, as
// they were when it was taken.
type snapshot struct {
	run      *ledger.RunRow
	tasks    []ledger.TaskRow
	attempts []ledger.AttemptRow
	ts       []*task
	as       []*attempt
}

// drain takes the rows changed since the last transition, in the run's
// order, and clears the marks. The caller holds e.mu.
func (r *run) drain() snapshot {
	var s snapshot
	if r.runChanged {
		row := r.runRow(r.crewJSON)
		s.run = &row
		r.runChanged = false
	}
	for _, t := range r.tasks {
		if _, ok := r.changedTasks[t]; ok {
			s.tasks = append(s.tasks, r.taskRow(t))
			s.ts = append(s.ts, t)
		}
		for _, a := range t.attempts {
			if _, ok := r.changedAttempts[a]; ok && !a.provisional {
				s.attempts = append(s.attempts, r.attemptRow(a))
				s.as = append(s.as, a)
			}
		}
	}
	clear(r.changedTasks)
	clear(r.changedAttempts)
	return s
}

// write puts the snapshot's rows: the run before its tasks, the tasks
// before their attempts.
func (s snapshot) write(tx LedgerTx) error {
	if s.run != nil {
		if err := tx.PutRun(*s.run); err != nil {
			return err
		}
	}
	for _, row := range s.tasks {
		if err := tx.PutTask(row); err != nil {
			return err
		}
	}
	for _, row := range s.attempts {
		if err := tx.PutAttempt(row); err != nil {
			return err
		}
	}
	return nil
}

// undo marks the snapshot's rows changed again, for the next transition,
// after a commit that failed. The caller holds e.mu.
func (s snapshot) undo(r *run) {
	if s.run != nil {
		r.markRun()
	}
	for _, t := range s.ts {
		r.markTask(t)
	}
	for _, a := range s.as {
		r.markAttempt(a)
	}
}

// event is one event of a transaction.
type event struct {
	kind    string
	payload map[string]any
}

// tx is what a transaction's change adds to its rows: its events, and
// rows of its own (a check's) written by more.
type tx struct {
	events []event
	more   func(LedgerTx) error
}

// ev is a transaction of one event.
func ev(kind string, payload map[string]any) tx {
	return tx{events: []event{{kind, payload}}}
}

// transact makes one change of r and records it: under r.txMu, mutate
// runs under e.mu and changes the state (marking what it changes), the
// rows changed are snapshotted there (drain), and the commit follows
// without e.mu. A run's transactions thus commit in the order their
// changes were made, each carrying the state as it was then, and no other
// transaction takes a change's rows. On failure the snapshot's rows are
// marked again for the next transaction (those of earlier failures among
// them), undo, when given, puts this change's state back under e.mu, and
// an end pending stays pending; the caller pauses the run's dispatch where
// the state stays ahead (afterTransact). A change that sets the run's
// outcome reports the end once committed. Nothing is written for a change
// that changed nothing.
func (e *Engine) transact(ctx context.Context, r *run, mutate func() tx, undo func()) error {
	if e.ledger == nil {
		return ErrNoLedger
	}
	r.txMu.Lock()
	defer r.txMu.Unlock()
	e.mu.Lock()
	t := mutate()
	snap := r.drain()
	e.mu.Unlock()
	if len(t.events) == 0 && t.more == nil && snap.run == nil && len(snap.tasks) == 0 && len(snap.attempts) == 0 {
		return nil
	}
	rev, err := e.ledger.Transition(ctx, r.id, func(ltx LedgerTx) error {
		if err := snap.write(ltx); err != nil {
			return err
		}
		if t.more != nil {
			if err := t.more(ltx); err != nil {
				return err
			}
		}
		for _, ev := range t.events {
			if err := ltx.Event(ev.kind, ev.payload); err != nil {
				return err
			}
		}
		return nil
	})
	e.mu.Lock()
	if err != nil {
		snap.undo(r)
		if undo != nil {
			undo()
		}
		e.mu.Unlock()
		return err
	}
	r.revision = rev
	r.touch()
	if r.endPending {
		r.endPending = false
		e.noteEndLater(r)
	}
	// The attempts whose cancel this commit made durable: their contexts
	// end (a check or a setup under way with them) and their sessions go.
	for _, cancel := range r.pendingCancels {
		cancel()
	}
	r.pendingCancels = nil
	stops := r.pendingStops
	r.pendingStops = nil
	e.mu.Unlock()
	for _, id := range stops {
		if l, ok := e.lookup(id); ok {
			go stopLocal(l)
		}
	}
	return nil
}

// pumpLocked dispatches the ready tasks on the state as committed: under
// r.txMu, so no transaction is between its change and its commit while
// the dispatch decides. The caller holds neither lock.
func (e *Engine) pumpLocked(r *run) {
	r.txMu.Lock()
	e.mu.Lock()
	e.pump(r)
	e.mu.Unlock()
	r.txMu.Unlock()
}

// afterTransact takes a transaction's outcome for what rests on it:
// committed, the ready tasks are dispatched; failed, the run's dispatch
// pauses until a person looks, the run log says why, and the pause's own
// commit carries the change's rows. It reports whether the change is
// durable now, by the one commit or the other: a side effect that must
// follow the change (a session stopped) waits for that. The caller holds
// neither lock.
func (e *Engine) afterTransact(r *run, err error) (durable bool) {
	if err == nil {
		e.pumpLocked(r)
		return true
	}
	return e.pauseFor(r, err) == nil
}

// pauseFor pauses the run's dispatch after the ledger refused a
// transaction, in a transaction that carries what the refused one could
// not. The caller holds neither lock.
func (e *Engine) pauseFor(r *run, err error) error {
	return e.transact(context.Background(), r, func() tx {
		return pausedTx(e.pauseDispatch(r, err), err)
	}, nil)
}

// pauseDispatch pauses the run's dispatch after the ledger refused a
// transaction: nothing starts until a person unpauses it; it reports
// whether it paused. A transaction the run's own stop cancelled (the
// refusal is the cancellation: an attempt's line recorded as the stop
// lands) pauses nothing: nothing dispatches in a run that stops, and the
// pause would be written into its row for its continuation to inherit,
// which then never dispatched (#367). A refusal that is no cancellation,
// the stop under way or not, pauses as ever: the ledger failed for real,
// and the continuation of a run that stopped so starts paused, for a
// person to look at (Codex's review of #367). The caller holds e.mu.
func (e *Engine) pauseDispatch(r *run, err error) bool {
	if r.ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		r.note(session.ActivityStatus, "the ledger did not record the run's state as it stopped: %v", err)
		return false
	}
	if !r.paused {
		r.paused = true
		r.markRun()
	}
	r.note(session.ActivityError, "dispatch paused: the ledger could not record the run's state: %v", err)
	return true
}

// pausedTx is the transaction's events after pauseDispatch: the pause's,
// when it paused; none when the run's stop made the pause moot.
func pausedTx(paused bool, err error) tx {
	if !paused {
		return tx{}
	}
	return ev("run.paused", map[string]any{"paused": true, "why": session.CleanMessage(err.Error())})
}

// settleRun derives the run's outcome once every task is terminal and,
// the first time it is, says so; the end is reported once the change is
// committed (transact). The caller holds e.mu, in a transaction's change.
func (e *Engine) settleRun(r *run) {
	if r.outcome != "" {
		return
	}
	r.settle()
	if r.outcome != "" {
		r.markRun()
		r.endPending = true
		r.note(session.ActivityStatus, "every task is done: the run %s", r.outcome)
	}
}

// settle derives the run's outcome once every task is terminal: succeeded
// when all succeeded, cancelled when one was cancelled and none failed,
// failed otherwise; "" while a task is not terminal. The caller holds e.mu.
func (r *run) settle() {
	failed, cancelled := false, false
	doomed := r.doomed()
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
			if !doomed[t] {
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

// doomed is, for every task, whether it waits behind a need that failed or
// was cancelled, or behind one so doomed: each task and need visited once,
// so a dense graph costs its edges. The caller holds e.mu.
func (r *run) doomed() map[*task]bool {
	out := make(map[*task]bool, len(r.tasks))
	var visit func(t *task) bool
	visit = func(t *task) bool {
		if d, ok := out[t]; ok {
			return d
		}
		out[t] = false // a cycle (none passes validation) resolves as not doomed
		d := false
		for _, n := range t.def.Needs {
			need := r.task(n)
			if need == nil {
				d = true
				break
			}
			switch need.state {
			case ledger.TaskFailed, ledger.TaskCancelled:
				d = true
			case ledger.TaskBlocked:
				d = visit(need)
			}
			if d {
				break
			}
		}
		out[t] = d
		return d
	}
	for _, t := range r.tasks {
		visit(t)
	}
	return out
}

// ready marks the blocked tasks whose needs all have results ready, and
// says what blocks the others. The caller holds e.mu.
func (r *run) ready() {
	var doomed map[*task]bool
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
		if doomed == nil {
			doomed = r.doomed()
		}
		state, reason := ledger.TaskReady, ""
		switch {
		case len(waiting) == 0:
		case doomed[t]:
			state, reason = ledger.TaskBlocked, "needs "+strings.Join(waiting, ", ")+", which will not succeed"
		default:
			state, reason = ledger.TaskBlocked, "needs "+strings.Join(waiting, ", ")
		}
		if state != t.state || reason != t.reason {
			t.state, t.reason = state, reason
			r.markTask(t)
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
		branch, commit := need.result.branch, need.result.commit
		for _, a := range need.attempts {
			if a.n == need.result.attempt {
				branch = a.branch
				if commit == "" && need.def.Kind == TaskReview {
					// A review submits no commit: its result is its verdict
					// on its candidate, the commit its attempt started at,
					// which is what a task needing it starts from (#351).
					commit = a.start
				}
			}
		}
		out = append(out, input{task: n, attempt: need.result.attempt, commit: commit, summary: need.result.summary, branch: branch, verdict: need.result.verdict})
	}
	return out
}

// startCommit is where t's next attempt starts: the result of its last
// attempt when that failed a check (the work is kept; the task file says
// what failed), else its first need's result, else the run's base.
func (r *run) startCommit(t *task) string {
	if t.repair != nil && t.repair.commit != "" {
		return t.repair.commit
	}
	if t.def.Kind == TaskReview {
		// A review always starts at its candidate, whatever its attempts
		// did: its result is its verdict on that commit.
		for _, in := range r.inputs(t) {
			if in.commit != "" {
				return in.commit
			}
		}
		return r.base
	}
	if n := len(t.attempts); n > 0 {
		last := t.attempts[n-1]
		if last.state == ledger.AttemptFailed && last.result != "" {
			return last.result
		}
		if last.state == ledger.AttemptFailed && last.result == "" && last.checkpoint != nil && last.checkpoint.commit != "" {
			// Failed without a result (its session went): its work up to
			// the checkpoint is kept. A withdrawn or cancelled attempt's
			// checkpoint rested on an input since changed: not used.
			return last.checkpoint.commit
		}
	} else if t.checkpoint != nil && t.checkpoint.commit != "" {
		// A continuation's first attempt: where the last one got to.
		return t.checkpoint.commit
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
// holds e.mu, and has committed the state the dispatch rests on; the
// dispatches run on goroutines of their own.
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
		if m == nil {
			continue
		}
		if t.candidate != nil {
			// A continuation's result to verify again: no session, so the
			// member's own attempt elsewhere does not matter.
			e.reverify(r, t, m)
			continue
		}
		if r.activeAttempt(m) != nil {
			continue
		}
		if len(t.attempts)+t.prior >= maxAttemptsPerTask+t.extra {
			t.state, t.reason = ledger.TaskFailed, fmt.Sprintf("%d attempts made; retry it to allow one more", len(t.attempts)+t.prior)
			r.markTask(t)
			e.settleRun(r)
			continue
		}
		ctx, cancel := context.WithCancel(r.ctx)
		a := &attempt{t: t, m: m, n: len(t.attempts) + t.prior + 1, state: ledger.AttemptReserved, ctx: ctx, cancel: cancel, reservedAt: time.Now().UTC(), inputs: r.inputs(t), start: r.startCommit(t), provisional: true}
		t.attempts = append(t.attempts, a)
		t.state, t.reason = ledger.TaskRunning, ""
		r.active++
		r.markTask(t)
		r.markAttempt(a)
		r.starts.Add(1)
		r.note(session.ActivityStatus, "task %s: attempt %d reserved for %s", t.def.ID, a.n, m.def.Name)
		go e.dispatch(ctx, r, a)
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
	if err := e.transact(ctx, r, func() tx {
		r.base = base
		r.markRun()
		for _, t := range r.tasks {
			r.markTask(t)
		}
		return ev("run.created", map[string]any{"base": base, "tasks": len(r.tasks)})
	}, nil); err != nil {
		return err
	}
	e.pumpLocked(r)
	return nil
}

// recordStop ends a managed run's attempts in flight as stopped and
// writes the stop: the attempts, their tasks, the run's row; the end is
// told once it is committed. The caller does not hold e.mu; the run is
// stopped. The error is the ledger's refusal: the stop stays pending.
func (e *Engine) recordStop(r *run) error {
	return e.transact(context.Background(), r, func() tx {
		n := 0
		for _, t := range r.tasks {
			if a := t.active(); a != nil {
				e.closeAttempt(r, a, ledger.AttemptStopped, a.state, "the run was stopped")
				n++
			}
		}
		r.markRun()
		r.endPending = true
		return ev("run.stopped", map[string]any{"attempts": n})
	}, nil)
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
// blocked for good; stopped leaves it ready for a continuation. The
// caller holds e.mu, and writes the transition before any dispatch.
func (e *Engine) closeAttempt(r *run, a *attempt, state, stage, errText string) {
	if !a.closedAt.IsZero() {
		return
	}
	e.endAttempt(r, a, state, stage, errText)
	t := a.t
	e.withdrawBlocker(t.blocker)
	t.blocker = nil
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
		if len(t.attempts)+t.prior < maxAttemptsPerTask+t.extra && state == ledger.AttemptFailed {
			t.state, t.reason = ledger.TaskReady, fmt.Sprintf("attempt %d failed at %s: %s", a.n, stage, errText)
		} else {
			t.state, t.reason = ledger.TaskFailed, fmt.Sprintf("attempt %d %s at %s: %s", a.n, state, stage, errText)
		}
	}
	r.markTask(t)
	r.ready()
	r.note(session.ActivityStatus, "task %s: attempt %d %s%s", t.def.ID, a.n, state, suffix(stage, errText))
	// A review that asks for changes reopens the task it reviewed next
	// (accept → reopen), which settles the run then: settled here, the run
	// would end for a moment on a result about to be withdrawn.
	if state == ledger.AttemptSucceeded && t.def.Kind == TaskReview && a.submitted != nil && a.submitted.Verdict == VerdictNeedsChanges {
		return
	}
	e.settleRun(r)
}

// endAttempt ends a with its state, stage and error, and lets the run's
// count go: a close's bookkeeping, without settling the task. The caller
// holds e.mu.
func (e *Engine) endAttempt(r *run, a *attempt, state, stage, errText string) {
	a.state, a.stage, a.err, a.closedAt = state, stage, errText, time.Now().UTC()
	a.provisional = false // closed, it is an attempt made, whatever it got to
	if state == ledger.AttemptCancelled {
		// Its context ends once the cancel is committed (pendingCancels).
		r.pendingCancels = append(r.pendingCancels, a.cancel)
	} else {
		a.cancel()
	}
	r.active--
	r.markAttempt(a)
}

// unreserve takes back an attempt nothing was made for (no place among
// the sessions, or the ledger refused its row): it is removed, and the
// task is ready again with why. One closed meanwhile (cancelled, stopped)
// was accounted for by what closed it. The caller holds e.mu.
func (r *run) unreserve(a *attempt, reason string) {
	if !a.closedAt.IsZero() {
		return
	}
	t := a.t
	a.closedAt = time.Now().UTC()
	a.cancel()
	if i := slices.Index(t.attempts, a); i >= 0 {
		t.attempts = slices.Delete(t.attempts, i, i+1)
	}
	delete(r.changedAttempts, a)
	r.active--
	if t.state == ledger.TaskRunning {
		t.state, t.reason = ledger.TaskReady, reason
	}
	r.markTask(t)
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

// attemptRow is a as the ledger keeps it. The caller holds e.mu.
func (r *run) attemptRow(a *attempt) ledger.AttemptRow {
	state := a.state
	if state == ledger.AttemptSubmitting {
		// A result being taken: running until it is recorded.
		state = ledger.AttemptRunning
	}
	row := ledger.AttemptRow{RunID: r.id, TaskID: a.t.def.ID, N: a.n, Member: a.m.def.Name, SessionID: a.sessionID, Worktree: a.worktree, Branch: a.branch,
		StartCommit: a.start, ResultCommit: a.result, State: state, Stage: a.stage, Error: a.err,
		ReservedAt: a.reservedAt, LaunchedAt: a.launchedAt, PromptedAt: a.promptedAt, ClosedAt: a.closedAt}
	if a.submitted != nil {
		// The whole result, bounded by its validation, so a review's
		// findings and a work task's issues outlive the server.
		if b, err := json.Marshal(a.submitted); err == nil {
			row.Result = string(b)
		}
	}
	if a.checkpoint != nil {
		if b, err := json.Marshal(a.checkpoint.api()); err == nil {
			row.Checkpoint = string(b)
		}
	}
	return row
}

// taskRow is t as the ledger keeps it. The caller holds e.mu.
func (r *run) taskRow(t *task) ledger.TaskRow {
	n := 0
	for _, a := range t.attempts {
		if !a.provisional {
			n++
		}
	}
	row := ledger.TaskRow{RunID: r.id, ID: t.def.ID, Member: t.def.Member, Kind: t.def.Kind, State: t.state, Reason: t.reason, Attempts: n + t.prior, ImportedFrom: t.imported, Extra: t.extra}
	if rp := t.repair; rp != nil {
		if b, err := json.Marshal(TaskRepair{Review: rp.review, Attempt: rp.attempt, Commit: rp.commit, Summary: rp.summary, Findings: rp.findings}); err == nil {
			row.Repair = string(b)
		}
	}
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
	row := ledger.RunRow{ID: r.id, CrewID: r.crewID, Crew: crewJSON, Base: r.base, State: state, Outcome: r.outcome, Paused: r.paused, CreatedAt: r.startedAt, ContinuedFrom: r.resumedFrom, ContinuedBy: r.resumedBy}
	if state != ledger.RunRunning {
		row.ClosedAt = time.Now().UTC()
	}
	return row
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
	// fail closes the attempt at its stage; with stop, its session goes
	// once the close is committed (pendingStops).
	fail := func(stage string, err error, stop bool) {
		terr := e.transact(context.Background(), r, func() tx {
			state := ledger.AttemptFailed
			if r.ctx.Err() != nil {
				state = ledger.AttemptStopped
			} else if ctx.Err() != nil {
				state = ledger.AttemptCancelled
			}
			if stop && a.sessionID != "" {
				r.pendingStops = append(r.pendingStops, a.sessionID)
			}
			e.closeAttempt(r, a, state, stage, session.CleanMessage(err.Error()))
			return ev("attempt.failed", map[string]any{"task": t.def.ID, "attempt": a.n, "stage": stage, "error": a.err, "state": a.state})
		}, nil)
		e.afterTransact(r, terr)
	}
	// The member's previous session, idle since its task: let go, so that
	// its place is free for this one.
	e.mu.Lock()
	prev := m.state.SessionID
	e.mu.Unlock()
	if prev != "" {
		if l, ok := e.lookup(prev); ok && !l.Info().Status.Ended() {
			if err := e.transact(ctx, r, func() tx {
				return ev("session.released", map[string]any{"member": m.def.Name, "session": prev, "for": t.def.ID})
			}, nil); err != nil {
				// Not recorded: the session stays, the attempt is not
				// spent, and the run's dispatch pauses.
				_ = e.transact(context.Background(), r, func() tx {
					r.unreserve(a, "the ledger could not record the attempt: "+session.CleanMessage(err.Error()))
					return pausedTx(e.pauseDispatch(r, err), err)
				}, nil)
				return
			}
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
		_ = e.transact(context.Background(), r, func() tx {
			if !a.closedAt.IsZero() {
				return tx{}
			}
			r.unreserve(a, "waiting for a place among the server's sessions: "+session.CleanMessage(err.Error()))
			r.note(session.ActivityStatus, "task %s: no place for attempt %d yet: %v", t.def.ID, a.n, err)
			return ev("attempt.unreserved", map[string]any{"task": t.def.ID, "attempt": a.n, "why": session.CleanMessage(err.Error())})
		}, nil)
		return
	}
	// The place goes to the launch; let go on every other way out.
	defer res.Release()
	worktree := filepath.Join(r.cwd, worktreesDir, r.id, fmt.Sprintf("%s.%d", t.def.ID, a.n))
	branch := fmt.Sprintf("crew/%s/%s.%d", r.id, t.def.ID, a.n)
	if err := e.transact(ctx, r, func() tx {
		if !a.closedAt.IsZero() {
			return tx{}
		}
		a.worktree, a.branch, a.provisional = worktree, branch, false
		r.markAttempt(a)
		r.markTask(t)
		return ev("attempt.reserved", map[string]any{"task": t.def.ID, "attempt": a.n, "member": m.def.Name, "start": a.start, "inputs": len(a.inputs)})
	}, func() { a.worktree, a.branch, a.provisional = "", "", true }); err != nil {
		// The ledger refused the intent: nothing is made, and the run's
		// dispatch pauses until a person looks.
		_ = e.transact(context.Background(), r, func() tx {
			r.unreserve(a, "the ledger could not record the attempt: "+session.CleanMessage(err.Error()))
			return pausedTx(e.pauseDispatch(r, err), err)
		}, nil)
		return
	}
	e.mu.Lock()
	closed := !a.closedAt.IsZero()
	e.mu.Unlock()
	if closed {
		return // cancelled or stopped before anything was made
	}
	// The worktree, at the start commit, under the repository's lock.
	if err := checkWorktreesDir(r.cwd); err != nil {
		fail("worktree", err, false)
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
		fail("worktree", err, false)
		return
	} else if tracked {
		fail("worktree", fmt.Errorf("the commit %s tracks %s/ in the working directory, which is Conductor's: the task files cannot be written", short(a.start), taskDir), false)
		return
	}
	e.gitMu.Lock()
	err = AddWorktreeAt(ctx, r.cwd, a.worktree, a.branch, a.start)
	e.gitMu.Unlock()
	if err != nil {
		fail("worktree", err, false)
		return
	}
	if head, err := headCommit(ctx, a.worktree); err != nil || head != a.start {
		fail("worktree", fmt.Errorf("the worktree is at %s, not %s", short(head), short(a.start)), false)
		return
	}
	if err := checkNoLinks(a.worktree, r.prefix); err != nil {
		fail("worktree", err, false)
		return
	}
	cwd := filepath.Join(a.worktree, r.prefix)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		fail("worktree", err, false)
		return
	}
	e.mu.Lock()
	m.state.Branch, m.state.Worktree = a.branch, a.worktree
	m.base = a.start
	e.mu.Unlock()
	// The task files, through a root at the working directory: a
	// .conductor that is not a directory of ours, or a task file that is
	// there already, is refused.
	md, js, err := e.taskFile(r, a)
	if err == nil {
		err = writeTaskFiles(a.worktree, r.prefix, md, js)
	}
	if err != nil {
		fail("task file", err, false)
		return
	}
	if err := e.transact(ctx, r, func() tx {
		return ev("attempt.workspace", map[string]any{"task": t.def.ID, "attempt": a.n, "worktree": a.worktree, "branch": a.branch, "start": a.start})
	}, nil); err != nil {
		fail("worktree", fmt.Errorf("the ledger could not record the worktree: %w", err), false)
		return
	}
	// The setup, in the worktree, before the member starts.
	for _, ch := range r.setup {
		if err := e.runSetup(ctx, r, a, cwd, ch, "attempt.setup"); err != nil {
			fail("setup "+ch.ID, err, false)
			return
		}
	}
	// The session, with the attempt in its crew reference.
	local, err := e.launcher.Launch(ctx, LaunchSpec{AgentID: m.def.AgentID, Name: fmt.Sprintf("%s · %s #%d", m.def.Name, t.def.ID, a.n), Cwd: cwd, Args: slices.Clone(m.def.Args),
		Env: map[string]string{"GOAL": r.goal}, Ref: session.CrewRef{RunID: r.id, CrewID: r.crewID, Member: m.def.Name, Task: t.def.ID, Attempt: a.n}, Yolo: r.yolo, Reservation: res})
	if err != nil {
		fail("launch", err, false)
		return
	}
	id := local.Info().ID
	started := time.Now().UTC()
	var stopping bool
	lerr := e.transact(ctx, r, func() tx {
		a.sessionID, a.launchedAt = id, started
		if a.closedAt.IsZero() {
			a.state = ledger.AttemptLaunched
		}
		m.state.SessionID, m.state.Started, m.state.Status, m.state.Ended, m.state.Err = id, &started, MemberStarting, nil, ""
		m.state.Task, m.state.Attempt = t.def.ID, a.n
		e.bySession.Store(id, sessionMember{r: r, m: m, a: a})
		r.markAttempt(a)
		r.note(session.ActivityStatus, "task %s: attempt %d started as %s", t.def.ID, a.n, m.def.Name)
		stopping = r.stopping
		return ev("attempt.launched", map[string]any{"task": t.def.ID, "attempt": a.n, "session": id})
	}, nil)
	go e.watchAttempt(r, a, local)
	// Closed before its session is stopped, so the stage is the launch's,
	// not the watcher's "session ended".
	if stopping {
		fail("launch", ErrRunStopped, true)
		return
	}
	if lerr != nil {
		// The session is not in the ledger: it does not get its task.
		fail("launch", fmt.Errorf("the ledger could not record the launch: %w", lerr), true)
		return
	}
	// Messages waiting for the member go into its session once it has its
	// task line (deliverMessages queues them; deliver waits for the prompt).
	e.deliverMessages(r, m)
	// The line: typed once the session is ready, as a role prompt is.
	if err := e.promptWith(ctx, r, m, local, taskLine(t.def, a.n)); err != nil {
		if r.ctx.Err() != nil {
			err = ErrRunStopped
		}
		fail("prompt", err, true)
		return
	}
	if err := e.transact(ctx, r, func() tx {
		if !a.closedAt.IsZero() || a.state != ledger.AttemptLaunched {
			return tx{}
		}
		a.promptedAt, a.state = time.Now().UTC(), ledger.AttemptRunning
		r.markAttempt(a)
		return ev("attempt.prompted", map[string]any{"task": t.def.ID, "attempt": a.n})
	}, nil); err != nil {
		// The line is typed and the agent at work: the state stays ahead,
		// marked, and the run's dispatch pauses until a person looks.
		_ = e.pauseFor(r, err)
	}
	m.poke()
}

// runSetup runs one setup command in dir (an attempt's working directory,
// or the checks' checkout) and records how it went under the event kind.
func (e *Engine) runSetup(ctx context.Context, r *run, a *attempt, dir string, ch Check, kind string) error {
	res := e.runCommand(ctx, dir, ch, io.Discard, nil)
	if err := e.transact(context.Background(), r, func() tx {
		return ev(kind, map[string]any{"task": a.t.def.ID, "attempt": a.n, "setup": ch.ID, "exit": res.Exit, "timedOut": res.TimedOut, "error": res.Err, "duration": res.Duration.String()})
	}, nil); err != nil {
		return fmt.Errorf("the ledger could not record %s: %w", ch.ID, err)
	}
	switch {
	case res.Err != "" && !res.TimedOut:
		return fmt.Errorf("%s could not run: %s", ch.ID, res.Err)
	case res.TimedOut:
		return fmt.Errorf("%s timed out after %s%s", ch.ID, checkTimeout(ch), tailWords(lastOutput(res, checkTail)))
	case res.Exit != 0:
		return fmt.Errorf("%s exited %d%s", ch.ID, res.Exit, tailWords(lastOutput(res, checkTail)))
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

// treeHasPath reports whether the commit's tree holds path, named from
// the repository's root whatever directory git runs in.
func treeHasPath(ctx context.Context, repo, commit, path string) (bool, error) {
	out, err := gitcli.Run(ctx, repo, "ls-tree", "-d", "--full-tree", "--name-only", commit, "--", path)
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
		// A handoff is a message to the member it names (messages.go),
		// typed once that member's attempt has its task line.
		r.note(session.ActivityStatus, "%s reported a handoff to %s: sent as a message", sm.m.def.Name, entry.To)
		go func() {
			if _, err := e.send(context.Background(), r, sm.m, entry.To, entry.Message); err != nil {
				e.mu.Lock()
				r.note(session.ActivityError, "the handoff from %s to %s was not sent: %v", sm.m.def.Name, entry.To, err)
				e.mu.Unlock()
			}
		}()
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

// managedEnded takes the end of an attempt's session: before its result,
// the attempt fails at its stage; while a result is being taken, the end
// is remembered, and a submission refused then fails the attempt. The
// caller does not hold e.mu.
func (e *Engine) managedEnded(r *run, a *attempt) {
	e.mu.Lock()
	if !a.closedAt.IsZero() {
		e.mu.Unlock()
		return
	}
	if a.state == ledger.AttemptSubmitting || a.state == ledger.AttemptVerifying {
		a.sessionEnded = true
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	err := e.transact(context.Background(), r, func() tx {
		if !a.closedAt.IsZero() || r.stopping || r.ctx.Err() != nil {
			// Closed already, or a stop under way, which closes it as
			// stopped.
			return tx{}
		}
		if a.state == ledger.AttemptSubmitting || a.state == ledger.AttemptVerifying {
			a.sessionEnded = true
			return tx{}
		}
		stage := a.state
		e.closeAttempt(r, a, ledger.AttemptFailed, stage, "the session ended before a result was submitted")
		return ev("attempt.failed", map[string]any{"task": a.t.def.ID, "attempt": a.n, "stage": stage, "error": a.err, "state": a.state})
	}, nil)
	e.afterTransact(r, err)
}

// SubmitResult takes the result the session submits for its attempt: the
// attempt must be its task's active, running one; the worktree on the
// attempt's branch and clean apart from the task files; HEAD descending
// from every input (a review's equal to its candidate); the result tree
// without a .conductor directory or a submodule. The result is recorded,
// and the task is verified (its checks, in a checkout of their own) and
// decided by the controller: a task with no checks succeeds at once. A
// second submit of the same commit while the first is verified answers as
// the first (repeat); another is refused. The task is returned as it is
// then.
func (e *Engine) Submit(ctx context.Context, sessionID string, res Result) (TaskState, bool, error) {
	v, ok := e.bySession.Load(sessionID)
	if !ok {
		return TaskState{}, false, ErrNotAnAttempt
	}
	sm := v.(sessionMember)
	r, a := sm.r, sm.a
	if a == nil {
		return TaskState{}, false, ErrNotAnAttempt
	}
	t := a.t
	if err := res.validate(t.def.Kind); err != nil {
		return TaskState{}, false, err
	}
	// The first look waits for a transaction in flight (a first submit's
	// commit), so a second submit answers on the committed state.
	r.txMu.Lock()
	e.mu.Lock()
	if e.runs[r.id] != r || t.active() != a {
		e.mu.Unlock()
		r.txMu.Unlock()
		return TaskState{}, false, ErrStaleAttempt
	}
	switch a.state {
	case ledger.AttemptSubmitting, ledger.AttemptVerifying:
		// The same result again: the first answer stands.
		if a.submitted != nil && (res.Commit == "" || res.Commit == a.result) {
			st := r.taskStateOf(t)
			e.mu.Unlock()
			r.txMu.Unlock()
			return st, true, nil
		}
		e.mu.Unlock()
		r.txMu.Unlock()
		return TaskState{}, false, ErrStaleAttempt
	case ledger.AttemptRunning:
	default:
		state := a.state
		e.mu.Unlock()
		r.txMu.Unlock()
		return TaskState{}, false, fmt.Errorf("%w: it is %s", ErrNotRunning, state)
	}
	a.state = ledger.AttemptSubmitting
	a.submitSeq++
	mine := a.submitSeq
	worktree, branch, start, inputs := a.worktree, a.branch, a.start, slices.Clone(a.inputs)
	e.mu.Unlock()
	r.txMu.Unlock()
	// back refuses the result: the attempt runs on, unless its session
	// ended meanwhile, which fails it. It puts back this submission's
	// state alone, never a later one's.
	back := func(err error) (TaskState, bool, error) {
		e.mu.Lock()
		if a.state == ledger.AttemptSubmitting && a.submitSeq == mine {
			a.state = ledger.AttemptRunning
		}
		ended := a.sessionEnded
		e.mu.Unlock()
		if ended {
			e.managedEnded(r, a)
		}
		return TaskState{}, false, err
	}
	// The tree: on the attempt's branch, clean apart from the task files.
	ref, err := gitcli.Run(ctx, worktree, "symbolic-ref", "-q", "HEAD")
	if err != nil || strings.TrimSpace(ref) != "refs/heads/"+branch {
		return back(fmt.Errorf("%w: HEAD is %s, the branch is %s", ErrOffBranch, strings.TrimSpace(ref), branch))
	}
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
	// Recorded, then decided: the states move with the commit, and move
	// back when it fails.
	stale := false
	if err := e.transact(ctx, r, func() tx {
		if t.active() != a || a.state != ledger.AttemptSubmitting {
			stale = true
			return tx{}
		}
		submitted := res
		a.submitted, a.result = &submitted, head
		if t.def.Kind == TaskReview {
			a.result = ""
		}
		a.state, t.state = ledger.AttemptVerifying, ledger.TaskVerifying
		r.markAttempt(a)
		r.markTask(t)
		r.note(session.ActivityStatus, "task %s: attempt %d submitted %s", t.def.ID, a.n, resultWords(t.def.Kind, head, res))
		return ev("result.submitted", map[string]any{"task": t.def.ID, "attempt": a.n, "commit": a.result, "verdict": res.Verdict, "summary": res.Summary, "issues": len(res.Issues), "findings": len(res.Findings)})
	}, func() {
		// Back to running within the transaction, so a second submit
		// waiting on it finds the attempt open again.
		if a.closedAt.IsZero() && a.state == ledger.AttemptVerifying {
			a.state, t.state = ledger.AttemptRunning, ledger.TaskRunning
			a.submitted, a.result = nil, ""
			r.note(session.ActivityError, "task %s: attempt %d's result was not recorded", t.def.ID, a.n)
		}
	}); err != nil {
		return back(err)
	}
	if stale {
		return TaskState{}, false, ErrStaleAttempt
	}
	// Verified on a goroutine the run's stop waits for, registered under
	// the lock a stop takes first: a stop under way takes the attempt.
	e.mu.Lock()
	if a.closedAt.IsZero() && !r.stopping {
		r.checks.Add(1)
		go e.verify(r, a)
	}
	st := r.taskStateOf(t)
	e.mu.Unlock()
	return st, false, nil
}

// SubmitResult is Submit without the repeat flag.
func (e *Engine) SubmitResult(ctx context.Context, sessionID string, res Result) (TaskState, error) {
	st, _, err := e.Submit(ctx, sessionID, res)
	return st, err
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

// treeReserved reports whether the commit's tree holds a path with a
// .conductor directory anywhere in it, and whether it holds a gitlink (a
// submodule).
func treeReserved(ctx context.Context, dir, commit string) (reserved, links bool, err error) {
	out, err := gitcli.Run(ctx, dir, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return false, false, err
	}
	for _, line := range strings.Split(out, "\x00") {
		if line == "" {
			continue
		}
		meta, p, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if strings.HasPrefix(meta, "160000 ") {
			links = true
		}
		if slices.Contains(strings.Split(p, "/"), taskDir) {
			reserved = true
		}
	}
	return reserved, links, nil
}

// accept records the decision on a's result, succeeded with the result or
// failed at a stage, and dispatches what follows once it is recorded. The
// caller does not hold e.mu.
func (e *Engine) accept(r *run, a *attempt, ok bool, stage, errText string) {
	t := a.t
	err := e.transact(context.Background(), r, func() tx {
		if !a.closedAt.IsZero() {
			// Closed meanwhile (cancelled, stopped): what closed it wrote it.
			return tx{}
		}
		if !ok && (r.stopping || r.ctx.Err() != nil) {
			// Cut short by a stop: the stop closes it as stopped, not
			// failed, and the task stays ready for a continuation.
			return tx{}
		}
		var reopened *task
		var reopenEvents []event
		var res *Result
		if ok {
			res = a.submitted
			t.result = &taskResult{attempt: a.n, commit: a.result, summary: res.Summary, issues: slices.Clone(res.Issues), verdict: res.Verdict, findings: slices.Clone(res.Findings), at: time.Now().UTC()}
			t.repair = nil
			e.closeAttempt(r, a, ledger.AttemptSucceeded, "", "")
			if t.def.Kind == TaskReview && res.Verdict == VerdictNeedsChanges {
				reopened, reopenEvents = e.reopen(r, t, a, res)
			}
		} else {
			e.closeAttempt(r, a, ledger.AttemptFailed, stage, errText)
		}
		kind := "task.succeeded"
		if !ok {
			kind = "attempt.failed"
		}
		out := ev(kind, map[string]any{"task": t.def.ID, "attempt": a.n, "stage": stage, "error": errText, "commit": a.result, "state": a.state})
		if reopened != nil {
			out.events = append(out.events, reopenEvents...)
		}
		if r.outcome != "" {
			out.events = append(out.events, event{"run.finished", map[string]any{"outcome": r.outcome}})
		}
		return out
	}, nil)
	e.afterTransact(r, err)
}

// reopen takes a review's needs_changes to the task it reviewed: the task
// has no result again, its next attempt starts at the commit reviewed with
// the findings in its task file (a repair), and what rested on the result
// is withdrawn: the review goes back to blocked, to run again on the new
// result, as does whatever else needed the task. A task with no attempt
// left fails instead, keeping the repair for a retry. The caller holds
// e.mu, in a transaction's change.
func (e *Engine) reopen(r *run, review *task, a *attempt, res *Result) (*task, []event) {
	if len(review.def.Needs) == 0 {
		return nil, nil
	}
	sub := r.task(review.def.Needs[0])
	if sub == nil || sub.state != ledger.TaskSucceeded || sub.result == nil {
		return nil, nil
	}
	sub.repair = &repair{review: review.def.ID, attempt: a.n, commit: sub.result.commit, summary: res.Summary, findings: slices.Clone(res.Findings)}
	sub.result = nil
	why := fmt.Sprintf("review %s asked for changes: %d findings", review.def.ID, len(res.Findings))
	if len(sub.attempts)+sub.prior >= maxAttemptsPerTask+sub.extra {
		sub.state, sub.reason = ledger.TaskFailed, fmt.Sprintf("review %s asked for changes and no attempt is left; retry it to allow one more", review.def.ID)
		r.note(session.ActivityStatus, "task %s: review %s asked for changes (%d findings), and no attempt is left", sub.def.ID, review.def.ID, len(res.Findings))
	} else {
		sub.state, sub.reason = ledger.TaskReady, why
		r.note(session.ActivityStatus, "task %s: review %s asked for changes (%d findings); attempt %d starts at the reviewed commit", sub.def.ID, review.def.ID, len(res.Findings), len(sub.attempts)+1)
	}
	r.markTask(sub)
	events := []event{{"task.reopened", map[string]any{"task": sub.def.ID, "review": review.def.ID, "attempt": a.n, "findings": len(res.Findings), "state": sub.state}}}
	events = append(events, e.withdraw(r, sub, "needs "+sub.def.ID+", reopened by review "+review.def.ID)...)
	r.ready()
	e.settleRun(r)
	return sub, events
}

// withdraw takes back what rested on sub's result, as a review reopened
// it: every task that needs sub, and what needs those, is blocked again
// with its result dropped; an attempt in flight on the withdrawn result
// ends (cancelled, its session stopped once that is durable), and neither
// it nor a result made on it counts against its task. A task that failed
// or was cancelled stays so. The caller holds e.mu, in a transaction.
func (e *Engine) withdraw(r *run, sub *task, why string) []event {
	var events []event
	for _, t := range r.tasks {
		if t == sub || !slices.Contains(t.def.Needs, sub.def.ID) {
			continue
		}
		switch {
		case t.active() != nil:
			a := t.active()
			if a.sessionID != "" {
				r.pendingStops = append(r.pendingStops, a.sessionID)
			}
			e.endAttempt(r, a, ledger.AttemptCancelled, "withdrawn", why)
			t.extra++
			r.note(session.ActivityStatus, "task %s: attempt %d withdrawn: %s", t.def.ID, a.n, why)
			events = append(events, event{"attempt.withdrawn", map[string]any{"task": t.def.ID, "attempt": a.n, "needs": sub.def.ID}})
		case t.state == ledger.TaskSucceeded:
			t.result = nil
			t.extra++
			r.note(session.ActivityStatus, "task %s: its result is withdrawn: %s", t.def.ID, why)
			events = append(events, event{"task.withdrawn", map[string]any{"task": t.def.ID, "needs": sub.def.ID}})
		case t.state == ledger.TaskReady:
		default:
			continue
		}
		// Its checkpoint rested on the input withdrawn: the next attempt
		// starts from the new result, not from it.
		t.checkpoint = nil
		t.state, t.reason = ledger.TaskBlocked, why
		r.markTask(t)
		events = append(events, e.withdraw(r, t, "needs "+t.def.ID+", withdrawn")...)
	}
	return events
}

// PauseRun pauses a managed run's dispatch: attempts in flight go on,
// none starts. Unpaused, it dispatches what is ready, once the pause is
// recorded. Pause is the same with a receipt.
func (e *Engine) PauseRun(ctx context.Context, runID string, paused bool) error {
	r, err := e.managedRun(runID)
	if err != nil {
		return err
	}
	e.mu.Lock()
	stopped := r.stoppedAt != nil
	e.mu.Unlock()
	if stopped {
		return ErrRunStopped
	}
	var was bool
	err = e.transact(ctx, r, func() tx {
		was = r.paused
		return tx{events: r.setPaused(paused)}
	}, func() {
		// Not recorded: as it was, so a dispatch never rests on a pause
		// or an unpause the ledger does not have.
		r.paused = was
		r.note(session.ActivityError, "the ledger could not record the %s", map[bool]string{true: "pause", false: "unpause"}[paused])
	})
	if err == nil && !paused {
		e.pumpLocked(r)
	}
	return err
}

// CancelTask cancels a task: its attempt in flight is ended (its session
// stopped), the task is cancelled, and the tasks that need it are blocked
// for good. Cancel is the same with a receipt.
func (e *Engine) CancelTask(ctx context.Context, runID, taskID string) error {
	r, t, err := e.taskIn(runID, taskID, cancellable)
	if err != nil {
		return err
	}
	err = e.transact(ctx, r, func() tx {
		if cancellable(t) != nil {
			return tx{} // ended meanwhile
		}
		return tx{events: e.cancelTask(r, t)}
	}, nil)
	e.afterTransact(r, err)
	return err
}

// RetryTask gives a failed or cancelled task one more attempt: it is ready
// again, dispatched as the run takes it, once the retry is recorded. Retry
// is the same with a receipt.
func (e *Engine) RetryTask(ctx context.Context, runID, taskID string) error {
	r, t, err := e.taskIn(runID, taskID, retryable)
	if err != nil {
		return err
	}
	err = e.transact(ctx, r, func() tx {
		if retryable(t) != nil {
			return tx{} // changed meanwhile
		}
		return tx{events: e.retryTask(r, t)}
	}, nil)
	e.afterTransact(r, err)
	return err
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
