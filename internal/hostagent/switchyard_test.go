package hostagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
)

// yardMsg is one message a stub switchyard received from a publisher: a
// JSON text message (t and raw set), or a RELAY envelope (viewer and inner
// set).
type yardMsg struct {
	t      string
	raw    map[string]any
	viewer string
	inner  proto.Frame
}

// stubYard is a switchyard reduced to its side of the host protocol: it
// takes one publisher's registration, answers it, keeps every message the
// publisher sends after it, and sends what a test tells it to. It answers a
// link request with a link of its own, as a switchyard mints one.
type stubYard struct {
	t   *testing.T
	srv *httptest.Server
	reg chan proto.Register
	in  chan yardMsg

	mu   sync.Mutex
	c    *websocket.Conn
	seen []yardMsg
	conn chan *websocket.Conn
}

func newStubYard(t *testing.T) *stubYard {
	t.Helper()
	y := &stubYard{t: t, conn: make(chan *websocket.Conn, 4), reg: make(chan proto.Register, 4), in: make(chan yardMsg, 4096)}
	done := make(chan struct{})
	y.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/host" {
			http.NotFound(w, r)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(proto.MaxHostMessage + proto.MaxFrame)
		_, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		var reg proto.Register
		_ = json.Unmarshal(data, &reg)
		y.reg <- reg
		b, _ := json.Marshal(proto.Registered{T: proto.HostRegistered, SessionID: "yard-session-1", Secret: "s", ShareBaseURL: "https://yard.invalid", Links: []string{}})
		if err := c.Write(r.Context(), websocket.MessageText, b); err != nil {
			return
		}
		y.mu.Lock()
		y.c = c
		y.mu.Unlock()
		y.conn <- c
		go y.read(c)
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(done); y.srv.Close() })
	return y
}

// read keeps what the publisher sends on c, answering link requests.
func (y *stubYard) read(c *websocket.Conn) {
	for {
		typ, data, err := c.Read(context.Background())
		if err != nil {
			return
		}
		var m yardMsg
		if typ == websocket.MessageText {
			_ = json.Unmarshal(data, &m.raw)
			m.t, _ = m.raw["t"].(string)
			if m.t == proto.HostLink {
				id, _ := m.raw["requestId"].(string)
				role, _ := m.raw["role"].(string)
				y.send(proto.LinkCreated{T: proto.HostLinkCreated, RequestID: id, URL: "https://yard.invalid/join/minted", LinkID: "link-1", Role: role})
			}
		} else {
			f, err := proto.Decode(data)
			if err != nil || f.Type != proto.TypeRelay {
				continue
			}
			if m.viewer, m.inner, err = proto.DecodeRelay(f.Payload); err != nil {
				continue
			}
		}
		y.mu.Lock()
		y.seen = append(y.seen, m)
		y.mu.Unlock()
		y.in <- m
	}
}

// send writes a JSON message to the publisher.
func (y *stubYard) send(v any) {
	b, _ := json.Marshal(v)
	y.mu.Lock()
	c := y.c
	y.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		y.t.Errorf("switchyard write: %v", err)
	}
}

// relay writes a terminal frame for a viewer to the publisher, as the
// switchyard relays one.
func (y *stubYard) relay(viewerID string, frame []byte) {
	y.t.Helper()
	env, err := proto.EncodeRelay(viewerID, frame)
	if err != nil {
		y.t.Fatal(err)
	}
	y.mu.Lock()
	c := y.c
	y.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, env); err != nil {
		y.t.Fatalf("switchyard relay write: %v", err)
	}
}

// wait returns the first message match accepts, within 10 s.
func (y *stubYard) wait(what string, match func(yardMsg) bool) yardMsg {
	y.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-y.in:
			if match(m) {
				return m
			}
		case <-deadline:
			y.t.Fatalf("the switchyard never received %s", what)
		}
	}
}

// received is every JSON message the switchyard has had of type t so far.
func (y *stubYard) received(t string) []yardMsg {
	y.mu.Lock()
	defer y.mu.Unlock()
	var out []yardMsg
	for _, m := range y.seen {
		if m.t == t {
			out = append(out, m)
		}
	}
	return out
}

