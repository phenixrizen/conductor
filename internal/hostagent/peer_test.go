package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/session/sessiontest"
)

// loopbackViewer connects a browser-like pion peer to p: the viewer creates the
// data channel and the offer, p answers, and ICE and the answer reach the
// viewer through out, which the agent's sendHook fills. It returns the viewer's
// data channel, once open, and the messages the viewer receives on it.
func loopbackViewer(t *testing.T, p *peer, out <-chan any) (*webrtc.DataChannel, <-chan []byte) {
	t.Helper()
	viewerPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { viewerPC.Close() })
	frames := make(chan []byte, 1024)
	dc, err := viewerPC.CreateDataChannel("term", nil)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	dc.OnOpen(func() { close(opened) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) { frames <- m.Data })
	viewerPC.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		p.handleICE(proto.ICECandidate{Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex})
	})
	offer, err := viewerPC.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := viewerPC.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	if err := p.handleOffer(offer.SDP); err != nil {
		t.Fatal(err)
	}
	// Pump host signaling back into the viewer peer.
	go func() {
		for v := range out {
			switch m := v.(type) {
			case proto.ViewerSDP:
				viewerPC.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
			case proto.ViewerICE:
				viewerPC.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate.Candidate, SDPMid: m.Candidate.SDPMid, SDPMLineIndex: m.Candidate.SDPMLineIndex})
			}
		}
	}()
	select {
	case <-opened:
	case <-time.After(15 * time.Second):
		t.Fatal("data channel did not open")
	}
	return dc, frames
}

// TestPeerDataChannelLoopback runs the real host peer code against an
// in-process browser-like pion peer: the viewer creates the data channel and
// the offer, the host answers, ICE trickles through channels.
func TestPeerDataChannelLoopback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "big.txt"), bytes.Repeat([]byte("k"), 200<<10), 0o600)
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: dir, Env: hostEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	local := session.NewLocal(session.Info{ID: "s", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{})
	t.Cleanup(func() { proc.Stop(t.Context(), time.Second) })

	out := make(chan any, 64)
	a := &agent{opts: Options{}, local: local, proc: proc, peers: map[string]*peer{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.sendHook = func(v any) { out <- v }

	p := newPeer(a, "0123456789abcdef", session.RoleControl, "", "", true)
	if err := p.startWebRTC(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)

	dc, frames := loopbackViewer(t, p, out)
	dc.Send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 100, Rows: 40}))
	next := func() proto.Frame {
		t.Helper()
		select {
		case raw := <-frames:
			f, err := proto.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			return f
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for frame")
		}
		return proto.Frame{}
	}
	control := func(f proto.Frame) map[string]any {
		var m map[string]any
		json.Unmarshal(f.Payload, &m)
		return m
	}
	if m := control(next()); m["t"] != proto.CtlWelcome || m["transport"] != "webrtc" || m["cols"] != float64(100) {
		t.Fatalf("welcome %v", m)
	}
	var seenReady bool
	for !seenReady {
		f := next()
		if f.Type == proto.TypeControl && control(f)["t"] == proto.CtlReady {
			seenReady = true
		}
	}
	dc.Send(proto.Encode(proto.TypeInput, []byte("over webrtc\n")))
	var acc []byte
	for !bytes.Contains(acc, []byte("over webrtc")) {
		f := next()
		if f.Type == proto.TypeOutput {
			acc = append(acc, f.Payload...)
		}
	}
	// A 200 KiB file arrives fragmented and reassembles.
	dc.Send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "big", Path: "big.txt"}))
	assembled := map[uint16][]byte{}
	var got []byte
	for got == nil {
		f := next()
		switch f.Type {
		case proto.TypeChunk:
			id, total, off, data, err := proto.ChunkInfo(f.Payload)
			if err != nil {
				t.Fatal(err)
			}
			buf := assembled[id]
			if buf == nil {
				buf = make([]byte, total)
				assembled[id] = buf
			}
			copy(buf[off:], data)
			if int(off)+len(data) == int(total) {
				got = buf
			}
		case proto.TypeFile:
			got = proto.Encode(proto.TypeFile, f.Payload)
		}
	}
	full, err := proto.Decode(got)
	if err != nil || full.Type != proto.TypeFile {
		t.Fatalf("reassembled frame: %v", err)
	}
	h, body, err := proto.DecodeFile(full.Payload)
	if err != nil || h.ReqID != "big" || len(body) != 200<<10 {
		t.Fatalf("file %+v %d %v", h, len(body), err)
	}
}

