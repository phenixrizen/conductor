package crew

import "context"

// endRuns ends the engine's runs, once, before the first of its processes
// is ended at the test's end; see endRuns.
func (f *fakeLauncher) endRuns() {
	f.ended.Do(func() {
		if f.engine != nil {
			endRuns(f.engine)
		}
	})
}

// endRuns ends e's runs as the server's end would, recording nothing: each
// run stops dispatching and its context ends, so that no attempt's watcher
// takes its session's end for a failure (watchAttempt returns with the run)
// and nothing is dispatched after the test; the starts, handoffs and checks
// under way are waited out. The ledger keeps what the test left in it: a
// test that abandoned an engine, as the server that ended before a
// continuation, wrote the rows the recovery would.
//
// An engine left running past its test saw its processes end at the test's
// cleanup, failed the attempt they ran and dispatched the next one, whose
// `git worktree add` wrote into the test's repository while t.TempDir
// removed it: "TempDir RemoveAll cleanup: ... .git: directory not empty"
// (#391).
func endRuns(e *Engine) {
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
		waitFor(context.Background(), &h.r.starts)
		waitFor(context.Background(), &h.r.deliveries)
		waitFor(context.Background(), &h.r.checks)
	}
}
