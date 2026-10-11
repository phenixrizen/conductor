//go:build !linux

package pty

// startDir is the directory the child changes into before it runs the
// command: Dir, checked against Spec.DirFile when it is set (a child
// changes directory by path here).
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
