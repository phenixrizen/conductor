package pty

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// output reads p until it exits and returns what it printed.
func output(t *testing.T, p *Process) string {
	t.Helper()
	var got []byte
	buf := make([]byte, 256)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n, err := p.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	<-p.Done()
	return string(bytes.TrimSpace(got))
}

// A process given the directory its caller opened starts there: in the
// directory opened even after its path came to name another one (on
// Linux, by the open directory itself), or, where a child changes
// directory by path, not at all; with its path unchanged it starts there
// as before.
func TestStartInTheDirectoryOpened(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir, other := filepath.Join(base, "dir"), filepath.Join(base, "other")
	os.Mkdir(dir, 0o755)
	os.Mkdir(other, 0o755)
	spec := func(f *os.File) Spec {
		return Spec{Argv: []string{"/bin/sh", "-c", "pwd -P"}, Dir: dir, DirFile: f, Env: BuildEnv(os.Environ(), nil, nil, nil)}
	}
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := Start(spec(f))
	if err != nil {
		t.Fatal(err)
	}
	if got := output(t, p); got != dir {
		t.Fatalf("started in %q, want %q", got, dir)
	}
	// The path now names another directory.
	os.Rename(dir, dir+".was")
	os.Symlink(other, dir)
	p, err = Start(spec(f))
	if runtime.GOOS != "linux" {
		if err == nil {
			t.Fatalf("started in %q once the path named another directory", output(t, p))
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := output(t, p); got != dir+".was" {
		t.Fatalf("started in %q, want the directory opened (%s.was)", got, dir)
	}
}

// Without an environment of the caller's, PWD is the directory's path, as
// exec sets it, not the descriptor's.
func TestStartInTheDirectoryOpenedKeepsPWD(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// env prints PWD as it was given (a shell would correct it).
	p, err := Start(Spec{Argv: []string{"/usr/bin/env"}, Dir: dir, DirFile: f})
	if err != nil {
		t.Fatal(err)
	}
	var pwd []string
	for _, line := range strings.Split(output(t, p), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "PWD="); ok {
			pwd = append(pwd, v)
		}
	}
	if len(pwd) != 1 || pwd[0] != dir {
		t.Fatalf("PWD %q, want %q", pwd, dir)
	}
}

// Where a child changes directory by path, the path must still name the
// directory opened.
func TestCheckedDirRefusesAnotherDirectory(t *testing.T) {
	dir := t.TempDir()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := checkedDir(dir, fi); err != nil || got != dir {
		t.Fatalf("the same directory: %q %v", got, err)
	}
	other, _ := os.Stat(t.TempDir())
	if _, err := checkedDir(dir, other); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("another directory: %v", err)
	}
	if _, err := checkedDir(filepath.Join(dir, "gone"), fi); err == nil {
		t.Fatal("a path that names nothing was taken")
	}
}
