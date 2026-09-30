package session

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	// FileDeny lists what no file read may reach, even inside the working
	// directory: a directory with everything in it, or a single file. The
	// server passes its data directory, whose catalog.json holds agent
	// secrets, its config file and its catalog file; `conductor host` has none.
	FileDeny []string
	// Transport is reported in welcome messages ("ws" on the server, "webrtc"/"relay" on hosts).
	Transport string
	Log       *slog.Logger
	// StopGrace is the SIGTERM grace period before SIGKILL.
	StopGrace time.Duration
	// OnChange is called (outside the session lock) after status, viewer
	// count or attention changes so listings and event streams stay current.
	OnChange func(Info)
	// OnActivity is called (outside the session lock) with the session ID and
	// the stored entry after Record has appended and broadcast it. It runs on
	// the recording goroutine, which may be the one reading the process, so it
	// must not block. Because it runs after the lock is released, calls for
	// different entries can overlap and arrive out of order: implementations
	// must be safe for concurrent use. An entry the event bucket drops never
	// reaches it; the attention entry of an attention change the session
	// applied always does.
	OnActivity func(sessionID string, e ActivityEntry)
	// Pattern, when set, is matched against the last line of the terminal
	// after patternQuiet without output. A match marks the session needs_input
	// (source "pattern", kind "prompt") unless it is that already. It is for
	// agents that neither run hooks nor ring the bell; a TUI that redraws
	// without pause never goes quiet and is not served by it.
	Pattern *regexp.Regexp
}

// patternQuiet is how long the output must stay silent before the last line is
// matched against Options.Pattern.
const patternQuiet = 500 * time.Millisecond

