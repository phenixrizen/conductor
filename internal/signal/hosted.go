// Package signal brokers hosted sessions: it tracks connected `conductor host`
// processes, forwards WebRTC signaling between viewers and hosts, and relays
// terminal frames when a data channel cannot be established.
package signal

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// Errors reported to viewers and hosts.
var (
	ErrHostGone   = errors.New("signal: host disconnected")
	ErrViewerGone = errors.New("signal: viewer closed")
	ErrSlowViewer = errors.New("signal: slow viewer")
	ErrSlowHost   = errors.New("signal: slow host")
	// ErrRateLimited is session.ErrRateLimited: a report is refused alike
	// whichever kind of session it is for.
	ErrRateLimited   = session.ErrRateLimited
	ErrTooManyViewer = errors.New("signal: too many viewers")
)

// DisconnectGrace is how long a hosted session survives without its host.
const DisconnectGrace = 60 * time.Second

const (
	viewerQueue = 2048
	hostQueue   = 8192
)

// Outbound is one message queued to a host connection.
type Outbound struct {
	Text   []byte // JSON control message
	Binary []byte // RELAY envelope
}

// HostConn is a live control connection from one host process. The API layer
// drains Send and writes to the WebSocket.
type HostConn struct {
	Send chan Outbound
	done chan struct{}
	once sync.Once
}

// NewHostConn creates a host connection with an outbound queue.
func NewHostConn() *HostConn {
	return &HostConn{Send: make(chan Outbound, hostQueue), done: make(chan struct{})}
}

// Done is closed when the connection must be torn down.
func (c *HostConn) Done() <-chan struct{} { return c.done }

// Close marks the connection dead.
func (c *HostConn) Close() { c.once.Do(func() { close(c.done) }) }

func (c *HostConn) send(o Outbound) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.Send <- o:
		return true
	default:
		c.Close()
		return false
	}
}

func (c *HostConn) sendJSON(v any) bool {
	b, err := jsonMarshal(v)
	if err != nil {
		return false
	}
	return c.send(Outbound{Text: b})
}

// Viewer is one browser attached to a hosted session. Frames queued to Out
// are written to the viewer's WebSocket by Pump, which the API layer runs.
type Viewer struct {
	ID        string
	Role      session.Role
	LinkID    string
	LinkLabel string
	Out       chan []byte

	relay  atomic.Bool
	done   chan struct{}
	once   sync.Once
	reason error
	// lapse closes the viewer when its link expires (ViewerOptions'
	// Authorize gave a Grant with Until); set by AddViewerWith, stopped by
	// RemoveViewer, both under the session's mu.
	lapse *time.Timer
}

func newViewer(id string, role session.Role, linkID, linkLabel string) *Viewer {
	return &Viewer{ID: id, Role: role, LinkID: linkID, LinkLabel: linkLabel, Out: make(chan []byte, viewerQueue), done: make(chan struct{})}
}

// Done is closed when the viewer must be disconnected.
func (v *Viewer) Done() <-chan struct{} { return v.done }

// Reason reports why the viewer was closed.
func (v *Viewer) Reason() error {
	select {
	case <-v.done:
		return v.reason
	default:
		return nil
	}
}

// Relay reports whether the viewer switched to the server relay.
func (v *Viewer) Relay() bool { return v.relay.Load() }

func (v *Viewer) close(reason error) {
	v.once.Do(func() {
		v.reason = reason
		close(v.done)
	})
}

// Pump writes the frames queued for the viewer to sink, in order, until the
// viewer is closed, and then closes sink with the reason it was closed for.
// The API layer runs it on its own goroutine for the viewer's connection.
//
// What becomes of the frames still queued when the viewer is closed depends on
// the reason. They are dropped when the link was revoked, when the viewer's own
// queue overflowed (ErrSlowViewer) and when the viewer left (a nil reason). For
// every other reason, the host going away, asking for the close or reporting an
// error for the viewer, they are written first: at most as many as the queue
// holds, and up to the first frame that cannot be written. They are the host's
// last words, the session's final status among them, and the viewer would
// otherwise be told the host is gone without being told why. A viewer that the
// host evicted as too slow (the host asks for the close) is in that second
// group and is drained too.
//
// The close is looked at before each frame, but it does not stop a frame that
// is being written, or one that is picked just as the close lands: a revoked
// viewer can still be written a frame.
func (v *Viewer) Pump(sink session.Sink) {
	for {
		// Once the viewer is closed, the close comes before any frame that is
		// still queued: select would pick at random between the two.
		select {
		case <-v.done:
			v.finish(sink)
			return
		default:
		}
		select {
		case <-v.done:
			v.finish(sink)
			return
		case frame := <-v.Out:
			if err := sink.WriteFrame(frame); err != nil {
				return
			}
		}
	}
}

