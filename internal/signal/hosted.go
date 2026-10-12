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
	"slices"
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

// The queues between the server and a hosted session's ends hold a number of
// messages and a number of bytes, whichever fills first.
const (
	viewerQueue = 2048
	// viewerQueueBytes bounds the frames queued to one viewer: past it the
	// viewer is closed as too slow (ErrSlowViewer). It is how far a viewer
	// on a slow connection may fall behind its host's output, a thousand
	// whole output frames, or a dozen file frames (proto.MaxFrame); the
	// frames alone, of up to proto.MaxFrame each, would let it hold GiBs.
	viewerQueueBytes = 16 << 20
	hostQueue        = 8192
	// hostQueueBytes bounds what is queued to a host connection.
	hostQueueBytes = 8 << 20
	// What the viewers' messages (their joins and leaves among them) may
	// fill of the host's queue (sendFrom): the rest is kept for the
	// server's own (the answer to the host's request, an agent's report),
	// so viewers that send more than the host reads, or come and go faster
	// than it reads, wait for room and never close the host's connection.
	hostViewerBytes = hostQueueBytes - 1<<20
	hostViewerSlots = hostQueue - 512
)

// Outbound is one message queued to a host connection.
type Outbound struct {
	Text   []byte // JSON control message
	Binary []byte // RELAY envelope
}

// size is what o counts for in its queue's bytes.
func (o Outbound) size() int { return len(o.Text) + len(o.Binary) }

// HostConn is a live control connection from one host process. The API layer
// drains Send, calls Took with each message it takes, and writes it to the
// WebSocket.
type HostConn struct {
	Send chan Outbound
	done chan struct{}
	once sync.Once

	// mu is held by every send into Send, with bytes, the bytes of what Send
	// holds; waiting, the viewers' messages waiting for room, in their order
	// (sendFrom); and room, which they wait on: Took closes it, as does a
	// waiter that leaves the head of the line, and the next to wait makes
	// another.
	mu      sync.Mutex
	bytes   int
	waiting []*roomWaiter
	room    chan struct{}
}

// roomWaiter is a viewer's message in the line for room in a host's queue:
// its place, and its size.
type roomWaiter struct{ n int }

// NewHostConn creates a host connection with an outbound queue.
func NewHostConn() *HostConn {
	return &HostConn{Send: make(chan Outbound, hostQueue), done: make(chan struct{})}
}

// Done is closed when the connection must be torn down.
func (c *HostConn) Done() <-chan struct{} { return c.done }

// Close marks the connection dead.
func (c *HostConn) Close() { c.once.Do(func() { close(c.done) }) }

// Took tells the connection its writer took o from Send: o's bytes leave
// the queue, and the viewers' messages waiting for room try again.
func (c *HostConn) Took(o Outbound) {
	c.mu.Lock()
	c.bytes = max(0, c.bytes-o.size())
	c.wake()
	c.mu.Unlock()
}

// wake has the viewers' messages waiting for room look again. Called with mu
// held.
func (c *HostConn) wake() {
	if c.room != nil {
		close(c.room)
		c.room = nil
	}
}

// queued reports the bytes Send holds.
func (c *HostConn) queued() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// send queues a message of the server's own without waiting. One that finds
// the queue full, in messages or in bytes, closes the connection: a host
// that reads that little is too slow to keep.
func (c *HostConn) send(o Outbound) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	n := o.size()
	c.mu.Lock()
	if c.bytes+n > hostQueueBytes {
		c.mu.Unlock()
		c.Close()
		return false
	}
	select {
	case c.Send <- o:
		c.bytes += n
		c.mu.Unlock()
		return true
	default:
		c.mu.Unlock()
		c.Close()
		return false
	}
}

// sendFrom queues a message a viewer sent (a frame it relays, its offer, a
// candidate) within the viewers' share of the queue, hostViewerBytes and
// hostViewerSlots: past it, sendFrom waits until the writer takes enough
// (Took). The viewers' messages that wait get room in their order, whatever
// their sizes: one that comes while others wait waits behind them. A viewer
// that sends more than the host reads is so held in its own read loop; the
// host's loop and the server's messages never wait for it, and it never
// closes the host's connection. It returns ErrSlowHost when the connection
// is closed, ErrViewerGone when gone is closed (the viewer was let go) before
// the message is queued, and ErrViewerGone when stop is closed while it
// waits: a message that needs no wait is queued whatever stop says.
func (c *HostConn) sendFrom(o Outbound, gone, stop <-chan struct{}) error {
	select {
	case <-c.done:
		return ErrSlowHost
	case <-gone:
		return ErrViewerGone
	default:
	}
	me := c.enter(o)
	if me == nil {
		return nil
	}
	return c.await(me, o, stop)
}

