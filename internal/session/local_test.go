package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// waitControl returns the first control message that sink has received, or
// receives within 3 s, for which match is true; nil on timeout. State that
// Info() already shows is not proof that its broadcast frame reached the sink:
// a subscription delivers from a queue on its own goroutine.
func waitControl(sink *chanSink, match func(m map[string]any) bool) map[string]any {
	deadline := time.After(3 * time.Second)
	for i := 0; ; {
		for ; i < sink.count(); i++ {
			f, err := proto.Decode(sink.frame(i))
			if err != nil || f.Type != proto.TypeControl {
				continue
			}
			var m map[string]any
			if json.Unmarshal(f.Payload, &m) == nil && match(m) {
				return m
			}
		}
		select {
		case <-sink.writeCh:
		case <-deadline:
			return nil
		}
	}
}

func newLocal(t *testing.T, cwd string) (*Local, *fakeProc) {
	t.Helper()
	p := newFakeProc()
	s := NewLocal(Info{ID: "sess", Cwd: cwd, Cols: 80, Rows: 24}, p, Options{ScrollbackBytes: 4096})
	t.Cleanup(func() { p.exit() })
	return s, p
}

// newLocalWith is newLocal with the caller's Options and a temporary
// working directory.
func newLocalWith(t *testing.T, opts Options) (*Local, *fakeProc) {
	t.Helper()
	p := newFakeProc()
	s := NewLocal(Info{ID: "sess", Cwd: t.TempDir(), Cols: 80, Rows: 24}, p, opts)
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
	// viewers broadcast and the join activity entry reach the subscriber too;
	// live output must follow them, never precede the ready marker.
	sink.waitFrames(t, 5)
	n := sink.count()
	p.outW.Write([]byte("live"))
	sink.waitFrames(t, n+1)
	var live bool
	for i := 3; i < sink.count(); i++ {
		if f, _ := proto.Decode(sink.frame(i)); f.Type == proto.TypeOutput && string(f.Payload) == "live" {
			live = true
		}
	}
	if !live {
		t.Fatalf("live output not received after ready (%d frames)", sink.count())
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

func TestSessionEndedLogShowsTheExitCode(t *testing.T) {
	t.Run("exited", func(t *testing.T) {
		logs := &syncBuffer{}
		status := make(chan struct{}, 1)
		_, p := newLocalWith(t, Options{
			Log: slog.New(slog.NewTextHandler(logs, nil)),
			// The status row is recorded after the log line, in the same goroutine.
			OnActivity: func(_ string, e ActivityEntry) {
				if e.Type == ActivityStatus {
					status <- struct{}{}
				}
			},
		})
		p.exit()
		select {
		case <-status:
		case <-time.After(3 * time.Second):
			t.Fatal("session did not end")
		}
		if line := logs.String(); !strings.Contains(line, "status=exited") || !strings.Contains(line, "exitCode=7") {
			t.Fatalf("want the exit code in the log line, got %q", line)
		}
	})
	t.Run("no exit status", func(t *testing.T) {
		logs := &syncBuffer{}
		s, _ := newLocalWith(t, Options{Log: slog.New(slog.NewTextHandler(logs, nil))})
		s.markEnded(StatusStopped) // ended before the process reported an exit status
		if line := logs.String(); !strings.Contains(line, "status=stopped") || strings.Contains(line, "exitCode") {
			t.Fatalf("want no exitCode in the log line, got %q", line)
		}
	})
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
	// Presence and activity frames may interleave; find the file frame.
	var f proto.Frame
	deadline := time.Now().Add(2 * time.Second)
	for f.Type != proto.TypeFile && time.Now().Before(deadline) {
		sink.waitFrames(t, before+1)
		for i := before; i < sink.count(); i++ {
			if fr, _ := proto.Decode(sink.frame(i)); fr.Type == proto.TypeFile {
				f = fr
			}
		}
		before = sink.count()
	}
	if f.Type != proto.TypeFile {
		t.Fatalf("expected a file frame, got none")
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
	// The broadcast reaches the viewer after Info() already shows the state,
	// so wait for the frame instead of scanning what has arrived so far.
	if waitControl(viewer, func(m map[string]any) bool {
		return m["t"] == proto.CtlAttention && m["state"] == "needs_input"
	}) == nil {
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
	// The current attention is the last thing a late attach queues, behind the
	// scrollback and the activity replay.
	if waitControl(late, func(m map[string]any) bool {
		return m["t"] == proto.CtlAttention && m["message"] == "again"
	}) == nil {
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

// rosterOf returns the list from the last viewers message a sink received.
func rosterOf(t *testing.T, sink *chanSink) (map[string]any, []map[string]any) {
	t.Helper()
	var last map[string]any
	for i := 0; i < sink.count(); i++ {
		f, err := proto.Decode(sink.frame(i))
		if err != nil || f.Type != proto.TypeControl {
			continue
		}
		var m map[string]any
		if json.Unmarshal(f.Payload, &m) == nil && m["t"] == proto.CtlViewers {
			last = m
		}
	}
	if last == nil {
		t.Fatal("no viewers message received")
	}
	raw, _ := last["list"].([]any)
	list := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		list = append(list, v.(map[string]any))
	}
	return last, list
}

func TestViewersRosterCarriesNamesAndTyping(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	a, b := newChanSink(false), newChanSink(false)
	subA, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Priya", LinkLabel: "pairing", Cols: 80, Rows: 24}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Cols: 80, Rows: 24}, b); err != nil {
		t.Fatal(err)
	}
	// B receives welcome, ready, A's replayed join, the roster and its own
	// join, one at a time from its queue: wait for the roster itself.
	if waitControl(b, func(m map[string]any) bool { return m["t"] == proto.CtlViewers && m["count"] == float64(2) }) == nil {
		t.Fatal("no viewers message listing both clients received")
	}
	msg, list := rosterOf(t, b)
	if msg["count"].(float64) != 2 || len(list) != 2 {
		t.Fatalf("roster: %v", msg)
	}
	byName := map[string]map[string]any{}
	for _, v := range list {
		byName[v["name"].(string)] = v
	}
	if byName["Priya"] == nil || byName["guest"] == nil {
		t.Fatalf("names: %v", list)
	}
	if byName["Priya"]["role"] != "control" || byName["Priya"]["link"] != "pairing" || byName["Priya"]["since"] == nil {
		t.Fatalf("Priya entry: %v", byName["Priya"])
	}
	if byName["guest"]["link"] != nil {
		t.Fatalf("guest should have no link label: %v", byName["guest"])
	}
	// Typing: input from A stamps lastInputAt and rebroadcasts the roster. Wait
	// for that roster, not for one more frame: B's own join may still be queued.
	if err := s.Input(subA, []byte("x")); err != nil {
		t.Fatal(err)
	}
	typed := waitControl(b, func(m map[string]any) bool {
		if m["t"] != proto.CtlViewers {
			return false
		}
		raw, _ := m["list"].([]any)
		for _, v := range raw {
			if e, ok := v.(map[string]any); ok && e["name"] == "Priya" && e["lastInputAt"] != nil {
				return true
			}
		}
		return false
	})
	if typed == nil {
		_, list = rosterOf(t, b)
		t.Fatalf("expected lastInputAt on Priya after input: %v", list)
	}
}

func TestAttachRejectsOverlongName(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	sink := newChanSink(false)
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: strings.Repeat("n", 500), Cols: 80, Rows: 24}, sink); err != nil {
		t.Fatalf("attach should clean rather than reject: %v", err)
	}
	sink.waitFrames(t, 3)
	_, list := rosterOf(t, sink)
	if got := list[0]["name"].(string); len([]rune(got)) != 40 {
		t.Fatalf("name not capped: %d runes", len([]rune(got)))
	}
}

func TestSetAttentionFullBroadcastsOptionsAndInputClears(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	sink := newChanSink(false)
	sub, _ := s.Attach("", RoleControl, "", 80, 24, sink)
	sink.waitFrames(t, 3)
	n := sink.count()
	s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, "permission", []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "3"}})
	var m map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for m == nil && time.Now().Before(deadline) {
		sink.waitFrames(t, n+1)
		for i := n; i < sink.count(); i++ {
			if c := decodeControl(t, sink.frame(i)); c["t"] == proto.CtlAttention {
				m = c
			}
		}
		n = sink.count()
	}
	if m == nil || m["kind"] != "permission" || len(m["options"].([]any)) != 2 {
		t.Fatalf("attention: %v", m)
	}
	if got := s.Info().Attention; got.Kind != "permission" || len(got.Options) != 2 || got.Options[1].Input != "3" {
		t.Fatalf("info: %+v", got)
	}
	_ = s.Input(sub, []byte("1"))
	deadline = time.Now().Add(time.Second)
	for s.Info().Attention.State != AttentionNone && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a := s.Info().Attention; a.State != AttentionNone || a.Kind != "" || a.Options != nil {
		t.Fatalf("not cleared: %+v", a)
	}
}

