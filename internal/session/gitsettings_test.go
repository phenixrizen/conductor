package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/phenixrizen/conductor/internal/gitcli"
	"github.com/phenixrizen/conductor/internal/proto"
)

// A repository's .git, and git's settings and credentials wherever git
// keeps them, are a controller's to read: anything of .git (its config,
// config.worktree and their copies, a linked worktree's, a submodule's, a
// bare repository's, the rest of it too, and a file of the working tree a
// .git/config link leads to or a hard link to it), a credential store, a
// .gitconfig and its copies, git's own directory in the user's
// configuration, and a link to any of them. Files whose names only look
// alike outside .git, and the controller's own reads, are as before.
func TestGitSettingsAreForControllers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", "")
	for p, body := range map[string]string{
		".git/HEAD":                        "ref: refs/heads/main\n",
		".git/config":                      "[core]\n",
		".git/config.worktree":             "[core]\n",
		".git/config.bak":                  "[core]\n",
		".git/modules/lib/HEAD":            "ref: refs/heads/main\n",
		".git/modules/lib/config":          "[core]\n",
		".git/worktrees/w/HEAD":            "ref: refs/heads/main\n",
		".git/worktrees/w/config.worktree": "[core]\n",
		"bare.git/HEAD":                    "ref: refs/heads/main\n",
		"bare.git/config":                  "[core]\n",
		"alias/.git/HEAD":                  "ref: refs/heads/main\n",
		"alias/settings":                   "[core]\n",
		".git-credentials":                 "https://me:t@example.com\n",
		"sub/.git-credentials.old":         "https://me:t@example.com\n",
		".gitconfig":                       "[user]\n",
		".GitConfig.local":                 "[user]\n",
		"dotfiles/gitconfig":               "[user]\n",
		"src/config.go":                    "package src\n",
		"docs/gitconfig-guide.md":          "# guide\n",
		"src/credentials_test.go":          "package src\n",
		"settings.txt":                     "plain\n",
		".config/git/config":               "[user]\n",
		".config/git/credentials":          "https://me:t@example.com\n",
		".config/git/ignore":               "*.o\n",
		".config/other/config":             "x\n",
	} {
		full := filepath.Join(root, p)
		for _, d := range []string{filepath.Dir(full), filepath.Join(root, ".git", "objects"), filepath.Join(root, ".git", "refs"), filepath.Join(root, "bare.git", "objects"), filepath.Join(root, "bare.git", "refs")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, ".git", "config"), filepath.Join(root, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	// A repository whose .git/config is a link to a file of its working tree.
	if err := os.Symlink(filepath.Join("..", "settings"), filepath.Join(root, "alias", ".git", "config")); err != nil {
		t.Fatal(err)
	}
	// A hard link to .git/config under an ordinary name.
	if err := os.Link(filepath.Join(root, ".git", "config"), filepath.Join(root, "hard.txt")); err != nil {
		t.Fatal(err)
	}
	held := []string{
		".git/config", ".git/config.worktree", ".git/config.bak", ".git/HEAD", ".git/modules/lib/config", ".git/modules/lib/HEAD",
		".git/worktrees/w/config.worktree", "bare.git/config", "bare.git/HEAD", "alias/settings", "alias/.git/config", "hard.txt",
		".git-credentials", "sub/.git-credentials.old", ".gitconfig", ".GitConfig.local", "dotfiles/gitconfig", "notes.txt",
		".config/git/config", ".config/git/credentials",
	}
	// Another spelling of .git/config, where the file system takes it.
	if _, err := os.Stat(filepath.Join(root, ".GIT", "CONFIG")); err == nil {
		held = append(held, ".GIT/CONFIG")
	}
	open := []string{"src/config.go", "docs/gitconfig-guide.md", "src/credentials_test.go", ".config/git/ignore", ".config/other/config", "settings.txt", "src"}
	f, err := OpenFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, p := range append(held, ".git", ".git/modules") {
		for _, stat := range []bool{false, true} {
			if h, body := f.read(p, stat, nil, RoleView); h.Kind != "error" || h.Error == nil || h.Error.Code != proto.ErrCodeFileDenied || body != nil {
				t.Errorf("view, %q (stat %t): %+v %q", p, stat, h, body)
			}
		}
	}
	for _, p := range held {
		if h, _ := f.read(p, false, nil, RoleControl); h.Kind != "file" {
			t.Errorf("control, %q: %+v", p, h)
		}
	}
	if h, _ := f.read(".git", false, nil, RoleControl); h.Kind != "dir" {
		t.Errorf("control, .git: %+v", h)
	}
	for _, p := range open {
		if h, _ := f.read(p, false, nil, RoleView); h.Kind == "error" {
			t.Errorf("view, %q: %+v", p, h)
		}
	}
}

// git's settings in the home are held as files: through the link the home
// keeps them at, and at the file the link leads to under any name, with the
// copies beside it, in a read and in a revision alike.
func TestGitSettingsBehindLinksAreForControllers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", "")
	run := func(args ...string) {
		t.Helper()
		if _, err := gitcli.Run(context.Background(), root, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q")
	run("config", "user.name", "t")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	for p, body := range map[string]string{"dotfiles/private-settings": "[user]\n", "dotfiles/private-settings.bak": "[user]\n", "dotfiles/mine": "[user]\n", "dotfiles/readme.md": "# dotfiles\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".config", "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "dotfiles", "private-settings"), filepath.Join(root, ".config", "git", "config")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("dotfiles", "mine"), filepath.Join(root, ".gitconfig")); err != nil {
		t.Fatal(err)
	}
	run("add", "dotfiles")
	run("commit", "-q", "-m", "dotfiles")
	f, err := OpenFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, p := range []string{".config/git/config", "dotfiles/private-settings", "dotfiles/private-settings.bak", ".gitconfig", "dotfiles/mine"} {
		if h, body := f.read(p, false, nil, RoleView); h.Kind != "error" || h.Error == nil || h.Error.Code != proto.ErrCodeFileDenied || body != nil {
			t.Errorf("view, %q: %+v %q", p, h, body)
		}
		if h, _ := f.read(p, false, nil, RoleControl); h.Kind != "file" {
			t.Errorf("control, %q: %+v", p, h)
		}
	}
	for _, p := range []string{"dotfiles/private-settings", "dotfiles/mine"} {
		if h, body := GitShowPath(root, "HEAD", p, nil, RoleView); h.Kind != "error" || h.Error == nil || h.Error.Code != proto.ErrCodeFileDenied || len(body) != 0 {
			t.Errorf("view, %s at HEAD: %+v %q", p, h, body)
		}
	}
	if h, _ := f.read("dotfiles/readme.md", false, nil, RoleView); h.Kind != "file" {
		t.Errorf("view, an ordinary file beside them: %+v", h)
	}
}

// The copies an editor or a person leaves of a git configuration file are
// held wherever they are, whatever wraps the name; names that only start
// the same are not.
func TestCopiesOfAGitConfigAreForControllers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	held := []string{"#.gitconfig#", ".#.gitconfig", ".gitconfig.swp", "x.gitconfig~", "#gitconfig#", ".#gitconfig", "gitconfig~", "gitconfig.bak", "#.git-credentials#", ".git-credentials~"}
	open := []string{"gitconfig-guide.md", "mygitconfig.go", "config.go"}
	for _, n := range append(slices.Clone(held), open...) {
		if err := os.WriteFile(filepath.Join(root, n), []byte("[user]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := OpenFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, n := range held {
		if h, body := f.read(n, false, nil, RoleView); h.Kind != "error" || h.Error == nil || h.Error.Code != proto.ErrCodeFileDenied || body != nil {
			t.Errorf("view, %q: %+v %q", n, h, body)
		}
		if h, _ := f.read(n, false, nil, RoleControl); h.Kind != "file" {
			t.Errorf("control, %q: %+v", n, h)
		}
	}
	for _, n := range open {
		if h, _ := f.read(n, false, nil, RoleView); h.Kind != "file" {
			t.Errorf("view, %q: %+v", n, h)
		}
	}
}

// Names are compared as a case-insensitive file system that leaves some
// characters out (HFS+) would take them: a .gitconfig or a credential store
// spelt with such characters in it is held, and so are the copies beside a
// denied file and a missing entry's path spelt so.
func TestNamesAreComparedWithoutTheCharactersHFSIgnores(t *testing.T) {
	held := newDenySet(nil)
	never := func() bool { return false }
	for _, n := range []string{".gitcon\u200cfig", ".GIT\u200dCONFIG.local", "git\ufeffconfig", ".git-cred\u200centials", "gitconfig~\u200c", "\u200c#gitconfig#", "#\u200dgitconfig\u200c#", ".\u200c#GitConfig"} {
		if !forControllers("dotfiles/"+n, "/x/dotfiles/"+n, held, never) {
			t.Errorf("%q not held", n)
		}
	}
	if forControllers("dotfiles/gitconfig-guide.md", "/x/dotfiles/gitconfig-guide.md", held, never) {
		t.Error("an ordinary name held")
	}
	root := setupTree(t)
	deny := []string{filepath.Join(root, "conductor.json"), filepath.Join(root, "*conductor.json*"), filepath.Join(root, "gone", "catalog.json")}
	for _, p := range []string{"conductor\u200c.json.bak", "gone/cata\u200dlog.json", "go\ufeffne/catalog.json"} {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%q resolved to %s", p, r)
		}
	}
	// A composed name and its decomposed spelling are one name (HFS+, APFS).
	composed, decomposed := "caf\u00e9", "cafe\u0301"
	deny = []string{filepath.Join(root, composed+".json"), filepath.Join(root, "*"+composed+".json*"), filepath.Join(root, "gone-"+composed, "x")}
	for _, p := range []string{decomposed + ".json", decomposed + ".json.bak", "gone-" + decomposed + "/x"} {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%q resolved to %s", p, r)
		}
	}
	deny = []string{filepath.Join(root, "*"+decomposed+".json*")}
	if r, err := ResolvePath(root, composed+".json.bak", deny); err == nil {
		t.Errorf("a composed copy beside a decomposed name resolved to %s", r)
	}
	// A letter whose composed form only one case has (U+01F0, j with caron):
	// the other case spelt out is the same name too.
	deny = []string{filepath.Join(root, "\u01f0.json"), filepath.Join(root, "*\u01f0.json*")}
	for _, p := range []string{"J\u030c.json.bak", "j\u030c.json.bak", "\u01f0.JSON.bak"} {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%q resolved to %s", p, r)
		}
	}
	if !sameName("J\u030c", "\u01f0") || !containsFold("x-J\u030c.json.bak", "\u01f0.json") || sameName("J", "\u01f0") {
		t.Error("j with caron in either case and spelling")
	}
}

