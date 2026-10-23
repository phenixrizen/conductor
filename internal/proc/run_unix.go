//go:build unix

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

// attr starts the command in a process group of its own and kills the whole
// group when its context ends (the timeout, or the caller's), so a test that
// spawns helpers does not outlive the run. The kill comes before the
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

// killGroup kills the command's process group: what the process left
// behind holding its output pipe. Run calls it only then: a holder of the
// pipe descends from the process, so the group is still theirs, and the id
// of a group with a member cannot be given to another.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
