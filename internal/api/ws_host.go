package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/signal"
)

// handleHostWS accepts a `conductor host` control connection.
func (s *Server) handleHostWS(w http.ResponseWriter, r *http.Request) {
	if !s.hostTokenOK(presentedToken(r, true)) {
		if !s.limiter.allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "host token required")
		return
	}
	c, err := websocket.Accept(w, r, s.acceptOptions())
	if err != nil {
		return
	}
	c.SetReadLimit(proto.MaxHostMessage + proto.MaxFrame)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Registration must arrive first.
	rctx, rcancel := context.WithTimeout(ctx, helloTimeout)
	typ, data, err := c.Read(rctx)
	rcancel()
	if err != nil || typ != websocket.MessageText {
		c.Close(proto.CloseProtocolError, "register expected")
		return
	}
	var reg proto.Register
	if json.Unmarshal(data, &reg) != nil || reg.T != proto.HostRegister {
		c.Close(proto.CloseProtocolError, "register expected")
		return
	}
	if reg.Session.RelayOnly && s.cfg.Switchyard.Enabled && !s.cfg.SwitchyardRelay() {
		c.Close(proto.CloseProtocolError, "relay_off: this switchyard does not relay")
		return
	}
	conn := signal.NewHostConn()
	hs, resumed, err := s.hosts.Register(reg, conn)
	if err != nil {
		msg := "registration rejected"
		code := websocket.StatusCode(proto.CloseProtocolError)
		if errors.Is(err, session.ErrTooManySessions) {
			msg, code = "session limit reached", proto.CloseTooManyViewers
		} else if errors.Is(err, signal.ErrBadResume) {
			msg, code = "cannot resume session", proto.CloseNotFound
		}
		c.Close(code, msg)
		return
	}
	info := hs.Info()
	hc := &hostConnState{base: s.publicBase(r)}
	s.log.Info("host registered", "session", info.ID, "host", info.HostName, "resumed", resumed)
	defer func() {
		hs.HostDisconnected(conn)
		s.log.Info("host disconnected", "session", info.ID)
	}()

	ice := make([]proto.ICEServer, 0, len(s.cfg.ICEServers))
	for _, srv := range s.cfg.ICEServers {
		ice = append(ice, proto.ICEServer{URLs: srv.URLs, Username: srv.Username, Credential: srv.Credential})
	}
	registered, _ := json.Marshal(proto.Registered{T: proto.HostRegistered, SessionID: info.ID, Secret: hs.Secret(), ShareBaseURL: s.publicBase(r), Resumed: resumed, ICEServers: ice})
	if err := writeText(ctx, c, registered); err != nil {
		return
	}

	// Writer goroutine.
	go func() {
		for {
			select {
			case <-conn.Done():
				c.Close(websocket.StatusPolicyViolation, "connection closed")
				cancel()
				return
			case <-ctx.Done():
				return
			case o := <-conn.Send:
				var err error
				if o.Text != nil {
					err = writeText(ctx, c, o.Text)
				} else {
					wctx, wc := context.WithTimeout(ctx, writeTimeout)
					err = c.Write(wctx, websocket.MessageBinary, o.Binary)
					wc()
				}
				if err != nil {
					conn.Close()
					cancel()
					return
				}
			}
		}
	}()
	go keepalive(ctx, c)

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			f, err := proto.Decode(data)
			if err != nil || f.Type != proto.TypeRelay {
				c.Close(proto.CloseProtocolError, "relay frame expected")
				return
			}
			viewerID, inner, err := proto.DecodeRelay(f.Payload)
			if err != nil {
				c.Close(proto.CloseProtocolError, "bad relay envelope")
				return
			}
			if inner.Type == proto.TypeInput {
				continue // hosts never send input to viewers
			}
			hs.HostRelayFrame(viewerID, inner)
			continue
		}
		if !s.handleHostMessage(hs, hc, data) {
			c.Close(proto.CloseProtocolError, "bad host message")
			return
		}
	}
}

func writeText(ctx context.Context, c *websocket.Conn, b []byte) error {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, b)
}

// hostConnState is what one host connection keeps between its messages:
// the base its links take and when it last asked for links.
type hostConnState struct {
	base      string
	linkTimes []time.Time
}

