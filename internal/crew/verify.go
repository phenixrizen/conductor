package crew

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
	"github.com/phenixrizen/conductor/internal/gitcli"
	"github.com/phenixrizen/conductor/internal/proc"
	"github.com/phenixrizen/conductor/internal/session"
)

// Check capture bounds (proc.Spec).
const (
	checkHead = 64 << 10
	checkTail = 2 << 10
)

// checkSlots is the bound on checks running at once across the server's
// managed runs: one, until SetCheckConcurrency says otherwise.
func (e *Engine) checkSlots() chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.checkSem == nil {
		e.checkSem = make(chan struct{}, 1)
	}
	return e.checkSem
}

// SetCheckConcurrency bounds the checks running at once across the
// server's managed runs (config checks.maxConcurrent; a go test beside
// four agents on a laptop wants one). Set it before the first launch.
func (e *Engine) SetCheckConcurrency(n int) {
	n = max(n, 1)
	e.mu.Lock()
	e.checkSem = make(chan struct{}, n)
	e.mu.Unlock()
}

// runCommand runs one check or setup command in dir through the bounded
// runner: a process group of its own, no PTY, the environment without
// Conductor's tokens, the output's head and tail kept, the log to log.
func (e *Engine) runCommand(ctx context.Context, dir string, ch Check, log io.Writer, started func(proc.Process)) proc.Result {
	return proc.Run(ctx, proc.Spec{Argv: slices.Clone(ch.Argv), Dir: dir, Timeout: checkTimeout(ch), Head: checkHead, Tail: checkTail, Log: log, LogLimit: ledger.MaxLog, Started: started})
}

func checkTimeout(ch Check) time.Duration {
	if ch.TimeoutSeconds > 0 {
		return time.Duration(ch.TimeoutSeconds) * time.Second
	}
	return defaultCheckTimeout * time.Second
}

