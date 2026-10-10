package session

import (
	"os"
	"path/filepath"
	"strings"
)

// MaxBranchLen bounds a reported branch name in bytes.
const MaxBranchLen = 200

// GitBranch returns the checked-out branch of the repository containing dir,
// or "" when dir is not in a repository, HEAD is detached, or anything fails.
// It reads .git/HEAD directly so no git binary is needed; a .git *file*
// (worktrees, submodules) is followed to its gitdir.
func GitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		gitPath := filepath.Join(dir, ".git")
		if fi, err := os.Stat(gitPath); err == nil {
			if !fi.IsDir() {
				gitPath = gitDirFromFile(gitPath, dir)
				if gitPath == "" {
					return ""
				}
			}
			return branchFromHead(filepath.Join(gitPath, "HEAD"))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// gitDirFromFile resolves a "gitdir: <path>" pointer file, read as git
// reads one (readGitFile), a relative path from base.
func gitDirFromFile(path, base string) string {
	target, ok := readGitFile(path)
	if !ok {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	return target
}

// branchFromHead parses "ref: refs/heads/<branch>"; a bare SHA is detached.
// HEAD is read with readPrefix: a regular file, at most 4 KiB of it.
func branchFromHead(headPath string) string {
	b, whole, ok := readPrefix(headPath, 4096)
	if !ok || !whole {
		return ""
	}
	line := strings.TrimSpace(string(b))
	ref, ok := strings.CutPrefix(line, "ref:")
	if !ok {
		return ""
	}
	ref = strings.TrimSpace(ref)
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok || branch == "" {
		return ""
	}
	if len(branch) > MaxBranchLen {
		branch = branch[:MaxBranchLen]
	}
	return branch
}
