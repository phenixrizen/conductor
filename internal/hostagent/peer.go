package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// peer is one viewer: either a WebRTC data channel or a relay through the
// server. Terminal frames from the viewer are dispatched to the local session
// identically for both transports.
type peer struct {
	a         *agent
	id        string
	role      session.Role
	linkID    string
	linkLabel string

	mu        sync.Mutex
	pc        *webrtc.PeerConnection
	dc        *dcSink
	relay     *relaySink
	sub       *session.Subscription
	pending   []webrtc.ICECandidateInit
	remoteSet bool
	closed    bool

	// typing runs the viewer's submits, chats to the agent and chat_sends;
	// nvimOpening counts its nvim_opens running (queue.go).
	typing      requestQueue
	nvimOpening atomic.Int32
}

var errNoWebRTC = errors.New("webrtc disabled on this host")

func newPeer(a *agent, id string, role session.Role, linkID, linkLabel string) *peer {
	return &peer{a: a, id: id, role: role, linkID: linkID, linkLabel: linkLabel}
}

// startWebRTC prepares a peer connection that answers the viewer's offer.
func (p *peer) startWebRTC(ice []proto.ICEServer) error {
	cfg := webrtc.Configuration{}
	for _, s := range ice {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	se := webrtc.SettingEngine{}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})
	if err := applyICE(&se, p.a.opts.ICE); err != nil {
		return err
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(cfg)
	if err != nil {
		return err
	}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		p.a.send(proto.ViewerICE{T: proto.HostICE, ViewerID: p.id, Candidate: proto.ICECandidate{
			Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex,
		}})
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		p.a.log.Debug("peer connection state", "viewer", p.id, "state", st.String())
		switch st {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			p.detachDataChannel()
		}
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		sink := newDCSink(dc, p.a.log)
		p.mu.Lock()
		p.dc = sink
		p.mu.Unlock()
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if msg.IsString {
				return
			}
			f, err := proto.Decode(msg.Data)
			if err != nil {
				return
			}
			p.handleFrame(f)
		})
		dc.OnClose(func() { p.detachDataChannel() })
	})
	p.mu.Lock()
	p.pc = pc
	p.mu.Unlock()
	return nil
}

func (p *peer) handleOffer(sdp string) error {
	p.mu.Lock()
	pc := p.pc
	p.mu.Unlock()
	if pc == nil {
		return errNoWebRTC
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		return err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}
	p.mu.Lock()
	p.remoteSet = true
	pending := p.pending
	p.pending = nil
	p.mu.Unlock()
	for _, c := range pending {
		_ = pc.AddICECandidate(c)
	}
	p.a.send(proto.ViewerSDP{T: proto.HostAnswer, ViewerID: p.id, SDP: answer.SDP})
	return nil
}

func (p *peer) handleICE(c proto.ICECandidate) {
	init := webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid, SDPMLineIndex: c.SDPMLineIndex}
	p.mu.Lock()
	pc, ready := p.pc, p.remoteSet
	if !ready {
		if len(p.pending) < 64 {
			p.pending = append(p.pending, init)
		}
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	if pc != nil {
		_ = pc.AddICECandidate(init)
	}
}

// startRelay switches the viewer to the server relay.
func (p *peer) startRelay() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	pc := p.pc
	p.pc = nil
	p.dc = nil
	if p.sub != nil {
		p.a.local.Detach(p.sub)
		p.sub = nil
	}
	p.relay = &relaySink{a: p.a, viewerID: p.id}
	p.mu.Unlock()
	if pc != nil {
		_ = pc.Close()
	}
}

// buffered returns the bytes the peer's data channel holds that the viewer has
// not acknowledged; zero for a viewer on the relay or one not connected yet.
func (p *peer) buffered() uint64 {
	p.mu.Lock()
	sink := p.dc
	p.mu.Unlock()
	if sink == nil {
		return 0
	}
	return sink.buffered()
}

