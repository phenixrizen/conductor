package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

const (
	eventQueue     = 256
	eventPingEvery = 20 * time.Second
	// activityQueueLimit is how full a client's queue may be before activity
	// entries skip it. Session changes and removals carry state a client
	// cannot do without, and a client that misses one is dropped and starts
	// over from a snapshot; the entries are a live feed a client may miss
	// some of. So a burst of entries never uses the last quarter of the queue.
	activityQueueLimit = eventQueue - eventQueue/4
)

// eventHub fans session changes out to Server-Sent Events clients, and
// activity entries to its sinks as well.
type eventHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	// sinks receive every activity entry after the clients: the webhooks and
	// the crew runs.
	sinks []activitySink
}

// activitySink receives an activity entry of a session and, for an attention
// entry, the attention state it records: the state the session was in when it
// recorded the entry, "" when that is not known.
type activitySink func(sessionID string, e session.ActivityEntry, state session.AttentionState)

func newEventHub() *eventHub { return &eventHub{clients: map[chan []byte]struct{}{}} }

func (h *eventHub) subscribe() chan []byte {
	ch := make(chan []byte, eventQueue)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *eventHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

// publish queues a session event; a client that cannot keep up is dropped.
func (h *eventHub) publish(info session.Info) {
	b, err := json.Marshal(info)
	if err != nil {
		return
	}
	msg := []byte("event: session\ndata: " + string(b) + "\n\n")
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			delete(h.clients, ch)
			close(ch)
		}
	}
}

// activityEvent is the data of an `activity` event: the entry, and the session
// it belongs to.
type activityEvent struct {
	SessionID string `json:"sessionId"`
	session.ActivityEntry
}

// addSink makes f receive every activity entry, after the clients have it.
// f runs where activity does, on the goroutine that recorded the entry: it
// must never wait, and must be safe for concurrent use.
func (h *eventHub) addSink(f activitySink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sinks = append(h.sinks, f)
}

// activity queues an activity entry of a session for every client, then hands
// it to every sink with state, the attention state an attention entry
// records ("" when it is not known); the clients get the entry alone. It is
// the OnActivity hook of every hosted session (a server session's is
// Server.localActivity, which calls it), so it runs on the goroutines that
// record, concurrently and out of order (each entry says when it happened),
// and it never waits: a client whose queue is past activityQueueLimit misses
// the entry and keeps its stream.
func (h *eventHub) activity(sessionID string, e session.ActivityEntry, state session.AttentionState) {
	b, err := json.Marshal(activityEvent{SessionID: sessionID, ActivityEntry: e})
	h.mu.Lock()
	if err == nil {
		msg := []byte("event: activity\ndata: " + string(b) + "\n\n")
		for ch := range h.clients {
			if len(ch) >= activityQueueLimit {
				continue
			}
			select {
			case ch <- msg:
			default:
			}
		}
	}
	sinks := h.sinks
	h.mu.Unlock()
	for _, f := range sinks {
		f(sessionID, e, state)
	}
}

// removed announces that a session left the registry.
func (h *eventHub) removed(id string) {
	msg := []byte("event: removed\ndata: " + fmt.Sprintf("{\"id\":%q}", id) + "\n\n")
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			delete(h.clients, ch)
			close(ch)
		}
	}
}

// handleEvents streams session changes as Server-Sent Events. The admin
// token must arrive in the Authorization header (the browser reads the
// stream with fetch, not EventSource) so it never appears in a URL.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r, false).admin {
		if !s.limiter.allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "admin token required")
		return
	}
	// The middleware wraps the writer; ResponseController reaches the flusher
	// through Unwrap.
	rc := http.NewResponseController(w)
	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	snapshot, _ := json.Marshal(s.registry.List())
	if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snapshot); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}

	ping := time.NewTicker(eventPingEvery)
	defer ping.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}
