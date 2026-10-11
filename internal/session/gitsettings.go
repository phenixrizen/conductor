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
// the copies an editor or a person leaves of it (gitconfigName); and git's
// settings and credentials in the home (held, the deny set of
// userGitFiles: compared as files, a link's target and the copies beside
// it included, and by their paths where they are not there). Names are
// compared ignoring case, raw's last element and real's both. inGit is
// asked only when the rest does not tell.
func forControllers(raw, real string, held denySet, inGit func() bool) bool {
	names := []string{filepath.Base(real)}
	if b := filepath.Base(filepath.Clean(raw)); b != names[0] {
		names = append(names, b)
	}
	for _, n := range names {
		if containsFold(n, "git-credentials") || gitconfigName(n) {
			return true
		}
	}
	return held.inside(real) || inGit()
}

// gitconfigName reports whether n names a git configuration file or a copy
// of one: a name that contains .gitconfig (.gitconfig.bak, #.gitconfig#,
// .gitconfig.swp), or gitconfig (as a dotfiles folder keeps it) with what
// an editor puts around it (#gitconfig#, .#gitconfig, gitconfig~) or a
// suffix after a dot (gitconfig.bak, gitconfig.local). A name that only
// starts the same (gitconfig-guide.md) is not one.
func gitconfigName(n string) bool {
	if containsFold(n, ".gitconfig") {
		return true
	}
	t := strings.TrimRight(strings.TrimLeft(n, "#.~"), "#~")
	return sameName(t, "gitconfig") || hasPrefixFold(t, "gitconfig.")
}

// hasPrefixFold is strings.HasPrefix comparing names as containsFold does.
func hasPrefixFold(s, prefix string) bool {
	prefix = foldName(prefix)
	n := len([]rune(prefix))
	r := []rune(foldName(s))
	return len(r) >= n && sameName(string(r[:n]), prefix)
}

// userGitFiles is the deny list (DenyList) of git's own settings and
// credentials in the user's home: ~/.gitconfig and ~/.git-credentials,
// and config and credentials in git's directory of the user's
// configuration ($XDG_CONFIG_HOME/git, ~/.config/git). Each is a file
// entry, so that a link to it, or the file a link of its leads to under
// another name, is the same file, with the name entries beside it and
// beside its target, so that its copies are too; one that is not there is
// compared by its path (missingEntry), so what a revision keeps there is
// held as well.
func userGitFiles() []string {
	var files []string
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		files = append(files,
			filepath.Join(home, ".gitconfig"), filepath.Join(home, ".git-credentials"),
			filepath.Join(home, ".config", "git", "config"), filepath.Join(home, ".config", "git", "credentials"))
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		files = append(files, filepath.Join(x, "git", "config"), filepath.Join(x, "git", "credentials"))
	}
	return DenyList(nil, files)
}
