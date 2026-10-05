package session

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/proto"
)

// ActivityEntry is one line of a session's activity log: who joined, left,
// answered, or what the agent signalled. The owner keeps the newest
// MaxActivity entries, broadcasts each new one as an `activity` control
// message and replays the last ActivityReplay to every new client.
type ActivityEntry struct {
	At      time.Time `json:"at"`
	Type    string    `json:"type"`              // one of the Activity* constants
	By      string    `json:"by,omitempty"`      // subscriber id
	ByName  string    `json:"byName,omitempty"`  // display name at the time
	Message string    `json:"message,omitempty"` // ≤ MaxAttentionMessage
	URL     string    `json:"url,omitempty"`     // artifact link, ≤ MaxEventURL bytes
	To      string    `json:"to,omitempty"`      // handoff target, ≤ MaxEventTo runes
	Tool    string    `json:"tool,omitempty"`    // tool involved, ≤ MaxEventTool bytes
}

// Activity entry types the session records itself.
const (
	ActivityAttention = "attention"
	ActivityInput     = "input"
	ActivityJoin      = "join"
	ActivityLeave     = "leave"
	ActivityLink      = "link"
	ActivityStatus    = "status"
)

// Event types: entries an agent reports about its work (docs/protocol.md,
// Events).
const (
	ActivityProgress   = "progress"    // a step finished; Message says which
	ActivityArtifact   = "artifact"    // something was produced; URL points at it
	ActivityHandoff    = "handoff"     // work passed to another member; To names them
	ActivityToolUse    = "tool_use"    // the agent ran a tool; Tool names it
	ActivityToolDenied = "tool_denied" // a tool call was refused; Tool names it
	ActivityError      = "error"       // the agent hit an error; Message says what
)

// Bounds for the activity log.
const (
	MaxActivity    = 200 // entries kept per session
	ActivityReplay = 50  // entries replayed to a new client
)

// Limits for the text of an event, beside MaxAttentionMessage for Message.
const (
	MaxEventURL  = 2048 // bytes
	MaxEventTo   = 40   // runes, like a display name
	MaxEventTool = 100  // bytes
)

// A session records EventRatePerSecond entries a second on average and
// EventBurst at once (a token bucket, EventBucket), so a chatty hook cannot
// flood its log, and an attention report from outside the session spends a
// token of the same bucket before it changes anything
// (Local.TrySetAttentionFull). The server spends the same allowance, from a
// bucket of its own, on the reports it forwards to a hosted session's host,
// so a flood of them cannot fill the host's connection either. Entries the
// session and the server produce themselves are exempt (see bucketed), and
// so is the attention entry of a change the session has applied.
const (
	EventRatePerSecond = 20
	EventBurst         = 40
)

// ErrRateLimited refuses a report from outside a session whose event bucket
// has no token for it: nothing was applied or sent. It is returned for a
// server session's own bucket (Local.TrySetAttentionFull) and for the one the
// server keeps for a hosted session's host; the API answers it with 429
// rate_limited.
var ErrRateLimited = errors.New("session: too many reports for this session")

// bucketed reports whether an entry of type t that is handed to Local.Record
// counts against the session's event bucket. join, leave, input, link and
// status entries are produced by the session and the server themselves, at
// the pace of people and of the process, and must never wait behind an
// agent's reports: a chatty hook may not starve the roster rows or the final
// status row. The six event types come from what an agent says or does, and
// so does any type not listed here. attention is bucketed for an entry handed
// to Record from outside the session, although nothing records one that way
// today: the attention entry of a change the session applies does not pass
// here (Local.recordOwn). The report that made the change has paid for it
// already (Local.TrySetAttentionFull), or the change is the session's own
// observation (the bell, an OSC notification, the screen pattern), which
// spends no token.
func bucketed(t string) bool {
	switch t {
	case ActivityJoin, ActivityLeave, ActivityInput, ActivityLink, ActivityStatus:
		return false
	}
	return true
}

// ValidEventType reports whether t is an activity entry type: one the session
// records itself or one an agent reports.
func ValidEventType(t string) bool {
	switch t {
	case ActivityAttention, ActivityInput, ActivityJoin, ActivityLeave, ActivityLink, ActivityStatus,
		ActivityProgress, ActivityArtifact, ActivityHandoff, ActivityToolUse, ActivityToolDenied, ActivityError:
		return true
	}
	return false
}

