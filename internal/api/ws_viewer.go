package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

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
	// A switchyard takes the desktop app's own workbench as a viewer.
	if s.cfg.Switchyard.Enabled {
		opts.OriginPatterns = append(opts.OriginPatterns, s.cfg.SwitchyardOrigins()...)
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
	// The role above lets the connection in; the link is checked again as
	// the viewer attaches, under the lock a revoke takes to close the link's
	// viewers, since the hello may come seconds later.
	check := s.attachCheck(p, id)
	switch drv := d.(type) {
	case *session.Local:
		s.serveLocalViewer(r.Context(), c, drv, role, p.linkID(), p.admin, check)
	case *signal.HostedSession:
		s.serveHostedViewer(r.Context(), c, drv, role, p.linkID(), check)
	default:
		c.Close(websocket.StatusInternalError, "unsupported session kind")
	}
}

// owner is the workbench's own connection (the admin token), the only one
// offered the editor's Neovim.
func (s *Server) serveLocalViewer(ctx context.Context, c *websocket.Conn, local *session.Local, role session.Role, linkID string, owner bool, check func() (session.Grant, error)) {
	hello, err := s.readHello(ctx, c)
	if err != nil {
		return
	}
	sink := newWSSink(c)
	sub, err := local.AttachWith(session.AttachOptions{
		Role: role, LinkID: linkID, LinkLabel: s.linkLabel(linkID), Name: hello.Name, Cols: hello.Cols, Rows: hello.Rows, ChatOnly: hello.ChatOnly,
		Authorize: check,
		Owner:     owner,
	}, sink)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrRevoked), errors.Is(err, session.ErrSessionEnded):
			// Said as a revoke (or the session's end) after the attach says
			// it: an error frame, then 4403 (or 4410).
			sink.Close(err)
		case errors.Is(err, session.ErrTooManyViewers):
			c.Close(proto.CloseTooManyViewers, "too many viewers")
		default:
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
		case proto.TypeFileWrite:
			// A save from the editor (design round 12, F6): its answer is a FILE frame.
			h, part, err := proto.DecodeFileWrite(f.Payload)
			if err != nil {
				local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "malformed file write"))
				continue
			}
			local.FileWrite(sub, h, part)
		default:
			local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "unexpected frame type"))
			c.Close(proto.CloseProtocolError, "unexpected frame type")
			return
		}
	}
}

// submitTimeout bounds a submit message's submission: its turn, the pause
// and its Enter.
const submitTimeout = 10 * time.Second

func (s *Server) sendInputError(sub *session.Subscription, local *session.Local, err error) {
	switch {
	case errors.Is(err, session.ErrReadOnly):
		local.Send(sub, proto.NewError(proto.ErrCodeReadOnly, "this link is view-only"))
	case errors.Is(err, session.ErrSessionEnded):
		local.Send(sub, proto.NewError(proto.ErrCodeSessionEnded, "the session has ended"))
	case errors.Is(err, session.ErrTrustQuestion):
		local.Send(sub, proto.NewError(proto.ErrCodeNotSent, session.TrustQuestionWords))
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
		if err := local.ResizeWith(sub, m.Cols, m.Rows, m.Take); err != nil {
			switch {
			case errors.Is(err, session.ErrReadOnly), errors.Is(err, session.ErrNotSizer):
				// view-only clients, and every viewer but the sizer, follow the size silently
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
	case proto.CtlNvimOpen:
		var m proto.NvimOpen
		if json.Unmarshal(payload, &m) != nil {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), nvimOpenTimeout)
		err := local.NvimOpen(ctx, sub, m)
		cancel()
		if err != nil {
			local.Send(sub, session.NvimRefused(m.ReqID, err))
		}
	case proto.CtlNvimInput:
		var m proto.NvimInput
		if json.Unmarshal(payload, &m) != nil {
			return false
		}
		if err := local.NvimInput(sub, m); err != nil {
			local.Send(sub, session.NvimRefused("", err))
		}
	case proto.CtlNvimClose:
		var m proto.NvimClose
		if json.Unmarshal(payload, &m) != nil {
			return false
		}
		local.NvimClose(sub, m)
	case proto.CtlNvimSwap:
		var m proto.NvimSwap
		if json.Unmarshal(payload, &m) != nil {
			return false
		}
		if err := local.NvimSwap(sub, m); err != nil {
			local.Send(sub, session.NvimRefused("", err))
		}
	case proto.CtlSubmit:
		var m proto.Submit
		if json.Unmarshal(payload, &m) != nil {
			return false
		}
		if len(m.Text) > proto.MaxSubmit {
			local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "submit text too long"))
			return true
		}
		// The submission goes on when the client goes: a reply box closes its
		// connection once it has sent, and a line cut in its pause would wait
		// without its Enter. The read loop waits for it, so what the client
		// sends next comes after it.
		ctx, cancel := context.WithTimeout(context.Background(), submitTimeout)
		_, err := local.Submit(ctx, session.Submission{Text: m.Text, By: sub})
		cancel()
		if err != nil {
			s.sendInputError(sub, local, err)
		}
	case proto.CtlChat:
		var m proto.ChatPost
		if json.Unmarshal(payload, &m) != nil {
			local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "bad chat message"))
			return true
		}
		msg, err := local.Chat(sub, m)
		if err != nil {
			local.Send(sub, session.ChatErrorFrame(err, m.Nonce))
			return true
		}
		if m.To != "" {
			// Typed into the agent (or the run's member named) as a submit is, on the read loop, so what the client sends next comes after it.
			ctx, cancel := context.WithTimeout(context.Background(), submitTimeout)
			err := local.ChatSend(ctx, sub, proto.ChatSend{T: proto.CtlChatSend, Ref: msg.ID, Scope: msg.Scope, To: m.To})
			cancel()
			if err != nil {
				local.Send(sub, session.ChatErrorFrame(err, msg.ID))
			}
		}
	case proto.CtlChatSend:
		var m proto.ChatSend
		if json.Unmarshal(payload, &m) != nil {
			local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "bad chat_send message"))
			return true
		}
		ctx, cancel := context.WithTimeout(context.Background(), submitTimeout)
		err := local.ChatSend(ctx, sub, m)
		cancel()
		if err != nil {
			local.Send(sub, session.ChatErrorFrame(err, m.Ref))
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
	if len(hello.Name) > 4*proto.MaxNameLen {
		c.Close(proto.CloseProtocolError, "name too long")
		return proto.Hello{}, errors.New("name too long")
	}
	return hello, nil
}

