package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/crew"
)

// paths asks GET /api/paths as the admin and returns the status and reply.
func (e *testEnv) paths(prefix, limit string) (int, map[string]any) {
	e.t.Helper()
	q := url.Values{"prefix": {prefix}}
	if limit != "" {
		q.Set("limit", limit)
	}
	resp, out := e.do("GET", "/api/paths?"+q.Encode(), adminToken, nil)
	return resp.StatusCode, out
}

// entryNames returns the names a paths reply lists, in order.
func entryNames(out map[string]any) []string {
	names := []string{}
	raw, _ := out["entries"].([]any)
	for _, e := range raw {
		names = append(names, e.(map[string]any)["name"].(string))
	}
	return names
}

// entryGit returns the git marks of the entry named name.
func entryGit(out map[string]any, name string) (repo, commits bool) {
	raw, _ := out["entries"].([]any)
	for _, e := range raw {
		m := e.(map[string]any)
		if m["name"] == name {
			g := m["git"].(map[string]any)
			return g["repo"] == true, g["commits"] == true
		}
	}
	return false, false
}

func mkdirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// realRoot is the test root with its symlinks resolved, as the server reports it.
func realRoot(t *testing.T, root string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestPathsListsChildDirectoriesUnderTheRoots(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	mkdirs(t, root, "api", "app", "docs", ".hidden")
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory: all of its child directories, hidden ones and files left out.
	status, out := e.paths(root, "")
	if status != http.StatusOK || out["dir"] != root || out["truncated"] != false {
		t.Fatalf("%d %v", status, out)
	}
	if got := entryNames(out); !slices.Equal(got, []string{"api", "app", "docs"}) {
		t.Fatalf("entries: %v", got)
	}
	if p := out["entries"].([]any)[0].(map[string]any)["path"]; p != filepath.Join(root, "api") {
		t.Fatalf("path: %v", p)
	}
	// A trailing separator means the same.
	if _, out := e.paths(root+string(filepath.Separator), ""); len(entryNames(out)) != 3 {
		t.Fatalf("trailing separator: %v", out)
	}
	// A typed element: the children whose names start with it.
	if _, out := e.paths(filepath.Join(root, "ap"), ""); !slices.Equal(entryNames(out), []string{"api", "app"}) {
		t.Fatalf("prefix ap: %v", out)
	}
	// A hidden directory shows only once the dot is typed, the dot alone included.
	if _, out := e.paths(filepath.Join(root, ".h"), ""); !slices.Equal(entryNames(out), []string{".hidden"}) {
		t.Fatalf("prefix .h: %v", out)
	}
	if _, out := e.paths(root+string(filepath.Separator)+".", ""); out["dir"] != root || !slices.Equal(entryNames(out), []string{".hidden"}) {
		t.Fatalf("prefix .: %v", out)
	}
	// A deeper path that does not exist lists from the longest directory that does.
	if _, out := e.paths(filepath.Join(root, "nope", "deeper"), ""); out["dir"] != root || len(entryNames(out)) != 0 {
		t.Fatalf("missing deeper: %v", out)
	}
	// An empty prefix is the server's default working directory, the root here.
	if _, out := e.paths("", ""); out["dir"] != root || len(entryNames(out)) != 3 {
		t.Fatalf("empty prefix: %v", out)
	}
}

func TestPathsNeverLeaveTheRoots(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	outside := t.TempDir()
	mkdirs(t, outside, "secret")
	mkdirs(t, root, "inside")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "inside"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	// Wholly outside: refused.
	for _, prefix := range []string{outside, filepath.Join(root, ".."), "/", filepath.Join(root, "..", filepath.Base(outside), "sec")} {
		if status, out := e.paths(prefix, ""); status != http.StatusBadRequest || errorCode(out) != "invalid_cwd" {
			t.Fatalf("%s: %d %v", prefix, status, out)
		}
	}
	// Through the link: the listing falls back to the root, and never shows the link or what is behind it.
	for _, prefix := range []string{filepath.Join(root, "escape"), filepath.Join(root, "escape", "sec"), filepath.Join(root, "escape") + string(filepath.Separator)} {
		status, out := e.paths(prefix, "")
		if status != http.StatusOK || out["dir"] != root || slices.Contains(entryNames(out), "escape") || slices.Contains(entryNames(out), "secret") {
			t.Fatalf("%s: %d %v", prefix, status, out)
		}
	}
	// A link that stays inside is listed; one that leaves is not.
	if _, out := e.paths(root, ""); !slices.Equal(entryNames(out), []string{"alias", "inside"}) {
		t.Fatalf("root: %v", entryNames(out))
	}
}

func TestPathsBoundsTheListing(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	for i := range 60 {
		mkdirs(t, root, fmt.Sprintf("d%02d", i))
	}
	if _, out := e.paths(root, ""); len(entryNames(out)) != 50 || out["truncated"] != true {
		t.Fatalf("default limit: %d %v", len(entryNames(out)), out["truncated"])
	}
	if _, out := e.paths(root, "5"); !slices.Equal(entryNames(out), []string{"d00", "d01", "d02", "d03", "d04"}) || out["truncated"] != true {
		t.Fatalf("limit 5: %v", out)
	}
	if _, out := e.paths(root, "500"); len(entryNames(out)) != 50 {
		t.Fatalf("limit 500 is clamped: %v", len(entryNames(out)))
	}
	for _, bad := range []string{"0", "-1", "x"} {
		if status, out := e.paths(root, bad); status != http.StatusBadRequest || errorCode(out) != "invalid_request" {
			t.Fatalf("limit %s: %d %v", bad, status, out)
		}
	}
	if status, out := e.paths(strings.Repeat("a", 4097), ""); status != http.StatusBadRequest || errorCode(out) != "invalid_request" {
		t.Fatalf("long prefix: %d %v", status, out)
	}
	if status, out := e.paths(root+"\x00", ""); status != http.StatusBadRequest || errorCode(out) != "invalid_request" {
		t.Fatalf("NUL: %d %v", status, out)
	}
	if resp, out := e.do("GET", "/api/paths?prefix="+url.QueryEscape(root), "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %v", resp.StatusCode, out)
	}
}

