package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/signal"
)

func (s *Server) acceptOptions() *websocket.AcceptOptions {
	opts := &websocket.AcceptOptions{OriginPatterns: append([]string{}, s.cfg.AllowedOrigins...)}
	if s.cfg.Dev {
		opts.OriginPatterns = append(opts.OriginPatterns, "localhost:*", "127.0.0.1:*")
	}
	return opts
}

// handleViewerWS attaches a browser to a session. Authentication failures are
// reported through WebSocket close codes because browsers cannot read the
// HTTP status of a failed upgrade.
func (s *Server) handleViewerWS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := s.authenticate(r, true)
	role := p.role(id)
	if role == "" && !s.limiter.allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	c, err := websocket.Accept(w, r, s.acceptOptions())
	if err != nil {
		s.log.Debug("websocket accept failed", "err", err)
		return
	}
	c.SetReadLimit(readLimit)
	if role == "" {
		c.Close(proto.CloseUnauthorized, "unauthorized")
		return
	}
	d, ok := s.registry.Get(id)
	if !ok {
		c.Close(proto.CloseNotFound, "no such session")
		return
	}
	switch drv := d.(type) {
	case *session.Local:
		s.serveLocalViewer(r.Context(), c, drv, role, p.linkID())
	case *signal.HostedSession:
		s.serveHostedViewer(r.Context(), c, drv, role, p.linkID())
	default:
		c.Close(websocket.StatusInternalError, "unsupported session kind")
	}
}

func (s *Server) serveLocalViewer(ctx context.Context, c *websocket.Conn, local *session.Local, role session.Role, linkID string) {
	hello, err := s.readHello(ctx, c)
	if err != nil {
		return
	}
	sink := newWSSink(c)
	sub, err := local.Attach("", role, linkID, hello.Cols, hello.Rows, sink)
	if err != nil {
		if errors.Is(err, session.ErrTooManyViewers) {
			c.Close(proto.CloseTooManyViewers, "too many viewers")
		} else {
			c.Close(websocket.StatusInternalError, "attach failed")
		}
		return
	}
	defer local.Detach(sub)
	go keepalive(sink.ctx, c)
	for {
		f, err := readFrame(sink.ctx, c, 0)
		if err != nil {
			if !errors.Is(err, context.Canceled) && websocket.CloseStatus(err) == -1 && sub.Reason() == nil {
				sink.Close(nil)
			}
			return
		}
		switch f.Type {
		case proto.TypeInput:
			if err := local.Input(sub, f.Payload); err != nil {
				s.sendInputError(sub, local, err)
			}
		case proto.TypeControl:
			if !s.handleLocalControl(sub, local, f.Payload) {
				sink.Close(nil)
				return
			}
		default:
			local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "unexpected frame type"))
			c.Close(proto.CloseProtocolError, "unexpected frame type")
			return
		}
	}
}

func (s *Server) sendInputError(sub *session.Subscription, local *session.Local, err error) {
	switch {
	case errors.Is(err, session.ErrReadOnly):
		local.Send(sub, proto.NewError(proto.ErrCodeReadOnly, "this link is view-only"))
	case errors.Is(err, session.ErrSessionEnded):
		local.Send(sub, proto.NewError(proto.ErrCodeSessionEnded, "the session has ended"))
	default:
		local.Send(sub, proto.NewError("input_failed", "input could not be delivered"))
	}
}

// handleLocalControl dispatches a control message; false means protocol error.
func (s *Server) handleLocalControl(sub *session.Subscription, local *session.Local, payload []byte) bool {
	t, err := proto.ParseHeader(payload)
	if err != nil {
		local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "bad control message"))
		return false
	}
	switch t {
	case proto.CtlResize:
		var m proto.Resize
		if json.Unmarshal(payload, &m) != nil {
			return false
		}
		if err := local.Resize(sub, m.Cols, m.Rows); err != nil {
			switch {
			case errors.Is(err, session.ErrReadOnly):
				// view-only clients follow the controller size silently
			case errors.Is(err, session.ErrBadDimension):
				local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "invalid terminal size"))
			}
		}
	case proto.CtlPing:
		var m proto.Ping
		_ = json.Unmarshal(payload, &m)
		local.Send(sub, proto.MustControl(proto.Ping{T: proto.CtlPong, TS: m.TS}))
	case proto.CtlFileGet:
		var m proto.FileGet
		if json.Unmarshal(payload, &m) != nil {
			return false
		}
		if err := local.FileGet(sub, m); err != nil {
			code := proto.ErrCodeFileDenied
			if errors.Is(err, session.ErrTooManyRequests) {
				code = proto.ErrCodeTooManyRequests
			}
			if frame, encErr := proto.EncodeFile(proto.FileHeader{ReqID: m.ReqID, Path: m.Path, Kind: "error",
				Error: &proto.ErrorInfo{Code: code, Message: err.Error()}}, nil); encErr == nil {
				local.Send(sub, frame)
			}
		}
	case proto.CtlHello:
		// duplicate hello is harmless
	default:
		local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "unknown control message"))
		return false
	}
	return true
}

