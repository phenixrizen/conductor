package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/session/sessiontest"
)

// submitViewer is a controller attached over a loopback data channel to a
// host whose session runs a FakeProc and pauses pause between a submission's
// text and its Enter. Editing is off, so an nvim_open is answered at once.
type submitViewer struct {
	t      *testing.T
	p      *peer
	proc   *sessiontest.FakeProc
	dc     *webrtc.DataChannel
	frames <-chan []byte
	ts     int64
}

func newSubmitViewer(t *testing.T, pause time.Duration) *submitViewer {
	t.Helper()
	proc := sessiontest.NewFakeProc()
	local := session.NewLocal(session.Info{ID: "s", Cwd: t.TempDir(), Cols: 80, Rows: 24}, proc, session.Options{SubmitPause: pause, FileEdit: "off", Log: discardLog})
	t.Cleanup(func() { proc.End(0) })
	out := make(chan any, 64)
	a := &agent{opts: Options{}, local: local, peers: map[string]*peer{}, log: discardLog}
	a.sendHook = func(v any) { out <- v }
	p := newPeer(a, "0123456789abcdef", session.RoleControl, "", "")
	a.peers[p.id] = p
	if err := p.startWebRTC(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	dc, frames := loopbackViewer(t, p, out)
	v := &submitViewer{t: t, p: p, proc: proc, dc: dc, frames: frames}
	v.send(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 80, Rows: 24, Name: "Nate"})
	v.until(func(m map[string]any) bool { return m["t"] == proto.CtlReady })
	return v
}

func (v *submitViewer) send(msg any) {
	v.t.Helper()
	if err := v.dc.Send(proto.MustControl(msg)); err != nil {
		v.t.Fatal(err)
	}
}

// until returns the control messages received up to and including the first
// that want matches.
func (v *submitViewer) until(want func(m map[string]any) bool) []map[string]any {
	v.t.Helper()
	var got []map[string]any
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-v.frames:
			f, err := proto.Decode(raw)
			if err != nil || f.Type != proto.TypeControl {
				continue
			}
			var m map[string]any
			if json.Unmarshal(f.Payload, &m) != nil {
				continue
			}
			got = append(got, m)
			if want(m) {
				return got
			}
		case <-deadline:
			v.t.Fatalf("no such control message; received %v", got)
		}
	}
}

// sync sends a ping and returns what came before its pong: a data channel
// delivers in order and the host handles one frame at a time, so everything
// sent before the ping has been handled, and its answers are in.
func (v *submitViewer) sync() []map[string]any {
	v.t.Helper()
	v.ts++
	ts := v.ts
	v.send(proto.Ping{T: proto.CtlPing, TS: ts})
	return v.until(func(m map[string]any) bool { return m["t"] == proto.CtlPong && m["ts"] == float64(ts) })
}

// hold fills the process's input so that the next write to it waits: a
// submission that has the session's turn keeps it until release.
func (v *submitViewer) hold() {
	for len(v.proc.Input) < cap(v.proc.Input) {
		v.proc.Input <- nil
	}
}

// release takes the fillers hold put in, and returns the first write after
// them.
func (v *submitViewer) release() string {
	v.t.Helper()
	for {
		select {
		case b := <-v.proc.Input:
			if b != nil {
				return string(b)
			}
		case <-time.After(10 * time.Second):
			v.t.Fatal("nothing was written to the process")
		}
	}
}

// typed returns the next n writes to the process.
func (v *submitViewer) typed(first string, n int) []string {
	v.t.Helper()
	got := []string{first}
	for len(got) < n {
		select {
		case b := <-v.proc.Input:
			got = append(got, string(b))
		case <-time.After(10 * time.Second):
			v.t.Fatalf("the process was written %q, want %d writes", got, n)
		}
	}
	return got
}

// quiet fails when anything more is written to the process within d.
func (v *submitViewer) quiet(d time.Duration) {
	v.t.Helper()
	select {
	case b := <-v.proc.Input:
		v.t.Fatalf("the process was written %q", b)
	case <-time.After(d):
	}
}

func isRefusal(m map[string]any) bool {
	return m["t"] == proto.CtlError && m["code"] == proto.ErrCodeTooManyRequests
}

