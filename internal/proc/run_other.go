//go:build !unix

package proc

import (
	"os"
	"os/exec"
)

func attr(*exec.Cmd) {}

func group(cmd *exec.Cmd) int { return cmd.Process.Pid }

func exitStatus(state *os.ProcessState) (int, string) { return state.ExitCode(), "" }

func killGroup(*exec.Cmd) {}

func watchExit(cmd *exec.Cmd) (<-chan struct{}, func() error) {
	exited := make(chan struct{})
	var err error
	go func() {
		err = cmd.Wait()
		close(exited)
	}()
	return exited, func() error { <-exited; return err }
}
