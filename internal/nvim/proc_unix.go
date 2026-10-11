//go:build unix

package nvim

import (
	"errors"
	"os/exec"
	"syscall"
)

// ownGroup starts the child in a process group of its own and makes its
// end (its context's, Editor's kill) end the group whole: a `nvim` on PATH
// that runs Neovim as a child of its own (an AppImage's runtime, a script
// without exec) is ended with what it started, which would otherwise hold
// the editor's pipes.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// groupLeft reports whether nothing is left of the child's process group.
func groupLeft(cmd *exec.Cmd) bool {
	return errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH)
}

// endGroup ends what is left of the child's process group once the child
// itself has been reaped: a wrapper that left Neovim running as it exited
// leaves it in the group, whose id is not reused while a member lives.
func endGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