// finish is Pump's end: the frames the viewer is owed, if any, and the close.
func (v *Viewer) finish(sink session.Sink) {
	reason := v.Reason()
	if framesOwed(reason) {
	owed:
		for {
			select {
			case frame := <-v.Out:
				if err := sink.WriteFrame(frame); err != nil {
					return
				}
			default:
				break owed
			}
		}
	}
	sink.Close(reason)
}

// framesOwed reports whether a viewer closed for reason is still sent the frames
// queued for it: it is unless the link was revoked, the viewer's own queue
// overflowed or the viewer left.
func framesOwed(reason error) bool {
	switch {
	case reason == nil, errors.Is(reason, session.ErrRevoked), errors.Is(reason, ErrSlowViewer):
		return false
	}
	return true
}

func (v *Viewer) push(frame []byte) {
	select {
	case <-v.done:
		return
	default:
	}
	select {
	case v.Out <- frame:
	default:
		v.close(ErrSlowViewer)
	}
}

// HostedSession is a session whose PTY lives in a host process. It implements
// session.Driver.
type HostedSession struct {
	hub    *Hub
	secret string
	log    *slog.Logger
	// owner is the hash of the host instance that registered the session
	// (hex), "" for a host that gave none: what ties a host's connections
	// together, for links over several of its sessions.
	owner string

	mu             sync.Mutex
	info           session.Info
	relayOnly      bool
	agentTokenHash [32]byte
	hasAgentToken  bool
	conn           *HostConn
	viewers        map[string]*Viewer
	disconnectedAt time.Time
	maxViewers     int
	// retired says the session left the server's registry (Retire): no
	// viewer is added any more.
	retired bool
	// events limits what the server forwards to the host on an agent's behalf
	// (ForwardActivity, and SetAttentionFull with forward): the host's
	// connection also carries its viewers' input, and closes when its queue is
	// full. Guarded by mu.
	events session.EventBucket
}

// Owner is the hash of the host instance that registered the session, "" when none.
func (h *HostedSession) Owner() string { return h.owner }

// Info returns the session description.
func (h *HostedSession) Info() session.Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	info := h.info
	info.Viewers = len(h.viewers)
	return info
}

// Stop asks the host to terminate its process. Completion is reported
// asynchronously through a host status message.
func (h *HostedSession) Stop(ctx context.Context) error {
	h.mu.Lock()
	conn := h.conn
	id := h.info.ID
	h.mu.Unlock()
	if conn == nil {
		return ErrHostGone
	}
	if !conn.sendJSON(proto.HostStopMsg{T: proto.HostStop, SessionID: id}) {
		return ErrSlowHost
	}
	return nil
}

// DisconnectLink closes viewers attached through linkID. It finds them
// under the lock AddViewerWith registers them under, so a caller that
// marked the link first (a revoke) misses none: a viewer added after this
// finds the mark (ViewerOptions.Authorize).
func (h *HostedSession) DisconnectLink(linkID string) {
	h.mu.Lock()
	var hit []*Viewer
	for _, v := range h.viewers {
		if v.LinkID == linkID {
			hit = append(hit, v)
		}
	}
	h.mu.Unlock()
	for _, v := range hit {
		v.close(session.ErrRevoked)
	}
}

// AgentTokenOK compares a presented agent token in constant time.
func (h *HostedSession) AgentTokenOK(tok string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.hasAgentToken || tok == "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(sum[:], h.agentTokenHash[:]) == 1
}

func (h *HostedSession) setAgentToken(tok string) {
	h.agentTokenHash = sha256.Sum256([]byte(tok))
	h.hasAgentToken = tok != ""
}

// SetAttention records an attention change without prompt details. See
// SetAttentionFull.
func (h *HostedSession) SetAttention(state session.AttentionState, message, source string, forward bool) error {
	return h.SetAttentionFull(state, message, source, "", nil, forward)
}