func (s *Server) readHello(ctx context.Context, c *websocket.Conn) (proto.Hello, error) {
	f, err := readFrame(ctx, c, helloTimeout)
	if err != nil {
		c.Close(proto.CloseProtocolError, "hello expected")
		return proto.Hello{}, err
	}
	var hello proto.Hello
	if f.Type != proto.TypeControl || json.Unmarshal(f.Payload, &hello) != nil || hello.T != proto.CtlHello {
		c.Close(proto.CloseProtocolError, "hello expected")
		return proto.Hello{}, errors.New("hello expected")
	}
	if hello.Proto != proto.ProtoVersion {
		c.Close(proto.CloseProtocolError, "unsupported protocol version")
		return proto.Hello{}, errors.New("unsupported protocol version")
	}
	return hello, nil
}

// serveHostedViewer brokers WebRTC signaling for a hosted session and relays
// frames through the host connection when the viewer asks for it.
func (s *Server) serveHostedViewer(ctx context.Context, c *websocket.Conn, hs *signal.HostedSession, role session.Role, linkID string) {
	viewerID := session.NewID()
	v, err := hs.AddViewer(viewerID, role, linkID)
	if err != nil {
		switch {
		case errors.Is(err, signal.ErrTooManyViewer):
			c.Close(proto.CloseTooManyViewers, "too many viewers")
		default:
			c.Close(proto.CloseSessionEnded, "host disconnected")
		}
		return
	}
	defer hs.RemoveViewer(v)
	sink := newWSSink(c)
	defer sink.cancel()

	ice := make([]proto.ICEServer, 0, len(s.cfg.ICEServers))
	for _, srv := range s.cfg.ICEServers {
		ice = append(ice, proto.ICEServer{URLs: srv.URLs, Username: srv.Username, Credential: srv.Credential})
	}
	info := hs.Info()
	welcome := proto.MustControl(proto.Welcome{
		T:              proto.CtlWelcome,
		Proto:          proto.ProtoVersion,
		SessionID:      info.ID,
		Role:           string(role),
		ViewerID:       viewerID,
		Cols:           info.Cols,
		Rows:           info.Rows,
		Status:         string(info.Status),
		Transport:      proto.TransportWebRTC,
		ICEServers:     ice,
		RelayTimeoutMs: s.cfg.RelayTimeoutMs,
	})
	if err := sink.WriteFrame(welcome); err != nil {
		return
	}
	// Writer: drain frames queued by the host side.
	go func() {
		for {
			select {
			case <-v.Done():
				sink.Close(v.Reason())
				return
			case frame := <-v.Out:
				if err := sink.WriteFrame(frame); err != nil {
					return
				}
			}
		}
	}()
	go keepalive(sink.ctx, c)
	for {
		f, err := readFrame(sink.ctx, c, 0)
		if err != nil {
			return
		}
		switch f.Type {
		case proto.TypeSignal:
			if !s.handleViewerSignal(sink, hs, v, f.Payload) {
				return
			}
		case proto.TypeInput, proto.TypeControl:
			if !v.Relay() {
				_ = sink.WriteFrame(proto.NewError(proto.ErrCodeBadFrame, "terminal frames require relay mode on this connection"))
				continue
			}
			if err := hs.RelayToHost(v, f); err != nil {
				switch {
				case errors.Is(err, session.ErrReadOnly):
					_ = sink.WriteFrame(proto.NewError(proto.ErrCodeReadOnly, "this link is view-only"))
				case errors.Is(err, signal.ErrHostGone):
					sink.Close(signal.ErrHostGone)
					return
				}
			}
		default:
			c.Close(proto.CloseProtocolError, "unexpected frame type")
			return
		}
	}
}

// handleViewerSignal forwards signaling; false ends the connection.
func (s *Server) handleViewerSignal(sink *wsSink, hs *signal.HostedSession, v *signal.Viewer, payload []byte) bool {
	t, err := proto.ParseHeader(payload)
	if err != nil {
		return false
	}
	fail := func(err error) bool {
		if errors.Is(err, signal.ErrHostGone) {
			sink.Close(signal.ErrHostGone)
			return false
		}
		return true
	}
	switch t {
	case proto.SigOffer:
		var m proto.SDP
		if json.Unmarshal(payload, &m) != nil || m.SDP == "" {
			return false
		}
		return fail(hs.ForwardOffer(v, m.SDP))
	case proto.SigICE:
		var m proto.ICE
		if json.Unmarshal(payload, &m) != nil || len(m.Candidate.Candidate) > proto.MaxICECandidate {
			return false
		}
		return fail(hs.ForwardICE(v, m.Candidate))
	case proto.SigRelay:
		if err := hs.StartRelay(v); err != nil {
			return fail(err)
		}
		if f, err := proto.EncodeJSON(proto.TypeSignal, proto.Simple{T: proto.SigRelayOK}); err == nil {
			_ = sink.WriteFrame(f)
		}
		return true
	}
	return false
}