func TestCleanOptionsBounds(t *testing.T) {
	in := make([]Option, 10)
	for i := range in {
		in[i] = Option{Label: strings.Repeat("l", 100), Input: strings.Repeat("i", 40)}
	}
	out := CleanOptions(in)
	if len(out) != MaxAttentionOptions || len([]rune(out[0].Label)) != MaxOptionLabel || len(out[0].Input) != MaxOptionInput {
		t.Fatalf("%d options, label %d, input %d", len(out), len([]rune(out[0].Label)), len(out[0].Input))
	}
	if CleanOptions([]Option{{Label: "", Input: "1"}, {Label: "x", Input: ""}}) != nil {
		t.Fatal("empty label or input must be dropped")
	}
	if CleanOptions(nil) != nil {
		t.Fatal("nil stays nil")
	}
}

func activityTypes(s *Local) map[string]int {
	out := map[string]int{}
	for _, e := range s.Activity() {
		out[e.Type]++
	}
	return out
}

func TestInputDuringNeedsInputRecordsOneAnswer(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	sink := newChanSink(false)
	sub, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Priya", Cols: 80, Rows: 24}, sink)
	s.SetAttention(AttentionNeedsInput, "Apply edit?", SourceAPI)
	_ = s.Input(sub, []byte("1"))
	_ = s.Input(sub, []byte("\r"))
	info := s.Info()
	if info.LastAnswer == nil || info.LastAnswer.ByName != "Priya" || info.LastAnswer.Message != "Apply edit?" || info.LastAnswer.At.IsZero() {
		t.Fatalf("lastAnswer: %+v", info.LastAnswer)
	}
	if got := activityTypes(s); got["input"] != 1 || got["join"] != 1 || got["attention"] < 1 {
		t.Fatalf("activity types: %v", got)
	}
}