// A viewer's submissions wait behind the one being typed in a queue of
// maxQueued; past that each is refused with too_many_requests, a chat to the
// agent and a chat_send naming their request, and the connection stays. The
// burst costs no goroutine, and what was taken is typed in the order sent.
func TestAViewersSubmissionsWaitInABoundedQueue(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	v.hold()
	v.send(proto.Submit{T: proto.CtlSubmit, Text: "first"})
	v.sync()
	before := runtime.NumGoroutine()

	const burst = 64
	for i := range burst {
		v.send(proto.Submit{T: proto.CtlSubmit, Text: fmt.Sprintf("line %d", i)})
	}
	v.send(proto.ChatSend{T: proto.CtlChatSend, Ref: "ref-1"})
	v.send(proto.ChatPost{T: proto.CtlChat, Text: "to the agent", Nonce: "n1", To: proto.ChatToAgent})
	got := v.sync()
	after := runtime.NumGoroutine()

	if after-before > 8 {
		t.Errorf("goroutines went from %d to %d over a burst of %d", before, after, burst)
	}
	var plain, chatID string
	refused := map[string]int{}
	for _, m := range got {
		if m["t"] == proto.CtlChat && m["nonce"] == "n1" {
			chatID, _ = m["id"].(string)
		}
		if m["t"] != proto.CtlError {
			continue
		}
		if !isRefusal(m) {
			t.Fatalf("error %v", m)
		}
		id, _ := m["requestId"].(string)
		if id == "" {
			plain = m["message"].(string)
		}
		refused[id]++
	}
	if refused[""] != burst-maxQueued {
		t.Fatalf("%d submits refused, want %d (%v)", refused[""], burst-maxQueued, refused)
	}
	if plain == "" || refused["ref-1"] != 1 || chatID == "" || refused[chatID] != 1 {
		t.Fatalf("refusals %v, the chat's id %q, message %q", refused, chatID, plain)
	}

	want := []string{"first", "\r"}
	for i := range maxQueued {
		want = append(want, fmt.Sprintf("line %d", i), "\r")
	}
	if typed := v.typed(v.release(), len(want)); fmt.Sprint(typed) != fmt.Sprint(want) {
		t.Fatalf("typed %q, want %q", typed, want)
	}
	v.quiet(200 * time.Millisecond)
}

// A chat to the agent and a chat_send take their turn with the submissions,
// in the order the viewer sent them.
func TestAViewersSubmissionsAndChatsAreTypedInOrder(t *testing.T) {
	v := newSubmitViewer(t, 5*time.Millisecond)
	v.send(proto.ChatPost{T: proto.CtlChat, Text: "kept", Nonce: "k"})
	kept := v.until(func(m map[string]any) bool { return m["t"] == proto.CtlChat && m["nonce"] == "k" })
	ref := kept[len(kept)-1]["id"].(string)

	v.hold()
	var want []string
	for i := range 3 {
		v.send(proto.Submit{T: proto.CtlSubmit, Text: fmt.Sprintf("submit %d", i)})
		v.send(proto.ChatPost{T: proto.CtlChat, Text: fmt.Sprintf("chat %d", i), Nonce: fmt.Sprintf("c%d", i), To: proto.ChatToAgent})
		want = append(want, fmt.Sprintf("submit %d", i), "\r", fmt.Sprintf("chat %d", i), "\r")
	}
	v.send(proto.ChatSend{T: proto.CtlChatSend, Ref: ref})
	want = append(want, "kept", "\r")
	for _, m := range v.sync() {
		if m["t"] == proto.CtlError {
			t.Fatalf("error %v", m)
		}
	}
	if typed := v.typed(v.release(), len(want)); fmt.Sprint(typed) != fmt.Sprint(want) {
		t.Fatalf("typed %q, want %q", typed, want)
	}
}

// When the viewer goes, what waits in its queue is dropped and the
// submission being typed finishes, its Enter included, as on the server: a
// reply box closes its connection once it has sent. Nothing of the viewer's
// runs after that.
func TestAViewerLeavingDropsWhatWaitsAndFinishesWhatIsTyped(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	before := runtime.NumGoroutine()
	v.hold()
	v.send(proto.Submit{T: proto.CtlSubmit, Text: "first"})
	for i := range maxQueued {
		v.send(proto.Submit{T: proto.CtlSubmit, Text: fmt.Sprintf("waits %d", i)})
	}
	v.send(proto.ChatSend{T: proto.CtlChatSend, Ref: "ref-1"})
	for _, m := range v.sync() {
		if isRefusal(m) && m["requestId"] != "ref-1" {
			t.Fatalf("refused %v", m)
		}
	}
	v.p.close()

	if typed := v.typed(v.release(), 2); fmt.Sprint(typed) != fmt.Sprint([]string{"first", "\r"}) {
		t.Fatalf("typed %q", typed)
	}
	v.quiet(300 * time.Millisecond)
	waited := make(chan struct{})
	go func() {
		v.p.typing.wait()
		v.p.editor.wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the viewer's queue still runs after it left")
	}
	if v.p.typing.add(func() {}) {
		t.Fatal("the queue of a viewer that left took a request")
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines after the viewer left, %d before it submitted", runtime.NumGoroutine(), before)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// An nvim_open is not held up behind a submission that waits for the
// process: the editor's requests have a queue of their own.
func TestAViewersEditorOpenDoesNotWaitForItsSubmissions(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	v.hold()
	v.send(proto.Submit{T: proto.CtlSubmit, Text: "first"})
	v.send(proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "e1", Path: "notes.txt"})
	got := v.until(func(m map[string]any) bool { return m["t"] == proto.CtlNvimEvent && m["reqId"] == "e1" })
	if m := got[len(got)-1]; m["kind"] != proto.NvimError || m["code"] != proto.ErrCodeNvimUnavailable {
		t.Fatalf("nvim_open answered %v", m)
	}
	v.release()
}

// An nvim_open past the editor's queue is refused at once with
// too_many_requests, naming its request.
func TestAViewersEditorOpensPastTheQueueAreRefused(t *testing.T) {
	v := newSubmitViewer(t, 10*time.Millisecond)
	block := make(chan struct{})
	defer close(block)
	for range maxQueued + 1 {
		if !v.p.editor.add(func() { <-block }) {
			t.Fatal("the editor's queue refused before it was full")
		}
	}
	v.send(proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "e1", Path: "notes.txt"})
	got := v.until(func(m map[string]any) bool { return m["t"] == proto.CtlNvimEvent && m["reqId"] == "e1" })
	if m := got[len(got)-1]; m["kind"] != proto.NvimError || m["code"] != proto.ErrCodeTooManyRequests {
		t.Fatalf("nvim_open answered %v", m)
	}
}

