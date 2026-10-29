package crew

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/rocksolidlabs/conductor/internal/crew/ledger"
	"github.com/rocksolidlabs/conductor/internal/proc"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// A managed run with a project manifest (manifest.go;
// docs/plans/project-manifests.md, EN1) makes every tree usable through
// it: an attempt's worktree before its session starts, and the checkout of
// its result before its checks. The manifest is resolved for the tree
// (resolveTree: the crew's, or conductor.manifest.json at the tree's
// commit), then its setup phase runs (prepareTree), each step a receipt
// (runStep: a checks row of its phase, started, running and finished, with
// its log and the words of its failure). The run's prerequisites run once,
// before the first attempt, in the crew's directory (prerequisites), and
// again at each unpause. When an attempt closes, however it closes, its
// trees end (endTree), each once, after the close is committed and the
// tree is quiet.
//
// A step that fails in a worktree is the host's or the manifest's: the
// attempt is not counted, and the dispatch pauses for a person
// (failUnusable). One that fails in the checkout of a result is the
// result's, counted as a failing check is. A worktree at a checkpoint the
// agent saved is the agent's: a failure there refuses the checkpoint,
// uncounted and unpaused, and the next attempt starts where the
// checkpointed one did.
//
// A run without a manifest (no setup, no manifest) makes no tree and
// keeps no step receipt: it runs as it did before manifests.

// The prerequisites' states (run.prereq): pending once armed, running
// from its generation's first step, then passed or failed. The pump
// dispatches only on passed.
const (
	prereqPending = "pending"
	prereqRunning = "running"
	prereqPassed  = "passed"
	prereqFailed  = "failed"
)

// The default timeouts of the manifest's phases, in seconds, when a step
// gives none (a setup or verify step's is a check's, defaultCheckTimeout).
const (
	defaultPrerequisiteTimeout = 60
	defaultCleanupTimeout      = 60
)

// Sources of a manifest: the crew's, or the file at a commit.
const (
	sourceCrew = "crew"
	sourceFile = "file"
)

// resolvedManifest is a project manifest as a tree, or the run's
// prerequisites, ran it: the manifest and where it came from, the crew at
// its revision or the file as a blob.
type resolvedManifest struct {
	m        Manifest
	source   string
	revision int64
	digest   string
}

// identity is the manifest's name in a receipt: crew:<revision> or
// file:<blob>.
func (rm resolvedManifest) identity() string {
	if rm.source == sourceFile {
		return "file:" + rm.digest
	}
	return fmt.Sprintf("crew:%d", rm.revision)
}

// task is the manifest's identity as a task file names it.
func (rm resolvedManifest) task() *TaskManifest {
	tm := &TaskManifest{Source: rm.source}
	if rm.source == sourceFile {
		tm.Digest = rm.digest
	} else {
		tm.Revision = rm.revision
	}
	return tm
}

// tree is a directory the engine makes usable through the run's project
// manifest: an attempt's worktree, or the checkout of its result. busy is
// held while one of its phases or checks runs (hold), handed is set once it
// is handed to endTree, ending once its end begins (nothing holds it
// after) and ended once its end is recorded; the three under e.mu.
type tree struct {
	kind   string // ledger.TreeWorktree | ledger.TreeCheckout
	dir    string // the worktree or the checkout
	cwd    string // the crew's working directory in it
	commit string
	man    resolvedManifest
	a      *attempt
	busy   sync.WaitGroup
	// handed, ending and ended: see above.
	handed, ending, ended bool
}

// treeExec is everything a tree does on the machine that runs it: a
// command, run as a check is (run), today; the services, the probes and the
// ports follow it. The native one runs proc.Run; a test replaces it whole,
// and the Runtime seam (C10) wraps it, so nothing of a tree reaches the
// machine but through it.
type treeExec struct {
	run func(ctx context.Context, s proc.Spec) proc.Result
}

// nativeExec runs a tree's commands on this machine.
func nativeExec() treeExec { return treeExec{run: proc.Run} }

// flight counts goroutines in flight under e.mu and is waited for without
// it: a sync.WaitGroup must not take an Add while another goroutine waits
// on it at zero, and a tree may be handed over while a stop waits for the
// trees. zero is closed while n is 0.
type flight struct {
	n    int
	zero chan struct{}
}

// add counts one more. The caller holds e.mu.
func (f *flight) add() {
	if f.n == 0 {
		f.zero = make(chan struct{})
	}
	f.n++
}

// done counts one less. The caller holds e.mu.
func (f *flight) done() {
	if f.n--; f.n == 0 {
		close(f.zero)
	}
}

