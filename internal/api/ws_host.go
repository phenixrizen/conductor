package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
	"github.com/phenixrizen/conductor/internal/signal"
)

// handleHostWS accepts a `conductor host` control connection.
func (s *Server) handleHostWS(w http.ResponseWriter, r *http.Request) {
	// An open host presents no token at all, on a switchyard that admits
	// them, and lives under the per-address limits; a wrong token is refused
	// as ever (a typo must show), a right one is trusted and unlimited.
	tok := presentedToken(r, true)
	open := tok == "" && s.cfg.Switchyard.Enabled && s.cfg.Switchyard.OpenHosts
	if !open && !s.hostTokenOK(tok) {
		if !s.limiter.allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "host token required")
		return
	}
	addr := clientKey(r)
	if open {
		if !s.hostLimiter.allow(addr) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many registrations from this address; ask the operator for a host token")
			return
		}
		if !s.openHosts.acquire(addr, s.cfg.Switchyard.OpenHostSessions) {
			writeError(w, http.StatusTooManyRequests, "open_host_limit", "this address holds as many open sessions as this switchyard allows; ask the operator for a host token")
			return
		}
		defer s.openHosts.release(addr)
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
	if open {
		hc.addr = addr
	}
	s.sawHost(hs.Owner())
	if s.cfg.Switchyard.Enabled {
		switch {
		case open && s.cfg.Switchyard.OpenHostRelayKBps > 0:
			// One bucket for every open host of the address: the bound is the address's, not each connection's.
			hc.relay = s.openRelay.acquire(addr, s.cfg.Switchyard.OpenHostRelayKBps*1024)
			defer s.openRelay.release(addr)
		case s.cfg.Switchyard.RelayKBps > 0:
			hc.relay = newByteBucket(s.cfg.Switchyard.RelayKBps * 1024)
		}
	}
	s.log.Info("host registered", "session", info.ID, "host", info.HostName, "resumed", resumed, "open", open)
	defer func() {
		hs.HostDisconnected(conn)
		s.log.Info("host disconnected", "session", info.ID)
	}()

	ice := make([]proto.ICEServer, 0, len(s.cfg.ICEServers))
	for _, srv := range s.cfg.ICEServers {
		ice = append(ice, proto.ICEServer{URLs: srv.URLs, Username: srv.Username, Credential: srv.Credential})
	}
	registered, _ := json.Marshal(proto.Registered{T: proto.HostRegistered, SessionID: info.ID, Secret: hs.Secret(), ShareBaseURL: s.publicBase(r), Resumed: resumed, ICEServers: ice, Links: s.liveLinkIDs(info.ID)})
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
			// A switchyard's bound on relayed output: the host waits for it.
			if hc.relay != nil {
				if err := hc.relay.take(ctx, len(data)); err != nil {
					return
				}
			}
			s.relayed.add(len(data))
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
// the base its links take, when it last asked for links, and the bucket
// its relayed output drains (nil without a bound).
type hostConnState struct {
	base string
	// addr is an open host's address, for the bound on the links it keeps; "" for a tokened host.
	addr      string
	linkTimes []time.Time
	// updateTimes bounds link_run_update (proto.RunLinkUpdatesPerMinute).
	updateTimes []time.Time
	relay       *byteBucket
}

// byteBucket is a token bucket of bytes: rate a second, burst at most.
// take waits, as long as ctx allows, until n bytes are there, so a host
// that streams more than the bound is slowed rather than cut off: the
// server reads its connection no faster than the bucket refills, and TCP
// does the rest.
// It is safe to share: the open hosts of one address take from one bucket.
type byteBucket struct {
	mu          sync.Mutex
	rate, burst float64
	tokens      float64
	last        time.Time
}

func newByteBucket(bytesPerSecond int) *byteBucket {
	r := float64(bytesPerSecond)
	return &byteBucket{rate: r, burst: 2 * r, tokens: 2 * r, last: time.Now()}
}