// webrtcViewerAgent is a host with a real session and one WebRTC viewer whose
// data channel is open and who is attached: what a host has while somebody
// watches over the default transport.
func webrtcViewerAgent(t *testing.T) (*agent, *peer, <-chan []byte) {
	t.Helper()
	a, out := activityTestAgent(t, 0)
	a.flushed = make(chan struct{}) // no watchStatus here to report the status message
	close(a.flushed)
	p := newPeer(a, "0123456789abcdef", session.RoleControl, "", "", true)
	a.peers[p.id] = p
	if err := p.startWebRTC(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	dc, frames := loopbackViewer(t, p, out)
	dc.Send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 80, Rows: 24}))
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-frames:
			if f, err := proto.Decode(raw); err == nil && f.Type == proto.TypeControl {
				if typ, _ := proto.ParseHeader(f.Payload); typ == proto.CtlReady {
					return a, p, frames
				}
			}
		case <-deadline:
			t.Fatal("the viewer was not attached")
		}
	}
}

// flood queues 480 KiB of output for the viewer: less than the session and the
// data channel take without holding anything up, more than the connection has
// carried and had acknowledged by the time the host is done.
func flood(t *testing.T, a *agent, p *peer) {
	t.Helper()
	p.mu.Lock()
	sub := p.sub
	p.mu.Unlock()
	if sub == nil {
		t.Fatal("the viewer is not attached")
	}
	frame := proto.Encode(proto.TypeOutput, bytes.Repeat([]byte{'x'}, 30<<10))
	for i := 0; i < 16; i++ {
		a.local.Send(sub, frame)
	}
}

// A data channel holds what it was given until the other end acknowledges it,
// and closing the peer connection drops what it still holds. Drained says the
// frames were handed to the channel, not that they got anywhere, so settle has
// to wait for the channel as well, or a WebRTC viewer, the default transport,
// is never told that the session ended.
func TestSettleWaitsForAWebRTCViewerToGetTheFinalStatus(t *testing.T) {
	a, p, frames := webrtcViewerAgent(t)
	flood(t, a, p)
	go a.local.Stop(t.Context())
	select {
	case <-a.local.Ended():
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not end")
	}
	start := time.Now()
	a.settle(t.Context())
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Fatalf("settle took %v: it ran into its limit instead of seeing the channel drain", took)
	}
	a.closeAllPeers()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-frames:
			f, err := proto.Decode(raw)
			if err != nil || f.Type != proto.TypeControl {
				continue
			}
			if typ, _ := proto.ParseHeader(f.Payload); typ == proto.CtlStatus {
				var m map[string]any
				_ = json.Unmarshal(f.Payload, &m)
				if m["status"] != "stopped" {
					t.Fatalf("status %v", m)
				}
				return
			}
		case <-deadline:
			t.Fatal("the viewer never learned that the session ended")
		}
	}
}

// Like the other waits, this one gives up at once when the host has been
// stopped from outside.
func TestSettleDoesNotWaitForWebRTCViewersOnceCancelled(t *testing.T) {
	a, p, _ := webrtcViewerAgent(t)
	flood(t, a, p)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	a.settle(ctx)
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("settle took %v after the host was cancelled", took)
	}
}

// A viewer's hello over the host's transports follows the same rule as on the
// server: (0, 0) follows the session's size, a view link's size is never
// applied, and a controller's two dimensions in range set it. The role is the
// one the server gave the peer, whatever the hello says.
func TestAPeersHelloOfZeroFollowsTheSize(t *testing.T) {
	proc := sessiontest.NewFakeProc()
	local := session.NewLocal(session.Info{ID: "s", Cols: 148, Rows: 57}, proc, session.Options{Log: slog.New(slog.DiscardHandler)})
	t.Cleanup(func() { proc.End(0) })
	a := &agent{opts: Options{}, local: local, peers: map[string]*peer{}, log: slog.New(slog.DiscardHandler), sendHook: func(any) {}}
	for i, tc := range []struct {
		role       session.Role
		cols, rows uint16
		want       [2]uint16
	}{
		{session.RoleControl, 0, 0, [2]uint16{0, 0}},
		{session.RoleView, 80, 24, [2]uint16{0, 0}},
		{session.RoleControl, 0, 24, [2]uint16{0, 0}},
		{session.RoleControl, 100, 30, [2]uint16{100, 30}},
	} {
		p := newPeer(a, fmt.Sprintf("%016x", i), tc.role, "", "", true)
		p.startRelay()
		p.handleFrame(proto.Frame{Type: proto.TypeControl, Payload: mustJSON(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: tc.cols, Rows: tc.rows})})
		if c, r := proc.Size(); [2]uint16{c, r} != tc.want {
			t.Fatalf("%s %dx%d: PTY %dx%d", tc.role, tc.cols, tc.rows, c, r)
		}
		p.close()
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// A chat post over the data channel reaches the host's session and comes
// back as a chat message; the host's welcome says chat is taken.
func TestPeerCarriesChatOverTheDataChannel(t *testing.T) {
	dir := t.TempDir()
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: dir, Env: hostEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	local := session.NewLocal(session.Info{ID: "s", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{})
	t.Cleanup(func() { proc.Stop(t.Context(), time.Second) })
	out := make(chan any, 64)
	a := &agent{opts: Options{}, local: local, proc: proc, peers: map[string]*peer{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.sendHook = func(v any) { out <- v }
	p := newPeer(a, "0123456789abcdef", session.RoleControl, "", "", true)
	if err := p.startWebRTC(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	dc, frames := loopbackViewer(t, p, out)
	dc.Send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 100, Rows: 40, Name: "Nate"}))
	control := func(want func(m map[string]any) bool) map[string]any {
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
				if json.Unmarshal(f.Payload, &m) == nil && want(m) {
					return m
				}
			case <-deadline:
				t.Fatal("no such control frame")
			}
		}
	}
	if w := control(func(m map[string]any) bool { return m["t"] == proto.CtlWelcome }); w["chat"] != true {
		t.Fatalf("welcome %v", w)
	}
	dc.Send(proto.MustControl(proto.ChatPost{T: proto.CtlChat, Text: "over the channel", Nonce: "c1"}))
	m := control(func(m map[string]any) bool { return m["t"] == proto.CtlChat && m["kind"] == proto.ChatKindMessage })
	if m["text"] != "over the channel" || m["nonce"] != "c1" || m["by"].(map[string]any)["name"] != "Nate" {
		t.Fatalf("chat %v", m)
	}
	if h := local.ChatHistory(); len(h) == 0 || h[len(h)-1].Text != "over the channel" {
		t.Fatalf("history %+v", h)
	}
}