// verify decides a's submitted result. A review, or a work task with no
// checks, succeeds at once. A work task's checks run in a detached
// checkout of the result commit (<worktree>.check), after the crew's
// setup there (what the worker had, the checks have; a setup that changes
// tracked content fails the attempt), one check at a time across the
// server (checkSlots), each recorded before it starts, with its process
// once started, and with its outcome when it ends. After each one the
// checkout is compared with the commit as git would store it
// (checkoutIntact): a check that cannot run, exits non-zero, times out,
// moves the checkout or changes tracked content fails the attempt at that
// check, and the rest do not run; untracked outputs are listed with the
// check. It runs on a goroutine the run's stop waits for (r.checks) and
// ends with the attempt's context.
func (e *Engine) verify(r *run, a *attempt) {
	defer r.checks.Done()
	t := a.t
	if t.def.Kind == TaskReview || len(t.def.Checks) == 0 {
		e.accept(r, a, true, "", "")
		return
	}
	ctx := a.ctx
	dir := a.worktree + ".check"
	e.gitMu.Lock()
	err := AddDetachedWorktree(ctx, r.cwd, dir, a.result)
	e.gitMu.Unlock()
	if err != nil {
		e.accept(r, a, false, "checkout", session.CleanMessage(err.Error()))
		return
	}
	if head, err := headCommit(ctx, dir); err != nil || head != a.result {
		e.accept(r, a, false, "checkout", fmt.Sprintf("the checkout is at %s, not the result %s", short(head), short(a.result)))
		return
	}
	// The crew's directory in the checkout, as in the worktree: not
	// through a link the result commits, which would send the checks
	// elsewhere than the tree compared.
	if err := checkNoLinks(dir, r.prefix); err != nil {
		e.accept(r, a, false, "checkout", session.CleanMessage(err.Error()))
		return
	}
	cwd := filepath.Join(dir, r.prefix)
	for _, ch := range r.setup {
		if ctx.Err() != nil {
			return
		}
		if err := e.runSetup(ctx, r, a, cwd, ch, "check.setup"); err != nil {
			e.accept(r, a, false, "setup "+ch.ID, session.CleanMessage(err.Error()))
			return
		}
	}
	if len(r.setup) > 0 {
		moved, changed, _, cerr := checkoutIntact(ctx, dir, a.result)
		if words := intactWords(moved, changed, cerr); words != "" {
			e.accept(r, a, false, "setup", "the setup "+words)
			return
		}
	}
	for _, ch := range t.def.Checks {
		if ctx.Err() != nil {
			return // closed meanwhile: cancelled or stopped
		}
		row := ledger.CheckRow{RunID: r.id, TaskID: t.def.ID, N: a.n, ID: ch.ID, K: 1, Argv: slices.Clone(ch.Argv), Dir: cwd, Subject: a.result, State: ledger.CheckStarted, StartedAt: time.Now().UTC()}
		if err := e.transact(ctx, r, func() tx {
			out := ev("check.started", map[string]any{"task": t.def.ID, "attempt": a.n, "check": ch.ID, "subject": a.result})
			out.more = func(ltx LedgerTx) error { return ltx.PutCheck(row) }
			return out
		}, nil); err != nil {
			e.accept(r, a, false, "check "+ch.ID, "the ledger could not record the check: "+session.CleanMessage(err.Error()))
			return
		}
		sem := e.checkSlots()
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		var log bytes.Buffer
		res := e.runCommand(ctx, cwd, ch, &log, func(p proc.Process) {
			running := row
			running.State, running.PID, running.Group, running.ProcessStartedAt = ledger.CheckRunning, p.PID, p.Group, p.StartedAt
			_ = e.transact(context.Background(), r, func() tx {
				out := ev("check.running", map[string]any{"task": t.def.ID, "attempt": a.n, "check": ch.ID, "pid": p.PID, "group": p.Group})
				out.more = func(ltx LedgerTx) error { return ltx.PutCheck(running) }
				return out
			}, nil)
		})
		<-sem
		if ctx.Err() != nil {
			// Cancelled or stopped under the check: the attempt is closed
			// by what cancelled it, with the check's end recorded.
			fin := row
			fin.State, fin.FinishedAt, fin.Killed, fin.Exit, fin.Signal = ledger.CheckInterrupted, time.Now().UTC(), res.Killed, res.Exit, res.Signal
			_ = e.transact(context.Background(), r, func() tx {
				out := ev("check.finished", map[string]any{"task": t.def.ID, "attempt": a.n, "check": ch.ID, "interrupted": true, "duration": res.Duration.String()})
				out.more = func(ltx LedgerTx) error { return ltx.PutCheck(fin) }
				return out
			}, nil)
			return
		}
		moved, changed, untracked, cerr := checkoutIntact(ctx, dir, a.result)
		fin := row
		fin.State, fin.FinishedAt = ledger.CheckFinished, time.Now().UTC()
		fin.PID, fin.Group, fin.ProcessStartedAt = res.Process.PID, res.Process.Group, res.Process.StartedAt
		fin.Exit, fin.Signal, fin.TimedOut, fin.Killed = res.Exit, res.Signal, res.TimedOut, res.Killed
		fin.Stable, fin.Truncated, fin.Total = cerr == nil && !moved && len(changed) == 0, res.Truncated, res.Total
		fin.Log = bytes.Clone(log.Bytes())
		sum := sha256.Sum256(fin.Log)
		fin.LogSHA256 = hex.EncodeToString(sum[:])
		fin.Tail = string(lastOutput(res, checkTail))
		words := checkWords(ch, res, moved, changed, cerr)
		// The check's outcome is evidence: not recorded, the result is not
		// taken on it.
		if err := e.transact(context.Background(), r, func() tx {
			out := ev("check.finished", map[string]any{"task": t.def.ID, "attempt": a.n, "check": ch.ID, "exit": res.Exit, "timedOut": res.TimedOut, "stable": fin.Stable,
				"changed": bounded(changed), "changedCount": len(changed), "untracked": bounded(untracked), "untrackedCount": len(untracked), "duration": res.Duration.String(), "error": words})
			out.more = func(ltx LedgerTx) error { return ltx.PutCheck(fin) }
			return out
		}, nil); err != nil {
			e.accept(r, a, false, "check "+ch.ID, "the ledger could not record the check's outcome: "+session.CleanMessage(err.Error()))
			return
		}
		if words != "" {
			e.accept(r, a, false, "check "+ch.ID, words)
			return
		}
	}
	e.accept(r, a, true, "", "")
}

// checkWords says why a check failed, "" for a pass.
func checkWords(ch Check, res proc.Result, moved bool, changed []string, cerr error) string {
	switch {
	case res.Err != "" && !res.TimedOut && !res.Canceled:
		return "could not run: " + session.CleanMessage(res.Err)
	case res.TimedOut:
		return fmt.Sprintf("timed out after %s%s", checkTimeout(ch), tailWords(lastOutput(res, checkTail)))
	case res.Canceled:
		return "cancelled"
	case res.Exit != 0:
		return fmt.Sprintf("exited %d%s", res.Exit, tailWords(lastOutput(res, checkTail)))
	}
	return intactWords(moved, changed, cerr)
}

