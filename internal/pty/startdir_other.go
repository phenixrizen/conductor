//go:build !linux

package pty

// startDir is the directory the child changes into before it runs the
// command: Dir, checked against Spec.DirFile when it is set. A child
// changes directory by path here (Go has no descriptor-based change for a
// new process on these systems), so a change to Dir's path between this
// check and the child's own change of directory is not seen; the session's
// file reads and saves do not depend on it, as they go through the folder
// the caller opened.
func startDir(spec Spec) (string, error) {
	if spec.DirFile == nil {
		return spec.Dir, nil
	}
	want, err := spec.DirFile.Stat()
	if err != nil {
		return "", err
	}
	return checkedDir(spec.Dir, want)
}
