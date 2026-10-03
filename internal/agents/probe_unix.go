//go:build unix

package agents

import (
	"os/exec"
	"syscall"
)

// probeAttr starts the probe in a process group of its own and kills the
// whole group when its context ends, so a CLI that spawns helpers (a node
// wrapper, a daemon it starts) does not outlive the probe.
func probeAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