// A home git file that is a link to nothing yet holds the copies beside the
// path its target would be: the copies there are a controller's, the files
// beside them with other names are not held.
func TestCopiesBesideADanglingGitFileTargetAreHeld(t *testing.T) {
	root := realDir(t, t.TempDir())
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := os.MkdirAll(filepath.Join(root, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"private-store.bak", "notes.md"} {
		if err := os.WriteFile(filepath.Join(root, "repo", n), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("repo", "private-store"), filepath.Join(root, ".git-credentials")); err != nil {
		t.Fatal(err)
	}
	if deny := DenyList(nil, []string{filepath.Join(root, ".git-credentials")}); !slices.Contains(deny, filepath.Join(root, "repo", "*private-store*")) {
		t.Fatalf("no entry beside the target to be: %q", deny)
	}
	f, err := OpenFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if h, body := f.read("repo/private-store.bak", false, nil, RoleView); h.Kind != "error" || h.Error == nil || h.Error.Code != proto.ErrCodeFileDenied || body != nil {
		t.Errorf("view, a copy beside the target to be: %+v %q", h, body)
	}
	if h, _ := f.read("repo/private-store.bak", false, nil, RoleControl); h.Kind != "file" {
		t.Errorf("control, a copy beside the target to be: %+v", h)
	}
	if h, _ := f.read("repo/notes.md", false, nil, RoleView); h.Kind != "file" {
		t.Errorf("view, a file beside it: %+v", h)
	}
}

// A view link's read is held to what it checked as it is opened: a file put
// in its place since as a hard link to .git/config, or a folder swapped for
// a repository's git directory, is refused, never read.
func TestAViewReadIsHeldToItsCheckWhenOpened(t *testing.T) {
	root := t.TempDir()
	for p, body := range map[string]string{".git/HEAD": "ref: refs/heads/main\n", ".git/config": "[remote]\n\turl = https://me:token@example.com\n", "notes.txt": "notes\n", "docs/config": "plain\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{".git/objects", ".git/refs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f, err := OpenFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	onceAfterCheck(t, func() {
		os.Remove(filepath.Join(root, "notes.txt"))
		if err := os.Link(filepath.Join(root, ".git", "config"), filepath.Join(root, "notes.txt")); err != nil {
			t.Error(err)
		}
	})
	if h, body := f.read("notes.txt", false, nil, RoleView); h.Kind != "error" || body != nil {
		t.Fatalf("a hard link to .git/config put in place since its check: %+v %q", h, body)
	}
	onceAfterCheck(t, func() {
		if err := os.Rename(filepath.Join(root, "docs"), filepath.Join(root, "docs.old")); err != nil {
			t.Error(err)
		}
		if err := os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "docs")); err != nil {
			t.Error(err)
		}
	})
	if h, body := f.read("docs/config", false, nil, RoleView); h.Kind != "error" || body != nil {
		t.Fatalf("a git directory swapped in since its check: %+v %q", h, body)
	}
}

// git's settings a revision keeps are a controller's too: a .gitconfig, and
// what git's directory of the user's configuration held, once that
// directory is gone from the working tree.
func TestGitShowOfSettingsIsForControllers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", "")
	run := func(args ...string) {
		t.Helper()
		if _, err := gitcli.Run(context.Background(), root, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q")
	run("config", "user.name", "t")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	for p, body := range map[string]string{".gitconfig": "[user]\n", ".config/git/config": "[user]\n", ".config/git/credentials": "x\n", "README.md": "hi\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "settings")
	if err := os.RemoveAll(filepath.Join(root, ".config")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".gitconfig", ".config/git/config", ".config/git/credentials"} {
		if h, body := GitShowPath(root, "HEAD", p, nil, RoleView); h.Kind != "error" || h.Error == nil || h.Error.Code != proto.ErrCodeFileDenied || len(body) != 0 {
			t.Errorf("view, %s at HEAD: %+v %q", p, h, body)
		}
		if h, _ := GitShowPath(root, "HEAD", p, nil, RoleControl); h.Kind != "show" {
			t.Errorf("control, %s at HEAD: %+v", p, h)
		}
	}
	if h, body := GitShowPath(root, "HEAD", "README.md", nil, RoleView); h.Kind != "show" || string(body) != "hi\n" {
		t.Errorf("view, README.md at HEAD: %+v %q", h, body)
	}
}
