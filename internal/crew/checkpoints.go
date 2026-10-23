package crew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
	"github.com/phenixrizen/conductor/internal/session"
)

// Blockers and checkpoints (#334, row 10). A member reports a blocker: what
// stops its attempt (a missing credential, a decision), in words for the
// people watching, who see the task as blocked and the session as needing
// input; it clears once the member works on or says it is done, and when
// the attempt ends. A member saves a checkpoint: what is done, what
// remains and notes, tied to its worktree's commit, kept on the attempt
// and in the ledger; the task's next attempt reads it in its task file,
// and one after an attempt that ended without a result, or a
// continuation's first, starts at the checkpoint's commit.

const maxCheckpointField = 4000

// Blocker and checkpoint errors.
var (
	ErrBadBlocker    = errors.New("a blocker is a reason of one to 500 characters, with a message of at most 4000")
	ErrBadCheckpoint = errors.New("a checkpoint says what is done and what remains, each at most 4000 characters, with notes of at most 4000")
	ErrNoCommit      = errors.New("the worktree has no commit to tie the checkpoint to")
)

// blocker is what an attempt says stops it.
type blocker struct {
	reason, message, by string
	at                  time.Time
}

// Blocker is a blocker as the run reports it.
type Blocker struct {
	Reason  string    `json:"reason"`
	Message string    `json:"message,omitempty"`
	By      string    `json:"by"`
	At      time.Time `json:"at"`
}

// checkpoint is where an attempt got to.
type checkpoint struct {
	done, remaining, notes string
	commit                 string
	at                     time.Time
}

// Checkpoint is a checkpoint as the run reports it and the ledger keeps it.
type Checkpoint struct {
	Done      string    `json:"done"`
	Remaining string    `json:"remaining"`
	Notes     string    `json:"notes,omitempty"`
	Commit    string    `json:"commit"`
	At        time.Time `json:"at"`
}

func (c *checkpoint) api() *Checkpoint {
	return &Checkpoint{Done: c.done, Remaining: c.remaining, Notes: c.notes, Commit: c.commit, At: c.at}
}

// decodeCheckpoint reads a checkpoint as the ledger keeps it; nil for an
// empty or unreadable one.
func decodeCheckpoint(raw string) *checkpoint {
	var c Checkpoint
	if raw == "" || json.Unmarshal([]byte(raw), &c) != nil {
		return nil
	}
	return &checkpoint{done: c.Done, remaining: c.Remaining, notes: c.Notes, commit: c.Commit, at: c.At}
}

// Blocked reports what stops the session's attempt: the task shows it,
// and the session asks for it (the message, else the reason) as the
// agent's own question. The task is returned as it then is.
func (e *Engine) Blocked(ctx context.Context, sessionID, reason, message string) (TaskState, error) {
	reason, message = strings.TrimSpace(reason), strings.TrimSpace(message)
	if reason == "" || utf8.RuneCountInString(reason) > 500 || utf8.RuneCountInString(message) > maxCheckpointField {
		return TaskState{}, ErrBadBlocker
	}
	r, a, err := e.attemptOf(sessionID)
	if err != nil {
		return TaskState{}, err
	}
	var st TaskState
	err = e.transact(ctx, r, func() tx {
		if !a.closedAt.IsZero() || a.state != ledger.AttemptRunning {
			return tx{}
		}
		t := a.t
		t.blocker = &blocker{reason: reason, message: message, by: a.m.def.Name, at: time.Now().UTC()}
		r.markTask(t)
		r.note(session.ActivityStatus, "task %s: %s is blocked: %s", t.def.ID, a.m.def.Name, reason)
		st = r.taskStateOf(t)
		return ev("task.blocked", map[string]any{"task": t.def.ID, "attempt": a.n, "reason": reason})
	}, nil)
	if err != nil {
		e.afterTransact(r, err)
		return TaskState{}, err
	}
	if st.ID == "" {
		return TaskState{}, fmt.Errorf("%w: the attempt is not running", ErrNotRunning)
	}
	ask := message
	if ask == "" {
		ask = reason
	}
	if l, ok := e.lookup(a.sessionID); ok {
		_ = l.TryAsk(ask, session.SourceAPI, "", nil)
	}
	return st, nil
}

