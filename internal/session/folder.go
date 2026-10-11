package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
)

// A session's folder, its working directory, is opened once (os.Root) when
// the session starts, and the Files tab's reads, stats, listings, finds and
// saves go through it. A request's path is checked as before (ResolvePath:
// its links followed, the folder, .git/objects, the deny list), then used
// one element at a time from the folder's handle: each element is looked at
// without following it and opened through the folder before it, and once
// open it must be the file the look found. The check resolved every link,
// so an element that is a link by then, or another file once open, changed
// after the check and is refused. What a request reaches is therefore in
// the folder whatever a link does meanwhile (os.Root refuses a way out of
// it in any case), is the file its check passed, and is held to the deny
// list again by identity as it is opened.
//
// The folder is the one the session started in: when its path comes to
// name another directory (the folder moved, a link put in its place), its
// files are refused rather than read from where the path leads now.

// Folder is a session's working directory, open.
type Folder struct {
	path string
	root *os.Root
	id   os.FileInfo
}

var (
	errFolderGone  = errors.New("session root unavailable")
	errFolderMoved = errors.New("the session's folder is no longer at its path")
	errPathChanged = errors.New("the path changed while it was opened; try again")
	errNotReadable = errors.New("path is not readable")
	errNotDir      = errors.New("not a directory")
)

// afterCheckHook, when set (by a test), runs once a request's path is
// checked and before it is used (afterCheck), where a test changes the
// folder as another process could meanwhile.
var afterCheckHook atomic.Pointer[func()]

func afterCheck() {
	if fn := afterCheckHook.Load(); fn != nil {
		(*fn)()
	}
}

// openDirOnly is how a directory is named to os.Root to open it: its path
// and ".", so that every element of the path is opened as a directory only
// (a FIFO put there fails at once instead of waiting for a writer).
func openDirOnly(path string) string {
	if strings.HasSuffix(path, string(filepath.Separator)) {
		return path + "."
	}
	return path + string(filepath.Separator) + "."
}

// OpenFolder opens path (relative to the process's own working directory
// when it is not absolute, as ResolvePath takes it) as a session's folder.
func OpenFolder(path string) (*Folder, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(openDirOnly(abs))
	if err != nil {
		return nil, err
	}
	return newFolder(path, r)
}

// OpenFolderIn opens the folder rel names under dir through dir (os.Root):
// what it opens is under dir whatever a link on the way does meanwhile. Its
// path is dir joined with rel.
func OpenFolderIn(dir, rel string) (*Folder, error) {
	parent, err := os.OpenRoot(openDirOnly(dir))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	r, err := parent.OpenRoot(openDirOnly(rel))
	if err != nil {
		return nil, err
	}
	return newFolder(filepath.Join(dir, rel), r)
}

func newFolder(path string, r *os.Root) (*Folder, error) {
	fi, err := r.Stat(".")
	if err != nil {
		r.Close()
		return nil, err
	}
	return &Folder{path: path, root: r, id: fi}, nil
}

// Path is the path the folder was opened at ("" for none).
func (f *Folder) Path() string {
	if f == nil {
		return ""
	}
	return f.path
}

// Close closes the folder; what is under way finishes or fails.
func (f *Folder) Close() error { return f.root.Close() }

// Dir opens the folder's directory itself, for a process to start in
// (pty.Spec.DirFile).
func (f *Folder) Dir() (*os.File, error) { return f.root.Open(".") }

// check reports whether the folder's path still names the folder, by its
// links resolved: the paths a request's check speaks of are the folder's.
func (f *Folder) check() error {
	if f == nil {
		return errFolderGone
	}
	abs, err := filepath.Abs(f.path)
	if err != nil {
		return errFolderGone
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return errFolderGone
	}
	return f.at(real)
}

// at reports whether rootReal, the folder's path with its links resolved,
// names the folder opened.
func (f *Folder) at(rootReal string) error {
	fi, err := os.Stat(rootReal)
	if err != nil {
		return errFolderGone
	}
	if !os.SameFile(fi, f.id) {
		return errFolderMoved
	}
	return nil
}