// publishedCat is a server's local session running /bin/cat in dir, published
// to y through the Uplink with every hook chained, as `conductor serve` does
// with rendezvous on.
type publishedCat struct {
	local *session.Local
	pub   *Published
	reg   proto.Register
}

func publishCat(t *testing.T, y *stubYard, dir string, command []string) *publishedCat {
	t.Helper()
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: dir, Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	pc := &publishedCat{}
	var mu sync.Mutex
	pub := func() *Published {
		mu.Lock()
		defer mu.Unlock()
		return pc.pub
	}
	info := session.Info{ID: "local-1", Name: "billing-refactor", Kind: session.KindServer, AgentID: "claude", Command: command, Cwd: dir, Status: session.StatusRunning, Cols: 80, Rows: 24, CreatedAt: time.Now().UTC()}
	pc.local = session.NewLocal(info, proc, session.Options{
		Transport: proto.TransportWS,
		Log:       discardLog,
		OnChange: func(i session.Info) {
			if p := pub(); p != nil {
				p.OnChange(i)
			}
		},
		OnActivity: func(id string, e session.ActivityEntry, st session.AttentionState) {
			if p := pub(); p != nil {
				p.OnActivity(id, e, st)
			}
		},
		OnChat: func(id string, m session.ChatMessage) {
			if p := pub(); p != nil {
				p.OnChat(id, m)
			}
		},
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = pc.local.Stop(ctx)
	})
	u := &Uplink{ServerURL: y.srv.URL, HostName: "office", Log: discardLog}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := u.Publish(ctx, pc.local)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	pc.pub = p
	mu.Unlock()
	t.Cleanup(p.Stop)
	pc.reg = <-y.reg
	<-y.conn
	return pc
}

// settled waits until whatever the publication queued for the switchyard has
// been written, then has the switchyard see one attention change sent after
// it: once that arrives, the switchyard has read everything sent before.
func (pc *publishedCat) settled(t *testing.T, y *stubYard) {
	t.Helper()
	waitIdle(t, pc.pub.a.activity)
	pc.local.SetAttention(session.AttentionWorking, "", session.SourceAPI)
	y.wait("the attention change sent last", func(m yardMsg) bool {
		return m.t == proto.HostAttention && m.raw["state"] == string(session.AttentionWorking)
	})
	pc.local.SetAttention(session.AttentionNone, "", session.SourceAPI)
}

