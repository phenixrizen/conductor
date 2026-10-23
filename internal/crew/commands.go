package crew

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
	"github.com/phenixrizen/conductor/internal/session"
)

// A command is a change a person asks of a managed run (pause, unpause,
// cancel, retry, accept) with a receipt (docs/plans/agent-harness-order.md,
// decision 10): the asker mints an id, the run keeps the outcome under
// (actor, id), and the same id sent again with the same content gets the
// same receipt, never a second change; other content is a conflict. An
// expected revision, when given, refuses the command once the run has
// moved on. The change a command makes is the engine's own (PauseRun,
// CancelTask, RetryTask: the same code), with the receipt written in the
// change's transaction.

// CommandIDPattern bounds a command id a client mints.
var CommandIDPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,32}$`)

// Command errors.
var (
	ErrBadCommandID    = errors.New("a command id is 1 to 32 letters, digits, '.', '_', '~' or '-'")
	ErrStaleRevision   = errors.New("the run has moved on since the revision expected")
	ErrCommandConflict = errors.New("the command id was used before with other content")
	ErrTooManyCommands = errors.New("the run has taken as many commands as it keeps")
)

// Command is what the asker sends with a change.
type Command struct {
	ID               string `json:"commandId"`
	ExpectedRevision int64  `json:"expectedRevision,omitempty"`
}

// Receipt is what a command gets: its id and intent, the run's revision
// after it, whether this answer is the one given before (a repeat), its
// status (done, or failed with why: the error's words and the refusal's
// name, which a repeat answers with again), and the task or the run's
// pause as they stand.
type Receipt struct {
	CommandID string     `json:"commandId"`
	Intent    string     `json:"intent"`
	Revision  int64      `json:"revision"`
	Repeat    bool       `json:"repeat,omitempty"`
	Status    string     `json:"status"`
	Error     string     `json:"error,omitempty"`
	Refusal   string     `json:"refusal,omitempty"`
	Task      *TaskState `json:"task,omitempty"`
	Paused    *bool      `json:"paused,omitempty"`
}

// Refusals a failed receipt names.
const (
	RefusalTaskState  = "task_state"
	RefusalRunStopped = "run_stopped"
	RefusalNotFound   = "not_found"
)

// refusalOf names the refusal err is.
func refusalOf(err error) string {
	switch {
	case errors.Is(err, ErrRunStopped):
		return RefusalRunStopped
	case errors.Is(err, ErrTaskNotFound):
		return RefusalNotFound
	}
	return RefusalTaskState
}

// fingerprint is a command's content, for the conflict check.
func fingerprint(intent string, args ...string) string {
	h := sha256.New()
	h.Write([]byte(intent))
	for _, a := range args {
		h.Write([]byte{0})
		h.Write([]byte(a))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// errCommandRepeated says the command's row was there as the transaction
// wrote: a repeat raced the first, whose commit is the answer.
var errCommandRepeated = errors.New("command repeated")

// A change is what a command does to the run, under e.mu in its
// transaction: the task it concerns (nil for the run), the events of the
// change, and an error that fails the command (its receipt says so) rather
// than the transaction; a change that errs changed nothing.
type change func(r *run) (t *task, events []event, err error)

// act runs a command of actor on the run: the receipt given before is
// returned for a repeat (a conflict for other content); otherwise do runs
// in the run's transaction, the revision checked first, with the receipt
// written beside the change, and what rests on the commit follows
// (afterTransact). undo, when given, puts the change back when the commit
// fails, as transact takes it.
func (e *Engine) act(ctx context.Context, runID, actor string, cmd Command, intent string, args []string, do change, undo func(r *run)) (Receipt, error) {
	if !CommandIDPattern.MatchString(cmd.ID) {
		return Receipt{}, ErrBadCommandID
	}
	if e.ledger == nil {
		return Receipt{}, ErrNoLedger
	}
	r, err := e.managedRun(runID)
	if err != nil {
		return Receipt{}, err
	}
	fp := fingerprint(intent, args...)
	if prior, found, err := e.ledger.Receipt(ctx, runID, actor, cmd.ID); err != nil {
		return Receipt{}, err
	} else if found {
		return priorReceipt(prior, intent, fp)
	}
	var out Receipt
	var cmdErr error
	var stale bool
	var back func()
	if undo != nil {
		back = func() { undo(r) }
	}
	err = e.transact(ctx, r, func() tx {
		if cmd.ExpectedRevision > 0 && cmd.ExpectedRevision != r.revision {
			stale = true
			cmdErr = fmt.Errorf("%w: expected %d, the run is at %d", ErrStaleRevision, cmd.ExpectedRevision, r.revision)
			return tx{} // not taken: nothing to write
		}
		t, events, err := do(r)
		// A run's transactions commit one after another, each as the
		// revision after the run's: the receipt kept says which.
		out = Receipt{CommandID: cmd.ID, Intent: intent, Revision: r.revision + 1, Status: ledger.CommandDone}
		if err != nil {
			cmdErr = err
			out.Status, out.Error, out.Refusal = ledger.CommandFailed, err.Error(), refusalOf(err)
		}
		if t != nil {
			st := r.taskStateOf(t)
			out.Task = &st
		}
		paused := r.paused
		out.Paused = &paused
		w := tx{events: append(events, event{"command." + intent, map[string]any{"actor": actor, "command": cmd.ID, "args": args, "status": out.Status, "error": out.Error}})}
		w.more = func(ltx LedgerTx) error {
			if _, found, err := ltx.Command(actor, cmd.ID, intent, fp); err != nil {
				return err
			} else if found {
				return errCommandRepeated
			}
			return ltx.Settle(actor, cmd.ID, out.Status, out)
		}
		return w
	}, back)
	if stale {
		return Receipt{}, cmdErr
	}
	switch {
	case errors.Is(err, errCommandRepeated):
		prior, found, rerr := e.ledger.Receipt(ctx, runID, actor, cmd.ID)
		if rerr != nil || !found {
			return Receipt{}, errors.Join(err, rerr)
		}
		return priorReceipt(prior, intent, fp)
	case err != nil:
		e.afterTransact(r, err)
		switch {
		case errors.Is(err, ledger.ErrCommandConflict):
			return Receipt{}, ErrCommandConflict
		case errors.Is(err, ledger.ErrTooManyCommands):
			return Receipt{}, ErrTooManyCommands
		}
		return Receipt{}, err
	}
	e.afterTransact(r, nil)
	return out, cmdErr
}

// priorReceipt is the receipt given before, as a repeat; a conflict when
// the content differs.
func priorReceipt(prior ledger.CommandRow, intent, fp string) (Receipt, error) {
	if prior.Intent != intent || prior.Fingerprint != fp {
		return Receipt{}, ErrCommandConflict
	}
	var out Receipt
	if prior.Receipt != "" {
		if err := json.Unmarshal([]byte(prior.Receipt), &out); err != nil {
			return Receipt{}, fmt.Errorf("the receipt kept cannot be read: %w", err)
		}
	}
	out.CommandID, out.Intent, out.Repeat = prior.ID, prior.Intent, true
	if out.Status == "" {
		out.Status = prior.Status
	}
	return out, nil
}

// managedRun is the run, when the engine holds it and it is managed.
func (e *Engine) managedRun(runID string) (*run, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.runs[runID]
	if !ok {
		return nil, ErrRunNotFound
	}
	if !r.managed {
		return nil, fmt.Errorf("%w: a legacy run takes no command", ErrTaskState)
	}
	return r, nil
}

// taskIn is the run's task, with check run on it under e.mu.
func (e *Engine) taskIn(runID, taskID string, check func(*task) error) (*run, *task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.runs[runID]
	if !ok {
		return nil, nil, ErrRunNotFound
	}
	t := r.task(taskID)
	if t == nil {
		return nil, nil, ErrTaskNotFound
	}
	if check != nil {
		if err := check(t); err != nil {
			return nil, nil, err
		}
	}
	return r, t, nil
}

// cancellable, retryable and acceptable say whether a task's state takes
// the command.
func cancellable(t *task) error {
	switch t.state {
	case ledger.TaskSucceeded, ledger.TaskFailed, ledger.TaskCancelled:
		return fmt.Errorf("%w: it is %s", ErrTaskState, t.state)
	}
	return nil
}

func retryable(t *task) error {
	if t.state != ledger.TaskFailed && t.state != ledger.TaskCancelled {
		return fmt.Errorf("%w: it is %s", ErrTaskState, t.state)
	}
	return nil
}

func acceptable(t *task) error {
	if t.state != ledger.TaskFailed {
		return fmt.Errorf("%w: it is %s, and only a failed task is accepted", ErrTaskState, t.state)
	}
	return nil
}

// setPaused pauses or resumes the run's dispatch, noting it; nothing when
// it is so already. The caller holds e.mu, in a transaction.
func (r *run) setPaused(paused bool) []event {
	if r.paused == paused {
		return nil
	}
	r.paused = paused
	r.markRun()
	if paused {
		r.note(session.ActivityStatus, "dispatch paused")
	} else {
		r.note(session.ActivityStatus, "dispatch resumed")
	}
	return []event{{"run.paused", map[string]any{"paused": paused}}}
}

// cancelTask cancels t: its attempt in flight is closed (its session
// stopped by the commit that makes the cancel durable), or the task is
// cancelled outright; what needs it is blocked for good, and the run may
// end. The caller holds e.mu, in a transaction; t is not terminal.
func (e *Engine) cancelTask(r *run, t *task) []event {
	if a := t.active(); a != nil {
		if a.sessionID != "" {
			r.pendingStops = append(r.pendingStops, a.sessionID)
		}
		e.closeAttempt(r, a, ledger.AttemptCancelled, a.state, "cancelled")
	} else {
		t.state, t.reason = ledger.TaskCancelled, "cancelled"
		r.markTask(t)
		r.ready()
		e.settleRun(r)
	}
	r.note(session.ActivityStatus, "task %s cancelled", t.def.ID)
	events := []event{{"task.cancelled", map[string]any{"task": t.def.ID}}}
	if r.outcome != "" {
		events = append(events, event{"run.finished", map[string]any{"outcome": r.outcome}})
	}
	return events
}

// retryTask gives failed or cancelled t one more attempt: it is blocked
// or ready again (a repair it carries stays), the run open. The caller
// holds e.mu, in a transaction.
func (e *Engine) retryTask(r *run, t *task) []event {
	if len(t.attempts)+t.prior >= maxAttemptsPerTask+t.extra {
		t.extra++
	}
	t.state, t.reason = ledger.TaskBlocked, ""
	r.markTask(t)
	if r.outcome != "" {
		r.outcome = ""
		r.markRun()
	}
	r.ready()
	r.note(session.ActivityStatus, "task %s: retried, one more attempt allowed", t.def.ID)
	return []event{{"task.retried", map[string]any{"task": t.def.ID}}}
}

// acceptTask takes failed t's last submitted result as its result all the
// same ("Accept anyway": a commit that failed its checks, decided by a
// person): the task succeeds with that commit, and what needs it goes on.
// The caller holds e.mu, in a transaction.
func (e *Engine) acceptTask(r *run, t *task, actor string) ([]event, error) {
	var last *attempt
	for i := len(t.attempts) - 1; i >= 0; i-- {
		if a := t.attempts[i]; a.submitted != nil && a.result != "" {
			last = a
			break
		}
	}
	if last == nil {
		return nil, fmt.Errorf("%w: no attempt submitted a commit to accept", ErrTaskState)
	}
	res := last.submitted
	t.result = &taskResult{attempt: last.n, commit: last.result, summary: res.Summary, issues: slices.Clone(res.Issues), verdict: res.Verdict, findings: slices.Clone(res.Findings), at: time.Now().UTC()}
	t.repair = nil
	t.state, t.reason = ledger.TaskSucceeded, fmt.Sprintf("accepted by %s with attempt %d's result", actor, last.n)
	r.markTask(t)
	if r.outcome != "" {
		r.outcome = ""
		r.markRun()
	}
	r.ready()
	e.settleRun(r)
	r.note(session.ActivityStatus, "task %s: attempt %d's result accepted by %s", t.def.ID, last.n, actor)
	events := []event{{"task.accepted", map[string]any{"task": t.def.ID, "attempt": last.n, "actor": actor, "commit": last.result}}}
	if r.outcome != "" {
		events = append(events, event{"run.finished", map[string]any{"outcome": r.outcome}})
	}
	return events, nil
}

// Pause is PauseRun as a command with a receipt (paused false: unpause).
func (e *Engine) Pause(ctx context.Context, runID, actor string, cmd Command, paused bool) (Receipt, error) {
	intent := "pause"
	if !paused {
		intent = "unpause"
	}
	var was bool
	return e.act(ctx, runID, actor, cmd, intent, nil, func(r *run) (*task, []event, error) {
		if r.stoppedAt != nil {
			return nil, nil, ErrRunStopped
		}
		was = r.paused
		return nil, r.setPaused(paused), nil
	}, func(r *run) {
		r.paused = was
		r.note(session.ActivityError, "the ledger could not record the %s", intent)
	})
}

// Cancel is CancelTask as a command with a receipt.
func (e *Engine) Cancel(ctx context.Context, runID, actor string, cmd Command, taskID string) (Receipt, error) {
	return e.act(ctx, runID, actor, cmd, "cancel", []string{taskID}, func(r *run) (*task, []event, error) {
		t := r.task(taskID)
		if t == nil {
			return nil, nil, ErrTaskNotFound
		}
		if err := cancellable(t); err != nil {
			return t, nil, err
		}
		return t, e.cancelTask(r, t), nil
	}, nil)
}

// Retry is RetryTask as a command with a receipt.
func (e *Engine) Retry(ctx context.Context, runID, actor string, cmd Command, taskID string) (Receipt, error) {
	return e.act(ctx, runID, actor, cmd, "retry", []string{taskID}, func(r *run) (*task, []event, error) {
		t := r.task(taskID)
		if t == nil {
			return nil, nil, ErrTaskNotFound
		}
		if err := retryable(t); err != nil {
			return t, nil, err
		}
		return t, e.retryTask(r, t), nil
	}, nil)
}

// Accept takes a failed task's last submitted result as its result, as a
// command with a receipt.
func (e *Engine) Accept(ctx context.Context, runID, actor string, cmd Command, taskID string) (Receipt, error) {
	return e.act(ctx, runID, actor, cmd, "accept", []string{taskID}, func(r *run) (*task, []event, error) {
		t := r.task(taskID)
		if t == nil {
			return nil, nil, ErrTaskNotFound
		}
		if err := acceptable(t); err != nil {
			return t, nil, err
		}
		events, err := e.acceptTask(r, t, actor)
		return t, events, err
	}, nil)
}

// TaskContext is an attempt's task as written for its session: task.md
// and task.json as they are in its working directory. ErrNotAnAttempt for
// a session that is no attempt, ErrNotRunning for one without a worktree
// yet.
func (e *Engine) TaskContext(sessionID string) (md, js []byte, err error) {
	v, ok := e.bySession.Load(sessionID)
	if !ok {
		return nil, nil, ErrNotAnAttempt
	}
	sm := v.(sessionMember)
	if sm.a == nil {
		return nil, nil, ErrNotAnAttempt
	}
	e.mu.Lock()
	dir := sm.a.worktree
	prefix := sm.r.prefix
	e.mu.Unlock()
	if dir == "" {
		return nil, nil, fmt.Errorf("%w: the attempt has no worktree yet", ErrNotRunning)
	}
	root, err := os.OpenRoot(filepath.Join(dir, prefix))
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	md, err = root.ReadFile(filepath.Join(taskDir, "task.md"))
	if err != nil {
		return nil, nil, err
	}
	js, err = root.ReadFile(filepath.Join(taskDir, "task.json"))
	if err != nil {
		return nil, nil, err
	}
	return md, js, nil
}

// TaskOf is a run's task as reported, with every attempt's state and what
// it submitted.
func (e *Engine) TaskOf(runID, taskID string) (TaskState, []AttemptState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.runs[runID]
	if !ok {
		return TaskState{}, nil, ErrRunNotFound
	}
	t := r.task(taskID)
	if t == nil {
		return TaskState{}, nil, ErrTaskNotFound
	}
	st := r.taskStateOf(t)
	attempts := make([]AttemptState, 0, len(t.attempts))
	for _, a := range t.attempts {
		as := attemptStateOf(a)
		if a.submitted != nil {
			res := *a.submitted
			res.Issues, res.Findings = slices.Clone(res.Issues), slices.Clone(res.Findings)
			as.Submitted = &res
		}
		attempts = append(attempts, as)
	}
	return st, attempts, nil
}
