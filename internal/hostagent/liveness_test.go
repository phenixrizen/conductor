package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// heardFromServer has a's server heard from all along, as over a live
// connection, until the test ends.
func heardFromServer(t *testing.T, a *agent) {
	t.Helper()
	l := newConnLive()
	a.live.Store(l)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				l.hear()
			}
		}
	}()
}

// stallProxy carries TCP connections to target until stall: from then on
// it carries nothing either way and closes nothing, as a connection that
// no longer carries what is sent does without either end noticing, until
// resume.
type stallProxy struct {
	ln     net.Listener
	target string
	// slow, when above 0, has what the server sends carried that many bytes
	// every 50 ms (trickle).
	slow atomic.Int64

	mu    sync.Mutex
	open  chan struct{} // closed while it carries
	conns []net.Conn
	done  chan struct{}
}

func newStallProxy(t *testing.T, target string) *stallProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &stallProxy{ln: ln, target: target, open: make(chan struct{}), done: make(chan struct{})}
	close(p.open)
	go p.accept()
	t.Cleanup(p.close)
	return p
}

// url is the proxy's, as an http base.
func (p *stallProxy) url() string { return "http://" + p.ln.Addr().String() }

func (p *stallProxy) accept() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		s, err := net.Dial("tcp", p.target)
		if err != nil {
			c.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, c, s)
		p.mu.Unlock()
		go p.pipe(s, c, false)
		go p.pipe(c, s, true)
	}
}

// pipe carries what src sends to dst, each read once the proxy carries;
// down, what the server sends, at the trickle's pace.
func (p *stallProxy) pipe(dst, src net.Conn, down bool) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			select {
			case <-p.gate():
			case <-p.done:
				return
			}
			for out := buf[:n]; len(out) > 0; {
				k := len(out)
				slow := int(p.slow.Load())
				if down && slow > 0 {
					k = min(k, slow)
				}
				if _, err := dst.Write(out[:k]); err != nil {
					return
				}
				out = out[k:]
				if down && slow > 0 {
					time.Sleep(50 * time.Millisecond)
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if tc, ok := dst.(*net.TCPConn); ok {
					_ = tc.CloseWrite()
				}
			}
			return
		}
	}
}

func (p *stallProxy) gate() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.open
}

// trickle has what the server sends carried n bytes every 50 ms.
func (p *stallProxy) trickle(n int) { p.slow.Store(int64(n)) }

// stall stops carrying anything, closing nothing.
func (p *stallProxy) stall() {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.open:
		p.open = make(chan struct{})
	default:
	}
}

// resume carries again, what waited first.
func (p *stallProxy) resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.open:
	default:
		close(p.open)
	}
}

func (p *stallProxy) close() {
	p.mu.Lock()
	select {
	case <-p.done:
		p.mu.Unlock()
		return
	default:
	}
	close(p.done)
	conns := p.conns
	p.mu.Unlock()
	p.ln.Close()
	for _, c := range conns {
		c.Close()
	}
}

// signalRouter hands what the host sends for each viewer (its answer, its
// candidates) to that viewer's loopback peer: the viewers of liveHost
// signal over no server.
type signalRouter struct {
	mu sync.Mutex
	to map[string]chan any
}

func (r *signalRouter) send(v any) {
	var id string
	switch m := v.(type) {
	case proto.ViewerSDP:
		id = m.ViewerID
	case proto.ViewerICE:
		id = m.ViewerID
	default:
		return
	}
	r.mu.Lock()
	ch := r.to[id]
	r.mu.Unlock()
	if ch != nil {
		ch <- v
	}
}

func (r *signalRouter) add(id string) <-chan any {
	ch := make(chan any, 64)
	r.mu.Lock()
	r.to[id] = ch
	r.mu.Unlock()
	return ch
}

// liveTestWithin and liveTestPing are liveHost's bounds: the server heard
// from within 400 ms, and pinged once quiet for 100 ms.
const (
	liveTestWithin = 400 * time.Millisecond
	liveTestPing   = 100 * time.Millisecond
)

// liveHost is a host of a session on a FakeProc whose Enter comes pause
// after a submission's text, registered with a server, whose own pings are
// 30 s apart, through a proxy that can stop carrying anything; its bounds
// liveTestWithin and liveTestPing. viewer attaches a viewer joined through
// linkID ("" for none: one of the owner's windows) over a data channel of
// its own, which the proxy does not carry.
func liveHost(t *testing.T, pause time.Duration) (a *agent, px *stallProxy, viewer func(id, linkID string) *submitViewer) {
	t.Helper()
	return liveHostStalling(t, pause, 0)
}

