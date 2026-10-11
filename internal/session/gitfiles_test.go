package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/gitcli"
	"github.com/phenixrizen/conductor/internal/gitcli/gitclitest"
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
	sh, body := GitShowPath(dir, "HEAD", "README.md", nil, RoleControl)
	if sh.Kind != "show" || sh.Rev != "HEAD" || string(body) != "one\ntwo\n" || sh.Binary {
		t.Fatalf("show %+v %q", sh, body)
	}
	if sh, body := GitShowPath(dir, "HEAD", "docs/old.md", nil, RoleControl); sh.Kind != "show" || string(body) != "old\n" {
		t.Fatalf("a deleted file at HEAD: %+v %q", sh, body)
	}
	if sh, _ := GitShowPath(dir, "HEAD", "nope.md", nil, RoleControl); sh.Kind != "error" || sh.Error.Code != "git" {
		t.Fatalf("a path the revision lacks: %+v", sh)
	}
	if sh, _ := GitShowPath(dir, "HEAD", "../outside", nil, RoleControl); sh.Kind != "error" || sh.Error.Code != "denied" {
		t.Fatalf("outside the root: %+v", sh)
	}
	if h := GitStatusPath(t.TempDir(), "", nil); h.Kind != "error" || h.Error.Code != "not_repo" {
		t.Fatalf("outside a repository: %+v", h)
	}
	if h := GitStatusPath(dir, "", []string{dir}); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("a denied root: %+v", h)
	}
	if sh, _ := GitShowPath(dir, "HEAD", "docs/old.md", []string{filepath.Join(dir, "docs")}, RoleControl); sh.Kind != "error" || sh.Error.Code != "denied" {
		t.Fatalf("a denied folder at a revision: %+v", sh)
	}
}

