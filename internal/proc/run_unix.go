//go:build unix

package proc

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// lifecycle orders the signals to the process and its group against the
// reaping of the process: a signal to the group by its number is safe only
// while the process, exited or not, still holds its pid, which it does
// until it is reaped, so no signal is sent once reap has begun, and reap
// waits for a signal in flight.
type lifecycle struct {
	mu     sync.Mutex
	reaped bool
}

// signal sends s to the process's group, and to the process itself should
// it have joined another group (Setpgid makes a group; it does not keep the
// process in it): sigKill kills them, sigTerm asks them to end (a service's
// stop). It is the one place proc signals a process. os.ErrProcessDone once
// the process is reaped, or before it exists.
func (g *lifecycle) signal(cmd *exec.Cmd, s sig) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reaped || cmd.Process == nil {
		return os.ErrProcessDone
	}
	n := syscall.SIGKILL
	if s == sigTerm {
		n = syscall.SIGTERM
	}
	syscall.Kill(-cmd.Process.Pid, n)
	if err := cmd.Process.Signal(n); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// members is the processes of cmd's process group that have not exited,
// cmd's own left out, and known whether the group could be listed. It is
// asked only before the reaping, while the exited process keeps the
// group's id its own, so the answer is about this group and no later one;
// the group is listed (groupMembers), never signalled, since a signal 0 to
// it would reach the exited process itself, which stays in the group until
// it is reaped.
func (g *lifecycle) members(cmd *exec.Cmd) (pids []int, known bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reaped || cmd.Process == nil {
		return nil, true
	}
	pids, err := groupMembers(cmd.Process.Pid, cmd.Process.Pid)
	return pids, err == nil
}

// still is those of pids, members found before, that are still in cmd's
// process group and have not exited, asked as members is.
func (g *lifecycle) still(cmd *exec.Cmd, pids []int) []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reaped || cmd.Process == nil {
		return nil
	}
	return stillMembers(cmd.Process.Pid, pids)
}

// reap waits for the process (exec reaps it), after which no signal is sent.
func (g *lifecycle) reap(cmd *exec.Cmd) error {
	g.mu.Lock()
	g.reaped = true
	g.mu.Unlock()
	return cmd.Wait()
}

// attr starts the command in a process group of its own and, when its
// context ends (the timeout, or the caller's), kills the group and the
// process through the lifecycle, so never after the reaping.
func attr(cmd *exec.Cmd, g *lifecycle) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return g.signal(cmd, sigKill) }
}

// noServiceRunner is nil here: Serve runs a service. A test makes it the
// refusal of a platform without a process group (run_other.go).
var noServiceRunner = func() error { return nil }

// group is the command's process group: its own pid (Setpgid).
func group(cmd *exec.Cmd) int { return cmd.Process.Pid }

// exitStatus reads the exit status, or the signal that ended the process.
func exitStatus(state *os.ProcessState) (int, string) {
	ws, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return state.ExitCode(), ""
	}
	if ws.Signaled() {
		return -1, ws.Signal().String()
	}
	return ws.ExitStatus(), ""
}

// errExited says the process had exited before its exit could be watched:
// it is then a zombie, unreaped, its group's id still its own.
var errExited = errors.New("proc: exited already")

// watchFails, when set, makes every exit watch fail: the tests' way to the
// path a watch that cannot be set up takes.
var watchFails = false

// watchExit reports the process's exit without reaping it (watchPID: a
// waitid without reaping on Linux, a kqueue on macOS) and gives the wait
// that reaps it after. The channel gives nil for an exit seen, or the
// error of a watch that failed while the process may be alive still. A
// process gone before the watch was set up counts as exited. A watch that
// cannot be set up is an error: Run then ends the command rather than
// guess at its exit, since a signal to the group after the reaping could
// reach another's.
func watchExit(cmd *exec.Cmd, g *lifecycle) (<-chan error, func() error, error) {
	exited := make(chan error, 1)
	if watchFails {
		return nil, nil, errors.New("the watch was made to fail")
	}
	stop, err := watchPID(cmd.Process.Pid, func(err error) { exited <- err })
	switch {
	case errors.Is(err, errExited):
		exited <- nil
		return exited, func() error { return g.reap(cmd) }, nil
	case err != nil:
		return nil, nil, err
	}
	return exited, func() error {
		err := g.reap(cmd)
		stop()
		return err
	}, nil
}