// idle is a channel closed once nothing is in flight. The caller holds
// e.mu, and waits on it without.
func (f *flight) idle() <-chan struct{} {
	if f.n == 0 {
		return closedChan
	}
	return f.zero
}

var closedChan = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// waitTrees waits for r's trees being ended, or for ctx.
func (e *Engine) waitTrees(ctx context.Context, r *run) {
	e.mu.Lock()
	idle := r.trees.idle()
	e.mu.Unlock()
	select {
	case <-idle:
	case <-ctx.Done():
	}
}

// hasManifest reports whether r has a project manifest: a crew's with
// something in it, a legacy setup lifted into one, or the file form. A run
// without one makes no tree.
func (r *run) hasManifest() bool { return r.managed && !r.manifest.empty() }

// stepTimeout is a step's timeout: its own, or its phase's default.
func stepTimeout(phase string, ch Check) time.Duration {
	if ch.TimeoutSeconds > 0 {
		return time.Duration(ch.TimeoutSeconds) * time.Second
	}
	switch phase {
	case ledger.PhasePrerequisite:
		return defaultPrerequisiteTimeout * time.Second
	case ledger.PhaseCleanup:
		return defaultCleanupTimeout * time.Second
	}
	return defaultCheckTimeout * time.Second
}

// errAttemptClosed says the attempt closed before its tree was resolved:
// nothing was made of it.
var errAttemptClosed = errors.New("the attempt closed")

// resolveAt is r's project manifest as a tree at commit has it: the crew's
// as launched, or, with the file form, conductor.manifest.json at commit,
// refused (matching ErrInvalid) when it names what this build does not run
// yet (runnable), so a result's commit or a continuation's candidate brings
// in no phase the engine would skip and no placeholder it would pass on as
// it is. The crew's was held to the same at the launch (prepare).
func (e *Engine) resolveAt(ctx context.Context, r *run, commit string) (resolvedManifest, error) {
	if !r.manifest.File {
		return resolvedManifest{m: r.manifest, source: sourceCrew, revision: r.manifestRev}, nil
	}
	rec, blob, err := resolveManifest(ctx, r.cwd, commit, r.prefix, r.plan)
	if err != nil {
		return resolvedManifest{}, err
	}
	if err := rec.runnable(); err != nil {
		return resolvedManifest{}, fmt.Errorf("%s at %s: %w", r.prefix+manifestFile, short(commit), err)
	}
	return resolvedManifest{m: rec, source: sourceFile, digest: blob}, nil
}

// resolveTree makes the tree of kind at dir (the crew's directory cwd in
// it, at commit) attempt a's: its manifest resolved (resolveAt), then
// manifest.resolved recorded with the tree's row, resolved, in one
// transaction, from which the tree is the attempt's (a.trees) and ends when
// it closes. The transaction is the run's, so it is ordered with the
// attempt's close: an attempt closed before it gets no tree
// (errAttemptClosed). Any other error is the manifest's, its stage
// "manifest".
func (e *Engine) resolveTree(ctx context.Context, r *run, a *attempt, kind, dir, cwd, commit string) (*tree, error) {
	man, err := e.resolveAt(ctx, r, commit)
	if err != nil {
		return nil, err
	}
	t := &tree{kind: kind, dir: dir, cwd: cwd, commit: commit, man: man, a: a}
	closed := false
	err = e.transact(ctx, r, func() tx {
		if !a.closedAt.IsZero() {
			closed = true
			return tx{}
		}
		a.trees = append(a.trees, t)
		payload := map[string]any{"tree": kind, "task": a.t.def.ID, "attempt": a.n, "source": man.source, "env": slices.Clone(man.m.Env)}
		if man.source == sourceFile {
			payload["digest"] = man.digest
		} else {
			payload["revision"] = man.revision
		}
		out := ev("manifest.resolved", payload)
		row := ledger.TreeRow{RunID: r.id, TaskID: a.t.def.ID, N: a.n, Kind: kind, Dir: dir, Commit: commit, Manifest: man.identity(), State: ledger.TreeResolved}
		out.more = func(ltx LedgerTx) error { return ltx.PutTree(row) }
		return out
	}, func() {
		a.trees = slices.DeleteFunc(a.trees, func(x *tree) bool { return x == t })
	})
	switch {
	case closed:
		return nil, errAttemptClosed
	case err != nil:
		return nil, fmt.Errorf("the ledger could not record the manifest: %w", err)
	}
	return t, nil
}

// hold marks t busy for a phase or a check about to run in it, unless its
// end has begun (false: the attempt closed). The caller ends it with
// t.busy.Done.
func (e *Engine) hold(t *tree) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.ending {
		return false
	}
	t.busy.Add(1)
	return true
}

