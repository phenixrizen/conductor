package hostagent

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/session"
)

// What the host writes on its control connection goes out one message at a
// time, from one goroutine per connection (controlWriter): the host's own
// messages first (its status, its attention and activity, the answers and
// candidates of its WebRTC viewers, its link requests), then the frames
// relayed to its viewers, each viewer's in the order its frames come and the
// viewers in turn. A message is handed to the connection once what it holds
// unsent is under a mark; until then it waits its turn, for a bounded time
// of its own, and one whose time runs out is withdrawn, never written late.
//
// Under the WebSocket library, the connection's socket is a pacedConn: a
// write to it never waits on the socket, which a goroutine of its own feeds,
// and the connection is given up only when the socket takes nothing at all
// for writeStall. A server may read the connection slowly on purpose (a
// switchyard's bound on what a host relays, which a host shares with the
// other open hosts of its address): what the host sends then waits, here,
// in its turn. A write that must be done within a time limit, as each of the
// host's was and as the library's answer to a ping is, closed the
// connection, and with it every viewer of the session, WebRTC ones included,
// once the server had paused for longer than that; now a relayed viewer
// whose frame waits too long is let go alone, as a slow consumer, and the
// connection stays.
const (
	// controlTurnWait bounds how long one of the host's own messages waits
	// for its turn; one that waits longer is dropped, as a write that timed
	// out was.
	controlTurnWait = 15 * time.Second
	// relayTurnWait bounds how long a frame relayed to a viewer waits for
	// its turn, as long as a frame for a WebRTC viewer waits for room in its
	// data channel (dcSendTimeout): past it the viewer is let go as a slow
	// consumer.
	relayTurnWait = dcSendTimeout
	// writeStall is how long the socket may take nothing at all before the
	// connection is taken for dead and closed. It is longer than a live
	// server pauses its reading: a switchyard's bound on what open hosts
	// relay pauses it until the address has paid for what its hosts sent,
	// about 35 s at the default bound with the most sessions an address may
	// publish each sending a frame of the largest size.
	writeStall = 2 * time.Minute
	// sendMark is what a connection may hold unsent before the next message
	// waits: little, so that a message of the host's own waits for no more
	// than one message ahead of it and what the socket holds.
	sendMark = 64 << 10
	// sendChunk is the most the socket is handed at once, each a sign that
	// it still takes what it is given.
	sendChunk = 64 << 10
	// unsentLimit bounds what a connection holds unsent. The writer adds a
	// message only under sendMark, so past the mark there is at most one
	// message (a relayed frame of the largest size, proto.MaxFrame, in its
	// envelope) and the library's own small frames, its answers to pings
	// among them: more is a server that pings and does not read, and the
	// connection is closed.
	unsentLimit = 4 << 20
)

var (
	// errConnDown is a message's error when the connection is down or ends
	// before the message is written.
	errConnDown = errors.New("host: control connection down")
	// errViewerGone is a relayed frame's error when its viewer goes while
	// the frame waits for its turn.
	errViewerGone = errors.New("host: viewer gone")
	// errTooMuchUnsent is a connection's failure when it would hold more
	// than unsentLimit unsent.
	errTooMuchUnsent = errors.New("host: the server takes too little of the control connection")
)

// pacedConn is a control connection's socket (see above). Writes are kept
// in order and never wait; pump hands them to the socket, sendChunk at a
// time, each within the stall, and the first failure is every later write's
// error and closes the socket. Close drops what is still kept: a host that
// lets the connection go on purpose waits for it to empty first (settle).
type pacedConn struct {
	net.Conn
	stall time.Duration
	// limit is unsentLimit (a test's own).
	limit int

	mu sync.Mutex
	// kept is what waits for the socket; sending, what pump is handing it
	// now (both count as unsent).
	kept    bytes.Buffer
	sending int
	err     error
	// more wakes pump; less is closed (and replaced) when what is unsent
	// shrinks or the connection fails.
	more chan struct{}
	less chan struct{}
}

func newPacedConn(c net.Conn, stall time.Duration) *pacedConn {
	if stall <= 0 {
		stall = writeStall
	}
	p := &pacedConn{Conn: c, stall: stall, limit: unsentLimit, more: make(chan struct{}, 1), less: make(chan struct{})}
	go p.pump()
	return p
}

