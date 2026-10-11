package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phenixrizen/conductor/internal/proto"
)

// git's settings and credentials are a controller's to read, wherever git
// keeps them: a repository's config and config.worktree and the copies
// beside them, a linked worktree's, a submodule's, a bare repository's, a
// credential store, a .gitconfig and its copies, git's own directory in
// the user's configuration, and a link to any of them. The rest of .git,
// files whose names only look alike outside it, and the controller's own
// reads are as before.
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
		".git-credentials":                 "https://me:t@example.com\n",
		"sub/.git-credentials.old":         "https://me:t@example.com\n",
		".gitconfig":                       "[user]\n",
		".GitConfig.local":                 "[user]\n",
		"dotfiles/gitconfig":               "[user]\n",
		"src/config.go":                    "package src\n",
		"docs/gitconfig-guide.md":          "# guide\n",
		"src/credentials_test.go":          "package src\n",
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
	held := []string{
		".git/config", ".git/config.worktree", ".git/config.bak", ".git/modules/lib/config",
		".git/worktrees/w/config.worktree", "bare.git/config", ".git-credentials", "sub/.git-credentials.old",
		".gitconfig", ".GitConfig.local", "dotfiles/gitconfig", "notes.txt", ".config/git/config", ".config/git/credentials",
	}
	// Another spelling of .git/config, where the file system takes it.
	if _, err := os.Stat(filepath.Join(root, ".GIT", "CONFIG")); err == nil {
		held = append(held, ".GIT/CONFIG")
	}
	open := []string{".git/HEAD", ".git/modules/lib/HEAD", "src/config.go", "docs/gitconfig-guide.md", "src/credentials_test.go", ".config/git/ignore", ".config/other/config", ".git"}
	f, err := OpenFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, p := range held {
		for _, stat := range []bool{false, true} {
			if h, body := f.read(p, stat, nil, RoleView); h.Kind != "error" || h.Error == nil || h.Error.Code != proto.ErrCodeFileDenied || body != nil {
				t.Errorf("view, %q (stat %t): %+v %q", p, stat, h, body)
			}
		}
		if h, _ := f.read(p, false, nil, RoleControl); h.Kind != "file" {
			t.Errorf("control, %q: %+v", p, h)
		}
	}
	for _, p := range open {
		if h, _ := f.read(p, false, nil, RoleView); h.Kind == "error" {
			t.Errorf("view, %q: %+v", p, h)
		}
	}
}