// prepareTree runs t's setup phase, each step a receipt (runStep), in the
// tree's working directory, stopping at the first that fails; in a
// checkout the tree is compared with the result after each step, which ran
// alone in it, so a change is that step's. It returns the stage of a
// failure ("setup <id>") with its words, "" and nil when every step
// passed. The caller holds t (hold).
func (e *Engine) prepareTree(ctx context.Context, r *run, a *attempt, t *tree) (string, error) {
	for _, ch := range t.man.m.Setup {
		stage := "setup " + ch.ID
		if _, err := e.runStep(ctx, r, a, t, ledger.PhaseSetup, ch, 0); err != nil {
			return stage, err
		}
		if t.kind == ledger.TreeCheckout {
			moved, changed, _, cerr := checkoutIntact(ctx, t.dir, t.commit)
			if words := intactWords(moved, changed, cerr); words != "" {
				return stage, fmt.Errorf("setup %s %s", ch.ID, words)
			}
		}
	}
	return "", nil
}

// runStep runs one step of r's project manifest and keeps its receipt:
// with an attempt, a checks row of the phase in tree t (its k the
// attempt's next of the id), else a prerequisite's row of the run, in the
// crew's directory (its k the run's next of the id, gen its generation).
// The row is put started before the process (step.started), running with
// the pid once it exists (step.running) and finished with the exit, the
// timing, the head and tail, the log (at most ledger.MaxLog, its SHA-256
// over the bytes kept) and the words of its failure (step.finished). The
// error is those words, nil for a pass; a row the ledger refuses fails
// the step ("the ledger could not record the step"). A step whose context
// ended before it began is not run and puts no row; one whose context
// ends under it is killed with its group and recorded interrupted, with
// why. A prerequisite's first row marks the generation running, and its
// failure pauses the run's dispatch with its words, in the transaction of
// its finished row, when the generation is still the run's.
func (e *Engine) runStep(ctx context.Context, r *run, a *attempt, t *tree, phase string, ch Check, gen int) (proc.Result, error) {
	if err := ctx.Err(); err != nil {
		return proc.Result{Exit: -1, Canceled: true}, fmt.Errorf("%s was not run: %w", ch.ID, err)
	}
	man, dir, subject := r.baseMan, r.cwd, r.base
	if t != nil {
		man, dir, subject = t.man, t.cwd, t.commit
	}
	e.mu.Lock()
	var k int
	if a != nil {
		k = a.nextK(ch.ID)
	} else {
		r.prereqK[ch.ID]++
		k = r.prereqK[ch.ID]
	}
	e.mu.Unlock()
	row := ledger.CheckRow{RunID: r.id, ID: ch.ID, K: k, Phase: phase, Manifest: man.identity(), Tries: 1, Argv: slices.Clone(ch.Argv), Dir: dir, Subject: subject, State: ledger.CheckStarted, Exit: -1, StartedAt: time.Now().UTC()}
	payload := func(more map[string]any) map[string]any {
		p := map[string]any{"phase": phase, "id": ch.ID, "k": k}
		if a != nil {
			p["task"], p["attempt"], p["tree"] = a.t.def.ID, a.n, t.kind
		}
		for key, v := range more {
			p[key] = v
		}
		return p
	}
	if a != nil {
		row.TaskID, row.N = a.t.def.ID, a.n
	}
	put := func(c ledger.CheckRow) func(LedgerTx) error {
		if a != nil {
			return func(ltx LedgerTx) error { return ltx.PutCheck(c) }
		}
		p := prerequisiteRow(c)
		return func(ltx LedgerTx) error { return ltx.PutPrerequisite(p) }
	}
	// keep is the receipt as the run reports it, a prerequisite's. The
	// caller holds e.mu.
	keep := func(c ledger.CheckRow) {
		if a == nil {
			c.Log = nil
			r.prereqRows[ch.ID] = prereqReceipt{row: c, gen: gen}
		}
	}
	if err := e.transact(ctx, r, func() tx {
		if a == nil && gen == r.prereqGen && r.prereq == prereqPending {
			r.prereq = prereqRunning
		}
		keep(row)
		out := ev("step.started", payload(map[string]any{"manifest": row.Manifest}))
		out.more = put(row)
		return out
	}, giveUp); err != nil {
		if ctx.Err() != nil {
			return proc.Result{Exit: -1, Canceled: true}, fmt.Errorf("%s was not run: %w", ch.ID, ctx.Err())
		}
		return proc.Result{Exit: -1}, fmt.Errorf("the ledger could not record the step %s: %w", ch.ID, err)
	}
	var log bytes.Buffer
	res := e.runCommand(ctx, dir, ch.Argv, stepTimeout(phase, ch), &log, func(p proc.Process) {
		running := row
		running.State, running.PID, running.Group, running.ProcessStartedAt = ledger.CheckRunning, p.PID, p.Group, p.StartedAt
		_ = e.transact(context.Background(), r, func() tx {
			keep(running)
			out := ev("step.running", payload(map[string]any{"pid": p.PID, "group": p.Group}))
			out.more = put(running)
			return out
		}, nil)
	})
	fin := row
	fin.FinishedAt = time.Now().UTC()
	fin.PID, fin.Group, fin.ProcessStartedAt = res.Process.PID, res.Process.Group, res.Process.StartedAt
	fin.Exit, fin.Signal, fin.TimedOut, fin.Killed = res.Exit, res.Signal, res.TimedOut, res.Killed
	fin.Truncated, fin.Total = res.Truncated, res.Total
	fin.Log = bytes.Clone(log.Bytes())
	sum := sha256.Sum256(fin.Log)
	fin.LogSHA256 = hex.EncodeToString(sum[:])
	fin.Tail = string(lastOutput(res, checkTail))
	interrupted := ctx.Err() != nil
	var words string
	if interrupted {
		fin.State = ledger.CheckInterrupted
		words = e.interruptedWhy(r, a, ch)
	} else {
		fin.State = ledger.CheckFinished
		words = stepWords(phase, ch, res)
	}
	fin.Error = session.CleanMessage(words)
	err := e.transact(context.Background(), r, func() tx {
		keep(fin)
		out := ev("step.finished", payload(map[string]any{"exit": res.Exit, "timedOut": res.TimedOut, "duration": res.Duration.String(), "error": fin.Error, "interrupted": interrupted}))
		if a == nil && !interrupted && words != "" && gen == r.prereqGen && !r.stopping && r.outcome == "" {
			r.prereq = prereqFailed
			out.events = append(out.events, e.pauseManifest(r, fmt.Sprintf("the prerequisite %s failed: %s", ch.ID, words))...)
		}
		out.more = put(fin)
		return out
	}, nil)
	switch {
	case words != "":
		return res, errors.New(fin.Error)
	case err != nil:
		return res, fmt.Errorf("the ledger could not record the step %s: %w", ch.ID, err)
	}
	return res, nil
}