// Write keeps b to be sent, after what was written before, and returns at
// once; the socket's failure, once it has failed. Past unsentLimit the
// connection fails and closes.
func (p *pacedConn) Write(b []byte) (int, error) {
	p.mu.Lock()
	if p.err != nil {
		err := p.err
		p.mu.Unlock()
		return 0, err
	}
	if p.kept.Len()+p.sending+len(b) > p.limit {
		p.mu.Unlock()
		p.fail(errTooMuchUnsent)
		_ = p.Conn.Close()
		return 0, errTooMuchUnsent
	}
	p.kept.Write(b)
	p.mu.Unlock()
	select {
	case p.more <- struct{}{}:
	default:
	}
	return len(b), nil
}

// SetWriteDeadline is pump's alone: writes to a pacedConn never wait.
func (p *pacedConn) SetWriteDeadline(time.Time) error { return nil }

// SetDeadline sets the read deadline (see SetWriteDeadline).
func (p *pacedConn) SetDeadline(t time.Time) error { return p.Conn.SetReadDeadline(t) }

// Close drops what is unsent and closes the socket.
func (p *pacedConn) Close() error {
	p.fail(net.ErrClosed)
	return p.Conn.Close()
}

// unsent is what was written and the socket has not taken, and why the
// connection failed (nil while it has not), with a channel closed once
// unsent shrinks or the connection fails. A nil pacedConn (a connection
// dialed without one) holds nothing.
func (p *pacedConn) unsent() (int, <-chan struct{}, error) {
	if p == nil {
		return 0, nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.kept.Len() + p.sending, p.less, p.err
}

// fail records the connection's failure, the first one: what is kept is
// dropped, and whoever waits on it is woken.
func (p *pacedConn) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return
	}
	p.err = err
	p.kept.Reset()
	p.sending = 0
	close(p.less)
	p.less = make(chan struct{})
	select {
	case p.more <- struct{}{}:
	default:
	}
}

// pump hands what is kept to the socket until the connection fails.
func (p *pacedConn) pump() {
	buf := make([]byte, sendChunk)
	for {
		p.mu.Lock()
		for p.err == nil && p.kept.Len() == 0 {
			p.mu.Unlock()
			<-p.more
			p.mu.Lock()
		}
		if p.err != nil {
			p.mu.Unlock()
			return
		}
		n, _ := p.kept.Read(buf)
		p.sending = n
		p.mu.Unlock()
		_ = p.Conn.SetWriteDeadline(time.Now().Add(p.stall))
		_, err := p.Conn.Write(buf[:n])
		if err != nil {
			p.fail(err)
			_ = p.Conn.Close()
			return
		}
		p.mu.Lock()
		p.sending = 0
		close(p.less)
		p.less = make(chan struct{})
		p.mu.Unlock()
	}
}