// Local owns a PTY process and serves attached clients. It is used by the
// server for server-hosted sessions and by `conductor host` for hosted ones.
type Local struct {
	opts Options
	proc Process
	ring *Ring
	hub  *Hub
	log  *slog.Logger

	mu   sync.Mutex // guards info, events and lastOutput, and orders ring writes against attaches
	info Info
	// stopRequested makes an exit observed by the pump report "stopped".
	stopRequested bool
	// lastOutput is when the pump last read output; zero until it has.
	lastOutput time.Time

	activity       activityRing
	events         EventBucket // guarded by mu
	dropped        atomic.Uint64
	scanner        Scanner
	pattern        *PatternWatcher // nil without Options.Pattern
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
	if opts.Pattern != nil {
		s.pattern = NewPatternWatcher(opts.Pattern, patternQuiet, s.firePattern)
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
			s.lastOutput = time.Now()
			s.mu.Unlock()
			s.scanOutput(chunk)
			if s.pattern != nil {
				s.pattern.Feed(chunk)
			}
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
	// A prompt left on the screen by a process that is gone must not raise
	// needs_input after the session has ended, where typing could not clear it.
	// Stop waits for a fire in flight, so it comes before the lock: that fire
	// takes it.
	if s.pattern != nil {
		s.pattern.Stop()
	}
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
	attrs := []any{"status", status}
	msg := string(status)
	if exitCode != nil {
		attrs = append(attrs, "exitCode", *exitCode)
		msg = fmt.Sprintf("%s (exit %d)", status, *exitCode)
	}
	s.log.Info("session ended", attrs...)
	s.Record(ActivityEntry{Type: ActivityStatus, Message: msg})
	s.notifyChange()
}

// Record appends an activity entry, broadcasts it to attached clients and
// passes it to OnActivity. What an agent reports (the six event types) is
// limited to EventRatePerSecond entries a second on average and EventBurst at
// once: beyond that Record does nothing else, counts the entry in Dropped and
// returns false. join, leave, input, link and status entries are the
// session's and the server's own; they skip the limit and spend no tokens, so
// a chatty hook cannot starve the roster rows or the final status row. The
// attention entry of an attention change the session applies skips it too:
// the session records that one itself (recordOwn). An attention entry handed
// to Record comes from outside the session and is limited like an event,
// although nothing records one that way today.
func (s *Local) Record(e ActivityEntry) bool {
	return s.record(e, bucketed(e.Type))
}

// recordOwn records an entry the session makes itself without asking the
// event bucket, whatever its type. It records the attention entry of an
// attention change the session has applied, which must not be dropped while
// the state it records is showing: a report from outside paid its token
// before the change (TrySetAttentionFull), and what the session observes
// itself (the bell, OSC notifications, the screen pattern) is held to its own
// pace and spends none.
func (s *Local) recordOwn(e ActivityEntry) {
	s.record(e, false)
}

// record is Record, with limited saying whether the entry spends a token of
// the event bucket.
func (s *Local) record(e ActivityEntry, limited bool) bool {
	s.mu.Lock()
	if limited && !s.events.Take(time.Now()) {
		log := s.log
		s.mu.Unlock()
		countDrop(&s.dropped, log, e.Type)
		return false
	}
	// The ring write and the broadcast share one critical section, so a client
	// attaching meanwhile finds the entry in its replay or receives the
	// broadcast, never both.
	e = s.activity.Add(e)
	s.hub.Broadcast(proto.MustControl(EntryToProto(e)))
	id := s.info.ID
	s.mu.Unlock()
	if s.opts.OnActivity != nil {
		s.opts.OnActivity(id, e)
	}
	return true
}

// countDrop counts in dropped something of type typ that the event bucket
// refused, an entry or a report, and logs the first and every 100th at debug
// level, so that a flood does not become a log flood.
func countDrop(dropped *atomic.Uint64, log *slog.Logger, typ string) {
	if n := dropped.Add(1); n == 1 || n%100 == 0 {
		log.Debug("activity dropped by the event rate limit", "type", typ, "dropped", n)
	}
}

// Dropped counts what the session's event bucket refused: the entries Record
// did not record and the reports TrySetAttentionFull did not apply.
func (s *Local) Dropped() uint64 { return s.dropped.Load() }

// Activity returns the activity log, oldest first.
func (s *Local) Activity() []ActivityEntry { return s.activity.Snapshot() }

// notifyChange hands a fresh Info snapshot to the OnChange hook.
func (s *Local) notifyChange() {
	if s.opts.OnChange != nil {
		s.opts.OnChange(s.Info())
	}
}

// scanOutput looks for bell/OSC signals in a chunk of terminal output. A
// burst of bells produces at most one change per 500 ms. They are the
// session's own observations and spend no token of the event bucket.
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

// firePattern is the pattern watcher's fire: the last line matched after the
// output went quiet. A session that is waiting already keeps what marked it so
// (a hook's message, kind and options, the bell), and the same prompt does not
// report itself again while it stays on the screen. That is decided under the
// lock that sets the state: a report that lands between a look at the state and
// the set would be overwritten.
func (s *Local) firePattern(line string) {
	s.setAttention(AttentionNeedsInput, promptMessage(line), SourcePattern, KindPrompt, nil, unlessWaiting)
}

// promptMessage is the attention message for the prompt on a line. A message
// is cut at MaxAttentionMessage bytes from its start, and it is the end of a
// long line that says what the agent waits for, so the line is trimmed at its
// start instead, on a character boundary.
func promptMessage(line string) string {
	const prefix = "prompt: "
	if room := MaxAttentionMessage - len(prefix); len(line) > room {
		line = line[len(line)-room:]
		// The cut may have landed inside a character (so may the tracker's, on
		// a line longer than it keeps): the next character starts at most 3
		// bytes on.
		for i := 0; i < utf8.UTFMax-1 && len(line) > 0 && !utf8.RuneStart(line[0]); i++ {
			line = line[1:]
		}
	}
	return prefix + line
}

// SetAttention records an attention change without prompt details. See
// SetAttentionFull.
func (s *Local) SetAttention(state AttentionState, message, source string) {
	s.SetAttentionFull(state, message, source, "", nil)
}

// SetAttentionFull records an attention change, broadcasts it to attached
// clients and notifies OnChange. AttentionNone clears the signal along with
// any kind and options. Unknown kinds are dropped rather than rejected. It
// asks the event bucket for nothing, and the attention entry of the change is
// always recorded: it is for what the session observes itself and for a
// report that has paid already, such as one the server sends on to a hosted
// session's host once it has spent the token it keeps for that host. A report
// from outside the session goes through TrySetAttentionFull.
func (s *Local) SetAttentionFull(state AttentionState, message, source, kind string, options []Option) {
	s.setAttention(state, message, source, kind, options, 0)
}

// TrySetAttentionFull is SetAttentionFull for a report from outside the
// session, an agent's or an admin's through the API. The report spends a
// token of the session's event bucket, the one Record spends on the events an
// agent reports, before it changes anything and whether or not it changes
// anything; the attention entry of the change it applies spends no second
// one. With no token left it returns ErrRateLimited having changed nothing,
// and the refusal counts in Dropped. The server spends a token of a bucket of
// the same size before it sends a report on to a hosted session's host, so a
// report is limited alike on both kinds of session.
func (s *Local) TrySetAttentionFull(state AttentionState, message, source, kind string, options []Option) error {
	return s.setAttention(state, message, source, kind, options, spendToken)
}

// attentionRules say how setAttention applies a change.
type attentionRules uint8

const (
	// unlessWaiting changes nothing when the session is needs_input already.
	unlessWaiting attentionRules = 1 << iota
	// spendToken spends a token of the event bucket before anything changes,
	// and refuses the change with ErrRateLimited when there is none.
	spendToken
)

// setAttention is SetAttentionFull, applied as rules say. The token, the look
// at the current state and the change share one critical section. The
// attention entry of an applied change is recorded without asking the bucket
// (recordOwn), stamped with the change's Since, taken in that section: whoever
// sees the new state sees it no earlier than the entry's At, however late the
// entry reaches OnActivity. An invalid state changes nothing and spends
// nothing.
func (s *Local) setAttention(state AttentionState, message, source, kind string, options []Option, rules attentionRules) error {
	if !state.Valid() {
		return nil
	}
	message = CleanMessage(message)
	if !ValidKind(kind) {
		kind = ""
	}
	options = CleanOptions(options)
	if state == AttentionNone {
		kind, options = "", nil
	}
	s.mu.Lock()
	if rules&spendToken != 0 && !s.events.Take(time.Now()) {
		log := s.log
		s.mu.Unlock()
		countDrop(&s.dropped, log, ActivityAttention)
		return ErrRateLimited
	}
	cur := s.info.Attention
	if rules&unlessWaiting != 0 && cur.State == AttentionNeedsInput {
		s.mu.Unlock()
		return nil
	}
	if cur.State == state && cur.Message == message && cur.Source == source && cur.Kind == kind && sameOptions(cur.Options, options) {
		s.mu.Unlock()
		return nil
	}
	att := Attention{State: state, Message: message, Source: source, Kind: kind, Options: options}
	if state != AttentionNone {
		now := time.Now().UTC()
		att.Since = &now
	}
	s.info.Attention = att
	s.hub.Broadcast(proto.MustControl(attentionMessage(att)))
	s.mu.Unlock()
	if state != AttentionNone {
		label := message
		if label == "" {
			label = string(state)
		}
		s.recordOwn(ActivityEntry{At: *att.Since, Type: ActivityAttention, Message: label})
	}
	s.notifyChange()
	return nil
}

func sameOptions(a, b []Option) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// attentionMessage encodes an Attention as the wire control message.
func attentionMessage(att Attention) proto.Attention {
	m := proto.Attention{T: proto.CtlAttention, State: string(att.State), Message: att.Message, Source: att.Source, Kind: att.Kind}
	for _, o := range att.Options {
		m.Options = append(m.Options, proto.AttentionOption{Label: o.Label, Input: o.Input})
	}
	return m
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

// LastOutputAt is when the process last wrote output, zero until it has. A
// crew run reads it to tell when a member's terminal has gone quiet.
func (s *Local) LastOutputAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastOutput
}

// AttachOptions describe a client joining a session.
type AttachOptions struct {
	ID        string // empty: generated
	Role      Role
	LinkID    string
	LinkLabel string
	Name      string // cleaned with CleanName
	Cols      uint16
	Rows      uint16
}

// Attach registers a client without a display name. See AttachWith.
func (s *Local) Attach(id string, role Role, linkID string, cols, rows uint16, sink Sink) (*Subscription, error) {
	return s.AttachWith(AttachOptions{ID: id, Role: role, LinkID: linkID, Cols: cols, Rows: rows}, sink)
}

// viewersFrame encodes the current roster. Callers hold s.mu.
func (s *Local) viewersFrame() []byte {
	roster := s.hub.Roster()
	return proto.MustControl(proto.Viewers{T: proto.CtlViewers, Count: len(roster), List: roster})
}

// AttachWith registers a client. The welcome, scrollback replay and ready
// marker are queued before any live output so the client sees a consistent
// stream; the roster is then broadcast to everyone.
func (s *Local) AttachWith(o AttachOptions, sink Sink) (*Subscription, error) {
	role, id, cols, rows := o.Role, o.ID, o.Cols, o.Rows
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
	sub := newSubscription(id, role, o.LinkID, sink)
	sub.Name = CleanName(o.Name)
	sub.LinkLabel = o.LinkLabel
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
	for _, e := range s.activity.Tail(ActivityReplay) {
		sub.send(proto.MustControl(EntryToProto(e)))
	}
	s.hub.add(sub)
	s.hub.Broadcast(s.viewersFrame())
	if s.info.Attention.State != AttentionNone {
		sub.send(proto.MustControl(attentionMessage(s.info.Attention)))
	}
	s.mu.Unlock()
	go func() {
		<-sub.done
		s.Detach(sub)
	}()
	s.Record(ActivityEntry{Type: ActivityJoin, By: sub.ID, ByName: sub.Name})
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
		s.hub.Broadcast(s.viewersFrame())
	}
	s.mu.Unlock()
	if present {
		s.Record(ActivityEntry{Type: ActivityLeave, By: sub.ID, ByName: sub.Name})
		s.notifyChange()
	}
}

