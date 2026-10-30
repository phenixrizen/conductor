package api

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

// TestMain runs the package's tests, then fails the run when a Dispatch
// store was left open. Every server New makes over a store opens one,
// whose pruning goroutine runs until the store is closed (WaitBackground,
// Shutdown): a server a test made and never ended (endAtCleanup), a
// second one over the first's store among them, shows here as that
// goroutine once every test and its cleanups are done.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if n := openDispatchStores(); n > 0 {
			fmt.Fprintf(os.Stderr, "FAIL: the tests left %d Dispatch store(s) open: a server a test made was never ended (endAtCleanup)\n", n)
			code = 1
		}
	}
	os.Exit(code)
}

// leftOpenWait bounds the wait for a closed store's pruning goroutine to
// end: Close waits for its done, which the goroutine closes on its way
// out, so it may show for a moment after. It bounds only a failure.
const leftOpenWait = 5 * time.Second

// openDispatchStores is how many Dispatch stores are open: their pruning
// goroutines, polled until none is left or leftOpenWait has passed.
func openDispatchStores() int {
	n := 0
	waitFor(leftOpenWait, func() bool {
		n = pruningGoroutines()
		return n == 0
	})
	return n
}

// pruningGoroutines counts the goroutines running a Dispatch store's
// pruning, from every goroutine's stack.
func pruningGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return bytes.Count(buf[:n], []byte("internal/dispatch.(*Store).pruning("))
		}
		buf = make([]byte, 2*len(buf))
	}
}
