package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
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

// failingWriter refuses every write, as a full disk or a closed pipe does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// A server whose generated token could go only into the --print-listen line
// (another server keeps the token file) stops when that line cannot be
// written, and leaves the other server's file as it is.
func TestServeStopsWhenItsTokensOnlyHandshakeFails(t *testing.T) {
	clearConductorEnv(t)
	t.Cleanup(agents.ForgetBinary())
	dir := t.TempDir()
	data := filepath.Join(dir, "state")
	path := filepath.Join(data, workbenchTokenFile)
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := lockTokenFile(data)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := os.WriteFile(path, []byte("live\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := writeServeConfig(t, dir, fmt.Sprintf(`{"allowedRoots": [%q], "defaultCwd": %q, "dataDir": %q}`, dir, dir, data))
	var logs syncBuffer
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := runServe(context.Background(), []string{"--listen", "127.0.0.1:0", "--config", cfg, "--print-listen"}, strings.NewReader(""), failingWriter{}, &logs)
		done <- result{code, err}
	}()
	select {
	case r := <-done:
		if r.code != 1 || r.err == nil || !strings.Contains(r.err.Error(), "print-listen") {
			t.Fatalf("serve: %d %v\n%s", r.code, r.err, logs.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("serve went on with its token nowhere:\n%s", logs.String())
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "live\n" {
		t.Fatalf("the other server's file changed: %q %v", b, err)
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
