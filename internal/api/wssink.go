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
	pingEvery    = 30 * time.Second
)

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

// keepalive pings the peer until ctx ends.
func keepalive(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(pingEvery)
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

// hostTokenOK reports whether tok authorizes a host registration.
func (s *Server) hostTokenOK(tok string) bool {
	if tok == "" {
		return false
	}
	if share.Equal(tok, s.cfg.AdminToken) {
		return true
	}
	for _, ht := range s.cfg.HostTokens {
		if share.Equal(tok, ht) {
			return true
		}
	}
	return false
}
