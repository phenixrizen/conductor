package session

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGitBranchReadsHeadRef(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feat/oauth-device\n")
	if got := GitBranch(repo); got != "feat/oauth-device" {
		t.Fatalf("got %q", got)
	}
	nested := filepath.Join(repo, "internal", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := GitBranch(nested); got != "feat/oauth-device" {
		t.Fatalf("nested: got %q", got)
	}
}

func TestGitBranchDetachedAndMissing(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	if got := GitBranch(repo); got != "" {
		t.Fatalf("detached: got %q", got)
	}
	if got := GitBranch(t.TempDir()); got != "" {
		t.Fatalf("no repo: got %q", got)
	}
	if got := GitBranch(""); got != "" {
		t.Fatalf("empty dir: got %q", got)
	}
}

func TestGitBranchFollowsWorktreeGitFile(t *testing.T) {
	main := t.TempDir()
	writeFile(t, filepath.Join(main, ".git", "worktrees", "wt", "HEAD"), "ref: refs/heads/wt-branch\n")
	wt := t.TempDir()
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
	if got := GitBranch(wt); got != "wt-branch" {
		t.Fatalf("worktree: got %q", got)
	}
}

func TestGitBranchTruncatesAbsurdNames(t *testing.T) {
	repo := t.TempDir()
	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'b'
	}
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/"+string(long)+"\n")
	if got := GitBranch(repo); len(got) != MaxBranchLen {
		t.Fatalf("len %d, want %d", len(got), MaxBranchLen)
	}
}