func TestActivityBroadcastAndReplay(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	for i := 0; i < 60; i++ {
		s.Record(ActivityEntry{Type: "link", Message: "x"})
	}
	sink := newChanSink(false)
	s.Attach("", RoleView, "", 80, 24, sink)
	sink.waitFrames(t, 3+ActivityReplay)
	replayed := 0
	for i := 0; i < sink.count(); i++ {
		f, err := proto.Decode(sink.frame(i))
		if err != nil || f.Type != proto.TypeControl {
			continue
		}
		var m map[string]any
		json.Unmarshal(f.Payload, &m)
		if m["t"] == proto.CtlActivity {
			replayed++
			if m["at"] == nil || m["type"] == nil {
				t.Fatalf("activity frame missing fields: %v", m)
			}
		}
	}
	// ActivityReplay history entries plus this viewer's own join entry.
	if replayed != ActivityReplay+1 {
		t.Fatalf("replayed %d, want %d", replayed, ActivityReplay+1)
	}
	// A live entry is broadcast to attached viewers.
	before := sink.count()
	s.Record(ActivityEntry{Type: "link", Message: "live"})
	sink.waitFrames(t, before+1)
	if m := decodeControl(t, sink.frame(before)); m["t"] != proto.CtlActivity || m["message"] != "live" {
		t.Fatalf("live entry: %v", m)
	}
}

func TestRecordRateLimitsPerSession(t *testing.T) {
	var got int
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, OnActivity: func(string, ActivityEntry) { got++ }})
	accepted := 0
	for i := 0; i < 200; i++ {
		if s.Record(ActivityEntry{Type: ActivityProgress, Message: "x"}) {
			accepted++
		}
	}
	// The bucket earns tokens while the loop runs: allow 200 ms of stall.
	if accepted > EventBurst+EventRatePerSecond/5 || accepted < EventBurst/2 || s.Dropped() != uint64(200-accepted) || got != accepted {
		t.Fatalf("accepted %d dropped %d hooks %d", accepted, s.Dropped(), got)
	}
	time.Sleep(1100 * time.Millisecond)
	if !s.Record(ActivityEntry{Type: ActivityProgress}) {
		t.Fatal("bucket did not refill")
	}
}