// Input forwards keystrokes from a controller.
func (s *Local) Input(sub *Subscription, data []byte) error {
	if sub.Role != RoleControl {
		return ErrReadOnly
	}
	_, err := s.write(data, sub, "", false)
	return err
}

// ErrTextTooLong refuses text to type longer than an INPUT frame may carry,
// proto.MaxInput bytes: nothing was written.
var ErrTextTooLong = fmt.Errorf("session: text to type is longer than %d bytes", proto.MaxInput)

// Type writes text to the process as a controller typing it would, for what
// Conductor types itself: a crew member's prompt. No subscription is behind
// it. Like Input it answers the needs_input prompt that was showing when it
// began, recording lastAnswer by byName, which is cleaned as a display name.
// Unlike Input it always records an input entry, by byName and with the text,
// less its trailing line break, as the message: what Conductor types is its
// own, where a person's keystrokes are recorded only as the prompt they
// answered. Text longer than proto.MaxInput bytes, its line break included, is
// refused with ErrTextTooLong. It returns ErrSessionEnded once the session has
// ended.
func (s *Local) Type(text, byName string) error {
	if len(text) > proto.MaxInput {
		return ErrTextTooLong
	}
	_, err := s.write([]byte(text), nil, CleanName(byName), false)
	return err
}

