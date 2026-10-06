package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
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
// entry, the attention state it records, which comes with the entry: set with
// the entry's stamp on a server session, sent by the host on a hosted one; ""
// when it is not known (an older host sends none, and a state that is not one
// of the three is dropped).
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

// activityEvent is the data of an `activity` event: the entry, the session it
// belongs to and, for an attention entry, the attention state it records.
type activityEvent struct {
	SessionID string `json:"sessionId"`
	session.ActivityEntry
	// State is the attention state an attention entry records: needs_input,
	// working or done. It is absent for any other entry, and for an attention
	// entry that came without one: from an older host, which sends no state,
	// or from a host whose state was not one of the three, which the server
	// drops.
	State session.AttentionState `json:"state,omitempty"`
}

// eventState is what an activity event says an entry records: state, for an
// attention entry and one of the three states (isAttentionState); nothing
// otherwise.
func eventState(e session.ActivityEntry, state session.AttentionState) session.AttentionState {
	if e.Type == session.ActivityAttention && isAttentionState(state) {
		return state
	}
	return ""
}

// addSink makes f receive every activity entry, after the clients have it.
// f runs where activity does, on the goroutine that recorded the entry: it
// must never wait, and must be safe for concurrent use.
func (h *eventHub) addSink(f activitySink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sinks = append(h.sinks, f)
}

// activity queues an activity entry of a session for every client, with the
// attention state an attention entry records (eventState), then hands it to
// every sink with state ("" when it is not known). It is the OnActivity hook
// of every session, server and hosted alike, so it runs on the goroutines that
// record, concurrently and out of order (each entry says when it happened),
// and it never waits: a client whose queue is past activityQueueLimit misses
// the entry and keeps its stream.
func (h *eventHub) activity(sessionID string, e session.ActivityEntry, state session.AttentionState) {
	b, err := json.Marshal(activityEvent{SessionID: sessionID, ActivityEntry: e, State: eventState(e, state)})
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
// chatEvent is the data of a `chat` event: a chat message as kept
// (proto.ChatMessage) and the session or the run whose chat it is.
type chatEvent struct {
	SessionID string `json:"sessionId,omitempty"`
	RunID     string `json:"runId,omitempty"`
	proto.ChatMessage
}

// chat queues a chat message of a session (sessionID) or a run (runID) for
// every client, as droppable as activity: a client past activityQueueLimit
// misses it and keeps its stream. It is the OnChat hook of every server
// session and the OnRunChat hook of the run engine, so it runs on the posting
// goroutines and never waits. Chat reaches no sink: no webhook or feed.
func (h *eventHub) chat(sessionID, runID string, m session.ChatMessage) {
	b, err := json.Marshal(chatEvent{SessionID: sessionID, RunID: runID, ChatMessage: session.ChatToProto(m)})
	if err != nil {
		return
	}
	msg := []byte("event: chat\ndata: " + string(b) + "\n\n")
	h.mu.Lock()
	defer h.mu.Unlock()
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

func (h *eventHub) removed(id string) {
	h.broadcast([]byte("event: removed\ndata: " + fmt.Sprintf("{\"id\":%q}", id) + "\n\n"))
}

// runEvent is the data of a `run` event: the run to read again, or, with
// Removed, the run the server forgot. It carries no more than the ID, so it
// is at most maxRunEvent bytes whatever happened to the run.
type runEvent struct {
	ID      string `json:"id"`
	Removed bool   `json:"removed,omitempty"`
}

// maxRunEvent bounds a run event's data: a run ID is a crew ID (at most 40
// characters, crew.ValidID) and 9 more.
const maxRunEvent = 128

// run announces that a run changed in a way no session change carries
// (crew.Engine.OnRunChange), or, removed, that the engine forgot it
// (OnForget). Like a session change it is state a client cannot do without: a
// client that cannot keep up is dropped and starts over from a snapshot. It
// runs under the engine's lock and never waits.
func (h *eventHub) run(id string, removed bool) {
	b, err := json.Marshal(runEvent{ID: id, Removed: removed})
	if err != nil || len(b) > maxRunEvent {
		return
	}
	h.broadcast([]byte("event: run\ndata: " + string(b) + "\n\n"))
}

// broadcast queues msg for every client; a client that cannot keep up is
// dropped.
func (h *eventHub) broadcast(msg []byte) {
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
		writeError(w, http.StatusUnauthorized, "unauthorized", "workbench token required")
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
