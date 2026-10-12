package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/nvim"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/session/sessiontest"
)

// key sends keystrokes over the viewer's data channel.
func (v *submitViewer) key(data string) {
	v.t.Helper()
	if err := v.dc.Send(proto.Encode(proto.TypeInput, []byte(data))); err != nil {
		v.t.Fatal(err)
	}
}

// A viewer's keystrokes are written in the order sent, while its frames go
// on being handled.
func TestAViewersKeystrokesAreWrittenInOrder(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	v.hold()
	for _, k := range []string{"1", "2", "3"} {
		v.key(k)
	}
	for _, m := range v.sync() {
		if m["t"] == proto.CtlError {
			t.Fatalf("error %v", m)
		}
	}
	want := []string{"1", "2", "3"}
	if typed := v.typed(v.release(), len(want)); fmt.Sprint(typed) != fmt.Sprint(want) {
		t.Fatalf("typed %q, want %q", typed, want)
	}
}

// The keystrokes that wait behind the one being written are bounded per
// viewer, in frames and in bytes: past either a frame is refused with
// too_many_requests, and the connection stays.
func TestAViewersKeystrokesPastTheQueueAreRefused(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	v.hold()
	v.key("first")
	v.sync()
	for range maxQueuedKeys + 3 {
		v.key("k")
	}
	refused := 0
	for _, m := range v.sync() {
		if m["t"] == proto.CtlError {
			if !isRefusal(m) || m["requestId"] != nil {
				t.Fatalf("error %v", m)
			}
			refused++
		}
	}
	if refused != 3 {
		t.Fatalf("%d refused, want 3", refused)
	}
	if first := v.release(); first != "first" {
		t.Fatalf("typed %q", first)
	}
	v.typed("first", maxQueuedKeys+1)

	// In bytes: four whole INPUT frames fill it.
	v.hold()
	v.key("first")
	v.sync()
	big := strings.Repeat("x", proto.MaxInput)
	for range 4 {
		v.key(big)
	}
	v.key("k")
	refused = 0
	for _, m := range v.sync() {
		if isRefusal(m) {
			refused++
		}
	}
	if refused != 1 {
		t.Fatalf("%d refused past %d bytes, want 1", refused, maxQueuedKeyBytes)
	}
	v.release()
}

// Keystrokes have their time from their arrival: those the program does
// not take are refused (input_failed) as it runs out, and those that never
// began are never written once it takes input again.
func TestAViewersKeystrokesThatWaitedOutTheirTimeAreNotWritten(t *testing.T) {
	const wait = 300 * time.Millisecond
	v := newSubmitViewerWaits(t, 10*time.Millisecond, 0, wait)
	v.hold()
	for _, k := range []string{"a", "b", "c"} {
		v.key(k)
	}
	failed := 0
	v.until(func(m map[string]any) bool {
		if m["t"] == proto.CtlError {
			if m["code"] != "input_failed" {
				t.Fatalf("error %v", m)
			}
			failed++
		}
		return failed == 3
	})
	// a's write had begun: it lands once the program reads again.
	if first := v.release(); first != "a" {
		t.Fatalf("typed %q", first)
	}
	v.quiet(300 * time.Millisecond)
	v.key("d")
	if got := v.typed("", 2)[1]; got != "d" {
		t.Fatalf("typed %q", got)
	}
}

// When the viewer goes, the keystrokes that wait are dropped; the one being
// written is finished.
func TestAViewerLeavingDropsTheKeystrokesThatWait(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	v.hold()
	v.key("first")
	// Being written, no longer waiting: keys not begun when the viewer
	// goes are never written.
	v.untilBegun(1)
	v.key("waits")
	v.sync()
	v.p.close()
	if first := v.release(); first != "first" {
		t.Fatalf("typed %q", first)
	}
	v.quiet(300 * time.Millisecond)
	v.p.keys.wait()
	if v.p.keys.add(request{run: func() {}}) {
		t.Fatal("the keys queue of a viewer that left took a request")
	}
}

