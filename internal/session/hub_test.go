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

// Drained is what a host asks before it closes the connections its clients
// read from: nothing sent to a client may still be queued, and nothing may be
// half written. A frame that was taken off the queue and is being written
// still counts.
func TestHubDrainedWaitsForQueuedAndInFlightFrames(t *testing.T) {
	h := NewHub()
	if !h.Drained() {
		t.Fatal("an empty hub is drained")
	}
	sink := newChanSink(true)
	h.add(newSubscription("s", RoleView, "", sink))
	if !h.Drained() {
		t.Fatal("a hub with nothing sent is drained")
	}
	h.Broadcast([]byte{1, 'a'})
	// The sleep lets the subscription take the frame off its queue and block
	// in WriteFrame, so that the check below usually sees the frame in flight.
	// Nothing depends on it: unsent counts a frame from send until WriteFrame
	// returns, so Drained is false whether the frame is still queued or
	// already being written, and a slow scheduler cannot make the check fail.
	time.Sleep(20 * time.Millisecond)
	if h.Drained() {
		t.Fatal("drained with a frame being written")
	}
	h.Broadcast([]byte{1, 'b'})
	if h.Drained() {
		t.Fatal("drained with a frame being written and one queued")
	}
	close(sink.block)
	deadline := time.Now().Add(3 * time.Second)
	for !h.Drained() {
		if time.Now().After(deadline) {
			t.Fatal("never drained")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if sink.count() != 2 {
		t.Fatalf("drained after %d of 2 frames were written", sink.count())
	}
}

// A subscription that has ended will write nothing more, so it cannot hold a
// host up.
func TestHubDrainedIgnoresEndedSubscriptions(t *testing.T) {
	h := NewHub()
	sink := newChanSink(true)
	sub := newSubscription("s", RoleView, "", sink)
	h.add(sub)
	h.Broadcast([]byte{1, 'a'})
	h.Broadcast([]byte{1, 'b'})
	sub.closeWith(nil)
	if !h.Drained() {
		t.Fatal("an ended subscription still holds the hub back")
	}
	close(sink.block)
}