// nextK is the next k of id on a: every row of the attempt takes the next
// number of its id, whatever its phase, so no row overwrites another of
// the same id. The caller holds e.mu.
func (a *attempt) nextK(id string) int {
	if a.ks == nil {
		a.ks = map[string]int{}
	}
	a.ks[id]++
	return a.ks[id]
}

// stepWords says why a step failed, "" for a pass.
func stepWords(phase string, ch Check, res proc.Result) string {
	switch {
	case res.Err != "" && !res.TimedOut && !res.Canceled:
		return fmt.Sprintf("%s could not run: %s", ch.ID, session.CleanMessage(res.Err))
	case res.TimedOut:
		return fmt.Sprintf("%s timed out after %s%s", ch.ID, stepTimeout(phase, ch), tailWords(lastOutput(res, checkTail)))
	case res.Canceled:
		return ch.ID + " was cancelled"
	case res.Exit != 0:
		return fmt.Sprintf("%s exited %d%s", ch.ID, res.Exit, tailWords(lastOutput(res, checkTail)))
	}
	return ""
}

// interruptedWhy says why a step's context ended under it: the run
// finished before a prerequisite ended, the run was stopped, or the
// attempt was cancelled or closed.
func (e *Engine) interruptedWhy(r *run, a *attempt, ch Check) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case a == nil && r.outcome != "":
		// The commit that finished the run ended the generation (a stop
		// after it found it ended).
		return fmt.Sprintf("%s was interrupted: the run finished before the prerequisite ended", ch.ID)
	case r.stopping || r.stoppedAt != nil:
		return fmt.Sprintf("%s was interrupted: the run was stopped", ch.ID)
	case a == nil:
		return fmt.Sprintf("%s was interrupted: a later run of the prerequisites took its place", ch.ID)
	case a.state == ledger.AttemptCancelled:
		return fmt.Sprintf("%s was interrupted: the attempt was cancelled", ch.ID)
	}
	return fmt.Sprintf("%s was interrupted: the attempt ended", ch.ID)
}