// handleHostMessage dispatches a JSON message from the host; false is fatal.
func (s *Server) handleHostMessage(hs *signal.HostedSession, hc *hostConnState, data []byte) bool {
	t, err := proto.ParseHeader(data)
	if err != nil {
		return false
	}
	switch t {
	case proto.HostAnswer:
		var m proto.ViewerSDP
		if json.Unmarshal(data, &m) != nil || m.SDP == "" {
			return false
		}
		hs.HostAnswer(m.ViewerID, m.SDP)
	case proto.HostICE:
		var m proto.ViewerICE
		if json.Unmarshal(data, &m) != nil || len(m.Candidate.Candidate) > proto.MaxICECandidate {
			return false
		}
		hs.HostICE(m.ViewerID, m.Candidate)
	case proto.HostStatus:
		var m proto.HostStatusMsg
		if json.Unmarshal(data, &m) != nil {
			return false
		}
		st := session.Status(m.Status)
		switch st {
		case session.StatusRunning, session.StatusExited, session.StatusStopped, session.StatusStarting:
		default:
			return false
		}
		hs.HostStatus(st, m.ExitCode)
	case proto.HostResize:
		var m proto.HostResizeMsg
		if json.Unmarshal(data, &m) != nil || !proto.ValidDimension(m.Cols) || !proto.ValidDimension(m.Rows) {
			return false
		}
		hs.HostResize(m.Cols, m.Rows)
	case proto.HostViewerError:
		var m proto.ViewerError
		if json.Unmarshal(data, &m) != nil {
			return false
		}
		hs.HostViewerError(m.ViewerID, m.Code, m.Message)
	case proto.HostViewerClose:
		var m proto.ViewerRef
		if json.Unmarshal(data, &m) != nil {
			return false
		}
		hs.HostViewerClosed(m.ViewerID)
	case proto.HostAttention:
		var m proto.HostAttentionMsg
		if json.Unmarshal(data, &m) != nil || !session.AttentionState(m.State).Valid() {
			return false
		}
		// The host's own report is not forwarded, so it is never limited.
		_ = hs.SetAttentionFull(session.AttentionState(m.State), m.Message, m.Source, m.Kind, signal.OptionsFromProto(m.Options), false)
	case proto.HostActivity:
		var m proto.HostActivityMsg
		if json.Unmarshal(data, &m) != nil {
			return false
		}
		// The session is the connection's, whatever m.SessionID says; the
		// entry and its state are checked and cleaned by HostActivity, and an
		// entry of a type this server does not know (a newer host's) is
		// dropped, not fatal.
		hs.HostActivity(m.Entry, m.State)
	case proto.HostLink:
		var m proto.HostLinkMsg
		if json.Unmarshal(data, &m) != nil || m.RequestID == "" || len(m.RequestID) > proto.MaxLinkRequestID {
			return false
		}
		s.hostLink(hs, hc, m)
	case proto.HostRegister:
		return false
	default:
		s.log.Debug("ignoring unknown host message", "t", t)
	}
	return true
}

// hostLink mints a share link to the host's session on its request and
// answers link_created, or error{requestId} for a refused one: a role that
// is not view or control, a label over proto.MaxLinkLabel bytes, a lifetime
// over a day, or more than proto.LinkRequestsPerMinute in a minute.
func (s *Server) hostLink(hs *signal.HostedSession, hc *hostConnState, m proto.HostLinkMsg) {
	refuse := func(code, msg string) {
		_ = hs.Tell(proto.ErrorMsg{T: proto.HostError, Code: code, Message: msg, RequestID: m.RequestID})
	}
	now := time.Now()
	hc.linkTimes = slices.DeleteFunc(hc.linkTimes, func(t time.Time) bool { return now.Sub(t) > time.Minute })
	if len(hc.linkTimes) >= proto.LinkRequestsPerMinute {
		refuse("rate_limited", "too many link requests; wait a minute")
		return
	}
	role := session.Role(m.Role)
	if !role.Valid() {
		refuse("invalid_role", "role must be view or control")
		return
	}
	if len(m.Label) > proto.MaxLinkLabel {
		refuse("invalid_request", "label too long")
		return
	}
	if m.TTLSeconds < 0 || m.TTLSeconds > proto.MaxLinkTTLSeconds {
		refuse("invalid_request", "ttlSeconds out of range: a day at most")
		return
	}
	hc.linkTimes = append(hc.linkTimes, now)
	ttl := time.Duration(m.TTLSeconds) * time.Second
	link, token, err := s.links.Create(hs.Info().ID, role, m.Label, ttl)
	if err != nil {
		refuse("link_refused", err.Error())
		return
	}
	s.log.Info("link minted for a host", "session", hs.Info().ID, "role", role, "label", link.Label)
	out := proto.LinkCreated{T: proto.HostLinkCreated, RequestID: m.RequestID, URL: hc.base + "/join/" + url.PathEscape(token), Invite: inviteFor(hc.base, token), LinkID: link.ID, Role: string(link.Role), Label: link.Label}
	if link.ExpiresAt != nil {
		out.ExpiresAt = link.ExpiresAt.UTC().Format(time.RFC3339)
	}
	_ = hs.Tell(out)
}
