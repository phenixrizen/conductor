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
		s.serveHostedViewer(r.Context(), c, drv, role, p.linkID(), check, clientKey(r))
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
	go keepalive(sink.ctx, c, s.pingEvery)
	for {
		f, err := readFrame(sink.ctx, c, 0)
		if err != nil {
			if !errors.Is(err, context.Canceled) && websocket.CloseStatus(err) == -1 && sub.Reason() == nil {
				sink.Close(nil)
			}
			return
		}
		// A client the session has ended (revoked, expired, dropped as a
		// slow consumer) is not acted on while its connection closes.
		select {
		case <-sub.Done():
			return
		default:
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
	case errors.Is(err, session.ErrQuestion):
		local.Send(sub, proto.NewError(proto.ErrCodeNotSent, session.QuestionWords))
	case errors.Is(err, session.ErrInputStalled):
		local.Send(sub, proto.NewError("input_failed", session.ErrInputStalled.Error()))
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
		// A line the program takes slowly is accepted, and entered as it
		// reads (ErrSubmitPending).
		if err != nil && !errors.Is(err, session.ErrSubmitPending) {
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
// frames through the host connection when the viewer asks for it. What the
// viewer sends is read under its address's budget (cfg.ViewerRelayKBps),
// one bucket for every hosted viewer of the address, as the open hosts of
// one address share theirs: past it the viewer is slowed, its connection
// read no faster than the bucket refills. Each frame is charged the larger
// of what the viewer sent and what it sends the host (a relayed frame in its
// envelope, a message encoded anew).
//
// What the viewer sent before its connection closed reaches the host before
// the viewer's leave: the read loop reads the connection until it closes, or
// for drainTimeout once it has failed (a write to the viewer, a ping), and
// the viewer is let go (RemoveViewer, which tells the host it left) when the
// read loop is done. The connection's failure ends the read loop's waits
// (the budget, the join, room in the host's queue): the frame that waited is
// dropped, and with it whatever the viewer sent after it.
func (s *Server) serveHostedViewer(ctx context.Context, c *websocket.Conn, hs *signal.HostedSession, role session.Role, linkID string, check func() (session.Grant, error), addr string) {
	sink := newWSSink(c)
	defer sink.cancel()
	// waits is what the read loop's waits wait under: it ends when the
	// viewer's connection fails, or its writer ends.
	waits, endWaits := context.WithCancel(sink.ctx)
	defer endWaits()
	var budget *byteBucket
	if kbps := s.cfg.ViewerRelayKBps; kbps > 0 {
		budget = s.viewerRelay.acquire(addr, kbps*1024, s.relayBurst(kbps*1024))
		defer s.viewerRelay.release(addr)
	}
	// credit is what the frame in hand was charged as it was read and what
	// it sends the host has not used yet: a message to the host is charged
	// only what it brings past it. Read and charged on the read loop alone,
	// which is where the viewer sends the host anything.
	var credit int
	var charge func(n int) error
	// chargeJoin charges the viewer's join and leave, on the join's own
	// goroutine (the read loop's credit is not its), until the viewer closes.
	var chargeJoin func(stop <-chan struct{}, n int) error
	if budget != nil {
		charge = func(n int) error {
			use := min(credit, n)
			credit -= use
			if n == use {
				return nil
			}
			return budget.take(waits, n-use)
		}
		chargeJoin = func(stop <-chan struct{}, n int) error {
			ctx, cancel := context.WithCancel(waits)
			defer cancel()
			go func() {
				select {
				case <-stop:
					cancel()
				case <-ctx.Done():
				}
			}()
			return budget.take(ctx, n)
		}
	}
	viewerID := session.NewID()
	v, err := hs.AddViewerWith(signal.ViewerOptions{ID: viewerID, Role: role, LinkID: linkID, LinkLabel: s.linkLabel(linkID), Authorize: check, Charge: charge, ChargeJoin: chargeJoin})
	if err != nil {
		switch {
		case errors.Is(err, session.ErrRevoked), errors.Is(err, session.ErrSessionEnded):
			sink.Close(err)
		case errors.Is(err, signal.ErrTooManyViewer):
			c.Close(proto.CloseTooManyViewers, "too many viewers")
		default:
			c.Close(proto.CloseSessionEnded, "host disconnected")
		}
		return
	}
	defer hs.RemoveViewer(v)

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
	// The read loop reads under reading: the connection's own until it
	// closes, and at most drainTimeout past its failure (waits).
	reading, endReading := context.WithCancel(sink.ctx)
	defer endReading()
	stopDrain := context.AfterFunc(waits, func() {
		t := time.NewTimer(drainTimeout)
		defer t.Stop()
		select {
		case <-t.C:
			endReading()
		case <-reading.Done():
		}
	})
	defer stopDrain()
	// cut says a message the viewer sent was dropped (err) once its
	// connection had failed, its wait cut short (the budget, the join, room
	// in the host's queue): nothing the viewer sent after it goes, and the
	// read loop ends. The failure is the viewer's own (Failed), set before
	// any of those waits ends for it. A viewer the server let go is not cut:
	// its connection closes as Pump ends, with what the viewer is owed.
	cut := func(err error) bool {
		return err != nil && v.Failed()
	}
	// Writer: drain frames queued by the host side. pumped closes when Pump
	// has closed the sink, or given up on a connection that failed; the read
	// loop's waits end with it, and its reads soon after.
	pumped := make(chan struct{})
	var out session.Sink = sink
	if s.viewerSeam != nil && s.viewerSeam.write != nil {
		out = seamSink{sink, s.viewerSeam.write}
	}
	go func() {
		defer close(pumped)
		v.Pump(out)
		if s.viewerSeam != nil && s.viewerSeam.ended != nil {
			s.viewerSeam.ended()
		}
		endWaits()
	}()
	// Once closed, the viewer is written what it is owed, its error and its
	// close within closeWait, or cut off (wsSink.Closing).
	go func() {
		select {
		case <-v.Done():
			sink.Closing()
		case <-pumped:
		}
	}()
	// A failed connection is left open for the read loop, the writer having
	// given up on it: it is closed once the read loop is done. (A connection
	// that has not failed is closed by the writer as it ends, which letting
	// the viewer go makes it do.)
	defer func() {
		if v.Failed() {
			c.CloseNow()
		}
	}()
	// A connection that fails while nothing is written to it is found by
	// its pings, though the read loop is held: its waits end, and the
	// viewer is let go as the read loop ends.
	go keepaliveUntilGone(sink.ctx, c, s.pingEvery, func() {
		v.ConnFailed()
		endWaits()
	})
	for {
		if s.viewerSeam != nil && s.viewerSeam.read != nil {
			s.viewerSeam.read(pumped)
		}
		f, err := readFrame(reading, c, 0)
		if err != nil {
			return
		}
		// Every frame is counted, whatever becomes of it, before the next
		// is read; a frame waits for the host's queue in its turn after
		// (signal.HostConn's share for viewers), so neither wait holds up
		// the host's connection or another viewer's.
		if budget != nil {
			credit = 1 + len(f.Payload)
			if budget.take(waits, credit) != nil {
				return
			}
		}
		switch f.Type {
		case proto.TypeSignal:
			if !s.handleViewerSignal(sink, pumped, hs, v, f.Payload, cut) {
				return
			}
		case proto.TypeInput, proto.TypeControl, proto.TypeFileWrite:
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
				case cut(err):
					return
				}
			}
		default:
			c.Close(proto.CloseProtocolError, "unexpected frame type")
			return
		}
	}
}

// handleViewerSignal forwards signaling; false ends the connection, as a
// message cut short does (serveHostedViewer).
func (s *Server) handleViewerSignal(sink *wsSink, pumped <-chan struct{}, hs *signal.HostedSession, v *signal.Viewer, payload []byte, cut func(error) bool) bool {
	t, err := proto.ParseHeader(payload)
	if err != nil {
		return false
	}
	fail := func(err error) bool {
		switch {
		case errors.Is(err, signal.ErrHostGone):
			hostGone(sink, pumped)
			return false
		case cut(err):
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

// drainTimeout bounds how long a hosted viewer's connection is still read
// once it has failed: what the viewer sent before it closed is in the
// socket already.
const drainTimeout = 2 * time.Second

// nvimOpenTimeout bounds the start of an editor (Neovim loading a config).
const nvimOpenTimeout = 15 * time.Second
