package crew

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/phenixrizen/conductor/internal/pty"
)

// With isolation "worktree" every member of a run works in a git worktree of
// the crew's working directory, on a branch of its own. git runs with argv,
// never through a shell. Conductor never removes a worktree or a branch.

// ErrNotRepo says that worktrees cannot be made of a directory: it is not the
// top of a git working tree (it has no .git), or its HEAD is no commit to
// branch from.
var ErrNotRepo = errors.New("not a git repository")

// repoError is an ErrNotRepo that says which of the two it is.
type repoError struct{ msg string }

func (e *repoError) Error() string { return e.msg }
func (e *repoError) Unwrap() error { return ErrNotRepo }

var (
	errNoGit    = &repoError{"the working directory is not a git repository (it has no .git)"}
	errNoCommit = &repoError{"the working directory is a git repository without a commit: a worktree needs one to branch from"}
)

// hasGit reports whether dir holds a .git: a directory, or the file of a
// linked worktree or a submodule.
func hasGit(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// CheckRepo reports whether worktrees can be made of repo: it must hold a
// .git and its HEAD must be a commit. The error matches ErrNotRepo otherwise.
func CheckRepo(ctx context.Context, repo string) error {
	if !hasGit(repo) {
		return errNoGit
	}
	if _, err := headCommit(ctx, repo); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errNoCommit
	}
	return nil
}

// AddWorktree adds a worktree of repo at path on a new branch made from HEAD:
// git -C repo worktree add -b branch path HEAD. git makes the parent
// directories of path. The error matches ErrNotRepo when repo has no .git.
func AddWorktree(ctx context.Context, repo, path, branch string) error {
	if !hasGit(repo) {
		return errNoGit
	}
	_, err := git(ctx, repo, "worktree", "add", "-b", branch, path, "HEAD")
	return err
}

// DiffStat counts the lines a worktree's branch adds and removes since base,
// the commit it started from: git -C worktree diff --shortstat base...HEAD.
// What is not committed does not count. The engine keeps what it reads for 10 s.
func DiffStat(ctx context.Context, worktree, base string) (added, removed int, err error) {
	if base == "" || strings.HasPrefix(base, "-") {
		return 0, 0, fmt.Errorf("git diff: invalid base %q", base)
	}
	out, err := git(ctx, worktree, "diff", "--shortstat", base+"...HEAD")
	if err != nil {
		return 0, 0, err
	}
	return parseShortstat(out)
}

// parseShortstat reads git diff --shortstat, as the C locale writes it:
// " 3 files changed, 10 insertions(+), 2 deletions(-)", where either count may
// be left out, and nothing at all when nothing changed.
func parseShortstat(s string) (added, removed int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	for part := range strings.SplitSeq(s, ",") {
		fields := strings.Fields(part)
		if len(fields) < 2 {
			return 0, 0, fmt.Errorf("git diff --shortstat: cannot read %q", s)
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0, 0, fmt.Errorf("git diff --shortstat: cannot read %q", s)
		}
		switch {
		case strings.HasPrefix(fields[1], "insertion"):
			added = n
		case strings.HasPrefix(fields[1], "deletion"):
			removed = n
		}
	}
	return added, removed, nil
}

// headCommit returns the commit HEAD names in dir.
func headCommit(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// git runs git -C dir args and returns what it writes to its standard output.
// Its environment is a session's allowlist from the server's (so no
// CONDUCTOR_* variable, the admin token among them, reaches git or the hooks
// it runs) in the C locale, which --shortstat is read in. A failure carries
// the first line git wrote to its standard error.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = pty.BuildEnv(pty.ParentEnv(), nil, map[string]string{"LC_ALL": "C"}, nil)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.String(), nil
}
