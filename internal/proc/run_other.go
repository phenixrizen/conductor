//go:build !unix

package proc

import (
	"errors"
	"os"
	"os/exec"
)

type lifecycle struct{}

func (*lifecycle) signal(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}

func (*lifecycle) reap(cmd *exec.Cmd) error { return cmd.Wait() }

func attr(cmd *exec.Cmd, g *lifecycle) { cmd.Cancel = func() error { return g.signal(cmd) } }

func group(cmd *exec.Cmd) int { return cmd.Process.Pid }

func exitStatus(state *os.ProcessState) (int, string) { return state.ExitCode(), "" }

var watchFails = false

func watchExit(*exec.Cmd, *lifecycle) (<-chan error, func() error, error) {
	return nil, nil, errors.New("proc: no exit watch on this platform")
}