// handleFrame processes a terminal frame from the viewer over either transport.
func (p *peer) handleFrame(f proto.Frame) {
	p.mu.Lock()
	sub := p.sub
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return
	}
	switch f.Type {
	case proto.TypeControl:
		t, err := proto.ParseHeader(f.Payload)
		if err != nil {
			return
		}
		if t == proto.CtlHello {
			if sub != nil {
				return
			}
			var hello proto.Hello
			if json.Unmarshal(f.Payload, &hello) != nil || hello.Proto != proto.ProtoVersion {
				p.a.sendViewerError(p.id, proto.ErrCodeBadFrame, "unsupported hello")
				return
			}
			p.attach(hello)
			return
		}
		if sub == nil {
			return
		}
		switch t {
		case proto.CtlResize:
			var m proto.Resize
			if json.Unmarshal(f.Payload, &m) == nil {
				_ = p.a.local.ResizeWith(sub, m.Cols, m.Rows, m.Take)
			}
		case proto.CtlPing:
			var m proto.Ping
			_ = json.Unmarshal(f.Payload, &m)
			p.a.local.Send(sub, proto.MustControl(proto.Ping{T: proto.CtlPong, TS: m.TS}))
		case proto.CtlSubmit:
			var m proto.Submit
			if json.Unmarshal(f.Payload, &m) != nil || len(m.Text) > proto.MaxSubmit {
				p.a.local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "bad submit message"))
				return
			}
			// Off the frame loop, which also carries the relay's other
			// viewers: the submission pauses before its Enter.
			p.typeIn(sub, "", func(ctx context.Context) error {
				_, err := p.a.local.Submit(ctx, session.Submission{Text: m.Text, By: sub})
				return err
			}, func(err error) { p.a.local.Send(sub, submitError(err)) })
		case proto.CtlChat:
			var m proto.ChatPost
			if json.Unmarshal(f.Payload, &m) != nil {
				p.a.local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "bad chat message"))
				return
			}
			msg, err := p.a.local.Chat(sub, m)
			if err != nil {
				p.a.local.Send(sub, session.ChatErrorFrame(err, m.Nonce))
				return
			}
			if m.To != "" {
				// Typed as a submit is, in its turn among them.
				send := proto.ChatSend{T: proto.CtlChatSend, Ref: msg.ID, Scope: msg.Scope, To: m.To}
				p.typeIn(sub, msg.ID, func(ctx context.Context) error {
					return p.a.local.ChatSend(ctx, sub, send)
				}, func(err error) { p.a.local.Send(sub, session.ChatErrorFrame(err, msg.ID)) })
			}
		case proto.CtlChatSend:
			var m proto.ChatSend
			if json.Unmarshal(f.Payload, &m) != nil {
				p.a.local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "bad chat_send message"))
				return
			}
			p.typeIn(sub, m.Ref, func(ctx context.Context) error {
				return p.a.local.ChatSend(ctx, sub, m)
			}, func(err error) { p.a.local.Send(sub, session.ChatErrorFrame(err, m.Ref)) })
		case proto.CtlNvimOpen:
			var m proto.NvimOpen
			if json.Unmarshal(f.Payload, &m) != nil {
				return
			}
			// Side by side, as many at once as the editors a connection
			// may hold: Neovim may take seconds to load the person's config.
			if p.nvimOpening.Add(1) > proto.MaxNvimPerSub {
				p.nvimOpening.Add(-1)
				p.a.local.Send(sub, session.NvimRefused(m.ReqID, session.ErrTooManyRequests))
				return
			}
			go func() {
				defer p.nvimOpening.Add(-1)
				p.nvimOpen(sub, m)
			}()
		case proto.CtlNvimInput:
			var m proto.NvimInput
			if json.Unmarshal(f.Payload, &m) != nil {
				return
			}
			if err := p.a.local.NvimInput(sub, m); err != nil {
				p.a.local.Send(sub, session.NvimRefused("", err))
			}
		case proto.CtlNvimClose:
			var m proto.NvimClose
			if json.Unmarshal(f.Payload, &m) != nil {
				return
			}
			p.a.local.NvimClose(sub, m)
		case proto.CtlNvimSwap:
			var m proto.NvimSwap
			if json.Unmarshal(f.Payload, &m) != nil {
				return
			}
			if err := p.a.local.NvimSwap(sub, m); err != nil {
				p.a.local.Send(sub, session.NvimRefused("", err))
			}
		case proto.CtlFileGet:
			var m proto.FileGet
			if json.Unmarshal(f.Payload, &m) != nil {
				return
			}
			if err := p.a.local.FileGet(sub, m); err != nil {
				code := proto.ErrCodeFileDenied
				if errors.Is(err, session.ErrTooManyRequests) {
					code = proto.ErrCodeTooManyRequests
				}
				if frame, encErr := proto.EncodeFile(proto.FileHeader{ReqID: m.ReqID, Path: m.Path, Kind: "error",
					Error: &proto.ErrorInfo{Code: code, Message: err.Error()}}, nil); encErr == nil {
					p.a.local.Send(sub, frame)
				}
			}
		}
	case proto.TypeFileWrite:
		// A save from the editor (design round 12, F6), written on this machine.
		if sub == nil {
			return
		}
		h, part, err := proto.DecodeFileWrite(f.Payload)
		if err != nil {
			p.a.local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "malformed file write"))
			return
		}
		p.a.local.FileWrite(sub, h, part)
	case proto.TypeInput:
		if sub == nil {
			return
		}
		if err := p.a.local.Input(sub, f.Payload); err != nil {
			switch {
			case errors.Is(err, session.ErrReadOnly):
				p.a.local.Send(sub, proto.NewError(proto.ErrCodeReadOnly, "this link is view-only"))
			case errors.Is(err, session.ErrSessionEnded):
				p.a.local.Send(sub, proto.NewError(proto.ErrCodeSessionEnded, "the session has ended"))
			}
		}
	}
}