// What the Files tab and the session read of a repository starts no program
// the repository's own configuration names (gitclitest): the status with
// its lines, a file at a revision, the log and a commit, the branch the
// session shows, the look after an agent's tool call. The results are
// still right.
func TestGitReadsStartNoProgramTheRepositoryNames(t *testing.T) {
	for _, hooksPath := range []bool{false, true} {
		r := gitclitest.New(t, gitclitest.Options{HooksPath: hooksPath})
		h := GitStatusPath(r.Dir, "", nil)
		if h.Kind != "status" || h.Branch != "main" || h.Base == "" || len(h.Changes) != len(gitclitest.Dirty) || h.Added != len(gitclitest.Dirty) || h.Removed != 0 {
			t.Fatalf("hooksPath %v: status %+v", hooksPath, h)
		}
		r.NoneFired(t, "the status")
		for path, body := range gitclitest.Committed {
			if sh, b := GitShowPath(r.Dir, "HEAD", path, nil, RoleControl); sh.Kind != "show" || string(b) != body {
				t.Fatalf("show %s: %+v %q", path, sh, b)
			}
		}
		r.NoneFired(t, "show")
		if lh := GitLogPath(r.Dir, "", time.Time{}, nil); lh.Kind != "log" || len(lh.Commits) != 1 {
			t.Fatalf("log: %+v", lh)
		} else if ch := GitCommitPath(r.Dir, lh.Commits[0].Sha, nil); ch.Kind != "commit" || len(ch.Changes) != len(gitclitest.Committed) {
			t.Fatalf("commit: %+v", ch)
		}
		if b := GitBranch(r.Dir); b != "main" {
			t.Fatalf("branch %q", b)
		}
		if snap, ok := gitSnapshot(context.Background(), r.Dir); !ok || len(snap) != len(gitclitest.Dirty) {
			t.Fatalf("the look after a tool call: %v %v", snap, ok)
		}
		r.NoneFired(t, "the log, the branch and the look")
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

// The status lists the files of the folder's own working tree: the folder,
// one inside it, or the one git finds above it by its .git (a session in a
// repository's subfolder sees the repository's changes). A working tree a
// repository's core.worktree puts elsewhere is not listed, nor looked at
// after a tool call: neither its names nor its files' lines. An untracked
// link's lines are not counted from what it points to.
func TestGitStatusListsOnlyTheFoldersOwnWorkingTree(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	root := filepath.Join(base, "root")
	for _, d := range []string{outside, filepath.Join(root, "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret-name.txt"), []byte("1\n2\n3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "inside.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		if _, err := gitcli.Run(context.Background(), dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	run(root, "init", "-q")
	for _, wt := range []string{outside, base} {
		run(root, "config", "core.worktree", wt)
		h := GitStatusPath(root, "", nil)
		if h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || len(h.Changes) != 0 {
			t.Errorf("core.worktree %s: %+v", wt, h)
		}
		if snap, ok := gitSnapshot(context.Background(), root); ok || len(snap) != 0 {
			t.Errorf("core.worktree %s: the look after a tool call: %v", wt, snap)
		}
	}
	// A working tree inside the folder is the folder's.
	run(root, "config", "core.worktree", filepath.Join(root, "sub"))
	if h := GitStatusPath(root, "", nil); h.Kind != "status" || len(h.Changes) != 1 || h.Changes[0].Path != "inside.txt" {
		t.Errorf("core.worktree inside the folder: %+v", h)
	}
	run(root, "config", "--unset", "core.worktree")
	// A session in a subfolder of an ordinary repository sees its changes.
	dir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("one\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := GitStatusPath(filepath.Join(dir, "docs"), "", nil); h.Kind != "status" || len(h.Changes) != 1 || h.Changes[0].Path != "README.md" {
		t.Errorf("a subfolder: %+v", h)
	}
	// An untracked link to a file outside counts no lines of it.
	if err := os.Symlink(filepath.Join(outside, "secret-name.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	h := GitStatusPath(dir, "", nil)
	for _, c := range h.Changes {
		if c.Path == "link.txt" && (c.Added != 0 || c.Status != "?") {
			t.Errorf("an untracked link: %+v", c)
		}
	}
}

// A deny entry the working tree no longer has is compared by its path: a
// repository that once committed what the list names (a data directory, a
// config file and the copies beside it) and removed it since does not show
// it at the revision that has it, whether the entry names it through a
// symbolic link on the way or in another case. Names that only start the
// same, and the rest of the revision, still show.
func TestGitShowRefusesDeniedPathsTheWorkingTreeNoLongerHas(t *testing.T) {
	dir := gitRepo(t)
	run := func(args ...string) {
		t.Helper()
		if _, err := gitcli.Run(context.Background(), dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	const secret = "sk-kept-in-history"
	files := map[string]string{
		".conductor/catalog.json":  secret,
		"cfg/conductor.json":       secret,
		"cfg/conductor.json.bak":   secret,
		".conductor-notes/read.md": "notes\n",
		"cfg-other/conductor.md":   "other\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "kept")
	run("rm", "-r", "-q", ".conductor", "cfg")
	run("commit", "-q", "-m", "removed")
	if _, err := os.Stat(filepath.Join(dir, "cfg")); !os.IsNotExist(err) {
		t.Fatalf("the working tree still has cfg: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		deny []string
		path string
	}{
		{"a directory", []string{filepath.Join(dir, ".conductor")}, ".conductor/catalog.json"},
		{"a directory through a link", []string{filepath.Join(link, ".conductor")}, ".conductor/catalog.json"},
		{"a directory in another case", []string{filepath.Join(dir, ".Conductor")}, ".conductor/catalog.json"},
		{"a file", DenyList(nil, []string{filepath.Join(dir, "cfg", "conductor.json")}), "cfg/conductor.json"},
		{"a copy beside a file", DenyList(nil, []string{filepath.Join(dir, "cfg", "conductor.json")}), "cfg/conductor.json.bak"},
		{"a copy beside a file through a link", DenyList(nil, []string{filepath.Join(link, "cfg", "conductor.json")}), "cfg/conductor.json.bak"},
		{"inside a directory named", []string{filepath.Join(dir, "cfg")}, "cfg/conductor.json"},
	} {
		if sh, body := GitShowPath(dir, "HEAD~1", c.path, c.deny, RoleControl); sh.Kind != "error" || sh.Error == nil || sh.Error.Code != "denied" || len(body) != 0 {
			t.Errorf("%s: %s at HEAD~1: %+v %q", c.name, c.path, sh, body)
		}
		if _, err := ResolvePath(dir, c.path, c.deny); err == nil {
			t.Errorf("%s: %s resolved", c.name, c.path)
		}
	}
	deny := DenyList([]string{filepath.Join(dir, ".conductor")}, []string{filepath.Join(dir, "cfg", "conductor.json")})
	// A show needs its revision: with none, "0:<path>" would name the
	// index's file, which the path's check never saw.
	run("add", "-f", "README.md")
	for _, c := range []struct{ rev, path string }{{"", "0:README.md"}, {"", "0:.conductor/catalog.json"}, {"", "README.md"}, {"HEAD:", "README.md"}, {"-p", "README.md"}} {
		if sh, body := GitShowPath(dir, c.rev, c.path, deny, RoleControl); sh.Kind != "error" || sh.Error == nil || sh.Error.Code != "bad_request" || len(body) != 0 {
			t.Errorf("revision %q, %s: %+v %q", c.rev, c.path, sh, body)
		}
	}
	// A revision names a commit, whose top the path is read from: a folder's
	// tree, or a range, would read a denied path under another one.
	tree, err := gitcli.Run(context.Background(), dir, "rev-parse", "HEAD~1:cfg")
	if err != nil {
		t.Fatal(err)
	}
	for _, rev := range []string{strings.TrimSpace(tree), "HEAD~1...HEAD~1"} {
		if sh, body := GitShowPath(dir, rev, "conductor.json", deny, RoleControl); sh.Kind != "error" || len(body) != 0 || strings.Contains(sh.Error.Message, secret) {
			t.Errorf("revision %q: %+v %q", rev, sh, body)
		}
	}
	for p, body := range map[string]string{".conductor-notes/read.md": "notes\n", "cfg-other/conductor.md": "other\n", "README.md": "one\ntwo\n"} {
		if sh, b := GitShowPath(dir, "HEAD~1", p, deny, RoleControl); sh.Kind != "show" || string(b) != body {
			t.Errorf("%s at HEAD~1: %+v %q", p, sh, b)
		}
	}
}
