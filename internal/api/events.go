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
)

// eventHub fans session changes out to Server-Sent Events clients.
type eventHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

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
