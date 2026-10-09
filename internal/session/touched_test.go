package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// Touched keeps one entry per file for the whole session: the latest op,
// every op seen, a count (a coalesced repeat is not a new touch), the first
// and last times; relative paths made absolute under the working directory.
func TestTouchedIndexOneEntryPerFile(t *testing.T) {
	cwd := t.TempDir()
	s, _ := newLocal(t, cwd)
	at := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	rec := func(op, path, tool string, sec int) {
		s.Record(ActivityEntry{Type: ActivityFile, Op: op, Path: path, Tool: tool, ByName: "claude", At: at.Add(time.Duration(sec) * time.Second)})
	}
	rec("read", "internal/a.go", "Read", 0)
	rec("edit", filepath.Join(cwd, "internal/a.go"), "Edit", 10)
	// The same report again (the base hook's and a tools launch's), as the
	// activity log coalesces it: the time moves, the count does not.
	rec("edit", filepath.Join(cwd, "internal/a.go"), "Edit", 11)
	rec("edit", "internal/a.go", "Edit", 30) // a later edit: a touch of its own
	rec("write", "README.md", "Write", 20)
	h := s.TouchedPath(nil)
	if h.Kind != "touched" || h.Path != cwd || h.Truncated || len(h.Touched) != 2 {
		t.Fatalf("%+v", h)
	}
	a, r := h.Touched[0], h.Touched[1]
	if a.Path != filepath.Join(cwd, "internal/a.go") || a.Op != "edit" || a.Count != 3 || strings.Join(a.Ops, ",") != "read,edit" || a.Tool != "Edit" || a.By != "claude" {
		t.Fatalf("a.go %+v", a)
	}
	if a.First != at.Format(time.RFC3339Nano) || a.Last != at.Add(30*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("a.go times %s %s", a.First, a.Last)
	}
	if r.Path != filepath.Join(cwd, "README.md") || r.Op != "write" || r.Count != 1 {
		t.Fatalf("README %+v", r)
	}
}

// Past MaxTouched files the least recently touched goes first; the deny
// list hides a file; the header's size cuts the list, Truncated then.
func TestTouchedIndexBoundsAndDeny(t *testing.T) {
	cwd := t.TempDir()
	s, _ := newLocal(t, cwd)
	at := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	s.mu.Lock()
	for i := 0; i <= MaxTouched; i++ {
		s.touched.note(filepath.Join(cwd, fmt.Sprintf("f%04d.go", i)), "read", "Read", "codex", at.Add(time.Duration(i)*time.Second), false)
	}
	n := len(s.touched.m)
	_, first := s.touched.m[filepath.Join(cwd, "f0000.go")]
	_, last := s.touched.m[filepath.Join(cwd, fmt.Sprintf("f%04d.go", MaxTouched))]
	s.mu.Unlock()
	if n != MaxTouched || first || !last {
		t.Fatalf("kept %d, the oldest kept %v, the newest kept %v", n, first, last)
	}
	h := s.TouchedPath(nil)
	if !h.Truncated || len(h.Touched) == 0 || len(h.Touched) >= MaxTouched {
		t.Fatalf("a full index must be cut by the header's size: %d, truncated %v", len(h.Touched), h.Truncated)
	}
	if _, err := proto.EncodeFile(h, nil); err != nil {
		t.Fatalf("the reply does not encode: %v", err)
	}
	if h.Touched[0].Path != filepath.Join(cwd, fmt.Sprintf("f%04d.go", MaxTouched)) {
		t.Fatalf("newest first: %s", h.Touched[0].Path)
	}

	d, _ := newLocal(t, cwd)
	d.Record(ActivityEntry{Type: ActivityFile, Op: "read", Path: "secret/key.pem", At: at})
	d.Record(ActivityEntry{Type: ActivityFile, Op: "read", Path: "ok.go", At: at.Add(time.Second)})
	secret := filepath.Join(cwd, "secret")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	h = d.TouchedPath([]string{secret})
	if len(h.Touched) != 1 || h.Touched[0].Path != filepath.Join(cwd, "ok.go") {
		t.Fatalf("deny %+v", h.Touched)
	}
}

// The file request answers it under the file policy, like any read.
func TestTouchedOverAFileRequest(t *testing.T) {
	cwd := t.TempDir()
	s, _ := newLocal(t, cwd)
	s.Record(ActivityEntry{Type: ActivityFile, Op: "edit", Path: "x.go", Tool: "Edit", At: time.Now()})
	sink := newChanSink(false)
	sub, err := s.Attach("", RoleControl, "", 80, 24, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FileGet(sub, proto.FileGet{ReqID: "t1", Op: proto.FileOpTouched}); err != nil {
		t.Fatal(err)
	}
	h := fileReply(t, sink, "t1")
	if h.Kind != "touched" || len(h.Touched) != 1 || h.Touched[0].Path != filepath.Join(cwd, "x.go") {
		t.Fatalf("%+v", h)
	}
}