// A view-only viewer's keystrokes are refused on the frame loop and never
// queued.
func TestAViewOnlyViewersKeystrokesAreRefused(t *testing.T) {
	proc := sessiontest.NewFakeProc()
	local := session.NewLocal(session.Info{ID: "s", Cwd: t.TempDir(), Cols: 80, Rows: 24}, proc, session.Options{Log: discardLog})
	t.Cleanup(func() { proc.End(0) })
	a := &agent{opts: Options{}, local: local, peers: map[string]*peer{}, log: discardLog}
	p := newPeer(a, "0123456789abcdef", session.RoleView, "", "", false)
	frames := make(chan []byte, 64)
	sub, err := local.AttachWith(session.AttachOptions{ID: p.id, Role: session.RoleView, Name: "guest"}, sinkFunc(func(f []byte) { frames <- f }))
	if err != nil {
		t.Fatal(err)
	}
	p.sub = sub
	t.Cleanup(p.close)
	p.handleFrame(proto.Frame{Type: proto.TypeInput, Payload: []byte("k")})
	untilControl(t, frames, func(m map[string]any) bool { return m["t"] == proto.CtlError && m["code"] == proto.ErrCodeReadOnly })
	p.keys.mu.Lock()
	running := p.keys.running
	p.keys.mu.Unlock()
	if running {
		t.Fatal("a view-only viewer's keystrokes were queued")
	}
	select {
	case b := <-proc.Input:
		t.Fatalf("written %q", b)
	case <-time.After(100 * time.Millisecond):
	}
}

// untilControl returns the first control message on frames that want
// matches.
func untilControl(t *testing.T, frames <-chan []byte, want func(m map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case raw := <-frames:
			f, err := proto.Decode(raw)
			if err != nil || f.Type != proto.TypeControl {
				continue
			}
			var m map[string]any
			if json.Unmarshal(f.Payload, &m) == nil && want(m) {
				return m
			}
		case <-deadline:
			t.Fatal("no such control message")
		}
	}
}

// The editor's keys wait for Neovim off the frame loop, in order: a Neovim
// busy with a command holds up neither the frames after them nor, on the
// relay, the other viewers'.
// editorPeer is one of the owner's viewers of a host's session with an
// editor open on a.txt, Neovim run with initVim as its config. control
// hands the peer a control message as the frame loop does.
func editorPeer(t *testing.T, initVim string) (p *peer, frames <-chan []byte, control func(v any), id string) {
	t.Helper()
	if !nvim.Available() {
		t.Skip("nvim is not on PATH; the bridge's test needs the real Neovim")
	}
	cfg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfg, "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "nvim", "init.vim"), []byte(initVim), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proc := sessiontest.NewFakeProc()
	local := session.NewLocal(session.Info{ID: "s", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{Log: discardLog})
	t.Cleanup(func() { proc.End(0) })
	a := &agent{opts: Options{}, local: local, peers: map[string]*peer{}, log: discardLog}
	p = newPeer(a, "0123456789abcdef", session.RoleControl, "", "", true)
	ch := make(chan []byte, 4096)
	sub, err := local.AttachWith(session.AttachOptions{ID: p.id, Role: session.RoleControl, Name: "Nate", Owner: true}, sinkFunc(func(f []byte) { ch <- f }))
	if err != nil {
		t.Fatal(err)
	}
	p.sub = sub
	t.Cleanup(p.close)
	control = func(v any) { p.handleFrame(proto.Frame{Type: proto.TypeControl, Payload: mustJSON(v)}) }
	control(proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "e1", Path: "a.txt"})
	opened := untilControl(t, ch, func(m map[string]any) bool { return m["t"] == proto.CtlNvimEvent && m["reqId"] == "e1" })
	id, _ = opened["id"].(string)
	if opened["kind"] != proto.NvimOpened || id == "" {
		t.Fatalf("nvim_open answered %v", opened)
	}
	return p, ch, control, id
}

func TestTheEditorsKeysDoNotHoldTheFrameLoop(t *testing.T) {
	p, frames, control, id := editorPeer(t, "")
	start := time.Now()
	// Two seconds of a loop, not :sleep, which goes on answering requests.
	control(proto.NvimInput{T: proto.CtlNvimInput, ID: id, Keys: ":let g:t = reltime() | while reltimefloat(reltime(g:t)) <lt> 2 | endwhile<CR>", Seq: 1})
	control(proto.NvimInput{T: proto.CtlNvimInput, ID: id, Keys: "ix<Esc>", Seq: 2})
	control(proto.Ping{T: proto.CtlPing, TS: 1})
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("the frame loop waited %v for Neovim", took)
	}
	// Neovim, done with its command, takes the keys after it.
	untilControl(t, frames, func(m map[string]any) bool {
		if m["t"] != proto.CtlNvimEvent || m["kind"] != proto.NvimLines {
			return false
		}
		lines, _ := m["lines"].([]any)
		return len(lines) > 0 && lines[0] == "xone"
	})
	// Closed, and gone, before the test's directories are.
	control(proto.NvimClose{T: proto.CtlNvimClose, ID: id, Discard: true})
	p.editing.wait()
}

