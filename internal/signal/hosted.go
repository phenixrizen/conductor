// Package signal brokers hosted sessions: it tracks connected `conductor host`
// processes, forwards WebRTC signaling between viewers and hosts, and relays
// terminal frames when a data channel cannot be established.
package signal

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// Errors reported to viewers and hosts.
var (
	ErrHostGone      = errors.New("signal: host disconnected")
	ErrViewerGone    = errors.New("signal: viewer closed")
	ErrSlowViewer    = errors.New("signal: slow viewer")
	ErrSlowHost      = errors.New("signal: slow host")
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
// are written to the viewer's WebSocket by the API layer.
type Viewer struct {
	ID     string
	Role   session.Role
	LinkID string
	Out    chan []byte

	relay  atomic.Bool
	done   chan struct{}
	once   sync.Once
	reason error
}

func newViewer(id string, role session.Role, linkID string) *Viewer {
	return &Viewer{ID: id, Role: role, LinkID: linkID, Out: make(chan []byte, viewerQueue), done: make(chan struct{})}
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

	mu             sync.Mutex
	info           session.Info
	relayOnly      bool
	conn           *HostConn
	viewers        map[string]*Viewer
	disconnectedAt time.Time
	maxViewers     int
}

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

// DisconnectLink closes viewers attached through linkID.
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

// RelayOnly reports whether the host asked viewers to skip WebRTC.
func (h *HostedSession) RelayOnly() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.relayOnly
}

// Secret returns the resume secret handed to the host.
func (h *HostedSession) Secret() string { return h.secret }

// Connected reports whether a host connection is attached.
func (h *HostedSession) Connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn != nil
}

// AddViewer registers a browser and notifies the host.
func (h *HostedSession) AddViewer(id string, role session.Role, linkID string) (*Viewer, error) {
	h.mu.Lock()
	if h.conn == nil {
		h.mu.Unlock()
		return nil, ErrHostGone
	}
	if len(h.viewers) >= h.maxViewers {
		h.mu.Unlock()
		return nil, ErrTooManyViewer
	}
	v := newViewer(id, role, linkID)
	h.viewers[id] = v
	conn := h.conn
	h.mu.Unlock()
	conn.sendJSON(proto.ViewerJoin{T: proto.HostViewerJoin, ViewerID: id, Role: string(role), LinkID: linkID})
	return v, nil
}

// RemoveViewer unregisters a browser and notifies the host.
func (h *HostedSession) RemoveViewer(v *Viewer) {
	h.mu.Lock()
	_, present := h.viewers[v.ID]
	delete(h.viewers, v.ID)
	conn := h.conn
	h.mu.Unlock()
	v.close(nil)
	if present && conn != nil {
		conn.sendJSON(proto.ViewerRef{T: proto.HostViewerLeave, ViewerID: v.ID})
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
		case proto.TypeInput:
			return session.ErrReadOnly
		case proto.TypeControl:
			if t, _ := proto.ParseHeader(inner.Payload); t == proto.CtlResize {
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
	if status.Ended() && h.info.EndedAt == nil {
		now := time.Now().UTC()
		h.info.EndedAt = &now
	}
	h.mu.Unlock()
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
	for _, v := range viewers {
		v.push(proto.NewError(proto.ErrCodeHostDisconnected, "the host disconnected"))
		v.close(ErrHostGone)
	}
	conn.Close()
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
