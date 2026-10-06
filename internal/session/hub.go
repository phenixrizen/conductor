package session

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// Sink delivers frames to one attached client over some transport. WriteFrame
// may block; the subscription queue in front of it enforces the slow-consumer
// policy. Close is called exactly once with the reason the subscription ended.
type Sink interface {
	WriteFrame(frame []byte) error
	Close(reason error)
}

// HighWaterMark is the number of queued bytes after which a subscriber is
// evicted as a slow consumer.
const HighWaterMark = 1 << 20

const queueSlots = 4096

// Subscription is one attached client.
type Subscription struct {
	ID     string
	Role   Role
	LinkID string
	// Name and LinkLabel are what other viewers see in the roster.
	Name      string
	LinkLabel string
	Since     time.Time

	lastInput atomic.Int64 // unix ms of the last accepted input

	sink   Sink
	queue  chan []byte
	queued atomic.Int64
	// unsent counts the frames that are queued or being written; a frame
	// leaves it when WriteFrame returns. See Hub.Drained.
	unsent atomic.Int64
	done   chan struct{}
	once   sync.Once
	reason error

	inflight atomic.Int32 // file requests in progress

	chat chatBucket // the connection's chat posts, bounded
	// quiet marks a connection for a run's chat alone (hello.chatOnly): no
	// output or scrollback, not a viewer of the session.
	quiet bool
}

func newSubscription(id string, role Role, linkID string, sink Sink) *Subscription {
	s := &Subscription{ID: id, Role: role, LinkID: linkID, Since: time.Now().UTC(), sink: sink, queue: make(chan []byte, queueSlots), done: make(chan struct{})}
	go s.run()
	return s
}

// Info describes the subscription for the viewers roster.
func (s *Subscription) Info() proto.ViewerInfo {
	v := proto.ViewerInfo{ID: s.ID, Name: s.Name, Role: string(s.Role), Link: s.LinkLabel, Since: s.Since.Format(time.RFC3339), Quiet: s.quiet}
	if ms := s.lastInput.Load(); ms > 0 {
		v.LastInputAt = time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
	}
	return v
}

func (s *Subscription) run() {
	for {
		select {
		case <-s.done:
			return
		case frame := <-s.queue:
			s.queued.Add(-int64(len(frame)))
			err := s.sink.WriteFrame(frame)
			s.unsent.Add(-1)
			if err != nil {
				s.closeWith(err)
				return
			}
		}
	}
}

// send enqueues a frame without blocking. It evicts the subscriber when the
// queue exceeds the high-water mark.
func (s *Subscription) send(frame []byte) {
	select {
	case <-s.done:
		return
	default:
	}
	if s.queued.Load()+int64(len(frame)) > HighWaterMark {
		s.closeWith(ErrSlowConsumer)
		return
	}
	s.unsent.Add(1) // before the frame can be seen, so a check never finds it missing
	select {
	case s.queue <- frame:
		s.queued.Add(int64(len(frame)))
	default:
		s.unsent.Add(-1)
		s.closeWith(ErrSlowConsumer)
	}
}

// drained reports whether the sink has been handed everything sent to it, or
// the subscription has ended and nothing more will be.
func (s *Subscription) drained() bool {
	select {
	case <-s.done:
		return true
	default:
	}
	return s.unsent.Load() == 0
}

func (s *Subscription) closeWith(reason error) {
	s.once.Do(func() {
		s.reason = reason
		close(s.done)
		s.sink.Close(reason)
	})
}

// Done is closed when the subscription ends.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Reason returns why the subscription ended (nil for a normal detach).
func (s *Subscription) Reason() error {
	select {
	case <-s.done:
		return s.reason
	default:
		return nil
	}
}

// Hub fans frames out to subscriptions.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]*Subscription
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{subs: map[string]*Subscription{}} }

// namePresent reports whether a live subscription other than `except` carries
// the name: the same person on another tab, for the chat's join and leave lines.
func (h *Hub) namePresent(name, except string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for id, s := range h.subs {
		if id != except && s.Name == name && s.Reason() == nil {
			return true
		}
	}
	return false
}

func (h *Hub) add(s *Subscription) {
	h.mu.Lock()
	h.subs[s.ID] = s
	h.mu.Unlock()
}

func (h *Hub) remove(id string) {
	h.mu.Lock()
	delete(h.subs, id)
	h.mu.Unlock()
}

// Broadcast enqueues frame to every subscription.
func (h *Hub) Broadcast(frame []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subs {
		s.send(frame)
	}
}

// BroadcastLoud enqueues frame to every subscription but the quiet ones: the
// terminal's output.
func (h *Hub) BroadcastLoud(frame []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subs {
		if !s.quiet {
			s.send(frame)
		}
	}
}

// Drained reports whether every live subscription has been handed all the frames
// sent to it: none is queued and none is being written. A host asks it before
// it closes the connection its clients' frames travel on. It says nothing of
// what a transport does with a frame after taking it.
func (h *Hub) Drained() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subs {
		if !s.drained() {
			return false
		}
	}
	return true
}

// Count returns the number of live subscriptions.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, s := range h.subs {
		if s.Reason() == nil {
			n++
		}
	}
	return n
}

// LoudCount returns the number of live subscriptions that are not quiet: the
// session's viewers.
func (h *Hub) LoudCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, s := range h.subs {
		if s.Reason() == nil && !s.quiet {
			n++
		}
	}
	return n
}

// Roster lists every live subscription, oldest first, the quiet ones marked.
func (h *Hub) Roster() []proto.ViewerInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]proto.ViewerInfo, 0, len(h.subs))
	for _, s := range h.subs {
		if s.Reason() == nil {
			out = append(out, s.Info())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since == out[j].Since {
			return out[i].ID < out[j].ID
		}
		return out[i].Since < out[j].Since
	})
	return out
}

// CountByLink counts live subscriptions per share link id.
func (h *Hub) CountByLink() map[string]int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := map[string]int{}
	for _, s := range h.subs {
		if s.LinkID != "" && s.Reason() == nil {
			out[s.LinkID]++
		}
	}
	return out
}

// Each calls fn for every subscription.
func (h *Hub) Each(fn func(*Subscription)) {
	h.mu.RLock()
	list := make([]*Subscription, 0, len(h.subs))
	for _, s := range h.subs {
		list = append(list, s)
	}
	h.mu.RUnlock()
	for _, s := range list {
		fn(s)
	}
}