// A viewer's keystrokes are handed on from the frame loop, which for the
// relay carries every relayed viewer's frames, without waiting for the
// program to take them.
func TestTheFrameLoopDoesNotWaitForTheProgramToTakeInput(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	v.hold()
	done := make(chan struct{})
	go func() {
		v.p.handleFrame(proto.Frame{Type: proto.TypeInput, Payload: []byte("k")})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the frame loop waited for the program to take a keystroke")
	}
	if got := v.release(); got != "k" {
		t.Fatalf("typed %q", got)
	}
}

// pongWithin reads v's frames until the pong of ts, failing when it has not
// come within d.
func pongWithin(t *testing.T, v *viewer, ts int64, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for {
		_, data, err := v.c.Read(ctx)
		if err != nil {
			t.Fatalf("no pong within %v: %v", d, err)
		}
		f, err := proto.Decode(data)
		if err != nil || f.Type != proto.TypeControl {
			continue
		}
		var m map[string]any
		_ = json.Unmarshal(f.Payload, &m)
		if m["t"] == proto.CtlPong && m["ts"] == float64(ts) {
			return
		}
	}
}

// relayControllerSeeing is relayController that returns once the session's
// output, its scrollback included, has shown needle.
func relayControllerSeeing(t *testing.T, base, sessionID, needle string) *viewer {
	t.Helper()
	v := dialViewer(t, base, sessionID, adminToken)
	v.expectJSON(proto.TypeControl, proto.CtlWelcome)
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.RelayRequest{T: proto.SigRelay, Reason: "forced"})
	v.send(relay)
	v.expectJSON(proto.TypeSignal, proto.SigRelayOK)
	v.send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 90, Rows: 25}))
	var acc []byte
	for ready := false; !ready || !bytes.Contains(acc, []byte(needle)); {
		f, err := v.read()
		if err != nil {
			t.Fatalf("waiting for %q: %v (have %q)", needle, err, acc)
		}
		switch f.Type {
		case proto.TypeOutput, proto.TypeScrollback:
			acc = append(acc, f.Payload...)
		case proto.TypeControl:
			var m map[string]any
			_ = json.Unmarshal(f.Payload, &m)
			ready = ready || m["t"] == proto.CtlReady
		}
	}
	return v
}

// A relayed controller types into a program that has stopped reading its
// terminal: another relayed viewer of the session is still served.
func TestARelayedViewerIsServedWhileAnotherTypesIntoAProgramThatTakesNoInput(t *testing.T) {
	base, id := relayHostOf(t, []string{"/bin/sh", "-c", "stty raw -echo; echo ready; exec sleep 600"})
	typist := relayControllerSeeing(t, base, id, "ready")
	watcher := relayController(t, base, id)
	big := bytes.Repeat([]byte("x"), proto.MaxInput)
	for range 2 {
		typist.send(proto.Encode(proto.TypeInput, big))
	}
	// The keystrokes reach the host before the ping does.
	time.Sleep(300 * time.Millisecond)
	watcher.send(proto.MustControl(proto.Ping{T: proto.CtlPing, TS: 7}))
	pongWithin(t, watcher, 7, 3*time.Second)
	_ = watcher.c.Close(websocket.StatusNormalClosure, "")
}

// A viewer that leaves while its editor's close waits for Neovim (a quit
// hook that hangs) does not hold the frame loop: the close goes on apart,
// and the leave waits for no Neovim.
func TestAViewerLeavingWhileItsEditorClosesDoesNotHoldTheFrameLoop(t *testing.T) {
	p, _, control, id := editorPeer(t, "autocmd VimLeavePre * sleep 3\n")
	start := time.Now()
	control(proto.NvimClose{T: proto.CtlNvimClose, ID: id, Discard: true})
	p.close() // as a viewer_leave does, on the frame loop
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("the frame loop waited %v for Neovim", took)
	}
	p.editing.wait()
}

// The keys a viewer sent before a submission are written before it,
// though they wait for the program on a queue of their own; those sent
// after it are not held back by its pause.
func TestAViewersKeysBeforeASubmissionAreWrittenFirst(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	v.hold()
	v.key("a")
	v.key("b")
	v.send(proto.Submit{T: proto.CtlSubmit, Text: ""})
	for _, m := range v.sync() {
		if m["t"] == proto.CtlError {
			t.Fatalf("error %v", m)
		}
	}
	want := []string{"a", "b", "\r"}
	if typed := v.typed(v.release(), len(want)); fmt.Sprint(typed) != fmt.Sprint(want) {
		t.Fatalf("typed %q, want %q", typed, want)
	}
}