// ExitCode returns the code of a process that exited on its own, read from
// the message of its status entry, "exited (exit N)" as the session writes it
// when the process ends. ok is false for any other message, "stopped (exit N)"
// among them: a process an admin stopped ends with a signal, which is not the
// agent failing. A code too large for an int reads as the nearest one. It
// agrees with exitCode in web/app/utils/events.ts.
func ExitCode(message string) (code int, ok bool) {
	rest, found := strings.CutPrefix(message, string(StatusExited)+" (exit ")
	if !found {
		return 0, false
	}
	digits, found := strings.CutSuffix(rest, ")")
	if !found {
		return 0, false
	}
	unsigned := strings.TrimPrefix(digits, "-")
	if unsigned == "" || strings.Trim(unsigned, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	return n, true
}

// CleanEntry bounds the text of an entry that may come from outside the
// session, so a hook or a peer cannot fill the log or a control frame with it:
// control characters are dropped, surrounding space is trimmed and each field
// is cut to its limit at a character boundary. Message and Tool follow
// CleanMessage (line breaks stay); URL, To and ByName are single lines. Empty
// fields stay empty, and cleaning twice changes nothing.
//
// Of the fields it bounds, only URL can push an entry past proto.MaxControl:
// JSON writes & < > as six bytes each, so a URL near its limit can encode to
// more than a control frame. The relay rejects such a frame and closes the
// host's connection, so that URL is dropped and the rest of the entry kept.
// The guarantee assumes what CleanEntry leaves alone: Type and By are short
// and set by trusted code (an Activity* constant or a type checked with
// ValidEventType, and a subscriber ID), so it holds without bounding them. A
// By from outside the session goes through CleanID.
func CleanEntry(e ActivityEntry) ActivityEntry {
	e.ByName = oneLine(e.ByName, proto.MaxNameLen)
	e.Message = CleanMessage(e.Message)
	e.To = oneLine(e.To, MaxEventTo)
	e.Tool = cutBytes(CleanMessage(e.Tool), MaxEventTool)
	e.URL = cutBytes(strings.TrimSpace(strings.Map(dropControl, e.URL)), MaxEventURL)
	if e.URL != "" && !fitsControlFrame(e) {
		e.URL = ""
	}
	return e
}

// dropControl is a strings.Map function that removes control characters.
func dropControl(r rune) rune {
	if r < 0x20 || r == 0x7f {
		return -1
	}
	return r
}

// CleanID holds an id that comes from outside the session, such as the By of
// an entry a host reports, to what CleanEntry holds a single-line field to:
// control characters and surrounding space are dropped. An id still longer
// than max bytes is dropped whole rather than cut, since a cut id would name
// someone else.
func CleanID(id string, max int) string {
	id = strings.TrimSpace(strings.Map(dropControl, id))
	if len(id) > max {
		return ""
	}
	return id
}

// oneLine drops control characters and surrounding space and keeps at most n
// runes: CleanName without its "guest" default.
func oneLine(s string, n int) string {
	s = strings.TrimSpace(strings.Map(dropControl, s))
	if utf8.RuneCountInString(s) > n {
		s = strings.TrimSpace(string([]rune(s)[:n]))
	}
	return s
}

// cutBytes keeps at most n bytes of s without splitting a character.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n])
}

// longestStamp formats as the longest RFC 3339 timestamp an entry can carry.
var longestStamp = time.Date(2006, 1, 2, 15, 4, 5, 999999999, time.UTC)

// fitsControlFrame reports whether e, as an `activity` message, fits in one
// CONTROL frame whatever its timestamp: entries are cleaned before they are
// stamped.
func fitsControlFrame(e ActivityEntry) bool {
	e.At = longestStamp
	b, err := json.Marshal(EntryToProto(e))
	return err == nil && len(b) <= proto.MaxControl
}

// EntryToProto encodes e as the `activity` message of docs/protocol.md, with
// the time as RFC 3339 in UTC. It is what a viewer is sent for an entry, and
// the entry of the host `activity` message in both directions, so it is the
// one place that lists an entry's fields. An entry without a time is encoded
// without one, for the receiver to stamp.
func EntryToProto(e ActivityEntry) proto.Activity {
	a := proto.Activity{T: proto.CtlActivity, Type: e.Type, By: e.By, ByName: e.ByName, Message: e.Message, URL: e.URL, To: e.To, Tool: e.Tool}
	if !e.At.IsZero() {
		a.At = e.At.UTC().Format(time.RFC3339Nano)
	}
	return a
}

// EntryFromProto decodes an `activity` message received from outside the
// session, a host or the server. Nothing is checked or cleaned (see
// ValidEventType and CleanEntry), and a time that cannot be read leaves At
// zero.
func EntryFromProto(a proto.Activity) ActivityEntry {
	at, _ := time.Parse(time.RFC3339Nano, a.At)
	return ActivityEntry{At: at.UTC(), Type: a.Type, By: a.By, ByName: a.ByName, Message: a.Message, URL: a.URL, To: a.To, Tool: a.Tool}
}

// EventBucket is a token bucket: it holds up to EventBurst tokens, earns
// EventRatePerSecond a second and spends one per entry. The zero value is
// full, so an owner needs no constructor. It is not safe for concurrent use:
// its owner guards it with its own lock (a Local with its session lock, a
// signal.HostedSession with its mutex).
type EventBucket struct {
	tokens float64
	last   time.Time
}

// Take spends a token, first adding those earned since the last call. It
// reports false when none is left. A clock that steps back earns nothing.
func (b *EventBucket) Take(now time.Time) bool {
	switch {
	case b.last.IsZero():
		b.tokens, b.last = EventBurst, now
	case now.After(b.last):
		b.tokens = min(EventBurst, b.tokens+now.Sub(b.last).Seconds()*EventRatePerSecond)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Answer records who last cleared a needs-input prompt.
type Answer struct {
	By      string    `json:"by,omitempty"`
	ByName  string    `json:"byName"`
	At      time.Time `json:"at"`
	Message string    `json:"message,omitempty"`
}

// activityRing is a fixed-capacity log; Snapshot returns oldest first.
type activityRing struct {
	mu  sync.Mutex
	buf []ActivityEntry
}

// Add appends e, stamping At when zero, cleaning its text with CleanEntry and
// dropping the oldest entry past MaxActivity. It returns the stored entry.
func (r *activityRing) Add(e ActivityEntry) ActivityEntry {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	e = CleanEntry(e)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) >= MaxActivity {
		copy(r.buf, r.buf[1:])
		r.buf = r.buf[:MaxActivity-1]
	}
	r.buf = append(r.buf, e)
	return e
}

// Snapshot copies the log, oldest first.
func (r *activityRing) Snapshot() []ActivityEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ActivityEntry(nil), r.buf...)
}

// Tail copies the newest n entries, oldest first.
func (r *activityRing) Tail(n int) []ActivityEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > len(r.buf) {
		n = len(r.buf)
	}
	return append([]ActivityEntry(nil), r.buf[len(r.buf)-n:]...)
}
