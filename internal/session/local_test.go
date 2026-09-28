package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	b.waitFrames(t, 3) // welcome, ready, viewers
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
	// Typing: input from A stamps lastInputAt and rebroadcasts the roster.
	before := b.count()
	if err := s.Input(subA, []byte("x")); err != nil {
		t.Fatal(err)
	}
	b.waitFrames(t, before+1)
	_, list = rosterOf(t, b)
	var typed bool
	for _, v := range list {
		if v["name"] == "Priya" && v["lastInputAt"] != nil {
			typed = true
		}
	}
	if !typed {
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
