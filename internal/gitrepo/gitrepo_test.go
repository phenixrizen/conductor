package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitIn runs the git binary in dir for the test's setup (the package itself
// reads with go-git), with a fixed committer time when at is set.
func gitIn(t *testing.T, dir string, at time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C")
	if !at.IsZero() {
		stamp := at.Format(time.RFC3339)
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func scratch(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed (the test builds its repository with it)")
	}
	dir := t.TempDir()
	gitIn(t, dir, time.Time{}, "init", "-q", "-b", "main")
	gitIn(t, dir, time.Time{}, "config", "user.name", "Ada")
	gitIn(t, dir, time.Time{}, "config", "user.email", "ada@example.invalid")
	gitIn(t, dir, time.Time{}, "config", "commit.gpgsign", "false")
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir string, at time.Time, msg string) string {
	t.Helper()
	gitIn(t, dir, at, "add", "-A")
	gitIn(t, dir, at, "commit", "-q", "-m", msg)
	return gitIn(t, dir, time.Time{}, "rev-parse", "HEAD")
}

// The log lists the commits since a time, newest first, with their subject,
// author and time; before the first commit it is empty; outside a
// repository it says so.
func TestCommitsSinceATime(t *testing.T) {
	dir := scratch(t)
	if log, err := Commits(context.Background(), dir, "", time.Time{}); err != nil || len(log.Commits) != 0 || log.Branch != "main" {
		t.Fatalf("an empty repository: %+v %v", log, err)
	}
	start := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	write(t, dir, "README.md", "# repo\n")
	commit(t, dir, start.Add(-time.Hour), "before the session")
	write(t, dir, "a.go", "package a\n")
	commit(t, dir, start.Add(time.Minute), "users: the handler\n\nThe body says why.")
	write(t, dir, "b.go", "package b\n")
	last := commit(t, dir, start.Add(2*time.Minute), "users: tests")
	log, err := Commits(context.Background(), dir, "", start)
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Commits) != 2 || log.Commits[0].Sha != last || log.Commits[0].Subject != "users: tests" || log.Commits[1].Subject != "users: the handler" {
		t.Fatalf("log: %+v", log.Commits)
	}
	c := log.Commits[0]
	if c.Short != last[:7] || c.Author != "Ada" || !c.At.Equal(start.Add(2*time.Minute)) || c.Parent == "" || c.Body != "" {
		t.Fatalf("a commit: %+v", c)
	}
	if real, _ := filepath.EvalSymlinks(dir); log.Top != dir && log.Top != real {
		t.Fatalf("top %q, want %q", log.Top, dir)
	}
	if _, err := Commits(context.Background(), t.TempDir(), "", start); err != ErrNotRepo {
		t.Fatalf("outside a repository: %v", err)
	}
}

// With a base the log is what HEAD has past their merge base, whatever the
// times; a linked worktree (a crew member's) opens through its common
// directory and lists its own branch's commits.
func TestCommitsAfterABaseInALinkedWorktree(t *testing.T) {
	dir := scratch(t)
	t0 := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	write(t, dir, "README.md", "# repo\n")
	base := commit(t, dir, t0, "base")
	wt := filepath.Join(t.TempDir(), "member")
	gitIn(t, dir, time.Time{}, "worktree", "add", "-q", "-b", "crew/core", wt, "main")
	write(t, wt, "core.go", "package core\n")
	commit(t, wt, t0.Add(-48*time.Hour), "core: an old-dated commit still counts")
	write(t, dir, "main-only.txt", "x\n")
	commit(t, dir, t0.Add(time.Hour), "main moves on")
	log, err := Commits(context.Background(), wt, "main", t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if log.Branch != "crew/core" || len(log.Commits) != 1 || log.Commits[0].Subject != "core: an old-dated commit still counts" || log.Base == "" {
		t.Fatalf("worktree log: %+v", log)
	}
	if log2, _ := Commits(context.Background(), wt, base, time.Time{}); len(log2.Commits) != 1 {
		t.Fatalf("by sha: %+v", log2.Commits)
	}
}

// A commit's files against its first parent: added, modified with its
// lines, deleted, renamed with its old path, binary without lines; the
// totals; the body; a root commit lists every file as added; an unknown
// revision is an error.
func TestCommitDetail(t *testing.T) {
	dir := scratch(t)
	t0 := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	write(t, dir, "users.go", "package api\n\nfunc A() {}\n")
	write(t, dir, "old.txt", "gone\n")
	write(t, dir, "notes/long-name-that-moves.md", strings.Repeat("a line that stays the same\n", 20))
	root := commit(t, dir, t0, "init")
	if d, err := CommitDetail(context.Background(), dir, root); err != nil || len(d.Changes) != 3 || d.Commit.Parent != "" || d.Changes[0].Status != "A" {
		t.Fatalf("a root commit: %+v %v", d, err)
	}
	write(t, dir, "users.go", "package api\n\nfunc A() {}\nfunc B() {}\nfunc C() {}\n")
	write(t, dir, "new.go", "package api\n// one\n// two\n")
	os.Remove(filepath.Join(dir, "old.txt"))
	gitIn(t, dir, time.Time{}, "mv", "notes/long-name-that-moves.md", "notes/moved.md")
	write(t, dir, "pic.bin", string([]byte{0, 1, 2, 3, 0, 5}))
	sha := commit(t, dir, t0.Add(time.Minute), "users: B and C\n\nTwo more handlers.")
	d, err := CommitDetail(context.Background(), dir, sha[:9])
	if err != nil {
		t.Fatal(err)
	}
	if d.Commit.Sha != sha || d.Commit.Subject != "users: B and C" || d.Commit.Body != "Two more handlers." || d.Commit.Parent != root {
		t.Fatalf("the commit: %+v", d.Commit)
	}
	got := map[string]Change{}
	for _, c := range d.Changes {
		got[c.Path] = c
	}
	if c := got["users.go"]; c.Status != "M" || c.Added != 2 || c.Removed != 0 {
		t.Fatalf("modified: %+v", c)
	}
	if c := got["new.go"]; c.Status != "A" || c.Added != 3 {
		t.Fatalf("added: %+v", c)
	}
	if c := got["old.txt"]; c.Status != "D" || c.Removed != 1 {
		t.Fatalf("deleted: %+v", c)
	}
	if c := got["notes/moved.md"]; c.Status != "R" || c.From != "notes/long-name-that-moves.md" {
		t.Fatalf("renamed: %+v (all %+v)", c, d.Changes)
	}
	if c := got["pic.bin"]; c.Status != "A" || !c.Binary {
		t.Fatalf("binary: %+v", c)
	}
	if d.Added != 5 || d.Removed != 1 {
		t.Fatalf("totals +%d -%d", d.Added, d.Removed)
	}
	if _, err := CommitDetail(context.Background(), dir, "nope"); err == nil {
		t.Fatal("an unknown revision")
	}
}
