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
	"strconv"
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
	// approval): it is watched for the session's whole life on a model of the
	// screen (Screen), by a watcher of its own, with source "question"
	// (fireAsked, missAsked).
	Questions []StartupQuestion
	// WorkingTitle, when set, matches the window title (OSC 0 or 2) the
	// agent writes while it works (the catalog's signal.workingTitle:
	// Codex's "⠋ Working", with the title Conductor's launch asks it for).
	// The session keeps whether the last title it read matches
	// (readTitle), and a title that matches shows working (source
	// "title"), as the agent's working report would: over a bell, an OSC
	// notification, the agent's report, the screen pattern, the empty state
	// or the done of a turn an Enter has followed; never over a question
	// the session holds (a startup question, a dialog on the screen), a
	// held turn, the done of the turn just ended, nor a wait younger than
	// TitleSettle. A wait raised while the title says the agent works, or
	// met by such a title before its TitleSettle has passed, gives way to
	// working once it has, unless a title that does not match came between
	// (settled). Titles are not weighed without it.
	WorkingTitle *regexp.Regexp
	// TitleSettle is how long a wait stands before a title that says the
	// agent works takes its place; TitleSettle when zero.
	TitleSettle time.Duration
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
	// InputWait bounds how long a write to the process waits for the
	// program to take it (input.go); InputWait when zero.
	InputWait time.Duration
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

// bellPace is the least time between two changes the bell or OSC
// notifications make (bellPaced): a burst of bells makes one.
const bellPace = 500 * time.Millisecond

// TitleSettle is how long a wait (a bell, an OSC notification, the agent's
// report) stands before a window title that says the agent works takes its
// place (Options.WorkingTitle): long enough for Codex to draw what it rang
// the bell for (an approval, whose title then says Action Required), short
// enough that a question Codex goes on from does not hold the session.
const TitleSettle = time.Second

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
	question string
	// answered is the id (Attention.ID) of the last question a write was
	// made to answer, set as the writer takes the write (claimQuestion,
	// noteAnswering): until the write is made and answer clears the
	// question, it still shows, and a reply to it that comes after is
	// refused (ErrQuestionAnswered). dialogAnswered is the words of the
	// dialog asked during the work that a write was last made to answer,
	// published or not yet, until the screen's watcher sees it go (a quiet
	// look with no dialog, missAsked) or another dialog in its place: a
	// dialog with those words asked meanwhile is that question still,
	// answered (sameDialog). dialogAnsweredAt is how many Feeds the
	// watcher had seen as it was noted (PatternWatcher.Fed): a look that
	// took its text before that (LookedAt) does not see it go. Guarded by
	// mu.
	answered         string
	dialogAnswered   string
	dialogAnsweredAt uint64
	// keysIn splits what is written to the program into the keys it reads
	// (keysWritten), every write made, in the writer's order. Guarded by mu.
	keysIn keyReader
	// startupAnswered says a write answered a startup question, and
	// startupAnsweredAt how many Feeds the startup questions' watcher had
	// seen then (Fed), as its text was put behind: a look of that watcher
	// that took its text no later (LookedAt) saw the answered screen, and
	// asks nothing (fireTrust). Guarded by mu.
	startupAnswered   bool
	startupAnsweredAt uint64
	// startupAnsweredWords are the words of the startup question last
	// answered, until another startup question takes its place: asked
	// again meanwhile (the program draws it again before it moves on), it
	// is asked answered, with an id no reply is taken for. Guarded by mu.
	startupAnsweredWords string
	// startupAnsweredFed is how many Feeds the screen the questions asked
	// during the work are read from had seen when a startup question was
	// last answered: asked again answered (startupAnsweredWords), it is held
	// so (heldAnswer) from then, and goes once that screen shows no startup
	// question. Guarded by mu.
	startupAnsweredFed uint64
	// heldAnswer is the startup question an Escape a write ended
	// with may have answered (holdStartupAnswered): its id once asked, its
	// words while not asked yet, who typed it, and how much output the
	// screen had been fed then. Held answered, it is cleared once the
	// screen shows no startup question (escGone). Guarded by mu.
	heldAnswer heldStartup
	// feedMu is held while output is fed to the startup questions' watcher
	// and the screen's (pump), and while escGone and heldGone look at the
	// screen and put the startup output behind: what they look at is all
	// that has been fed, and none is put behind unseen. Taken before mu.
	feedMu sync.Mutex

	events    EventBucket // guarded by mu
	dropped   atomic.Uint64
	scanner   Scanner
	pattern   *PatternWatcher   // nil without Options.Pattern
	trust     *PatternWatcher   // nil without a startup question
	questions []StartupQuestion // Options.TrustPattern's, then Options.Questions not marked Anytime
	// asked watches the screen for the questions the agent asks during its
	// work (Options.Questions marked Anytime, askedQuestions) until the
	// session ends; nil without one. askedKeys are their answers' keys.
	// typedAtAsked is the person who last typed into the session while one
	// showed, and that question's Since (missAsked); guarded by mu.
	asked          *PatternWatcher
	askedQuestions []StartupQuestion
	askedKeys      map[string]bool
	typedAtAsked   *typedAt
	// lastBell is when the bell or an OSC notification last changed the
	// attention (bellPaced); zero once a new turn has begun (beginTurn).
	// Guarded by mu.
	lastBell time.Time
	// titleWorking says the last window title read matched
	// Options.WorkingTitle (readTitle). settleID is the id of the wait
	// shown last that a title may settle (needs_input, but no question the
	// session holds and no held turn; empty while none shows), settleAt the
	// time (monotonic) its TitleSettle ends, and settleTimer the timer that
	// gives it way to working then (settled), armed while the title says
	// the agent works, nil otherwise; settleGen tells a timer from the one
	// before. Kept only with Options.WorkingTitle. Guarded by mu.
	titleWorking bool
	settleID     string
	settleAt     time.Time
	settleTimer  *time.Timer
	settleGen    uint64
	// turnEnded says a done was reported and neither an Enter has been
	// written since (Input, Submit) nor another state shown: the turn it
	// ended is still the last one, and a bell or OSC notification now is
	// that turn's own (unlessTurnEnded). Guarded by mu.
	turnEnded bool
	// turn counts the turns begun after a done (beginTurn), changed under
	// mu: output read before one began, and scanned after, is the ended
	// turn's own (scanOutput). The pump takes it as soon as a read
	// returns, before anything else can hold it up.
	turn atomic.Uint64
	// scanBegun is turn as the chunk was read in which the escape sequence
	// the scanner has under way began (scanOutput); the pump's own.
	scanBegun uint64
	// beforeScan, a test's hook, runs with each chunk read between the
	// read (and the turn taken with it) and everything the pump does with
	// it.
	beforeScan atomic.Pointer[func(chunk []byte)]
	// beforeSettle, a test's hook, runs as the settle timer fires, before
	// it looks at anything (settled); afterTitle between readTitle's look
	// at a title that matches and its change.
	beforeSettle   atomic.Pointer[func()]
	afterTitle     atomic.Pointer[func()]
	agentTokenHash [32]byte
	hasAgentToken  bool

	ended chan struct{}

	// submitting holds a token while a submission runs (Submit): one at a
	// time per session. turnWaiters counts the submissions waiting for it
	// (awaitTurn), at most maxTurnWaiters.
	submitting  chan struct{}
	turnWaiters atomic.Int32
	// held is closed by the first keys a person types into the terminal
	// (input) while the session's turn is held for a submission's text
	// typed without its Enter (holdTyped); nil while it is not. heldNext is
	// closed as the next such hold begins, nil until someone asks
	// (Holding). Under mu.
	held     chan struct{}
	heldNext chan struct{}
	// heldOver is the question the hold of the turn took the place of as
	// it showed (an agent's, waiting on a line: the held text was typed
	// for it), shown again when a person's keys end the hold without
	// answering it (endHoldTyped); empty when none. Under mu.
	heldOver Attention
	// writer makes every write to the process, in order (input.go).
	writer procWriter
	// nvimEndWait is nvimEndWait as the session was made: its editors'.
	nvimEndWait time.Duration
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
	if opts.InputWait <= 0 {
		opts.InputWait = InputWait
	}
	if opts.TitleSettle <= 0 {
		opts.TitleSettle = TitleSettle
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
		nvimEndWait: nvimEndWait,
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
	if len(s.askedQuestions) > 0 || len(s.questions) > 0 {
		// The screen the questions asked during the work are read from. It
		// is also what tells a startup question held answered gone
		// (escGone): with startup questions alone it is kept for that,
		// matching nothing, until the startup questions' watch ends.
		re := matchNothing
		if len(s.askedQuestions) > 0 {
			re = anyOf(s.askedQuestions)
		}
		s.asked = NewScreenModelWatcher(re, patternQuiet, int(info.Cols), int(info.Rows), s.fireAsked, s.missAsked)
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
		// The turn the output was read in: taken at once, so that a turn
		// begun while the pump waits for the lock below leaves it the
		// ended turn's (scanOutput).
		turn := s.turn.Load()
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if hook := s.beforeScan.Load(); hook != nil {
				(*hook)(chunk)
			}
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
			s.scanOutput(chunk, turn)
			if s.pattern != nil {
				s.pattern.Feed(chunk)
			}
			s.feedMu.Lock()
			if s.trust != nil && !title {
				s.trust.Feed(chunk)
			}
			if s.asked != nil && !title {
				s.asked.Feed(chunk)
			}
			s.feedMu.Unlock()
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
	s.settleLocked(s.info.Attention)
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
// burst of bells makes at most one change per bellPace (bellPaced). They
// are the session's own observations and spend no token of the event
// bucket.
//
// A bare bell says only that something waits: a session that waits already
// keeps what it waits on (a question's words and choices, a hook's report),
// which the bell would have replaced with "terminal bell" and no choice. An
// OSC notification has words of its own and replaces a wait that has none
// to answer it with, but never a question with its choices or one the
// session holds (unlessChoices). Neither undoes a done until the next Enter
// (unlessTurnEnded): it is the ended turn's own, as Codex's bell is. Nor
// does one read during the ended turn (turn, the count of turns begun as
// the chunk was read; for a notification begun in an earlier chunk, that
// chunk's) and looked at once the next has begun: a done and an Enter that
// come between the read and the look leave it the last turn's. A window
// title is weighed only with Options.WorkingTitle (readTitle), with the
// same turn. Only the pump calls it.
func (s *Local) scanOutput(chunk []byte, turn uint64) {
	begun := s.scanBegun
	events := s.scanner.Scan(chunk)
	if !s.scanner.Through() {
		// A sequence left unfinished began in this chunk.
		s.scanBegun = turn
	}
	for i, ev := range events {
		read := turn
		if i == 0 && s.scanner.Carried() {
			read = begun
		}
		if ev.Source == SourceTitle {
			if s.opts.WorkingTitle != nil {
				s.readTitle(ev.Message, read)
			}
			continue
		}
		msg, rule := ev.Message, unlessChoices
		if msg == "" {
			msg, rule = "terminal bell", unlessWaiting
		}
		s.setAttentionRead(AttentionNeedsInput, msg, ev.Source, "", nil, rule|unlessTurnEnded|bellPaced, read)
	}
}

// titleRules are how a window title that says the agent works is applied
// (readTitle, settled): as the agent's working report would be, but over
// nothing the screen or the session holds, not over the done of the turn
// just ended, not over a wait younger than its TitleSettle, and as no
// change while the session shows working already or has ended. It spends
// no token and stamps no bell's pace: it is the session's own observation,
// recorded only when it changes the state.
const titleRules = unlessTurnEnded | unlessHeld | unlessOnScreen | unlessFresh | unlessWorking | unlessEnded

// readTitle weighs a window title read in turn (Options.WorkingTitle). One
// that does not match ("[ ! ] Action Required", "Ready", "Starting", a cut
// one) says the agent shows something for a person, or is at rest: it
// changes nothing, and the wait showing stands (the settle timer is
// disarmed). One that matches shows working (titleRules); a wait younger
// than its TitleSettle is left to the settle timer, armed for what is left
// of it: the title is written only when its text changes, once per state
// with animations off, so a later title may never come.
//
// A title that matches read during the ended turn and looked at once the
// next has begun (turn) is that turn's own: it changes nothing, not even
// what the last title says, so that it arms no timer for the next turn's
// wait. One that does not match is weighed whenever it was read: it can
// only keep a wait standing.
func (s *Local) readTitle(title string, turn uint64) {
	working := s.opts.WorkingTitle.MatchString(title)
	s.mu.Lock()
	if working && turn != anyTurn && turn != s.turn.Load() {
		s.mu.Unlock()
		return
	}
	s.titleWorking = working
	if !working {
		s.disarmSettleLocked()
		s.mu.Unlock()
		return
	}
	if cur := s.info.Attention; cur.State == AttentionNeedsInput && cur.ID != "" && cur.ID == s.settleID && time.Now().Before(s.settleAt) {
		s.armSettleLocked()
	}
	s.mu.Unlock()
	if hook := s.afterTitle.Load(); hook != nil {
		(*hook)()
	}
	s.setAttentionRead(AttentionWorking, "", SourceTitle, "", nil, titleRules, turn)
}

// settleLocked follows the attention now showing, att, for the settle
// timer: a wait a title may settle (needs_input, but no question the
// session holds and no held turn) begins its TitleSettle, and the timer is
// armed for it while the last title says the agent works; anything else
// ends the last one's. Only with Options.WorkingTitle. The caller holds
// s.mu, and calls it wherever the attention shown changes.
func (s *Local) settleLocked(att Attention) {
	if s.opts.WorkingTitle == nil {
		return
	}
	if att.State == AttentionNeedsInput && att.ID != "" && att.ID == s.settleID {
		return // the same wait, still showing
	}
	s.disarmSettleLocked()
	s.settleID = ""
	if att.State != AttentionNeedsInput || att.ID == "" || att.Source == SourceSubmit || heldSource(att.Source) {
		return
	}
	s.settleID, s.settleAt = att.ID, time.Now().Add(s.opts.TitleSettle)
	if s.titleWorking {
		s.armSettleLocked()
	}
}

// armSettleLocked arms the settle timer for the wait settleID names, to
// fire as its TitleSettle ends (settleAt), unless it is armed already. The
// caller holds s.mu.
func (s *Local) armSettleLocked() {
	if s.settleTimer != nil || s.settleID == "" {
		return
	}
	s.settleGen++
	gen := s.settleGen
	s.settleTimer = time.AfterFunc(time.Until(s.settleAt), func() { s.settled(gen) })
}

// disarmSettleLocked stops the settle timer: a timer that has fired
// already finds itself disarmed (settled). The caller holds s.mu.
func (s *Local) disarmSettleLocked() {
	if s.settleTimer != nil {
		s.settleTimer.Stop()
		s.settleTimer = nil
	}
}

// settled is the settle timer's fire, gen the timer's: the wait it was
// armed for, if it still shows, the timer still armed for it and the last
// title still says the agent works (no title that does not match came
// since, which disarms it), gives way to working, with the title's rules
// (titleRules): a done, a question on the screen, a held turn or another
// wait that came meanwhile stays. All of that is weighed under the lock
// that applies the change (onlySettling): a title read between the fire
// and the change counts.
func (s *Local) settled(gen uint64) {
	if hook := s.beforeSettle.Load(); hook != nil {
		(*hook)()
	}
	s.setAttentionLooked(AttentionWorking, "", SourceTitle, "", nil, titleRules|onlySettling, anyTurn, gen)
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
// questions (the workspace-trust question, then Options.Questions) is on
// the screen, text its last ScreenTail. The session needs input, with the
// words of the question drawn last in that tail (one that keeps what came
// before, lastStartup) and its answers, unless it waits already (on
// anything but a startup question answered, or one the screen has left for
// this one). A look
// that took its text before a startup question was answered (Reset does
// not wait for one under way) saw the answered screen, and asks nothing:
// asked again, the question would take a second answer, which the next
// would get (Codex's update question after its background-server one,
// where 1 runs npm install). That is weighed under the lock that sets the
// question (startupLook).
func (s *Local) fireTrust(text string) {
	if q, words := s.lastStartupQ(text); q != nil {
		if s.heldGone(words) {
			// An Escape typed at it before it was asked took it away.
			return
		}
		s.setAttentionLooked(AttentionNeedsInput, words, SourceTrust, KindPrompt, CleanOptions(q.Answers), unlessWaiting|startupLook, anyTurn, s.trust.LookedAt())
		// One held answered that the screen no longer shows (the program
		// took the Escape) is cleared.
		s.escGone()
	}
}

// fireAsked is the fire of the watcher for the questions the agent asks
// during its work (Options.Questions marked Anytime): one is on the screen,
// text the screen's (Screen.Text). The session needs input with source
// "question", the words of the first question found and its answers. It
// takes the place of whatever the session shows but a startup question
// (unlessStartup): the bell that rang for the dialog, an OSC notification,
// a report, with choices of its own or none, a done, its own question with
// other words (the next of a queue). The dialog on the screen is what the
// program waits on now, and its choices are the only safe answer: typed
// text, or another question's choice, would reach the dialog's keys. A
// dialog with other words than the one last answered is that one gone
// (dialogAnswered); one with its words is that question still (sameDialog).
// The question it takes the place of, answered by a key typed while it
// showed that did not clear it (an Escape, which may have been read with
// what came next), is answered by that key, as when it goes (missAsked).
func (s *Local) fireAsked(text string) {
	s.escGone()
	// The look's own (Look): the width text was read at, and whether it
	// is a whole frame. Half a frame (a synchronized update under way, or
	// one drawn at another size not yet erased and drawn again) shows what
	// the program has not finished drawing: it asks, and forgets no dialog
	// answered. Output since the look (the frame's end, a resize) does not
	// change that: the look decides on the screen it saw.
	look, settled, cols := s.asked.Look()
	for _, q := range s.askedQuestions {
		if words := atWidth(q.words(text), cols); words != "" {
			s.mu.Lock()
			if settled && !sameWords(s.dialogAnswered, words) {
				s.dialogGone(look)
			}
			cur, typed := s.info.Attention, s.typedAtAsked
			gone := settled && cur.State == AttentionNeedsInput && cur.Source == SourceQuestion && typed != nil && cur.Since != nil && sameWords(typed.words, cur.words()) && !sameWords(words, cur.words())
			s.mu.Unlock()
			if gone {
				s.answer(cur.Since, typed.by, typed.name, true, true)
			}
			s.setAttention(AttentionNeedsInput, words, SourceQuestion, KindPermission, CleanOptions(q.Answers), unlessStartup|sameDialog)
			return
		}
	}
}

// missAsked is the same watcher's look at a quiet screen that shows none of
// the questions: the dialog last answered has gone (dialogAnswered). A
// question asked during the work that is still held then gives way, as
// answered, when a person typed into the session while a question with the
// same words showed (asked again, as after a key the program was slow to
// answer, it is the same dialog): the dialog went with a key Conductor does
// not offer (d, a click) or one it did that the program took late. Without
// such a key it stays: the dialog cannot have been answered, and a held
// question keeps every Enter from the screen.
func (s *Local) missAsked(string) {
	s.escGone()
	look, settled, _ := s.asked.Look()
	if !settled {
		// Half a frame (fireAsked), the look's own: the dialog answered
		// may be drawn again in the rest of it.
		return
	}
	s.mu.Lock()
	s.dialogGone(look)
	att, typed := s.info.Attention, s.typedAtAsked
	gone := att.State == AttentionNeedsInput && att.Source == SourceQuestion && typed != nil && att.Since != nil && sameWords(typed.words, att.words())
	s.mu.Unlock()
	if gone {
		s.answer(att.Since, typed.by, typed.name, true, true)
	}
}

// heldSource reports whether a prompt of source src is a question the
// session holds: the agent's trust or other startup question, or a
// question it asks during its work. Typed text is refused while one shows
// (Submit), and only its choice or an Enter answers it (Input).
func heldSource(src string) bool { return src == SourceTrust || src == SourceQuestion }

// matchNothing is a pattern no text matches.
var matchNothing = regexp.MustCompile(`[^\s\S]`)

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
	// matches nothing, the words are what Pattern matched last: a text that
	// keeps what came before (the startup watcher's ScreenTail) shows the
	// question drawn last there.
	Words   *regexp.Regexp
	Answers []Option
}

// words returns the question's words when text shows it, "" when it does
// not: as rows (row breaks kept, each row trimmed, blank rows left out),
// whole. A dialog asked during the work (Anytime), read off the screen,
// keeps the white space within its rows as drawn (a command's own: two
// spaces in a quoted path are not one); a startup question's runs of white
// space are one space each, as the output its watcher keeps reads them.
// The rows are what tells one question from the next (sameWords: a dialog
// answered, asked again; a startup question still on the screen); the
// message shown is cut from them (shownWords).
func (q StartupQuestion) words(text string) string {
	all := q.Pattern.FindAllString(text, -1)
	if len(all) == 0 || all[len(all)-1] == "" {
		return ""
	}
	words := all[len(all)-1]
	if q.Words != nil {
		if all := q.Words.FindAllStringSubmatch(text, -1); len(all) > 0 {
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
			if len(parts) > 0 {
				words = strings.Join(parts, " ")
			}
		}
	}
	return asRows(words, q.Anytime)
}

// asRows is text as a question's rows (words): each row trimmed, blank rows
// left out, the rows joined by line breaks; within a row its white space as
// it is with keep, else each run of it one space.
func asRows(text string, keep bool) string {
	var rows []string
	for _, row := range strings.Split(text, "\n") {
		if keep {
			row = strings.TrimSpace(row)
		} else {
			row = strings.Join(strings.Fields(row), " ")
		}
		if row != "" {
			rows = append(rows, row)
		}
	}
	return strings.Join(rows, "\n")
}

// cleanRows is m without its control characters but line breaks and tabs,
// as CleanMessage, and not cut.
func cleanRows(m string) string {
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			return -1
		}
		return r
	}, m)
}

