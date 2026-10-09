package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/gitcli"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := gitcli.Run(context.Background(), dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q")
	run("config", "user.name", "t")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("one\ntwo\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "docs", "old.md"), []byte("old\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

// A status reply names the branch, the base and each change with its lines;
// a show reply is the file at the revision, a deleted file's too; outside a
// repository the status is not_repo; the deny list holds for both.
func TestGitStatusAndShowThroughTheFilePolicy(t *testing.T) {
	dir := gitRepo(t)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("one\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package x\n"), 0o644)
	os.Remove(filepath.Join(dir, "docs", "old.md"))
	h := GitStatusPath(dir, "", nil)
	if h.Kind != "status" || h.Branch == "" || h.Base == "" || len(h.Changes) != 3 || h.Added != 2 || h.Removed != 2 {
		t.Fatalf("status %+v", h)
	}
	byPath := map[string]string{}
	for _, c := range h.Changes {
		byPath[c.Path] = c.Status
	}
	if byPath["README.md"] != "M" || byPath["new.go"] != "?" || byPath["docs/old.md"] != "D" {
		t.Fatalf("changes %v", byPath)
	}
	sh, body := GitShowPath(dir, "HEAD", "README.md", nil)
	if sh.Kind != "show" || sh.Rev != "HEAD" || string(body) != "one\ntwo\n" || sh.Binary {
		t.Fatalf("show %+v %q", sh, body)
	}
	if sh, body := GitShowPath(dir, "HEAD", "docs/old.md", nil); sh.Kind != "show" || string(body) != "old\n" {
		t.Fatalf("a deleted file at HEAD: %+v %q", sh, body)
	}
	if sh, _ := GitShowPath(dir, "HEAD", "nope.md", nil); sh.Kind != "error" || sh.Error.Code != "git" {
		t.Fatalf("a path the revision lacks: %+v", sh)
	}
	if sh, _ := GitShowPath(dir, "HEAD", "../outside", nil); sh.Kind != "error" || sh.Error.Code != "denied" {
		t.Fatalf("outside the root: %+v", sh)
	}
	if h := GitStatusPath(t.TempDir(), "", nil); h.Kind != "error" || h.Error.Code != "not_repo" {
		t.Fatalf("outside a repository: %+v", h)
	}
	if h := GitStatusPath(dir, "", []string{dir}); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("a denied root: %+v", h)
	}
	if sh, _ := GitShowPath(dir, "HEAD", "docs/old.md", []string{filepath.Join(dir, "docs")}); sh.Kind != "error" || sh.Error.Code != "denied" {
		t.Fatalf("a denied folder at a revision: %+v", sh)
	}
}

// The log since the session started and a commit's files, through the file
// policy (design 4e): a commit made after the start lists, one before does
// not; a commit reply carries its files with their lines; outside a
// repository the log is not_repo; a root in the deny list is refused; a
// revision that reads as a flag is refused.
func TestGitLogAndCommitThroughTheFilePolicy(t *testing.T) {
	dir := gitRepo(t)
	time.Sleep(1100 * time.Millisecond) // the init commit's second is past
	start := time.Now()
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("one\ntwo\nthree\n"), 0o644)
	if _, err := gitcli.Run(context.Background(), dir, "commit", "-q", "-am", "readme: a third line"); err != nil {
		t.Fatal(err)
	}
	h := GitLogPath(dir, "", start, nil)
	if h.Kind != "log" || h.Branch == "" || h.Since == "" || len(h.Commits) != 1 || h.Commits[0].Subject != "readme: a third line" {
		t.Fatalf("log %+v", h)
	}
	if later := GitLogPath(dir, "", time.Now().Add(time.Hour), nil); len(later.Commits) != 0 {
		t.Fatalf("nothing after a later start: %+v", later.Commits)
	}
	c := GitCommitPath(dir, h.Commits[0].Sha, nil)
	if c.Kind != "commit" || c.Commit == nil || c.Commit.Subject != "readme: a third line" || len(c.Changes) != 1 || c.Changes[0].Path != "README.md" || c.Changes[0].Added != 1 || c.Added != 1 {
		t.Fatalf("commit %+v", c)
	}
	if h := GitLogPath(t.TempDir(), "", start, nil); h.Kind != "error" || h.Error.Code != "not_repo" {
		t.Fatalf("outside a repository: %+v", h)
	}
	if h := GitLogPath(dir, "", start, []string{dir}); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("a denied root: %+v", h)
	}
	if h := GitCommitPath(dir, "--all", nil); h.Kind != "error" || h.Error.Code != "bad_request" {
		t.Fatalf("a flag as a revision: %+v", h)
	}
}
