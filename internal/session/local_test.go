package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
)

// fakeProc is an in-memory Process: bytes written to out appear as PTY output.
type fakeProc struct {
	out    io.ReadCloser
	outW   io.WriteCloser
	input  chan []byte
	done   chan struct{}
	cols   uint16
	rows   uint16
	resize chan [2]uint16
}

func newFakeProc() *fakeProc {
	r, w := io.Pipe()
	return &fakeProc{out: r, outW: w, input: make(chan []byte, 64), done: make(chan struct{}), resize: make(chan [2]uint16, 16)}
}

func (f *fakeProc) Read(b []byte) (int, error) { return f.out.Read(b) }
func (f *fakeProc) Write(b []byte) (int, error) {
	f.input <- append([]byte(nil), b...)
	return len(b), nil
}
func (f *fakeProc) Resize(c, r uint16) error {
	f.cols, f.rows = c, r
	f.resize <- [2]uint16{c, r}
	return nil
}
func (f *fakeProc) Done() <-chan struct{} { return f.done }
func (f *fakeProc) Exit() pty.ExitStatus  { return pty.ExitStatus{Code: 7} }
func (f *fakeProc) Stop(context.Context, time.Duration) error {
	f.exit()
	return nil
}
func (f *fakeProc) exit() {
	select {
	case <-f.done:
	default:
		close(f.done)
		f.outW.Close()
	}
}

func decodeControl(t *testing.T, frame []byte) map[string]any {
	t.Helper()
	f, err := proto.Decode(frame)
	if err != nil || f.Type != proto.TypeControl {
		t.Fatalf("expected control frame, got type %d err %v", f.Type, err)
	}
	var m map[string]any
	json.Unmarshal(f.Payload, &m)
	return m
}

func newLocal(t *testing.T, cwd string) (*Local, *fakeProc) {
	t.Helper()
	p := newFakeProc()
	s := NewLocal(Info{ID: "sess", Cwd: cwd, Cols: 80, Rows: 24}, p, Options{ScrollbackBytes: 4096})
	t.Cleanup(func() { p.exit() })
	return s, p
}

func TestLateAttachReceivesScrollbackThenLive(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	p.outW.Write([]byte("early\n"))
	time.Sleep(50 * time.Millisecond)
	sink := newChanSink(false)
	sub, err := s.Attach("", RoleView, "", 80, 24, sink)
	if err != nil {
		t.Fatal(err)
	}
	sink.waitFrames(t, 3)
	if m := decodeControl(t, sink.frame(0)); m["t"] != proto.CtlWelcome || m["role"] != "view" {
		t.Fatalf("welcome: %v", m)
	}
	if f, _ := proto.Decode(sink.frame(1)); f.Type != proto.TypeScrollback || string(f.Payload) != "early\n" {
		t.Fatalf("scrollback: %+v", f)
	}
	if m := decodeControl(t, sink.frame(2)); m["t"] != proto.CtlReady {
		t.Fatalf("ready: %v", m)
	}
	// viewers broadcast reaches the subscriber too
	sink.waitFrames(t, 4)
	p.outW.Write([]byte("live"))
	sink.waitFrames(t, 5)
	if f, _ := proto.Decode(sink.frame(4)); f.Type != proto.TypeOutput || string(f.Payload) != "live" {
		t.Fatalf("live: %+v", f)
	}
	if s.Info().Viewers != 1 {
		t.Fatal("viewer count")
	}
	s.Detach(sub)
	if s.Info().Viewers != 0 {
		t.Fatal("viewer count after detach")
	}
}

func TestReadOnlyInputAndResize(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	sink := newChanSink(false)
	sub, _ := s.Attach("", RoleView, "", 80, 24, sink)
	if err := s.Input(sub, []byte("x")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("input: %v", err)
	}
	if err := s.Resize(sub, 10, 10); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("resize: %v", err)
	}
}

