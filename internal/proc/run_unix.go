//go:build unix

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

// attr starts the command in a process group of its own and kills the whole
// group when its context ends (the timeout, or the caller's), so a test that
// spawns helpers does not outlive the run.
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

// endGroup kills what is left of the command's process group after the
// wait and reports whether there was anything: a child the command left
// behind, holding its pipe or not.
func endGroup(cmd *exec.Cmd) bool {
	if cmd.Process == nil || syscall.Kill(-cmd.Process.Pid, 0) != nil {
		return false
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return true
}