// syncBuffer is a log destination the test can read while the session writes.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestRecordDropsAreNotStoredBroadcastOrReported(t *testing.T) {
	var hmu sync.Mutex
	hooked := map[string]bool{}
	logs := &syncBuffer{}
	s, _ := newLocalWith(t, Options{
		Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		OnActivity: func(_ string, e ActivityEntry) {
			hmu.Lock()
			hooked[e.Message] = true
			hmu.Unlock()
		},
	})
	sink := newChanSink(false)
	if _, err := s.Attach("", RoleView, "", 80, 24, sink); err != nil {
		t.Fatal(err)
	}
	dropped := map[string]bool{}
	for i := 0; i < 200; i++ {
		msg := fmt.Sprintf("p%d", i)
		if !s.Record(ActivityEntry{Type: ActivityProgress, Message: msg}) {
			dropped[msg] = true
		}
	}
	if len(dropped) < 100 || s.Dropped() != uint64(len(dropped)) {
		t.Fatalf("%d entries refused, Dropped() = %d", len(dropped), s.Dropped())
	}

	// A fresh bucket is full: this stands in for waiting a second. The sink
	// receives frames in the order they were queued, so a dropped entry that
	// had been broadcast would already be in front of the sentinel.
	s.mu.Lock()
	s.events = eventBucket{}
	s.mu.Unlock()
	if !s.Record(ActivityEntry{Type: ActivityProgress, Message: "sentinel"}) {
		t.Fatal("a fresh bucket refused an entry")
	}
	if waitControl(sink, func(m map[string]any) bool { return m["message"] == "sentinel" }) == nil {
		t.Fatal("sentinel not broadcast")
	}
	for i := 0; i < sink.count(); i++ {
		if f, err := proto.Decode(sink.frame(i)); err == nil && f.Type == proto.TypeControl {
			var m map[string]any
			if json.Unmarshal(f.Payload, &m) == nil && dropped[fmt.Sprint(m["message"])] {
				t.Fatalf("dropped entry %v was broadcast", m["message"])
			}
		}
	}
	for _, e := range s.Activity() {
		if dropped[e.Message] {
			t.Fatalf("dropped entry %q was stored", e.Message)
		}
	}
	hmu.Lock()
	for msg := range dropped {
		if hooked[msg] {
			t.Fatalf("OnActivity was called for dropped entry %q", msg)
		}
	}
	hmu.Unlock()

	// A flood must not become a log flood: the first drop and every 100th.
	if n := strings.Count(logs.String(), "event rate limit"); n != 2 {
		t.Fatalf("%d debug lines for %d drops, want 2:\n%s", n, len(dropped), logs.String())
	}
}

// drainBucket empties the session's event bucket and stops it refilling for an
// hour, so a test can rely on every bucketed entry being dropped however
// slowly it runs.
func drainBucket(s *Local) {
	s.mu.Lock()
	s.events = eventBucket{last: time.Now().Add(time.Hour)}
	s.mu.Unlock()
}

