package proc

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A watch that fails once set up (waitid refused by a seccomp filter, say)
// saw no exit: the command is ended and the result says so, never an exit
// invented.
func TestRunReportsAWatchThatFailed(t *testing.T) {
	old := waitid
	waitid = func(int, int, *unix.Siginfo, int, *unix.Rusage) error { return unix.ENOSYS }
	t.Cleanup(func() { waitid = old })
	res := sh(t, time.Minute, "sleep 30")
	if !strings.Contains(res.Err, "could not be watched") || !strings.Contains(res.Err, "function not implemented") || res.Signal != "killed" || res.TimedOut {
		t.Fatalf("%+v", res)
	}
	if !gone(t, res.Process.PID, 3*time.Second) {
		t.Fatalf("the process %d outlived the run", res.Process.PID)
	}
}
