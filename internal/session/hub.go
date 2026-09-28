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
	done   chan struct{}
	once   sync.Once
	reason error

	inflight atomic.Int32 // file requests in progress
}

func newSubscription(id string, role Role, linkID string, sink Sink) *Subscription {
	s := &Subscription{ID: id, Role: role, LinkID: linkID, Since: time.Now().UTC(), sink: sink, queue: make(chan []byte, queueSlots), done: make(chan struct{})}
	go s.run()
	return s
}

// Info describes the subscription for the viewers roster.
func (s *Subscription) Info() proto.ViewerInfo {
	v := proto.ViewerInfo{ID: s.ID, Name: s.Name, Role: string(s.Role), Link: s.LinkLabel, Since: s.Since.Format(time.RFC3339)}
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
			if err := s.sink.WriteFrame(frame); err != nil {
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
	select {
	case s.queue <- frame:
		s.queued.Add(int64(len(frame)))
	default:
		s.closeWith(ErrSlowConsumer)
	}
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

// Roster lists every live subscription, oldest first.
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
