package hostagent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
)

// outputCount is a viewer that reads at once and counts the output.
type outputCount struct {
	mu  sync.Mutex
	out int
}

func (*outputCount) Transport() string { return "test" }
func (s *outputCount) WriteFrame(f []byte) error {
	if fr, err := proto.Decode(f); err == nil && fr.Type == proto.TypeOutput {
		s.mu.Lock()
		s.out += len(fr.Payload)
		s.mu.Unlock()
	}
	return nil
}
func (*outputCount) Close(error) {}

func (s *outputCount) bytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out
}

// readBufListener gives each accepted connection a small receive buffer.
type readBufListener struct{ net.Listener }

func (l readBufListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(4 << 10)
	}
	return c, err
}

// A relay viewer's frames go out on the host's one control connection. When
// that connection falls behind while the agent prints fast, the relay viewer
// is dropped as a slow consumer, and its sink's last words (an error frame and
// the close for the server) wait on the connection on the viewer's own
// goroutine: the host's session goes on for its other viewers and its input.
func TestARelayViewerThatFallsBehindDoesNotStallTheHostSession(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		<-release // a server that does not read the host
	}))
	srv.Listener = readBufListener{srv.Listener}
	srv.Start()
	defer srv.Close()
	defer close(release)
	dialer := &net.Dialer{Control: func(_, _ string, rc syscall.RawConn) error {
		var serr error
		_ = rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, 4<<10) })
		return serr
	}}
	hc := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}
	conn, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	dir := t.TempDir()
	// About 6 MB/s: far more than the stalled connection takes, and far
	// less than a viewer that reads at once falls behind on.
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/sh", "-c", "while :; do head -c 65536 /dev/zero; sleep 0.01; done"}, Dir: dir, Env: hostEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Stop(context.Background(), time.Second)
	a := &agent{opts: Options{}, proc: proc, peers: map[string]*peer{}, log: discardLog, sessID: "sess-1", conn: conn}
	a.local = session.NewLocal(session.Info{ID: "sess-1", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{})

	healthy := &outputCount{}
	hsub, err := a.local.AttachWith(session.AttachOptions{ID: "local-1", Role: session.RoleControl, Name: "healthy"}, healthy)
	if err != nil {
		t.Fatal(err)
	}
	rsub, err := a.local.AttachWith(session.AttachOptions{ID: "relay-0123456789", Role: session.RoleView, Name: "relay"}, &relaySink{a: a, viewerID: "relay-0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !errors.Is(rsub.Reason(), session.ErrSlowConsumer) {
		if time.Now().After(deadline) {
			t.Fatalf("the relay viewer was never dropped (%v; %d bytes of output)", rsub.Reason(), healthy.bytes())
		}
		time.Sleep(time.Millisecond)
	}
	before := healthy.bytes()
	for name, fn := range map[string]func(){
		"Info":  func() { _ = a.local.Info() },
		"Input": func() { _ = a.local.Input(hsub, []byte("k")) },
	} {
		done := make(chan struct{})
		go func() { fn(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Errorf("%s waited on the control connection", name)
		}
	}
	deadline = time.Now().Add(time.Second)
	for healthy.bytes() == before {
		if time.Now().After(deadline) {
			t.Fatal("the other viewer stopped receiving the output")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r := hsub.Reason(); r != nil {
		t.Fatalf("the other viewer was closed: %v", r)
	}
}

// typedProc is a process that prints nothing and counts what it is typed.
type typedProc struct {
	done  chan struct{}
	once  sync.Once
	mu    sync.Mutex
	typed []byte
}

func (p *typedProc) Read([]byte) (int, error) { <-p.done; return 0, io.EOF }
func (p *typedProc) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.typed = append(p.typed, b...)
	p.mu.Unlock()
	return len(b), nil
}
func (p *typedProc) Resize(uint16, uint16) error { return nil }
func (p *typedProc) Done() <-chan struct{}       { return p.done }
func (p *typedProc) Exit() pty.ExitStatus        { return pty.ExitStatus{} }
func (p *typedProc) Stop(context.Context, time.Duration) error {
	p.once.Do(func() { close(p.done) })
	return nil
}

func (p *typedProc) text() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return string(p.typed)
}

// A viewer the session has ended (dropped as a slow consumer, which leaves
// the peer holding it until its connection closes) is not acted on: what it
// sends after is not typed into the agent.
func TestAViewerTheSessionEndedIsNotActedOn(t *testing.T) {
	proc := &typedProc{done: make(chan struct{})}
	defer proc.Stop(context.Background(), 0)
	a := &agent{opts: Options{}, peers: map[string]*peer{}, log: discardLog, sessID: "sess-1"}
	a.local = session.NewLocal(session.Info{ID: "sess-1", Cwd: t.TempDir(), Cols: 80, Rows: 24}, proc, session.Options{})
	const id = "viewer-012345678"
	sub, err := a.local.AttachWith(session.AttachOptions{ID: id, Role: session.RoleControl, Name: "viewer"}, &outputCount{})
	if err != nil {
		t.Fatal(err)
	}
	p := newPeer(a, id, session.RoleControl, "", "")
	p.sub = sub
	p.handleFrame(proto.Frame{Type: proto.TypeInput, Payload: []byte("before")})
	if got := proc.text(); got != "before" {
		t.Fatalf("typed %q", got)
	}
	a.local.Detach(sub) // the session ends it; the peer still holds it
	p.handleFrame(proto.Frame{Type: proto.TypeInput, Payload: []byte("after")})
	if got := proc.text(); got != "before" {
		t.Fatalf("the ended viewer's input was typed: %q", got)
	}
}
