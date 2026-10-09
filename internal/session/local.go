package session

import (
	"bytes"
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
	// FileEdit decides whether a controller may edit files through the
	// editor's Neovim (design round 12, F8): "control" (the default), "off".
	FileEdit string
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
	// OnActivity is called (outside the session lock) with the session ID,
	// the stored entry and, for the attention entry of a change the session
	// applied, the attention state it records: the state set in the same
	// critical section as the entry's stamp ("" for every other entry). It
	// runs on the recording goroutine, which may be the one reading the
	// process, so it must not block. Because it runs after the lock is
	// released, calls for different entries can overlap and arrive out of
	// order: implementations must be safe for concurrent use, and must take
	// the state from here, never from Info, which may have moved on. An entry
	// the event bucket drops never reaches it; the attention entry of an
	// attention change the session applied always does.
	OnActivity func(sessionID string, e ActivityEntry, state AttentionState)
	// OnChat is called (outside the session lock) with the session ID and a
	// chat message as kept: a post, a join or leave line, a sent-to-agent
	// marker. The same contract as OnActivity: it runs on the posting
	// goroutine and must not block; calls can overlap.
	OnChat func(sessionID string, m ChatMessage)
	// RunChat, when set, is the chat of the run this session is a member of
	// (ChatRoom): its viewers read and post it over their own connection,
	// with scope "run", and are listed on its roster.
	RunChat *ChatRoom
	// Pattern, when set, is matched against the last line of the terminal
	// after patternQuiet without output. A match marks the session needs_input
	// (source "pattern", kind "prompt") unless it is that already. It is for
	// agents that neither run hooks nor ring the bell; a TUI that redraws
	// without pause never goes quiet and is not served by it.
	Pattern *regexp.Regexp
	// TrustPattern, when set, matches the agent's workspace-trust question in
	// the text of its screen, each escape sequence read as a space
	// (ScreenTail), after patternQuiet without output other than title
	// updates. A match marks the session needs_input (source "trust", kind
	// "prompt", the matched words as the message) unless it waits already,
	// until the first submission's Enter: the run engine types no prompt
	// while it shows, and a person answers the question.
	TrustPattern *regexp.Regexp
	// TrustAnswers are the question's answers as its quick-reply choices (the
	// catalog's trustAnswers): a label and the keys that pick it, cleaned and
	// bounded as any prompt's choices are (CleanOptions). A person's typed
	// text (Submit) is refused while the question shows (ErrTrustQuestion),
	// since its Enter would pick whatever the dialog highlights.
	TrustAnswers []Option
	// Questions are the agent's other startup questions (the catalog's
	// questions; round 13, G2c), held exactly as the trust question is:
	// source "trust", their answers the choices, typed text refused while
	// one shows. Codex's "Hooks need review" is one.
	Questions []StartupQuestion
	// SubmitPause is the pause between a submission's text and its Enter;
	// SubmitPause when zero.
	SubmitPause time.Duration
	// ConfirmSubmit says the agent reports taking a prompt (Claude Code's
	// UserPromptSubmit hook reports working): a submission that asks for it
	// (Submission.Confirm) and is not taken within ConfirmWait gets one more
	// Enter.
	ConfirmSubmit bool
	// ConfirmWait is that wait; ConfirmWait when zero.
	ConfirmWait time.Duration
	// WatchGit records what the agent changed in a git working directory
	// without a hook naming the file (files seen by git, gitseen.go): the
	// server's and the host's agent sessions set it.
	WatchGit bool
	// Launched is what the session was launched with, for Resume.
	Launched Launched
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

	activity activityRing
	// touched is the Touched section's index of the files the agent touched (touched.go), under mu.
	touched touchedIndex
	// gitSeen watches the working tree for files no hook named (gitseen.go); its own lock.
	gitSeen gitSeen
	chat    chatRing // guarded by mu
	// question is the id of the agent's question in the chat while one
	// stands (askInChat), for the line that says it was answered.
	question       string
	events         EventBucket // guarded by mu
	dropped        atomic.Uint64
	scanner        Scanner
	pattern        *PatternWatcher   // nil without Options.Pattern
	trust          *PatternWatcher   // nil without a startup question
	questions      []StartupQuestion // Options.TrustPattern's, then Options.Questions
	lastBell       time.Time
	agentTokenHash [32]byte
	hasAgentToken  bool

	ended chan struct{}

	// submitting holds a token while a submission runs (Submit): one at a
	// time per session.
	submitting chan struct{}
	// paste follows the program's bracketed-paste mode (Submit, BracketedPaste).
	paste pasteMode
	// attnChanged is closed and replaced, under mu, on every attention change:
	// Submit waits on it for the agent to take a prompt.
	attnChanged chan struct{}
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
	if opts.SubmitPause <= 0 {
		opts.SubmitPause = SubmitPause
	}
	if opts.ConfirmWait <= 0 {
		opts.ConfirmWait = ConfirmWait
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

		submitting:  make(chan struct{}, 1),
		attnChanged: make(chan struct{}),
	}
	if opts.Pattern != nil {
		s.pattern = NewPatternWatcher(opts.Pattern, patternQuiet, s.firePattern)
	}
	if opts.TrustPattern != nil {
		s.questions = append(s.questions, StartupQuestion{Pattern: opts.TrustPattern, Answers: opts.TrustAnswers})
	}
	for _, q := range opts.Questions {
		if q.Pattern != nil {
			s.questions = append(s.questions, q)
		}
	}
	if len(s.questions) > 0 {
		// One watcher for every question: its pattern is theirs together, and
		// the fire tells which one shows.
		parts := make([]string, len(s.questions))
		for i, q := range s.questions {
			parts[i] = "(?:" + q.Pattern.String() + ")"
		}
		s.trust = NewScreenWatcher(regexp.MustCompile(strings.Join(parts, "|")), patternQuiet, s.fireTrust)
	}
	if opts.WatchGit && info.Cwd != "" {
		s.gitSeenStart()
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
			// Output that only sets the window title (a spinner in it) does
			// not count as output for the quiet the run engine and the
			// trust watcher wait for.
			title := titleOnly.Match(chunk)
			s.mu.Lock()
			s.ring.Write(chunk)
			s.hub.BroadcastLoud(proto.Encode(proto.TypeOutput, chunk))
			if !title {
				s.lastOutput = time.Now()
			}
			s.mu.Unlock()
			s.paste.feed(chunk)
			s.scanOutput(chunk)
			if s.pattern != nil {
				s.pattern.Feed(chunk)
			}
			if s.trust != nil && !title {
				s.trust.Feed(chunk)
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
	if s.trust != nil {
		s.trust.Stop()
	}
	s.mu.Lock()
	if s.info.Status.Ended() {
		s.mu.Unlock()
		return
	}
	s.info.Status = status
	// An ended session needs nothing: what it waited on goes with its
	// process, in the critical section that ends it, so nothing ever sees an
	// ended session that still needs input. Its history stays in the activity
	// log.
	cleared := s.info.Attention.State != AttentionNone
	if cleared {
		s.info.Attention = Attention{}
		s.signalAttention()
	}
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
	if cleared {
		s.hub.Broadcast(proto.MustControl(attentionMessage(s.info.Attention)))
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
	return s.record(e, bucketed(e.Type), "")
}

// recordOwn records an entry the session makes itself without asking the
// event bucket, whatever its type, and hands OnActivity the attention state
// it records. It records the attention entry of an attention change the
// session has applied, which must not be dropped while the state it records
// is showing: a report from outside paid its token before the change
// (TrySetAttentionFull), and what the session observes itself (the bell, OSC
// notifications, the screen pattern) is held to its own pace and spends none.
func (s *Local) recordOwn(e ActivityEntry, state AttentionState) {
	s.record(e, false, state)
}

// record is Record, with limited saying whether the entry spends a token of
// the event bucket, and state what OnActivity is told the entry records.
func (s *Local) record(e ActivityEntry, limited bool, state AttentionState) bool {
	s.mu.Lock()
	if limited && !s.events.Take(time.Now()) {
		log := s.log
		s.mu.Unlock()
		countDrop(&s.dropped, log, e.Type)
		return false
	}
	// A file event repeating the newest one (the same path and op within
	// FileCoalesce: an agent saving a file forty times) is one entry: the
	// newest's time moves, nothing is appended or broadcast again.
	if e.Type == ActivityFile && s.activity.CoalesceFile(CleanEntry(e)) {
		s.noteTouched(CleanEntry(e), true)
		s.mu.Unlock()
		s.gitSeenReported(e)
		return true
	}
	// The ring write and the broadcast share one critical section, so a client
	// attaching meanwhile finds the entry in its replay or receives the
	// broadcast, never both.
	e = s.activity.Add(e)
	if e.Type == ActivityFile {
		s.noteTouched(e, false)
	}
	s.hub.Broadcast(proto.MustControl(EntryToProto(e)))
	id := s.info.ID
	s.mu.Unlock()
	switch e.Type {
	case ActivityFile:
		s.gitSeenReported(e)
	case ActivityToolUse, ActivityAttention:
		// A tool ran, or a turn ended or asked: a look at the tree a moment later.
		s.gitSeenPoke()
	}
	if s.opts.OnActivity != nil {
		s.opts.OnActivity(id, e, state)
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

// fireTrust is the trust watcher's fire: one of the agent's startup
// questions (the workspace-trust question first, then Options.Questions) is
// on the screen, text its last ScreenTail. The session needs input, with
// the words of the first question found and its answers, unless it waits
// already.
func (s *Local) fireTrust(text string) {
	for _, q := range s.questions {
		if words := q.Pattern.FindString(text); words != "" {
			s.setAttention(AttentionNeedsInput, words, SourceTrust, KindPrompt, CleanOptions(q.Answers), unlessWaiting)
			return
		}
	}
}

// StartupQuestion is one question an agent asks as it starts (Options.Questions):
// the pattern of its words on the screen and its answers as choices.
type StartupQuestion struct {
	Pattern *regexp.Regexp
	Answers []Option
}

// signalAttention wakes whoever waits for an attention change (Submit). The
// caller holds s.mu.
func (s *Local) signalAttention() {
	close(s.attnChanged)
	s.attnChanged = make(chan struct{})
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
	s.signalAttention()
	s.hub.Broadcast(proto.MustControl(attentionMessage(att)))
	// The agent's question goes into the chat too (design: the agents' questions in the chat).
	var asked ChatMessage
	var askedID string
	if state == AttentionNeedsInput {
		asked, askedID = s.askInChat(att)
	}
	s.mu.Unlock()
	if askedID != "" {
		s.chatHook(askedID, asked)
		s.tellRun(asked)
	}
	if state != AttentionNone {
		label := message
		if label == "" {
			label = string(state)
		}
		s.recordOwn(ActivityEntry{At: *att.Since, Type: ActivityAttention, Message: label}, state)
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
	info.Viewers = s.hub.LoudCount()
	if info.AgentSession != nil {
		as := *info.AgentSession
		info.AgentSession = &as
	}
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
	// ChatOnly makes the connection quiet (hello.chatOnly): for a run's
	// chat alone, with no scrollback or output, uncounted among the viewers.
	ChatOnly bool
}

// Attach registers a client without a display name. See AttachWith.
func (s *Local) Attach(id string, role Role, linkID string, cols, rows uint16, sink Sink) (*Subscription, error) {
	return s.AttachWith(AttachOptions{ID: id, Role: role, LinkID: linkID, Cols: cols, Rows: rows}, sink)
}

// viewersFrame encodes the current roster: the session's viewers, a quiet
// connection for the run's chat left out. Callers hold s.mu.
func (s *Local) viewersFrame() []byte {
	roster := make([]proto.ViewerInfo, 0)
	for _, v := range s.hub.Roster() {
		if !v.Quiet {
			roster = append(roster, v)
		}
	}
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
	// The hello's size: a controller's sets the PTY's, (0, 0) follows it
	// (proto.HelloSize). The role comes from the token, never from the hello.
	if role == RoleControl && proto.HelloSize(cols, rows) && !s.info.Status.Ended() {
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
	sub.quiet = o.ChatOnly
	room := s.opts.RunChat
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
		FileEdit:        s.editAllowed(role),
		Nvim:            nvimAvailable(),
		Chat:            true,
		RunChat:         room != nil,
	}))
	if !sub.quiet {
		snap := s.ring.Snapshot()
		for len(snap) > 0 {
			n := min(len(snap), proto.MaxOutput)
			sub.send(proto.Encode(proto.TypeScrollback, snap[:n]))
			snap = snap[n:]
		}
	}
	sub.send(proto.MustControl(proto.Simple{T: proto.CtlReady}))
	for _, e := range s.activity.Tail(ActivityReplay) {
		sub.send(proto.MustControl(EntryToProto(e)))
	}
	// The kept chat, after the activity replay and before the hub holds the
	// viewer: a message posted meanwhile reaches it live, never twice. The
	// run's chat follows the session's; a run message posted meanwhile may
	// arrive twice (the room posts without this lock), which clients take
	// once by id.
	for _, f := range chatReplayFrames(proto.ChatScopeSession, s.chat.snapshot()) {
		sub.send(f)
	}
	if room != nil {
		for _, f := range room.replayFrames() {
			sub.send(f)
		}
	}
	s.hub.add(sub)
	s.hub.Broadcast(s.viewersFrame())
	if s.info.Attention.State != AttentionNone {
		sub.send(proto.MustControl(attentionMessage(s.info.Attention)))
	}
	var joined ChatMessage
	var told bool
	if !sub.quiet {
		joined, told = s.chatSystem(sub, "join")
	}
	sessionID := s.info.ID
	s.mu.Unlock()
	if told {
		s.chatHook(sessionID, joined)
	}
	if room != nil {
		room.system(sub, "join")
		room.broadcastRoster()
	}
	go func() {
		<-sub.done
		s.Detach(sub)
	}()
	if !sub.quiet {
		s.Record(ActivityEntry{Type: ActivityJoin, By: sub.ID, ByName: sub.Name})
	}
	s.notifyChange()
	return sub, nil
}

// Detach removes a client. It is safe to call more than once.
func (s *Local) Detach(sub *Subscription) {
	s.closeNvims(sub)
	s.mu.Lock()
	_, present := s.hub.subs[sub.ID]
	s.hub.remove(sub.ID)
	sub.closeWith(nil)
	var left ChatMessage
	var told bool
	if present {
		s.hub.Broadcast(s.viewersFrame())
		if !sub.quiet {
			left, told = s.chatSystem(sub, "leave")
		}
	}
	sessionID := s.info.ID
	room := s.opts.RunChat
	s.mu.Unlock()
	if told {
		s.chatHook(sessionID, left)
	}
	if present && room != nil {
		room.system(sub, "leave")
		room.broadcastRoster()
	}
	if present {
		if !sub.quiet {
			s.Record(ActivityEntry{Type: ActivityLeave, By: sub.ID, ByName: sub.Name})
		}
		s.notifyChange()
	}
}

// Input forwards keystrokes from a controller, raw. They answer the
// needs_input prompt that was showing as they were written, unless they are
// only a terminal's own report (isTerminalReport: xterm answering a cursor
// position query), which is written and answers nothing.
func (s *Local) Input(sub *Subscription, data []byte) error {
	if sub.Role != RoleControl {
		return ErrReadOnly
	}
	s.mu.Lock()
	ended := s.info.Status.Ended()
	// The prompt this input answers is the one on the screen as it is typed.
	// A process that reacts before Write returns (an echo that rings the bell)
	// may raise the next one meanwhile, and typing must not clear that one.
	// Each attention change gets a new Since, which tells the prompts apart.
	promptSince := s.info.Attention.Since
	s.mu.Unlock()
	if ended {
		return ErrSessionEnded
	}
	if _, err := s.proc.Write(data); err != nil {
		return err
	}
	if isTerminalReport(data) {
		return nil
	}
	s.stampTyping(sub)
	s.answer(promptSince, sub.ID, sub.Name, true, bytes.IndexByte(data, '\r') >= 0)
	return nil
}

// ErrTextTooLong refuses a submission whose text is longer than MaxSubmitText
// bytes once cleaned: nothing was written.
var ErrTextTooLong = fmt.Errorf("session: text to submit is longer than %d bytes", MaxSubmitText)

// stampTyping marks sub as typing now and refreshes the roster at most every
// 2 s (presence).
func (s *Local) stampTyping(sub *Subscription) {
	now := time.Now().UnixMilli()
	if prev := sub.lastInput.Swap(now); now-prev > 2000 {
		s.mu.Lock()
		s.hub.Broadcast(s.viewersFrame())
		s.mu.Unlock()
	}
}

// answer settles the needs_input prompt a write answered: the one stamped
// promptSince, when it is still showing, is cleared and recorded as answered
// by by (a subscriber ID, "" for Conductor) and byName. A trust question is
// answered only by a write with Enter in it (enter): an arrow key that moves
// its selection leaves it showing, so that no prompt is typed into it. First
// reply wins: the check, the answer and the clear share one critical
// section, so two concurrent typists cannot both claim the prompt. With
// record, an input entry with the question records the answer.
func (s *Local) answer(promptSince *time.Time, by, byName string, record, enter bool) {
	s.mu.Lock()
	att := s.info.Attention
	waiting := att.State == AttentionNeedsInput && att.Since == promptSince && (enter || att.Source != SourceTrust)
	question := att.Message
	var answered ChatMessage
	var answeredID string
	var told bool
	if waiting {
		s.info.LastAnswer = &Answer{By: by, ByName: byName, At: time.Now().UTC(), Message: question}
		s.info.Attention = Attention{State: AttentionNone, Source: SourceInput}
		s.signalAttention()
		s.hub.Broadcast(proto.MustControl(attentionMessage(s.info.Attention)))
		answered, answeredID, told = s.answeredInChat(by, byName)
	}
	s.mu.Unlock()
	if !waiting {
		return
	}
	if told {
		s.chatHook(answeredID, answered)
		s.tellRun(answered)
	}
	if att.Source == SourceTrust && s.trust != nil {
		// What showed the question is behind: only a new one counts.
		s.trust.Reset()
	}
	if record {
		s.Record(ActivityEntry{Type: ActivityInput, By: by, ByName: byName, Message: question})
	}
	s.notifyChange()
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

// DisconnectLink evicts every subscription created through linkID: as
// revoked, or, once the session has ended (its links go with it), as the
// session's end, so that a viewer whose status frame the close overtakes
// still learns which.
func (s *Local) DisconnectLink(linkID string) {
	reason := ErrRevoked
	s.mu.Lock()
	if s.info.Status.Ended() {
		reason = ErrSessionEnded
	}
	s.mu.Unlock()
	s.hub.Each(func(sub *Subscription) {
		if sub.LinkID == linkID {
			sub.closeWith(reason)
		}
	})
}

// CloseAll evicts every subscription with the given reason (server shutdown).
func (s *Local) CloseAll(reason error) {
	s.hub.Each(func(sub *Subscription) {
		s.closeNvims(sub)
		sub.closeWith(reason)
	})
}

// Send queues an arbitrary frame to one subscription (used for file responses).
func (s *Local) Send(sub *Subscription, frame []byte) { sub.send(frame) }

// Viewers returns the number of attached clients.
func (s *Local) Viewers() int { return s.hub.LoudCount() }

// Drained reports whether every attached client has been handed all the frames
// the session sent it, the final status among them once the session has ended.
func (s *Local) Drained() bool { return s.hub.Drained() }

// LinkViewers counts attached clients per share link id.
func (s *Local) LinkViewers() map[string]int { return s.hub.CountByLink() }
