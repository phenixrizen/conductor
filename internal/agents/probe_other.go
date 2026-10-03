//go:build !unix

package agents

import "os/exec"

func probeAttr(*exec.Cmd) {}