// shownWords is the message a question's rows show as: every run of white
// space one space, and words longer than a message can be cut on a
// character boundary with an ellipsis at their end, so that a long command
// reads as cut rather than as all there is.
func shownWords(rows string) string {
	_, rows = widthOf(rows)
	words := strings.Join(strings.Fields(rows), " ")
	const more = "…"
	if len(words) > MaxAttentionMessage {
		cut := MaxAttentionMessage - len(more)
		for cut > 0 && !utf8.RuneStart(words[cut]) {
			cut--
		}
		words = words[:cut] + more
	}
	return words
}

// sameWords reports whether a and b, questions' words as rows (words), are
// the words of one question: the same but where a row break in one stands
// where the other has a space, or nothing (the screen's width broke the
// rows there: between two words, or inside one the program cut at the
// edge). Elsewhere their white space is the same, run for run: a space in
// one and none in the other ("build output" and "buildoutput", each on one
// row), or one space and two ("build output" and "build  output"), is two
// questions.
func sameWords(a, b string) bool {
	if a == b {
		return true
	}
	ca, a := widthOf(a)
	cb, b := widthOf(b)
	if ca != 0 && ca == cb {
		// Read at one width, its rows broken where the program breaks
		// them: a row break is the program's own (a line break in a
		// command), and the words are the same row for row.
		return a == b
	}
	ta, ga, ba := wordMarks(a)
	tb, gb, bb := wordMarks(b)
	if ta != tb {
		return false
	}
	for i := range ga {
		if ga[i] != gb[i] && !ba[i] && !bb[i] {
			return false
		}
	}
	return true
}

// wordMarks returns words without their white space, and for each place in
// that (before the byte at its index, its end included), the white space
// words has there within a row ("" for none), and whether a row break.
func wordMarks(words string) (string, []string, []bool) {
	var b strings.Builder
	gap, brk := []string{""}, []bool{false}
	for _, r := range words {
		at := b.Len()
		switch r {
		case '\n':
			brk[at] = true
		case ' ', '\t':
			gap[at] += string(r)
		default:
			b.WriteRune(r)
			for len(gap) <= b.Len() {
				gap, brk = append(gap, ""), append(brk, false)
			}
		}
	}
	return b.String(), gap, brk
}

// widthMark begins and ends the width a dialog's words were read at
// (atWidth): a character of Unicode's private use area, which a screen's
// words do not begin with.
const widthMark = "\ue000"

// atWidth is a dialog's words (StartupQuestion.words) read off a screen
// cols wide, with that width before them (widthOf), which tells whether a
// row break is the program's own (sameWords); "" for none.
func atWidth(words string, cols int) string {
	if words == "" || cols <= 0 {
		return words
	}
	return widthMark + strconv.Itoa(cols) + widthMark + words
}

// widthOf returns the width words were read at (atWidth), 0 for none, and
// the words without it.
func widthOf(words string) (int, string) {
	rest, ok := strings.CutPrefix(words, widthMark)
	if !ok {
		return 0, words
	}
	num, after, ok := strings.Cut(rest, widthMark)
	if !ok {
		return 0, words
	}
	cols, err := strconv.Atoi(num)
	if err != nil || cols <= 0 {
		return 0, words
	}
	return cols, after
}

