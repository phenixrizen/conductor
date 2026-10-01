package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	// onWrite, when set, runs in Write before the bytes arrive: what a
	// process does as it is written to.
	onWrite func()
}

func newFakeProc() *fakeProc {
	r, w := io.Pipe()
	return &fakeProc{out: r, outW: w, input: make(chan []byte, 64), done: make(chan struct{}), resize: make(chan [2]uint16, 16)}
}

func (f *fakeProc) Read(b []byte) (int, error) { return f.out.Read(b) }
func (f *fakeProc) Write(b []byte) (int, error) {
	if f.onWrite != nil {
		f.onWrite()
	}
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
			OnActivity: func(_ string, e ActivityEntry, _ AttentionState) {
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

// reactingProc answers a write at once, before Write returns, the way an echo
// does.
type reactingProc struct {
	*fakeProc
	react func()
}

func (r *reactingProc) Write(b []byte) (int, error) {
	n, err := r.fakeProc.Write(b)
	r.react()
	return n, err
}

// Typing answers the prompt that was on the screen when it was typed. A process
// that reacts before the write returns, cat echoing a bell for one, may raise
// the next prompt in that time, and that one is not answered yet.
func TestInputDoesNotClearAPromptItsOwnOutputRaised(t *testing.T) {
	fp := newFakeProc()
	sink := newChanSink(false)
	reacted := false // Input, and so react, runs on this goroutine only
	p := &reactingProc{fakeProc: fp}
	p.react = func() {
		if reacted {
			return
		}
		reacted = true
		fp.outW.Write([]byte("\a")) // returns once the pump has read it
		// The prompt the bell raises reaches the viewer as a frame: the
		// session has applied it before Input goes on.
		if waitControl(sink, func(m map[string]any) bool {
			return m["t"] == proto.CtlAttention && m["state"] == string(AttentionNeedsInput)
		}) == nil {
			t.Error("the bell raised no prompt")
		}
	}
	s := NewLocal(Info{ID: "sess", Cwd: t.TempDir(), Cols: 80, Rows: 24}, p, Options{})
	t.Cleanup(fp.exit)
	sub, err := s.Attach("", RoleControl, "", 80, 24, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Input(sub, []byte("\n")); err != nil {
		t.Fatal(err)
	}
	if att := s.Info().Attention; att.State != AttentionNeedsInput || att.Source != SourceBell {
		t.Fatalf("typing cleared the prompt its own output raised: %+v", att)
	}
	// The next input does answer it.
	if err := s.Input(sub, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if att := s.Info().Attention; att.State != AttentionNone {
		t.Fatalf("the answer left %+v", att)
	}
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

// inputEntries returns the input entries of the activity log, oldest first.
func inputEntries(s *Local) []ActivityEntry {
	var out []ActivityEntry
	for _, e := range s.Activity() {
		if e.Type == ActivityInput {
			out = append(out, e)
		}
	}
	return out
}

// Type is typing with no subscription behind it, what Conductor types itself
// (a crew member's prompt): it writes the text, answers the prompt that was
// showing like a controller's input, and records an input entry by the name
// it is given, with the text, whether or not it answered anything.
func TestTypeWritesAndRecordsInput(t *testing.T) {
	var changes atomic.Int32
	s, p := newLocalWith(t, Options{OnChange: func(Info) { changes.Add(1) }})

	// Nothing is waiting: the text goes in and an entry records it.
	if err := s.Type("Own the plan for /v1/users.\r", "crew"); err != nil {
		t.Fatal(err)
	}
	if got := string(<-p.input); got != "Own the plan for /v1/users.\r" {
		t.Fatalf("wrote %q", got)
	}
	entries := inputEntries(s)
	if len(entries) != 1 || entries[0].ByName != "crew" || entries[0].By != "" || entries[0].Message != "Own the plan for /v1/users." {
		t.Fatalf("input entries %+v", entries)
	}
	if info := s.Info(); info.LastAnswer != nil || changes.Load() != 0 {
		t.Fatalf("nothing was answered, yet lastAnswer %+v and %d changes", info.LastAnswer, changes.Load())
	}

	// A prompt is showing: typing answers it, as a controller's input does.
	s.SetAttention(AttentionNeedsInput, "Apply edit?", SourceAPI)
	before := changes.Load()
	if err := s.Type("1", "crew"); err != nil {
		t.Fatal(err)
	}
	<-p.input
	info := s.Info()
	if info.Attention.State != AttentionNone || info.Attention.Source != SourceInput {
		t.Fatalf("the prompt is still showing: %+v", info.Attention)
	}
	if a := info.LastAnswer; a == nil || a.ByName != "crew" || a.By != "" || a.Message != "Apply edit?" || a.At.IsZero() {
		t.Fatalf("lastAnswer %+v", info.LastAnswer)
	}
	if changes.Load() == before {
		t.Fatal("answering the prompt did not notify OnChange")
	}
	if entries := inputEntries(s); len(entries) != 2 || entries[1].Message != "1" || entries[1].ByName != "crew" {
		t.Fatalf("input entries %+v", entries)
	}

	// Typing clears needs_input only: a finished turn stays done.
	s.SetAttention(AttentionDone, "", SourceAPI)
	if err := s.Type("x", "crew"); err != nil {
		t.Fatal(err)
	}
	<-p.input
	if state := s.Info().Attention.State; state != AttentionDone {
		t.Fatalf("typing changed done to %q", state)
	}

	// The name is a display name: cleaned like one.
	if err := s.Type("y", " cr\x1bew "); err != nil {
		t.Fatal(err)
	}
	<-p.input
	if entries := inputEntries(s); entries[len(entries)-1].ByName != "crew" {
		t.Fatalf("name %q", entries[len(entries)-1].ByName)
	}

	// An ended session takes nothing.
	p.exit()
	<-s.Ended()
	if err := s.Type("late\r", "crew"); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("typing into an ended session: %v", err)
	}
	select {
	case got := <-p.input:
		t.Fatalf("an ended session was written %q", got)
	default:
	}
}

// Type and TypeUnlessWaiting refuse text longer than an INPUT frame,
// proto.MaxInput bytes with the carriage return, before writing anything.
func TestTypeRefusesTextLongerThanAnInputFrame(t *testing.T) {
	s, p := newLocalWith(t, Options{Log: slog.New(slog.DiscardHandler)})
	long := strings.Repeat("x", proto.MaxInput) + "\r"
	if err := s.Type(long, "crew"); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("Type: %v", err)
	}
	if typed, err := s.TypeUnlessWaiting(long, "crew"); typed || !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("TypeUnlessWaiting: %v %v", typed, err)
	}
	select {
	case got := <-p.input:
		t.Fatalf("wrote %d bytes", len(got))
	default:
	}
	if entries := inputEntries(s); len(entries) != 0 {
		t.Fatalf("input entries %+v", entries)
	}
	fits := strings.Repeat("x", proto.MaxInput-1) + "\r"
	if err := s.Type(fits, "crew"); err != nil {
		t.Fatal(err)
	}
	if got := <-p.input; len(got) != proto.MaxInput {
		t.Fatalf("wrote %d bytes", len(got))
	}
}

// TypeUnlessWaiting types as Type does unless the session waits for input:
// then it writes, records and changes nothing. The look and the prompt the
// text would answer are one: a prompt raised as the text is written is not
// answered by it.
func TestTypeUnlessWaitingLeavesAPromptAlone(t *testing.T) {
	var changes atomic.Int32
	s, p := newLocalWith(t, Options{OnChange: func(Info) { changes.Add(1) }, Log: slog.New(slog.DiscardHandler)})

	if typed, err := s.TypeUnlessWaiting("Handoff from lead: go\r", "crew"); err != nil || !typed {
		t.Fatalf("not waiting: %v %v", typed, err)
	}
	if got := string(<-p.input); got != "Handoff from lead: go\r" {
		t.Fatalf("wrote %q", got)
	}
	if entries := inputEntries(s); len(entries) != 1 || entries[0].ByName != "crew" || entries[0].Message != "Handoff from lead: go" {
		t.Fatalf("input entries %+v", entries)
	}

	// Waiting: nothing happens.
	s.SetAttention(AttentionNeedsInput, "Allow edit?", SourceAPI)
	before, logged := changes.Load(), len(s.Activity())
	if typed, err := s.TypeUnlessWaiting("Handoff from lead: more\r", "crew"); err != nil || typed {
		t.Fatalf("waiting: %v %v", typed, err)
	}
	select {
	case got := <-p.input:
		t.Fatalf("wrote %q into a prompt", got)
	default:
	}
	if info := s.Info(); info.Attention.State != AttentionNeedsInput || info.LastAnswer != nil || changes.Load() != before || len(s.Activity()) != logged {
		t.Fatalf("waiting: %+v, %d changes, %d entries", info, changes.Load()-before, len(s.Activity())-logged)
	}

	// A finished turn is not waiting.
	s.SetAttention(AttentionDone, "", SourceAPI)
	if typed, err := s.TypeUnlessWaiting("x\r", "crew"); err != nil || !typed {
		t.Fatalf("done: %v %v", typed, err)
	}
	<-p.input

	// A prompt the process raises as it is written to stays up.
	p.onWrite = func() { s.SetAttention(AttentionNeedsInput, "Allow write?", SourceAPI) }
	if typed, err := s.TypeUnlessWaiting("y\r", "crew"); err != nil || !typed {
		t.Fatalf("raised during the write: %v %v", typed, err)
	}
	<-p.input
	p.onWrite = nil
	if info := s.Info(); info.Attention.State != AttentionNeedsInput || info.Attention.Message != "Allow write?" || info.LastAnswer != nil {
		t.Fatalf("the text answered a prompt raised as it was written: %+v", info)
	}

	p.exit()
	<-s.Ended()
	if typed, err := s.TypeUnlessWaiting("late\r", "crew"); typed || !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("ended: %v %v", typed, err)
	}
}

// LastOutputAt is when the process last wrote output: zero before it has.
func TestLastOutputAtFollowsTheOutput(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	if at := s.LastOutputAt(); !at.IsZero() {
		t.Fatalf("no output yet, but LastOutputAt %v", at)
	}
	waitAfter := func(after time.Time) time.Time {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if at := s.LastOutputAt(); at.After(after) {
				return at
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("LastOutputAt stayed at %v", s.LastOutputAt())
		return time.Time{}
	}
	before := time.Now()
	p.outW.Write([]byte("hello"))
	first := waitAfter(before.Add(-time.Nanosecond))
	if first.Before(before) || first.After(time.Now()) {
		t.Fatalf("LastOutputAt %v, output written after %v", first, before)
	}
	time.Sleep(20 * time.Millisecond)
	p.outW.Write([]byte(" again"))
	if second := waitAfter(first); second.Sub(first) < 20*time.Millisecond {
		t.Fatalf("second output at %v, first at %v", second, first)
	}
}
