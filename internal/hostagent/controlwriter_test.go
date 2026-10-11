package hostagent

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
)

// heldServer is a WebSocket server that reads what a host sends only once
// it is let (read), and hands over what it reads, in order.
type heldServer struct {
	read chan struct{}
	got  chan []byte
	conn chan *websocket.Conn
}

// dialHeld starts a heldServer and dials it as a host does, its socket a
// pacedConn of the given stall with a small send buffer, so that it backs up
// soon after the server stops reading.
func dialHeld(t *testing.T, stall time.Duration) (*heldServer, *websocket.Conn, *pacedConn) {
	t.Helper()
	s := &heldServer{read: make(chan struct{}), got: make(chan []byte, 64), conn: make(chan *websocket.Conn, 1)}
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(8 << 20)
		s.conn <- c
		select {
		case <-s.read:
		case <-done:
			return
		}
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			s.got <- data
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) })
	dialer := &net.Dialer{Control: func(_, _ string, rc syscall.RawConn) error {
		var serr error
		_ = rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, 4<<10) })
		return serr
	}}
	hc, dialed := pacedClient(&http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}, stall)
	c, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	pc := dialed()
	if pc == nil {
		t.Fatal("the connection was not dialed paced")
	}
	return s, c, pc
}

// backedUp waits until pc holds more than it may before the next message
// goes: the socket under it takes nothing more.
func backedUp(t *testing.T, pc *pacedConn) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, _, err := pc.unsent()
		if err != nil {
			t.Fatalf("the connection failed: %v", err)
		}
		if n > sendMark {
			// Held, not merely on its way: still held a moment later.
			time.Sleep(100 * time.Millisecond)
			if m, _, _ := pc.unsent(); m > sendMark {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the connection never backed up (%d unsent)", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waiting is how many messages wait in w's lanes.
func waiting(w *controlWriter) (control, relay int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.control), len(w.relay)
}

func waitLanes(t *testing.T, w *controlWriter, control, relay int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, r := waiting(w)
		if c == control && r == relay {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lanes hold %d and %d, want %d and %d", c, r, control, relay)
		}
		time.Sleep(time.Millisecond)
	}
}

