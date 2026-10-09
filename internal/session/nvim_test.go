package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/nvim"
	"github.com/phenixrizen/conductor/internal/proto"
)

// nvimEvents reads the nvim_event control frames a chanSink received, from
// index *from, until one of kind `kind` arrives (or the deadline), returning
// every event seen and advancing *from past them.
func nvimEvents(t *testing.T, sink *chanSink, from *int, kind string, within time.Duration) []proto.NvimEvent {
	t.Helper()
	var seen []proto.NvimEvent
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		frames := append([][]byte(nil), sink.frames[*from:]...)
		sink.mu.Unlock()
		for _, frame := range frames {
			*from++
			f, err := proto.Decode(frame)
			if err != nil || f.Type != proto.TypeControl {
				continue
			}
			var ev proto.NvimEvent
			if json.Unmarshal(f.Payload, &ev) != nil || ev.T != proto.CtlNvimEvent {
				continue
			}
			seen = append(seen, ev)
			if ev.Kind == kind {
				return seen
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no nvim_event of kind %q; saw %+v", kind, seen)
	return nil
}

// A controller on a session that allows editing opens the real nvim on a
// file: the opened event, the whole buffer, a key's change coming back as
// lines, :w writing the file and landing in Activity as the person's file
// event; a view role, the off policy, a path outside the cwd and the bounds
// are refused with their codes; the connection's end closes the editor.
func TestNvimBridgeThroughTheSession(t *testing.T) {
	if !nvim.Available() {
		t.Skip("nvim is not on PATH; the bridge's test needs the real Neovim")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644)
	s, _ := newLocal(t, dir)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	from := 0
	if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "r1", Path: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	evs := nvimEvents(t, sink, &from, proto.NvimLines, 10*time.Second)
	if evs[0].Kind != proto.NvimOpened || evs[0].ReqID != "r1" || !strings.HasSuffix(evs[0].Path, "/a.txt") || evs[0].ID == "" {
		t.Fatalf("opened: %+v", evs[0])
	}
	id := evs[0].ID
	if last := evs[len(evs)-1]; last.First != 0 || last.Last != -1 || len(last.Lines) != 2 || last.Lines[1] != "two" {
		t.Fatalf("the whole buffer: %+v", last)
	}
	if err := s.NvimInput(sub, proto.NvimInput{T: proto.CtlNvimInput, ID: id, Keys: "dd"}); err != nil {
		t.Fatal(err)
	}
	evs = nvimEvents(t, sink, &from, proto.NvimLines, 10*time.Second)
	if last := evs[len(evs)-1]; last.First != 0 || last.Last != 1 || len(last.Lines) != 0 {
		t.Fatalf("dd: %+v", last)
	}
	if err := s.NvimInput(sub, proto.NvimInput{T: proto.CtlNvimInput, ID: id, Keys: ":w<CR>"}); err != nil {
		t.Fatal(err)
	}
	nvimEvents(t, sink, &from, proto.NvimWritten, 10*time.Second)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "two\n" {
		t.Fatalf("file after :w: %q", b)
	}
	var wrote bool
	for _, e := range s.Activity() {
		if e.Type == ActivityFile && e.Op == FileOpWrite && e.Path == "a.txt" && e.Tool == "nvim" && e.ByName == "nate" {
			wrote = true
		}
	}
	if !wrote {
		t.Fatal("the write is not in Activity as the person's file event")
	}
	// Refusals.
	if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "r2", Path: "../outside.txt"}); err != ErrFileDenied {
		t.Fatalf("outside the cwd: %v", err)
	}
	if err := s.NvimInput(sub, proto.NvimInput{T: proto.CtlNvimInput, ID: "nope", Keys: "x"}); err != ErrFileDenied {
		t.Fatalf("an unknown editor: %v", err)
	}
	if err := s.NvimInput(sub, proto.NvimInput{T: proto.CtlNvimInput, ID: id, Keys: strings.Repeat("j", proto.MaxNvimKeys+1)}); err == nil {
		t.Fatal("over-long keys accepted")
	}
	view, _ := s.AttachWith(AttachOptions{Role: RoleView, Name: "guest"}, newChanSink(false))
	if err := s.NvimOpen(context.Background(), view, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "r3", Path: "a.txt"}); err != ErrNvimUnavailable {
		t.Fatalf("a view role: %v", err)
	}
	// The bound per connection.
	if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "r4", Path: "a.txt"}); err != nil {
		t.Fatalf("second editor: %v", err)
	}
	if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "r5", Path: "a.txt"}); err != ErrTooManyRequests {
		t.Fatalf("third editor: %v", err)
	}
	// The refusal frame names the code.
	var ev proto.NvimEvent
	f, _ := proto.Decode(NvimRefused("r5", ErrTooManyRequests))
	json.Unmarshal(f.Payload, &ev)
	if ev.Kind != proto.NvimError || ev.Code != proto.ErrCodeTooManyRequests || ev.ReqID != "r5" {
		t.Fatalf("refusal: %+v", ev)
	}
	// Detach closes the editors.
	s.Detach(sub)
	sub.nvimMu.Lock()
	left := len(sub.nvims)
	sub.nvimMu.Unlock()
	if left != 0 || nvimTotal.Load() != 0 {
		t.Fatalf("editors left: %d (total %d)", left, nvimTotal.Load())
	}
}

// With FileEdit off nobody edits, whatever the role.
func TestNvimRefusedWhenEditingIsOff(t *testing.T) {
	if !nvim.Available() {
		t.Skip("nvim is not on PATH")
	}
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, FileEdit: "off"})
	os.WriteFile(filepath.Join(s.info.Cwd, "a.txt"), []byte("one\n"), 0o644)
	sub, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, newChanSink(false))
	if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "r1", Path: "a.txt"}); err != ErrNvimUnavailable {
		t.Fatalf("editing off: %v", err)
	}
}
