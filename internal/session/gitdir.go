package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/gitcli"
)

// A repository's .git is read only through the Files tab: its directory,
// the .git file of a linked worktree or a submodule, the git directory
// such a file or a .git link names, a bare repository. A save there is
// refused, as is a Neovim editor on a file there (Neovim would write it),
// and a read says so (ReadOnly), so the editor shows Read only. Reads stay
// as they were, .git/objects aside: the tab leaves .git out of the tree
// and of Find, but a path an agent printed or touched still opens, and
// what is in .git is no more private than the rest of the working
// directory a link may read.

// errGitDir refuses a save or an editor on a file in a repository's .git.
var errGitDir = errors.New("session: a repository's .git is read only here")

// gitDirTimeout bounds the git that inGitDir asks.
const gitDirTimeout = 5 * time.Second

// gitMetadata reports whether a file request for raw, which ResolvePath
// resolved to real, is for a repository's .git: an element of raw as asked
// (before its links are resolved) is .git however spelled, or inGitDir
// says so of real.
func gitMetadata(raw, real string) bool {
	for _, el := range strings.Split(filepath.ToSlash(raw), "/") {
		if isDotGit(el) {
			return true
		}
	}
	return inGitDir(real)
}

// inGitDir reports whether real, an absolute path with its symbolic links
// resolved (ResolvePath's), is in a repository's .git. Any one of three
// says so: an element of the path is .git however a file system may spell
// it (isDotGit); the path is, or is inside, what a .git beside it or
// beside one of its parents stands for (a .git directory, a .git file
// itself, the git directory it names), compared as files (os.SameFile) so
// that neither a link nor another spelling gets around it (gitDirsIn); git
// itself, asked in the nearest directory that exists, says that directory
// is inside a git directory, which also finds a git directory a .git
// elsewhere points to and a bare repository (without git, the first two
// still hold).
func inGitDir(real string) bool {
	for _, el := range strings.Split(real, string(filepath.Separator)) {
		if isDotGit(el) {
			return true
		}
	}
	var dirs []os.FileInfo
	for p := real; ; p = filepath.Dir(p) {
		dirs = append(dirs, gitDirsIn(p)...)
		if filepath.Dir(p) == p {
			break
		}
	}
	for p := real; len(dirs) > 0; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil {
			for _, d := range dirs {
				if os.SameFile(fi, d) {
					return true
				}
			}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return gitSaysGitDir(real)
}

// gitDirsIn is what dir's .git stands for: itself when it is a directory
// (or a link to one); when it is a file (or a link to one), the file
// itself, the directory its "gitdir:" line names and the one that
// directory's commondir names.
func gitDirsIn(dir string) []os.FileInfo {
	dotgit := filepath.Join(dir, ".git")
	fi, err := os.Stat(dotgit)
	switch {
	case err != nil:
		return nil
	case fi.IsDir():
		return []os.FileInfo{fi}
	case !fi.Mode().IsRegular():
		return nil
	}
	out := []os.FileInfo{fi}
	gd := gitDirFromFile(dotgit, dir)
	if gd == "" {
		return out
	}
	gfi, err := os.Stat(gd)
	if err != nil || !gfi.IsDir() {
		return out
	}
	out = append(out, gfi)
	if b, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil && len(b) <= 4096 {
		common := strings.TrimSpace(string(b))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gd, common)
		}
		if cfi, err := os.Stat(common); err == nil && cfi.IsDir() {
			out = append(out, cfi)
		}
	}
	return out
}

// gitSaysGitDir asks git (gitcli.Run, which starts nothing the repository
// configures) whether the nearest directory of real that exists is inside
// a git directory. No git, or any failure, is no.
func gitSaysGitDir(real string) bool {
	dir := real
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitDirTimeout)
	defer cancel()
	out, err := gitcli.Run(ctx, dir, "rev-parse", "--is-inside-git-dir")
	return err == nil && strings.TrimSpace(out) == "true"
}

// isDotGit reports whether a file system may take name for .git: in any
// case, with trailing dots or spaces (NTFS drops them), with characters
// HFS+ ignores, or as NTFS's short name GIT~1. A name that does not exist
// yet has only its spelling to go by.
func isDotGit(name string) bool {
	n := strings.Map(func(r rune) rune {
		if hfsIgnored(r) {
			return -1
		}
		return r
	}, name)
	n = strings.TrimRight(n, ". ")
	return strings.EqualFold(n, ".git") || strings.EqualFold(n, "git~1")
}

// hfsIgnored is a code point HFS+ leaves out when it compares names (the
// list git checks .git against too).
func hfsIgnored(r rune) bool {
	switch {
	case r >= 0x200c && r <= 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x206a && r <= 0x206f, r == 0xfeff:
		return true
	}
	return false
}