// enter is sendFrom's first half: o is queued at once when nobody waits and
// the viewers' share has room (nil), or takes its place in line (the waiter
// await then waits as). What is sent after o goes after it either way.
func (c *HostConn) enter(o Outbound) *roomWaiter {
	n := o.size()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.waiting) == 0 && c.admit(o, n) {
		return nil
	}
	me := &roomWaiter{n: n}
	c.waiting = append(c.waiting, me)
	return me
}

// await is sendFrom's second half: o, in line as me, waits for room in its
// turn, until the connection closes (ErrSlowHost) or stop does
// (ErrViewerGone), when it leaves the line.
func (c *HostConn) await(me *roomWaiter, o Outbound, stop <-chan struct{}) error {
	n := me.n
	c.mu.Lock()
	for {
		if c.waiting[0] == me && c.admit(o, n) {
			c.waiting = c.waiting[1:]
			c.wake() // the next in line may fit as well
			c.mu.Unlock()
			return nil
		}
		if c.room == nil {
			c.room = make(chan struct{})
		}
		room := c.room
		c.mu.Unlock()
		var err error
		select {
		case <-room:
		case <-c.done:
			err = ErrSlowHost
		case <-stop:
			err = ErrViewerGone
		}
		c.mu.Lock()
		if err != nil {
			if c.waiting[0] == me {
				c.wake()
			}
			c.waiting = slices.DeleteFunc(c.waiting, func(w *roomWaiter) bool { return w == me })
			c.mu.Unlock()
			return err
		}
	}
}

