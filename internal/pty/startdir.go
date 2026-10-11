package pty

import (
	"errors"
	"os"
)

// errDirChanged refuses a start whose directory's path no longer names the
// directory its caller opened (Spec.DirFile).
var errDirChanged = errors.New("the working directory changed since it was opened")

// checkedDir is dir once it is checked to name want, the directory a
// caller opened.
func checkedDir(dir string, want os.FileInfo) (string, error) {
	got, err := os.Stat(dir)
	if err != nil || !os.SameFile(got, want) {
		return "", errDirChanged
	}
	return dir, nil
}
