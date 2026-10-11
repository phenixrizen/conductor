//go:build linux

package pty

import (
	"os"
	"strconv"
)

// startDir is the directory the child changes into before it runs the
// command: with Spec.DirFile, the open directory itself through its
// descriptor in /proc/self/fd (the child holds the same descriptors until
// it runs the command, and changes directory before that), so nothing
// done to Dir's path in between moves it; without /proc, Dir checked
// against DirFile.
func startDir(spec Spec) (string, error) {
	if spec.DirFile == nil {
		return spec.Dir, nil
	}
	want, err := spec.DirFile.Stat()
	if err != nil {
		return "", err
	}
	byHandle := "/proc/self/fd/" + strconv.Itoa(int(spec.DirFile.Fd()))
	if fi, err := os.Stat(byHandle); err == nil && os.SameFile(fi, want) {
		return byHandle, nil
	}
	return checkedDir(spec.Dir, want)
}
