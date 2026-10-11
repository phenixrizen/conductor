package session

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// The Explorer's filter reaches folders not opened yet (design round 12):
// the files under the working directory whose name holds the query, found
// by a bounded walk under the same deny list as a read.
const (
	MaxFindMatches = 200
	MaxFindScan    = 20000
	MaxFindQuery   = 200
	findTimeout    = 3 * time.Second
)

// errFindStop ends the walk early.
var errFindStop = errors.New("stop")

// FindPath walks root for files whose name holds query (case-insensitive),
// skipping .git, node_modules and what the deny list refuses, and answers a
// FILE header of kind `find`: Path the root, Matches the paths from it,
// Truncated when the walk stopped at a bound (200 matches, 20,000 entries,
// 3 s, the header's size). root is opened as a folder for this one find; a
// session finds through the folder it holds.
func FindPath(root, query string, deny []string) proto.FileHeader {
	f, err := OpenFolder(root)
	if err != nil {
		return deniedHeader(root, errFolderGone)
	}
	defer f.Close()
	return f.find(query, deny)
}

// find is FindPath in the folder. The walk goes through the folder's
// handles as a read does: each folder it goes into is opened through the
// one above it (stepDir), never through a link, and held to the deny list
// by identity; a file that matches is looked at through its folder's handle
// for the deny list too. Folders are walked in name order, as
// filepath.WalkDir walks them, with one of them open at a time: a folder is
// closed while one inside it is walked and opened again from the top after
// (openDir), so a deep tree costs time, which the bounds limit, not open
// files.
func (f *Folder) find(query string, deny []string) proto.FileHeader {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || len(q) > MaxFindQuery {
		return proto.FileHeader{Path: f.Path(), Kind: "error", Error: &proto.ErrorInfo{Code: "bad_request", Message: "a find needs its words, at most 200 bytes"}}
	}
	// The deny list as looked up once, for the whole walk.
	ds := newDenySet(deny)
	base, _, err := f.resolve(".", ds)
	if err != nil {
		return deniedHeader(f.Path(), err)
	}
	h := proto.FileHeader{Path: base, Kind: "find", Exists: true}
	w := finder{f: f, deny: ds, q: q, base: base, h: &h, deadline: time.Now().Add(findTimeout), budget: proto.MaxFileHeader - 4096}
	dir, id, err := f.openDir(".", w.deny)
	if err != nil {
		return deniedHeader(f.Path(), err)
	}
	w.walk(".", dir, id)
	return h
}

// finder is one find's walk and what it found so far.
type finder struct {
	f        *Folder
	deny     denySet
	q        string
	base     string
	h        *proto.FileHeader
	deadline time.Time
	budget   int
	scanned  int
	used     int
}

// walk reads the folder rel, open as dir (id being dir as a file), which
// it closes, and goes down into the folders in it. It returns errFindStop
// once a bound is reached.
func (w *finder) walk(rel string, dir *os.Root, id os.FileInfo) error {
	defer func() {
		if dir != nil {
			dir.Close()
		}
	}()
	entries, err := readEntries(dir, MaxFindScan-w.scanned+1)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		w.scanned++
		if w.scanned > MaxFindScan || time.Now().After(w.deadline) {
			w.h.Truncated = true
			return errFindStop
		}
		name := e.Name()
		p := path.Join(filepath.ToSlash(rel), name)
		if e.IsDir() {
			if name == ".git" || name == "node_modules" {
				continue
			}
			afterCheck()
			child, cid, err := stepDir(dir, id, name, w.deny)
			if err != nil {
				// Denied, gone, or changed since the listing: not walked.
				continue
			}
			dir.Close()
			dir = nil
			if err := w.walk(p, child, cid); err != nil {
				return err
			}
			again, aid, err := w.f.openDir(filepath.FromSlash(rel), w.deny)
			if err != nil || !os.SameFile(id, aid) {
				// The folder changed while one inside it was walked.
				if again != nil {
					again.Close()
				}
				w.h.Truncated = true
				return nil
			}
			dir = again
			continue
		}
		if !strings.Contains(strings.ToLower(name), w.q) {
			continue
		}
		fi, err := dir.Lstat(name)
		if err != nil || w.deny.hit(fi, name, id) {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 && w.deny.inside(filepath.Join(w.base, filepath.FromSlash(p))) {
			continue
		}
		if w.used+len(p)+8 > w.budget || len(w.h.Matches) == MaxFindMatches {
			w.h.Truncated = true
			return errFindStop
		}
		w.used += len(p) + 8
		w.h.Matches = append(w.h.Matches, p)
	}
	return nil
}

// readEntries reads at most n entries of dir, open.
func readEntries(dir *os.Root, n int) ([]os.DirEntry, error) {
	d, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	entries, err := d.ReadDir(n)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, err
}