// admit queues o, of n bytes, when the viewers' share has room for it (any
// message when the queue is empty). Called with mu held.
func (c *HostConn) admit(o Outbound, n int) bool {
	if (c.bytes != 0 && c.bytes+n > hostViewerBytes) || len(c.Send) >= hostViewerSlots {
		return false
	}
	select {
	case c.Send <- o:
		c.bytes += n
		return true
	default:
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
	// queued is the bytes of the frames in Out, bounded by viewerQueueBytes.
	queued atomic.Int64

	relay  atomic.Bool
	done   chan struct{}
	once   sync.Once
	reason error
	// stop is closed when the viewer is closed, or before that when its
	// connection fails (ConnFailed): what waits on the viewer's behalf (its
	// join, a message of its for room in the host's queue) ends with it.
	stop     chan struct{}
	stopOnce sync.Once
	// failed is set by ConnFailed before it closes stop.
	failed atomic.Bool
	// lapse closes the viewer when its link expires (ViewerOptions'
	// Authorize gave a Grant with Until); set by AddViewerWith, stopped by
	// RemoveViewer, both under the session's mu.
	lapse *time.Timer
	// charge is ViewerOptions.Charge.
	charge func(n int) error
	// joinDone is closed once the viewer's join has gone to the host, or
	// will not (joinSent false: the viewer or the connection closed first,
	// or its charge failed); joinSent is written before, and read after.
	joinDone chan struct{}
	joinSent bool
}

// chargeHost tells the viewer's Charge of a message of n bytes it sends the
// host.
func (v *Viewer) chargeHost(n int) error {
	if v.charge == nil {
		return nil
	}
	return v.charge(n)
}

// joined waits for the viewer's join, which every message of the viewer's
// to the host follows: ErrViewerGone when the viewer closes, or its
// connection fails, while the join waits, or the join did not go.
func (v *Viewer) joined() error {
	select {
	case <-v.joinDone:
	default:
		select {
		case <-v.joinDone:
		case <-v.stop:
			return ErrViewerGone
		}
	}
	if !v.joinSent {
		return ErrViewerGone
	}
	return nil
}

func newViewer(id string, role session.Role, linkID, linkLabel string) *Viewer {
	return &Viewer{ID: id, Role: role, LinkID: linkID, LinkLabel: linkLabel, Out: make(chan []byte, viewerQueue), done: make(chan struct{}), stop: make(chan struct{}), joinDone: make(chan struct{})}
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
	v.halt()
}

// ConnFailed tells the viewer its connection has failed (a write to it, a
// ping): what waits on its behalf, its join or a message of its for room in
// the host's queue, stops waiting, and the message is dropped. The viewer is
// not closed: what it sent before still goes to the host, behind what went
// before, when it needs no wait; whoever serves it lets it go
// (RemoveViewer) once it has read what the connection held, and its leave
// comes after.
func (v *Viewer) ConnFailed() {
	v.failed.Store(true)
	v.halt()
}

// Failed reports whether the viewer's connection has failed (ConnFailed). A
// message of the viewer's that stopped waiting for it sees it so.
func (v *Viewer) Failed() bool { return v.failed.Load() }

func (v *Viewer) halt() { v.stopOnce.Do(func() { close(v.stop) }) }

// Pump writes the frames queued for the viewer to sink, in order, until the
// viewer is closed, and then closes sink with the reason it was closed for.
// The API layer runs it on its own goroutine for the viewer's connection. A
// frame that cannot be written ends it at once, sink left as it is: the
// connection has failed (ConnFailed), and the viewer stays until whoever
// serves it lets it go, having read what the connection still held.
//
// What becomes of the frames still queued when the viewer is closed depends on
// the reason. They are dropped when the link was revoked (or expired), when the
// viewer's own queue overflowed (ErrSlowViewer) and when the viewer left (a nil
// reason). For every other reason, the session's end (its links end with it,
// EndLink, as does its leaving the server, Retire), the host going
// away, asking for the close or reporting an error for the viewer, they are
// written first: at most as many as the queue holds, and up to the first frame
// that cannot be written. They are the session's last output, its final status
// among them, which the host relayed before it reported the end, and the
// viewer would otherwise be told the session ended, or the host is gone,
// without what it printed last. A viewer that the host evicted as too slow
// (the host asks for the close) is in that second group and is drained too.
// Whoever serves the viewer bounds how long that may take: a viewer that reads
// too little is cut off (the sink's write fails, and Pump ends).
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
			v.queued.Add(-int64(len(frame)))
			if err := sink.WriteFrame(frame); err != nil {
				// The connection failed: what waits on the viewer's behalf
				// (a frame for the host's queue) stops.
				v.ConnFailed()
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
				v.queued.Add(-int64(len(frame)))
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
// queued for it: it is unless the link was revoked (or expired), the viewer's
// own queue overflowed or the viewer left. A viewer the session's end closed
// (session.ErrSessionEnded) is.
func framesOwed(reason error) bool {
	switch {
	case reason == nil, errors.Is(reason, session.ErrRevoked), errors.Is(reason, ErrSlowViewer):
		return false
	}
	return true
}

// push queues a frame for the viewer without waiting. A viewer whose queue
// would hold more than viewerQueue frames or viewerQueueBytes bytes is closed
// as too slow (ErrSlowViewer).
func (v *Viewer) push(frame []byte) {
	select {
	case <-v.done:
		return
	default:
	}
	n := int64(len(frame))
	if v.queued.Add(n) > viewerQueueBytes {
		v.queued.Add(-n)
		v.close(ErrSlowViewer)
		return
	}
	select {
	case v.Out <- frame:
	default:
		v.queued.Add(-n)
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

// DisconnectLink closes the viewers attached through linkID as revoked,
// whether or not the session has ended (a run's link keeps its viewers on a
// member that ended, until it is revoked): each is written nothing more
// (Pump). It finds them under the lock AddViewerWith registers them under,
// so a caller that marked the link first (a revoke) misses none: a viewer
// added after this finds the mark (ViewerOptions.Authorize).
func (h *HostedSession) DisconnectLink(linkID string) { h.closeLink(linkID, session.ErrRevoked) }

// EndLink closes the viewers attached through linkID, one of the session's
// own links, as the session's end, which ends its links with it (the
// caller marks the link first, as a revoke does), as a server session's
// are (session.Local.EndLink): each is still written what was queued for
// it, the session's last output, and then told the session ended (Pump).
// Otherwise it is DisconnectLink.
func (h *HostedSession) EndLink(linkID string) { h.closeLink(linkID, session.ErrSessionEnded) }

// closeLink is DisconnectLink and EndLink, which close for reason.
func (h *HostedSession) closeLink(linkID string, reason error) {
	h.mu.Lock()
	var hit []*Viewer
	for _, v := range h.viewers {
		if v.LinkID == linkID {
			hit = append(hit, v)
		}
	}
	h.mu.Unlock()
	for _, v := range hit {
		v.close(reason)
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
	// Charge, when set, is told the size of each message the viewer sends
	// the host as the host's connection carries it (a relayed frame in its
	// envelope, an offer or a candidate as encoded for the host), before
	// the message waits for room: what the server bounds is what goes to
	// the host. It is called on the goroutine that sends (RelayToHost,
	// ForwardOffer, ForwardICE, StartRelay); an error drops the message
	// and is returned.
	Charge func(n int) error
	// ChargeJoin, when set, is told the size of the viewer's join and leave
	// together, as the host's connection carries them, before the join
	// goes: a viewer that comes and goes is charged both. It is called on a
	// goroutine of its own, not the one that sends, and must return once
	// stop (the viewer's closing, or its connection failing) is closed; an
	// error drops the join, and with it the leave.
	ChargeJoin func(stop <-chan struct{}, n int) error
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
	v.charge = o.Charge
	h.viewers[o.ID] = v
	if !until.IsZero() {
		v.lapse = time.AfterFunc(time.Until(until), func() { v.close(session.ErrExpired) })
	}
	conn := h.conn
	h.mu.Unlock()
	h.sendJoin(conn, v, proto.ViewerJoin{T: proto.HostViewerJoin, ViewerID: o.ID, Role: string(role), LinkID: o.LinkID, LinkLabel: o.LinkLabel}, o.ChargeJoin)
	h.notifyChange()
	return v, nil
}

// sendJoin sends the host v's join without holding up the caller, which
// goes on to serve the viewer (and so notices it leave). The join is the
// viewer's own message: charged to it with its leave (chargeJoin), and
// within the viewers' share of the host's queue in its turn, so viewers
// coming and going never spend the room kept for the server's messages.
// Without a charge it takes its place in line at once; with one, it does
// once charged. Either way it waits on a goroutine of its own, and every
// later message of the viewer's waits for it (Viewer.joined).
func (h *HostedSession) sendJoin(conn *HostConn, v *Viewer, msg proto.ViewerJoin, chargeJoin func(stop <-chan struct{}, n int) error) {
	join, err := jsonMarshal(msg)
	if err != nil {
		close(v.joinDone)
		return
	}
	o := Outbound{Text: join}
	if chargeJoin == nil {
		me := conn.enter(o)
		if me == nil {
			v.joinSent = true
			close(v.joinDone)
			return
		}
		go func() {
			v.joinSent = conn.await(me, o, v.stop) == nil
			close(v.joinDone)
		}()
		return
	}
	leave, _ := jsonMarshal(proto.ViewerRef{T: proto.HostViewerLeave, ViewerID: v.ID})
	go func() {
		defer close(v.joinDone)
		if chargeJoin(v.stop, len(join)+len(leave)) != nil {
			return
		}
		v.joinSent = conn.sendFrom(o, v.done, v.stop) == nil
	}()
}

// RemoveViewer unregisters a browser and notifies the host. The leave goes
// as the viewer's join did, within the viewers' share of the host's queue
// (charged with the join): it waits for room until the host's connection
// closes, and is never dropped, for the host would keep a viewer it was not
// told left. A viewer whose join never went sends none.
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
	if !present {
		return
	}
	// The join stops waiting as the viewer closes; one that never went
	// needs no leave.
	<-v.joinDone
	if conn != nil && v.joinSent {
		if leave, err := jsonMarshal(proto.ViewerRef{T: proto.HostViewerLeave, ViewerID: v.ID}); err == nil {
			_ = conn.sendFrom(Outbound{Text: leave}, nil, nil)
		}
	}
	h.notifyChange()
}

// ForwardOffer sends a viewer's SDP offer to the host. Like every message a
// viewer sends the host, it waits while the viewers' share of the host's
// queue is full (HostConn.sendFrom).
func (h *HostedSession) ForwardOffer(v *Viewer, sdp string) error {
	return h.fromViewer(v, proto.ViewerSDP{T: proto.HostOffer, ViewerID: v.ID, SDP: sdp})
}

// ForwardICE sends a viewer's ICE candidate to the host.
func (h *HostedSession) ForwardICE(v *Viewer, c proto.ICECandidate) error {
	return h.fromViewer(v, proto.ViewerICE{T: proto.HostICE, ViewerID: v.ID, Candidate: c})
}

// Tell sends the host a message of the server's own (a link_created, an
// error answering its request): ErrHostGone without a host, ErrSlowHost
// when its queue is full.
func (h *HostedSession) Tell(v any) error { return h.toHost(v) }

// StartRelay switches a viewer to relay mode and tells the host.
func (h *HostedSession) StartRelay(v *Viewer) error {
	v.relay.Store(true)
	return h.fromViewer(v, proto.ViewerRef{T: proto.HostRelayStart, ViewerID: v.ID})
}

// RelayToHost wraps a viewer frame in a RELAY envelope for the host. Input
// and resize from view-role viewers are dropped here as defence in depth.
// The envelope waits while the viewers' share of the host's queue is full
// (HostConn.sendFrom): ErrSlowHost when the host's connection closes
// meanwhile, ErrViewerGone when the viewer is closed, or its connection
// fails (ConnFailed) while the envelope waits; one that needs no wait goes
// though the connection has failed.
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
	if err := v.joined(); err != nil {
		return err
	}
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		return ErrHostGone
	}
	if err := v.chargeHost(len(env)); err != nil {
		return err
	}
	return conn.sendFrom(Outbound{Binary: env}, v.done, v.stop)
}

// fromViewer sends the host a message on v's behalf, charged as encoded
// (ViewerOptions.Charge), then queued (HostConn.sendFrom) as RelayToHost
// queues an envelope.
func (h *HostedSession) fromViewer(v *Viewer, msg any) error {
	b, err := jsonMarshal(msg)
	if err != nil {
		return err
	}
	if err := v.joined(); err != nil {
		return err
	}
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		return ErrHostGone
	}
	if err := v.chargeHost(len(b)); err != nil {
		return err
	}
	return conn.sendFrom(Outbound{Text: b}, v.done, v.stop)
}

// toHost sends the host a message of the server's own, without waiting
// (HostConn.send).
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

// AnswerFrame is the SIGNAL frame HostAnswer queues for a viewer, and
// ICEFrame the one HostICE does: the host's message encoded anew, which can
// be larger than the host sent it (json.Marshal writes a '<' as six bytes).
// A bound on what the host sends charges it before it is queued (HostSignal).
func AnswerFrame(sdp string) ([]byte, error) {
	return proto.EncodeJSON(proto.TypeSignal, proto.SDP{T: proto.SigAnswer, SDP: sdp})
}

// ICEFrame: see AnswerFrame.
func ICEFrame(c proto.ICECandidate) ([]byte, error) {
	return proto.EncodeJSON(proto.TypeSignal, proto.ICE{T: proto.SigICE, Candidate: c})
}

// HostSignal queues a SIGNAL frame AnswerFrame or ICEFrame made for a viewer.
func (h *HostedSession) HostSignal(viewerID string, f []byte) {
	if v := h.viewer(viewerID); v != nil {
		v.push(f)
	}
}

// HostAnswer delivers an SDP answer to a viewer.
func (h *HostedSession) HostAnswer(viewerID, sdp string) {
	if f, err := AnswerFrame(sdp); err == nil {
		h.HostSignal(viewerID, f)
	}
}

// HostICE delivers a host ICE candidate to a viewer.
func (h *HostedSession) HostICE(viewerID string, c proto.ICECandidate) {
	if f, err := ICEFrame(c); err == nil {
		h.HostSignal(viewerID, f)
	}
}

// HostRelayFrame delivers a relayed terminal frame to a viewer.
func (h *HostedSession) HostRelayFrame(viewerID string, inner proto.Frame) {
	if v := h.viewer(viewerID); v != nil {
		v.push(proto.Encode(inner.Type, inner.Payload))
	}
}

// HostViewerError forwards a per-viewer error and closes the viewer. The
// viewer is sent proto.NewError(code, message).
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