// A listing reads at most maxPathScan entries of a directory, however many
// it holds, and says it may have missed some.
func TestPathsReadsABoundedNumberOfEntries(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	for i := range 30 {
		mkdirs(t, root, fmt.Sprintf("d%02d", i))
	}
	was := maxPathScan
	maxPathScan = 10
	t.Cleanup(func() { maxPathScan = was })
	if _, out := e.paths(root, ""); len(entryNames(out)) != 10 || out["truncated"] != true {
		t.Fatalf("capped read: %v", out)
	}
}

// One listing asks git about each listed entry with a .git of its own and,
// once, about the directory for the others: never more than the limit, one
// call at a time.
func TestPathsAsksGitAtMostOncePerEntry(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	for i := range 60 {
		mkdirs(t, root, filepath.Join(fmt.Sprintf("r%02d", i), ".git"))
	}
	mkdirs(t, root, "zplain") // after the 50 listed, so the directory's marks are never needed
	var asked []string
	was := gitState
	gitState = func(_ context.Context, dir string) (crew.State, error) {
		asked = append(asked, dir)
		return crew.State{InRepo: true, HasCommit: dir != root}, nil
	}
	t.Cleanup(func() { gitState = was })
	_, out := e.paths(root, "")
	if len(entryNames(out)) != 50 || len(asked) != 50 || slices.Contains(asked, root) {
		t.Fatalf("all repositories: %d entries, asked %d: %v", len(entryNames(out)), len(asked), asked)
	}
	asked = nil
	_, out = e.paths(filepath.Join(root, "z"), "")
	if r, c := entryGit(out, "zplain"); !r || c || !slices.Equal(asked, []string{root}) {
		t.Fatalf("zplain carries the directory's marks: %v %v, asked %v", r, c, asked)
	}
}

// gitInit makes dir, then makes it a git repository, with one commit when
// commit is set. It skips the test when git is not installed. HOME is a
// temporary directory so that no configuration of the user running the tests
// applies.
func gitInit(t *testing.T, dir string, commit bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if commit {
		run("-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init")
	}
}

func TestPathsMarksGitRepositories(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	gitInit(t, filepath.Join(root, "repo"), true)
	gitInit(t, filepath.Join(root, "fresh"), false)
	mkdirs(t, root, "plain", filepath.Join("repo", "pkg"))
	_, out := e.paths(root, "")
	for name, want := range map[string][2]bool{"repo": {true, true}, "fresh": {true, false}, "plain": {false, false}} {
		if repo, commits := entryGit(out, name); repo != want[0] || commits != want[1] {
			t.Fatalf("%s: repo %v commits %v", name, repo, commits)
		}
	}
	// A child of a repository, with no .git of its own, carries its parent's marks.
	if _, out := e.paths(filepath.Join(root, "repo"), ""); func() bool { r, c := entryGit(out, "pkg"); return !r || !c }() {
		t.Fatalf("pkg below repo: %v", out)
	}
}

func TestGitCheckAnswersByTheLaunchRules(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	gitInit(t, filepath.Join(root, "repo"), true)
	gitInit(t, filepath.Join(root, "fresh"), false)
	mkdirs(t, root, "plain", filepath.Join("repo", "pkg"))
	check := func(cwd string) (int, map[string]any) {
		t.Helper()
		resp, out := e.do("GET", "/api/git/check?cwd="+url.QueryEscape(cwd), adminToken, nil)
		return resp.StatusCode, out
	}
	if status, out := check(filepath.Join(root, "repo", "pkg")); status != http.StatusOK || out["inRepo"] != true || out["toplevel"] != filepath.Join(root, "repo") || out["hasCommit"] != true {
		t.Fatalf("repo: %d %v", status, out)
	}
	if _, out := check(filepath.Join(root, "fresh")); out["inRepo"] != true || out["hasCommit"] != false || !strings.Contains(out["message"].(string), "without a commit") {
		t.Fatalf("fresh: %v", out)
	}
	if _, out := check(filepath.Join(root, "plain")); out["inRepo"] != false || out["hasCommit"] != false || !strings.Contains(out["message"].(string), "not in a git repository") {
		t.Fatalf("plain: %v", out)
	}
	// Empty is the default working directory; outside the roots is refused as a launch would.
	if _, out := check(""); out["inRepo"] != false {
		t.Fatalf("default cwd: %v", out)
	}
	if status, out := check(t.TempDir()); status != http.StatusBadRequest || errorCode(out) != "invalid_cwd" {
		t.Fatalf("outside: %d %v", status, out)
	}
	if resp, _ := e.do("GET", "/api/git/check?cwd="+url.QueryEscape(root), "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
}