// The session and the server produce join, leave, input, link and status
// entries themselves; a chatty hook must never starve the roster rows or the
// final status row.
func TestRecordSessionEntriesBypassTheBucket(t *testing.T) {
	var hmu sync.Mutex
	hooked := map[string]bool{}
	s, _ := newLocalWith(t, Options{OnActivity: func(_ string, e ActivityEntry) {
		hmu.Lock()
		hooked[e.Message] = true
		hmu.Unlock()
	}})
	sink := newChanSink(false)
	if _, err := s.Attach("", RoleView, "", 80, 24, sink); err != nil {
		t.Fatal(err)
	}

	// They do not spend tokens: after sixty of them the bucket holds a full burst.
	for i := 0; i < 60; i++ {
		s.Record(ActivityEntry{Type: ActivityLink, Message: "warm-up"})
	}
	accepted := 0
	for i := 0; i < 200; i++ { // a chatty hook empties the bucket
		if s.Record(ActivityEntry{Type: ActivityProgress, Message: "flood"}) {
			accepted++
		}
	}
	if accepted < EventBurst {
		t.Fatalf("only %d of the first entries an agent reported were accepted: session entries spent tokens", accepted)
	}
	drainBucket(s) // and it stays empty however slowly this test runs
	dropped := s.Dropped()

	// What an agent reports is still refused, and so is a type nobody knows ...
	agent := []string{ActivityAttention, ActivityProgress, ActivityArtifact, ActivityHandoff, ActivityToolUse, ActivityToolDenied, ActivityError, "mystery"}
	for _, typ := range agent {
		if s.Record(ActivityEntry{Type: typ, Message: "agent " + typ}) {
			t.Errorf("a %s entry got past an empty bucket", typ)
		}
	}
	// ... but what the session and the server produce themselves is not.
	own := []string{ActivityStatus, ActivityJoin, ActivityLeave, ActivityInput, ActivityLink}
	for _, typ := range own {
		if !s.Record(ActivityEntry{Type: typ, Message: "own " + typ}) {
			t.Errorf("a %s entry was refused by an empty bucket", typ)
		}
	}
	if got, want := s.Dropped(), dropped+uint64(len(agent)); got != want {
		t.Errorf("Dropped() = %d, want %d: only the agent entries count", got, want)
	}

	// Frames reach the sink in the order they were queued, so once the last
	// entry has arrived every earlier one has too.
	if waitControl(sink, func(m map[string]any) bool { return m["message"] == "own link" }) == nil {
		t.Fatal("the last session entry was not broadcast")
	}
	stored := map[string]bool{}
	for _, e := range s.Activity() {
		stored[e.Message] = true
	}
	broadcast := map[string]bool{}
	for i := 0; i < sink.count(); i++ {
		if f, err := proto.Decode(sink.frame(i)); err == nil && f.Type == proto.TypeControl {
			var m map[string]any
			if json.Unmarshal(f.Payload, &m) == nil && m["t"] == proto.CtlActivity {
				broadcast[fmt.Sprint(m["message"])] = true
			}
		}
	}
	hmu.Lock()
	defer hmu.Unlock()
	for _, typ := range own {
		msg := "own " + typ
		if !stored[msg] || !broadcast[msg] || !hooked[msg] {
			t.Errorf("%s: stored %v, broadcast %v, passed to OnActivity %v; want all three", typ, stored[msg], broadcast[msg], hooked[msg])
		}
	}
	for _, typ := range agent {
		msg := "agent " + typ
		if stored[msg] || broadcast[msg] || hooked[msg] {
			t.Errorf("%s: stored %v, broadcast %v, passed to OnActivity %v; want none", typ, stored[msg], broadcast[msg], hooked[msg])
		}
	}
}

func TestOnActivityRunsAfterTheBroadcastAndOutsideTheLock(t *testing.T) {
	type seen struct {
		id        string
		entry     ActivityEntry
		stored    ActivityEntry
		info      Info
		broadcast bool
	}
	sink := newChanSink(false)
	got := make(chan seen, 1)
	var s *Local
	s, _ = newLocalWith(t, Options{OnActivity: func(id string, e ActivityEntry) {
		if e.Type != ActivityArtifact {
			return
		}
		log := s.Activity()
		got <- seen{
			id:     id,
			entry:  e,
			stored: log[len(log)-1],
			info:   s.Info(), // takes the session lock: hangs if Record still holds it
			// The viewer's frame is already queued, so it arrives without Record's help.
			broadcast: waitControl(sink, func(m map[string]any) bool { return m["type"] == ActivityArtifact }) != nil,
		}
	}})
	if _, err := s.Attach("", RoleView, "", 80, 24, sink); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		done <- s.Record(ActivityEntry{Type: ActivityArtifact, Message: "PR opened\x1b", URL: " https://example.com/pull/1 "})
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("Record refused the entry")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Record did not return: the hook ran under the session lock")
	}
	var c seen
	select {
	case c = <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("OnActivity was not called")
	}
	if c.id != "sess" || c.info.ID != "sess" {
		t.Errorf("hook session id %q (info %q), want sess", c.id, c.info.ID)
	}
	if c.entry.Message != "PR opened" || c.entry.URL != "https://example.com/pull/1" || c.entry.At.IsZero() {
		t.Errorf("hook got an entry that is not the cleaned, stamped one: %+v", c.entry)
	}
	if c.stored != c.entry {
		t.Errorf("hook entry %+v differs from the stored %+v", c.entry, c.stored)
	}
	if !c.broadcast {
		t.Error("the hook ran before the entry was broadcast")
	}
}

