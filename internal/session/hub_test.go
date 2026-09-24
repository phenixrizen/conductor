package session

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// chanSink collects frames; a nil-buffered release channel can block writes.
type chanSink struct {
	mu      sync.Mutex
	frames  [][]byte
	block   chan struct{}
	closed  chan error
	writeCh chan struct{}
}

func newChanSink(block bool) *chanSink {
	s := &chanSink{closed: make(chan error, 1), writeCh: make(chan struct{}, 1024)}
	if block {
		s.block = make(chan struct{})
	}
	return s
}

func (s *chanSink) WriteFrame(f []byte) error {
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	s.frames = append(s.frames, f)
	s.mu.Unlock()
	select {
	case s.writeCh <- struct{}{}:
	default:
	}
	return nil
}

func (s *chanSink) Close(reason error) { s.closed <- reason }

func (s *chanSink) frame(i int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frames[i]
}

func (s *chanSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames)
}

func (s *chanSink) waitFrames(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for s.count() < n {
		select {
		case <-s.writeCh:
		case <-deadline:
			t.Fatalf("timeout waiting for %d frames, have %d", n, s.count())
		}
	}
}

func TestHubFanOut(t *testing.T) {
	h := NewHub()
	a, b := newChanSink(false), newChanSink(false)
	sa := newSubscription("a", RoleView, "", a)
	sb := newSubscription("b", RoleControl, "l1", b)
	h.add(sa)
	h.add(sb)
	h.Broadcast([]byte{1, 'x'})
	h.Broadcast([]byte{1, 'y'})
	a.waitFrames(t, 2)
	b.waitFrames(t, 2)
	if h.Count() != 2 {
		t.Fatalf("count %d", h.Count())
	}
	h.remove("a")
	sa.closeWith(nil)
	if h.Count() != 1 {
		t.Fatalf("count after remove %d", h.Count())
	}
}

func TestSlowConsumerEviction(t *testing.T) {
	h := NewHub()
	slow := newChanSink(true)
	sub := newSubscription("slow", RoleView, "", slow)
	h.add(sub)
	frame := make([]byte, 64<<10)
	for i := 0; i < 40; i++ { // 2.5 MiB > high-water mark
		h.Broadcast(frame)
	}
	select {
	case reason := <-slow.closed:
		if !errors.Is(reason, ErrSlowConsumer) {
			t.Fatalf("reason %v", reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("slow consumer was not evicted")
	}
	if h.Count() != 0 {
		t.Fatalf("evicted subscription still counted")
	}
	close(slow.block)
}

func TestSinkWriteErrorClosesSubscription(t *testing.T) {
	sink := &errSink{closed: make(chan error, 1)}
	sub := newSubscription("e", RoleView, "", sink)
	sub.send([]byte{1})
	select {
	case r := <-sink.closed:
		if r == nil || r.Error() != "boom" {
			t.Fatalf("reason %v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not closed")
	}
}

type errSink struct{ closed chan error }

func (e *errSink) WriteFrame([]byte) error { return errors.New("boom") }
func (e *errSink) Close(reason error)      { e.closed <- reason }
