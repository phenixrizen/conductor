package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
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
	// FileDeny lists what no file read, save or editor may reach, even inside
	// the working directory: a directory with everything in it, a single
	// file, or a name entry (see insideAny); DenyList builds one. The server
	// passes its data directory, whose catalog.json holds agent secrets, its
	// config file and its catalog file.
	FileDeny []string
	// FileDenyFunc, when set, gives the deny list for each file request in
	// place of FileDeny: a list that may grow while the session runs, as
	// `conductor host`'s follows the server on its machine
	// (config.LocalFileDenyFunc). It is called from many goroutines.
	FileDenyFunc func() []string
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
	// one shows. Codex's "Hooks need review" is one. A question marked
	// Anytime is one the agent asks during its work (Codex's command
	// approval): it is watched for the session's whole life by a watcher of
	// its own, with source "question" (fireAsked).
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
	// Folder is the session's working directory, opened (and checked) by
	// whoever started the process in it: the Files tab reads and saves
	// through it, and the session closes it when it is retired. NewLocal
	// opens Info.Cwd itself when it is nil.
	Folder *Folder
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
	// folder is the working directory as opened (Options.Folder); nil when
	// it could not be opened, which refuses every file request.
	folder *Folder

	mu   sync.Mutex // guards info, events and lastOutput, and orders ring writes against attaches
	info Info
	// stopRequested makes an exit observed by the pump report "stopped".
	stopRequested bool
	// retired says the session left the server's registry (Retire): no
	// client attaches any more.
	retired bool
	// closing holds the clients that have left the hub (Detach) while their
	// transport still closes: with the hub's, every client until its
	// transport has closed, for DisconnectLink and CloseAll to wait for.
	closing map[*Subscription]struct{}
	// passedOn holds the clients whose slot toward MaxViewers another took
	// over (AttachOptions.Replaces), still attached or still closing: they
	// no longer count toward the cap, while revokes and shutdowns still wait
	// for them. A client leaves it as its transport closes.
	passedOn map[*Subscription]struct{}
	// lastOutput is when the pump last read output; zero until it has.
	lastOutput time.Time

	activity activityRing
	// touched is the Touched section's index of the files the agent touched (touched.go), under mu.
	touched touchedIndex
	// gitSeen watches the working tree for files no hook named (gitseen.go); its own lock.
	gitSeen gitSeen
	// sizer is the viewer whose window sizes the PTY, "" while none holds it
	// (round 14): one of the owner's own windows takes it as it attaches,
	// any controller by asking (ResizeWith take, Fit to my window), and
	// every other viewer follows the size, scaled to its window. Under mu.
	sizer string
	// nvimCount is the editors open on the session (nvim.go), every
	// connection's together.
	nvimCount atomic.Int32
	chat      chatRing // guarded by mu
	// question is the id of the agent's question in the chat while one
	// stands (askInChat), for the line that says it was answered.
	question  string
	events    EventBucket // guarded by mu
	dropped   atomic.Uint64
	scanner   Scanner
	pattern   *PatternWatcher   // nil without Options.Pattern
	trust     *PatternWatcher   // nil without a startup question
	questions []StartupQuestion // Options.TrustPattern's, then Options.Questions not marked Anytime
	// asked watches for the questions the agent asks during its work
	// (Options.Questions marked Anytime, askedQuestions) until the session
	// ends; nil without one. askedKeys are their answers' keys.
	asked          *PatternWatcher
	askedQuestions []StartupQuestion
	askedKeys      map[string]bool
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
	folder := opts.Folder
	if folder == nil {
		folder, _ = OpenFolder(info.Cwd)
	}
	s := &Local{
		opts:   opts,
		folder: folder,
		proc:   proc,
		ring:   NewRing(opts.ScrollbackBytes),
		hub:    NewHub(),
		log:    opts.Log.With("session", info.ID),
		info:   info,
		ended:  make(chan struct{}),

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
		switch {
		case q.Pattern == nil:
		case q.Anytime:
			s.askedQuestions = append(s.askedQuestions, q)
		default:
			s.questions = append(s.questions, q)
		}
	}
	if len(s.questions) > 0 {
		// One watcher for every question: its pattern is theirs together, and
		// the fire tells which one shows.
		s.trust = NewScreenWatcher(anyOf(s.questions), patternQuiet, s.fireTrust)
	}
	if len(s.askedQuestions) > 0 {
		s.asked = NewScreenWatcher(anyOf(s.askedQuestions), patternQuiet, s.fireAsked)
		s.askedKeys = map[string]bool{}
		for _, q := range s.askedQuestions {
			for _, a := range q.Answers {
				s.askedKeys[a.Input] = true
			}
		}
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
			if s.asked != nil && !title {
				s.asked.Feed(chunk)
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
	if s.asked != nil {
		s.asked.Stop()
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
//
// A bare bell says only that something waits: a session that waits already
// keeps what it waits on (a question's words and choices, a hook's report),
// which the bell would have replaced with "terminal bell" and no choice. An
// OSC notification has words of its own and replaces a wait that has none
// to answer it with, but never a question with its choices or one the
// session holds (unlessChoices).
func (s *Local) scanOutput(chunk []byte) {
	for _, ev := range s.scanner.Scan(chunk) {
		s.mu.Lock()
		if time.Since(s.lastBell) < 500*time.Millisecond {
			s.mu.Unlock()
			continue
		}
		s.lastBell = time.Now()
		s.mu.Unlock()
		msg, rule := ev.Message, unlessChoices
		if msg == "" {
			msg, rule = "terminal bell", unlessWaiting
		}
		s.setAttention(AttentionNeedsInput, msg, ev.Source, "", nil, rule)
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
		if words := q.words(text); words != "" {
			s.setAttention(AttentionNeedsInput, words, SourceTrust, KindPrompt, CleanOptions(q.Answers), unlessWaiting)
			return
		}
	}
}

// fireAsked is the fire of the watcher for the questions the agent asks
// during its work (Options.Questions marked Anytime): one is on the screen,
// text its last ScreenTail. The session needs input with source
// "question", the words of the first question found and its answers. It
// takes the place of a wait that has nothing to answer it with (the bell
// that rang for the dialog, an OSC notification, a hook's report without
// choices) and of a done: the dialog on the screen is what the program
// waits on now, and its choices are the only safe answer, since typed text
// would reach the dialog's keys. It never replaces a question with its
// choices, a hook's or another (unlessChoices).
func (s *Local) fireAsked(text string) {
	for _, q := range s.askedQuestions {
		if words := q.words(text); words != "" {
			s.setAttention(AttentionNeedsInput, words, SourceQuestion, KindPermission, CleanOptions(q.Answers), unlessChoices)
			return
		}
	}
}

// heldSource reports whether a prompt of source src is a question the
// session holds: the agent's trust or other startup question, or a
// question it asks during its work. Typed text is refused while one shows
// (Submit), and only its choice or an Enter answers it (Input).
func heldSource(src string) bool { return src == SourceTrust || src == SourceQuestion }

// anyOf is one pattern for the questions together: a watcher's, whose fire
// tells which one shows.
func anyOf(qs []StartupQuestion) *regexp.Regexp {
	parts := make([]string, len(qs))
	for i, q := range qs {
		parts[i] = "(?:" + q.Pattern.String() + ")"
	}
	return regexp.MustCompile(strings.Join(parts, "|"))
}

// StartupQuestion is one question an agent asks as it starts (Options.Questions):
// the pattern of its words on the screen and its answers as choices. One
// marked Anytime is asked during the agent's work instead.
type StartupQuestion struct {
	Pattern *regexp.Regexp
	// Anytime marks a question the agent can ask at any time in the
	// session's life (the catalog's anytime): watched until the session
	// ends, not only until the first submission.
	Anytime bool
	// Words, when set, picks the words a page shows out of the same text
	// (the catalog's words): its last match's capturing groups joined by a
	// space, or that whole match without a group. Without it, or when it
	// matches nothing, the words are what Pattern matched.
	Words   *regexp.Regexp
	Answers []Option
}

// words returns the question's words when text shows it, "" when it does not.
func (q StartupQuestion) words(text string) string {
	match := q.Pattern.FindString(text)
	if match == "" || q.Words == nil {
		return match
	}
	all := q.Words.FindAllStringSubmatch(text, -1)
	if len(all) == 0 {
		return match
	}
	last := all[len(all)-1]
	groups := last[1:]
	if len(groups) == 0 {
		groups = last[:1]
	}
	var parts []string
	for _, g := range groups {
		if g = strings.TrimSpace(g); g != "" {
			parts = append(parts, g)
		}
	}
	if len(parts) == 0 {
		return match
	}
	return strings.Join(parts, " ")
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
	// unlessChoices changes nothing when the session is needs_input on a
	// prompt that has choices, or on a question it holds (heldSource): a
	// wait without anything to answer it with gives way, one with its
	// answers stays.
	unlessChoices
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
	waiting := cur.State == AttentionNeedsInput
	answerable := waiting && (len(cur.Options) > 0 || heldSource(cur.Source))
	// A report from outside that the session waits, without choices, does
	// not hide a question asked during the work, whose choices are what
	// answers the dialog on the screen (a hook that reports the same dialog
	// late, say).
	keepAsked := rules&spendToken != 0 && state == AttentionNeedsInput && len(options) == 0 && waiting && cur.Source == SourceQuestion
	if (rules&unlessWaiting != 0 && waiting) || (rules&unlessChoices != 0 && answerable) || keepAsked {
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
	// Authorize, when set, says what the client's credential gives it now.
	// AttachWith calls it under the session's lock, in the critical section
	// that registers the client, and attaches with the role it returns in
	// place of Role; an error refuses the attach and AttachWith returns it
	// (ErrRevoked, or ErrExpired, for a link that no longer opens the
	// session). Whoever revokes a link marks it first and then closes its
	// clients (DisconnectLink, which takes the same lock), so either this
	// check sees the mark or that close sees the client. A Grant with Until
	// closes the client with ErrExpired then. Authorize must not call the
	// session.
	Authorize func() (Grant, error)
	// Owner marks one of the owner's own connections: the workbench's on a
	// server, a host's viewer with no link from a server the person said is
	// their own (never a switchyard's). Only such a
	// connection is offered the editor's Neovim (see nvimAllowed); a link's
	// never is, nor a paste invite's, whatever the caller says.
	Owner bool
	// Replaces is the subscription this client takes over: the same viewer
	// moving to another transport (a host's viewer falling back from WebRTC
	// to the relay). While that one's transport still closes, its slot is
	// this client's and is not counted twice toward MaxViewers. Only a
	// subscription of this session already ended counts.
	Replaces *Subscription
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
	if o.Authorize == nil && !role.Valid() {
		return nil, errors.New("session: invalid role")
	}
	if id == "" {
		id = NewID()
	}
	s.mu.Lock()
	if s.retired {
		s.mu.Unlock()
		return nil, ErrSessionEnded
	}
	// The credential is checked under the lock that registers the client
	// (see AttachOptions.Authorize).
	var until time.Time
	if o.Authorize != nil {
		g, err := o.Authorize()
		if err == nil && !g.Role.Valid() {
			err = errors.New("session: invalid role")
		}
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		role, until = g.Role, g.Until
	}
	// Every client whose transport is still open counts, an ended one whose
	// transport still closes included: at most MaxViewers connections at once.
	// The one this client replaces gives it its slot.
	// The client this one takes over gives it its slot, once. One still
	// attached (the move seen before its old transport's end) ends here: its
	// own Detach later finds it gone, so the move makes no leave and no join.
	r := o.Replaces
	_, given := s.passedOn[r]
	replacing := r != nil && !given
	if replacing && s.hub.remove(r) {
		r.closeWith(nil)
		// The size stays with the viewer's id, which a replacement under the
		// same id keeps; under another id it is handed on as at a leave.
		if s.sizer == r.ID && o.ID != r.ID {
			s.handOverSize()
		}
		select {
		case <-r.closed:
		default:
			if s.closing == nil {
				s.closing = map[*Subscription]struct{}{}
			}
			s.closing[r] = struct{}{}
		}
	}
	_, closing := s.closing[r]
	replacing = replacing && closing
	open := s.hub.Len() + len(s.closing) - len(s.passedOn)
	if replacing {
		open--
	}
	if open >= s.opts.MaxViewers {
		s.mu.Unlock()
		return nil, ErrTooManyViewers
	}
	// The hello's size ((0, 0) follows; proto.HelloSize). One viewer sizes
	// the PTY: one of the owner's own windows (a controller with no link)
	// takes the size as it attaches while nobody holds it; any other
	// controller's size is kept as what its window would want, and it takes
	// the size only by asking (Fit to my window). So a coworker's laptop
	// joining through a link never shrinks the session on the owner's
	// screen, and two of the owner's windows never take turns. The role
	// comes from the token, never from the hello.
	sized := role == RoleControl && proto.HelloSize(cols, rows) && !s.info.Status.Ended()
	if sized && o.LinkID == "" && !o.ChatOnly && s.sizer == "" {
		s.sizer = id
		if cols != s.info.Cols || rows != s.info.Rows {
			if err := s.proc.Resize(cols, rows); err == nil {
				s.info.Cols, s.info.Rows = cols, rows
			}
		}
		s.hub.Broadcast(proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: s.info.Cols, Rows: s.info.Rows, By: id}))
	}
	transport := s.opts.Transport
	if named, ok := sink.(interface{ Transport() string }); ok {
		transport = named.Transport()
	}
	sub := newSubscription(id, role, o.LinkID, sink)
	sub.Name = CleanName(o.Name)
	sub.LinkLabel = o.LinkLabel
	sub.quiet = o.ChatOnly
	sub.owner = o.Owner && o.LinkID == ""
	if sized {
		sub.want = [2]uint16{cols, rows}
	}
	room := s.opts.RunChat
	sub.send(proto.MustControl(proto.Welcome{
		T:               proto.CtlWelcome,
		Proto:           proto.ProtoVersion,
		SessionID:       s.info.ID,
		Role:            string(role),
		SubscriberID:    id,
		Cols:            s.info.Cols,
		Rows:            s.info.Rows,
		Sizer:           true,
		SizedBy:         s.sizer,
		Status:          string(s.info.Status),
		ScrollbackBytes: s.ring.Cap(),
		Transport:       transport,
		FileView:        s.fileAllowed(role),
		FileEdit:        s.editAllowed(role),
		Nvim:            nvimAvailable() && !s.nvimOwnerOnly(sub),
		NvimOwnerOnly:   s.nvimOwnerOnly(sub),
		Chat:            true,
		RunChat:         room != nil,
		Cwd:             welcomeCwd(s.info.Cwd, s.fileAllowed(role)),
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
	if replacing {
		// Its slot is this client's now, for every later check too.
		if s.passedOn == nil {
			s.passedOn = map[*Subscription]struct{}{}
		}
		s.passedOn[r] = struct{}{}
	}
	// A credential that lapses closes the client as it lapses, one that
	// attached a moment before included (at once when that moment has passed).
	var lapse *time.Timer
	if !until.IsZero() {
		lapse = time.AfterFunc(time.Until(until), func() { sub.closeWith(ErrExpired) })
	}
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
		if lapse != nil {
			lapse.Stop()
		}
		s.Detach(sub)
		// Detach keeps it among the closing until its transport has closed.
		<-sub.closed
		s.mu.Lock()
		delete(s.closing, sub)
		delete(s.passedOn, sub)
		s.mu.Unlock()
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
	present := s.hub.remove(sub)
	sub.closeWith(nil)
	// Out of the hub, it stays among the closing until its transport has
	// closed (AttachWith's watcher takes it out), for a revoke to wait for.
	select {
	case <-sub.closed:
		// Closed already: gone from the closing and from the slots passed on
		// together, so no check in between counts it again.
		delete(s.closing, sub)
		delete(s.passedOn, sub)
	default:
		if s.closing == nil {
			s.closing = map[*Subscription]struct{}{}
		}
		s.closing[sub] = struct{}{}
	}
	// Only the client that held the size as it left hands it on: a late
	// Detach of one a replacement took over (the same id) leaves the size
	// with the replacement.
	if present && s.sizer == sub.ID {
		s.handOverSize()
	}
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
	// A question's own choice answers it, with an Enter or without: some
	// dialogs pick on a digit (Codex's background-server question) or skip on
	// Escape (its update question), and an Enter after those would land on
	// the next screen.
	choice := false
	for _, o := range s.info.Attention.Options {
		if o.Input != "" && o.Input == string(data) {
			choice = true
			break
		}
	}
	enter := choice || bytes.IndexByte(data, '\r') >= 0
	// The screen that showed a startup question is behind once it is
	// answered: only a new question counts. It is cleared before the answer
	// is written, never after: a program that draws its next question as the
	// first is answered (Codex's update question after its background-server
	// one) can have drawn it before the write returns, and a clear after it
	// took that question with it, unseen (round 14, the trust spec's flake).
	answersQuestion := enter && s.info.Attention.State == AttentionNeedsInput && heldSource(s.info.Attention.Source) && s.info.Attention.Since == promptSince
	s.mu.Unlock()
	if ended {
		return ErrSessionEnded
	}
	if answersQuestion && s.trust != nil {
		s.trust.Reset()
	}
	// A question asked during the work is behind once a key that answers
	// such a dialog is written (an Enter, or one of its choices' keys),
	// whether or not the session showed it yet: a person who answers in the
	// terminal before the watcher has seen the screen quiet leaves the
	// dialog's words in its text, and they must not raise the question
	// again once the program has moved on. Other keys (a mouse report, an
	// arrow that moves the highlight) leave it.
	if s.asked != nil && (enter || s.askedKeys[string(data)]) {
		s.asked.Reset()
	}
	if _, err := s.proc.Write(data); err != nil {
		return err
	}
	if isTerminalReport(data) {
		return nil
	}
	s.stampTyping(sub)
	s.answer(promptSince, sub.ID, sub.Name, true, enter)
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
// by by (a subscriber ID, "" for Conductor) and byName. A question the
// session holds (a trust or other startup question, or one asked during the
// work) is answered only by a write with Enter in it or one of its choices
// (enter): an arrow key that moves its selection leaves it showing, so that
// no prompt is typed into it. First reply wins: the check, the answer and
// the clear share one critical section, so two concurrent typists cannot
// both claim the prompt. With record, an input entry with the question
// records the answer.
func (s *Local) answer(promptSince *time.Time, by, byName string, record, enter bool) {
	s.mu.Lock()
	att := s.info.Attention
	waiting := att.State == AttentionNeedsInput && att.Since == promptSince && (enter || !heldSource(att.Source))
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
	if record {
		s.Record(ActivityEntry{Type: ActivityInput, By: by, ByName: byName, Message: question})
	}
	s.notifyChange()
}

// Resize applies the latest-controller-wins policy and broadcasts the result.
func (s *Local) Resize(sub *Subscription, cols, rows uint16) error {
	return s.ResizeWith(sub, cols, rows, false)
}

// ResizeWith sizes the PTY for sub's window. Only the sizer's window sizes
// it (see AttachWith): a passive resize (the window changed, a tab came
// back) from another viewer is kept as what its window would want and
// refused with ErrNotSizer, except from one of the owner's own windows
// while nobody holds the size. With take (Fit to my window) any controller
// becomes the sizer. Every viewer hears who sizes it in the resize frame's
// By, a change of sizer at the same size included.
func (s *Local) ResizeWith(sub *Subscription, cols, rows uint16, take bool) error {
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
	sub.want = [2]uint16{cols, rows}
	if !take && s.sizer != sub.ID && (s.sizer != "" || !sub.ownsSize()) {
		return ErrNotSizer
	}
	handed := s.sizer != sub.ID
	s.sizer = sub.ID
	if cols == s.info.Cols && rows == s.info.Rows && !handed {
		return nil
	}
	if cols != s.info.Cols || rows != s.info.Rows {
		if err := s.proc.Resize(cols, rows); err != nil {
			return err
		}
		s.info.Cols, s.info.Rows = cols, rows
	}
	s.hub.Broadcast(proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: cols, Rows: rows, By: sub.ID}))
	return nil
}

// handOverSize finds the size a new holder as the sizer leaves: the
// owner's own window that attached last and said its size, at that size,
// else nobody (the size stays, and every viewer hears By ""). Callers hold
// s.mu; the leaving viewer is out of the hub already.
func (s *Local) handOverSize() {
	s.sizer = ""
	var next *Subscription
	s.hub.Each(func(c *Subscription) {
		if c.ownsSize() && proto.HelloSize(c.want[0], c.want[1]) && (next == nil || c.Since.After(next.Since)) {
			next = c
		}
	})
	if next != nil && !s.info.Status.Ended() {
		s.sizer = next.ID
		if cols, rows := next.want[0], next.want[1]; cols != s.info.Cols || rows != s.info.Rows {
			if err := s.proc.Resize(cols, rows); err == nil {
				s.info.Cols, s.info.Rows = cols, rows
			}
		}
	}
	s.hub.Broadcast(proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: s.info.Cols, Rows: s.info.Rows, By: s.sizer}))
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
// still learns which. It ends them under the lock AttachWith registers them
// under, so a caller that marked the link first (a revoke) misses none: one
// that attaches after this finds the mark (AttachOptions.Authorize); and
// nothing broadcast after reaches them. It returns once their transports
// have closed, which they do together, each on its own goroutine, holding no
// lock: as long as the slowest takes. That includes the link's clients that
// had ended already (a slow consumer, an earlier revoke) and whose transport
// still closes.
func (s *Local) DisconnectLink(linkID string) {
	reason := ErrRevoked
	var hit []*Subscription
	s.mu.Lock()
	if s.info.Status.Ended() {
		reason = ErrSessionEnded
	}
	s.hub.Each(func(sub *Subscription) {
		if sub.LinkID == linkID {
			sub.closeWith(reason)
			hit = append(hit, sub)
		}
	})
	for sub := range s.closing {
		if sub.LinkID == linkID {
			hit = append(hit, sub)
		}
	}
	s.mu.Unlock()
	waitClosed(hit)
}

// Retire is the session leaving the server's registry, which only an ended
// session does: every client is closed as at the session's end, and none
// attaches after (ErrSessionEnded). Whoever closes a link's clients finds
// them through the registry, so none may stay where it no longer looks.
// It does not wait: each client's transport closes on the client's own
// goroutine, and its editors go as it detaches, so one that does not read
// holds up only its own.
func (s *Local) Retire() {
	s.mu.Lock()
	s.retired = true
	s.hub.Each(func(sub *Subscription) { sub.closeWith(ErrSessionEnded) })
	s.mu.Unlock()
	if s.folder != nil {
		// A file request under way finishes or fails; none comes after.
		s.folder.Close()
	}
}

// CloseAll evicts every subscription with the given reason (server shutdown)
// and ends their editors. It returns once their transports have closed,
// those of the clients that had ended already included, together, as
// DisconnectLink's do.
func (s *Local) CloseAll(reason error) {
	var all []*Subscription
	s.mu.Lock()
	s.hub.Each(func(sub *Subscription) {
		sub.closeWith(reason)
		all = append(all, sub)
	})
	for sub := range s.closing {
		all = append(all, sub)
	}
	s.mu.Unlock()
	for _, sub := range all {
		s.closeNvims(sub)
	}
	waitClosed(all)
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

// welcomeCwd is the working directory a welcome carries: the session's own,
// to a viewer whose role may read files (it roots the Files pane, which is
// all it is for), or "" when its JSON string, as the welcome encodes it
// (`&`, `<` and `>` escaped to six bytes each, a control character or a byte
// that is not UTF-8 likewise), passes proto.MaxCwd bytes: the welcome must
// fit one CONTROL frame on every path, the relay's included.
func welcomeCwd(cwd string, files bool) string {
	if !files || len(cwd) > proto.MaxCwd {
		return ""
	}
	if b, err := json.Marshal(cwd); err != nil || len(b) > proto.MaxCwd {
		return ""
	}
	return cwd
}