// prerequisiteRow is a prerequisite's receipt from the row runStep makes.
func prerequisiteRow(c ledger.CheckRow) ledger.PrerequisiteRow {
	return ledger.PrerequisiteRow{RunID: c.RunID, ID: c.ID, K: c.K, Manifest: c.Manifest, Error: c.Error, URL: c.URL, Argv: c.Argv, Dir: c.Dir, Subject: c.Subject,
		State: c.State, PID: c.PID, Group: c.Group, ProcessStartedAt: c.ProcessStartedAt, StartedAt: c.StartedAt, FinishedAt: c.FinishedAt, Exit: c.Exit, Signal: c.Signal,
		TimedOut: c.TimedOut, Killed: c.Killed, Stable: c.Stable, Truncated: c.Truncated, Total: c.Total, Log: c.Log, LogDropped: c.LogDropped, LogSHA256: c.LogSHA256, Tail: c.Tail}
}

// prereqReceipt is a prerequisite's latest receipt, as the run reports it,
// with the generation that made it.
type prereqReceipt struct {
	row ledger.CheckRow
	gen int
}

// armPrerequisites sets r's prerequisites pending, to run once the
// transaction that arms them is committed (transact starts the
// generation), or passed when the run's manifest has none. The caller
// holds e.mu, in a transaction's change.
func (r *run) armPrerequisites() {
	if len(r.baseMan.m.Prerequisites) == 0 {
		r.prereq = prereqPassed
		return
	}
	r.prereq, r.prereqStart = prereqPending, true
}

// rearmPrerequisites arms them again at an unpause, so a host fixed in
// between is seen, unless a generation is in flight (pending or running:
// its result stands), the run has none or it has finished. A generation
// whose last commit is in but whose goroutine has not ended yet starts the
// one armed here as it ends (prerequisites), one at a time. The caller
// holds e.mu, in a transaction's change.
func (r *run) rearmPrerequisites() {
	if len(r.baseMan.m.Prerequisites) == 0 || r.outcome != "" || (r.prereq != prereqPassed && r.prereq != prereqFailed) {
		return
	}
	r.prereq, r.prereqStart = prereqPending, true
}

// startPrerequisites starts a generation of r's prerequisites, armed and
// committed: one at a time (r.prereqs), none once the run stops. The
// generation runs on a goroutine counted in r.starts, under a context of
// its own beneath r.ctx, which a commit that finishes the run cancels
// (settleRun). The caller holds e.mu.
func (e *Engine) startPrerequisites(r *run) {
	if r.stopping || r.ctx.Err() != nil || r.outcome != "" || r.prereqs > 0 || r.prereq != prereqPending {
		return
	}
	r.prereqGen++
	gen := r.prereqGen
	ctx, cancel := context.WithCancel(r.ctx)
	r.prereqs++
	r.prereqCancel = cancel
	r.starts.Add(1)
	go e.prerequisites(ctx, cancel, r, gen)
}

// prerequisites runs r's prerequisites of generation gen, in order, in
// the crew's directory, each a receipt (runStep), and records
// prerequisites.passed once every one passed, then dispatches. One that
// fails pauses the run's dispatch with its words (runStep), and the rest
// do not run. A commit from a generation that is no longer the run's is
// dropped, its rows kept. The count of generations in flight drops once
// its last commit is in, so the run is held (idle) until then; a
// generation armed meanwhile (an unpause after this one's result) starts
// then.
func (e *Engine) prerequisites(ctx context.Context, cancel context.CancelFunc, r *run, gen int) {
	defer r.starts.Done()
	defer func() {
		cancel()
		if e.beforePrereqEnd != nil {
			e.beforePrereqEnd()
		}
		e.mu.Lock()
		r.prereqs--
		if r.prereqGen == gen {
			r.prereqCancel = nil
		}
		e.startPrerequisites(r)
		r.touch()
		e.mu.Unlock()
	}()
	for _, ch := range r.baseMan.m.Prerequisites {
		if _, err := e.runStep(ctx, r, nil, nil, ledger.PhasePrerequisite, ch, gen); err != nil {
			if ctx.Err() == nil {
				e.prerequisiteRefused(r, gen, ch, err)
			}
			return
		}
	}
	passed := false
	err := e.transact(context.Background(), r, func() tx {
		if gen != r.prereqGen || r.stopping || ctx.Err() != nil {
			return tx{}
		}
		r.prereq, passed = prereqPassed, true
		r.note(session.ActivityStatus, "the prerequisites passed")
		return ev("prerequisites.passed", map[string]any{"manifest": r.baseMan.identity()})
	}, nil)
	if passed || err != nil {
		e.afterTransact(r, err)
	}
}

