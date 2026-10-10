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
// it (isDotGit); the path is a file git would take as a .git file naming a
// git directory, which a .git link may point to (isGitFile); the path or one of its
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
// any other here: no rule on paths tells it apart. Nor is a link out of a
// .git that is not beside the path or one of its parents (a repository
// nested elsewhere in the folder) followed: the Files tab makes no links,
// and finding them would mean walking the folder for every check.
// Conductor's own git runs none of it (gitcli.Run).
func inGitDir(real string) bool {
	for _, el := range strings.Split(real, string(filepath.Separator)) {
		if isDotGit(el) {
			return true
		}
	}
	if isGitFile(real) {
		return true
	}
	ids := gitIDs{budget: maxScan}
	for p := real; ; p = filepath.Dir(p) {
		gitDirsIn(p, &ids)
		if ids.incomplete || filepath.Dir(p) == p {
			break
		}
	}
	if ids.incomplete {
		// What a repository on the way stands for could not be told in full
		// (an oversized commondir, more entries than are looked at): read
		// only rather than guess.
		return true
	}
	for _, m := range ids.missing {
		if sameMissing(m, real) {
			return true
		}
	}
	for p := real; ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil {
			for _, d := range ids.files {
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

// gitIDs is what a repository's metadata is, to compare a path with:
// files and directories by identity, the paths metadata links point to
// that do not exist yet (a save there would make the file), and whether
// any of it could not be told in full.
type gitIDs struct {
	files      []os.FileInfo
	missing    []string
	incomplete bool
	// scanned holds the git directories looked at already, each looked at
	// once however many .git name it; budget is how many more entries may
	// be looked at for this check (maxScan at first).
	scanned []os.FileInfo
	budget  int
}

// maxScan bounds the entries one check looks at, all git directories
// together; past it, the path is read only (incomplete).
var maxScan = 16384

// rawJoin joins name to dir as the kernel walks them, without cleaning
// the result: a link in dir followed by .. in name goes where git goes. An
// absolute name stands alone.
func rawJoin(dir, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator) + name
}

// gitDirsIn adds to ids what dir's .git stands for: itself when it is a
// directory (or a link to one); when it is a file (or a link to one), the
// file itself and the git directory its "gitdir: " line names; and for each
// git directory, the one its commondir names and the targets of its
// metadata that are links (gitDirFiles).
func gitDirsIn(dir string, ids *gitIDs) {
	dotgit := filepath.Join(dir, ".git")
	fi, err := os.Stat(dotgit)
	switch {
	case err != nil:
		return
	case fi.IsDir():
		ids.files = append(ids.files, fi)
		gitDirFiles(dotgit, ids)
		return
	case !fi.Mode().IsRegular():
		return
	}
	ids.files = append(ids.files, fi)
	gd := gitDirFromFile(dotgit, dir)
	if gd == "" {
		return
	}
	if gfi, err := os.Stat(gd); err == nil && gfi.IsDir() {
		ids.files = append(ids.files, gfi)
		gitDirFiles(gd, ids)
	}
}

// gitDirFiles adds to ids, for the git directory gitDir, the common
// directory its commondir names (git reads the configuration, refs and
// objects there, with or without a HEAD of its own) and, for both, the
// targets of their entries that are links (linkedMetadata).
func gitDirFiles(gitDir string, ids *gitIDs) {
	linkedMetadata(gitDir, ids)
	common, ok, known := readCommonDir(gitDir)
	if !known {
		ids.incomplete = true
		return
	}
	if ok {
		if cfi, err := os.Stat(common); err == nil && cfi.IsDir() {
			ids.files = append(ids.files, cfi)
			linkedMetadata(common, ids)
		}
	}
}

// readCommonDir reads gitDir's commondir as git does: its trailing line
// breaks dropped, the path up to the first NUL (spaces are part of it),
// relative to gitDir. known is false when there is one that cannot be read
// in full here (over maxGitFile, not a regular file): git may still use it.
func readCommonDir(gitDir string) (common string, ok, known bool) {
	path := rawJoin(gitDir, "commondir")
	if _, err := os.Lstat(path); err != nil {
		return "", false, true
	}
	b, whole, readable := readPrefix(path, maxGitFile)
	if !readable || !whole {
		return "", false, false
	}
	b = bytes.TrimRight(b, "\r\n")
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	if len(b) == 0 {
		return "", false, true
	}
	return rawJoin(gitDir, string(b)), true, true
}

// The entries of a git directory are looked at whole: every entry at its
// top (config, HEAD, refs, reftable, objects, hooks, ... whatever git
// keeps there), and those of info and hooks, and objects/info/alternates.
// Links deeper inside (a loose ref, an object) are not followed: only
// someone who can already write inside .git makes one, and none of them is
// a program git runs. More entries than maxEntries in one of these
// folders leaves the repository not told in full.
const maxEntries = 4096

// linkedMetadata adds to ids what the entries of gitDir that are links
// point to: a working-tree file a .git/config link names is the
// repository's configuration all the same, a folder .git/refs or
// .git/reftable links to holds its refs, and a link to nothing yet names
// the file a save would make (linkDestination).
func linkedMetadata(gitDir string, ids *gitIDs) {
	if ids.incomplete {
		return
	}
	if fi, err := os.Stat(gitDir); err == nil {
		for _, s := range ids.scanned {
			if os.SameFile(fi, s) {
				return
			}
		}
		ids.scanned = append(ids.scanned, fi)
	}
	add := func(p string) {
		fi, err := os.Lstat(p)
		if err != nil {
			return
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			// The entry itself: a hard link to it elsewhere is the same file.
			ids.files = append(ids.files, fi)
			return
		}
		if target, err := os.Stat(p); err == nil {
			ids.files = append(ids.files, target)
		} else if dest, ok := linkDestination(p); ok {
			ids.missing = append(ids.missing, dest)
		} else {
			ids.incomplete = true
		}
	}
	for _, dir := range []string{gitDir, rawJoin(gitDir, "info"), rawJoin(gitDir, "hooks")} {
		names, complete := dirNames(dir, min(maxEntries, ids.budget))
		ids.budget -= len(names)
		if !complete || ids.budget <= 0 {
			ids.incomplete = true
			return
		}
		for _, n := range names {
			add(rawJoin(dir, n))
		}
	}
	add(rawJoin(gitDir, "objects/info/alternates"))
}

// sameMissing reports whether real, a path a save would make, may be the
// file at missing, a metadata link's destination that does not exist yet:
// the same directory, compared as a file, and the same name as a file
// system that folds case or leaves characters out may take it.
func sameMissing(missing, real string) bool {
	if !strings.EqualFold(foldName(filepath.Base(missing)), foldName(filepath.Base(real))) {
		return false
	}
	a, err := os.Stat(filepath.Dir(missing))
	if err != nil {
		return false
	}
	b, err := os.Stat(filepath.Dir(real))
	return err == nil && os.SameFile(a, b)
}

// foldName leaves out of name the characters HFS+ ignores in names.
func foldName(name string) string {
	return strings.Map(func(r rune) rune {
		if hfsIgnored(r) {
			return -1
		}
		return r
	}, name)
}

// maxLinkHops bounds how many links linkDestination follows.
const maxLinkHops = 40

// linkDestination is the file a save would make where the link at p
// leads, when nothing is at its end: the links of the chain followed one by
// one, each target from its link's own directory, as the kernel walks
// them, and the last spelt as ResolvePath spells a path whose last element
// does not exist (its parent's links resolved, the element as written).
func linkDestination(p string) (string, bool) {
	for range maxLinkHops {
		t, err := os.Readlink(p)
		if err != nil {
			return "", false
		}
		dest := rawJoin(rawDir(p), t)
		parent, err := filepath.EvalSymlinks(rawDir(dest))
		if err != nil {
			return "", false
		}
		last := dest[strings.LastIndexByte(dest, filepath.Separator)+1:]
		if last == "" || last == "." || last == ".." {
			return "", false
		}
		next := filepath.Join(parent, last)
		fi, err := os.Lstat(next)
		if err != nil {
			return next, true
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return "", false
		}
		p = next
	}
	return "", false
}

// rawDir is the directory part of p, not cleaned: rawJoin's counterpart.
func rawDir(p string) string {
	i := strings.LastIndexByte(p, filepath.Separator)
	if i <= 0 {
		return string(filepath.Separator)
	}
	return p[:i]
}

// dirNames lists the names in path when it is a directory (none when it is
// not), at most n of them; complete is false when there were more. The
// open does not block (a FIFO there is not waited on), and what was opened
// is checked to be a directory.
func dirNames(path string, n int) (names []string, complete bool) {
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return nil, true
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.IsDir() {
		return nil, true
	}
	names, _ = f.Readdirnames(n + 1)
	if len(names) > n {
		return names[:n], false
	}
	return names, true
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
// (readGitFile) naming a git directory: an absolute path, or a relative one
// from the file's own directory, which is where git starts from when the
// file is a directory's .git. A file whose "path" leads to no git directory
// (a YAML file with a gitdir key) is an ordinary file. A pointer that a
// .git link in some other directory names, with a path relative to that
// directory, is not told apart: the Files tab makes no links, and such a
// layout is the person's own.
func isGitFile(path string) bool {
	target, ok := readGitFile(path)
	if !ok {
		return false
	}
	return isGitDirectory(rawJoin(filepath.Dir(path), target))
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