// A switchyard is told a published session's attention state and nothing of
// what is said in it: a chat line posted at home, the prompt Conductor types
// for a crew, an event an agent reports and the excerpt of the agent's reply
// a "done" carries all stay on the publishing machine, where the session's
// own viewers have them.
func TestASwitchyardIsToldNoChatActivityOrAttentionWords(t *testing.T) {
	y := newStubYard(t)
	pc := publishCat(t, y, t.TempDir(), []string{"/bin/cat"})

	owner, err := pc.local.AttachWith(session.AttachOptions{Role: session.RoleControl, Name: "Owner"}, nopSink{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.local.Chat(owner, proto.ChatPost{T: proto.CtlChat, Text: "the staging password is not here"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pc.local.Submit(ctx, session.Submission{Text: "You are the reviewer: check deploy/keys and report.", ByName: "crew"}); err != nil {
		t.Fatal(err)
	}
	pc.local.Record(session.ActivityEntry{Type: session.ActivityProgress, Message: "rotated the signing key"})
	pc.local.SetAttentionFull(session.AttentionDone, "I rotated the key in deploy/keys", session.SourceAPI, session.KindDone, []session.Option{{Label: "Ship it", Input: "ship it\r"}})
	done := y.wait("the done state", func(m yardMsg) bool {
		return m.t == proto.HostAttention && m.raw["state"] == string(session.AttentionDone)
	})
	if done.raw["message"] != nil || done.raw["options"] != nil || done.raw["kind"] != session.KindDone || done.raw["source"] != session.SourceAPI {
		t.Fatalf("the switchyard was told %v", done.raw)
	}
	pc.settled(t, y)

	if got := y.received(proto.HostChat); len(got) != 0 {
		t.Fatalf("the switchyard was sent chat: %v", got[0].raw)
	}
	if got := y.received(proto.HostActivity); len(got) != 0 {
		t.Fatalf("the switchyard was sent activity: %v", got[0].raw)
	}
	for _, m := range y.received(proto.HostAttention) {
		if m.raw["message"] != nil || m.raw["options"] != nil {
			t.Fatalf("an attention change went with its words: %v", m.raw)
		}
	}
	// The session at home keeps all of it.
	if h := pc.local.ChatHistory(); len(h) == 0 || !strings.Contains(h[len(h)-1].Text, "staging password") {
		t.Fatalf("chat at home %+v", h)
	}
	if att := pc.local.Info().Attention; att.State != session.AttentionNone {
		t.Fatalf("attention at home %+v", att)
	}
}

// The owner's own server (`conductor host`, Options.Switchyard off) is still
// told everything, for its workbench; a switchyard agent queues nothing.
func TestOnlyASwitchyardIsToldLess(t *testing.T) {
	for _, yard := range []bool{false, true} {
		a, out := activityTestAgent(t, 0)
		a.opts.Switchyard = yard
		a.local.SetAttentionFull(session.AttentionNeedsInput, "Allow Bash: ls?", session.SourceAPI, session.KindPermission, []session.Option{{Label: "Yes", Input: "1"}})
		a.onLocalChange(a.local.Info())
		a.onLocalChat("sess-1", session.ChatMessage{ID: "m1", Kind: proto.ChatKindMessage, Text: "hello", By: session.ChatBy{ID: "b1", Name: "Nate", Role: session.RoleControl}})
		a.local.Record(session.ActivityEntry{Type: session.ActivityProgress, Message: "step 1"})
		waitIdle(t, a.activity)
		var chats, entries int
		var att *proto.HostAttentionMsg
	drain:
		for {
			select {
			case v := <-out:
				switch m := v.(type) {
				case proto.HostChatMsg:
					chats++
				case proto.HostActivityMsg:
					entries++
				case proto.HostAttentionMsg:
					att = &m
				}
			default:
				break drain
			}
		}
		if att == nil || att.State != string(session.AttentionNeedsInput) || att.Kind != session.KindPermission {
			t.Fatalf("switchyard %v: attention %+v", yard, att)
		}
		if yard {
			if chats != 0 || entries != 0 || att.Message != "" || len(att.Options) != 0 {
				t.Fatalf("a switchyard was told chat %d, entries %d, attention %+v", chats, entries, att)
			}
		} else if chats == 0 || entries == 0 || att.Message != "Allow Bash: ls?" || len(att.Options) != 1 {
			t.Fatalf("the owner's server was told chat %d, entries %d, attention %+v", chats, entries, att)
		}
	}
}

// waitChat reads frames until a chat message with text arrives.
func waitChat(t *testing.T, frames <-chan []byte, text string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-frames:
			f, err := proto.Decode(raw)
			if err != nil || f.Type != proto.TypeControl {
				continue
			}
			var m map[string]any
			if json.Unmarshal(f.Payload, &m) == nil && m["t"] == proto.CtlChat && m["text"] == text {
				return m
			}
		case <-deadline:
			t.Fatalf("the guest never got the chat line %q", text)
		}
	}
}

// A guest who reaches a published session over WebRTC has its chat from the
// publishing machine over the data channel, while the switchyard is sent
// none of it: the line posted at home reaches the guest, and the guest's
// own line reaches home.
func TestASwitchyardGuestHasChatOverTheDataChannel(t *testing.T) {
	y := newStubYard(t)
	pc := publishCat(t, y, t.TempDir(), []string{"/bin/cat"})
	const guest = "guestwebrtc00001"
	y.send(proto.ViewerJoin{T: proto.HostViewerJoin, ViewerID: guest, Role: string(session.RoleView), LinkID: "link-1", LinkLabel: "for Priya"})

	pc2, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc2.Close() })
	dc, err := pc2.CreateDataChannel("term", nil)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	frames := make(chan []byte, 1024)
	dc.OnOpen(func() { close(opened) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) { frames <- m.Data })
	pc2.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		y.send(proto.ViewerICE{T: proto.HostICE, ViewerID: guest, Candidate: proto.ICECandidate{Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex}})
	})
	offer, err := pc2.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pc2.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	// The browser's offer, as the switchyard forwards it.
	y.send(proto.ViewerSDP{T: proto.HostOffer, ViewerID: guest, SDP: offer.SDP})
	var early []yardMsg
	ans := y.wait("the publisher's answer", func(m yardMsg) bool {
		if m.t == proto.HostICE {
			early = append(early, m)
		}
		return m.t == proto.HostAnswer
	})
	if err := pc2.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.raw["sdp"].(string)}); err != nil {
		t.Fatal(err)
	}
	addICE := func(m yardMsg) {
		c, _ := m.raw["candidate"].(map[string]any)
		cand, _ := c["candidate"].(string)
		init := webrtc.ICECandidateInit{Candidate: cand}
		if mid, ok := c["sdpMid"].(string); ok {
			init.SDPMid = &mid
		}
		if idx, ok := c["sdpMLineIndex"].(float64); ok {
			i := uint16(idx)
			init.SDPMLineIndex = &i
		}
		_ = pc2.AddICECandidate(init)
	}
	for _, m := range early {
		addICE(m)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case m := <-y.in:
				if m.t == proto.HostICE {
					addICE(m)
				}
			case <-stop:
				return
			}
		}
	}()
	select {
	case <-opened:
	case <-time.After(20 * time.Second):
		t.Fatal("the data channel did not open")
	}
	dc.Send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: proto.ProtoVersion, Cols: 80, Rows: 24, Name: "Priya"}))
	attached := time.After(10 * time.Second)
	for ready := false; !ready; {
		select {
		case raw := <-frames:
			if f, err := proto.Decode(raw); err == nil && f.Type == proto.TypeControl {
				typ, _ := proto.ParseHeader(f.Payload)
				ready = typ == proto.CtlReady
			}
		case <-attached:
			t.Fatal("the guest was not attached")
		}
	}

	owner, err := pc.local.AttachWith(session.AttachOptions{Role: session.RoleControl, Name: "Owner"}, nopSink{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.local.Chat(owner, proto.ChatPost{T: proto.CtlChat, Text: "from home"}); err != nil {
		t.Fatal(err)
	}
	if m := waitChat(t, frames, "from home"); m["by"].(map[string]any)["name"] != "Owner" {
		t.Fatalf("the guest got %v", m)
	}
	dc.Send(proto.MustControl(proto.ChatPost{T: proto.CtlChat, Text: "from afar", Nonce: "g1"}))
	deadline := time.Now().Add(10 * time.Second)
	for {
		h := pc.local.ChatHistory()
		if len(h) > 0 && h[len(h)-1].Text == "from afar" && h[len(h)-1].By.Name == "Priya" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the guest's line never reached home: %+v", h)
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitIdle(t, pc.pub.a.activity)
	if got := y.received(proto.HostChat); len(got) != 0 {
		t.Fatalf("the switchyard was sent chat: %v", got[0].raw)
	}
}

// A guest whose terminal goes through the switchyard's relay has the chat
// too, in relayed frames: those pass through the switchyard, which reads what
// it relays, and nothing else carries the chat to it.
func TestASwitchyardGuestHasChatOverTheRelay(t *testing.T) {
	y := newStubYard(t)
	pc := publishCat(t, y, t.TempDir(), []string{"/bin/cat"})
	const guest = "guestrelay000001"
	y.send(proto.ViewerJoin{T: proto.HostViewerJoin, ViewerID: guest, Role: string(session.RoleView), LinkID: "link-1"})
	y.send(proto.ViewerRef{T: proto.HostRelayStart, ViewerID: guest})
	y.relay(guest, proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: proto.ProtoVersion, Cols: 80, Rows: 24, Name: "Priya"}))
	y.wait("the guest's welcome", func(m yardMsg) bool {
		var c map[string]any
		return m.viewer == guest && m.inner.Type == proto.TypeControl && json.Unmarshal(m.inner.Payload, &c) == nil && c["t"] == proto.CtlReady
	})
	owner, err := pc.local.AttachWith(session.AttachOptions{Role: session.RoleControl, Name: "Owner"}, nopSink{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.local.Chat(owner, proto.ChatPost{T: proto.CtlChat, Text: "from home"}); err != nil {
		t.Fatal(err)
	}
	y.wait("the chat line relayed to the guest", func(m yardMsg) bool {
		var c map[string]any
		return m.viewer == guest && m.inner.Type == proto.TypeControl && json.Unmarshal(m.inner.Payload, &c) == nil && c["t"] == proto.CtlChat && c["text"] == "from home"
	})
	y.relay(guest, proto.MustControl(proto.ChatPost{T: proto.CtlChat, Text: "from afar"}))
	y.wait("the guest's own line, back to it", func(m yardMsg) bool {
		var c map[string]any
		return m.viewer == guest && m.inner.Type == proto.TypeControl && json.Unmarshal(m.inner.Payload, &c) == nil && c["t"] == proto.CtlChat && c["text"] == "from afar"
	})
	pc.settled(t, y)
	if got := y.received(proto.HostChat); len(got) != 0 {
		t.Fatalf("the switchyard was sent chat apart from the relay: %v", got[0].raw)
	}
}

// A switchyard is told the session's name, its agent, the machine's name and
// the last element of its folder: no command line (its flags, the agent's
// own session id), no OS user, no branch. A guest has the folder in full
// from the session's welcome, which the publishing machine sends.
func TestASwitchyardIsToldLessOfTheSession(t *testing.T) {
	y := newStubYard(t)
	dir := filepath.Join(t.TempDir(), "acme-billing")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/fix-acme-invoices\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pc := publishCat(t, y, dir, []string{"/usr/local/bin/claude", "--dangerously-skip-permissions", "--session-id", "0b8c5c1e-0000-4000-8000-000000000001"})
	reg := pc.reg
	if reg.Host.User != "" || reg.Session.Branch != "" {
		t.Fatalf("the switchyard was told the user %q and the branch %q", reg.Host.User, reg.Session.Branch)
	}
	if len(reg.Session.Command) != 1 || reg.Session.Command[0] != "claude" {
		t.Fatalf("the switchyard was told the command %q", reg.Session.Command)
	}
	if reg.Session.Cwd != "acme-billing" {
		t.Fatalf("the switchyard was told the folder %q", reg.Session.Cwd)
	}
	if reg.Session.Name != "billing-refactor" || reg.Session.AgentID != "claude" || reg.Host.Name != "office" {
		t.Fatalf("what the switchyard lists the session by: %+v %+v", reg.Session, reg.Host)
	}

	const guest = "guestrelay000002"
	y.send(proto.ViewerJoin{T: proto.HostViewerJoin, ViewerID: guest, Role: string(session.RoleView), LinkID: "link-1"})
	y.send(proto.ViewerRef{T: proto.HostRelayStart, ViewerID: guest})
	y.relay(guest, proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: proto.ProtoVersion, Cols: 80, Rows: 24, Name: "Priya"}))
	w := y.wait("the guest's welcome", func(m yardMsg) bool {
		var c map[string]any
		return m.viewer == guest && m.inner.Type == proto.TypeControl && json.Unmarshal(m.inner.Payload, &c) == nil && c["t"] == proto.CtlWelcome
	})
	var welcome map[string]any
	_ = json.Unmarshal(w.inner.Payload, &welcome)
	if welcome["cwd"] != dir {
		t.Fatalf("the guest's welcome names the folder %v", welcome["cwd"])
	}
}

// What a registration says, to the owner's own server and to a switchyard:
// the program's name stands in for a session without an agent id, and the
// owner's server is told it all.
func TestTheRegistrationToEachKindOfServer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	a := &agent{opts: Options{Name: "n", HostName: "h", Argv: []string{"/opt/bin/aider", "--yes"}}, dir: dir, instance: "i", localID: "1"}
	full := a.registration(80, 24)
	if full.Session.Cwd != dir || len(full.Session.Command) != 2 || full.Host.User != currentUser() {
		t.Fatalf("to the owner's server %+v %+v", full.Session, full.Host)
	}
	a.opts.Switchyard = true
	less := a.registration(80, 24)
	if less.Session.Cwd != "proj" || len(less.Session.Command) != 1 || less.Session.Command[0] != "aider" || less.Host.User != "" {
		t.Fatalf("to a switchyard %+v %+v", less.Session, less.Host)
	}
	a.opts.AgentID = "aider-dev"
	if c := a.registration(80, 24).Session.Command; len(c) != 1 || c[0] != "aider-dev" {
		t.Fatalf("with an agent id: %q", c)
	}
}