// linkLabel returns the label of a share link for the viewers roster.
func (s *Server) linkLabel(linkID string) string {
	if linkID == "" {
		return ""
	}
	if l, ok := s.links.Get(linkID); ok {
		return l.Label
	}
	return ""
}

// serveHostedViewer brokers WebRTC signaling for a hosted session and relays
// frames through the host connection when the viewer asks for it.
func (s *Server) serveHostedViewer(ctx context.Context, c *websocket.Conn, hs *signal.HostedSession, role session.Role, linkID string, check func() (session.Grant, error)) {
	viewerID := session.NewID()
	v, err := hs.AddViewerWith(signal.ViewerOptions{ID: viewerID, Role: role, LinkID: linkID, LinkLabel: s.linkLabel(linkID), Authorize: check})
	if err != nil {
		switch {
		case errors.Is(err, session.ErrRevoked), errors.Is(err, session.ErrSessionEnded):
			newWSSink(c).Close(err)
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
		Role:           string(v.Role),
		ViewerID:       viewerID,
		Cols:           info.Cols,
		Rows:           info.Rows,
		Status:         string(info.Status),
		Transport:      proto.TransportWebRTC,
		ICEServers:     ice,
		RelayTimeoutMs: s.cfg.RelayTimeoutMs,
		RelayOnly:      hs.RelayOnly(),
	})
	if err := sink.WriteFrame(welcome); err != nil {
		return
	}
	// Writer: drain frames queued by the host side. pumped closes when Pump
	// has closed the sink.
	pumped := make(chan struct{})
	go func() {
		defer close(pumped)
		v.Pump(sink)
	}()
	go keepalive(sink.ctx, c)
	for {
		f, err := readFrame(sink.ctx, c, 0)
		if err != nil {
			return
		}
		switch f.Type {
		case proto.TypeSignal:
			if !s.handleViewerSignal(sink, pumped, hs, v, f.Payload) {
				return
			}
		case proto.TypeInput, proto.TypeControl:
			if !v.Relay() {
				_ = sink.WriteFrame(proto.NewError(proto.ErrCodeBadFrame, "terminal frames require relay mode on this connection"))
				continue
			}
			s.relayed.add(len(f.Payload))
			if err := hs.RelayToHost(v, f); err != nil {
				switch {
				case errors.Is(err, session.ErrReadOnly):
					_ = sink.WriteFrame(proto.NewError(proto.ErrCodeReadOnly, "this link is view-only"))
				case errors.Is(err, signal.ErrHostGone):
					hostGone(sink, pumped)
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
func (s *Server) handleViewerSignal(sink *wsSink, pumped <-chan struct{}, hs *signal.HostedSession, v *signal.Viewer, payload []byte) bool {
	t, err := proto.ParseHeader(payload)
	if err != nil {
		return false
	}
	fail := func(err error) bool {
		if errors.Is(err, signal.ErrHostGone) {
			hostGone(sink, pumped)
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
		if s.cfg.Switchyard.Enabled && !s.cfg.SwitchyardRelay() {
			// A switchyard without a relay: the viewer is told, and stays
			// on its WebRTC attempt.
			_ = sink.WriteFrame(proto.NewError(proto.ErrCodeRelayOff, "this switchyard does not relay: the terminal connects peer to peer or not at all"))
			return true
		}
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

// nvimOpenTimeout bounds the start of an editor (Neovim loading a config).
const nvimOpenTimeout = 15 * time.Second