// liveHostStalling is liveHost whose writeStall is stall (its own when 0).
func liveHostStalling(t *testing.T, pause, stall time.Duration) (a *agent, px *stallProxy, viewer func(id, linkID string) *submitViewer) {
	t.Helper()
	_, hs := startServer(t)
	px = newStallProxy(t, strings.TrimPrefix(hs.URL, "http://"))
	a, viewer = liveHostAt(t, px.url(), pause, stall)
	return a, px, viewer
}

// liveHostAt is liveHostStalling registered with the server at base
// directly.
func liveHostAt(t *testing.T, base string, pause, stall time.Duration) (a *agent, viewer func(id, linkID string) *submitViewer) {
	t.Helper()
	a, proc, begun := newSubmitHost(t, pause, 0, 0)
	a.liveWithin, a.livePing, a.stallWait = liveTestWithin, liveTestPing, stall
	a.opts = Options{ServerURL: base, Token: hostToken, Name: "liveness", Argv: []string{"stub"}, AgentID: "stub", HostName: "h", ReconnectMax: time.Second, Log: discardLog}
	a.dir = t.TempDir()
	wsURL, err := controlURL(base)
	if err != nil {
		t.Fatal(err)
	}
	a.wsURL = wsURL
	r := &signalRouter{to: map[string]chan any{}}
	a.sendHook = r.send
	ctx, cancel := context.WithCancel(context.Background())
	conn, _, err := a.dialAndRegister(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.controlLoop(ctx, conn)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return a, func(id, linkID string) *submitViewer {
		t.Helper()
		return submitViewerOn(t, a, proc, begun, id, linkID, r.add(id))
	}
}

// untilVouches waits until the host vouches for its links (want true) or
// no longer does.
func untilVouches(t *testing.T, a *agent, want bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for (a.vouches() == nil) != want {
		if time.Now().After(deadline) {
			t.Fatalf("vouches %v, want %v", a.vouches() == nil, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A line of a viewer who joined through a link, its text typed, has no
// Enter once the host's connection to the server stops carrying anything
// for longer than the bound, the server's side gone quiet and neither end
// closing: the host would not hear of the link going, and the session's
// turn is held for a person, as for a link gone. The owner's line waits
// for that person, whose keys end the hold, and is typed and entered with
// the server still unheard.
func TestALinksLineIsNotEnteredWhileTheServerIsNotHeardFrom(t *testing.T) {
	a, px, viewer := liveHost(t, 1500*time.Millisecond)
	guest := viewer("0123456789abcdef", "l1")
	owner := viewer("fedcba9876543210", "")
	guest.send(proto.Submit{T: proto.CtlSubmit, Text: "guest line"})
	guest.sync()
	if got := guest.release(); got != "guest line" {
		t.Fatalf("typed %q", got)
	}
	px.stall()
	untilVouches(t, a, false)
	guest.quiet(1500 * time.Millisecond)
	holdShows(t, a.local)

	owner.send(proto.Submit{T: proto.CtlSubmit, Text: "owner line"})
	owner.sync()
	owner.quiet(300 * time.Millisecond)
	owner.key("\x15")
	if typed := owner.typed(owner.release(), 3); strings.Join(typed, "|") != "\x15|owner line|\r" {
		t.Fatalf("typed %q", typed)
	}
	if a.vouches() == nil {
		t.Fatal("the server was heard from through the stalled proxy")
	}
}

// A line of a link's viewer whose text is not typed yet is withdrawn while
// the server is not heard from within the bound: nothing of it is typed,
// the viewer is told, and the turn is not held. The owner's line meanwhile
// is typed and entered. Once the connection carries again the host hears
// the server, and the link's next line is typed and entered.
func TestALinksLineNotTypedYetIsWithdrawnWhileTheServerIsNotHeardFrom(t *testing.T) {
	a, px, viewer := liveHost(t, 50*time.Millisecond)
	guest := viewer("0123456789abcdef", "l1")
	owner := viewer("fedcba9876543210", "")
	px.stall()
	untilVouches(t, a, false)

	guest.send(proto.Submit{T: proto.CtlSubmit, Text: "guest line"})
	refused := false
	for _, m := range guest.sync() {
		refused = refused || m["t"] == proto.CtlError && m["code"] == "input_failed"
	}
	if !refused {
		// The refusal may come after the pong: the submission runs off the
		// frame loop.
		guest.until(func(m map[string]any) bool { return m["t"] == proto.CtlError && m["code"] == "input_failed" })
	}
	guest.quiet(300 * time.Millisecond)
	if a.local.Held() {
		t.Fatal("a line withdrawn before its text holds the turn")
	}

	owner.send(proto.Submit{T: proto.CtlSubmit, Text: "owner line"})
	if typed := owner.typed(owner.release(), 2); strings.Join(typed, "|") != "owner line|\r" {
		t.Fatalf("typed %q", typed)
	}

	px.resume()
	untilVouches(t, a, true)
	guest.send(proto.Submit{T: proto.CtlSubmit, Text: "again"})
	if typed := guest.typed(guest.release(), 2); strings.Join(typed, "|") != "again|\r" {
		t.Fatalf("typed %q", typed)
	}
}

// A connection that is live and quiet, the server sending nothing of its
// own for longer than the bound (its pings are 30 s apart), is not taken
// for gone: the host pings it, the server answers, and a link's line is
// typed and entered after a pause longer than the bound, nothing held.
func TestALinksLineIsEnteredOverAQuietLiveConnection(t *testing.T) {
	a, _, viewer := liveHost(t, 3*liveTestWithin)
	guest := viewer("0123456789abcdef", "l1")
	time.Sleep(3 * liveTestWithin)
	untilVouches(t, a, true)
	guest.send(proto.Submit{T: proto.CtlSubmit, Text: "guest line"})
	if typed := guest.typed(guest.release(), 2); strings.Join(typed, "|") != "guest line|\r" {
		t.Fatalf("typed %q", typed)
	}
	guest.quiet(300 * time.Millisecond)
	if a.local.Held() {
		t.Fatal("the turn is held")
	}
}

// With no link's viewer, nor anything of theirs, the host does not ping:
// what it needs the server's word for is a link's. The owner's line does
// not need it. Once a link is held, the host pings, and hears the server.
func TestTheHostPingsOnlyForALinksViewer(t *testing.T) {
	a, _, viewer := liveHost(t, 50*time.Millisecond)
	owner := viewer("fedcba9876543210", "")
	untilVouches(t, a, false)
	owner.send(proto.Submit{T: proto.CtlSubmit, Text: "owner line"})
	if typed := owner.typed(owner.release(), 2); strings.Join(typed, "|") != "owner line|\r" {
		t.Fatalf("typed %q", typed)
	}
	held := a.creds.of("l1") // a viewer joining through it, as viewer_join adds one
	untilVouches(t, a, true)
	runtime.KeepAlive(held)
}

// A connection the host pings and hears nothing on for writeStall is given
// up, as one whose socket takes nothing for that long is: every link its
// viewers joined through counts as gone (connLost), and the host connects
// again once a connection carries.
func TestAConnectionPingedAndNeverHeardIsGivenUp(t *testing.T) {
	a, px, viewer := liveHostStalling(t, 50*time.Millisecond, 1500*time.Millisecond)
	guest := viewer("0123456789abcdef", "l1")
	first := a.live.Load()
	px.stall()
	deadline := time.Now().Add(10 * time.Second)
	for !errors.Is(guest.p.cred.err(), session.ErrRevoked) {
		if time.Now().After(deadline) {
			t.Fatal("the connection was kept")
		}
		time.Sleep(10 * time.Millisecond)
	}
	px.resume()
	for l := a.live.Load(); l == nil || l == first; l = a.live.Load() {
		if time.Now().After(deadline.Add(10 * time.Second)) {
			t.Fatal("the host did not connect again")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A frame of the server's that comes in slowly, larger than what comes in
// within the bound, is the server heard from as it comes: a link's line is
// typed and entered meanwhile, though nothing whole, not a pong, comes
// before the frame is done.
func TestALinksLineIsEnteredWhileALargeFrameComesInSlowly(t *testing.T) {
	conns := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(proto.MaxHostMessage + proto.MaxFrame)
		ctx := r.Context()
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
		reg, _ := json.Marshal(proto.Registered{T: proto.HostRegistered, SessionID: "s1", Secret: "secret", ShareBaseURL: "http://yard.invalid"})
		if c.Write(ctx, websocket.MessageText, reg) != nil {
			return
		}
		conns <- c
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	px := newStallProxy(t, strings.TrimPrefix(srv.URL, "http://"))
	a, viewer := liveHostAt(t, px.url(), 1500*time.Millisecond, 0)
	guest := viewer("0123456789abcdef", "l1")
	server := <-conns
	// 64 KiB at 5 KiB a second: about 13 s, the host's pongs behind it.
	px.trickle(256)
	big := []byte(`{"t":"something_else","pad":"` + strings.Repeat("p", 64<<10) + `"}`)
	go server.Write(context.Background(), websocket.MessageText, big)
	time.Sleep(2 * liveTestWithin)
	guest.send(proto.Submit{T: proto.CtlSubmit, Text: "guest line"})
	if typed := guest.typed(guest.release(), 2); strings.Join(typed, "|") != "guest line|\r" {
		t.Fatalf("typed %q", typed)
	}
	if a.local.Held() {
		t.Fatal("the turn is held")
	}
}

// A server that answers none of the host's pings but goes on sending,
// something after each ping among it, is heard from: the host does not
// give the connection up, however long its pings go unanswered.
func TestAConnectionHeardFromIsKeptThoughItsPongsDoNotCome(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pinged := make(chan struct{}, 16)
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OnPingReceived: func(context.Context, []byte) bool {
			select {
			case pinged <- struct{}{}:
			default:
			}
			return false // no pong
		}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		conns.Add(1)
		ctx := r.Context()
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
		reg, _ := json.Marshal(proto.Registered{T: proto.HostRegistered, SessionID: "s1", Secret: "secret", ShareBaseURL: "http://yard.invalid"})
		if c.Write(ctx, websocket.MessageText, reg) != nil {
			return
		}
		go func() {
			for {
				if _, _, err := c.Read(ctx); err != nil {
					return
				}
			}
		}()
		for {
			select {
			case <-pinged:
				if c.Write(ctx, websocket.MessageText, []byte(`{"t":"something_else"}`)) != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	a, _ := liveHostAt(t, srv.URL, 50*time.Millisecond, 1500*time.Millisecond)
	held := a.creds.of("l1")
	first := a.live.Load()
	time.Sleep(4 * time.Second)
	if n := conns.Load(); n != 1 || a.live.Load() != first {
		t.Fatalf("the host gave up a connection it heard from (%d connections)", n)
	}
	runtime.KeepAlive(held)
}

// serverQuiet is a link gone for the session (errors.Is ErrRevoked), and a
// host with no connection vouches for no link.
func TestServerQuietIsALinkGone(t *testing.T) {
	err := error(serverQuiet{within: time.Second})
	if !errors.Is(err, session.ErrRevoked) || errors.Is(err, session.ErrExpired) {
		t.Fatalf("%v: revoked %v, expired %v", err, errors.Is(err, session.ErrRevoked), errors.Is(err, session.ErrExpired))
	}
	a := &agent{}
	if !errors.Is(a.vouches(), session.ErrRevoked) {
		t.Fatal("a host with no connection vouches")
	}
	l := newConnLive()
	a.live.Store(l)
	if a.vouches() != nil {
		t.Fatal("a connection heard from now does not vouch")
	}
}

// Once a link's viewer has left and nothing of theirs is under way, no link
// is held, and the host stops pinging: nothing of the session keeps what it
// knew of the link. The viewer's side here holds nothing of the host's (its
// candidates find the peer by its id), as a browser's does not.
func TestNoLinkIsHeldOnceItsViewerLeft(t *testing.T) {
	a, proc, _ := newSubmitHost(t, 50*time.Millisecond, 0, 0)
	heardFromServer(t, a)
	out := make(chan any, 64)
	a.sendHook = func(v any) {
		select {
		case out <- v:
		default:
		}
	}
	const id = "0123456789abcdef"
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
		if p := a.peer(id); p != nil {
			p.handleICE(proto.ICECandidate{Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex})
		}
	})
	go func() {
		for v := range out {
			switch m := v.(type) {
			case proto.ViewerSDP:
				_ = viewerPC.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
			case proto.ViewerICE:
				_ = viewerPC.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate.Candidate, SDPMid: m.Candidate.SDPMid, SDPMLineIndex: m.Candidate.SDPMLineIndex})
			}
		}
	}()
	offer, err := viewerPC.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := viewerPC.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	a.addPeer(id, session.RoleControl, "l1", "") // as a viewer_join does
	if err := a.peer(id).handleOffer(offer.SDP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	case <-time.After(15 * time.Second):
		t.Fatal("data channel did not open")
	}
	v := &submitViewer{t: t, proc: proc, dc: dc, frames: frames}
	v.send(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 80, Rows: 24, Name: "Guest"})
	v.until(func(m map[string]any) bool { return m["t"] == proto.CtlReady })
	v.sync()
	v.send(proto.Submit{T: proto.CtlSubmit, Text: "guest line"})
	if typed := v.typed(v.release(), 2); strings.Join(typed, "|") != "guest line|\r" {
		t.Fatalf("typed %q", typed)
	}
	if !a.creds.held() {
		t.Fatal("no link held while its viewer is attached")
	}
	a.removePeer(id) // as a viewer_leave does
	deadline := time.Now().Add(10 * time.Second)
	for a.creds.held() {
		if time.Now().After(deadline) {
			t.Fatal("a link is still held once its viewer left")
		}
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
}
