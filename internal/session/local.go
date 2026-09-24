package session

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
)

// Process is what Local needs from a PTY-backed process.
type Process interface {
	io.Reader
	io.Writer
	Resize(cols, rows uint16) error
	Done() <-chan struct{}
	Exit() pty.ExitStatus
	Stop(ctx context.Context, grace time.Duration) error
}

// Options tune a Local session.
type Options struct {
	ScrollbackBytes int
	MaxViewers      int
	// FileView decides which roles may read files: "view" (both), "control", "off".
	FileView string
	// Transport is reported in welcome messages ("ws" on the server, "webrtc"/"relay" on hosts).
	Transport string
	Log       *slog.Logger
	// StopGrace is the SIGTERM grace period before SIGKILL.
	StopGrace time.Duration
	// OnChange is called (outside the session lock) after status, viewer
	// count or attention changes so listings and event streams stay current.
	OnChange func(Info)
}

// Local owns a PTY process and serves attached clients. It is used by the
// server for server-hosted sessions and by `conductor host` for hosted ones.
type Local struct {
	opts Options
	proc Process
	ring *Ring
	hub  *Hub
	log  *slog.Logger

	mu   sync.Mutex // guards info and orders ring writes against attaches
	info Info
	// stopRequested makes an exit observed by the pump report "stopped".
	stopRequested bool

	scanner        Scanner
	lastBell       time.Time
	agentTokenHash [32]byte
	hasAgentToken  bool

	ended chan struct{}
}

// NewLocal wraps a started process and begins pumping its output.
func NewLocal(info Info, proc Process, opts Options) *Local {
	if opts.ScrollbackBytes <= 0 {
		opts.ScrollbackBytes = 256 << 10
	}
	if opts.MaxViewers <= 0 {
		opts.MaxViewers = 32
	}
	if opts.FileView == "" {
		opts.FileView = "view"
	}
	if opts.Transport == "" {
		opts.Transport = proto.TransportWS
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.StopGrace <= 0 {
		opts.StopGrace = 5 * time.Second
	}
	if info.Status == "" {
		info.Status = StatusRunning
	}
	if info.CreatedAt.IsZero() {
		info.CreatedAt = time.Now().UTC()
	}
	s := &Local{
		opts:  opts,
		proc:  proc,
		ring:  NewRing(opts.ScrollbackBytes),
		hub:   NewHub(),
		log:   opts.Log.With("session", info.ID),
		info:  info,
		ended: make(chan struct{}),
	}
	go s.pump()
	return s
}

func (s *Local) pump() {
	buf := make([]byte, proto.MaxOutput)
	for {
		n, err := s.proc.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.mu.Lock()
			s.ring.Write(chunk)
			s.hub.Broadcast(proto.Encode(proto.TypeOutput, chunk))
			s.mu.Unlock()
			s.scanOutput(chunk)
		}
		if err != nil {
			break
		}
	}
	// The read side closes when the child exits; wait briefly for the status.
	select {
	case <-s.proc.Done():
	case <-time.After(5 * time.Second):
	}
	s.mu.Lock()
	status := StatusExited
	if s.stopRequested {
		status = StatusStopped
	}
	s.mu.Unlock()
	s.markEnded(status)
}

func (s *Local) markEnded(status Status) {
	s.mu.Lock()
	if s.info.Status.Ended() {
		s.mu.Unlock()
		return
	}
	s.info.Status = status
	now := time.Now().UTC()
	s.info.EndedAt = &now
	var exitCode *int
	select {
	case <-s.proc.Done():
		st := s.proc.Exit()
		code := st.Code
		exitCode = &code
		s.info.ExitCode = exitCode
	default:
	}
	frame := proto.MustControl(proto.Status{T: proto.CtlStatus, Status: string(status), ExitCode: exitCode})
	s.hub.Broadcast(frame)
	s.mu.Unlock()
	close(s.ended)
	s.log.Info("session ended", "status", status, "exitCode", exitCode)
	s.notifyChange()
}