// prerequisiteRefused ends a generation that stopped at ch with err but
// without its failure recorded with the step (the ledger refused the
// step's row): when it is still the run's, the prerequisites are failed
// and the dispatch pauses with why, so the run never waits on a
// generation that has ended, and an unpause runs them again.
func (e *Engine) prerequisiteRefused(r *run, gen int, ch Check, err error) {
	terr := e.transact(context.Background(), r, func() tx {
		if gen != r.prereqGen || r.prereq == prereqFailed || r.stopping || r.outcome != "" {
			return tx{}
		}
		r.prereq = prereqFailed
		return tx{events: e.pauseManifest(r, fmt.Sprintf("the prerequisite %s failed: %v", ch.ID, err))}
	}, nil)
	if terr != nil {
		e.afterTransact(r, terr)
	}
}

// pauseManifest pauses r's dispatch for a project manifest's failure: the
// pause and its words on the run (pausedWhy, written on its row with the
// pause), in the run log ("scheduling paused: <why>") and in the event. The
// caller holds e.mu, in a transaction's change.
func (e *Engine) pauseManifest(r *run, why string) []event {
	why = session.CleanMessage(why)
	if !r.paused || r.pausedWhy != why {
		r.paused, r.pausedWhy = true, why
		r.markRun()
	}
	r.note(session.ActivityError, "scheduling paused: %s", why)
	return []event{{"run.paused", map[string]any{"paused": true, "why": why}}}
}

// unusable is the task's last attempt when its worktree could not be made
// usable (a manifest step failed there, or the manifest could not be
// resolved): not counted, and the next attempt starts at next, where it
// started, or, when it refused a checkpoint the agent saved (checkpoint,
// on branch, saved by attempt checkpointN), where the checkpointed attempt
// started. prev is the counted attempt before it as its own task file
// named it, and prevResult that attempt's result, for a task file that
// has no counted attempt of its own run to name (a continuation's first).
type unusable struct {
	n                  int
	stage, err         string
	next               string
	checkpoint, branch string
	checkpointN        int
	prev               *TaskPrevious
	prevResult         string
}

// failUnusable closes a, whose worktree could not be made usable at stage,
// as failed and uncounted: the task gets one more attempt (extra) and is
// ready with why, and the next attempt starts where a started (unusable).
// A failure at a checkpoint the agent saved (a.start the task's checkpoint
// commit, one the agent made) refuses the checkpoint as a refused result
// is refused: the task's checkpoint is dropped, the next attempt starts
// where the checkpointed attempt started, and the run goes on. Any other
// pauses the run's dispatch with the same words (pauseManifest): a missing
// tool or a dead registry needs a person, and the task is not spent on
// it. The caller holds e.mu, in a transaction's change; a is open.
func (e *Engine) failUnusable(r *run, a *attempt, stage, errText string) []event {
	t := a.t
	cp := t.checkpoint
	atCheckpoint := cp != nil && t.kept(cp.n) && cp.commit != "" && cp.commit != cp.start && a.start == cp.commit
	u := &unusable{n: a.n, stage: stage, err: errText, next: a.start, prev: previousOf(t, a)}
	if old := t.unusableBefore(a); old != nil {
		u.prevResult = old.prevResult
	}
	for _, x := range t.attempts {
		if x.n < a.n && t.kept(x.n) && !x.uncounted {
			u.prevResult = x.result
		}
	}
	e.endAttempt(r, a, ledger.AttemptFailed, stage, errText)
	a.uncounted = true
	e.withdrawBlocker(a, t.blocker)
	t.blocker = nil
	t.extra++
	payload := map[string]any{"task": t.def.ID, "attempt": a.n, "stage": stage, "error": errText, "state": a.state, "uncounted": true}
	why := fmt.Sprintf("attempt %d could not be made usable at %s: %s", a.n, stage, errText)
	if atCheckpoint {
		u.checkpoint, u.branch, u.checkpointN, u.next = cp.commit, cp.branch, cp.n, cp.start
		t.checkpoint = nil
		payload["checkpoint"] = cp.commit
		why = fmt.Sprintf("attempt %d could not be made usable at the checkpoint %s, at %s: %s", a.n, short(cp.commit), stage, errText)
	}
	t.unusable = u
	t.state, t.reason = ledger.TaskReady, why
	r.markTask(t)
	r.ready()
	r.note(session.ActivityStatus, "task %s: %s", t.def.ID, why)
	events := []event{{"attempt.failed", payload}}
	if !atCheckpoint {
		events = append(events, e.pauseManifest(r, why)...)
	}
	return events
}

// treeEnd is a tree handed to endTree: why it ends, and the session it
// waits for (one being stopped), "" for none.
type treeEnd struct {
	t       *tree
	why     string
	session string
}

