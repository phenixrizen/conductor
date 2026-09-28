package session

import (
	"sync"
	"time"
)

// ActivityEntry is one line of a session's activity log: who joined, left,
// answered, or what the agent signalled. The owner keeps the newest
// MaxActivity entries, broadcasts each new one as an `activity` control
// message and replays the last ActivityReplay to every new client.
type ActivityEntry struct {
	At      time.Time `json:"at"`
	Type    string    `json:"type"`              // attention | input | join | leave | link | status
	By      string    `json:"by,omitempty"`      // subscriber id
	ByName  string    `json:"byName,omitempty"`  // display name at the time
	Message string    `json:"message,omitempty"` // ≤ MaxAttentionMessage
}

// Activity entry types.
const (
	ActivityAttention = "attention"
	ActivityInput     = "input"
	ActivityJoin      = "join"
	ActivityLeave     = "leave"
	ActivityLink      = "link"
	ActivityStatus    = "status"
)

// Bounds for the activity log.
const (
	MaxActivity    = 200 // entries kept per session
	ActivityReplay = 50  // entries replayed to a new client
)

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

// Add appends e, stamping At when zero and dropping the oldest entry past
// MaxActivity. It returns the stored entry.
func (r *activityRing) Add(e ActivityEntry) ActivityEntry {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	e.Message = CleanMessage(e.Message)
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
