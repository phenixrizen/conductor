// Package gitcli runs the git binary for the server and conductor host: the
// working directory's status with each file's added and removed lines, a
// file at a revision, and the helper the crew engine's worktrees use. The
// commands are argv arrays run in the C locale; nothing is a shell string.
// go-git (the round 12 decision) is for what it does without hashing the
// working tree, the log and a commit's diffs; status stays here.
package gitcli

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

// ErrNotRepo is a directory outside any git working tree.
var ErrNotRepo = errors.New("not a git repository")

// Bounds of a status: how many changed files are listed, and how much of a
// file is read to count an untracked file's lines or to show a revision.
const (
	MaxChanges   = 500
	MaxShowBytes = 1 << 20
)

// Change is one changed file of a status: its path relative to the working
// tree's top, its status letter (M modified, A added, D deleted, R renamed,
// ? untracked), its added and removed lines against the base, and whether
// git counts it as binary.
type Change struct {
	Path    string
	Status  string
	Added   int
	Removed int
	Binary  bool
}

// Status is a working directory's git status against a base revision.
type Status struct {
	// Top is the working tree's top; Branch the branch checked out (HEAD
	// when detached); Base the base revision's short id, empty when the
	// base cannot be resolved (a repository without a commit yet).
	Top, Branch, Base string
	Changes           []Change
	// Added and Removed sum the changes listed.
	Added, Removed int
	// Truncated says the list stopped at MaxChanges.
	Truncated bool
}

// Run runs git with args in dir (git -C dir, so it is the same wherever the
// process runs) in the C locale. A failure carries the line that says why.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = pty.BuildEnv(pty.ParentEnv(), nil, map[string]string{"LC_ALL": "C"}, nil)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if msg := Message(stderr.String()); msg != "" {
			if strings.Contains(msg, "not a git repository") {
				return "", ErrNotRepo
			}
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

// Message picks the line of git's standard error that says why it failed:
// the first "fatal:" or "error:" line, else the last line. Newer gits print
// progress first ("Preparing worktree (new branch …)"), so the first line
// is not it.
func Message(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	last := ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") {
			return l
		}
		last = l
	}
	return last
}

// GetStatus is dir's working tree status against base (HEAD when empty):
// every changed and untracked file with its status letter and its added and
// removed lines (an untracked file's lines are counted from the file itself,
// a binary one as none), at most MaxChanges of them, paths relative to the
// tree's top. ErrNotRepo outside a repository; a base that does not resolve
// leaves Base empty and the counts at zero.
func GetStatus(ctx context.Context, dir, base string) (Status, error) {
	if base == "" {
		base = "HEAD"
	}
	if strings.HasPrefix(base, "-") {
		return Status{}, fmt.Errorf("git: invalid base %q", base)
	}
	top, err := Run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Status{}, err
	}
	st := Status{Top: strings.TrimSpace(top)}
	if out, err := Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		st.Branch = strings.TrimSpace(out)
	}
	if out, err := Run(ctx, dir, "rev-parse", "--short", "--verify", "--quiet", base+"^{commit}"); err == nil {
		st.Base = strings.TrimSpace(out)
	}
	raw, err := Run(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return Status{}, err
	}
	counts := map[string][2]int{}
	binary := map[string]bool{}
	if st.Base != "" {
		if out, err := Run(ctx, dir, "diff", "--numstat", "-z", "--no-renames", base, "--"); err == nil {
			for _, rec := range strings.Split(out, "\x00") {
				parts := strings.SplitN(rec, "\t", 3)
				if len(parts) != 3 {
					continue
				}
				if parts[0] == "-" {
					binary[parts[2]] = true
					continue
				}
				a, _ := strconv.Atoi(parts[0])
				r, _ := strconv.Atoi(parts[1])
				counts[parts[2]] = [2]int{a, r}
			}
		}
	}
	tokens := strings.Split(raw, "\x00")
	for i := 0; i < len(tokens); i++ {
		rec := tokens[i]
		if len(rec) < 4 {
			continue
		}
		x, y, path := rec[0], rec[1], rec[3:]
		if x == 'R' || x == 'C' {
			// The record the rename came from follows as its own entry.
			i++
		}
		status := "M"
		switch {
		case x == '?' || y == '?':
			status = "?"
		case x == 'D' || y == 'D':
			status = "D"
		case x == 'R' || y == 'R':
			status = "R"
		case x == 'A' || y == 'A':
			status = "A"
		}
		c := Change{Path: path, Status: status, Binary: binary[path]}
		if n, ok := counts[path]; ok {
			c.Added, c.Removed = n[0], n[1]
		} else if status == "?" || status == "A" {
			c.Added, c.Binary = countLines(filepath.Join(st.Top, filepath.FromSlash(path)))
		}
		if len(st.Changes) == MaxChanges {
			st.Truncated = true
			break
		}
		st.Changes = append(st.Changes, c)
		st.Added += c.Added
		st.Removed += c.Removed
	}
	return st, nil
}

// countLines counts a file's lines for an untracked file's added count, up
// to MaxShowBytes of it; a file with a NUL in its first bytes is binary and
// counts none.
func countLines(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	buf := make([]byte, MaxShowBytes)
	n, _ := f.Read(buf)
	buf = buf[:n]
	head := buf
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return 0, true
	}
	if n == 0 {
		return 0, false
	}
	lines := bytes.Count(buf, []byte{'\n'})
	if buf[n-1] != '\n' {
		lines++
	}
	return lines, false
}

// Show is the content of path (relative to the working tree's top) at rev,
// at most MaxShowBytes of it (truncated says so); ErrNotRepo outside a
// repository, an error for a path the revision does not have.
func Show(ctx context.Context, dir, rev, path string) (content []byte, truncated bool, err error) {
	if strings.HasPrefix(rev, "-") || strings.HasPrefix(path, "-") || strings.Contains(rev, ":") {
		return nil, false, fmt.Errorf("git show: invalid revision or path")
	}
	out, err := Run(ctx, dir, "show", rev+":"+filepath.ToSlash(path))
	if err != nil {
		return nil, false, err
	}
	b := []byte(out)
	if len(b) > MaxShowBytes {
		return b[:MaxShowBytes], true, nil
	}
	return b, false, nil
}