// notifyChange hands a fresh Info snapshot to the OnChange hook.
func (s *Local) notifyChange() {
	if s.opts.OnChange != nil {
		s.opts.OnChange(s.Info())
	}
}

// scanOutput looks for bell/OSC signals in a chunk of terminal output. A
// burst of bells produces at most one change per 500 ms.
func (s *Local) scanOutput(chunk []byte) {
	for _, ev := range s.scanner.Scan(chunk) {
		s.mu.Lock()
		if time.Since(s.lastBell) < 500*time.Millisecond {
			s.mu.Unlock()
			continue
		}
		s.lastBell = time.Now()
		s.mu.Unlock()
		msg := ev.Message
		if msg == "" {
			msg = "terminal bell"
		}
		s.SetAttention(AttentionNeedsInput, msg, ev.Source)
	}
}

// SetAttention records an attention change, broadcasts it to attached
// clients and notifies OnChange. AttentionNone clears the signal.
func (s *Local) SetAttention(state AttentionState, message, source string) {
	if !state.Valid() {
		return
	}
	message = CleanMessage(message)
	s.mu.Lock()
	cur := s.info.Attention
	if cur.State == state && cur.Message == message && cur.Source == source {
		s.mu.Unlock()
		return
	}
	att := Attention{State: state, Message: message, Source: source}
	if state != AttentionNone {
		now := time.Now().UTC()
		att.Since = &now
	}
	s.info.Attention = att
	s.hub.Broadcast(proto.MustControl(proto.Attention{T: proto.CtlAttention, State: string(state), Message: message, Source: source}))
	s.mu.Unlock()
	s.notifyChange()
}

// SetAgentToken records the per-session token agents use to report attention.
func (s *Local) SetAgentToken(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentTokenHash = sha256.Sum256([]byte(tok))
	s.hasAgentToken = tok != ""
}

// AgentTokenOK compares a presented agent token in constant time.
func (s *Local) AgentTokenOK(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasAgentToken || tok == "" {
		return false
	}
	h := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(h[:], s.agentTokenHash[:]) == 1
}

// SetID assigns the session ID once it is known (hosts learn it from the
// server after registration). It only applies while the ID is empty.
func (s *Local) SetID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.ID == "" {
		s.info.ID = id
		s.log = s.log.With("session", id)
	}
}

// Ended is closed once the process has exited or been stopped.
func (s *Local) Ended() <-chan struct{} { return s.ended }

// Info returns a snapshot of the session description.
func (s *Local) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := s.info
	info.Viewers = s.hub.Count()
	return info
}

// Attach registers a client. The welcome, scrollback replay and ready marker
// are queued before any live output so the client sees a consistent stream.
func (s *Local) Attach(id string, role Role, linkID string, cols, rows uint16, sink Sink) (*Subscription, error) {
	if !role.Valid() {
		return nil, errors.New("session: invalid role")
	}
	if id == "" {
		id = NewID()
	}
	s.mu.Lock()
	if s.hub.Count() >= s.opts.MaxViewers {
		s.mu.Unlock()
		return nil, ErrTooManyViewers
	}
	if role == RoleControl && proto.ValidDimension(cols) && proto.ValidDimension(rows) && !s.info.Status.Ended() {
		if cols != s.info.Cols || rows != s.info.Rows {
			if err := s.proc.Resize(cols, rows); err == nil {
				s.info.Cols, s.info.Rows = cols, rows
				s.hub.Broadcast(proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: cols, Rows: rows, By: id}))
			}
		}
	}
	transport := s.opts.Transport
	if named, ok := sink.(interface{ Transport() string }); ok {
		transport = named.Transport()
	}
	sub := newSubscription(id, role, linkID, sink)
	sub.send(proto.MustControl(proto.Welcome{
		T:               proto.CtlWelcome,
		Proto:           proto.ProtoVersion,
		SessionID:       s.info.ID,
		Role:            string(role),
		SubscriberID:    id,
		Cols:            s.info.Cols,
		Rows:            s.info.Rows,
		Status:          string(s.info.Status),
		ScrollbackBytes: s.ring.Cap(),
		Transport:       transport,
		FileView:        s.fileAllowed(role),
	}))
	snap := s.ring.Snapshot()
	for len(snap) > 0 {
		n := min(len(snap), proto.MaxOutput)
		sub.send(proto.Encode(proto.TypeScrollback, snap[:n]))
		snap = snap[n:]
	}
	sub.send(proto.MustControl(proto.Simple{T: proto.CtlReady}))
	s.hub.add(sub)
	count := s.hub.Count()
	s.hub.Broadcast(proto.MustControl(proto.Viewers{T: proto.CtlViewers, Count: count}))
	if s.info.Attention.State != AttentionNone {
		att := s.info.Attention
		sub.send(proto.MustControl(proto.Attention{T: proto.CtlAttention, State: string(att.State), Message: att.Message, Source: att.Source}))
	}
	s.mu.Unlock()
	go func() {
		<-sub.done
		s.Detach(sub)
	}()
	s.notifyChange()
	return sub, nil
}