// SetAttentionFull records an attention change. When forward is true (API
// origin), the report spends a token of the session's bucket, the one
// ForwardActivity spends from, whether a host is connected or not: it returns
// ErrRateLimited, having changed nothing, when there is none. A connected host
// is told, so its viewers see the change too; without one the change is held
// on the server. A change from the host (forward false) never spends one.
func (h *HostedSession) SetAttentionFull(state session.AttentionState, message, source, kind string, options []session.Option, forward bool) error {
	if !state.Valid() {
		return nil
	}
	message = session.CleanMessage(message)
	if !session.ValidKind(kind) {
		kind = ""
	}
	options = session.CleanOptions(options)
	if state == session.AttentionNone {
		kind, options = "", nil
	}
	h.mu.Lock()
	conn := h.conn
	if forward && !h.events.Take(time.Now()) {
		h.mu.Unlock()
		return ErrRateLimited
	}
	att := session.Attention{State: state, Message: message, Source: source, Kind: kind, Options: options}
	if state != session.AttentionNone {
		now := time.Now().UTC()
		att.Since = &now
	}
	h.info.Attention = att
	h.mu.Unlock()
	if forward && conn != nil {
		conn.sendJSON(hostAttentionMsg("", att))
	}
	h.notifyChange()
	return nil
}

// hostAttentionMsg encodes an attention change for the host control link.
func hostAttentionMsg(sessionID string, att session.Attention) proto.HostAttentionMsg {
	m := proto.HostAttentionMsg{T: proto.HostAttention, SessionID: sessionID, State: string(att.State), Message: att.Message, Source: att.Source, Kind: att.Kind}
	for _, o := range att.Options {
		m.Options = append(m.Options, proto.AttentionOption{Label: o.Label, Input: o.Input})
	}
	return m
}

// OptionsFromProto converts wire options to session options.
func OptionsFromProto(in []proto.AttentionOption) []session.Option {
	if len(in) == 0 {
		return nil
	}
	out := make([]session.Option, 0, len(in))
	for _, o := range in {
		out = append(out, session.Option{Label: o.Label, Input: o.Input})
	}
	return out
}

func (h *HostedSession) notifyChange() {
	if h.hub != nil && h.hub.OnChange != nil {
		h.hub.OnChange(h.Info())
	}
}

// RelayOnly reports whether the host asked viewers to skip WebRTC.
func (h *HostedSession) RelayOnly() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.relayOnly
}

// LinkViewers counts server-side viewers per share link id.
func (h *HostedSession) LinkViewers() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]int{}
	for _, v := range h.viewers {
		if v.LinkID != "" && v.Reason() == nil {
			out[v.LinkID]++
		}
	}
	return out
}

// Secret returns the resume secret handed to the host.
func (h *HostedSession) Secret() string { return h.secret }

// Connected reports whether a host connection is attached.
func (h *HostedSession) Connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn != nil
}

// AddViewer registers a browser and notifies the host. linkLabel travels to
// the host for its roster; the link token never does. See AddViewerWith.
func (h *HostedSession) AddViewer(id string, role session.Role, linkID, linkLabel string) (*Viewer, error) {
	return h.AddViewerWith(ViewerOptions{ID: id, Role: role, LinkID: linkID, LinkLabel: linkLabel})
}

// ViewerOptions describe a browser joining a hosted session.
type ViewerOptions struct {
	ID        string
	Role      session.Role
	LinkID    string
	LinkLabel string
	// Authorize, when set, is session.AttachOptions.Authorize for a hosted
	// session: called under the session's lock, in the critical section
	// that registers the viewer, its role replaces Role and its error
	// refuses the viewer; a Grant with Until closes the viewer with
	// session.ErrExpired then. It must not call the session.
	Authorize func() (session.Grant, error)
}