// TypeUnlessWaiting is Type for what must never answer a prompt, such as a
// handoff between crew members: when the session is needs_input it writes,
// records and changes nothing and returns typed false. The look at the state
// is the one that finds the prompt the text would answer, so a prompt raised
// after it, while the text is written, is not answered by the text either.
func (s *Local) TypeUnlessWaiting(text, byName string) (typed bool, err error) {
	if len(text) > proto.MaxInput {
		return false, ErrTextTooLong
	}
	return s.write([]byte(text), nil, CleanName(byName), true)
}

// write writes data to the process, for sub, a controller's subscription, or,
// with sub nil, for Type as byName. It answers the needs_input prompt that was
// showing when it began, and records an input entry when it does, or always
// for Type. With skipWhileWaiting it writes nothing while that prompt shows,
// and reports whether it wrote.
func (s *Local) write(data []byte, sub *Subscription, byName string, skipWhileWaiting bool) (bool, error) {
	s.mu.Lock()
	ended := s.info.Status.Ended()
	// The prompt this input answers is the one on the screen as it is typed.
	// A process that reacts before Write returns (an echo that rings the bell)
	// may raise the next one meanwhile, and typing must not clear that one.
	// Each attention change gets a new Since, which tells the prompts apart.
	promptSince := s.info.Attention.Since
	showing := s.info.Attention.State == AttentionNeedsInput
	s.mu.Unlock()
	if ended {
		return false, ErrSessionEnded
	}
	if skipWhileWaiting && showing {
		return false, nil
	}
	if _, err := s.proc.Write(data); err != nil {
		return false, err
	}
	by := ""
	if sub != nil {
		by, byName = sub.ID, sub.Name
		// Presence: stamp the typist and refresh the roster at most every 2 s.
		now := time.Now().UnixMilli()
		if prev := sub.lastInput.Swap(now); now-prev > 2000 {
			s.mu.Lock()
			s.hub.Broadcast(s.viewersFrame())
			s.mu.Unlock()
		}
	}
	// First reply wins: the check, the answer record and the clear all happen
	// in one critical section so two concurrent typists cannot both claim the
	// prompt.
	s.mu.Lock()
	waiting := s.info.Attention.State == AttentionNeedsInput && s.info.Attention.Since == promptSince
	question := s.info.Attention.Message
	if waiting {
		s.info.LastAnswer = &Answer{By: by, ByName: byName, At: time.Now().UTC(), Message: question}
		s.info.Attention = Attention{State: AttentionNone, Source: SourceInput}
		s.hub.Broadcast(proto.MustControl(attentionMessage(s.info.Attention)))
	}
	s.mu.Unlock()
	switch {
	case sub == nil:
		s.Record(ActivityEntry{Type: ActivityInput, ByName: byName, Message: strings.TrimRight(string(data), "\r\n")})
	case waiting:
		s.Record(ActivityEntry{Type: ActivityInput, By: by, ByName: byName, Message: question})
	}
	if waiting {
		s.notifyChange()
	}
	return true, nil
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

// Drained reports whether every attached client has been handed all the frames
// the session sent it, the final status among them once the session has ended.
func (s *Local) Drained() bool { return s.hub.Drained() }

// LinkViewers counts attached clients per share link id.
func (s *Local) LinkViewers() map[string]int { return s.hub.CountByLink() }