// Detach removes a client. It is safe to call more than once.
func (s *Local) Detach(sub *Subscription) {
	s.mu.Lock()
	_, present := s.hub.subs[sub.ID]
	s.hub.remove(sub.ID)
	sub.closeWith(nil)
	if present {
		s.hub.Broadcast(proto.MustControl(proto.Viewers{T: proto.CtlViewers, Count: s.hub.Count()}))
	}
	s.mu.Unlock()
	if present {
		s.notifyChange()
	}
}

// Input forwards keystrokes from a controller.
func (s *Local) Input(sub *Subscription, data []byte) error {
	if sub.Role != RoleControl {
		return ErrReadOnly
	}
	s.mu.Lock()
	ended := s.info.Status.Ended()
	s.mu.Unlock()
	if ended {
		return ErrSessionEnded
	}
	_, err := s.proc.Write(data)
	if err == nil {
		s.mu.Lock()
		waiting := s.info.Attention.State == AttentionNeedsInput
		s.mu.Unlock()
		if waiting {
			s.SetAttention(AttentionNone, "", SourceInput)
		}
	}
	return err
}

// Resize applies the latest-controller-wins policy and broadcasts the result.
func (s *Local) Resize(sub *Subscription, cols, rows uint16) error {
	if sub.Role != RoleControl {
		return ErrReadOnly
	}
	if !proto.ValidDimension(cols) || !proto.ValidDimension(rows) {
		return ErrBadDimension
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.Status.Ended() {
		return ErrSessionEnded
	}
	if cols == s.info.Cols && rows == s.info.Rows {
		return nil
	}
	if err := s.proc.Resize(cols, rows); err != nil {
		return err
	}
	s.info.Cols, s.info.Rows = cols, rows
	s.hub.Broadcast(proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: cols, Rows: rows, By: sub.ID}))
	return nil
}

// Stop terminates the process.
func (s *Local) Stop(ctx context.Context) error {
	s.mu.Lock()
	ended := s.info.Status.Ended()
	s.stopRequested = true
	s.mu.Unlock()
	if ended {
		return nil
	}
	err := s.proc.Stop(ctx, s.opts.StopGrace)
	s.markEnded(StatusStopped)
	return err
}

// DisconnectLink evicts every subscription created through linkID.
func (s *Local) DisconnectLink(linkID string) {
	s.hub.Each(func(sub *Subscription) {
		if sub.LinkID == linkID {
			sub.closeWith(ErrRevoked)
		}
	})
}

// CloseAll evicts every subscription with the given reason (server shutdown).
func (s *Local) CloseAll(reason error) {
	s.hub.Each(func(sub *Subscription) { sub.closeWith(reason) })
}

// Send queues an arbitrary frame to one subscription (used for file responses).
func (s *Local) Send(sub *Subscription, frame []byte) { sub.send(frame) }

// Viewers returns the number of attached clients.
func (s *Local) Viewers() int { return s.hub.Count() }