// attached says whether sub is still the viewer's attachment: neither let go
// by the peer (its data channel closed, a move to the relay, the viewer
// gone), which the peer does before the session's Detach (that ends the
// viewer's editors first, and may wait for Neovim to exit), nor ended by the
// session (a revoked link, a slow consumer).
func (p *peer) attached(sub *session.Subscription) func() bool {
	return func() bool {
		select {
		case <-sub.Done():
			return false
		default:
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.sub == sub && !p.closed
	}
}

// typeIn queues what types into the agent for sub (a submit, a chat to the
// agent, a chat_send) on the viewer's typing queue. Its time runs from now
// (agent.submitDeadline): do runs in its turn within it, and refuse tells
// the viewer why it was not done, do's error or the time run out, whether
// its turn came or not. A full queue refuses it at once (too_many_requests,
// requestID naming it).
func (p *peer) typeIn(sub *session.Subscription, requestID string, do func(context.Context) error, refuse func(error)) {
	deadline := p.a.submitDeadline()
	if !p.typing.add(request{
		run: func() {
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			defer cancel()
			err := ctx.Err()
			if err == nil {
				err = do(ctx)
			}
			if err != nil {
				refuse(err)
			}
		},
		live:     p.attached(sub),
		deadline: deadline,
		expired:  func() { refuse(context.DeadlineExceeded) },
	}) {
		p.a.local.Send(sub, queueFull(requestID))
	}
}

// submitError is the error a submit gets for err.
func submitError(err error) []byte {
	code := "input_failed"
	switch {
	case errors.Is(err, session.ErrReadOnly):
		code = proto.ErrCodeReadOnly
	case errors.Is(err, session.ErrSessionEnded):
		code = proto.ErrCodeSessionEnded
	case errors.Is(err, session.ErrTrustQuestion):
		code = proto.ErrCodeNotSent
	}
	return proto.NewError(code, err.Error())
}

// nvimOpen starts the viewer's Neovim on a file.
func (p *peer) nvimOpen(sub *session.Subscription, req proto.NvimOpen) {
	ctx, cancel := context.WithTimeout(context.Background(), nvimOpenTimeout)
	defer cancel()
	if err := p.a.local.NvimOpen(ctx, sub, req); err != nil {
		p.a.local.Send(sub, session.NvimRefused(req.ReqID, err))
	}
}

func (p *peer) attach(hello proto.Hello) {
	p.mu.Lock()
	var sink session.Sink
	if p.relay != nil {
		sink = p.relay
	} else if p.dc != nil {
		sink = p.dc
	}
	p.mu.Unlock()
	if sink == nil {
		return
	}
	sub, err := p.a.local.AttachWith(session.AttachOptions{
		ID: p.id, Role: p.role, LinkID: p.linkID, LinkLabel: p.linkLabel, Name: hello.Name, Cols: hello.Cols, Rows: hello.Rows,
	}, sink)
	if err != nil {
		p.a.sendViewerError(p.id, "attach_failed", err.Error())
		return
	}
	p.mu.Lock()
	p.sub = sub
	p.mu.Unlock()
}

func (p *peer) detachDataChannel() {
	p.mu.Lock()
	sub := p.sub
	if p.relay == nil {
		p.sub = nil
	} else {
		sub = nil
	}
	p.mu.Unlock()
	if sub != nil {
		p.a.local.Detach(sub)
	}
}

func (p *peer) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	pc, sub := p.pc, p.sub
	p.pc, p.sub = nil, nil
	p.mu.Unlock()
	p.typing.close()
	if sub != nil {
		p.a.local.Detach(sub)
	}
	if pc != nil {
		_ = pc.Close()
	}
}