// relayHost starts a relay-only host of /bin/cat on a test server and
// returns the server's address and the session's id; the host stops with
// the test.
func relayHost(t *testing.T) (string, string) {
	t.Helper()
	_, hs := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	registered := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := Run(ctx, Options{
			ServerURL: hs.URL, Token: hostToken, Name: "submit test", Argv: []string{"/bin/cat"}, RelayOnly: true,
			Log:        discardLog,
			Registered: func(id, _ string) { registered <- id },
		}); err != nil {
			t.Errorf("run: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("host did not stop")
		}
	})
	select {
	case id := <-registered:
		return hs.URL, id
	case <-time.After(10 * time.Second):
		t.Fatal("host did not register")
	}
	return "", ""
}

// relayController attaches a controller to the session over the relay.
func relayController(t *testing.T, base, sessionID string) *viewer {
	t.Helper()
	v := dialViewer(t, base, sessionID, adminToken)
	v.expectJSON(proto.TypeControl, proto.CtlWelcome)
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.RelayRequest{T: proto.SigRelay, Reason: "forced"})
	v.send(relay)
	v.expectJSON(proto.TypeSignal, proto.SigRelayOK)
	v.send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 90, Rows: 25}))
	v.expectJSON(proto.TypeControl, proto.CtlWelcome)
	v.expectJSON(proto.TypeControl, proto.CtlReady)
	return v
}

// The relay hands a viewer's frames to the host on the loop every relayed
// viewer shares: a burst of submissions is refused past the queue there
// too, and the viewer's connection stays.
func TestARelayedViewersSubmissionsPastTheQueueAreRefused(t *testing.T) {
	base, id := relayHost(t)
	v := relayController(t, base, id)
	const burst = 64
	for i := range burst {
		v.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: fmt.Sprintf("line %d", i)}))
	}
	v.send(proto.MustControl(proto.Ping{T: proto.CtlPing, TS: 7}))
	refused := 0
	for {
		f, err := v.read()
		if err != nil {
			t.Fatalf("after %d refusals: %v", refused, err)
		}
		if f.Type != proto.TypeControl {
			continue
		}
		var m map[string]any
		_ = json.Unmarshal(f.Payload, &m)
		if m["t"] == proto.CtlError {
			if !isRefusal(m) || m["requestId"] != nil {
				t.Fatalf("error %v", m)
			}
			refused++
		}
		if m["t"] == proto.CtlPong {
			break
		}
	}
	// The first is typed at once and maxQueued wait behind it; one waiting
	// is taken each time a submission ends, which the burst seldom sees.
	if refused < burst/2 || refused > burst-1-maxQueued {
		t.Fatalf("%d of %d refused", refused, burst)
	}
}

// A reply box closes its connection once it has sent: over the relay, the
// line it submitted is still typed and entered after the viewer has gone.
func TestARelayedSubmissionFinishesAfterItsViewerLeaves(t *testing.T) {
	base, id := relayHost(t)
	watcher := relayController(t, base, id)
	reply := relayController(t, base, id)
	reply.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "quick line"}))
	reply.c.CloseNow()
	// cat's terminal echoes the line as it is typed, and cat writes it
	// back once its Enter comes.
	var acc []byte
	for bytes.Count(acc, []byte("quick line")) < 2 {
		f, err := watcher.read()
		if err != nil {
			t.Fatalf("the watcher saw %q: %v", acc, err)
		}
		if f.Type == proto.TypeOutput {
			acc = append(acc, f.Payload...)
		}
	}
}