func TestActivityFramesCarryEventFields(t *testing.T) {
	s, _ := newLocalWith(t, Options{})
	sink := newChanSink(false)
	if _, err := s.Attach("", RoleView, "", 80, 24, sink); err != nil {
		t.Fatal(err)
	}
	for _, e := range []ActivityEntry{
		{Type: ActivityHandoff, Message: "review please", To: "Marco"},
		{Type: ActivityArtifact, URL: "https://example.com/pull/1"},
		{Type: ActivityToolDenied, Tool: "Bash"},
		{Type: ActivityProgress, Message: "3/7"},
	} {
		if !s.Record(e) {
			t.Fatalf("Record refused %+v", e)
		}
	}
	entry := func(sink *chanSink, typ string) map[string]any {
		return waitControl(sink, func(m map[string]any) bool { return m["t"] == proto.CtlActivity && m["type"] == typ })
	}
	check := func(who string, sink *chanSink) {
		if m := entry(sink, ActivityHandoff); m == nil || m["to"] != "Marco" || m["message"] != "review please" {
			t.Errorf("%s handoff: %v", who, m)
		}
		if m := entry(sink, ActivityArtifact); m == nil || m["url"] != "https://example.com/pull/1" {
			t.Errorf("%s artifact: %v", who, m)
		}
		if m := entry(sink, ActivityToolDenied); m == nil || m["tool"] != "Bash" {
			t.Errorf("%s tool_denied: %v", who, m)
		}
		// Fields an entry does not use are left out of the JSON, not sent empty.
		m := entry(sink, ActivityProgress)
		if m == nil {
			t.Fatalf("%s progress entry missing", who)
		}
		for _, k := range []string{"url", "to", "tool", "by", "byName"} {
			if _, present := m[k]; present {
				t.Errorf("%s progress entry carries %q: %v", who, k, m)
			}
		}
	}
	check("live", sink)

	late := newChanSink(false)
	if _, err := s.Attach("", RoleView, "", 80, 24, late); err != nil {
		t.Fatal(err)
	}
	check("replayed", late)
}

func TestLinkViewersCountsPerLink(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	a, b, c := newChanSink(false), newChanSink(false), newChanSink(false)
	s.Attach("", RoleView, "L1", 80, 24, a)
	s.Attach("", RoleView, "L1", 80, 24, b)
	sub, _ := s.Attach("", RoleControl, "", 80, 24, c)
	got := s.LinkViewers()
	if got["L1"] != 2 || len(got) != 1 {
		t.Fatalf("LinkViewers: %v", got)
	}
	s.Detach(sub)
	if got := s.LinkViewers(); got["L1"] != 2 {
		t.Fatalf("after detaching the owner: %v", got)
	}
}

func TestConcurrentAnswersRecordExactlyOne(t *testing.T) {
	for round := 0; round < 20; round++ {
		s, _ := newLocal(t, t.TempDir())
		a, b := newChanSink(false), newChanSink(false)
		subA, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Priya", Cols: 80, Rows: 24}, a)
		subB, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Marco", Cols: 80, Rows: 24}, b)
		s.SetAttention(AttentionNeedsInput, "Apply edit?", SourceAPI)
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})
		go func() { defer wg.Done(); <-start; _ = s.Input(subA, []byte("1")) }()
		go func() { defer wg.Done(); <-start; _ = s.Input(subB, []byte("1")) }()
		close(start)
		wg.Wait()
		if got := activityTypes(s)["input"]; got != 1 {
			t.Fatalf("round %d: %d input entries, want exactly one", round, got)
		}
		info := s.Info()
		if info.LastAnswer == nil || info.Attention.State != AttentionNone {
			t.Fatalf("round %d: lastAnswer %+v state %q", round, info.LastAnswer, info.Attention.State)
		}
		// The recorded answer names the same person as lastAnswer.
		for _, e := range s.Activity() {
			if e.Type == ActivityInput && e.ByName != info.LastAnswer.ByName {
				t.Fatalf("round %d: entry by %q but lastAnswer by %q", round, e.ByName, info.LastAnswer.ByName)
			}
		}
	}
}
