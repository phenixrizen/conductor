package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// Saving a file from the editor (design round 12, F6): a controller on a
// session whose FileEdit allows it sends the file in parts (TypeFileWrite),
// in order, one save at a time per connection; the last part writes the
// file, unless it changed on disk since the controller read it. A save's
// next part is waited for however long it takes (a relayed save's parts can
// be slowed on their way, and the browser waits for every save's answer): an
// unfinished save goes when the connection's next save starts, or with the
// connection.

type pendingWrite struct {
	reqID string
	h     proto.FileWrite
	buf   []byte
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// FileWrite takes one part of a save for sub. Every refusal and the result
// go back to sub as a FILE frame with the save's ReqID: kind `written`, or
// `error` with a code (read_only, also for a file in a repository's .git;
// revoked or expired, sub's link no longer opening the session; denied,
// too_large, out_of_order, not_found, changed_on_disk, write). sub's
// credential (AttachOptions.Authorize) is asked at each part, after the
// role, and once more just before the save's rename (saveFile): a part it
// refuses drops the save under way, and nothing of it reaches the folder.
func (s *Local) FileWrite(sub *Subscription, h proto.FileWrite, part []byte) {
	reply := func(fh proto.FileHeader) {
		fh.ReqID = h.ReqID
		if frame, err := proto.EncodeFile(fh, nil); err == nil {
			sub.send(frame)
		}
	}
	refuse := func(code, msg string) {
		reply(proto.FileHeader{Path: h.Path, Kind: "error", Error: &proto.ErrorInfo{Code: code, Message: msg}})
	}
	if h.ReqID == "" || len(h.ReqID) > 64 || len(h.Path) > 4096 {
		refuse("bad_request", "a save needs its request id and path")
		return
	}
	if !s.editAllowed(sub.Role) {
		refuse("read_only", "this connection may not edit files here")
		return
	}
	if err := revoked(sub); err != nil {
		// The save under way goes with the link: none of its parts after
		// this one is taken as its continuation.
		sub.writeMu.Lock()
		sub.write = nil
		sub.writeMu.Unlock()
		code, msg, _ := LinkRefusal(err)
		refuse(code, msg)
		return
	}
	if h.Total < 0 || h.Total > proto.MaxFileBytes {
		refuse("too_large", "a file saved from the editor is at most 1 MiB")
		return
	}
	sub.writeMu.Lock()
	w := sub.write
	if w == nil || w.reqID != h.ReqID {
		if h.Offset != 0 {
			sub.write = nil
			sub.writeMu.Unlock()
			refuse("out_of_order", "the save's parts came out of order; save again")
			return
		}
		w = &pendingWrite{reqID: h.ReqID, h: h, buf: make([]byte, 0, h.Total)}
		sub.write = w
	}
	if h.Offset != int64(len(w.buf)) || h.Total != w.h.Total || h.Path != w.h.Path || int64(len(w.buf)+len(part)) > w.h.Total {
		sub.write = nil
		sub.writeMu.Unlock()
		refuse("out_of_order", "the save's parts came out of order; save again")
		return
	}
	w.buf = append(w.buf, part...)
	done := int64(len(w.buf)) == w.h.Total
	if done {
		sub.write = nil
	}
	sub.writeMu.Unlock()
	if !done {
		return
	}
	reply(s.saveFile(sub, w.h, w.buf))
}

// saveFile writes body over the file h names, after the checks: the path
// through ResolvePath and the deny list, not in a repository's .git
// (gitMetadata), an existing regular file, and its content unchanged since the
// read (BaseSha256) unless Force. It writes through the session's folder:
// the file's directory opened from it one element at a time as a read
// opens it (Folder.openDir), the file looked at and compared there, and the
// temporary file made and renamed over it in that directory by its handle,
// so that a save lands where its check said or nowhere. sub's credential is
// asked just before the rename: a link gone by then leaves the file as it
// was.
func (s *Local) saveFile(sub *Subscription, h proto.FileWrite, body []byte) proto.FileHeader {
	errh := func(code, msg string) proto.FileHeader {
		return proto.FileHeader{Path: h.Path, Kind: "error", Error: &proto.ErrorInfo{Code: code, Message: msg}}
	}
	// The deny list as looked up once, for the check and the use (read).
	ds := newDenySet(s.fileDeny())
	// The session's folder, or the plans folder for one of the agent's
	// plans outside it (folderFor, #289): the save goes where the check went.
	folder := s.folderFor(h.Path)
	target, rel, err := folder.resolve(h.Path, ds)
	if err != nil {
		return errh("denied", err.Error())
	}
	inGit, gitSnap := gitMetadataIDs(h.Path, target)
	if inGit {
		return errh("read_only", "a repository's .git is read only here")
	}
	// The .git check looked by path: the session's path must still name
	// the folder the save goes to.
	if err := folder.check(); err != nil {
		return errh("denied", err.Error())
	}
	afterCheck()
	deleted := func() proto.FileHeader {
		out := errh("changed_on_disk", "the file was deleted since you opened it")
		out.Path = target
		s.lastFileEvent(target, &out)
		return out
	}
	if rel == "." {
		return errh("denied", "not a regular file")
	}
	// Each directory on the way, and the file replaced, is held to the .git
	// rule again as opened.
	dir, dirID, err := folder.openDirChecked(filepath.Dir(rel), ds, refuseGit(gitSnap))
	switch {
	case errors.Is(err, errGitDir):
		return errh("read_only", "a repository's .git is read only here")
	case errors.Is(err, errPathChanged), errors.Is(err, errNotReadable), errors.Is(err, errFolderGone):
		return errh("denied", err.Error())
	case err != nil && !h.Force:
		return deleted()
	case err != nil:
		return errh("write", "cannot write beside the file")
	}
	defer dir.Close()
	name := filepath.Base(rel)
	fi, err := dir.Lstat(name)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		// A link here leads nowhere (the check resolves the others): a
		// save puts the file in its place, as a rename does.
		fi, err = nil, os.ErrNotExist
	}
	switch {
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return errh("write", "cannot read the file")
	case ds.hit(fi, name, dirID):
		return errh("denied", errNotReadable.Error())
	case err == nil && sameAsAny(fi, gitSnap.files), gitSnap.makes(dirID, name):
		// What the repository's metadata was, or a file one of its links
		// to nothing would make, moved here since the check.
		return errh("read_only", "a repository's .git is read only here")
	case err == nil && !fi.Mode().IsRegular():
		return errh("denied", "not a regular file")
	case err != nil && !h.Force:
		return deleted()
	}
	mode := os.FileMode(0o644)
	if err == nil {
		mode = fi.Mode().Perm()
		if !h.Force && h.BaseSha256 != "" {
			sha, cur, rerr := hashFileIn(dir, name, fi)
			if rerr != nil {
				return errh("write", "cannot read the file")
			}
			if sha != h.BaseSha256 {
				out := errh("changed_on_disk", "the file changed on disk since you opened it")
				out.Path, out.Sha256, out.Mtime, out.Size, out.Exists = target, sha, cur.ModTime().UTC().Format(time.RFC3339Nano), cur.Size(), true
				s.lastFileEvent(target, &out)
				return out
			}
		}
	}
	// Atomically: a temporary file beside it, then a rename over it, both
	// in the directory as opened.
	tmp, tmpName, err := createTempIn(dir, "."+name+".conductor-")
	if err != nil {
		return errh("write", "cannot write beside the file")
	}
	_, werr := tmp.Write(body)
	if werr == nil {
		werr = tmp.Chmod(mode)
	}
	cerr := tmp.Close()
	if werr == nil && cerr == nil {
		// The rename is the save's commit: sub's credential is asked once
		// more just before it, after the checks, the hash and the
		// temporary file, which a slow disk can draw out. Refused, the
		// temporary file goes and the file stays as it was.
		if err := revoked(sub); err != nil {
			dir.Remove(tmpName)
			code, msg, _ := LinkRefusal(err)
			return errh(code, msg)
		}
		werr = dir.Rename(tmpName, name)
	}
	if werr != nil || cerr != nil {
		dir.Remove(tmpName)
		return errh("write", "cannot write the file")
	}
	out := proto.FileHeader{Path: target, Kind: "written", Exists: true, Size: int64(len(body)), Sha256: sha256Hex(body)}
	if fi, err := dir.Lstat(name); err == nil {
		out.Mtime = fi.ModTime().UTC().Format(time.RFC3339Nano)
	}
	// Recorded from the working directory, as the agent names its files;
	// a plan outside it by its whole path.
	recorded := target
	if r, err := filepath.Rel(s.info.Cwd, target); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		recorded = r
	}
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: recorded, Tool: "editor", By: sub.ID, ByName: sub.Name})
	return out
}

