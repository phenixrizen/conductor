package session

import (
	"bytes"
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
// (a .git directory, a .git file itself, the git directory it names, the
// targets of the metadata in it that are links), compared as files
// (os.SameFile) so that neither a link nor another spelling gets around it
// (gitDirsIn); or the path or one of its parents is a git directory by its
// own files (isGitDirectory), which finds a bare repository and a git
// directory a .git elsewhere points to, whatever git's own settings would
// discover. Nothing here runs git, and every file it reads is read bounded
// and without blocking (readPrefix).
//
// What a repository's configuration names outside it (an include.path
// file, a core.hooksPath folder such as .husky) is a working-tree file like
// any other here: no rule on paths tells it apart. Conductor's own git does
// not run it (gitcli.Run).
func inGitDir(real string) bool {
	for _, el := range strings.Split(real, string(filepath.Separator)) {
		if isDotGit(el) {
			return true
		}
	}
	if isGitFile(real) {
		return true
	}
	var ids []os.FileInfo
	for p := real; ; p = filepath.Dir(p) {
		ids = append(ids, gitDirsIn(p)...)
		if filepath.Dir(p) == p {
			break
		}
	}
	for p := real; ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil {
			for _, d := range ids {
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

// rawJoin joins name to dir as the kernel walks them, without cleaning
// the result: a link in dir followed by .. in name goes where git goes. An
// absolute name stands alone.
func rawJoin(dir, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator) + name
}

// gitDirsIn is what dir's .git stands for, as files to compare: itself
// when it is a directory (or a link to one); when it is a file (or a link
// to one), the file itself, the git directory its "gitdir: " line names
// and the one that directory's commondir names; and for each of those git
// directories the targets of its metadata that are links (linkedMetadata).
func gitDirsIn(dir string) []os.FileInfo {
	dotgit := filepath.Join(dir, ".git")
	fi, err := os.Stat(dotgit)
	switch {
	case err != nil:
		return nil
	case fi.IsDir():
		return append([]os.FileInfo{fi}, linkedMetadata(dotgit)...)
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
	out = append(out, linkedMetadata(gd)...)
	if b, whole, ok := readPrefix(rawJoin(gd, "commondir"), maxGitFile); ok && whole {
		common := rawJoin(gd, strings.TrimSpace(string(b)))
		if cfi, err := os.Stat(common); err == nil && cfi.IsDir() {
			out = append(out, cfi)
			out = append(out, linkedMetadata(common)...)
		}
	}
	return out
}

// metadataEntries are the entries of a git directory git reads as its own
// that may be links out of it; the hooks in hooks are looked at one by one
// too (at most maxHooks of them).
var metadataEntries = []string{"config", "config.worktree", "HEAD", "commondir", "gitdir", "packed-refs", "info", "info/attributes", "info/exclude", "hooks", "objects/info/alternates"}

const maxHooks = 256

// linkedMetadata is what the metadata entries of gitDir that are links
// point to: a working-tree file a .git/config link names is the
// repository's configuration all the same.
func linkedMetadata(gitDir string) []os.FileInfo {
	var out []os.FileInfo
	add := func(p string) {
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Stat(p); err == nil {
				out = append(out, target)
			}
		}
	}
	for _, e := range metadataEntries {
		add(rawJoin(gitDir, e))
	}
	if f, err := os.Open(rawJoin(gitDir, "hooks")); err == nil {
		names, _ := f.Readdirnames(maxHooks)
		f.Close()
		for _, n := range names {
			add(rawJoin(rawJoin(gitDir, "hooks"), n))
		}
	}
	return out
}

// isGitDirectory reports whether dir is a git directory by its files, as
// git tells one: a valid HEAD (validHead), and either the refs and objects
// directories or (a linked worktree's) a commondir file.
func isGitDirectory(dir string) bool {
	if !validHead(rawJoin(dir, "HEAD")) {
		return false
	}
	if fi, err := os.Stat(rawJoin(dir, "commondir")); err == nil && fi.Mode().IsRegular() {
		return true
	}
	for _, sub := range []string{"objects", "refs"} {
		if fi, err := os.Stat(rawJoin(dir, sub)); err != nil || !fi.IsDir() {
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
// it. One naming an absolute path counts when that is a git directory. A
// relative path is relative to the linking .git's directory, unknown here:
// it counts when it names a git directory from the file's own directory,
// or when it is a single line, as a pointer is; a file whose "path" runs
// over more lines and leads nowhere (a YAML file with a gitdir key and
// more) does not.
func isGitFile(path string) bool {
	target, ok := readGitFile(path)
	if !ok {
		return false
	}
	if filepath.IsAbs(target) {
		return isGitDirectory(target)
	}
	return isGitDirectory(rawJoin(filepath.Dir(path), target)) || !strings.ContainsAny(target, "\r\n")
}

// readGitFile reads path as git reads a .git file: a regular file of at
// most maxGitFile bytes that starts with "gitdir: "; its trailing line
// breaks dropped, something after it (git counts before it cuts), and the
// path is what comes before the first NUL, returned as written: empty
// stands for the directory of the .git that names the file, line breaks
// inside are git's to refuse or not.
func readGitFile(path string) (string, bool) {
	b, whole, ok := readPrefix(path, maxGitFile)
	if !ok || !whole {
		return "", false
	}
	rest, ok := bytes.CutPrefix(bytes.TrimRight(b, "\r\n"), []byte("gitdir: "))
	if !ok || len(rest) == 0 {
		return "", false
	}
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[:i]
	}
	return string(rest), true
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
