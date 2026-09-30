package crew

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo makes a git repository with one commit holding README (three
// lines) and returns its path. It skips the test when git is not installed.
// HOME is a temporary directory, so that no configuration of the user running
// the tests (hooks, signing) applies to the test's git or the engine's.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	writeFile(t, filepath.Join(dir, "README"), "one\ntwo\nthree\n")
	commitAll(t, dir, "init")
	return dir
}

// runGit runs git -C dir args and returns its output; a failure fails the test.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitAll(t *testing.T, dir, message string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", message)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// AddWorktree makes the branch from HEAD and checks it out at the path, whose
// parent directories it makes; a branch that exists already is an error.
func TestAddWorktreeMakesTheBranchAndThePath(t *testing.T) {
	repo := newRepo(t)
	path := filepath.Join(repo, ".conductor", "worktrees", "api-sweep-1a2b3c4d", "lead")
	if err := AddWorktree(t.Context(), repo, path, "crew/api-sweep-1a2b3c4d/lead"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(path, "README")); err != nil || string(b) != "one\ntwo\nthree\n" {
		t.Fatalf("README in the worktree: %q %v", b, err)
	}
	if branch := runGit(t, path, "rev-parse", "--abbrev-ref", "HEAD"); branch != "crew/api-sweep-1a2b3c4d/lead" {
		t.Fatalf("the worktree is on %q", branch)
	}
	if head, fork := runGit(t, repo, "rev-parse", "HEAD"), runGit(t, path, "rev-parse", "HEAD"); head != fork {
		t.Fatalf("the branch starts at %s, HEAD is %s", fork, head)
	}
	if list := runGit(t, repo, "worktree", "list"); !strings.Contains(list, path) {
		t.Fatalf("git worktree list:\n%s", list)
	}
	// The branch exists now: a second worktree cannot take its name.
	err := AddWorktree(t.Context(), repo, filepath.Join(repo, ".conductor", "worktrees", "other", "lead"), "crew/api-sweep-1a2b3c4d/lead")
	if err == nil || errors.Is(err, ErrNotRepo) || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("the same branch twice: %v", err)
	}
}

// A directory without a .git is no repository to make worktrees of, and
// neither is a repository without a commit to branch from. Nothing is made.
func TestAddWorktreeNeedsARepository(t *testing.T) {
	newRepo(t) // skips without git
	plain := t.TempDir()
	path := filepath.Join(plain, ".conductor", "worktrees", "run", "lead")
	if err := AddWorktree(t.Context(), plain, path, "crew/run/lead"); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("AddWorktree in a plain directory: %v", err)
	}
	if err := CheckRepo(t.Context(), plain); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("CheckRepo of a plain directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plain, ".conductor")); !os.IsNotExist(err) {
		t.Fatalf("something was made: %v", err)
	}

	empty := t.TempDir()
	runGit(t, empty, "init", "-q")
	if err := CheckRepo(t.Context(), empty); !errors.Is(err, ErrNotRepo) || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("CheckRepo of a repository without a commit: %v", err)
	}
	repo := newRepo(t)
	if err := CheckRepo(t.Context(), repo); err != nil {
		t.Fatalf("CheckRepo of a repository: %v", err)
	}
	// A directory inside a repository is one too, and worktrees of the
	// repository are made from it.
	sub := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckRepo(t.Context(), sub); err != nil {
		t.Fatalf("CheckRepo of a directory in a repository: %v", err)
	}
	subPath := filepath.Join(sub, ".conductor", "worktrees", "run", "lead")
	if err := AddWorktree(t.Context(), sub, subPath, "crew/run/lead"); err != nil {
		t.Fatalf("AddWorktree from a directory in a repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(subPath, "README")); err != nil {
		t.Fatalf("the worktree holds the whole repository: %v", err)
	}
}

