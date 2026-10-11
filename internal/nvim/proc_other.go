//go:build !unix

package nvim

import "os/exec"

// ownGroup leaves the child as exec starts it here: its end ends the
// process alone.
func ownGroup(*exec.Cmd) {}

// groupLeft has no group to look at here.
func groupLeft(*exec.Cmd) bool { return true }

// endGroup has no group to end here.
func endGroup(*exec.Cmd) {}