// AddViewerWith registers a browser and notifies the host.
func (h *HostedSession) AddViewerWith(o ViewerOptions) (*Viewer, error) {
	role := o.Role
	h.mu.Lock()
	if h.retired {
		h.mu.Unlock()
		return nil, session.ErrSessionEnded
	}
	// The credential first: a link revoked meanwhile says so, whatever the
	// host's state.
	var until time.Time
	if o.Authorize != nil {
		g, err := o.Authorize()
		if err == nil && !g.Role.Valid() {
			err = errors.New("signal: invalid role")
		}
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		role, until = g.Role, g.Until
	}
	if h.conn == nil {
		h.mu.Unlock()
		return nil, ErrHostGone
	}
	if len(h.viewers) >= h.maxViewers {
		h.mu.Unlock()
		return nil, ErrTooManyViewer
	}
	v := newViewer(o.ID, role, o.LinkID, o.LinkLabel)
	h.viewers[o.ID] = v
	if !until.IsZero() {
		v.lapse = time.AfterFunc(time.Until(until), func() { v.close(session.ErrExpired) })
	}
	conn := h.conn
	h.mu.Unlock()
	conn.sendJSON(proto.ViewerJoin{T: proto.HostViewerJoin, ViewerID: o.ID, Role: string(role), LinkID: o.LinkID, LinkLabel: o.LinkLabel})
	h.notifyChange()
	return v, nil
}

// RemoveViewer unregisters a browser and notifies the host.
func (h *HostedSession) RemoveViewer(v *Viewer) {
	h.mu.Lock()
	_, present := h.viewers[v.ID]
	delete(h.viewers, v.ID)
	conn := h.conn
	if v.lapse != nil {
		v.lapse.Stop()
	}
	h.mu.Unlock()
	v.close(nil)
	if present && conn != nil {
		conn.sendJSON(proto.ViewerRef{T: proto.HostViewerLeave, ViewerID: v.ID})
	}
	if present {
		h.notifyChange()
	}
}

// ForwardOffer sends a viewer's SDP offer to the host.
func (h *HostedSession) ForwardOffer(v *Viewer, sdp string) error {
	return h.toHost(proto.ViewerSDP{T: proto.HostOffer, ViewerID: v.ID, SDP: sdp})
}

// ForwardICE sends a viewer's ICE candidate to the host.
func (h *HostedSession) ForwardICE(v *Viewer, c proto.ICECandidate) error {
	return h.toHost(proto.ViewerICE{T: proto.HostICE, ViewerID: v.ID, Candidate: c})
}

// Tell sends the host a message of the server's own (a link_created, an
// error answering its request): ErrHostGone without a host, ErrSlowHost
// when its queue is full.
func (h *HostedSession) Tell(v any) error { return h.toHost(v) }

// StartRelay switches a viewer to relay mode and tells the host.
func (h *HostedSession) StartRelay(v *Viewer) error {
	v.relay.Store(true)
	return h.toHost(proto.ViewerRef{T: proto.HostRelayStart, ViewerID: v.ID})
}

// RelayToHost wraps a viewer frame in a RELAY envelope for the host. Input
// and resize from view-role viewers are dropped here as defence in depth.
func (h *HostedSession) RelayToHost(v *Viewer, inner proto.Frame) error {
	if !v.relay.Load() {
		return errors.New("signal: viewer is not in relay mode")
	}
	if v.Role != session.RoleControl {
		switch inner.Type {
		case proto.TypeInput, proto.TypeFileWrite:
			return session.ErrReadOnly
		case proto.TypeControl:
			// A chat post passes for every role (the host refuses one that is
			// also for the agent); typing a message into the agent does not.
			if t, _ := proto.ParseHeader(inner.Payload); t == proto.CtlResize || t == proto.CtlSubmit || t == proto.CtlChatSend || t == proto.CtlNvimOpen || t == proto.CtlNvimInput || t == proto.CtlNvimClose || t == proto.CtlNvimSwap {
				return session.ErrReadOnly
			}
		}
	}
	env, err := proto.EncodeRelay(v.ID, proto.Encode(inner.Type, inner.Payload))
	if err != nil {
		return err
	}
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		return ErrHostGone
	}
	if !conn.send(Outbound{Binary: env}) {
		return ErrSlowHost
	}
	return nil
}

func (h *HostedSession) toHost(v any) error {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		return ErrHostGone
	}
	if !conn.sendJSON(v) {
		return ErrSlowHost
	}
	return nil
}

func (h *HostedSession) viewer(id string) *Viewer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.viewers[id]
}

// --- activity ---

// maxEntryBy bounds the subscriber id of an entry a host reports: ids are 16
// characters, and session.CleanEntry leaves By alone (see session.CleanID).
const maxEntryBy = 64

