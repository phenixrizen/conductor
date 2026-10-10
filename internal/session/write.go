package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// Saving a file from the editor (design round 12, F6): a controller on a
// session whose FileEdit allows it sends the file in parts (TypeFileWrite),
// in order, one save at a time per connection; the last part writes the
// file, unless it changed on disk since the controller read it.

// writeStale drops a save whose next part has not come for this long.
const writeStale = 30 * time.Second

type pendingWrite struct {
	reqID string
	h     proto.FileWrite
	buf   []byte
	at    time.Time
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// FileWrite takes one part of a save for sub. Every refusal and the result
// go back to sub as a FILE frame with the save's ReqID: kind `written`, or
// `error` with a code (read_only, also for a file in a repository's .git;
// denied, too_large, out_of_order, not_found, changed_on_disk, write).
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
	if h.Total < 0 || h.Total > proto.MaxFileBytes {
		refuse("too_large", "a file saved from the editor is at most 1 MiB")
		return
	}
	sub.writeMu.Lock()
	w := sub.write
	if w == nil || w.reqID != h.ReqID || time.Since(w.at) > writeStale {
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
	w.at = time.Now()
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
// (inGitDir), an existing regular file, and its content unchanged since the
// read (BaseSha256) unless Force.
func (s *Local) saveFile(sub *Subscription, h proto.FileWrite, body []byte) proto.FileHeader {
	errh := func(code, msg string) proto.FileHeader {
		return proto.FileHeader{Path: h.Path, Kind: "error", Error: &proto.ErrorInfo{Code: code, Message: msg}}
	}
	target, err := ResolvePath(s.info.Cwd, h.Path, s.fileDeny())
	if err != nil {
		return errh("denied", err.Error())
	}
	if inGitDir(target) {
		return errh("read_only", "a repository's .git is read only here")
	}
	fi, err := os.Stat(target)
	switch {
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return errh("write", "cannot read the file")
	case err == nil && !fi.Mode().IsRegular():
		return errh("denied", "not a regular file")
	case err != nil && !h.Force:
		out := errh("changed_on_disk", "the file was deleted since you opened it")
		out.Path = target
		s.lastFileEvent(target, &out)
		return out
	}
	mode := os.FileMode(0o644)
	if err == nil {
		mode = fi.Mode().Perm()
		if !h.Force && h.BaseSha256 != "" {
			cur, rerr := os.ReadFile(target)
			if rerr != nil {
				return errh("write", "cannot read the file")
			}
			if sha := sha256Hex(cur); sha != h.BaseSha256 {
				out := errh("changed_on_disk", "the file changed on disk since you opened it")
				out.Path, out.Sha256, out.Mtime, out.Size, out.Exists = target, sha, fi.ModTime().UTC().Format(time.RFC3339Nano), fi.Size(), true
				s.lastFileEvent(target, &out)
				return out
			}
		}
	}
	// Atomically: a temporary file beside it, then a rename over it.
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".conductor-*")
	if err != nil {
		return errh("write", "cannot write beside the file")
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr == nil && cerr == nil {
		werr = os.Chmod(tmpName, mode)
	}
	if werr == nil && cerr == nil {
		werr = os.Rename(tmpName, target)
	}
	if werr != nil || cerr != nil {
		os.Remove(tmpName)
		return errh("write", "cannot write the file")
	}
	out := proto.FileHeader{Path: target, Kind: "written", Exists: true, Size: int64(len(body)), Sha256: sha256Hex(body)}
	if fi, err := os.Stat(target); err == nil {
		out.Mtime = fi.ModTime().UTC().Format(time.RFC3339Nano)
	}
	rel := target
	if r, err := filepath.Rel(s.info.Cwd, target); err == nil {
		rel = r
	}
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: rel, Tool: "editor", By: sub.ID, ByName: sub.Name})
	return out
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
