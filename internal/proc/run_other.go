//go:build !unix

package proc

import (
	"os"
	"os/exec"
)

func attr(*exec.Cmd) {}

func group(cmd *exec.Cmd) int { return cmd.Process.Pid }

func exitStatus(state *os.ProcessState) (int, string) { return state.ExitCode(), "" }

func endGroup(*exec.Cmd) bool { return false }
