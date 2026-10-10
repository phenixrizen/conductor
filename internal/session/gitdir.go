package session

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// What is read of a repository's files, as git reads them: a .git file or
// a commondir whole, up to the 1 MiB git takes of a .git file; of a HEAD
// its first bytes, which are all git looks at to tell a git directory.
const (
	maxGitFile = 1 << 20
	headPrefix = 255
)

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
// resolved (ResolvePath's), is in a repository's .git. Any one of four
// says so: an element of the path is .git however a file system may spell
// it (isDotGit); the path is a file git would take as a .git file, which a
// .git link anywhere may point to (isGitFile); the path or one of its
// parents is what a .git beside it or beside one of its parents stands for
// (a .git directory, a .git file itself, the git directory it names),
// compared as files (os.SameFile) so that neither a link nor another
// spelling gets around it (gitDirsIn); or the path or one of its parents is
// a git directory by its own files (isGitDirectory), which finds a bare
// repository and a git directory a .git elsewhere points to, whatever
// git's own settings would discover. Nothing here runs git, and every file
// it reads is read bounded and without blocking (readPrefix).
func inGitDir(real string) bool {
	for _, el := range strings.Split(real, string(filepath.Separator)) {
		if isDotGit(el) {
			return true
		}
	}
	if isGitFile(real) {
		return true
	}
	var dirs []os.FileInfo
	for p := real; ; p = filepath.Dir(p) {
		dirs = append(dirs, gitDirsIn(p)...)
		if filepath.Dir(p) == p {
			break
		}
	}
	for p := real; ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil {
			for _, d := range dirs {
				if os.SameFile(fi, d) {
					return true
				}
			}
		}
		if isGitDirectory(p) {
			return true
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

// gitDirsIn is what dir's .git stands for: itself when it is a directory
// (or a link to one); when it is a file (or a link to one), the file
// itself, the directory its "gitdir: " line names and the one that
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
	if b, whole, ok := readPrefix(filepath.Join(gd, "commondir"), maxGitFile); ok && whole {
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

// isGitDirectory reports whether dir is a git directory by its files, as
// git tells one: a valid HEAD (validHead), and either the refs and objects
// directories or (a linked worktree's) a commondir file.
func isGitDirectory(dir string) bool {
	if !validHead(filepath.Join(dir, "HEAD")) {
		return false
	}
	if fi, err := os.Stat(filepath.Join(dir, "commondir")); err == nil && fi.Mode().IsRegular() {
		return true
	}
	for _, sub := range []string{"objects", "refs"} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}

// validHead reports whether path is a HEAD as git takes one: a link whose
// target starts with refs/ (not followed: its ref may be packed or not
// born yet), or a regular file whose first bytes are "ref:", spaces and
// refs/..., or an object id in hex of either case.
func validHead(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return err == nil && strings.HasPrefix(target, "refs/")
	}
	head, _, ok := readPrefix(path, headPrefix)
	if !ok {
		return false
	}
	if ref, ok := strings.CutPrefix(string(head), "ref:"); ok {
		return strings.HasPrefix(strings.TrimLeft(ref, " \t\n\v\f\r"), "refs/")
	}
	return hexPrefix(string(head), 40)
}

// isGitFile reports whether path is a file git would take as a .git file
// (readGitFile), wherever it is, since a .git link anywhere may point to
// it: one naming an absolute directory counts when that is a git directory;
// one naming a relative path counts as it is, since the directory that path
// is relative to is the linking .git's, unknown here.
func isGitFile(path string) bool {
	target, ok := readGitFile(path)
	if !ok {
		return false
	}
	if filepath.IsAbs(target) {
		return isGitDirectory(target)
	}
	return true
}

// readGitFile reads path as git reads a .git file: a regular file of at
// most maxGitFile bytes that starts with "gitdir: ", its trailing line
// breaks dropped, the rest a path on one line; the path is returned as
// written.
func readGitFile(path string) (string, bool) {
	b, whole, ok := readPrefix(path, maxGitFile)
	if !ok || !whole {
		return "", false
	}
	rest, ok := strings.CutPrefix(strings.TrimRight(string(b), "\r\n"), "gitdir: ")
	if !ok || rest == "" || strings.ContainsAny(rest, "\r\n") {
		return "", false
	}
	return rest, true
}

// hexPrefix reports whether s starts with n hex digits of either case.
func hexPrefix(s string, n int) bool {
	if len(s) < n {
		return false
	}
	for _, c := range s[:n] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// readPrefix reads at most n bytes from the start of path when it is a
// regular file, and nothing else: a link to a device or a FIFO is neither
// opened for long nor read, since what is in a working directory is not
// trusted. The open does not block, and the file is checked again once
// open. whole says the file has no more than what was read.
func readPrefix(path string, n int) (b []byte, whole, ok bool) {
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return nil, false, false
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false, false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, false, false
	}
	b, err = io.ReadAll(io.LimitReader(f, int64(n)+1))
	if err != nil {
		return nil, false, false
	}
	if len(b) > n {
		return b[:n], false, true
	}
	return b, true, true
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