// handTrees queues a's trees for endTree, each once, as a's close is
// made: the commit that makes the close durable starts them (transact).
// A worktree whose session the close stops (a cancel, a withdrawal, a stop,
// a failure that stops it) waits for that session's end. The caller holds
// e.mu, in a transaction's change.
func (e *Engine) handTrees(r *run, a *attempt) {
	stopping := a.sessionID != "" && (r.stopping || slices.Contains(r.pendingStops, a.sessionID))
	why := fmt.Sprintf("attempt %d %s%s", a.n, a.state, suffix(a.stage, a.err))
	for _, t := range a.trees {
		if t.handed {
			continue
		}
		t.handed = true
		h := treeEnd{t: t, why: why}
		if stopping && t.kind == ledger.TreeWorktree {
			h.session = a.sessionID
		}
		r.pendingTrees = append(r.pendingTrees, h)
	}
}

// startTrees starts the ends a committed close handed over, each on a
// goroutine of its own counted in r.trees. The caller holds e.mu.
func (e *Engine) startTrees(r *run) {
	for _, h := range r.pendingTrees {
		r.trees.add()
		go e.endTree(r, h)
	}
	r.pendingTrees = nil
}

// endTree ends a tree, once, after its attempt's close is committed: under
// r.teardown, never the attempt's context (ended by then), it marks the
// tree ending, waits for it to be quiet (the phase or check that held it
// returned; the session being stopped ended, bounded by stopTimeout) and
// records tree.ended with the tree's row ended. Nothing runs in it after:
// it has no services and no cleanup steps yet. A refused commit is kept
// for the run's next (transact), and a row left not ended is concluded by
// the recovery at the next start.
func (e *Engine) endTree(r *run, h treeEnd) {
	t := h.t
	defer func() {
		e.mu.Lock()
		r.trees.done()
		r.touch()
		e.mu.Unlock()
	}()
	e.mu.Lock()
	t.ending = true
	e.mu.Unlock()
	t.busy.Wait()
	if h.session != "" {
		if l, ok := e.lookup(h.session); ok {
			select {
			case <-l.Ended():
			case <-time.After(stopTimeout):
			case <-r.teardown.Done():
			}
		}
	}
	if e.beforeTreeEnd != nil {
		e.beforeTreeEnd(t.kind)
	}
	a := t.a
	_ = e.transact(r.teardown, r, func() tx {
		if t.ended {
			return tx{}
		}
		t.ended = true
		out := ev("tree.ended", map[string]any{"tree": t.kind, "task": a.t.def.ID, "attempt": a.n, "why": h.why})
		row := ledger.TreeRow{RunID: r.id, TaskID: a.t.def.ID, N: a.n, Kind: t.kind, Dir: t.dir, Commit: t.commit, Manifest: t.man.identity(), State: ledger.TreeEnded, Why: session.CleanMessage(h.why), EndedAt: time.Now().UTC()}
		out.more = func(ltx LedgerTx) error { return ltx.PutTree(row) }
		return out
	}, nil)
}

// RunManifest is the run's project manifest as the run reports it: where
// it comes from (the crew's at its revision, or the file at the base, its
// blob) and how its prerequisites go.
type RunManifest struct {
	Source        string              `json:"source"`
	Revision      int64               `json:"revision,omitempty"`
	Digest        string              `json:"digest,omitempty"`
	Prerequisites []PrerequisiteState `json:"prerequisites,omitempty"`
}

// PrerequisiteState is a prerequisite of the run as it goes: its latest
// run's k (0 before its first), and its state: pending (not run in the
// generation), running (while its generation runs), passed, failed or
// interrupted, with its exit, its words and how long it took, in
// milliseconds.
type PrerequisiteState struct {
	ID       string `json:"id"`
	K        int    `json:"k,omitempty"`
	State    string `json:"state"`
	Exit     *int   `json:"exit,omitempty"`
	Error    string `json:"error,omitempty"`
	Duration int64  `json:"duration,omitempty"`
}

// manifestState is r's manifest as reported, nil without one. The caller
// holds e.mu.
func (r *run) manifestState() *RunManifest {
	if !r.hasManifest() {
		return nil
	}
	rm := &RunManifest{Source: r.baseMan.source, Revision: r.baseMan.revision, Digest: r.baseMan.digest}
	inFlight := r.prereq == prereqPending || r.prereq == prereqRunning
	for _, ch := range r.baseMan.m.Prerequisites {
		ps := PrerequisiteState{ID: ch.ID, State: "pending"}
		rc, ok := r.prereqRows[ch.ID]
		if ok {
			ps.K = rc.row.K
		}
		switch {
		case ok && rc.gen == r.prereqGen && rc.row.State == ledger.CheckFinished:
			exit := rc.row.Exit
			ps.Exit, ps.Error, ps.Duration = &exit, rc.row.Error, rc.row.FinishedAt.Sub(rc.row.StartedAt).Milliseconds()
			ps.State = "passed"
			if rc.row.Error != "" {
				ps.State = "failed"
			}
		case ok && rc.gen == r.prereqGen && rc.row.State == ledger.CheckInterrupted:
			ps.State, ps.Error = "interrupted", rc.row.Error
		case inFlight:
			ps.State = "running"
		}
		rm.Prerequisites = append(rm.Prerequisites, ps)
	}
	return rm
}

