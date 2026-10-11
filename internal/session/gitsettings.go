package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// A repository's .git and git's own settings and credentials are read by
// controllers only: they often hold access tokens (a remote URL with one in
// it, an http.extraHeader, a url.<base>.insteadOf with one, a credential
// store's lines), and a file of .git may be reached under any name (a
// .git/config that is a link to a file of the working tree, a hard link).
// A view link's read of one, or of one a revision keeps, is answered
// file_denied; a controller, who may run commands in the session anyway,
// reads them as before.

// errForControllers answers a view link's read of a repository's .git or
// of git's settings or credentials (proto.ErrCodeFileDenied).
var errForControllers = errors.New("a repository's .git and git's settings and credentials are for controllers")

// forControllers reports whether a read of raw, which ResolvePath resolved
// to real, is one a view link is not given: anything of a repository's
// .git, however the path reaches it (inGit, which is gitMetadata(raw,
// real): a .git element, a .git file, a git directory, what a metadata
// link leads to, a hard link to an entry); anywhere, a name that contains
// git-credentials (git's credential store), a .gitconfig or gitconfig and
// the copies beside it (.gitconfig.bak, .gitconfig.local); and a name that
// contains config or credential in git's directory of the user's
// configuration (~/.config/git; inUserGitDir). Names are compared ignoring
// case, raw's last element and real's both, so a link to one under another
// name is caught at its target and one named so at its name. inGit is
// asked only when the names do not tell.
func forControllers(raw, real string, inGit func() bool) bool {
	names := []string{filepath.Base(real)}
	if b := filepath.Base(filepath.Clean(raw)); b != names[0] {
		names = append(names, b)
	}
	for _, n := range names {
		if containsFold(n, "git-credentials") || hasPrefixFold(n, ".gitconfig") || strings.EqualFold(n, "gitconfig") {
			return true
		}
		if (containsFold(n, "config") || containsFold(n, "credential")) && inUserGitDir(real) {
			return true
		}
	}
	return inGit()
}

// hasPrefixFold is strings.HasPrefix ignoring case as containsFold does.
func hasPrefixFold(s, prefix string) bool {
	n := len([]rune(prefix))
	r := []rune(s)
	return len(r) >= n && strings.EqualFold(string(r[:n]), prefix)
}

// inUserGitDir reports whether real is in git's directory of the user's
// configuration: git under $XDG_CONFIG_HOME, and under ~/.config, compared
// as a deny entry is (insideAny): as a file where it is there, by its path
// where it is not, so that what a revision keeps there is held too once
// the directory is gone.
func inUserGitDir(real string) bool {
	var dirs []string
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		dirs = append(dirs, filepath.Join(x, "git"))
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		dirs = append(dirs, filepath.Join(home, ".config", "git"))
	}
	return insideAny(real, dirs)
}
