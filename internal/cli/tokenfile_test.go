package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/phenixrizen/conductor/internal/store"
)

// A stale token file is removed under the lock, which is made when it is
// missing; while another holder has the lock, the file stays.
func TestDropStaleWorkbenchTokenTakesTheLock(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), workbenchTokenFile)
	lockPath := filepath.Join(st.Dir(), workbenchTokenLock)

	// Nothing to remove: no lock file is made.
	if err := dropStaleWorkbenchToken(st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(lockPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a lock file with no token file: %v", err)
	}

	// A stale file and no lock file: the lock is made and taken, the file goes.
	if err := os.WriteFile(path, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dropStaleWorkbenchToken(st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the stale file stayed: %v", err)
	}
	fi, err := os.Lstat(lockPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("lock file %v %v", fi, err)
	}

	// A holder of the lock (a running server) keeps its file.
	held, err := lockTokenFile(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("live\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dropStaleWorkbenchToken(st); !errors.Is(err, errTokenFileHeld) {
		t.Fatalf("drop under a held lock: %v", err)
	}
	if _, err := keepWorkbenchToken(st, "another"); !errors.Is(err, errTokenFileHeld) {
		t.Fatalf("keep under a held lock: %v", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "live\n" {
		t.Fatalf("the live file changed: %q %v", b, err)
	}
	held.Close()
	if err := dropStaleWorkbenchToken(st); err != nil {
		t.Fatalf("drop once the holder is gone: %v", err)
	}
}

// The lock file is never opened through a symbolic link.
func TestLockTokenFileRefusesALink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, workbenchTokenLock)); err != nil {
		t.Fatal(err)
	}
	if f, err := lockTokenFile(dir); err == nil {
		f.Close()
		t.Fatal("the lock file was opened through a link")
	}
}
