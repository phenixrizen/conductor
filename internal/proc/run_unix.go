//go:build unix

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

// attr starts the command in a process group of its own and kills the whole
// group when its context ends (the timeout, or the caller's), before the
// process is reaped, so its group is still its own.
func attr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

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

// killGroup kills the command's process group. Run calls it once the
// process has exited and before it is reaped: the exited process keeps its
// pid, and so the group's id, until then, so the group is still its own.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// watchExit reports the process's exit without reaping it, through the
// platform's notification (watchPID), and gives the wait that reaps it
// after. Without a notification (an old kernel), the exit is learned by
// reaping, and the group is killed right after: the one moment its id is
// not held.
func watchExit(cmd *exec.Cmd) (<-chan struct{}, func() error) {
	exited := make(chan struct{})
	if stop, ok := watchPID(cmd.Process.Pid, func() { close(exited) }); ok {
		return exited, func() error {
			err := cmd.Wait()
			stop()
			return err
		}
	}
	var err error
	go func() {
		err = cmd.Wait()
		close(exited)
	}()
	return exited, func() error { <-exited; return err }
}