// ForwardActivity sends an event that an agent reported through the API to
// the host. A hosted session has no activity log on the server: the host
// records the event in its own and reports it back, which reaches
// HostActivity. e is cleaned again here, which changes nothing for an entry
// that was cleaned already, so that no caller can push the message past
// proto.MaxHostMessage, which the host takes as a protocol error.
//
// Each event spends a token of the session's bucket (see SetAttentionFull):
// the host's connection also carries its viewers' input and closes when its
// queue is full, and an event can be several KiB. It returns ErrHostGone when
// no host is connected, before it spends anything, ErrRateLimited when the
// bucket is empty and ErrSlowHost when the host's queue is full.
func (h *HostedSession) ForwardActivity(e session.ActivityEntry) error {
	msg := hostActivityMsg(session.CleanEntry(e))
	h.mu.Lock()
	conn := h.conn
	if conn == nil {
		h.mu.Unlock()
		return ErrHostGone
	}
	if !h.events.Take(time.Now()) {
		h.mu.Unlock()
		return ErrRateLimited
	}
	h.mu.Unlock()
	if !conn.sendJSON(msg) {
		return ErrSlowHost
	}
	return nil
}

// HostActivity takes an activity entry the host reports, with the attention
// state the host says an attention entry records, and hands them to the hub's
// OnActivity. The host is not trusted with them: an entry of a type this
// server does not know is dropped (a newer host may have more), the text is
// cut to its limits, By included (a By still over maxEntryBy bytes once
// cleaned is dropped), and a missing or unreadable time becomes the time of
// receipt; the state is kept only with an attention entry and only when it is
// needs_input, working or done. The entry is attributed to this session
// whatever session the host named. HostActivity does not send the entry back
// to the host.
func (h *HostedSession) HostActivity(a proto.Activity, state string) {
	if !session.ValidEventType(a.Type) {
		return
	}
	e := session.CleanEntry(session.EntryFromProto(a))
	e.By = session.CleanID(e.By, maxEntryBy)
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	var recorded session.AttentionState
	if e.Type == session.ActivityAttention {
		switch s := session.AttentionState(state); s {
		case session.AttentionNeedsInput, session.AttentionWorking, session.AttentionDone:
			recorded = s
		}
	}
	h.mu.Lock()
	id := h.info.ID
	h.mu.Unlock()
	if h.hub != nil && h.hub.OnActivity != nil {
		h.hub.OnActivity(id, e, recorded)
	}
}

// HostChat takes a chat message the host's session kept and hands it to the
// hub's OnChat, cleaned as the host is not trusted with it: a kind this
// server does not know is dropped, the scope is this session's (a host has
// no run), the ids are bounded, the names cleaned, the text cleaned and cut
// to proto.MaxChatText, a missing or unreadable time becomes the time of
// receipt, and `to` is the agent or nothing. The message is attributed to
// this session whatever session the host named. Nothing goes back to the
// host.
func (h *HostedSession) HostChat(m proto.ChatMessage) {
	switch m.Kind {
	case proto.ChatKindMessage, proto.ChatKindSystem, proto.ChatKindSentToAgent:
	default:
		return
	}
	id := session.CleanID(m.ID, maxEntryBy)
	role := session.Role(m.By.Role)
	if id == "" || !role.Valid() {
		return
	}
	at, err := time.Parse(time.RFC3339Nano, m.At)
	if err != nil {
		at = time.Now().UTC()
	}
	text := session.CleanChatText(m.Text)
	for len(text) > proto.MaxChatText {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}
	out := session.ChatMessage{
		ID:    id,
		At:    at.UTC(),
		Scope: proto.ChatScopeSession,
		Kind:  m.Kind,
		By:    session.ChatBy{ID: session.CleanID(m.By.ID, maxEntryBy), Name: session.CleanName(m.By.Name), Role: role},
		Text:  strings.TrimSpace(text),
		Ref:   session.CleanID(m.Ref, maxEntryBy),
	}
	if m.To == proto.ChatToAgent {
		out.To = m.To
	}
	if m.Event == "join" || m.Event == "leave" {
		out.Event = m.Event
	}
	h.mu.Lock()
	sid := h.info.ID
	h.mu.Unlock()
	if h.hub != nil && h.hub.OnChat != nil {
		h.hub.OnChat(sid, out)
	}
}

// hostActivityMsg wraps an entry in the host `activity` message. The server
// names no session: the connection says which.
func hostActivityMsg(e session.ActivityEntry) proto.HostActivityMsg {
	return proto.HostActivityMsg{T: proto.HostActivity, Entry: session.EntryToProto(e)}
}