// A control link on a hosted session follows the size the host's own
// terminal gives it: its window's resize is refused, and Fit to my window
// (take) hands it the size (round 14, Local's sizer, over the data channel).
func TestPeerTakesTheSizeOnlyByAsking(t *testing.T) {
	dir := t.TempDir()
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: dir, Env: hostEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	local := session.NewLocal(session.Info{ID: "s", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{})
	t.Cleanup(func() { proc.Stop(t.Context(), time.Second) })
	// The host's own terminal: a controller with no link, the sizer.
	own, err := local.AttachWith(session.AttachOptions{Role: session.RoleControl, Name: "host terminal", Cols: 120, Rows: 40}, nopSink{})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Detach(own)
	out := make(chan any, 64)
	a := &agent{opts: Options{}, local: local, proc: proc, peers: map[string]*peer{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.sendHook = func(v any) { out <- v }
	p := newPeer(a, "0123456789abcdef", session.RoleControl, "link1", "laptop", false)
	if err := p.startWebRTC(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	dc, frames := loopbackViewer(t, p, out)
	dc.Send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 90, Rows: 30, Name: "Jane"}))
	control := func(want func(m map[string]any) bool) map[string]any {
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
				if json.Unmarshal(f.Payload, &m) == nil && want(m) {
					return m
				}
			case <-deadline:
				t.Fatal("no such control frame")
			}
		}
	}
	w := control(func(m map[string]any) bool { return m["t"] == proto.CtlWelcome })
	if w["sizer"] != true || w["sizedBy"] != own.ID || w["cols"] != float64(120) {
		t.Fatalf("welcome %v", w)
	}
	dc.Send(proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: 90, Rows: 30}))
	dc.Send(proto.MustControl(proto.Ping{T: proto.CtlPing, TS: 7}))
	control(func(m map[string]any) bool { return m["t"] == proto.CtlPong })
	if i := local.Info(); i.Cols != 120 {
		t.Fatalf("a link's resize moved the size to %dx%d", i.Cols, i.Rows)
	}
	dc.Send(proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: 90, Rows: 30, Take: true}))
	m := control(func(m map[string]any) bool { return m["t"] == proto.CtlResize && m["cols"] == float64(90) })
	if m["by"] != w["subscriberId"] {
		t.Fatalf("the take's resize %v (welcome %v)", m, w)
	}
}

type nopSink struct{}

func (nopSink) WriteFrame([]byte) error { return nil }
func (nopSink) Close(error)             {}

// A viewer a server sends with no link is one of the owner's own windows
// only when that server is on the owner's machine (ServerIsOwners); from a
// switchyard, whose workbench is its operator's, it is not, and a link is
// never the owner's.
func TestAViewerWithNoLinkIsTheOwnersOnlyFromTheOwnersServer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mine   bool
		linkID string
		owner  bool
	}{
		{"a switchyard's, no link", false, "", false},
		{"the owner's server, no link", true, "", true},
		{"the owner's server, a link", true, "l1", false},
		{"a switchyard's, a link", false, "l1", false},
	} {
		a := &agent{opts: Options{RelayOnly: true, ServerIsOwners: tc.mine}, peers: map[string]*peer{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		a.addPeer("0123456789abcdef", session.RoleControl, tc.linkID, "")
		p := a.peers["0123456789abcdef"]
		if p == nil || p.owner != tc.owner {
			t.Fatalf("%s: owner %v, want %v", tc.name, p != nil && p.owner, tc.owner)
		}
		a.removePeer("0123456789abcdef")
	}
}