// expectGot reads what the server got next and compares it with want.
func (s *heldServer) expectGot(t *testing.T, want []byte) {
	t.Helper()
	select {
	case got := <-s.got:
		if !bytes.Equal(got, want) {
			t.Fatalf("the server got %d bytes %.12q, want %d bytes %.12q", len(got), got, len(want), want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the server got nothing; want %.12q", want)
	}
}

// While the server does not read, a write to the connection still returns
// at once; what follows waits its turn, the host's own messages ahead of the
// frames relayed to its viewers. Once the server reads, it gets them all in
// that order.
func TestTheHostsOwnMessagesGoAheadOfRelayedFrames(t *testing.T) {
	s, c, pc := dialHeld(t, 0)
	w := newControlWriter(c, pc)
	t.Cleanup(w.stop)
	big := bytes.Repeat([]byte("b"), 1<<20)
	start := time.Now()
	if err := w.sendRelay(big, time.Second, nil); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("a write waited %v on a connection the server does not read", took)
	}
	backedUp(t, pc)
	relayed := make(chan error, 1)
	go func() { relayed <- w.sendRelay([]byte("relayed"), time.Minute, nil) }()
	waitLanes(t, w, 0, 1)
	own := make(chan error, 1)
	go func() { own <- w.sendControl(websocket.MessageText, []byte(`{"t":"status"}`)) }()
	waitLanes(t, w, 1, 1)
	close(s.read)
	s.expectGot(t, big)
	s.expectGot(t, []byte(`{"t":"status"}`))
	s.expectGot(t, []byte("relayed"))
	for _, ch := range []chan error{own, relayed} {
		if err := <-ch; err != nil {
			t.Fatal(err)
		}
	}
}

// A relayed frame whose turn does not come within its wait is withdrawn as
// a slow consumer's, and one whose viewer goes is withdrawn at once: neither
// is ever written, and the connection stays, carrying what comes after.
func TestARelayedFrameThatWaitsTooLongIsWithdrawnAndTheConnectionStays(t *testing.T) {
	s, c, pc := dialHeld(t, 0)
	w := newControlWriter(c, pc)
	t.Cleanup(w.stop)
	big := bytes.Repeat([]byte("b"), 1<<20)
	if err := w.sendRelay(big, time.Second, nil); err != nil {
		t.Fatal(err)
	}
	backedUp(t, pc)
	start := time.Now()
	if err := w.sendRelay([]byte("late"), 200*time.Millisecond, nil); !errors.Is(err, session.ErrSlowConsumer) {
		t.Fatalf("a frame that waited past its turn: %v", err)
	}
	if took := time.Since(start); took < 200*time.Millisecond || took > 2*time.Second {
		t.Fatalf("it was withdrawn after %v", took)
	}
	gone := make(chan struct{})
	left := make(chan error, 1)
	go func() { left <- w.sendRelay([]byte("gone"), time.Minute, gone) }()
	waitLanes(t, w, 0, 1)
	close(gone)
	select {
	case err := <-left:
		if !errors.Is(err, errViewerGone) {
			t.Fatalf("a frame whose viewer went: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("a frame whose viewer went went on waiting")
	}
	if _, _, err := pc.unsent(); err != nil {
		t.Fatalf("the connection failed: %v", err)
	}
	close(s.read)
	after := make(chan error, 1)
	go func() { after <- w.sendRelay([]byte("after"), time.Minute, nil) }()
	s.expectGot(t, big)
	s.expectGot(t, []byte("after"))
	if err := <-after; err != nil {
		t.Fatal(err)
	}
}

// The library answers a ping whatever the connection holds unsent: an answer
// it cannot write within its own time limit (5 s) would end the reading of
// the connection, and with it the host's every viewer.
func TestAPingIsAnsweredWhileTheConnectionIsBackedUp(t *testing.T) {
	s, c, pc := dialHeld(t, 0)
	w := newControlWriter(c, pc)
	t.Cleanup(w.stop)
	if err := w.sendRelay(bytes.Repeat([]byte("b"), 1<<20), time.Second, nil); err != nil {
		t.Fatal(err)
	}
	backedUp(t, pc)
	readErr := make(chan error, 1)
	go func() {
		_, _, err := c.Read(t.Context())
		readErr <- err
	}()
	server := <-s.conn
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		_ = server.Ping(ctx) // its answer waits behind what is unsent
	}()
	select {
	case err := <-readErr:
		t.Fatalf("the host's reading ended while the connection was backed up: %v", err)
	case <-time.After(7 * time.Second):
	}
	if _, _, err := pc.unsent(); err != nil {
		t.Fatalf("the connection failed: %v", err)
	}
}

// A socket that takes nothing at all for the stall is taken for dead: the
// connection is closed, what waited is answered as the connection's end,
// and so is what is sent after.
func TestASocketThatTakesNothingForTheStallIsGivenUp(t *testing.T) {
	_, c, pc := dialHeld(t, 300*time.Millisecond)
	w := newControlWriter(c, pc)
	t.Cleanup(w.stop)
	if err := w.sendRelay(bytes.Repeat([]byte("b"), 1<<20), time.Second, nil); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- w.sendRelay([]byte("next"), time.Minute, nil) }()
	select {
	case err := <-waited:
		if !errors.Is(err, errConnDown) {
			t.Fatalf("a frame waiting as the connection was given up: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a socket that took nothing was never given up")
	}
	if _, _, err := pc.unsent(); err == nil {
		t.Fatal("the connection did not record its failure")
	}
	if err := w.sendControl(websocket.MessageText, []byte(`{}`)); !errors.Is(err, errConnDown) {
		t.Fatalf("a message after the connection was given up: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("the connection was not closed: %v", err)
	}
}

// A relayed viewer whose frames do not get their turn on the control
// connection in time is let go alone, as a slow consumer: the session goes
// on for its other viewers, the connection stays, and the server is told
// the viewer is closed once the connection takes it.
func TestARelayedViewerWhoseFramesWaitTooLongIsLetGoAlone(t *testing.T) {
	s, c, pc := dialHeld(t, 0)
	dir := t.TempDir()
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: dir, Env: hostEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Stop(context.Background(), time.Second)
	a := &agent{opts: Options{}, proc: proc, peers: map[string]*peer{}, log: discardLog, sessID: "sess-1", relayWait: 300 * time.Millisecond}
	w := a.useConn(c, pc)
	t.Cleanup(w.stop)
	a.local = session.NewLocal(session.Info{ID: "sess-1", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{})
	big := bytes.Repeat([]byte("b"), 1<<20)
	if err := w.sendRelay(big, time.Second, nil); err != nil {
		t.Fatal(err)
	}
	backedUp(t, pc)

	healthy := &outputCount{}
	hsub, err := a.local.AttachWith(session.AttachOptions{ID: "local-1", Role: session.RoleControl, Name: "healthy"}, healthy)
	if err != nil {
		t.Fatal(err)
	}
	const relayed = "relay-0123456789"
	rsub, err := a.local.AttachWith(session.AttachOptions{ID: relayed, Role: session.RoleView, Name: "relayed"}, newRelaySink(a, relayed))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-rsub.Done():
		if !errors.Is(rsub.Reason(), session.ErrSlowConsumer) {
			t.Fatalf("the relayed viewer ended: %v", rsub.Reason())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the relayed viewer was never let go")
	}
	if err := a.local.Input(hsub, []byte("still here\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for healthy.bytes() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the other viewer was sent no output")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r := hsub.Reason(); r != nil {
		t.Fatalf("the other viewer was closed: %v", r)
	}
	if _, _, err := pc.unsent(); err != nil {
		t.Fatalf("the connection failed: %v", err)
	}
	close(s.read)
	s.expectGot(t, big)
	// Why, if its turn came in time, and then the close.
	for {
		select {
		case got := <-s.got:
			if bytes.Contains(got, []byte(`"slow_consumer"`)) && bytes.Contains(got, []byte(relayed)) {
				continue
			}
			if !bytes.Contains(got, []byte(`"viewer_closed"`)) || !bytes.Contains(got, []byte(relayed)) {
				t.Fatalf("the server got %.80q, want the viewer's close", got)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the server was never told the viewer is closed")
		}
		break
	}
}

// An attention change the server was not told (no connection, or no turn on
// it in time) is told with the session's next change, though the attention
// itself did not change again.
func TestAnAttentionChangeNotSentIsSentWithTheNextChange(t *testing.T) {
	a := &agent{opts: Options{}, peers: map[string]*peer{}, log: discardLog, sessID: "sess-1"}
	info := session.Info{ID: "sess-1", Attention: session.Attention{State: session.AttentionNeedsInput, Message: "Allow?", Source: "hook"}}
	a.onLocalChange(info) // no connection: not told
	s, c, pc := dialHeld(t, 0)
	close(s.read)
	t.Cleanup(a.useConn(c, pc).stop)
	a.onLocalChange(info)
	select {
	case got := <-s.got:
		if !bytes.Contains(got, []byte(`"needs_input"`)) {
			t.Fatalf("the server got %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the attention was never told")
	}
	// Told now: the same again is not sent again.
	a.onLocalChange(info)
	select {
	case got := <-s.got:
		t.Fatalf("the server got %q again", got)
	case <-time.After(200 * time.Millisecond):
	}
}

// A host dials a server over TLS, one that offers HTTP/2 besides, through
// the paced transport made from the default one, as conductor host dials a
// switchyard: the upgrade is made, and what the host writes arrives.
func TestAPacedDialWorksOverTLS(t *testing.T) {
	got := make(chan []byte, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		if _, data, err := c.Read(r.Context()); err == nil {
			got <- data
		}
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	hc, dialed := pacedClient(&http.Client{Transport: tr}, 0)
	c, _, err := websocket.Dial(t.Context(), "wss"+strings.TrimPrefix(srv.URL, "https"), &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	w := newControlWriter(c, dialed())
	t.Cleanup(w.stop)
	if w.pc == nil {
		t.Fatal("the connection was not dialed paced")
	}
	if err := w.sendControl(websocket.MessageText, []byte(`{"t":"status"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-got:
		if string(data) != `{"t":"status"}` {
			t.Fatalf("the server got %q", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server got nothing")
	}
}

// What a connection holds unsent is bounded: the writer adds a message only
// under its mark, and the library's own frames (its answers to pings) that
// would take it past the limit close the connection instead.
func TestAConnectionHoldsBoundedUnsent(t *testing.T) {
	_, c, pc := dialHeld(t, 0)
	w := newControlWriter(c, pc)
	t.Cleanup(w.stop)
	if err := w.sendRelay(bytes.Repeat([]byte("b"), 1<<20), time.Second, nil); err != nil {
		t.Fatal(err)
	}
	backedUp(t, pc)
	pc.mu.Lock()
	pc.limit = pc.kept.Len() + pc.sending + 1024
	pc.mu.Unlock()
	if _, err := pc.Write(make([]byte, 512)); err != nil {
		t.Fatalf("a write within the limit: %v", err)
	}
	if _, err := pc.Write(make([]byte, 1024)); !errors.Is(err, errTooMuchUnsent) {
		t.Fatalf("a write past the limit: %v", err)
	}
	if _, _, err := pc.unsent(); !errors.Is(err, errTooMuchUnsent) {
		t.Fatalf("the connection's failure: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("the connection was not closed: %v", err)
	}
}

// Stopping the writer, as the connection ends, answers what waits at once.
func TestStoppingTheWriterAnswersWhatWaits(t *testing.T) {
	_, c, pc := dialHeld(t, 0)
	w := newControlWriter(c, pc)
	if err := w.sendRelay(bytes.Repeat([]byte("b"), 1<<20), time.Second, nil); err != nil {
		t.Fatal(err)
	}
	backedUp(t, pc)
	waited := make(chan error, 2)
	go func() { waited <- w.sendRelay([]byte("r"), time.Minute, nil) }()
	go func() { waited <- w.sendControl(websocket.MessageText, []byte(`{}`)) }()
	waitLanes(t, w, 1, 1)
	w.stop()
	for range 2 {
		select {
		case err := <-waited:
			if !errors.Is(err, errConnDown) {
				t.Fatalf("a message waiting as the writer stopped: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("a message went on waiting after the writer stopped")
		}
	}
}