// words returns the question's words a's are, as rows (StartupQuestion.words):
// those it was asked with, or its message.
func (a Attention) words() string {
	if a.rows != "" {
		return a.rows
	}
	return a.Message
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
type attentionRules uint16

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
	// unlessTurnEnded changes nothing when the session shows done and no
	// Enter has been written since (turnEnded): Codex rings the bell it is
	// asked for as its turn ends, after or before its notify reports done,
	// and the turn that ended asks nothing more. The next Enter, a person's
	// or a submission's, starts the next turn, whose bell is a question
	// again; so does any change of state (a working report, a question).
	unlessTurnEnded
	// unlessStartup changes nothing while the session holds a startup
	// question (source trust): the program shows that one.
	unlessStartup
	// bellPaced changes nothing within bellPace of the last change the
	// bell or an OSC notification made (lastBell), and stamps the change
	// it applies as the last: a burst of bells makes at most one change
	// per bellPace. Only a change is stamped: a bell that changes nothing
	// (its ended turn's own, or one at a wait that stays) has nothing to
	// pace, and must not keep the next turn's bell from asking.
	bellPaced
	// whileHeld changes nothing unless the session's turn is held for a
	// typed text (held) and the session has not ended: the hold's own
	// attention, which keys that ended the hold before it showed, or the
	// session's end, must not leave behind.
	whileHeld
	// sameDialog (a dialog asked during the work, fireAsked): a dialog with
	// the words of the one a write was last made to answer
	// (dialogAnswered), asked before the screen's watcher has seen that one
	// go or another dialog take its place, is that question still: the
	// program may not have taken the key yet (it is slow, or draws only
	// what leaves the dialog as it was). It is asked answered, with an id
	// no reply is taken for, so that no reply to it is written twice.
	sameDialog
	// startupLook (fireTrust): the change comes from a look of the startup
	// questions' watcher that took its text when it had seen look Feeds;
	// one no later than a startup answer (startupAnsweredAt) saw the
	// answered screen, and changes nothing. Weighed under the lock that
	// sets the change, so an answer that comes between the look and the
	// set is not overtaken.
	startupLook
	// unlessHeld changes nothing, shown or recorded, while the session's
	// turn is held for a typed text (held): the hold records every change
	// it hides as an entry, and a title comes ten times a second.
	unlessHeld
	// unlessOnScreen changes nothing while the session shows a question it
	// holds (heldSource: a startup question, a dialog on the screen): the
	// screen decides those.
	unlessOnScreen
	// unlessFresh changes nothing while the wait showing is one a title may
	// settle whose Options.TitleSettle has not passed (settleID, settleAt):
	// the settle timer decides it. A frame drawn between an approval's bell
	// and its dialog does not clear the bell.
	unlessFresh
	// unlessWorking changes nothing while the session shows working,
	// whatever reported it.
	unlessWorking
	// unlessEnded changes nothing once the session has ended.
	unlessEnded
	// onlySettling (the settle timer, settled) changes nothing unless the
	// timer of generation look (settleGen) is still armed, for the wait
	// the session still shows (settleID), and the last title read still
	// says the agent works (titleWorking).
	onlySettling
)

// setAttention is SetAttentionFull, applied as rules say. The token, the look
// at the current state and the change share one critical section. The
// attention entry of an applied change is recorded without asking the bucket
// (recordOwn), stamped with the change's Since, taken in that section: whoever
// sees the new state sees it no earlier than the entry's At, however late the
// entry reaches OnActivity. An invalid state changes nothing and spends
// nothing.
func (s *Local) setAttention(state AttentionState, message, source, kind string, options []Option, rules attentionRules) error {
	return s.setAttentionRead(state, message, source, kind, options, rules, anyTurn)
}

// anyTurn is the turn of a change that is not output read (setAttentionRead).
const anyTurn = ^uint64(0)

// setAttentionRead is setAttention for what output read during turn (the
// count of turns begun after a done as it was read, Local.turn) says: it
// changes nothing once another turn has begun, the turn that ended asking
// nothing more. anyTurn is setAttention's.
func (s *Local) setAttentionRead(state AttentionState, message, source, kind string, options []Option, rules attentionRules, turn uint64) error {
	return s.setAttentionLooked(state, message, source, kind, options, rules, turn, 0)
}