// DiffStat counts the lines the worktree adds and removes since base, the
// commit it started from: commits on the branch and uncommitted changes to
// tracked files count; untracked files and commits made on the main branch
// since do not.
func TestDiffStatCountsTheBranchChanges(t *testing.T) {
	repo := newRepo(t)
	path := filepath.Join(repo, ".conductor", "worktrees", "run", "core")
	if err := AddWorktree(t.Context(), repo, path, "crew/run/core"); err != nil {
		t.Fatal(err)
	}
	base, err := headCommit(t.Context(), path)
	if err != nil || base != runGit(t, repo, "rev-parse", "HEAD") {
		t.Fatalf("headCommit: %q %v", base, err)
	}
	if added, removed, err := DiffStat(t.Context(), path, base); err != nil || added != 0 || removed != 0 {
		t.Fatalf("before any commit: +%d -%d %v", added, removed, err)
	}
	// One line changed and two added in README, three lines in a new file.
	writeFile(t, filepath.Join(path, "README"), "one\n2\nthree\nfour\nfive\n")
	writeFile(t, filepath.Join(path, "users.go"), "a\nb\nc\n")
	commitAll(t, path, "users")
	// A commit on the main branch is not the member's work.
	writeFile(t, filepath.Join(repo, "MAIN"), "x\ny\n")
	commitAll(t, repo, "main moves on")
	if added, removed, err := DiffStat(t.Context(), path, base); err != nil || added != 6 || removed != 1 {
		t.Fatalf("after a commit on the branch: +%d -%d %v", added, removed, err)
	}
	// Work not committed yet counts; a file git does not track does not.
	writeFile(t, filepath.Join(path, "users.go"), "a\nb\n")
	writeFile(t, filepath.Join(path, "scratch.txt"), "1\n2\n3\n4\n")
	if added, removed, err := DiffStat(t.Context(), path, base); err != nil || added != 5 || removed != 1 {
		t.Fatalf("with an uncommitted edit and an untracked file: +%d -%d %v", added, removed, err)
	}
	if _, _, err := DiffStat(t.Context(), path, "-p"); err == nil {
		t.Fatal("a base that reads as an option was run")
	}
}

// A file in the worktree named like the base commit does not make git read
// the base as a path.
func TestDiffStatWithAFileNamedLikeTheBase(t *testing.T) {
	repo := newRepo(t)
	path := filepath.Join(repo, ".conductor", "worktrees", "run", "core")
	if err := AddWorktree(t.Context(), repo, path, "crew/run/core"); err != nil {
		t.Fatal(err)
	}
	base, err := headCommit(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(path, base), "x\n")
	writeFile(t, filepath.Join(path, "README"), "one\ntwo\nthree\nfour\n")
	if added, removed, err := DiffStat(t.Context(), path, base); err != nil || added != 1 || removed != 0 {
		t.Fatalf("with a file named %s: +%d -%d %v", base, added, removed, err)
	}
}

func TestParseShortstat(t *testing.T) {
	for _, tc := range []struct {
		in             string
		added, removed int
		wantErr        bool
	}{
		{"", 0, 0, false},
		{"\n", 0, 0, false},
		{" 1 file changed, 1 insertion(+)\n", 1, 0, false},
		{" 2 files changed, 3 deletions(-)\n", 0, 3, false},
		{" 3 files changed, 10 insertions(+), 2 deletions(-)\n", 10, 2, false},
		{" 1 file changed, 1 insertion(+), 1 deletion(-)\n", 1, 1, false},
		{"fatal: something\n", 0, 0, true},
	} {
		added, removed, err := parseShortstat(tc.in)
		if added != tc.added || removed != tc.removed || (err != nil) != tc.wantErr {
			t.Errorf("parseShortstat(%q) = +%d -%d %v", tc.in, added, removed, err)
		}
	}
}

// excludeWorktrees adds an unanchored .conductor/ to the file git names for
// info/exclude, making info/ when it is missing, once, after what is there,
// from the top of the repository or a directory in it.
func TestExcludeWorktreesAddsTheLineOnce(t *testing.T) {
	repo := newRepo(t)
	if err := os.RemoveAll(filepath.Join(repo, ".git", "info")); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	for _, dir := range []string{repo, sub, repo} {
		if err := excludeWorktrees(t.Context(), dir); err != nil {
			t.Fatalf("from %s: %v", dir, err)
		}
	}
	if b, err := os.ReadFile(exclude); err != nil || string(b) != ".conductor/\n" {
		t.Fatalf("info/exclude %q %v", b, err)
	}
	// A file without a final line break keeps its last line whole.
	writeFile(t, exclude, "*.log")
	if err := excludeWorktrees(t.Context(), sub); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exclude); string(b) != "*.log\n.conductor/\n" {
		t.Fatalf("info/exclude %q", b)
	}
	for _, dir := range []string{filepath.Join(repo, ".conductor", "worktrees", "run", "lead"), filepath.Join(sub, ".conductor", "x")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "file"), "x\n")
	}
	if status := runGit(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("git status:\n%s", status)
	}
}
