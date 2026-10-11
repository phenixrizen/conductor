package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
	"github.com/phenixrizen/conductor/internal/signal"
)

const (
	writeTimeout = 15 * time.Second
	helloTimeout = 5 * time.Second
	readLimit    = 64 << 10
)

// defaultPingEvery is how often a connection is pinged.
const defaultPingEvery = 30 * time.Second

// pingEvery is defaultPingEvery, which a server takes when it is made
// (Server.pingEvery), so a test may shorten it for the servers it makes.
var pingEvery = defaultPingEvery

// wsSink writes session frames to a viewer WebSocket and translates the close
// reason into a close code.
type wsSink struct {
	c      *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func newWSSink(c *websocket.Conn) *wsSink {
	ctx, cancel := context.WithCancel(context.Background())
	return &wsSink{c: c, ctx: ctx, cancel: cancel}
}

func (s *wsSink) WriteFrame(frame []byte) error {
	ctx, cancel := context.WithTimeout(s.ctx, writeTimeout)
	defer cancel()
	return s.c.Write(ctx, websocket.MessageBinary, frame)
}

func (s *wsSink) Close(reason error) {
	s.once.Do(func() {
		code, msg := closeCodeFor(reason)
		if reason != nil {
			// Best-effort error frame before the close so the UI can show why.
			if errCode := errorCodeFor(reason); errCode != "" {
				_ = s.WriteFrame(proto.NewError(errCode, msg))
			}
		}
		_ = s.c.Close(code, msg)
		s.cancel()
	})
}

func closeCodeFor(reason error) (websocket.StatusCode, string) {
	switch {
	case reason == nil:
		return websocket.StatusNormalClosure, "bye"
	case errors.Is(reason, session.ErrExpired):
		return proto.CloseForbidden, "link expired"
	case errors.Is(reason, session.ErrRevoked):
		return proto.CloseForbidden, "link revoked"
	case errors.Is(reason, session.ErrSlowConsumer), errors.Is(reason, signal.ErrSlowViewer):
		return websocket.StatusPolicyViolation, "slow consumer"
	case errors.Is(reason, errShuttingDown):
		return websocket.StatusGoingAway, "server shutting down"
	case errors.Is(reason, signal.ErrHostGone):
		return proto.CloseSessionEnded, "host disconnected"
	case errors.Is(reason, session.ErrSessionEnded):
		return proto.CloseSessionEnded, "session ended"
	}
	return websocket.StatusInternalError, "connection closed"
}

func errorCodeFor(reason error) string {
	switch {
	case errors.Is(reason, session.ErrExpired):
		return proto.ErrCodeExpired
	case errors.Is(reason, session.ErrRevoked):
		return proto.ErrCodeRevoked
	case errors.Is(reason, session.ErrSlowConsumer), errors.Is(reason, signal.ErrSlowViewer):
		return proto.ErrCodeSlowConsumer
	case errors.Is(reason, signal.ErrHostGone):
		return proto.ErrCodeHostDisconnected
	case errors.Is(reason, session.ErrSessionEnded):
		return proto.ErrCodeSessionEnded
	}
	return ""
}

// viewerSeam lets a test order what a hosted viewer's connection does as a
// network may (serveHostedViewer). read, when set, is called before each read
// of the connection, with a channel closed once the viewer's writer has
// ended; write, when set, is called before each frame the writer writes, and
// an error it returns fails that write.
type viewerSeam struct {
	read  func(written <-chan struct{})
	write func() error
}

// seamSink is a sink whose writes fail when the seam says so.
type seamSink struct {
	*wsSink
	fail func() error
}

func (s seamSink) WriteFrame(frame []byte) error {
	if err := s.fail(); err != nil {
		return err
	}
	return s.wsSink.WriteFrame(frame)
}

// readFrame reads one binary frame with a deadline.
func readFrame(ctx context.Context, c *websocket.Conn, timeout time.Duration) (proto.Frame, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	typ, data, err := c.Read(ctx)
	if err != nil {
		return proto.Frame{}, err
	}
	if typ != websocket.MessageBinary {
		return proto.Frame{}, errors.New("text frames are not accepted")
	}
	return proto.Decode(data)
}

// keepalive pings the peer every `every` until ctx ends or a ping fails.
func keepalive(ctx context.Context, c *websocket.Conn, every time.Duration) {
	if every <= 0 {
		every = defaultPingEvery // a Server made without New
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// keepaliveUntilGone pings the peer every `every` until ctx ends, and calls
// gone, once, when a ping cannot be written: the connection has failed. A
// pong that does not come is no such sign, for pongs are read by the
// connection's reader, which may be held meanwhile (a hosted viewer slowed
// by its address's budget, or waiting for room in its host's queue); the
// pings go on. A write that times out closes the connection, and the next
// ping says so.
func keepaliveUntilGone(ctx context.Context, c *websocket.Conn, every time.Duration, gone func()) {
	if every <= 0 {
		every = defaultPingEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, min(writeTimeout, every))
			err := c.Ping(pctx)
			cancel()
			if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				gone()
				return
			}
		}
	}
}

// hostGone ends a hosted viewer whose host has left. ErrHostGone comes back
// only once HostDisconnected has detached the host, and it closes every viewer
// right after, so Pump soon writes what the host still owed and then closes
// the sink with one error frame. The reader waits for that rather than closing
// the sink under Pump. Should Pump's drain and close take longer than
// writeTimeout (a viewer that reads slowly), the reader closes the sink
// itself, under Pump: that is safe, for Close runs once and the connection
// takes concurrent writes.
func hostGone(sink *wsSink, pumped <-chan struct{}) {
	select {
	case <-pumped:
	case <-time.After(writeTimeout):
		sink.Close(signal.ErrHostGone)
	}
}

// hostTokenOK reports whether tok authorizes a host registration.
func (s *Server) hostTokenOK(tok string) bool {
	if tok == "" {
		return false
	}
	if share.Equal(tok, s.cfg.WorkbenchToken) {
		return true
	}
	for _, ht := range s.cfg.HostTokens {
		if share.Equal(tok, ht) {
			return true
		}
	}
	return false
}
