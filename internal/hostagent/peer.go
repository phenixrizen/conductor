package hostagent

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// peer is one viewer: either a WebRTC data channel or a relay through the
// server. Terminal frames from the viewer are dispatched to the local session
// identically for both transports.
type peer struct {
	a      *agent
	id     string
	role   session.Role
	linkID string

	mu        sync.Mutex
	pc        *webrtc.PeerConnection
	dc        *dcSink
	relay     *relaySink
	sub       *session.Subscription
	pending   []webrtc.ICECandidateInit
	remoteSet bool
	closed    bool
}

var errNoWebRTC = errors.New("webrtc disabled on this host")

func newPeer(a *agent, id string, role session.Role, linkID string) *peer {
	return &peer{a: a, id: id, role: role, linkID: linkID}
}

// startWebRTC prepares a peer connection that answers the viewer's offer.
func (p *peer) startWebRTC(ice []proto.ICEServer) error {
	cfg := webrtc.Configuration{}
	for _, s := range ice {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	se := webrtc.SettingEngine{}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})
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
				_ = p.a.local.Resize(sub, m.Cols, m.Rows)
			}
		case proto.CtlPing:
			var m proto.Ping
			_ = json.Unmarshal(f.Payload, &m)
			p.a.local.Send(sub, proto.MustControl(proto.Ping{T: proto.CtlPong, TS: m.TS}))
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
	sub, err := p.a.local.Attach(p.id, p.role, p.linkID, hello.Cols, hello.Rows, sink)
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
	if sub != nil {
		p.a.local.Detach(sub)
	}
	if pc != nil {
		_ = pc.Close()
	}
}
