package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// onLaunch runs fn once when a launch reaches stage (launchHook): "resolved"
// once its working directory's path is resolved, "checked" once the folder
// is checked, before the process starts.
func onLaunch(t *testing.T, stage string, fn func()) {
	t.Helper()
	var once sync.Once
	hook := func(s string) {
		if s == stage {
			once.Do(fn)
		}
	}
	launchHookFn.Store(&hook)
	t.Cleanup(func() { launchHookFn.Store(nil) })
}

// launchTree makes proj under the test's allowed root and a folder outside
// the roots, each with a file.txt of its own, and a file the process
// writes its working directory to.
func launchTree(t *testing.T, e *testEnv) (proj, outside, marker string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(e.root)
	if err != nil {
		t.Fatal(err)
	}
	proj = filepath.Join(root, "proj")
	os.MkdirAll(proj, 0o755)
	os.WriteFile(filepath.Join(proj, "file.txt"), []byte("inside\n"), 0o644)
	outside, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(outside, "file.txt"), []byte("outside\n"), 0o644)
	marker = filepath.Join(t.TempDir(), "cwd")
	return proj, outside, marker
}

// launchIn starts sh in cwd writing its working directory (as the kernel
// has it) to marker, then waiting.
func (e *testEnv) launchIn(cwd, marker string) (*http.Response, map[string]any) {
	e.t.Helper()
	resp, out := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": "sh", "cwd": cwd, "args": []string{"-c", "pwd -P > " + marker + "; exec cat"}})
	if resp.StatusCode == http.StatusCreated {
		id, _ := out["id"].(string)
		e.t.Cleanup(func() {
			if d, ok := e.srv.registry.Get(id); ok {
				_ = d.Stop(e.t.Context())
			}
		})
	}
	return resp, out
}

// readMarker waits for the launched process to write its working directory.
func readMarker(t *testing.T, marker string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil && strings.HasSuffix(string(b), "\n") {
			return strings.TrimSpace(string(b))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the process wrote no working directory")
	return ""
}

// A launch's folder is checked as it was opened: a folder whose path
// becomes a link out of the allowed roots once resolved is refused, not
// started in where the link leads.
func TestLaunchRefusesAFolderThatBecameALinkOutOfTheRoots(t *testing.T) {
	e := newTestEnv(t, nil)
	proj, outside, marker := launchTree(t, e)
	onLaunch(t, "resolved", func() {
		os.Rename(proj, proj+".was")
		os.Symlink(outside, proj)
	})
	resp, out := e.launchIn(proj, marker)
	if resp.StatusCode != http.StatusBadRequest || errorCode(out) != "invalid_cwd" {
		t.Fatalf("launch in a folder that became a link out: %d %v", resp.StatusCode, out)
	}
	time.Sleep(100 * time.Millisecond)
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("a process started in %s", b)
	}
}

// Once checked, the folder is what the session uses: its process starts in
// it (on Linux, by the handle the check opened; elsewhere a path that no
// longer names it refuses the launch) and its files are read from it, not
// from where its path leads once changed.
func TestLaunchUsesTheFolderItChecked(t *testing.T) {
	e := newTestEnv(t, nil)
	proj, outside, marker := launchTree(t, e)
	onLaunch(t, "checked", func() {
		os.Rename(proj, proj+".was")
		os.Symlink(outside, proj)
	})
	resp, out := e.launchIn(proj, marker)
	if runtime.GOOS != "linux" {
		if resp.StatusCode == http.StatusCreated {
			t.Fatalf("launch in a folder whose path changed after its check: %d %v", resp.StatusCode, out)
		}
		return
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
	}
	if got := readMarker(t, marker); got != proj+".was" {
		t.Fatalf("the process started in %s, want the folder checked (%s.was)", got, proj)
	}
	id, _ := out["id"].(string)
	status, body, _ := e.getFile(id, adminToken, "file.txt", "")
	if strings.Contains(body, "outside") {
		t.Fatalf("the session's file read went outside the folder: %d %s", status, body)
	}
}
