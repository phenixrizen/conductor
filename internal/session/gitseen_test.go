package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The comparison: a file new to the list is written (untracked) or edited
// (tracked), one gone is deleted, one unchanged is nothing, one a file
// entry named is skipped; one that left the list is deleted when gone,
// edited when its time moved, nothing when only committed.
func TestGitSeenDiff(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	kept := filepath.Join(dir, "kept.go")
	reverted := filepath.Join(dir, "reverted.go")
	os.WriteFile(kept, []byte("x"), 0o644)
	os.WriteFile(reverted, []byte("x"), 0o644)
	os.Chtimes(kept, at, at)
	os.Chtimes(reverted, at.Add(time.Minute), at.Add(time.Minute))
	base := map[string]fileSig{
		"/r/same.go":    {status: "M", size: 3, mod: at, exists: true},
		"/r/grew.go":    {status: "M", size: 3, mod: at, exists: true},
		"/r/hooked.go":  {status: "M", size: 3, mod: at, exists: true},
		kept:            {status: "M", size: 1, mod: at, exists: true}, // committed: left the list, same time
		reverted:        {status: "M", size: 1, mod: at, exists: true}, // checked out again: left the list, new time
		"/r/vanished.c": {status: "?", size: 1, mod: at, exists: true}, // removed: left the list, gone
		"/r/gone.go":    {status: "D", exists: false},                  // still deleted
	}
	snap := map[string]fileSig{
		"/r/same.go":   {status: "M", size: 3, mod: at, exists: true},
		"/r/grew.go":   {status: "M", size: 9, mod: at.Add(time.Second), exists: true},
		"/r/hooked.go": {status: "M", size: 9, mod: at.Add(time.Second), exists: true},
		"/r/new.txt":   {status: "?", size: 1, mod: at, exists: true},
		"/r/staged.go": {status: "A", size: 1, mod: at, exists: true},
		"/r/main.go":   {status: "M", size: 1, mod: at, exists: true},
		"/r/old.md":    {status: "D", exists: false},
		"/r/gone.go":   {status: "D", exists: false},
	}
	got := gitSeenDiff(base, snap, map[string]bool{"/r/hooked.go": true})
	want := map[string]string{
		"/r/grew.go": FileOpEdit, "/r/new.txt": FileOpWrite, "/r/staged.go": FileOpWrite, "/r/main.go": FileOpEdit,
		"/r/old.md": FileOpDelete, reverted: FileOpEdit, "/r/vanished.c": FileOpDelete,
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, m := range got {
		if want[m.path] != m.op {
			t.Fatalf("%s: %s, want %q (all %+v)", m.path, m.op, want[m.path], got)
		}
		if i > 0 && got[i-1].path > m.path {
			t.Fatalf("not sorted: %+v", got)
		}
	}
}

// In a real repository: what a shell writes after a tool call shows as a
// file entry with the tool git, attributed to the agent; a file a hook named
// is not added again; changes made before the session are not reported;
// outside a work tree nothing is watched.
func TestGitSeenRecordsWhatNoHookNamed(t *testing.T) {
	dir := gitRepo(t)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("dirty before the session\n"), 0o644)
	s := NewLocal(Info{ID: "g", Cwd: dir, Cols: 80, Rows: 24}, newFakeProc(), Options{WatchGit: true})
	waitBaseline(t, s)
	os.WriteFile(filepath.Join(dir, "gen.txt"), []byte("generated\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "docs", "old.md"), []byte("old\nmore\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "hooked.go"), []byte("package x\n"), 0o644)
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: "hooked.go", Tool: "Write", ByName: "agent"})
	s.Record(ActivityEntry{Type: ActivityToolUse, Tool: "Bash"})
	files := waitGitSeen(t, s, 2)
	if files["gen.txt"] != FileOpWrite || files[filepath.Join("docs", "old.md")] != FileOpEdit {
		t.Fatalf("seen %v", files)
	}
	for _, e := range s.Activity() {
		if e.Tool == GitSeenTool && (e.Path == "hooked.go" || e.Path == "README.md" || e.ByName != "agent") {
			t.Fatalf("unexpected %+v", e)
		}
	}
	// The next look reports only what moved since this one.
	os.Remove(filepath.Join(dir, "gen.txt"))
	s.Record(ActivityEntry{Type: ActivityToolUse, Tool: "Bash"})
	if files := waitGitSeen(t, s, 3); files["gen.txt"] != FileOpDelete {
		t.Fatalf("after the removal %v", files)
	}

	plain := NewLocal(Info{ID: "p", Cwd: t.TempDir(), Cols: 80, Rows: 24}, newFakeProc(), Options{WatchGit: true})
	deadline := time.Now().Add(5 * time.Second)
	for {
		plain.gitSeen.mu.Lock()
		off := plain.gitSeen.off
		plain.gitSeen.mu.Unlock()
		if off {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a directory outside a work tree is still watched")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitBaseline(t *testing.T, s *Local) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.gitSeen.mu.Lock()
		ok := s.gitSeen.base != nil
		s.gitSeen.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no baseline")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitGitSeen waits until n entries git saw are in the activity and returns
// the latest op of each path.
func waitGitSeen(t *testing.T, s *Local, n int) map[string]string {
	t.Helper()
	deadline := time.Now().Add(gitSeenDelay + 5*time.Second)
	for {
		files := map[string]string{}
		count := 0
		for _, e := range s.Activity() {
			if e.Type == ActivityFile && e.Tool == GitSeenTool {
				files[e.Path] = e.Op
				count++
			}
		}
		if count >= n {
			return files
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d entries git saw, want %d: %v", count, n, files)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
