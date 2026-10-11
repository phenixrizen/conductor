package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// git's settings and credentials are read by controllers only: they often
// hold access tokens (a remote URL with one in it, an http.extraHeader, a
// url.<base>.insteadOf with one, a credential store's lines). A view link's
// read of one, or of one a revision keeps, is answered file_denied; a
// controller, who may run commands in the session anyway, reads them as
// before.

// errForControllers answers a view link's read of git's settings or
// credentials (proto.ErrCodeFileDenied).
var errForControllers = errors.New("git's settings and credentials are for controllers")

// forControllers reports whether a read of raw, which ResolvePath resolved
// to real, is of git's settings or credentials: in a repository's .git
// (gitMetadata), a name that contains config or credential, which takes in
// config and config.worktree, a linked worktree's and a submodule's, and
// the copies beside them; anywhere, a name that contains git-credentials
// (git's credential store), a .gitconfig or gitconfig and the copies beside
// it (.gitconfig.bak, .gitconfig.local); and a name that contains config or
// credential in git's directory of the user's configuration (~/.config/git,
// compared as a file). Names are compared ignoring case, raw's last element
// and real's both, so a link to one under another name is caught at its
// target and one named so at its name.
func forControllers(raw, real string) bool {
	names := []string{filepath.Base(real)}
	if b := filepath.Base(filepath.Clean(raw)); b != names[0] {
		names = append(names, b)
	}
	looked, inGit := false, false
	for _, n := range names {
		if containsFold(n, "git-credentials") || hasPrefixFold(n, ".gitconfig") || strings.EqualFold(n, "gitconfig") {
			return true
		}
		if !containsFold(n, "config") && !containsFold(n, "credential") {
			continue
		}
		if inUserGitDir(real) {
			return true
		}
		if !looked {
			looked, inGit = true, gitMetadata(raw, real)
		}
		if inGit {
			return true
		}
	}
	return false
}

// hasPrefixFold is strings.HasPrefix ignoring case as containsFold does.
func hasPrefixFold(s, prefix string) bool {
	n := len([]rune(prefix))
	r := []rune(s)
	return len(r) >= n && strings.EqualFold(string(r[:n]), prefix)
}

// inUserGitDir reports whether real's directory is git's directory of the
// user's configuration: git under $XDG_CONFIG_HOME, else under
// ~/.config, compared as files.
func inUserGitDir(real string) bool {
	dir, err := os.Stat(filepath.Dir(real))
	if err != nil {
		return false
	}
	var bases []string
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		bases = append(bases, x)
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		bases = append(bases, filepath.Join(home, ".config"))
	}
	for _, b := range bases {
		if fi, err := os.Stat(filepath.Join(b, "git")); err == nil && os.SameFile(fi, dir) {
			return true
		}
	}
	return false
}