// StepState is a receipt as the task's read and the receipt routes report
// it: the row whole but for its log, whose size and SHA-256 it says. A
// probe over HTTP has its URL for a command; exit and finishedAt come once
// it ended; duration is in milliseconds.
type StepState struct {
	Phase      string         `json:"phase"`
	ID         string         `json:"id"`
	K          int            `json:"k"`
	State      string         `json:"state"`
	Argv       []string       `json:"argv,omitempty"`
	URL        string         `json:"url,omitempty"`
	Dir        string         `json:"dir,omitempty"`
	Subject    string         `json:"subject,omitempty"`
	Manifest   string         `json:"manifest,omitempty"`
	Exit       *int           `json:"exit,omitempty"`
	TimedOut   bool           `json:"timedOut,omitempty"`
	Duration   int64          `json:"duration,omitempty"`
	Error      string         `json:"error,omitempty"`
	Ports      map[string]int `json:"ports,omitempty"`
	Tries      int            `json:"tries"`
	StartedAt  time.Time      `json:"startedAt"`
	FinishedAt *time.Time     `json:"finishedAt,omitempty"`
	LogBytes   int64          `json:"logBytes"`
	LogDropped bool           `json:"logDropped,omitempty"`
	LogSHA256  string         `json:"logSha256,omitempty"`
}

// StepStates is an attempt's receipts as reported, by when each started.
func StepStates(rows []ledger.CheckRow) []StepState {
	out := make([]StepState, 0, len(rows))
	for _, c := range rows {
		out = append(out, stepState(c.Phase, c.ID, c.K, c.State, c.Argv, c.URL, c.Dir, c.Subject, c.Manifest, c.Exit, c.TimedOut, c.Error, c.Ports, c.Tries, c.StartedAt, c.FinishedAt, c.LogBytes, c.LogDropped, c.LogSHA256))
	}
	sortSteps(out)
	return out
}

// PrerequisiteStates is a run's prerequisites' receipts as reported, by
// when each started.
func PrerequisiteStates(rows []ledger.PrerequisiteRow) []StepState {
	out := make([]StepState, 0, len(rows))
	for _, c := range rows {
		out = append(out, stepState(ledger.PhasePrerequisite, c.ID, c.K, c.State, c.Argv, c.URL, c.Dir, c.Subject, c.Manifest, c.Exit, c.TimedOut, c.Error, nil, 1, c.StartedAt, c.FinishedAt, c.LogBytes, c.LogDropped, c.LogSHA256))
	}
	sortSteps(out)
	return out
}

func stepState(phase, id string, k int, state string, argv []string, url, dir, subject, manifest string, exit int, timedOut bool, errText string, ports map[string]int, tries int,
	started, finished time.Time, logBytes int64, dropped bool, sha string) StepState {
	st := StepState{Phase: phase, ID: id, K: k, State: state, Argv: argv, URL: url, Dir: dir, Subject: subject, Manifest: manifest, TimedOut: timedOut, Error: errText,
		Ports: ports, Tries: max(tries, 1), StartedAt: started, LogBytes: logBytes, LogDropped: dropped, LogSHA256: sha}
	if !finished.IsZero() {
		at := finished
		st.FinishedAt = &at
		st.Duration = finished.Sub(started).Milliseconds()
		if state == ledger.CheckFinished {
			x := exit
			st.Exit = &x
		}
	}
	return st
}

func sortSteps(steps []StepState) {
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].StartedAt.Before(steps[j].StartedAt) })
}

// runCommand runs one command of a tree, a check or a step, in dir
// through the tree's executor: a process group of its own, no PTY, the
// environment without Conductor's tokens, the output's head and tail kept,
// the log to log, whole within ledger.MaxLog.
func (e *Engine) runCommand(ctx context.Context, dir string, argv []string, timeout time.Duration, log io.Writer, started func(proc.Process)) proc.Result {
	return e.exec.run(ctx, proc.Spec{Argv: slices.Clone(argv), Dir: dir, Timeout: timeout, Head: checkHead, Tail: checkTail, Log: log, LogLimit: ledger.MaxLog, Started: started})
}