// resolve checks raw as ResolvePath does against the folder's path, and
// returns what it resolves to and the same path from the folder (no
// element of it a link when it was resolved), once that path is checked to
// still name the folder.
func (f *Folder) resolve(raw string, deny []string) (real, rel string, err error) {
	if f == nil {
		return "", "", errFolderGone
	}
	real, rootReal, err := resolvePath(f.path, raw, deny)
	if err != nil {
		return "", "", err
	}
	if err := f.at(rootReal); err != nil {
		return "", "", err
	}
	rel, err = filepath.Rel(rootReal, real)
	if err != nil {
		return "", "", errFolderGone
	}
	return real, rel, nil
}

// openDir opens the directory rel names in the folder (a path resolve
// returned, or "."), one element at a time (stepDir). The caller closes
// what it returns.
func (f *Folder) openDir(rel string, deny denySet) (*os.Root, os.FileInfo, error) {
	cur, err := f.root.OpenRoot(".")
	if err != nil {
		return nil, nil, errFolderGone
	}
	id := f.id
	if rel == "." || rel == "" {
		if fi, err := cur.Stat("."); err == nil {
			id = fi
		}
		return cur, id, nil
	}
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		next, nid, err := stepDir(cur, id, name, deny)
		cur.Close()
		if err != nil {
			return nil, nil, err
		}
		cur, id = next, nid
	}
	return cur, id, nil
}

// stepDir opens the directory name in dir (dirID being dir as a file): it
// is looked at without following it, must not be a link (a link where its
// check found none changed since) nor in the deny list, and once open it
// must be the directory looked at. The caller closes what it returns.
func stepDir(dir *os.Root, dirID os.FileInfo, name string, deny denySet) (*os.Root, os.FileInfo, error) {
	fi, err := dir.Lstat(name)
	switch {
	case err != nil:
		return nil, nil, err
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, nil, errPathChanged
	case !fi.IsDir():
		return nil, nil, errNotDir
	case deny.hit(fi, name, dirID):
		return nil, nil, errNotReadable
	}
	next, err := dir.OpenRoot(openDirOnly(name))
	if err != nil {
		return nil, nil, errPathChanged
	}
	got, err := next.Stat(".")
	if err != nil || !os.SameFile(fi, got) {
		next.Close()
		return nil, nil, errPathChanged
	}
	return next, got, nil
}

// openFileIn opens the regular file name in dir for reading, which must be
// the file fi a look at it found: one put in its place since (a link
// included) is refused. The open does not wait on a FIFO.
func openFileIn(dir *os.Root, name string, fi os.FileInfo) (*os.File, os.FileInfo, error) {
	file, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	got, err := file.Stat()
	if err != nil || !got.Mode().IsRegular() || !os.SameFile(fi, got) {
		file.Close()
		return nil, nil, errPathChanged
	}
	return file, got, nil
}

// denySet is a deny list (see insideAny) ready to compare files with: its
// directory and file entries as files, its name entries as their
// directory, as a file, and the name.
type denySet struct {
	files []os.FileInfo
	names []nameEntry
}

type nameEntry struct {
	dir  os.FileInfo
	name string
}

// newDenySet looks up deny's entries; one that does not exist, or whose
// directory does not, denies nothing and is left out.
func newDenySet(deny []string) denySet {
	var d denySet
	for _, e := range deny {
		if name, ok := nameOf(filepath.Base(e)); ok {
			if fi, err := os.Stat(filepath.Dir(e)); err == nil {
				d.names = append(d.names, nameEntry{fi, name})
			}
			continue
		}
		if fi, err := os.Stat(e); err == nil {
			d.files = append(d.files, fi)
		}
	}
	return d
}

func (d denySet) empty() bool { return len(d.files) == 0 && len(d.names) == 0 }

// hit reports whether the file fi (nil for a name nothing has yet), named
// name in the directory dir, is denied: one of the entries itself, compared
// as a file, or a name a name entry for dir matches.
func (d denySet) hit(fi os.FileInfo, name string, dir os.FileInfo) bool {
	if fi != nil {
		for _, e := range d.files {
			if os.SameFile(fi, e) {
				return true
			}
		}
	}
	if dir == nil {
		return false
	}
	for _, n := range d.names {
		if containsFold(name, n.name) && os.SameFile(dir, n.dir) {
			return true
		}
	}
	return false
}