// hashFileIn is the sha256 (hex) of the regular file name in dir, which
// must be fi, the file a look at it found, and its stat once open; "" for a
// file over MaxFileRead bytes, which no read whole (a save's base) can be.
func hashFileIn(dir *os.Root, name string, fi os.FileInfo) (string, os.FileInfo, error) {
	file, got, err := openFileIn(dir, name, fi)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	if got.Size() > MaxFileRead {
		return "", got, nil
	}
	b, err := io.ReadAll(io.LimitReader(file, MaxFileRead+1))
	if err != nil {
		return "", nil, err
	}
	if len(b) > MaxFileRead {
		return "", got, nil
	}
	return sha256Hex(b), got, nil
}

// createTempIn makes a new file in dir, open for writing, named prefix and
// a random number as os.CreateTemp names one; never through a link
// (O_EXCL), and readable by its owner alone until it is chmod'ed.
func createTempIn(dir *os.Root, prefix string) (*os.File, string, error) {
	for range 100 {
		name := prefix + strconv.FormatUint(uint64(rand.Uint32()), 10)
		f, err := dir.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, name, err
	}
	return nil, "", fs.ErrExist
}

// lastFileEvent fills out's By, Tool and At from the newest file event on
// target that changed it (an edit, a write or a delete), when there is one.
func (s *Local) lastFileEvent(target string, out *proto.FileHeader) {
	entries := s.activity.Snapshot()
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type != ActivityFile || e.Op == FileOpRead {
			continue
		}
		p := e.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.info.Cwd, p)
		}
		if filepath.Clean(p) != target {
			continue
		}
		out.By, out.Tool, out.At = e.ByName, e.Tool, e.At.UTC().Format(time.RFC3339)
		return
	}
}