// setAttentionLooked is setAttentionRead for a change a look of the startup
// questions' watcher found (startupLook): look is the Feeds that look's text
// had seen (PatternWatcher.LookedAt); for the settle timer's change
// (onlySettling), the timer's generation.
func (s *Local) setAttentionLooked(state AttentionState, message, source, kind string, options []Option, rules attentionRules, turn, look uint64) error {
	if !state.Valid() {
		return nil
	}
	// A question asked from the screen comes with its rows (a startup
	// question, a dialog: StartupQuestion.words), which tell it from the
	// next, and shows as a message cut from them.
	var rows string
	if heldSource(source) && state == AttentionNeedsInput {
		rows = asRows(cleanRows(message), true)
		message = shownWords(rows)
	}
	message = CleanMessage(message)
	words := rows
	if words == "" {
		words = message
	}
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
	if rules&whileHeld != 0 && (s.held == nil || s.info.Status.Ended() ||
		s.info.Attention.State == AttentionNeedsInput && heldSource(s.info.Attention.Source)) {
		// The hold shows only while it stands, and never in place of a
		// question on the screen: that one is what the program waits on,
		// its words and choices what a person needs (answer shows the hold
		// once it is answered, if the hold still stands).
		s.mu.Unlock()
		return nil
	}
	cur := s.info.Attention
	// The hold's words give way to a question drawn on the screen (a
	// startup question's unlessWaiting): the program waits on that one now.
	waiting := cur.State == AttentionNeedsInput && !(cur.Source == SourceSubmit && heldSource(source))
	answerable := waiting && (len(cur.Options) > 0 || heldSource(cur.Source))
	// A report from outside that the session waits, with choices or
	// without, does not hide a question asked during the work: the dialog
	// on the screen is what answers go to (a hook that reports the same
	// dialog late, say, or another question whose choices would land on
	// it).
	keepAsked := rules&spendToken != 0 && state == AttentionNeedsInput && waiting && cur.Source == SourceQuestion
	turnsOwn := rules&unlessTurnEnded != 0 && cur.State == AttentionDone && s.turnEnded
	startup := rules&unlessStartup != 0 && waiting && cur.Source == SourceTrust
	paced := rules&bellPaced != 0 && !s.lastBell.IsZero() && time.Since(s.lastBell) < bellPace
	lastTurns := turn != anyTurn && turn != s.turn.Load()
	staleLook := rules&startupLook != 0 && s.startupAnswered && look <= s.startupAnsweredAt
	// A window title's (titleRules), and the settle timer's.
	held := rules&unlessHeld != 0 && s.held != nil
	onScreen := rules&unlessOnScreen != 0 && cur.State == AttentionNeedsInput && heldSource(cur.Source)
	fresh := rules&unlessFresh != 0 && cur.State == AttentionNeedsInput && cur.ID != "" && cur.ID == s.settleID && time.Now().Before(s.settleAt)
	working := rules&unlessWorking != 0 && cur.State == AttentionWorking
	ended := rules&unlessEnded != 0 && s.info.Status.Ended()
	notSettling := rules&onlySettling != 0 && (s.settleTimer == nil || s.settleGen != look || !s.titleWorking ||
		cur.State != AttentionNeedsInput || cur.ID == "" || cur.ID != s.settleID)
	// A startup question answered and still shown (its write not made yet,
	// or the session slow to clear it) waits on nothing: the next startup
	// question takes its place.
	answeredStartup := rules&startupLook != 0 && waiting && cur.Source == SourceTrust && cur.ID != "" && cur.ID == s.answered
	// A startup question still waiting that the screen has left for
	// another (drawn after it, lastStartupQ: the program took an answer not
	// read as one) waits on nothing either: the question the screen shows
	// takes its place, with an id of its own, and a reply naming the one
	// left is refused.
	movedOn := rules&startupLook != 0 && waiting && cur.Source == SourceTrust && state == AttentionNeedsInput && source == SourceTrust && !sameWords(words, cur.words())
	if (rules&unlessWaiting != 0 && waiting && !answeredStartup && !movedOn) || (rules&unlessChoices != 0 && answerable) || keepAsked || turnsOwn || startup || paced || lastTurns || staleLook ||
		held || onScreen || fresh || working || ended || notSettling {
		s.mu.Unlock()
		return nil
	}
	if s.held != nil && source != SourceSubmit && !heldSource(source) {
		// The session's turn is held for a typed text (holdTyped): the hold
		// is what shows, until a person types into the terminal, but for a
		// question on the screen, whose answer ends the hold too. Another
		// change, past the rules above (the bell's pace among them), is
		// recorded, as the report it is, and not shown; its turn and pace
		// are kept as a change shown keeps them.
		if state == AttentionDone {
			s.turnEnded = true
		} else {
			s.beginTurn()
		}
		if rules&bellPaced != 0 {
			s.lastBell = time.Now()
		}
		// The agent reports another state (at work, done, another
		// question): the question the hold took the place of waits no
		// longer, and does not show again as the hold ends.
		if o := s.heldOver; o.ID != "" && !(state == o.State && message == o.Message && source == o.Source && kind == o.Kind && sameOptions(options, o.Options)) {
			s.heldOver = Attention{}
		}
		s.mu.Unlock()
		if state != AttentionNone {
			label := message
			if label == "" {
				label = string(state)
			}
			s.recordOwn(ActivityEntry{At: time.Now().UTC(), Type: ActivityAttention, Message: label}, state)
		}
		return nil
	}
	if state == AttentionDone {
		// Also a done that changes nothing shown (the same words as the
		// last turn's): the turn it reports has ended.
		s.turnEnded = true
	}
	// The dialog last answered, asked before it has gone (sameDialog): its
	// question is answered, whatever id it shows with.
	stillAnswered := rules&sameDialog != 0 && state == AttentionNeedsInput && s.dialogAnswered != "" && sameWords(s.dialogAnswered, words)
	// The startup question last answered, asked again before another
	// startup question took its place (the program draws it again before
	// it moves on), is asked answered too; another startup question asked
	// ends that.
	// The startup question an Escape a write ended with may have
	// answered before it was asked (heldAnswer's words): asked answered,
	// and held so until the screen shows it gone (escGone).
	heldAsked, startupStill := false, false
	if rules&startupLook != 0 && state == AttentionNeedsInput {
		if s.startupAnsweredWords != "" && sameWords(s.startupAnsweredWords, words) {
			stillAnswered, startupStill = true, true
		} else {
			s.startupAnsweredWords = ""
		}
		if s.heldAnswer.words != "" {
			if sameWords(s.heldAnswer.words, words) {
				stillAnswered, heldAsked = true, true
			} else {
				s.heldAnswer.words = ""
			}
		}
	}
	if cur.State == state && sameWords(cur.words(), words) && cur.Source == source && cur.Kind == kind && sameOptions(cur.Options, options) {
		if rows != "" && rows != cur.rows {
			// The same question read at another width (the window
			// resized, its rows broken elsewhere): it is told by its rows
			// as they are now, so that at this width a row break is the
			// program's own against the next question (sameWords), and
			// so is the dialog last answered, when it is this one.
			s.info.Attention.rows = rows
			if rules&sameDialog != 0 && s.dialogAnswered != "" && sameWords(s.dialogAnswered, words) {
				s.dialogAnswered = words
			}
		}
		if stillAnswered && cur.ID != "" {
			s.answered = cur.ID
			s.holdAskedAgain(cur.ID, words, heldAsked, startupStill)
		}
		s.mu.Unlock()
		return nil
	}
	if state != AttentionDone {
		// Another state after a done (a working report: the agent went on
		// by itself, a question, a clear) begins the next turn, as an
		// Enter does.
		s.beginTurn()
	}
	if rules&bellPaced != 0 {
		s.lastBell = time.Now()
	}
	att := Attention{State: state, Message: message, Source: source, Kind: kind, Options: options, rows: rows}
	if source == SourceSubmit {
		// The hold of the turn shows (holdTyped): the question it takes the
		// place of, one the agent asked and not a question on the screen
		// (which shows over the hold), shows again when a person's keys
		// end the hold without answering it (endHoldTyped).
		s.heldOver = Attention{}
		if cur.State == AttentionNeedsInput && cur.ID != "" && cur.Source != SourceSubmit && !heldSource(cur.Source) {
			s.heldOver = cur
		}
	}
	if state != AttentionNone {
		now := time.Now().UTC()
		att.Since = &now
		att.ID = NewID()
		if stillAnswered {
			s.answered = att.ID
			s.holdAskedAgain(att.ID, words, heldAsked, startupStill)
		}
	}
	s.info.Attention = att
	s.settleLocked(att)
	s.signalAttention()
	s.hub.Broadcast(proto.MustControl(attentionMessage(att)))
	// The agent's question goes into the chat too (design: the agents' questions in the chat);
	// the session's own hold is no question of the agent's.
	var asked ChatMessage
	var askedID string
	if state == AttentionNeedsInput && source != SourceSubmit {
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
	m := proto.Attention{T: proto.CtlAttention, State: string(att.State), Message: att.Message, Source: att.Source, Kind: att.Kind, ID: att.ID}
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
				s.sized(cols, rows)
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
	sub.authorize = o.Authorize
	if sized {
		sub.want = [2]uint16{cols, rows}
	}
	room := s.opts.RunChat
	offerNvim := nvimAvailable() && !s.nvimOwnerOnly(sub)
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
		Nvim:            offerNvim,
		NvimOwnerOnly:   s.nvimOwnerOnly(sub),
		NvimSettled:     offerNvim,
		Chat:            true,
		RunChat:         room != nil,
		Answer:          true,
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
	// run's chat follows the session's. The room posts without this lock: its
	// replay and the hub's add are made under the room's (attach), so a run
	// message posted meanwhile reaches the viewer in the replay or live, or
	// both, which clients take once by id; never neither.
	for _, f := range chatReplayFrames(proto.ChatScopeSession, s.chat.snapshot()) {
		sub.send(f)
	}
	if room != nil {
		room.attach(sub, s.hub)
	} else {
		s.hub.add(sub)
	}
	if replacing {
		// Its slot is this client's now, for every later check too.
		if s.passedOn == nil {
			s.passedOn = map[*Subscription]struct{}{}
		}
		s.passedOn[r] = struct{}{}
	}
	// A credential that lapses closes the client as it lapses, one that
	// attached a moment before included (at once when that moment has
	// passed); one closed before (the session's end) is written nothing
	// more from then on (Subscription.expire), until its transport closes.
	var lapse *time.Timer
	if !until.IsZero() {
		lapse = time.AfterFunc(time.Until(until), sub.expire)
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
		s.Detach(sub)
		// Detach keeps it among the closing until its transport has closed.
		<-sub.closed
		if lapse != nil {
			lapse.Stop()
		}
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
// position query), which is written and answers nothing. While a question
// asked during the work shows, a write of several keys with an Enter that
// is not one of its choices is refused with ErrQuestion, nothing written.
// They are written in their turn among the session's writes (input.go), and
// Input waits for that at most Options.InputWait: a program that takes
// nothing meanwhile gets ErrInputStalled, the keystrokes written later if
// the write had begun (see writeProc) and never otherwise. Keystrokes not
// yet written when sub ends (revoked, detached), or once its credential no
// longer stands (AttachOptions.Authorize: ErrRevoked, ErrExpired, or a kind
// of either), are never written: refused with that error, asked as they are
// queued and again as the writer takes them. A viewer with no credential
// (one of the owner's windows, a host's own terminal) is never refused so.
// Keys written (a terminal's own report aside) end the hold of the session's
// turn for a submission's text typed without its Enter (holdTyped). data
// may be reused once Input returns.
func (s *Local) Input(sub *Subscription, data []byte) error {
	return s.input(context.Background(), sub, "", data, writeWait{wait: s.opts.InputWait, stall: s.opts.InputWait})
}

// AnswerInput is Input for a reply to the question named question (its
// Attention.ID, as the person was shown it: a choice's keys clicked or
// pressed), waiting also until ctx ends. The keys are written only while
// that question is the one waiting and no write before them was made to
// answer it, which is looked at as they are queued and again, together
// with noting them as its answer, as the writer takes them; otherwise
// nothing is written and it returns ErrQuestionAnswered. Of two replies to
// one question the first is written and the second refused, so that a late
// click never lands on the question that came next.
func (s *Local) AnswerInput(ctx context.Context, sub *Subscription, question string, data []byte) error {
	return s.input(ctx, sub, question, data, writeWait{wait: s.opts.InputWait, stall: s.opts.InputWait})
}

// InputContext is Input waiting until ctx ends in place of
// Options.InputWait (ctx's error then), and refused at once, as Input is,
// while the program is known not to read: a host hands a viewer's
// keystrokes on from its frame loop and gives each its time from its
// arrival.
func (s *Local) InputContext(ctx context.Context, sub *Subscription, data []byte) error {
	return s.input(ctx, sub, "", data, writeWait{stall: s.opts.InputWait})
}

// InputWaiting is InputContext for a terminal of the person's own (a
// host's): it waits for the program to read, however long it has not, as
// a terminal does, until ctx ends.
func (s *Local) InputWaiting(ctx context.Context, sub *Subscription, data []byte) error {
	return s.input(ctx, sub, "", data, writeWait{})
}

// input is Input waiting as o says, until ctx ends; with question, the
// keys are a reply to that question (AnswerInput).
func (s *Local) input(ctx context.Context, sub *Subscription, question string, data []byte, o writeWait) error {
	if sub.Role != RoleControl {
		return ErrReadOnly
	}
	// The keys are this call's own: what runs as they are written, or once
	// they land late, reads them after Input has returned, and the caller
	// may reuse its buffer then.
	data = bytes.Clone(data)
	if question != "" {
		// A reply to a question answered or gone is refused before
		// anything else is looked at: nothing of it is written.
		if err := s.questionWaits(question); err != nil {
			return err
		}
		if err := s.stillOnScreen(question); err != nil {
			return err
		}
	}
	if s.asked != nil && len(data) > 1 && bytes.IndexByte(data, '\r') >= 0 && !isTerminalReport(data) {
		// Several keys with an Enter (a page's choice, a paste): look at the
		// screen first, so that a dialog drawn and not yet seen quiet is the
		// question they are weighed against, not the one it replaced. Right
		// after a key that answered a dialog the screen is not looked at yet
		// (Hold): they wait for the look, refused meanwhile, since the key
		// may have done nothing and left the dialog.
		if s.asked.Held() {
			return ErrQuestion
		}
		s.asked.CheckNow()
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
	// A question asked during the work is a dialog's numbered list: a digit
	// picks a line at once and Ctrl+C cancels it (Codex 0.161's approval
	// overlay), whether the choices offer those keys or not. A key that
	// leaves the dialog where it was is answered all the same; the screen,
	// looked at again, asks it again.
	if s.info.Attention.State == AttentionNeedsInput && s.info.Attention.Source == SourceQuestion && listKey(data) {
		choice = true
	}
	enter := choice || bytes.IndexByte(data, '\r') >= 0
	// The screen that showed a startup question is behind once it is
	// answered: only a new question counts. It is cleared before the answer
	// is written, never after: a program that draws its next question as the
	// first is answered (Codex's update question after its background-server
	// one) can have drawn it before the write returns, and a clear after it
	// took that question with it, unseen (round 14, the trust spec's flake).
	answersQuestion := enter && s.info.Attention.State == AttentionNeedsInput && heldSource(s.info.Attention.Source) && s.info.Attention.Since == promptSince
	// More than one key with an Enter among them, while a question asked
	// during the work shows, is no key of its dialog: a choice of the
	// question it took the place of (a page's button clicked late), or a
	// paste. Its Enter would pick the dialog's highlighted line.
	stale := s.info.Attention.State == AttentionNeedsInput && s.info.Attention.Source == SourceQuestion && !choice && len(data) > 1 && bytes.IndexByte(data, '\r') >= 0 && !isTerminalReport(data)
	s.mu.Unlock()
	if ended {
		return ErrSessionEnded
	}
	if stale {
		return ErrQuestion
	}
	// What a key does to the session's questions is done as the writer
	// takes it, just before it is written, never for keys not written (a
	// time run out, a stall, a connection gone): noted is set then, and
	// read once the write is made.
	var noted bool
	// Set as the writer takes the keys, as keysWritten reads them (a paste
	// or an escape sequence a write before left unfinished carried in):
	// typedEnter says they answer the prompt they were typed at as answer
	// takes it (enter, when the write is its own keys, else read from its
	// keys: a paste's text is none); quiet that they are no key a person
	// typed (the rest of a terminal's report, or of a sequence not ended
	// yet), which answer nothing and stamp nobody typing; startupSince
	// stamps the startup question showing then that the keys answer
	// (whatever prompt they were typed at), cleared once they are written.
	//
	// An Escape that ends a write is held (keysWritten): it may be the
	// Escape key and may be the start of what the next write goes on with
	// (a report's rest, a key read with it as Alt and that key), and
	// nothing tells which. It counts for what puts a question behind (a
	// reply to it refused, the safer mistake), never for what clears one,
	// puts its screen behind or keeps it from being asked: a startup
	// question it may answer is held answered (heldAnswer) until
	// the screen shows it gone (escGone), a key surely answers it, or the
	// next startup question takes its place, so that no submission's Enter
	// reaches a question that still shows.
	typedEnter := enter
	var quiet bool
	var startupSince *time.Time
	// Keys are withdrawn as the connection ends, and, as a submission is,
	// once its credential no longer stands (a link's: revoked or expired,
	// or on a host its server not heard from lately), asked as they are
	// queued and again as the writer takes them (writeWait.from): a write
	// begun before lands as the program reads, and none after it begins.
	o.from, o.anyEnd = sub, true
	// Whether a person typed anything here, or only the terminal reported
	// (its replies, the pointer moved or clicked), as many reports as the
	// write holds, one the connection's last write began included.
	typed := sub.typesSomething(data)
	if s.asked != nil && len(data) > 1 && bytes.IndexByte(data, '\r') >= 0 && !isTerminalReport(data) {
		// Several keys with an Enter, looked at again as the writer takes
		// them (they may have waited behind another write): a key that
		// answered a dialog not looked after yet, or a dialog on the screen
		// now whose choices they are not, refuses them (ErrQuestion), as
		// before they were queued.
		o.guard = func() error {
			if s.asked.Held() {
				return ErrQuestion
			}
			s.asked.CheckNow()
			s.mu.Lock()
			att := s.info.Attention
			s.mu.Unlock()
			if att.State == AttentionNeedsInput && att.Source == SourceQuestion && !isChoice(att, data) {
				return ErrQuestion
			}
			return nil
		}
	}
	if question != "" {
		// A reply to a question, looked at again as the writer takes it
		// (other writes may have gone before it): still the question
		// waiting, and noted as its answer in the same critical section,
		// so that a reply to it queued behind is refused although the
		// question shows until this one is written and answers it.
		dialog := o.guard
		o.guard = func() error {
			if err := s.questionWaits(question); err != nil {
				return err
			}
			if dialog != nil {
				if err := dialog(); err != nil {
					return err
				}
			}
			if err := s.stillOnScreen(question); err != nil {
				return err
			}
			return s.claimQuestion(question)
		}
	}
	o.before = func() {
		// The keys the program reads from the write, a paste or an escape
		// sequence a write before it left unfinished carried in.
		keys, unkeyed, extra := s.keysWritten(data)
		// The keys that may answer: keys, and those the write may hold
		// besides (the Escape held, an Alt chord's key alone). A write
		// with those is never matched whole: Alt and Enter is no Enter.
		held := len(extra) > 0
		maybe := keys
		if held {
			maybe = append(keys[:len(keys):len(keys)], extra...)
		}
		if typed && question == "" {
			// A person types into the terminal: a submission's text typed
			// without its Enter no longer holds the session's turn
			// (holdTyped), and what comes next is written after these keys.
			// The terminal's reports, the pointer's among them, type nothing.
			// A reply's choice (an answer to a question on the screen over
			// the hold) is no person at the terminal: the text still waits
			// there, and the hold shows again once the question is
			// answered (answer). Keys that answer nothing (Ctrl+U, the
			// text killed) leave the question the hold took the place of
			// waiting still: it shows again (endHoldTyped).
			s.endHoldTyped(data, maybe)
		}
		own := ownKeys(data, keys) && !held
		quiet = unkeyed || allReports(keys) && !held
		s.mu.Lock()
		shown := s.info.Attention
		s.mu.Unlock()
		if !own {
			typedEnter = anyKey(keys, func(k []byte) bool {
				return string(k) == "\r" || isChoice(shown, k) || shown.Source == SourceQuestion && listKey(k)
			})
		}
		if quiet {
			typedEnter = false
		}
		// The dialog the screen the dialogs are read from shows as keys
		// are written that may answer one (any key but an escape sequence:
		// a paste alone, an arrow alone, do not read it).
		var words string
		var onScreen *dialogView
		if s.asked != nil && mayAnswerDialog(maybe) && !isTerminalReport(data) {
			screen, cols := s.asked.TextAt()
			var q *StartupQuestion
			if q, words = s.askedIn(screen, cols); q != nil {
				onScreen = readDialog(q, words, screen)
			}
		}
		// A startup question a key surely answers is put behind, its screen
		// too, and cleared once written (startupSince); one only the Escape
		// held may answer is held answered (heldAnswer).
		startupAnswer, heldAnswer := false, false
		// The startup question the keys reach is the one drawn last: the
		// one the session shows, unless another has been drawn since and
		// not asked yet, which they reach instead (shownDrawn false): a
		// choice of the older one is no answer to it.
		shownDrawn := true
		if s.trust != nil && shown.State == AttentionNeedsInput && shown.Source == SourceTrust && s.trust.Watching() {
			if _, drawn := s.lastStartupQ(s.trust.Text()); drawn != "" && !sameWords(drawn, shown.words()) {
				shownDrawn = false
			}
		}
		ownAnswer := answersQuestion && own && shownDrawn
		if !shownDrawn {
			// Nor is it cleared by them: it gives way to the one drawn
			// once that is asked, the session held meanwhile.
			typedEnter = false
		}
		if !isTerminalReport(data) && !quiet {
			// Keys that answer the question showing, or the dialog on the
			// screen, put it behind for the replies queued after them.
			s.noteAnswering(promptSince, data, maybe, onScreen)
			// The startup question showing as the keys are written, when
			// they pick one of its answers (picksStartup: a digit of its
			// items it offers no reply for too; one key of several, an
			// arrow then its digit; keys typed before it showed) is answered as
			// its choice alone answers it: its screen behind now, the
			// question cleared once written.
			if shownDrawn && shown.State == AttentionNeedsInput && shown.Source == SourceTrust && s.startupPicks(shown, data, maybe) {
				if s.startupPicks(shown, data, keys) {
					startupAnswer, startupSince = true, shown.Since
				} else {
					heldAnswer = true
				}
			}
		}
		// The startup question the screen shows as the keys are written,
		// asked or not yet (the watcher asks once the screen is quiet): keys
		// that pick one of its answers (picksStartup) answer it, and its screen goes
		// behind, so that the watcher asks only what is drawn after.
		startupOnScreen, onScreenHeld := false, false
		var startupWords string
		if s.trust != nil && !quiet && !isTerminalReport(data) && s.trust.Watching() {
			text := s.trust.Text()
			if q, words := s.lastStartupQ(text); q != nil && picksStartup(CleanOptions(q.Answers), startupItems(q, text), data, maybe) {
				startupWords = words
				if picksStartup(CleanOptions(q.Answers), startupItems(q, text), data, keys) {
					startupOnScreen = true
				} else {
					onScreenHeld = true
				}
			}
		}
		if startupOnScreen && !startupAnswer && !ownAnswer {
			s.mu.Lock()
			att := s.info.Attention
			asked := att.State == AttentionNeedsInput && att.Source == SourceTrust && att.ID != "" && sameWords(att.words(), startupWords)
			s.mu.Unlock()
			if !asked {
				// Drawn and not asked yet (the watcher asks once the screen
				// is quiet): the program may not have read the key, for
				// Codex discards what came before its question's screen took
				// input. It is held answered by its words and asked so,
				// nothing put behind, until the screen shows it gone or the
				// next takes its place. A question asked has had its screen
				// quiet since it was drawn, the discard long done.
				startupOnScreen, onScreenHeld = false, true
			}
		}
		if (heldAnswer || onScreenHeld) && !startupAnswer && !startupOnScreen && !ownAnswer {
			// Only the Escape held may answer it: the startup question
			// showing (or, not asked yet, the one the screen shows, by its
			// words) is held answered, nothing else done.
			s.holdStartupAnswered(startupWords, sub)
		}
		if (ownAnswer || startupAnswer || startupOnScreen) && s.trust != nil {
			// The answered screen behind, a look that took its text before
			// now asking nothing as it fires (fireTrust), and the question
			// answered, asked again before another takes its place, asked
			// answered. The question the session shows now is read again
			// in the same step: one asked since the keys were looked at
			// (shown) is the one they answer, noted and cleared once
			// written.
			s.mu.Lock()
			s.startupAnswered, s.startupAnsweredAt = true, s.trust.Fed()
			if s.asked != nil {
				s.startupAnsweredFed = s.asked.Fed()
			}
			if startupWords == "" && shown.Source == SourceTrust {
				startupWords = shown.words()
			}
			if startupWords != "" {
				s.startupAnsweredWords = startupWords
			}
			if att := s.info.Attention; att.State == AttentionNeedsInput && att.Source == SourceTrust && att.ID != "" && (startupWords == "" || sameWords(att.words(), startupWords)) {
				s.markAnswered(att)
				if startupSince == nil {
					startupSince = att.Since
				}
			}
			s.mu.Unlock()
			s.trust.Reset()
		}
		if typedEnter {
			// The turn a done ended is behind as the Enter is written (an
			// encoded one too).
			s.nextTurn()
		}
		answering := enter || s.askedKeys[string(data)] || listKey(data)
		if !own {
			answering = anyKey(maybe, func(k []byte) bool { return string(k) == "\r" || s.askedKeys[string(k)] || listKey(k) })
		}
		if words != "" && answering {
			// A key that answers a dialog on the screen: the look before a
			// submission does not ask the dialog again before the program
			// draws what follows (the look at the end of a quiet period
			// still does). The key is noted against the dialog, also one
			// not yet asked: the dialog asked later, before the program
			// took it away, gives way to it (missAsked).
			s.mu.Lock()
			s.typedAtAsked = &typedAt{words: words, by: sub.ID, name: sub.Name}
			s.mu.Unlock()
			s.asked.Hold()
			noted = true
		}
	}
	// written is what follows the keys once written: the prompt they were
	// typed at answered (as their keys answer it: a paste's text answers no
	// held question, the rest of a report nothing), and the startup
	// question they answered as the writer took them.
	written := func() {
		if !quiet {
			s.typedKeys(sub, data, promptSince, typedEnter, noted, typed)
		}
		if startupSince != nil {
			s.answer(startupSince, sub.ID, sub.Name, true, true)
		}
	}
	landed, err := s.writeProc(ctx, data, o)
	if err != nil {
		if landed != nil && !errors.Is(err, ErrSessionEnded) {
			// Begun: the keys land as the program reads, and what follows
			// written keys is done then (the prompt they answer still the
			// one they were typed at). At most one write is begun at a
			// time, so at most one such wait is under way.
			go func() {
				select {
				case werr := <-landed:
					if werr == nil {
						written()
					}
				case <-s.ended:
				}
			}()
		}
		return err
	}
	written()
	return nil
}

// isChoice reports whether data is the input of one of att's choices.
func isChoice(att Attention, data []byte) bool {
	for _, o := range att.Options {
		if o.Input != "" && o.Input == string(data) {
			return true
		}
	}
	return false
}

// typedKeys is what follows keys written to the program (input): sub is
// typing, the dialog or question they answer is noted and looked at again,
// and the prompt that showed as they were typed (promptSince) is answered.
// typed says a person typed something (typesSomething), not only the
// terminal's reports.
func (s *Local) typedKeys(sub *Subscription, data []byte, promptSince *time.Time, enter, noted, typed bool) {
	if isTerminalReport(data) {
		return
	}
	s.stampTyping(sub)
	if s.asked != nil {
		if !noted {
			// The dialog the screen showed as the key was written, when
			// there was one, says what it answers; the question shown is
			// only the fallback (it can be one the screen has replaced).
			s.noteTyped(promptSince, sub)
		}
		if enter || s.askedKeys[string(data)] {
			// Look at the screen again even if the program draws nothing:
			// a dialog the key did not take away is asked again.
			s.asked.Poke()
		}
	}
	if !typed {
		s.mu.Lock()
		hold := s.info.Attention.Source == SourceSubmit && s.info.Attention.Since == promptSince
		s.mu.Unlock()
		if hold {
			// Only the terminal reported (the pointer moved, say): the
			// hold's words stay with it.
			return
		}
	}
	s.answer(promptSince, sub.ID, sub.Name, true, enter)
}

// askedIn returns the question asked during the work that text (the
// screen's, read cols wide: TextAt) shows, and its words; nil and ""
// when it shows none.
func (s *Local) askedIn(text string, cols int) (*StartupQuestion, string) {
	for i := range s.askedQuestions {
		if words := s.askedQuestions[i].words(text); words != "" {
			return &s.askedQuestions[i], atWidth(words, cols)
		}
	}
	return nil, ""
}

// typedAt is who typed into the session while a question asked during the
// work showed, and that question's words.
type typedAt struct {
	words    string
	by, name string
}

// noteTyped notes sub as the person who typed into the session while the
// question asked during the work stamped promptSince was showing.
func (s *Local) noteTyped(promptSince *time.Time, sub *Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if att := s.info.Attention; promptSince != nil && att.Since == promptSince && att.Source == SourceQuestion {
		s.typedAtAsked = &typedAt{words: att.words(), by: sub.ID, name: sub.Name}
	}
}

// sized records the PTY's new size, which the screen the questions asked
// during the work are read from follows. The caller holds s.mu.
func (s *Local) sized(cols, rows uint16) {
	s.info.Cols, s.info.Rows = cols, rows
	if s.asked != nil {
		s.asked.Resize(int(cols), int(rows))
	}
}

// listKey reports whether data is a key a dialog's list takes as an answer
// of its own: a digit from 1 to 9, or Ctrl+C.
func listKey(data []byte) bool {
	return len(data) == 1 && (data[0] >= '1' && data[0] <= '9' || data[0] == 0x03)
}

// nextTurn says an Enter is about to be written: the turn a done ended is
// behind, and a bell is a question again (unlessTurnEnded).
func (s *Local) nextTurn() {
	s.mu.Lock()
	s.beginTurn()
	s.mu.Unlock()
}

// beginTurn begins the next turn after a done, whichever comes first: an
// Enter (nextTurn) or another state shown (setAttention). Nothing when no
// done ended the last one. The new turn's bells are no burst of the last
// turn's: the pace starts again (bellPaced), so that its first bell asks
// however soon it rings after one of the last turn's (Codex's can ring
// before its done, and a crew's next prompt follows the done at once).
// What was read before it is the last turn's (turn), the last window title
// read included (titleWorking): it says nothing of the next turn, whose
// waits it neither settles nor arms a timer for (readTitle). An Enter with
// no done before it in its turn (an answer, a line typed during the turn)
// keeps the pace: a burst's tail does not ask again. The caller holds
// s.mu.
func (s *Local) beginTurn() {
	if !s.turnEnded {
		return
	}
	s.turnEnded = false
	s.lastBell = time.Time{}
	s.turn.Add(1)
	s.titleWorking = false
	s.disarmSettleLocked()
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

// QuestionAnsweredWords say why a reply to a question was refused: the
// question it was shown for is no longer the one waiting, answered by
// another reply that came first or gone, and nothing of it was written.
const QuestionAnsweredWords = "this question was already answered: nothing was sent"

// ErrQuestionAnswered refuses a reply to a question that is no longer the
// one waiting (AnswerInput, Submission.Question).
var ErrQuestionAnswered = errors.New(QuestionAnsweredWords)

// questionWaits reports whether the question named id waits for a reply:
// it is the session's needs_input prompt, and no write was made to answer
// it (answered). ErrQuestionAnswered otherwise.
func (s *Local) questionWaits(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitsLocked(id)
}

// waitsLocked is questionWaits for a caller that holds s.mu.
func (s *Local) waitsLocked(id string) error {
	att := s.info.Attention
	// The session's turn held for a typed text (source submit) is no
	// question: a person at the terminal deals with it, and a reply naming
	// it writes nothing.
	if id == "" || att.State != AttentionNeedsInput || att.ID != id || s.answered == id || att.Source == SourceSubmit {
		return ErrQuestionAnswered
	}
	return nil
}

// stillOnScreen reports, for a reply to the question named id, whether the
// screen still shows that question and no other the reply's keys would
// reach: ErrQuestionAnswered when it does not. A question the session reads
// off the screen (a dialog asked during the work, a startup question) is
// still on it, and no other is drawn in its place, not asked yet (a
// watcher asks once the screen is quiet). Any other question (an agent's
// report, a bell's) has no dialog drawn over it, nor a startup question:
// the program waits on that one now, and a key for the report (its `1`)
// would answer it.
func (s *Local) stillOnScreen(id string) error {
	s.mu.Lock()
	att := s.info.Attention
	s.mu.Unlock()
	if att.ID != id {
		return nil
	}
	if s.asked != nil {
		text, cols := s.asked.TextAt()
		_, words := s.askedIn(text, cols)
		if att.Source == SourceQuestion && !sameWords(words, att.words()) || att.Source != SourceQuestion && words != "" {
			return ErrQuestionAnswered
		}
	}
	switch {
	case att.Source == SourceTrust && s.trust != nil:
		if !sameWords(s.lastStartup(s.trust.Text()), att.words()) {
			return ErrQuestionAnswered
		}
		// The output the watcher keeps holds what came before a screen
		// erased: a question the program left by a key Conductor does not
		// read as an answer (one its choices leave out) is still in it. The
		// screen as it is now, a whole frame, shows the question, or the
		// reply would reach what took its place.
		if s.asked != nil {
			if text, settled, _, _ := s.asked.Whole(); settled && !sameWords(s.lastStartup(text), att.words()) {
				return ErrQuestionAnswered
			}
		}
	case att.Source != SourceQuestion && s.trust != nil && s.trust.Watching():
		// A startup question drawn: the screen as it is now, a whole frame,
		// or else the output the watcher keeps, shows one, other than the
		// one last answered (still shown, the program slow to go on).
		text := s.trust.Text()
		if s.asked != nil {
			if whole, settled, _, _ := s.asked.Whole(); settled {
				text = whole
			}
		}
		if words := s.lastStartup(text); words != "" {
			s.mu.Lock()
			answered := sameWords(words, s.startupAnsweredWords)
			s.mu.Unlock()
			if !answered {
				return ErrQuestionAnswered
			}
		}
	}
	return nil
}

// lastStartup returns the words of the startup question drawn last in text,
// the output's tail the startup questions' watcher keeps (ScreenTail: what
// came before stays in it), "" when it shows none.
func (s *Local) lastStartup(text string) string {
	_, words := s.lastStartupQ(text)
	return words
}

// lastStartupQ is lastStartup with the question: nil and "" for none.
func (s *Local) lastStartupQ(text string) (*StartupQuestion, string) {
	var last *StartupQuestion
	words, at := "", -1
	for i := range s.questions {
		q := &s.questions[i]
		m := q.Pattern.FindAllStringIndex(text, -1)
		if len(m) == 0 {
			continue
		}
		// The question is told by where its pattern matched last; its
		// words (a heading drawn before what finds it) by the whole text.
		if start := m[len(m)-1][0]; start > at {
			last, words, at = q, q.words(text), start
		}
	}
	return last, words
}

// claimQuestion is questionWaits, and notes the question as answered in the
// same critical section: asked as the writer takes the reply's write, the
// last of its checks (writeWait.guard), so that the write is made when it
// returns nil, and a reply to the same question after it is refused.
func (s *Local) claimQuestion(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.waitsLocked(id); err != nil {
		return err
	}
	if s.inputBusy() {
		return ErrInputBusy
	}
	s.markAnswered(s.info.Attention)
	return nil
}

// markAnswered notes att's question as the one a write is being made to
// answer (answered), and a dialog asked during the work by its words
// (dialogAnswered). The caller holds s.mu.
func (s *Local) markAnswered(att Attention) {
	s.answered = att.ID
	switch att.Source {
	case SourceQuestion:
		s.noteDialogAnswered(att.words())
	case SourceTrust:
		s.startupAnsweredWords = att.words()
	}
}

// noteDialogAnswered notes the dialog of words as answered (dialogAnswered),
// as the screen is now. The caller holds s.mu.
func (s *Local) noteDialogAnswered(words string) {
	s.dialogAnswered = words
	if s.asked != nil {
		s.dialogAnsweredAt = s.asked.Fed()
	}
}

// dialogGone forgets the dialog answered when the look in progress, which
// saw it go (no dialog, or another), took its text after it was noted: a
// look that took its text before saw an older screen. The caller holds
// s.mu; look is the watcher's LookedAt, read before.
func (s *Local) dialogGone(look uint64) {
	if look > s.dialogAnsweredAt {
		s.dialogAnswered = ""
	}
}

// noteAnswering notes, as the writer takes a write of data, that the
// question showing is answered when the write answers it: a reply to it
// that comes after is refused (questionWaits), though it shows until the
// write is made and answer clears it. A question the session holds (a
// startup question, a dialog asked during the work) is answered by keys
// that pick one of its answers: an Enter, one of its choices, or for a
// dialog Ctrl+C or a digit of one of its numbered lines (dialogPicks).
// That is so for whatever question shows as they are written: keys typed
// before it showed and written once it shows (they waited behind another
// write) reach it. A prompt the session does not hold is answered by any
// key typed while it showed (promptSince), as answer takes it. Keys typed
// into the terminal, a reply box's line and Conductor's own Enter put a
// question behind as a reply to it does.
//
// onScreen is what the screen the dialogs asked during the work are read
// from shows as the write is taken, when it shows one (nil otherwise, or
// when the screen was not read), read by the caller before the session's
// lock: keys that pick one of its answers answer it whether the session
// has asked it yet or not (the watcher asks a dialog once the screen is
// quiet), and a dialog with its words asked later, before it has gone, is
// answered already (sameDialog). It is the dialog the keys reach: a
// question the session still shows with other words (one answered and
// asked again, before the watcher has seen the next) is not what they
// answer.
func (s *Local) noteAnswering(promptSince *time.Time, data []byte, keys [][]byte, onScreen *dialogView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if onScreen != nil && onScreen.picks(onScreen.answers, data, keys) {
		s.noteDialogAnswered(onScreen.words)
	}
	att := s.info.Attention
	if att.State != AttentionNeedsInput || att.ID == "" {
		return
	}
	var picks bool
	switch {
	case att.Source == SourceQuestion && onScreen != nil && !sameWords(onScreen.words, att.words()):
		return
	case att.Source == SourceQuestion:
		picks = onScreen.picks(att.Options, data, keys)
	default:
		picks = picksChoice(att.Options, data, keys)
	}
	typedAt := att.Since == promptSince && !heldSource(att.Source)
	if !picks && !typedAt {
		return
	}
	s.markAnswered(att)
}

// dialogView is a dialog asked during the work as the screen shows it: its
// words, its question's choices (answers), and what its own rows offer:
// the keys they name ("(y)", "(p)", "(esc)", "esc to cancel") and how many
// numbered lines they have (0 when none were read).
type dialogView struct {
	words   string
	answers []Option
	keys    map[string]bool
	lines   int
}

// picks reports whether data, whose keys are keys (keyReader), picks an
// answer of the dialog v shows (a nil v: one whose rows were not read)
// whose choices are answers: data is one of its choices, or one of the
// keys does (a batch of keys from a terminal, an arrow then y, is each of
// them): an Enter (the highlighted line), one of its choices' keys, a key
// its rows name (in either case), Escape where they name it, Ctrl+C
// (cancel), or a digit of one of its numbered lines (a digit past them does
// nothing; with none read, every digit counts). A bracketed paste is text,
// no key.
func (v *dialogView) picks(answers []Option, data []byte, keys [][]byte) bool {
	if ownKeys(data, keys) && isAnswer(answers, data) {
		return true
	}
	for _, k := range keys {
		if v.picksKey(answers, k) {
			return true
		}
	}
	return false
}

// picksChoice reports whether data, whose keys are keys, picks one of a
// question's choices (answers) or presses Enter: data is one (when its
// keys are its own, ownKeys), or one of its keys is an Enter or a choice. A
// write that is all paste text (no key), or the rest of a sequence a write
// before began, is none.
func picksChoice(answers []Option, data []byte, keys [][]byte) bool {
	if ownKeys(data, keys) && isAnswer(answers, data) {
		return true
	}
	for _, k := range keys {
		if string(k) == "\r" || isAnswer(answers, k) {
			return true
		}
	}
	return false
}

// takesDigit reports whether a startup question whose answers are answers
// picks one of its numbered items on a digit at once: one of its answers is
// a digit alone (Codex's background-server and update questions). One whose
// digit only moves the highlight (Codex's hooks review: its answers are a
// digit and an Enter) does not.
func takesDigit(answers []Option) bool {
	for _, o := range answers {
		if len(o.Input) == 1 && o.Input[0] >= '1' && o.Input[0] <= '9' {
			return true
		}
	}
	return false
}

// startupItem matches a numbered item of a startup question in the text the
// startup questions' watcher keeps (ScreenTail: its rows run together, each
// run of white space or control one space): "1. ", "› 2. ".
var startupItem = regexp.MustCompile(`(?:^|\s)([1-9])\.\s`)

// startupItems returns how many numbered items the startup question q shows
// in text (the watcher's): items 1, 2, … in order from its heading on (the
// last match of its words or of its pattern, whichever begins first; the
// update question's pattern is in its first item); 0 when none is read.
func startupItems(q *StartupQuestion, text string) int {
	start := -1
	if m := q.Pattern.FindAllStringIndex(text, -1); len(m) > 0 {
		start = m[len(m)-1][0]
	}
	if start < 0 {
		return 0
	}
	if q.Words != nil {
		if m := q.Words.FindAllStringIndex(text, -1); len(m) > 0 && m[len(m)-1][0] < start {
			start = m[len(m)-1][0]
		}
	}
	n := 0
	for _, m := range startupItem.FindAllStringSubmatch(text[start:], -1) {
		if d := int(m[1][0] - '0'); d == n+1 {
			n = d
		}
	}
	return n
}

// picksStartup reports whether data, whose keys are keys, answers a startup
// question whose answers are answers and that shows items numbered items (0
// when they were not read): one of its answers, or an Enter (picksChoice),
// or, when a digit picks at once (takesDigit), the digit of one of its
// items. Codex's background-server question takes 2, to restart, though no
// quick reply offers it. With its items not read (its heading gone from
// the text the watcher keeps), a digit it offers no reply for answers
// nothing: one that does nothing must not clear a question that still
// shows (a submission's Enter would reach it); a reply to it is refused
// then anyway (stillOnScreen).
func picksStartup(answers []Option, items int, data []byte, keys [][]byte) bool {
	if picksChoice(answers, data, keys) {
		return true
	}
	if !takesDigit(answers) {
		return false
	}
	return anyKey(keys, func(k []byte) bool {
		return len(k) == 1 && k[0] >= '1' && k[0] <= '9' && int(k[0]-'0') <= items
	})
}

// startupPicks is picksStartup for the startup question att shows, its
// items read from the text the startup questions' watcher keeps while that
// still shows it.
func (s *Local) startupPicks(att Attention, data []byte, keys [][]byte) bool {
	items := 0
	if s.trust != nil {
		text := s.trust.Text()
		if q, words := s.lastStartupQ(text); q != nil && sameWords(words, att.words()) {
			items = startupItems(q, text)
		}
	}
	return picksStartup(att.Options, items, data, keys)
}

// picksKey is picks for one key.
func (v *dialogView) picksKey(answers []Option, key []byte) bool {
	if isAnswer(answers, key) {
		return true
	}
	if v != nil && (v.keys[string(key)] || v.keys[strings.ToLower(string(key))]) {
		return true
	}
	if len(key) != 1 {
		return false
	}
	switch d := key[0]; {
	case d == '\r', d == 0x03:
		return true
	case d >= '1' && d <= '9':
		return v == nil || v.lines == 0 || int(d-'0') <= v.lines
	}
	return false
}

// mayAnswerDialog reports whether keys hold one that may answer a dialog:
// one that is not an escape sequence (a lone Escape is).
func mayAnswerDialog(keys [][]byte) bool {
	for _, k := range keys {
		if len(k) == 1 {
			return true
		}
	}
	return false
}

// isAnswer reports whether data is the input of one of answers.
func isAnswer(answers []Option, data []byte) bool {
	for _, o := range answers {
		if o.Input != "" && o.Input == string(data) {
			return true
		}
	}
	return false
}

// keysWritten returns the keys the program reads from data, a write the
// writer is taking (its before), whatever a write before it left
// unfinished (a paste, an escape sequence, a control string) carried in,
// and whether data is none of a person's typing: no key and no paste's text,
// all of it the rest of a sequence or a control string a write before
// began (a terminal's report cut across its reads), or the start of one the
// next ends. Every write made goes through it once, in order.
//
// maybe are the keys data may hold besides keys, never surely: an Escape
// that ends it, held (keyReader.escHeld: it may be the Escape key and may
// be the start of what the next write goes on with, and nothing tells
// which, the program reading two writes as one however they came), and the
// keys an Alt chord's (ESC and a key) may be read as alone. data is no
// quiet write with them.
func (s *Local) keysWritten(data []byte) (keys [][]byte, quiet bool, maybe [][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys = s.keysIn.keys(data)
	maybe = s.keysIn.maybe
	if s.keysIn.escHeld {
		maybe = append(maybe, []byte{0x1b})
	}
	return keys, len(keys) == 0 && !s.keysIn.pasted && len(maybe) == 0, maybe
}

// escWait is how long an Escape a write ended with stays unfinished for a
// reply (inputBusy): a report a terminal's read cut comes on at once, and a
// reply written right after it could be read with it as one key.
const escWait = 50 * time.Millisecond

// ErrInputBusy refuses a reply to a question while what was written to the
// session before it is unfinished (a paste, an escape sequence another
// connection's terminal began): its keys would be read as part of it.
// Nothing was written, and the question was not noted; it can be tried
// again.
var ErrInputBusy = errors.New("another connection's paste or key is being written to the session: try again in a moment")

// inputBusy reports whether a write before the next left something
// unfinished (keyReader). The caller holds s.mu.
func (s *Local) inputBusy() bool {
	if s.keysIn.escHeld && time.Since(s.keysIn.escAt) > escWait {
		// An Escape a write ended with, long enough ago to have been read
		// alone.
		return false
	}
	return s.keysIn.pasting || s.keysIn.inString || s.keysIn.inCSI || len(s.keysIn.carry) > 0
}

// ownKeys reports whether keys, as keysWritten read them from data, are
// data's own bytes and all of them: nothing carried in, nothing left
// unfinished, no paste. Only then is data matched whole (a choice's input
// of several keys).
func ownKeys(data []byte, keys [][]byte) bool {
	return len(keys) > 0 && bytes.Equal(bytes.Join(keys, nil), data)
}

// allReports reports whether every key is a terminal's own report
// (isTerminalReport): one cut across writes, read whole.
func allReports(keys [][]byte) bool {
	for _, k := range keys {
		if !isTerminalReport(k) {
			return false
		}
	}
	return len(keys) > 0
}

// anyKey reports whether one of keys is one ok says.
func anyKey(keys [][]byte, ok func(k []byte) bool) bool {
	for _, k := range keys {
		if ok(k) {
			return true
		}
	}
	return false
}

// maxKeyCarry bounds the end of a write a keyReader keeps for the next: an
// escape sequence cut by a terminal's read.
const maxKeyCarry = 32

// keyReader splits the writes to a program, in order, into the keys it
// reads from them: a terminal's report or a mouse report is one key, read
// by reportLen as typesSomething reads it (one grammar: what is a report
// here is one there), an escape sequence (CSI, ESC [ … final byte; SS3,
// ESC O x) is one key, as is a control string (OSC ESC ], DCS ESC P, SOS ESC X,
// PM ESC ^, APC ESC _, through its BEL or ST: a terminal's colour or
// version report), an Escape not starting one is a key, a bracketed paste
// is text and no key, every other byte is a key. A paste, a control string
// or an escape sequence one write begins and a later one ends (a
// terminal's reads cut them anywhere) is carried over: pasting says a
// paste goes on, inString a control string (its bytes no keys), carry is
// the unfinished end of the last write (at most maxKeyCarry bytes).
//
// An Escape that ends a write outside a paste is kept too (escHeld, the
// carry its ESC): a terminal's read can cut a report right after its ESC,
// the rest coming in the next, and two writes (a page's frames, ESC then
// z) can reach the program as one read (Alt+z). The next write that goes
// on with a sequence makes it that sequence's start; one that does not
// makes it, with that write's first key, one Alt chord (the key alone one
// it may be read as), and an ESC after it the Escape key.
type keyReader struct {
	pasting  bool
	inString bool
	// inCSI says an escape sequence cut longer than maxKeyCarry goes on:
	// its bytes are skipped, no key, through its final byte.
	inCSI bool
	carry []byte
	// pasted says the last write held a paste's text.
	pasted bool
	// escHeld says the last write ended in an Escape kept, at escAt.
	escHeld bool
	escAt   time.Time
	// maybe are the keys the last write's Alt chords may be read as alone.
	maybe [][]byte
}

// stringEnd returns the index just past the BEL or ST that ends a control
// string in data from i, -1 when data ends first (cutAt says where an ESC
// of a cut ST begins then, -1 when none).
func stringEnd(data []byte, i int) (end, cutAt int) {
	for j := i; j < len(data); j++ {
		switch data[j] {
		case 0x07:
			return j + 1, -1
		case 0x1b:
			if j+1 == len(data) {
				return -1, j
			}
			if data[j+1] == '\\' {
				return j + 2, -1
			}
		}
	}
	return -1, -1
}

// holdAskedAgain holds the startup question asked again answered as id
// (heldAnswer), so that it goes once the screen shows no startup question
// (escGone): one an Escape may have answered before it was asked
// (heldAsked) keeps who typed it; one asked again after its answer
// (startupStill: the program drew it again, then may move on without
// another) is held from that answer, by who gave it. The caller holds
// s.mu.
func (s *Local) holdAskedAgain(id, words string, heldAsked, startupStill bool) {
	switch {
	case heldAsked:
		s.heldAnswer.id, s.heldAnswer.words = id, ""
	case s.heldAnswer.id == id:
		// Held already (an Escape put it behind, which noted its words
		// answered): who typed it stays.
	case startupStill:
		held := heldStartup{id: id, fed: s.startupAnsweredFed}
		if s.asked != nil {
			held.cols, held.rows = s.asked.Size()
		}
		if last := s.info.LastAnswer; last != nil && sameWords(last.Message, words) {
			held.by, held.name = last.By, last.ByName
		}
		s.heldAnswer = held
	}
}

// heldStartup is a startup question an Escape a write ended with
// may have answered: its id (asked) or its words (not asked yet), who typed
// it, and how many Feeds the screen the questions asked during the work are
// read from had seen as it was written (fed).
type heldStartup struct {
	id, words, by, name string
	fed                 uint64
	// cols and rows are that screen's size then: on a smaller one the
	// question may not read as itself (cut, or drawn narrower), and what
	// it shows proves nothing gone.
	cols, rows int
}

// holdStartupAnswered notes the startup question showing, or (words, not
// asked yet) the one the screen shows, as one an Escape a write
// ended with may have answered, typed by sub: held answered (a reply to it
// refused), not cleared, its screen not put behind, until the screen shows
// it gone (escGone), a key surely answers it, or the next startup question
// takes its place.
func (s *Local) holdStartupAnswered(words string, sub *Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := heldStartup{by: sub.ID, name: sub.Name}
	if s.asked != nil {
		held.fed = s.asked.Fed()
		held.cols, held.rows = s.asked.Size()
	}
	att := s.info.Attention
	if att.State == AttentionNeedsInput && att.Source == SourceTrust && att.ID != "" && (words == "" || sameWords(att.words(), words)) {
		s.markAnswered(att)
		held.id = att.ID
		if prev := s.heldAnswer; prev.id == att.ID {
			// Held already: by who held it first, the screen at least as
			// large as either asked for (a key typed at a smaller size
			// asks no less), output after this key.
			held.by, held.name = prev.by, prev.name
			held.cols, held.rows = max(held.cols, prev.cols), max(held.rows, prev.rows)
		}
		s.heldAnswer = held
		return
	}
	if words != "" {
		held.words = words
		if prev := s.heldAnswer; sameWords(prev.words, held.words) {
			held.by, held.name = prev.by, prev.name
			held.cols, held.rows = max(held.cols, prev.cols), max(held.rows, prev.rows)
		}
		s.heldAnswer = held
	}
}

// escGone clears the startup question held answered (heldAnswer) once the
// screen the questions asked during the work are read from shows no
// startup question, as it is now and after output fed since the Escape was
// written: the program took the Escape and moved on. Its answer is sure
// then, and the startup output is put behind as a sure answer's is
// (startupGone). The look and that are made with no output fed meanwhile
// (feedMu): a look that took its text earlier (a watcher's, its callback
// late) is never what decides, and no startup question drawn since is put
// behind unseen. Asked at each look of that screen, and as a startup
// question is asked; a session without that screen has nothing to tell it
// by, and keeps the question until a key surely answers it or the next
// takes its place.
func (s *Local) escGone() {
	if s.asked == nil {
		return
	}
	s.feedMu.Lock()
	fed := s.asked.Fed()
	s.mu.Lock()
	att, held := s.info.Attention, s.heldAnswer
	s.mu.Unlock()
	if held.id == "" || att.ID != held.id || att.State != AttentionNeedsInput || att.Source != SourceTrust || fed <= held.fed || !s.screenShowsNoStartup(held) {
		s.feedMu.Unlock()
		return
	}
	s.mu.Lock()
	if s.heldAnswer != held || s.info.Attention.ID != held.id {
		s.mu.Unlock()
		s.feedMu.Unlock()
		return
	}
	s.startupGone(att.words())
	s.mu.Unlock()
	s.trust.Reset()
	s.feedMu.Unlock()
	s.answer(att.Since, held.by, held.name, true, true)
}

// screenShowsNoStartup reports whether the screen the questions asked
// during the work are read from, whole, shows no startup question, and is
// a whole frame (no synchronized update under way, which a terminal would
// still show the frame before; nothing clipped), at least the size it was
// when the question was held (held): smaller, the question may not read
// as itself. The caller holds feedMu.
func (s *Local) screenShowsNoStartup(held heldStartup) bool {
	text, settled, cols, rows := s.asked.Whole()
	return settled && cols >= held.cols && rows >= held.rows && s.lastStartup(text) == ""
}

// heldGone reports whether the startup question with words, about to be
// asked, is the one an Escape a write ended with may have
// answered before it was asked (heldAnswer's words), and the screen the
// questions asked during the work are read from shows no startup question,
// as it is now and after output fed since the Escape was written: the
// program took the Escape and moved on, and it is not asked; the startup
// output is put behind (startupGone). Made as escGone's is, with no output
// fed meanwhile.
func (s *Local) heldGone(words string) bool {
	if s.asked == nil {
		return false
	}
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	fed := s.asked.Fed()
	s.mu.Lock()
	held := s.heldAnswer
	s.mu.Unlock()
	if held.words == "" || !sameWords(held.words, words) || fed <= held.fed || !s.screenShowsNoStartup(held) {
		return false
	}
	s.mu.Lock()
	if s.heldAnswer != held {
		s.mu.Unlock()
		return false
	}
	s.startupGone(held.words)
	s.mu.Unlock()
	s.trust.Reset()
	return true
}

// startupGone notes the startup question with words, held answered by an
// Escape a write ended with, as surely answered: a look of the
// startup questions' watcher that took its text before now asks nothing,
// and the question asked again before another takes its place is asked
// answered (as a key that surely answers one does in input). The caller
// holds s.mu, and resets the watcher's text once it has let go.
func (s *Local) startupGone(words string) {
	s.heldAnswer = heldStartup{}
	s.startupAnswered, s.startupAnsweredAt = true, s.trust.Fed()
	s.startupAnsweredWords = words
	if s.asked != nil {
		s.startupAnsweredFed = s.asked.Fed()
	}
}

// keys returns the keys of data, the next write.
func (r *keyReader) keys(data []byte) [][]byte {
	// An Escape held from the write before is read with data as one write
	// is: the start of the sequence data goes on with, the Escape key with
	// an ESC after it, else an Alt chord with data's first key (the program
	// may read the two writes as one).
	if len(r.carry) > 0 {
		data = append(r.carry, data...)
		r.carry = nil
	}
	r.pasted, r.escHeld, r.maybe = false, false, nil
	var keys [][]byte
	i := 0
	cut := func(from int) {
		if len(data)-from <= maxKeyCarry {
			r.carry = bytes.Clone(data[from:])
		}
	}
	for i < len(data) {
		if r.inCSI {
			for i < len(data) && (data[i] < 0x40 || data[i] > 0x7e) {
				i++
			}
			if i == len(data) {
				return keys
			}
			i++ // its final byte
			r.inCSI = false
			continue
		}
		if r.inString {
			end, cutAt := stringEnd(data, i)
			if end < 0 {
				if cutAt >= 0 {
					cut(cutAt)
				}
				return keys
			}
			i = end
			r.inString = false
			continue
		}
		if r.pasting {
			end := bytes.Index(data[i:], []byte(pasteEnd))
			if end != 0 {
				r.pasted = true
			}
			if end < 0 {
				// The end marker may be cut: keep what could begin it.
				for n := min(len(pasteEnd)-1, len(data)-i); n > 0; n-- {
					if bytes.HasPrefix([]byte(pasteEnd), data[len(data)-n:]) {
						cut(len(data) - n)
						break
					}
				}
				return keys
			}
			i += end + len(pasteEnd)
			r.pasting = false
			continue
		}
		report := 0
		if data[i] == 0x1b && i+1 < len(data) {
			report = reportLen(data[i:])
		}
		switch {
		case data[i] != 0x1b:
			keys = append(keys, data[i:i+1])
			i++
		case i+1 == len(data):
			// An Escape that ends a write: kept until the next write says
			// whether it began a sequence (escHeld).
			r.carry, r.escHeld, r.escAt = []byte{0x1b}, true, time.Now()
			return keys
		case report > 0:
			// A terminal's report or a mouse report, read as
			// typesSomething reads it (one grammar for both): one key,
			// its bytes none of their own.
			keys = append(keys, data[i:i+report])
			i += report
		case legacyBegun(data[i:]):
			// An X10 mouse report (ESC [ M and three bytes of 32 or more:
			// the button and the cell) cut short: carried to the next
			// write. With a byte under 32 among them it is no mouse
			// report: CSI M, and that byte a key.
			cut(i)
			return keys
		case data[i+1] == '[':
			j := i + 2
			for j < len(data) && (data[j] < 0x40 || data[j] > 0x7e) {
				j++
			}
			if j == len(data) {
				if len(data)-i > maxKeyCarry {
					// Too long to carry: skipped through its final byte.
					r.inCSI = true
					return keys
				}
				cut(i)
				return keys
			}
			if seq := data[i : j+1]; string(seq) == pasteStart {
				r.pasting = true
			} else if key, maybe, encoded := encodedKey(seq); encoded {
				// A key the terminal encodes (CSI u): the key it is, a
				// release none.
				if key != nil {
					keys = append(keys, key)
				}
			} else {
				keys = append(keys, seq)
				if maybe != nil {
					r.maybe = append(r.maybe, maybe)
				}
			}
			i = j + 1
		case data[i+1] == 'O':
			if i+2 >= len(data) {
				cut(i)
				return keys
			}
			keys = append(keys, data[i:i+3])
			i += 3
		case data[i+1] == ']' || data[i+1] == 'P' || data[i+1] == 'X' || data[i+1] == '^' || data[i+1] == '_':
			end, cutAt := stringEnd(data, i+2)
			if end < 0 {
				// Its end comes in a later write: its bytes are no keys
				// until then.
				r.inString = true
				if cutAt >= 0 {
					cut(cutAt)
				}
				return keys
			}
			keys = append(keys, data[i:end])
			i = end
		case data[i+1] == 0x1b:
			// An ESC another follows: the Escape key, however the program
			// reads them (Codex's crossterm reads two at once as Esc); the
			// second is read as it goes on (a report's start, an Alt chord).
			keys = append(keys, data[i:i+1])
			i++
		default:
			// ESC and a key at once: one key, the key with Alt (a page's
			// xterm sends Alt+z as ESC z; Codex's crossterm reads one Alt
			// chord, which its questions ignore), never the Escape key.
			// The key alone is one it may be read as (a program that
			// ignores Alt), counted where that is the safer mistake.
			_, size := utf8.DecodeRune(data[i+1:])
			keys = append(keys, data[i:i+1+size])
			r.maybe = append(r.maybe, data[i+1:i+1+size])
			i += 1 + size
		}
	}
	return keys
}

// keypadKeys are the kitty keyboard protocol's codes for the keypad's keys
// that type something (KP_0 to KP_9, the decimal point, the operators,
// Enter, equals, the separator), and the key each types.
var keypadKeys = map[int]byte{
	57399: '0', 57400: '1', 57401: '2', 57402: '3', 57403: '4', 57404: '5', 57405: '6', 57406: '7', 57407: '8', 57408: '9',
	57409: '.', 57410: '/', 57411: '*', 57412: '-', 57413: '+', 57414: '\r', 57415: '=', 57416: ',',
}

// encodedKey reads seq, one escape sequence a terminal sent, as a key the
// terminal encodes once a program asks it to tell keys apart: the kitty
// keyboard protocol's CSI u (CSI code[:alternates] ; mods[:event] ; text u,
// which Codex asks for: Escape is ESC [ 2 7 u, Ctrl+C ESC [ 9 9 ; 5 u).
// xterm's modifyOtherKeys (CSI 27 ; mods ; code ~) is not decoded: Codex's
// crossterm does not read it as the key it encodes. A press of a key read
// the same unencoded is that key: Escape, Enter, Tab, Backspace, a
// keypad key (its digit, its Enter), a character (Shift and a letter its
// capital, or the shifted key the
// terminal names), Ctrl and a letter its control byte (not Ctrl+H, I, J or
// M, whose bytes are other keys'); a release is no key (nil). encoded is
// false for anything else (a repeat, an arrow, Shift+Enter, Ctrl+M, a
// terminal's report such as CSI ? flags u): seq stays the key it is, and
// maybe is the key a program may read it as (Enter for a repeated Enter,
// or for Shift+Enter: a handler may take repeats, or look at the key, not
// its modifiers). Caps Lock and Num Lock change nothing.
func encodedKey(seq []byte) (key, maybe []byte, encoded bool) {
	if len(seq) < 4 || seq[0] != 0x1b || seq[1] != '[' {
		return nil, nil, false
	}
	params, final := string(seq[2:len(seq)-1]), seq[len(seq)-1]
	if strings.Trim(params, "0123456789:;") != "" || params == "" {
		return nil, nil, false
	}
	fields := strings.Split(params, ";")
	field := func(i int) []string {
		if i < len(fields) {
			return strings.Split(fields[i], ":")
		}
		return nil
	}
	number := func(subs []string, j, def int) int {
		if j >= len(subs) || subs[j] == "" {
			return def
		}
		n, err := strconv.Atoi(subs[j])
		if err != nil || n > 0x10ffff {
			return -1
		}
		return n
	}
	var code, shifted, mods, event int
	switch final {
	case 'u':
		codes, mod := field(0), field(1)
		code, shifted = number(codes, 0, -1), number(codes, 1, -1)
		mods, event = number(mod, 0, 1), number(mod, 1, 1)
	default:
		return nil, nil, false
	}
	if code < 0 || mods < 1 || event < 1 {
		return nil, nil, false
	}
	switch event {
	case 1, 2:
	case 3:
		return nil, nil, true
	default:
		return nil, nil, false
	}
	const shift, ctrl, locks = 1, 4, 64 | 128
	printable := func(c int) bool { return c >= ' ' && c <= '~' }
	if k, ok := keypadKeys[code]; ok {
		// A keypad key (the protocol's own codes): the key it types, as
		// Codex's crossterm reads it (its digits, its Enter).
		code = int(k)
	}
	var pressed []byte
	switch m := (mods - 1) &^ locks; {
	case m == 0 && (code == 27 || code == '\r' || code == '\t' || code == 0x7f || printable(code)):
		pressed = []byte{byte(code)}
	case m == shift && printable(shifted):
		pressed = []byte{byte(shifted)}
	case m == shift && code >= 'a' && code <= 'z':
		pressed = []byte{byte(code - 'a' + 'A')}
	case m == ctrl && code >= 'a' && code <= 'z' && !strings.ContainsRune("hijm", rune(code)):
		// Ctrl and a letter, its control byte, but for those whose byte
		// is another key's (Backspace, Tab, a line feed, Enter): the
		// protocol sends those keys as their own codes, and Codex's
		// crossterm reads Ctrl+M as Ctrl and m, never Enter.
		pressed = []byte{byte(code - 'a' + 1)}
	}
	if pressed != nil {
		if event == 2 {
			// A repeat (a key held down): a program may take it as the
			// key or ignore it (Codex's trust question ignores repeats),
			// so it is the key only maybe.
			return nil, pressed, false
		}
		return pressed, nil, true
	}
	if code == 27 || code == '\r' || code == '\t' || code == 0x7f || printable(code) {
		// A key with modifiers read as no other (Shift+Enter, Ctrl+M,
		// Ctrl+3): a program may read it as its key alone (a handler that
		// looks at the key, not the modifiers), which maybe is, counted
		// where that is the safer mistake.
		return nil, []byte{byte(code)}, false
	}
	return nil, nil, false
}

// numberedLine matches a numbered line of a dialog's list ("› 1. Yes,
// proceed (y)", "  2. No, …"): its number.
var numberedLine = regexp.MustCompile(`^\s*(?:[›❯>]\s*)?([1-9])\.\s`)

// namedKey matches a key a dialog's row names at its end: "(y)", "(esc)".
var namedKey = regexp.MustCompile(`\(\s*([A-Za-z]{1,3})\s*\)\s*$`)

// escToCancel matches a dialog's footer that names Escape.
var escToCancel = regexp.MustCompile(`(?i)\besc\b`)

// readDialog is the dialog view of words, the question q's, as screen (its
// text, rows joined by line breaks) shows it: its numbered lines and the
// keys its rows name are read from its own rows, the run of rows without a
// blank one that holds the last numbered line, and the rows below it (the
// footer); numbered lines elsewhere (a list in the output above) are not
// its own.
func readDialog(q *StartupQuestion, words, screen string) *dialogView {
	v := &dialogView{words: words, answers: CleanOptions(q.Answers), keys: map[string]bool{}}
	rows := strings.Split(screen, "\n")
	last := -1
	for i := len(rows) - 1; i >= 0 && last < 0; i-- {
		if numberedLine.MatchString(rows[i]) {
			last = i
		}
	}
	if last < 0 {
		return v
	}
	first := last
	for first > 0 && strings.TrimSpace(rows[first-1]) != "" {
		first--
	}
	for _, row := range rows[first:] {
		if m := numberedLine.FindStringSubmatch(row); m != nil {
			v.lines = max(v.lines, int(m[1][0]-'0'))
		}
		if m := namedKey.FindStringSubmatch(row); m != nil {
			key := strings.ToLower(m[1])
			if key == "esc" {
				key = "\x1b"
			}
			if len(key) == 1 {
				v.keys[key] = true
			}
		}
		if escToCancel.MatchString(row) {
			v.keys["\x1b"] = true
		}
	}
	return v
}

// ErrBadAnswer refuses an answer message out of its bounds
// (proto.Answer.Valid): nothing is written.
var ErrBadAnswer = errors.New("bad answer message")

// AnswerResult is the owner's word on a viewer's answer (proto.Answer)
// that ended with err, to that viewer: an answer_ok naming reqID when err is
// nil (nil when there is no reqID to name: nothing is sent), else an error
// naming it, question_answered for ErrQuestionAnswered. A line accepted
// while its program takes it slowly (ErrSubmitPending) is taken. A reqID
// out of its bounds is named by nothing.
func AnswerResult(reqID string, err error) []byte {
	if !proto.ValidAnswerReqID(reqID) {
		reqID = ""
	}
	if err == nil || errors.Is(err, ErrSubmitPending) {
		if reqID == "" {
			return nil
		}
		return proto.MustControl(proto.AnswerOK{T: proto.CtlAnswerOK, RequestID: reqID})
	}
	code, msg := "input_failed", "input could not be delivered"
	switch {
	case errors.Is(err, ErrQuestionAnswered):
		code, msg = proto.ErrCodeQuestionAnswered, QuestionAnsweredWords
	case errors.Is(err, ErrTrustQuestion):
		code, msg = proto.ErrCodeNotSent, TrustQuestionWords
	case errors.Is(err, ErrQuestion):
		code, msg = proto.ErrCodeNotSent, QuestionWords
	case errors.Is(err, ErrReadOnly):
		code, msg = proto.ErrCodeReadOnly, "this link is view-only"
	case errors.Is(err, ErrSessionEnded):
		code, msg = proto.ErrCodeSessionEnded, "the session has ended"
	case errors.Is(err, ErrTooManyRequests):
		code, msg = proto.ErrCodeTooManyRequests, "too many requests at once"
	case errors.Is(err, ErrTextTooLong), errors.Is(err, ErrBadAnswer):
		code, msg = proto.ErrCodeBadFrame, err.Error()
	case errors.Is(err, ErrInputStalled), errors.Is(err, context.DeadlineExceeded):
		msg = ErrInputStalled.Error()
	case errors.Is(err, ErrInputBusy):
		msg = ErrInputBusy.Error()
	}
	return proto.MustControl(proto.ErrorMsg{T: proto.CtlError, Code: code, Message: msg, RequestID: reqID})
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
		if s.held != nil && att.Source != SourceSubmit && !s.info.Status.Ended() {
			// A question answered while the turn is still held for a typed
			// text (one gone with keys typed before the hold): the hold
			// shows again.
			now := time.Now().UTC()
			s.info.Attention = Attention{State: AttentionNeedsInput, Message: HeldWords, Source: SourceSubmit, Since: &now, ID: NewID()}
		}
		s.settleLocked(s.info.Attention)
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
		s.sized(cols, rows)
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
				s.sized(cols, rows)
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

// DisconnectLink evicts every subscription created through linkID as
// revoked, whether or not the session has ended (a run's link keeps its
// viewers on a member that ended, until it is revoked): each is written
// nothing more that was queued for it. It ends them under the lock
// AttachWith registers them under, so a caller that marked the link first
// (a revoke) misses none: one that attaches after this finds the mark
// (AttachOptions.Authorize); and nothing broadcast after reaches them. It
// returns once their transports have closed, which they do together, each
// on its own goroutine, holding no lock: as long as the slowest takes. That
// includes the link's clients that had ended already (a slow consumer, an
// earlier revoke, the session's end) and whose transport still closes: what
// the session's end still writes them stops (Subscription.revoke).
func (s *Local) DisconnectLink(linkID string) { s.closeLink(linkID, true) }

// EndLink evicts every subscription created through linkID, one of the
// session's own links, as the session's end, which ends its links with it
// (the caller marks the link first, as a revoke does): each is still
// written what was queued for it, the session's last output and its
// status, before its close (Subscription.owed). Otherwise it is
// DisconnectLink.
func (s *Local) EndLink(linkID string) { s.closeLink(linkID, false) }

// closeLink is DisconnectLink (revoked) and EndLink. A revoke also stops
// what the session's end still writes the link's clients
// (Subscription.revoke), should it have closed them first.
func (s *Local) closeLink(linkID string, revoked bool) {
	end := func(sub *Subscription) {
		if revoked {
			sub.revoke()
		} else {
			sub.closeWith(ErrSessionEnded)
		}
	}
	var hit []*Subscription
	s.mu.Lock()
	s.hub.Each(func(sub *Subscription) {
		if sub.LinkID == linkID {
			end(sub)
			hit = append(hit, sub)
		}
	})
	for sub := range s.closing {
		if sub.LinkID == linkID {
			end(sub)
			hit = append(hit, sub)
		}
	}
	s.mu.Unlock()
	waitClosed(hit)
}

// Retire is the session leaving the server's registry, which only an ended
// session does: every client is closed as at the session's end, and none
// attaches after (ErrSessionEnded). Whoever closes a link's clients finds
// them through the registry, so none may stay where it no longer looks:
// none is written anything more, whatever it was owed (the session's end
// of its links may still be writing theirs), as a revoke would stop it.
// It does not wait: each client's transport closes on the client's own
// goroutine, and its editors go as it detaches, so one that does not read
// holds up only its own.
func (s *Local) Retire() {
	s.mu.Lock()
	s.retired = true
	s.hub.Each(func(sub *Subscription) { sub.retire() })
	for sub := range s.closing {
		sub.retire()
	}
	s.mu.Unlock()
	if s.folder != nil {
		// A file request under way finishes or fails; none comes after.
		s.folder.Close()
	}
}

// CloseAll evicts every subscription with the given reason (server shutdown)
// and ends their editors (closeNvims), waiting for those (WaitEditors). It
// returns once their transports have closed,
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
	s.WaitEditors()
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
