package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