func (b *byteBucket) take(ctx context.Context, n int) error {
	for {
		b.mu.Lock()
		now := time.Now()
		b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
		b.last = now
		need := min(float64(n), b.burst)
		if b.tokens >= need {
			b.tokens -= need
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((need - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
	case proto.HostChat:
		var m proto.HostChatMsg
		if json.Unmarshal(data, &m) != nil {
			return false
		}
		// The session is the connection's, whatever m.SessionID says; the
		// message is checked and cleaned by HostChat, and one of a kind this
		// server does not know is dropped, not fatal.
		hs.HostChat(m.Message)
	case proto.HostLink:
		var m proto.HostLinkMsg
		if json.Unmarshal(data, &m) != nil || m.RequestID == "" || len(m.RequestID) > proto.MaxLinkRequestID {
			return false
		}
		s.hostLink(hs, hc, m)
	case proto.HostLinkRevoke:
		var m proto.HostLinkRevokeMsg
		if json.Unmarshal(data, &m) != nil || m.RequestID == "" || len(m.RequestID) > proto.MaxLinkRequestID || m.LinkID == "" || len(m.LinkID) > proto.MaxLinkID {
			return false
		}
		// A revoke is never counted against the link bucket: it must always go through.
		found, _ := s.links.Revoke(hs.Info().ID, m.LinkID)
		if !found && hs.Owner() != "" {
			found, _ = s.links.RevokeGroup(hs.Owner(), m.LinkID)
		}
		if !found {
			_ = hs.Tell(proto.ErrorMsg{T: proto.HostError, Code: "not_found", Message: "no such link", RequestID: m.RequestID})
		} else {
			s.log.Info("link revoked by its host", "session", hs.Info().ID, "link", m.LinkID)
			_ = hs.Tell(proto.LinkRevoked{T: proto.HostLinkRevoked, RequestID: m.RequestID, LinkID: m.LinkID})
		}
	case proto.HostRunLink:
		var m proto.HostRunLinkMsg
		if json.Unmarshal(data, &m) != nil || m.RequestID == "" || len(m.RequestID) > proto.MaxLinkRequestID {
			return false
		}
		s.hostRunLink(hs, hc, m)
	case proto.HostRunLinkUpdate:
		var m proto.HostRunLinkUpdateMsg
		if json.Unmarshal(data, &m) != nil || m.RequestID == "" || len(m.RequestID) > proto.MaxLinkRequestID {
			return false
		}
		s.hostRunLinkUpdate(hs, hc, m)
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
	if !s.keepLink(link, token, hs.Owner(), hc.addr) {
		refuse("link_refused", "this address holds as many links as this switchyard keeps; revoke one, or ask the operator for a host token")
		return
	}
	s.log.Info("link minted for a host", "session", hs.Info().ID, "role", role, "label", link.Label)
	out := proto.LinkCreated{T: proto.HostLinkCreated, RequestID: m.RequestID, URL: hc.base + "/join/" + url.PathEscape(token), Invite: inviteFor(hc.base, token), LinkID: link.ID, Role: string(link.Role), Label: link.Label}
	if link.ExpiresAt != nil {
		out.ExpiresAt = link.ExpiresAt.UTC().Format(time.RFC3339)
	}
	_ = hs.Tell(out)
}

// liveLinkIDs are the ids of the links that still open a hosted session here:
// not revoked, not expired. Never nil, so registered always carries the list.
func (s *Server) liveLinkIDs(sessionID string) []string {
	ids := []string{}
	now := time.Now()
	for _, l := range append(s.links.ListBySession(sessionID), s.links.ListNaming(sessionID)...) {
		if l.Revoked || (l.ExpiresAt != nil && !now.Before(*l.ExpiresAt)) {
			continue
		}
		ids = append(ids, l.ID)
	}
	return ids
}

// groupOf checks a host's run group: within its bounds, and every member
// session named one this host registered here (the same instance). It
// returns the group as the link store keeps it, or the code and words of a
// refusal.
func (s *Server) groupOf(hs *signal.HostedSession, g proto.RunGroup) (share.Group, string, string) {
	if hs.Owner() == "" {
		return share.Group{}, "no_instance", "a run link needs a host that registers with an instance"
	}
	if err := g.Validate(); err != nil {
		return share.Group{}, "invalid_request", err.Error()
	}
	out := share.Group{Name: g.Name}
	for _, m := range g.Members {
		if m.SessionID != "" {
			other, ok := s.hosts.Get(m.SessionID)
			if !ok || other.Owner() != hs.Owner() {
				return share.Group{}, "invalid_request", "session " + m.SessionID + " is not this host's"
			}
		}
		out.Members = append(out.Members, share.GroupMember{Name: m.Name, SessionID: m.SessionID, AgentID: m.AgentID, Status: m.Status})
	}
	return out, "", ""
}

// hostRunLink mints one link to the sessions of a host's crew run and
// answers link_created with the run's id.
func (s *Server) hostRunLink(hs *signal.HostedSession, hc *hostConnState, m proto.HostRunLinkMsg) {
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
	if len(m.Label) > proto.MaxLinkLabel || m.TTLSeconds < 0 || m.TTLSeconds > proto.MaxLinkTTLSeconds {
		refuse("invalid_request", "label or ttlSeconds out of range")
		return
	}
	g, code, msg := s.groupOf(hs, m.Run)
	if code != "" {
		refuse(code, msg)
		return
	}
	hc.linkTimes = append(hc.linkTimes, now)
	link, token, err := s.links.CreateGroupLink(m.Run.ID, hs.Owner(), g, role, m.Label, time.Duration(m.TTLSeconds)*time.Second)
	if err != nil {
		refuse("link_refused", err.Error())
		return
	}
	if !s.keepLink(link, token, hs.Owner(), hc.addr) {
		refuse("link_refused", "this address holds as many links as this switchyard keeps; revoke one, or ask the operator for a host token")
		return
	}
	s.log.Info("run link minted for a host", "run", m.Run.ID, "members", len(g.Members), "role", role)
	out := proto.LinkCreated{T: proto.HostLinkCreated, RequestID: m.RequestID, URL: hc.base + "/join/" + url.PathEscape(token), Invite: inviteFor(hc.base, token), LinkID: link.ID, Role: string(link.Role), Label: link.Label, RunID: m.Run.ID}
	if link.ExpiresAt != nil {
		out.ExpiresAt = link.ExpiresAt.UTC().Format(time.RFC3339)
	}
	_ = hs.Tell(out)
}

// hostRunLinkUpdate gives the links of a host's run its members now and
// answers link_run_updated, or not_found when the run has none here.
func (s *Server) hostRunLinkUpdate(hs *signal.HostedSession, hc *hostConnState, m proto.HostRunLinkUpdateMsg) {
	refuse := func(code, msg string) {
		_ = hs.Tell(proto.ErrorMsg{T: proto.HostError, Code: code, Message: msg, RequestID: m.RequestID})
	}
	now := time.Now()
	hc.updateTimes = slices.DeleteFunc(hc.updateTimes, func(t time.Time) bool { return now.Sub(t) > time.Minute })
	if len(hc.updateTimes) >= proto.RunLinkUpdatesPerMinute {
		refuse("rate_limited", "too many run updates; wait a minute")
		return
	}
	hc.updateTimes = append(hc.updateTimes, now)
	g, code, msg := s.groupOf(hs, m.Run)
	if code != "" {
		refuse(code, msg)
		return
	}
	changed, left := s.links.SetGroup(hs.Owner(), m.Run.ID, g)
	if len(changed) == 0 {
		refuse("not_found", "no links for this run")
		return
	}
	// A session the run no longer names is no longer opened by its links:
	// whoever is attached to it through one of them goes. The links name
	// the new group first, so one attaching meanwhile is refused.
	for _, sid := range left {
		if d, ok := s.registry.Get(sid); ok {
			for _, lid := range changed {
				d.DisconnectLink(lid)
			}
		}
	}
	s.keepGroup(hs.Owner(), m.Run.ID)
	_ = hs.Tell(proto.RunLinkUpdated{T: proto.HostRunLinkUpdated, RequestID: m.RequestID, RunID: m.Run.ID, Links: len(changed)})
}