// unblock clears the attempt's task's blocker, as the attempt works on or
// says it is done. The caller does not hold e.mu.
func (e *Engine) unblock(r *run, a *attempt, state session.AttentionState) {
	e.mu.Lock()
	blocked := a.t.blocker != nil && a.closedAt.IsZero()
	e.mu.Unlock()
	if !blocked {
		return
	}
	err := e.transact(context.Background(), r, func() tx {
		t := a.t
		if t.blocker == nil || !a.closedAt.IsZero() {
			return tx{}
		}
		t.blocker = nil
		r.markTask(t)
		r.note(session.ActivityStatus, "task %s: %s is no longer blocked (%s)", t.def.ID, a.m.def.Name, state)
		return ev("task.unblocked", map[string]any{"task": t.def.ID, "attempt": a.n, "state": string(state)})
	}, nil)
	if err != nil {
		e.afterTransact(r, err)
	}
}

// SaveCheckpoint keeps where the session's attempt got to, tied to its
// worktree's commit: on the attempt, as the task's latest, and in the
// ledger. The attempt as it then is, is returned.
func (e *Engine) SaveCheckpoint(ctx context.Context, sessionID, done, remaining, notes string) (AttemptState, error) {
	done, remaining, notes = strings.TrimSpace(done), strings.TrimSpace(remaining), strings.TrimSpace(notes)
	if done == "" || utf8.RuneCountInString(done) > maxCheckpointField || utf8.RuneCountInString(remaining) > maxCheckpointField || utf8.RuneCountInString(notes) > maxCheckpointField {
		return AttemptState{}, ErrBadCheckpoint
	}
	r, a, err := e.attemptOf(sessionID)
	if err != nil {
		return AttemptState{}, err
	}
	e.mu.Lock()
	worktree := a.worktree
	e.mu.Unlock()
	if worktree == "" {
		return AttemptState{}, fmt.Errorf("%w: the attempt has no worktree yet", ErrNotRunning)
	}
	head, err := headCommit(ctx, worktree)
	if err != nil || head == "" {
		return AttemptState{}, ErrNoCommit
	}
	var out AttemptState
	err = e.transact(ctx, r, func() tx {
		if !a.closedAt.IsZero() || a.state != ledger.AttemptRunning {
			return tx{}
		}
		c := &checkpoint{done: done, remaining: remaining, notes: notes, commit: head, at: time.Now().UTC()}
		a.checkpoint, a.t.checkpoint = c, c
		r.markAttempt(a)
		r.note(session.ActivityStatus, "task %s: attempt %d checkpoint at %s: %s", a.t.def.ID, a.n, short(head), oneLine.Replace(shortText(done)))
		out = attemptStateOf(a)
		return ev("task.checkpoint", map[string]any{"task": a.t.def.ID, "attempt": a.n, "commit": head})
	}, nil)
	if err != nil {
		e.afterTransact(r, err)
		return AttemptState{}, err
	}
	if out.N == 0 {
		return AttemptState{}, fmt.Errorf("%w: the attempt is not running", ErrNotRunning)
	}
	return out, nil
}

// attemptOf is the session's run and attempt; ErrNotAnAttempt otherwise.
func (e *Engine) attemptOf(sessionID string) (*run, *attempt, error) {
	v, ok := e.bySession.Load(sessionID)
	if !ok {
		return nil, nil, ErrNotAnAttempt
	}
	sm := v.(sessionMember)
	if sm.a == nil {
		return nil, nil, ErrNotAnAttempt
	}
	return sm.r, sm.a, nil
}