// pacedClient is base (http.DefaultClient's transport when nil) with each
// connection it dials made a pacedConn of the given stall; dialed returns
// the last one. Its transport is its own: call its CloseIdleConnections once
// the dial is done.
func pacedClient(base *http.Client, stall time.Duration) (client *http.Client, dialed func() *pacedConn) {
	if base == nil {
		base = &http.Client{}
	}
	t, ok := base.Transport.(*http.Transport)
	if base.Transport == nil {
		t, ok = http.DefaultTransport.(*http.Transport)
	}
	if !ok {
		return base, func() *pacedConn { return nil }
	}
	t = t.Clone()
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	var mu sync.Mutex
	var last *pacedConn
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		pc := newPacedConn(c, stall)
		mu.Lock()
		last = pc
		mu.Unlock()
		return pc, nil
	}
	// The transport layers TLS over what it dials.
	t.DialTLSContext = nil
	out := *base
	out.Transport = t
	return &out, func() *pacedConn {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// outMessage is one message waiting for, or taking, its turn.
type outMessage struct {
	typ  websocket.MessageType
	data []byte
	// taken says the writer has it; under the writer's mu.
	taken bool
	// done carries the write's outcome, once: from the writer, or from the
	// connection's end while the message still waits.
	done chan error
}

// controlWriter writes the messages of one control connection, c over pc
// (see above).
type controlWriter struct {
	c  *websocket.Conn
	pc *pacedConn

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	control []*outMessage
	relay   []*outMessage
	stopped bool
}

// newControlWriter starts the writer of c, whose socket is pc.
func newControlWriter(c *websocket.Conn, pc *pacedConn) *controlWriter {
	ctx, cancel := context.WithCancel(context.Background())
	w := &controlWriter{c: c, pc: pc, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go w.run()
	return w
}

// sendControl writes one of the host's own messages, ahead of the relayed
// frames, once its turn comes within controlTurnWait.
func (w *controlWriter) sendControl(typ websocket.MessageType, data []byte) error {
	return w.put(true, typ, data, controlTurnWait, nil)
}

// sendRelay writes a frame relayed to a viewer once its turn comes within
// turn: session.ErrSlowConsumer when it does not, errViewerGone when gone
// closes first. Either way the frame is withdrawn, and the connection is
// left as it is.
func (w *controlWriter) sendRelay(data []byte, turn time.Duration, gone <-chan struct{}) error {
	return w.put(false, websocket.MessageBinary, data, turn, gone)
}

// unsent is what the connection holds unsent.
func (w *controlWriter) unsent() int {
	n, _, _ := w.pc.unsent()
	return n
}

// put queues a message in its lane and waits until it is written or its
// wait for its turn ends (turn, gone): then it is withdrawn.
func (w *controlWriter) put(control bool, typ websocket.MessageType, data []byte, turn time.Duration, gone <-chan struct{}) error {
	m := &outMessage{typ: typ, data: data, done: make(chan error, 1)}
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return errConnDown
	}
	if control {
		w.control = append(w.control, m)
	} else {
		w.relay = append(w.relay, m)
	}
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	t := time.NewTimer(turn)
	defer t.Stop()
	why := session.ErrSlowConsumer
	select {
	case err := <-m.done:
		return err
	case <-t.C:
	case <-gone:
		why = errViewerGone
	}
	w.mu.Lock()
	if m.taken {
		// Handed to the connection already, which takes no time.
		w.mu.Unlock()
		return <-m.done
	}
	w.control = slices.DeleteFunc(w.control, func(o *outMessage) bool { return o == m })
	w.relay = slices.DeleteFunc(w.relay, func(o *outMessage) bool { return o == m })
	w.mu.Unlock()
	// Withdrawn, unless the connection's end answered it meanwhile.
	select {
	case err := <-m.done:
		return err
	default:
		return why
	}
}

// run hands the messages to the connection in their turn, each once the
// connection holds less than sendMark unsent, until the connection fails or
// the writer is stopped.
func (w *controlWriter) run() {
	defer close(w.done)
	defer w.stop()
	for {
		n, less, err := w.pc.unsent()
		if err != nil {
			return
		}
		if n > sendMark {
			select {
			case <-less:
			case <-w.ctx.Done():
				return
			}
			continue
		}
		w.mu.Lock()
		if w.stopped {
			w.mu.Unlock()
			return
		}
		var m *outMessage
		switch {
		case len(w.control) > 0:
			m, w.control = w.control[0], w.control[1:]
		case len(w.relay) > 0:
			m, w.relay = w.relay[0], w.relay[1:]
		}
		if m != nil {
			m.taken = true
		}
		w.mu.Unlock()
		if m == nil {
			select {
			case <-w.wake:
			case <-w.ctx.Done():
				return
			}
			continue
		}
		// A write to a pacedConn does not wait on the socket.
		err = w.c.Write(w.ctx, m.typ, m.data)
		m.done <- err
		if err != nil {
			// A message cut short leaves the connection unusable.
			w.c.CloseNow()
			return
		}
	}
}

// stop ends the writer: what waits is answered errConnDown, as is every
// message put after. It does not wait for the writer's goroutine, which
// ends at once.
func (w *controlWriter) stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	waiting := append(w.control, w.relay...)
	w.control, w.relay = nil, nil
	w.mu.Unlock()
	w.cancel()
	for _, m := range waiting {
		m.done <- errConnDown
	}
}