// --- messages from the host ---

// HostAnswer delivers an SDP answer to a viewer.
func (h *HostedSession) HostAnswer(viewerID, sdp string) {
	if v := h.viewer(viewerID); v != nil {
		if f, err := proto.EncodeJSON(proto.TypeSignal, proto.SDP{T: proto.SigAnswer, SDP: sdp}); err == nil {
			v.push(f)
		}
	}
}

// HostICE delivers a host ICE candidate to a viewer.
func (h *HostedSession) HostICE(viewerID string, c proto.ICECandidate) {
	if v := h.viewer(viewerID); v != nil {
		if f, err := proto.EncodeJSON(proto.TypeSignal, proto.ICE{T: proto.SigICE, Candidate: c}); err == nil {
			v.push(f)
		}
	}
}

// HostRelayFrame delivers a relayed terminal frame to a viewer.
func (h *HostedSession) HostRelayFrame(viewerID string, inner proto.Frame) {
	if v := h.viewer(viewerID); v != nil {
		v.push(proto.Encode(inner.Type, inner.Payload))
	}
}

// HostViewerError forwards a per-viewer error and closes the viewer.
func (h *HostedSession) HostViewerError(viewerID, code, message string) {
	if v := h.viewer(viewerID); v != nil {
		v.push(proto.NewError(code, message))
		v.close(errors.New(code))
	}
}

// HostViewerClosed closes a viewer at the host's request.
func (h *HostedSession) HostViewerClosed(viewerID string) {
	if v := h.viewer(viewerID); v != nil {
		v.close(ErrViewerGone)
	}
}

// HostStatus records a status change reported by the host.
func (h *HostedSession) HostStatus(status session.Status, exitCode *int) {
	h.mu.Lock()
	h.info.Status = status
	h.info.ExitCode = exitCode
	if status.Ended() {
		if h.info.EndedAt == nil {
			now := time.Now().UTC()
			h.info.EndedAt = &now
		}
		// An ended session needs nothing, whatever the host said before. A
		// host that only went away (host_disconnected) may come back: its
		// session keeps its state.
		h.info.Attention = session.Attention{}
	}
	h.mu.Unlock()
	h.notifyChange()
}

// HostResize records the host's current PTY size.
func (h *HostedSession) HostResize(cols, rows uint16) {
	h.mu.Lock()
	h.info.Cols, h.info.Rows = cols, rows
	h.mu.Unlock()
}

// HostDisconnected detaches the control connection and closes every viewer.
func (h *HostedSession) HostDisconnected(conn *HostConn) {
	h.mu.Lock()
	if h.conn != conn {
		h.mu.Unlock()
		return
	}
	h.conn = nil
	h.disconnectedAt = time.Now()
	if !h.info.Status.Ended() {
		h.info.Status = session.StatusHostDisconnected
	}
	viewers := make([]*Viewer, 0, len(h.viewers))
	for _, v := range h.viewers {
		viewers = append(viewers, v)
	}
	h.viewers = map[string]*Viewer{}
	h.mu.Unlock()
	// Each viewer is closed with ErrHostGone: Pump writes what the host still
	// owed it, then closes its sink, which tells the viewer why with one error
	// frame (host_disconnected).
	for _, v := range viewers {
		v.close(ErrHostGone)
	}
	conn.Close()
	h.notifyChange()
}

// Retire is the session leaving the server's registry: every viewer is
// closed as at the session's end, and none is added after
// (session.ErrSessionEnded). Whoever closes a link's viewers finds them
// through the registry, so none may stay where it no longer looks.
func (h *HostedSession) Retire() {
	h.mu.Lock()
	h.retired = true
	viewers := make([]*Viewer, 0, len(h.viewers))
	for _, v := range h.viewers {
		viewers = append(viewers, v)
	}
	h.mu.Unlock()
	for _, v := range viewers {
		v.close(session.ErrSessionEnded)
	}
}

// CloseViewers disconnects every viewer with reason (server shutdown).
func (h *HostedSession) CloseViewers(reason error) {
	h.mu.Lock()
	viewers := make([]*Viewer, 0, len(h.viewers))
	for _, v := range h.viewers {
		viewers = append(viewers, v)
	}
	h.mu.Unlock()
	for _, v := range viewers {
		v.close(reason)
	}
}