// intactWords says what a command did to the checkout, "" for nothing.
func intactWords(moved bool, changed []string, err error) string {
	switch {
	case err != nil:
		return "left a checkout that could not be compared: " + session.CleanMessage(err.Error())
	case moved:
		return "moved the checkout to another commit"
	case len(changed) > 0:
		list := changed
		if len(list) > 10 {
			list = append(slices.Clone(list[:10]), "…")
		}
		return "changed tracked files: " + strings.Join(list, ", ")
	}
	return ""
}

// checkoutIntact compares the checkout at dir with the commit as git
// would store it: HEAD still the commit, and every tracked path's
// content, mode and link target as the commit's tree has them, read
// through an index of its own (read-tree, add -u, write-tree) that
// carries no flag a command could set to hide a change. It reports
// whether HEAD moved, the tracked paths that differ, and the untracked
// paths left (ignored ones aside).
func checkoutIntact(ctx context.Context, dir, commit string) (moved bool, changed, untracked []string, err error) {
	head, err := headCommit(ctx, dir)
	if err != nil {
		return false, nil, nil, err
	}
	if head != commit {
		return true, nil, nil, nil
	}
	f, err := os.CreateTemp("", "conductor-index-")
	if err != nil {
		return false, nil, nil, err
	}
	index := f.Name()
	f.Close()
	os.Remove(index) // git makes it
	defer os.Remove(index)
	env := map[string]string{"GIT_INDEX_FILE": index}
	if modesKept(dir) {
		// The filesystem keeps the executable bit: the comparison keeps it
		// too, whatever core.fileMode the repository set, so a check that
		// makes a file executable is seen.
		env["GIT_CONFIG_COUNT"], env["GIT_CONFIG_KEY_0"], env["GIT_CONFIG_VALUE_0"] = "1", "core.fileMode", "true"
	}
	if _, err := gitcli.RunEnv(ctx, dir, env, "read-tree", commit); err != nil {
		return false, nil, nil, err
	}
	if _, err := gitcli.RunEnv(ctx, dir, env, "add", "-u"); err != nil {
		return false, nil, nil, err
	}
	tree, err := gitcli.RunEnv(ctx, dir, env, "write-tree")
	if err != nil {
		return false, nil, nil, err
	}
	want, err := gitcli.Run(ctx, dir, "rev-parse", commit+"^{tree}")
	if err != nil {
		return false, nil, nil, err
	}
	if strings.TrimSpace(tree) != strings.TrimSpace(want) {
		out, err := gitcli.RunEnv(ctx, dir, env, "diff", "--cached", "--name-only", "-z", commit)
		if err != nil {
			return false, nil, nil, err
		}
		changed = splitNUL(out)
		if len(changed) == 0 {
			changed = []string{"(the tree differs)"}
		}
	}
	out, err := gitcli.RunEnv(ctx, dir, env, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return false, changed, nil, err
	}
	return false, changed, splitNUL(out), nil
}

// maxListed bounds a path list in an event's payload.
const maxListed = 50

// bounded is the first maxListed of paths, with a mark for the rest.
func bounded(paths []string) []string {
	if len(paths) <= maxListed {
		return paths
	}
	return append(slices.Clone(paths[:maxListed]), "…")
}

// modesKept reports whether the filesystem under dir keeps the executable
// bit, by setting it on a file made there for the look and removed.
func modesKept(dir string) bool {
	f, err := os.CreateTemp(dir, ".conductor-mode-")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	if err := os.Chmod(name, 0o755); err != nil {
		return false
	}
	fi, err := os.Lstat(name)
	return err == nil && fi.Mode().Perm()&0o100 != 0
}

// splitNUL splits a -z listing into its paths.
func splitNUL(out string) []string {
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// lastOutput is the last n bytes of what the process printed, whether or
// not its output outgrew the head proc.Result keeps first: a short output
// is all in Head and Tail is empty then, which left a failed check's words
// and its recorded tail empty (#355).
func lastOutput(res proc.Result, n int) []byte {
	all := res.Tail
	if !res.Truncated {
		all = append(append([]byte(nil), res.Head...), res.Tail...)
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}
