package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
