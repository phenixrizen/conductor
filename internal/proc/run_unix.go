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
	// output, once noteKills is called, is the output's end of file
	// (launched.eof); killed says a SIGKILL has been sent since, and
	// killedOpen whether the output was open when the first was.
	output             <-chan struct{}
	killed, killedOpen bool
	// looks counts the looks at the group (members, still), for looking.
	looks int
}

// looking, when set, is called before each look at the group, outside the
// lifecycle's lock: n numbers the looks from 1, and listing says it is a
// listing (members) rather than a follow (still). Its answer makes a
// listing fail. It is the tests' way to order a look against a member's
// end or a kill, and to the path a listing that fails takes. Set it before
// the process starts.
var looking func(n int, listing bool) (fail bool)

// look numbers a look at the group for looking, and says whether a
// listing is to fail.
func (g *lifecycle) look(listing bool) (fail bool) {
	if looking == nil {
		return false
	}
	g.mu.Lock()
	g.looks++
	n := g.looks
	g.mu.Unlock()
	return looking(n, listing)
}

// noteKills has the first SIGKILL sent from now on noted with whether
// output was open when it was sent (killedHolding): a stopped service's
// wait for its group (settleStopped) runs beside the stop's kill and the
// context's, and reads from the note what such a kill took.
func (g *lifecycle) noteKills(output <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.output = output
}

// killedHolding says whether a SIGKILL has been sent since noteKills, and
// whether the output was open when the first was: what the kill took held
// it.
func (g *lifecycle) killedHolding() (killed, held bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.killed, g.killedOpen
}

// signalled, when set, is told each signal signal sends, once it is sent:
// to the group, or to the process alone (alone). It is the tests' way to
// see what is sent, and to let a process handle the group's SIGTERM before
// anything more is sent. Set it before the process starts.
var signalled func(s sig, alone bool)

// signal sends s to the process's group, and to the process itself should
// it have joined another group (Setpgid makes a group; it does not keep the
// process in it): sigKill kills them, sigTerm asks them to end (a service's
// stop). A SIGTERM reaches the process once: it is sent one of its own only
// when it is no longer in the group, since a second, come after its handler
// has put the default back while it cleans up, would end it before its
// grace. One whose group cannot be read is sent none: unreaped, its pid
// is still its own, so it has exited and needs none. A SIGKILL, which no
// handler sees, goes to it either way; the first after noteKills is
// noted with whether the output was open. It is the one place proc
// signals a process. os.ErrProcessDone once the process is reaped, or
// before it exists.
func (g *lifecycle) signal(cmd *exec.Cmd, s sig) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reaped || cmd.Process == nil {
		return os.ErrProcessDone
	}
	pid := cmd.Process.Pid
	n := syscall.SIGKILL
	if s == sigTerm {
		n = syscall.SIGTERM
	} else if g.output != nil && !g.killed {
		g.killed, g.killedOpen = true, outputOpen(g.output)
	}
	syscall.Kill(-pid, n)
	if signalled != nil {
		signalled(s, false)
	}
	if s == sigTerm {
		if pgid, err := syscall.Getpgid(pid); err != nil || pgid == pid {
			return nil
		}
	}
	err := cmd.Process.Signal(n)
	if signalled != nil {
		signalled(s, true)
	}
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
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
	fail := g.look(true)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reaped || cmd.Process == nil {
		return nil, true
	}
	if fail {
		return nil, false
	}
	pids, err := groupMembers(cmd.Process.Pid, cmd.Process.Pid)
	return pids, err == nil
}

// still is those of pids, members found before, that are still in cmd's
// process group and have not exited, asked as members is.
func (g *lifecycle) still(cmd *exec.Cmd, pids []int) []int {
	g.look(false)
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
