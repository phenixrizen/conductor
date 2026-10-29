package crew

import "context"

// endRuns ends the engine's runs, once, before the first of its processes
// is ended at the test's end; see endRuns. A test that failed is not waited
// for.
func (f *fakeLauncher) endRuns() {
	f.ended.Do(func() {
		if f.engine != nil {
			endRuns(f.engine, !f.t.Failed())
		}
	})
}

// endRuns ends e's runs as the server's end would, recording nothing: each
// run stops dispatching and its context ends, so that no attempt's watcher
// takes its session's end for a failure (watchAttempt returns with the run)
// and nothing is dispatched after the test; with wait, the starts, handoffs
// and checks under way are waited out. The ledger keeps what the test left
// in it: a test that abandoned an engine, as the server that ended before a
// continuation, wrote the rows the recovery would.
//
// An engine left running past its test saw its processes end at the test's
// cleanup, failed the attempt they ran and dispatched the next one, whose
// `git worktree add` wrote into the test's repository while t.TempDir
// removed it: "TempDir RemoveAll cleanup: ... .git: directory not empty"
// (#391).
//
// A test that failed is not waited for: it may have stopped with a gate of
// its own still shut (a fake ledger's held transaction, a hook's release),
// and waiting on the work behind that gate would hang the package's run
// where the test only failed. What it leaves behind misleads nothing.
func endRuns(e *Engine, wait bool) {
	type held struct {
		r      *run
		cancel context.CancelFunc
	}
	e.mu.Lock()
	runs := make([]held, 0, len(e.runs))
	for _, r := range e.runs {
		r.stopping = true
		runs = append(runs, held{r, r.cancel})
	}
	e.mu.Unlock()
	for _, h := range runs {
		if h.cancel != nil {
			h.cancel()
		}
	}
	if !wait {
		return
	}
	for _, h := range runs {
		waitFor(context.Background(), &h.r.starts)
		waitFor(context.Background(), &h.r.deliveries)
		waitFor(context.Background(), &h.r.checks)
	}
}