// A viewer that goes (a revoked link's leave among them) while its
// submission waits for the keys it sent before it, which wait behind
// another's write: neither its keys nor its submission are typed.
func TestAViewerThatGoesWhileItsKeysWaitHasNothingTyped(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	v.hold()
	other, err := v.p.a.local.AttachWith(session.AttachOptions{Role: session.RoleControl, Name: "other"}, sinkFunc(func([]byte) {}))
	if err != nil {
		t.Fatal(err)
	}
	go v.p.a.local.Input(other, []byte("other's"))
	time.Sleep(100 * time.Millisecond) // the other's write has the writer
	v.key("b's")
	v.send(proto.Submit{T: proto.CtlSubmit, Text: "line"})
	v.sync()
	v.p.close()
	if first := v.release(); first != "other's" {
		t.Fatalf("typed %q", first)
	}
	v.quiet(300 * time.Millisecond)
	v.p.typing.wait()
}

// A viewer that goes as soon as it has sent keys and a submission after
// them, before the submission's turn has looked at the keys: the
// submission, which had keys to wait for, is never typed, however soon the
// viewer goes.
func TestASubmissionWithKeysToWaitForIsDroppedHoweverSoonItsViewerGoes(t *testing.T) {
	for range 10 {
		v := newSubmitViewer(t, 10*time.Millisecond)
		v.hold()
		other, err := v.p.a.local.AttachWith(session.AttachOptions{Role: session.RoleControl, Name: "other"}, sinkFunc(func([]byte) {}))
		if err != nil {
			t.Fatal(err)
		}
		go v.p.a.local.Input(other, []byte("other's"))
		time.Sleep(50 * time.Millisecond) // the other's write has the writer
		for _, k := range []string{"b1", "b2"} {
			v.p.handleFrame(proto.Frame{Type: proto.TypeInput, Payload: []byte(k)})
		}
		v.p.handleFrame(proto.Frame{Type: proto.TypeControl, Payload: mustJSON(proto.Submit{T: proto.CtlSubmit, Text: "line"})})
		v.p.close()
		if first := v.release(); first != "other's" {
			t.Fatalf("typed %q", first)
		}
		v.quiet(100 * time.Millisecond)
		v.p.typing.wait()
	}
}

// A keystroke that answers a prompt changes the session's attention, which
// the host forwards to the server: a control connection that takes none of
// those messages holds neither the viewer's keys after it nor its
// submissions, the session's change hook being the host's own.
func TestKeysAfterAnAnswerDoNotWaitForTheControlConnection(t *testing.T) {
	proc := sessiontest.NewFakeProc()
	t.Cleanup(func() { proc.End(0) })
	out := make(chan any, 64)
	held := make(chan struct{})
	t.Cleanup(func() { close(held) })
	a := &agent{opts: Options{}, peers: map[string]*peer{}, log: discardLog, inputWait: 300 * time.Millisecond}
	a.sendHook = func(v any) {
		if _, ok := v.(proto.HostAttentionMsg); ok {
			<-held // the control connection has no turn for it
			return
		}
		out <- v
	}
	a.local = session.NewLocal(session.Info{ID: "s", Cwd: t.TempDir(), Cols: 80, Rows: 24}, proc,
		session.Options{SubmitPause: 10 * time.Millisecond, FileEdit: "off", Log: discardLog, OnChange: a.noteChange})
	p := newPeer(a, "0123456789abcdef", session.RoleControl, "", "", true)
	a.peers[p.id] = p
	if err := p.startWebRTC(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	dc, frames := loopbackViewer(t, p, out)
	v := &submitViewer{t: t, p: p, proc: proc, dc: dc, frames: frames}
	v.send(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 80, Rows: 24, Name: "Nate"})
	v.until(func(m map[string]any) bool { return m["t"] == proto.CtlReady })
	go a.local.SetAttention(session.AttentionNeedsInput, "Allow?", session.SourceAPI)
	deadline := time.Now().Add(5 * time.Second)
	for a.local.Info().Attention.State != session.AttentionNeedsInput {
		if time.Now().After(deadline) {
			t.Fatal("the session never waited")
		}
		time.Sleep(10 * time.Millisecond)
	}
	v.key("y") // answers it
	v.key("z")
	v.send(proto.Submit{T: proto.CtlSubmit, Text: "line"})
	want := []string{"y", "z", "line", "\r"}
	if typed := v.typed(written1(t, v), len(want)); fmt.Sprint(typed) != fmt.Sprint(want) {
		t.Fatalf("typed %q, want %q", typed, want)
	}
}

// written1 is the first write the viewer's process takes.
func written1(t *testing.T, v *submitViewer) string {
	t.Helper()
	select {
	case b := <-v.proc.Input:
		return string(b)
	case <-time.After(10 * time.Second):
		t.Fatal("nothing was written to the process")
	}
	return ""
}
