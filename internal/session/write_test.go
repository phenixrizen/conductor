package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// fileReply waits for the FILE frame answering reqID on sink.
func fileReply(t *testing.T, sink *chanSink, reqID string) proto.FileHeader {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		frames := append([][]byte(nil), sink.frames...)
		sink.mu.Unlock()
		for _, fr := range frames {
			f, err := proto.Decode(fr)
			if err != nil || f.Type != proto.TypeFile {
				continue
			}
			h, _, err := proto.DecodeFile(f.Payload)
			if err == nil && h.ReqID == reqID {
				return h
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no FILE reply for %s", reqID)
	return proto.FileHeader{}
}

// save sends body in parts of size n.
func save(s *Local, sub *Subscription, reqID, path string, body []byte, n int, base string, force bool) {
	for off := 0; off < len(body) || off == 0; off += n {
		end := min(off+n, len(body))
		s.FileWrite(sub, proto.FileWrite{ReqID: reqID, Path: path, Offset: int64(off), Total: int64(len(body)), BaseSha256: base, Force: force}, body[off:end])
		if end == len(body) {
			break
		}
	}
}

// A controller saves a file read before: the parts in order, the file
// written with its mode kept, the reply with the new hash, the save in
// Activity as the person's; a file changed on disk since the read is
// refused with what changed it, unless forced; the refusals have codes.
func TestFileWriteSavesAndRefuses(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	dir := s.info.Cwd
	file := filepath.Join(dir, "users.go")
	os.WriteFile(file, []byte("package api\n"), 0o640)
	read, body := ReadPath(dir, "users.go", false, nil)
	if read.Sha256 == "" || read.Mtime == "" || string(body) != "package api\n" {
		t.Fatalf("the read's hash and time: %+v", read)
	}
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	next := []byte("package api\n\n" + strings.Repeat("// a long line of a file saved in parts\n", 40))
	save(s, sub, "w1", "users.go", next, 512, read.Sha256, false)
	h := fileReply(t, sink, "w1")
	if h.Kind != "written" || h.Sha256 != sha256Hex(next) || h.Size != int64(len(next)) || h.Mtime == "" {
		t.Fatalf("written: %+v", h)
	}
	if b, _ := os.ReadFile(file); string(b) != string(next) {
		t.Fatal("the file on disk is not what was saved")
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v, want the file's own 0640", fi.Mode().Perm())
	}
	var saved bool
	for _, e := range s.Activity() {
		if e.Type == ActivityFile && e.Op == FileOpWrite && e.Path == "users.go" && e.Tool == "editor" && e.ByName == "nate" {
			saved = true
		}
	}
	if !saved {
		t.Fatal("the save is not in Activity")
	}
	// The agent edits the file; a save from the old read is refused with what changed it.
	os.WriteFile(file, []byte("package api // the agent's\n"), 0o640)
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpEdit, Path: "users.go", Tool: "Edit", ByName: "codex"})
	save(s, sub, "w2", "users.go", []byte("mine\n"), 512, h.Sha256, false)
	c := fileReply(t, sink, "w2")
	if c.Kind != "error" || c.Error.Code != "changed_on_disk" || c.By != "codex" || c.Tool != "Edit" || c.At == "" || c.Sha256 != sha256Hex([]byte("package api // the agent's\n")) {
		t.Fatalf("changed on disk: %+v %+v", c, c.Error)
	}
	if b, _ := os.ReadFile(file); string(b) != "package api // the agent's\n" {
		t.Fatal("a refused save wrote the file")
	}
	// Save anyway.
	save(s, sub, "w3", "users.go", []byte("mine\n"), 512, h.Sha256, true)
	if f := fileReply(t, sink, "w3"); f.Kind != "written" {
		t.Fatalf("forced: %+v", f)
	}
	// Refusals.
	view, _ := s.AttachWith(AttachOptions{Role: RoleView, Name: "guest"}, sink)
	s.FileWrite(view, proto.FileWrite{ReqID: "v1", Path: "users.go", Total: 1}, []byte("x"))
	if f := fileReply(t, sink, "v1"); f.Error == nil || f.Error.Code != "read_only" {
		t.Fatalf("a view role: %+v", f)
	}
	s.FileWrite(sub, proto.FileWrite{ReqID: "o1", Path: "users.go", Offset: 4, Total: 8}, []byte("abcd"))
	if f := fileReply(t, sink, "o1"); f.Error == nil || f.Error.Code != "out_of_order" {
		t.Fatalf("out of order: %+v", f)
	}
	s.FileWrite(sub, proto.FileWrite{ReqID: "t1", Path: "users.go", Total: proto.MaxFileBytes + 1}, nil)
	if f := fileReply(t, sink, "t1"); f.Error == nil || f.Error.Code != "too_large" {
		t.Fatalf("too large: %+v", f)
	}
	save(s, sub, "d1", "../escape.txt", []byte("x"), 512, "", true)
	if f := fileReply(t, sink, "d1"); f.Error == nil || f.Error.Code != "denied" {
		t.Fatalf("outside the cwd: %+v", f)
	}
	os.Remove(file)
	save(s, sub, "g1", "users.go", []byte("back\n"), 512, h.Sha256, false)
	if f := fileReply(t, sink, "g1"); f.Error == nil || f.Error.Code != "changed_on_disk" {
		t.Fatalf("deleted since: %+v", f)
	}
}

// With FileEdit off a controller saves nothing.
func TestFileWriteRefusedWhenEditingIsOff(t *testing.T) {
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, FileEdit: "off"})
	os.WriteFile(filepath.Join(s.info.Cwd, "a.txt"), []byte("a\n"), 0o644)
	sink := newChanSink(false)
	sub, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	s.FileWrite(sub, proto.FileWrite{ReqID: "w1", Path: "a.txt", Total: 2, Force: true}, []byte("b\n"))
	if f := fileReply(t, sink, "w1"); f.Error == nil || f.Error.Code != "read_only" {
		t.Fatalf("editing off: %+v", f)
	}
	if b, _ := os.ReadFile(filepath.Join(s.info.Cwd, "a.txt")); string(b) != "a\n" {
		t.Fatal("written with editing off")
	}
}
