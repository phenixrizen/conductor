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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
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
	// The buffer holds a change not written, and says so.
	if ev := nvimEvents(t, sink, &from, proto.NvimModified, 10*time.Second); !ev[len(ev)-1].Modified {
		t.Fatalf("modified after dd: %+v", ev[len(ev)-1])
	}
	if err := s.NvimInput(sub, proto.NvimInput{T: proto.CtlNvimInput, ID: id, Keys: ":w<CR>"}); err != nil {
		t.Fatal(err)
	}
	evs = nvimEvents(t, sink, &from, proto.NvimWritten, 10*time.Second)
	// Written, it no longer does (the event comes with the write, either side of it).
	if !anyModified(evs, false) {
		if ev := nvimEvents(t, sink, &from, proto.NvimModified, 10*time.Second); ev[len(ev)-1].Modified {
			t.Fatalf("modified after :w: %+v", ev[len(ev)-1])
		}
	}
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
	// Keys at machine speed, one message each and far past the chat's 20 at
	// once, all land: Neovim's keys have their own bound, sized for typing
	// (the shared one dropped every key past the 20th, seen in the Windows app).
	keys := []string{"O"}
	for i := 0; i < 60; i++ {
		keys = append(keys, "x")
	}
	keys = append(keys, "<Esc>", ":w<CR>")
	for i, k := range keys {
		if err := s.NvimInput(sub, proto.NvimInput{T: proto.CtlNvimInput, ID: id, Keys: k, Seq: uint32(100 + i)}); err != nil {
			t.Fatalf("key %d of a quick burst refused: %v", i, err)
		}
	}
	nvimEvents(t, sink, &from, proto.NvimWritten, 10*time.Second)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != strings.Repeat("x", 60)+"\ntwo\n" {
		t.Fatalf("file after a quick burst: %q", b)
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
	for i := 2; i <= proto.MaxNvimPerSub; i++ {
		if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "r4", Path: "a.txt"}); err != nil {
			t.Fatalf("editor %d: %v", i, err)
		}
	}
	if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "r5", Path: "a.txt"}); err != ErrTooManyRequests {
		t.Fatalf("editor %d: %v", proto.MaxNvimPerSub+1, err)
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
	if left != 0 || s.nvimCount.Load() != 0 {
		t.Fatalf("editors left: %d (session %d)", left, s.nvimCount.Load())
	}
}

func anyModified(evs []proto.NvimEvent, modified bool) bool {
	for _, ev := range evs {
		if ev.Kind == proto.NvimModified && ev.Modified == modified {
			return true
		}
	}
	return false
}

// Closing an editor whose buffer holds changes not written: as the
// connection's end does, its swap file stays for recovery (the next open
// offers it); with Discard, the person dropped the changes and the swap file
// goes with them. The bound is the session's own: another session's editors
// do not count against it.
func TestNvimCloseKeepsOrDiscardsTheSwapFile(t *testing.T) {
	if !nvim.Available() {
		t.Skip("nvim is not on PATH; the bridge's test needs the real Neovim")
	}
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	swaps := func() []string {
		m, _ := filepath.Glob(filepath.Join(state, "nvim", "swap", "*"))
		return m
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	s, _ := newLocal(t, dir)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Detach(sub)
	from := 0
	edit := func(req string) string {
		t.Helper()
		if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: req, Path: "a.txt"}); err != nil {
			t.Fatal(err)
		}
		id := nvimEvents(t, sink, &from, proto.NvimOpened, 10*time.Second)
		if err := s.NvimInput(sub, proto.NvimInput{T: proto.CtlNvimInput, ID: id[len(id)-1].ID, Keys: "ixx<Esc>"}); err != nil {
			t.Fatal(err)
		}
		nvimEvents(t, sink, &from, proto.NvimModified, 10*time.Second)
		return id[len(id)-1].ID
	}
	gone := func(id string) {
		t.Helper()
		for i := 0; i < 100; i++ {
			sub.nvimMu.Lock()
			_, open := sub.nvims[id]
			sub.nvimMu.Unlock()
			if !open {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("editor %s still open", id)
	}
	id := edit("kept")
	s.NvimClose(sub, proto.NvimClose{T: proto.CtlNvimClose, ID: id})
	gone(id)
	kept := swaps()
	if len(kept) != 1 {
		t.Fatalf("closed with changes not written, the swap files: %q", kept)
	}
	os.Remove(kept[0])
	id = edit("dropped")
	s.NvimClose(sub, proto.NvimClose{T: proto.CtlNvimClose, ID: id, Discard: true})
	gone(id)
	if left := swaps(); len(left) != 0 {
		t.Fatalf("discarded, the swap files: %q", left)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\n" {
		t.Fatalf("the file after discarding: %q", b)
	}
	// Neovim's own :q is let finish its exit, so its swap file goes too (it
	// was killed halfway before, leaving one for the next open to stumble on).
	if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "quit", Path: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	opened := nvimEvents(t, sink, &from, proto.NvimOpened, 10*time.Second)
	id = opened[len(opened)-1].ID
	if err := s.NvimInput(sub, proto.NvimInput{T: proto.CtlNvimInput, ID: id, Keys: ":q<CR>"}); err != nil {
		t.Fatal(err)
	}
	nvimEvents(t, sink, &from, proto.NvimClosed, 10*time.Second)
	gone(id)
	for i := 0; i < 100 && len(swaps()) > 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if left := swaps(); len(left) != 0 {
		t.Fatalf("after :q, the swap files: %q", left)
	}
	// Another session's editors leave this one's bound alone.
	other, _ := newLocal(t, dir)
	osub, err := other.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Detach(osub)
	if err := other.NvimOpen(context.Background(), osub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "o1", Path: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	if s.nvimCount.Load() != 0 || other.nvimCount.Load() != 1 {
		t.Fatalf("counts: this %d, other %d", s.nvimCount.Load(), other.nvimCount.Load())
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