func TestLatestControllerWinsResize(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	a, b := newChanSink(false), newChanSink(false)
	subA, _ := s.Attach("", RoleControl, "", 100, 30, a)
	<-p.resize // attach resized to 100x30
	subB, _ := s.Attach("", RoleControl, "", 120, 40, b)
	if got := <-p.resize; got != [2]uint16{120, 40} {
		t.Fatalf("resize on attach: %v", got)
	}
	if err := s.Resize(subA, 90, 20); err != nil {
		t.Fatal(err)
	}
	if got := <-p.resize; got != [2]uint16{90, 20} {
		t.Fatalf("resize: %v", got)
	}
	if err := s.Resize(subB, 0, 20); !errors.Is(err, ErrBadDimension) {
		t.Fatalf("bad dimension: %v", err)
	}
	info := s.Info()
	if info.Cols != 90 || info.Rows != 20 {
		t.Fatalf("info size %dx%d", info.Cols, info.Rows)
	}
	// b must have observed a resize control frame attributed to subA
	deadline := time.After(2 * time.Second)
	for {
		b.mu.Lock()
		found := false
		for _, fr := range b.frames {
			f, _ := proto.Decode(fr)
			if f.Type == proto.TypeControl {
				var m map[string]any
				json.Unmarshal(f.Payload, &m)
				if m["t"] == proto.CtlResize && m["by"] == subA.ID && m["cols"] == float64(90) {
					found = true
				}
			}
		}
		b.mu.Unlock()
		if found {
			break
		}
		select {
		case <-deadline:
			t.Fatal("resize broadcast not observed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := s.Input(subB, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if got := <-p.input; string(got) != "hi" {
		t.Fatalf("input %q", got)
	}
}

func TestExitBroadcastsStatus(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	sink := newChanSink(false)
	sub, _ := s.Attach("", RoleControl, "", 80, 24, sink)
	p.exit()
	select {
	case <-s.Ended():
	case <-time.After(3 * time.Second):
		t.Fatal("session did not end")
	}
	info := s.Info()
	if info.Status != StatusExited || info.ExitCode == nil || *info.ExitCode != 7 {
		t.Fatalf("info %+v", info)
	}
	if err := s.Input(sub, []byte("x")); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("input after exit: %v", err)
	}
	// late attach still works for scrollback
	late := newChanSink(false)
	if _, err := s.Attach("", RoleView, "", 80, 24, late); err != nil {
		t.Fatal(err)
	}
	late.waitFrames(t, 1)
	if m := decodeControl(t, late.frame(0)); m["status"] != string(StatusExited) {
		t.Fatalf("welcome status %v", m)
	}
}

func TestDisconnectLink(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	a, b := newChanSink(false), newChanSink(false)
	s.Attach("", RoleView, "link-1", 80, 24, a)
	s.Attach("", RoleView, "link-2", 80, 24, b)
	s.DisconnectLink("link-1")
	select {
	case r := <-a.closed:
		if !errors.Is(r, ErrRevoked) {
			t.Fatalf("reason %v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("link-1 not disconnected")
	}
	select {
	case <-b.closed:
		t.Fatal("link-2 must stay attached")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestMaxViewers(t *testing.T) {
	p := newFakeProc()
	defer p.exit()
	s := NewLocal(Info{ID: "s"}, p, Options{MaxViewers: 1})
	if _, err := s.Attach("", RoleView, "", 0, 0, newChanSink(false)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Attach("", RoleView, "", 0, 0, newChanSink(false)); !errors.Is(err, ErrTooManyViewers) {
		t.Fatalf("expected ErrTooManyViewers, got %v", err)
	}
}

func TestFileGetPolicyAndResponse(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o600)
	s, _ := newLocal(t, dir)
	sink := newChanSink(false)
	sub, _ := s.Attach("", RoleView, "", 80, 24, sink)
	sink.waitFrames(t, 3)
	before := sink.count()
	if err := s.FileGet(sub, proto.FileGet{ReqID: "r1", Path: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	sink.waitFrames(t, before+1)
	f, _ := proto.Decode(sink.frame(before))
	if f.Type != proto.TypeFile {
		t.Fatalf("expected file frame, got %d", f.Type)
	}
	h, body, err := proto.DecodeFile(f.Payload)
	if err != nil || h.ReqID != "r1" || h.Kind != "file" || string(body) != "hello" {
		t.Fatalf("file response %+v %q %v", h, body, err)
	}
	if err := s.FileGet(sub, proto.FileGet{ReqID: "", Path: "a.txt"}); err == nil {
		t.Fatal("empty reqId must fail")
	}

	restricted := NewLocal(Info{ID: "r", Cwd: dir}, newFakeProc(), Options{FileView: "control"})
	vs := newChanSink(false)
	vsub, _ := restricted.Attach("", RoleView, "", 80, 24, vs)
	if err := restricted.FileGet(vsub, proto.FileGet{ReqID: "r2", Path: "a.txt"}); !errors.Is(err, ErrFileDenied) {
		t.Fatalf("expected denial, got %v", err)
	}
	vs.waitFrames(t, 1)
	if m := decodeControl(t, vs.frame(0)); m["fileView"] != false {
		t.Fatalf("welcome should advertise fileView=false: %v", m)
	}
}

func TestAttentionFromBellAndClearOnInput(t *testing.T) {
	var changes []Attention
	var cmu sync.Mutex
	p := newFakeProc()
	s := NewLocal(Info{ID: "att", Cwd: t.TempDir(), Cols: 80, Rows: 24}, p, Options{OnChange: func(i Info) {
		cmu.Lock()
		changes = append(changes, i.Attention)
		cmu.Unlock()
	}})
	t.Cleanup(p.exit)
	viewer := newChanSink(false)
	vsub, _ := s.Attach("", RoleView, "", 80, 24, viewer)
	ctl := newChanSink(false)
	csub, _ := s.Attach("", RoleControl, "", 80, 24, ctl)
	viewer.waitFrames(t, 3)

	p.outW.Write([]byte("prompt> \x1b]9;need approval\a"))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && s.Info().Attention.State != AttentionNeedsInput {
		time.Sleep(10 * time.Millisecond)
	}
	att := s.Info().Attention
	if att.State != AttentionNeedsInput || att.Message != "need approval" || att.Source != SourceOSC || att.Since == nil {
		t.Fatalf("attention %+v", att)
	}
	// broadcast reached the viewer
	found := false
	for i := 0; i < viewer.count(); i++ {
		if f, _ := proto.Decode(viewer.frame(i)); f.Type == proto.TypeControl {
			var m map[string]any
			json.Unmarshal(f.Payload, &m)
			if m["t"] == proto.CtlAttention && m["state"] == "needs_input" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("attention control frame not broadcast")
	}
	// view-role input is rejected and does not clear
	if err := s.Input(vsub, []byte("x")); !errors.Is(err, ErrReadOnly) || s.Info().Attention.State != AttentionNeedsInput {
		t.Fatalf("view input: %v %+v", err, s.Info().Attention)
	}
	if err := s.Input(csub, []byte("y")); err != nil {
		t.Fatal(err)
	}
	<-p.input
	if a := s.Info().Attention; a.State != AttentionNone || a.Since != nil {
		t.Fatalf("not cleared: %+v", a)
	}
	// a late attach sees the current attention immediately
	s.SetAttention(AttentionNeedsInput, "again", SourceAPI)
	late := newChanSink(false)
	s.Attach("", RoleView, "", 80, 24, late)
	late.waitFrames(t, 4)
	seen := false
	for i := 0; i < late.count(); i++ {
		if f, _ := proto.Decode(late.frame(i)); f.Type == proto.TypeControl {
			var m map[string]any
			json.Unmarshal(f.Payload, &m)
			if m["t"] == proto.CtlAttention && m["message"] == "again" {
				seen = true
			}
		}
	}
	if !seen {
		t.Fatal("late attach did not receive attention state")
	}
	cmu.Lock()
	n := len(changes)
	cmu.Unlock()
	if n == 0 {
		t.Fatal("OnChange never called")
	}
	if !s.AgentTokenOK("x") {
		s.SetAgentToken("secret")
		if !s.AgentTokenOK("secret") || s.AgentTokenOK("nope") || s.AgentTokenOK("") {
			t.Fatal("agent token check")
		}
	}
}
