package session

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// heldSink is a viewer whose transport stops taking frames: WriteFrame waits
// for writes to open (a full send buffer), and Close says it began and then
// waits for closes to open (an error frame and a close handshake on that
// same connection).
type heldSink struct {
	writes   chan struct{}
	closes   chan struct{}
	entered  chan error
	returned atomic.Bool
}

func newHeldSink() *heldSink {
	return &heldSink{writes: make(chan struct{}), closes: make(chan struct{}), entered: make(chan error, 1)}
}

func (s *heldSink) WriteFrame([]byte) error { <-s.writes; return nil }
func (s *heldSink) Close(reason error) {
	s.entered <- reason
	<-s.closes
	s.returned.Store(true)
}

// countSink is a viewer that takes every frame at once and counts the output
// it was handed, noting a marker in it.
type countSink struct {
	mu     sync.Mutex
	out    int
	last   time.Time
	marker bool
}

func (s *countSink) WriteFrame(f []byte) error {
	fr, err := proto.Decode(f)
	if err == nil && fr.Type == proto.TypeOutput {
		s.mu.Lock()
		s.out += len(fr.Payload)
		s.last = time.Now()
		if bytes.Contains(fr.Payload, []byte("MARKER")) {
			s.marker = true
		}
		s.mu.Unlock()
	}
	return nil
}
func (s *countSink) Close(error) {}

func (s *countSink) sawMarker() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.marker
}

// within runs fn and reports whether it returned within d. It does not wait
// for fn beyond d.
func within(d time.Duration, fn func()) bool {
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// waitFor polls cond until it holds or d passes.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}

// A viewer that stops reading while the agent prints is dropped as a slow
// consumer, and its transport, which takes its time to close, holds up nobody:
// the other viewers keep receiving the output, and the session, its registry
// and the agent's own writes carry on, while that transport's write and then
// its close wait. The close comes once, after the write, with the reason.
func TestAViewerThatStopsReadingHoldsUpNobody(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	reg := NewRegistry(4)
	if err := reg.Add(s); err != nil {
		t.Fatal(err)
	}
	healthy := &countSink{}
	hsub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "healthy", Cols: 80, Rows: 24}, healthy)
	if err != nil {
		t.Fatal(err)
	}
	held := newHeldSink()
	hs, err := s.AttachWith(AttachOptions{Role: RoleView, LinkID: "held", Name: "held"}, held)
	if err != nil {
		t.Fatal(err)
	}
	defer close(held.closes)
	writesOpen := false
	defer func() {
		if !writesOpen {
			close(held.writes)
		}
	}()
	const total = 4 << 20
	go func() {
		chunk := bytes.Repeat([]byte("x"), proto.MaxOutput)
		for n := 0; n < total; n += len(chunk) {
			if _, err := p.outW.Write(chunk); err != nil {
				return
			}
		}
		p.outW.Write([]byte("MARKER"))
	}()
	if !waitFor(5*time.Second, func() bool { return errors.Is(hs.Reason(), ErrSlowConsumer) }) {
		t.Fatal("the viewer that stopped reading was never dropped")
	}
	// Its write is still waiting: everything else goes on.
	if !waitFor(5*time.Second, healthy.sawMarker) {
		t.Fatal("the healthy viewer stopped receiving the output")
	}
	third := &countSink{}
	ops := []struct {
		name string
		fn   func()
	}{
		{"Input", func() { _ = s.Input(hsub, []byte("a")) }},
		{"Resize", func() { _ = s.Resize(hsub, 100, 30) }},
		{"AttachWith", func() { _, _ = s.AttachWith(AttachOptions{Role: RoleView, Name: "third"}, third) }},
		{"Info", func() { _ = s.Info() }},
		{"LastOutputAt", func() { _ = s.LastOutputAt() }},
		{"Record", func() { s.Record(ActivityEntry{Type: ActivityToolUse, Message: "probe"}) }},
		{"SetAttention", func() { s.SetAttention(AttentionNeedsInput, "probe", SourceAdmin) }},
		{"DisconnectLink", func() { s.DisconnectLink("another-link") }},
		{"Registry.List", func() { _ = reg.List() }},
	}
	for _, o := range ops {
		if !within(time.Second, o.fn) {
			t.Errorf("%s waited on the dropped viewer's transport", o.name)
		}
	}
	select {
	case r := <-held.entered:
		t.Fatalf("the transport was closed (%v) while its write was in progress", r)
	default:
	}
	// The write gives up; the close follows, with the reason, and still holds up nobody.
	close(held.writes)
	writesOpen = true
	select {
	case r := <-held.entered:
		if !errors.Is(r, ErrSlowConsumer) {
			t.Fatalf("closed for %v, want a slow consumer", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the dropped viewer's transport was never closed")
	}
	if !within(time.Second, func() { _ = s.Info(); _ = s.Input(hsub, []byte("b")) }) {
		t.Error("the session waited on the dropped viewer's close")
	}
}

// A revoke closes the link's viewers, and returns once their transports have
// closed; meanwhile the session carries on: a transport that takes its time
// to close holds up neither the output, nor the input, nor the session's
// description.
func TestARevokeWaitsForTheTransportAndTheSessionDoesNot(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	healthy := &countSink{}
	hsub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "healthy", Cols: 80, Rows: 24}, healthy)
	if err != nil {
		t.Fatal(err)
	}
	held := newHeldSink()
	close(held.writes) // it takes frames; only its close is slow
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, LinkID: "revoked", Name: "revoked"}, held); err != nil {
		t.Fatal(err)
	}
	revoked := make(chan struct{})
	go func() { s.DisconnectLink("revoked"); close(revoked) }()
	select {
	case r := <-held.entered:
		if !errors.Is(r, ErrRevoked) {
			t.Fatalf("closed for %v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the revoked viewer's transport was never closed")
	}
	time.Sleep(50 * time.Millisecond) // the viewer's own goroutine leaves the session meanwhile
	if !within(time.Second, func() { _ = s.Input(hsub, []byte("a")) }) {
		t.Error("input waited on the revoked viewer's close")
	}
	if !within(time.Second, func() { _ = s.Info() }) {
		t.Error("Info waited on the revoked viewer's close")
	}
	go p.outW.Write([]byte("MARKER after the revoke"))
	if !waitFor(time.Second, healthy.sawMarker) {
		t.Error("the output waited on the revoked viewer's close")
	}
	select {
	case <-revoked:
		t.Fatal("the revoke returned before the transport closed")
	default:
	}
	close(held.closes)
	select {
	case <-revoked:
	case <-time.After(3 * time.Second):
		t.Fatal("the revoke never returned")
	}
	if !held.returned.Load() {
		t.Fatal("the revoke returned before the transport's close did")
	}
}

// timedCloseSink takes frames at once; its Close takes closeFor and notes when
// it returned.
type timedCloseSink struct {
	closeFor time.Duration
	closedAt atomic.Int64
}

func (s *timedCloseSink) WriteFrame([]byte) error { return nil }
func (s *timedCloseSink) Close(error) {
	time.Sleep(s.closeFor)
	s.closedAt.Store(time.Now().UnixNano())
}

// A link's viewer that the session ended already (dropped as a slow
// consumer) has left the roster while its transport still closes: a revoke
// of the link waits for that transport too, as a shutdown does.
func TestARevokeWaitsForALinksViewerDroppedBefore(t *testing.T) {
	for _, end := range []string{"revoke", "shutdown"} {
		t.Run(end, func(t *testing.T) {
			s, p := newLocal(t, t.TempDir())
			held := newHeldSink()
			sub, err := s.AttachWith(AttachOptions{Role: RoleControl, LinkID: "L", Name: "dropped"}, held)
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				chunk := bytes.Repeat([]byte("x"), proto.MaxOutput)
				for range 2 * HighWaterMark / len(chunk) {
					if _, err := p.outW.Write(chunk); err != nil {
						return
					}
				}
			}()
			if !waitFor(5*time.Second, func() bool { return errors.Is(sub.Reason(), ErrSlowConsumer) }) {
				t.Fatal("the viewer was never dropped")
			}
			if !waitFor(time.Second, func() bool { return s.Viewers() == 0 }) {
				t.Fatal("the dropped viewer stayed on the roster")
			}
			ended := make(chan struct{})
			go func() {
				if end == "revoke" {
					s.DisconnectLink("L")
				} else {
					s.CloseAll(ErrSessionEnded)
				}
				close(ended)
			}()
			select {
			case <-ended:
				t.Fatalf("the %s returned while the dropped viewer's transport was still open", end)
			case <-time.After(200 * time.Millisecond):
			}
			close(held.writes)
			if r := <-held.entered; !errors.Is(r, ErrSlowConsumer) {
				t.Fatalf("closed for %v", r)
			}
			select {
			case <-ended:
				t.Fatalf("the %s returned before the transport's close did", end)
			case <-time.After(100 * time.Millisecond):
			}
			close(held.closes)
			select {
			case <-ended:
			case <-time.After(3 * time.Second):
				t.Fatalf("the %s never returned", end)
			}
		})
	}
}

// A client the session dropped counts toward MaxViewers until its transport
// has closed: dropping and attaching again holds at most MaxViewers
// connections open at once.
func TestAClientStillClosingCountsTowardTheViewerCap(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, MaxViewers: 2})
	held := newHeldSink()
	sub, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "dropped"}, held)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		chunk := bytes.Repeat([]byte("x"), proto.MaxOutput)
		for range 2 * HighWaterMark / len(chunk) {
			if _, err := p.outW.Write(chunk); err != nil {
				return
			}
		}
	}()
	if !waitFor(5*time.Second, func() bool { return errors.Is(sub.Reason(), ErrSlowConsumer) }) {
		t.Fatal("the viewer was never dropped")
	}
	if !waitFor(time.Second, func() bool { return s.Viewers() == 0 }) {
		t.Fatal("the dropped viewer stayed on the roster")
	}
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "second"}, newChanSink(false)); err != nil {
		t.Fatalf("the second viewer: %v", err)
	}
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "third"}, newChanSink(false)); !errors.Is(err, ErrTooManyViewers) {
		t.Fatalf("a third viewer while the dropped one's transport is open: %v", err)
	}
	close(held.writes)
	<-held.entered
	close(held.closes)
	<-sub.Closed()
	if !waitFor(time.Second, func() bool {
		_, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "third"}, newChanSink(false))
		return err == nil
	}) {
		t.Fatal("the slot of the closed transport never came back")
	}
}

// A second revoke of a link whose viewer's transport is still closing waits
// for it as the first does.
func TestARepeatedRevokeWaitsForTheTransportToo(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	held := newHeldSink()
	close(held.writes)
	if _, err := s.AttachWith(AttachOptions{Role: RoleControl, LinkID: "L", Name: "revoked"}, held); err != nil {
		t.Fatal(err)
	}
	first := make(chan struct{})
	go func() { s.DisconnectLink("L"); close(first) }()
	<-held.entered
	if !waitFor(time.Second, func() bool { return s.Viewers() == 0 }) {
		t.Fatal("the revoked viewer stayed on the roster")
	}
	second := make(chan struct{})
	go func() { s.DisconnectLink("L"); close(second) }()
	select {
	case <-first:
		t.Fatal("the first revoke returned before the transport closed")
	case <-second:
		t.Fatal("the second revoke returned before the transport closed")
	case <-time.After(200 * time.Millisecond):
	}
	close(held.closes)
	for _, ch := range []chan struct{}{first, second} {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("a revoke never returned")
		}
	}
	closing := func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.closing)
	}
	if !waitFor(time.Second, func() bool { return closing() == 0 }) {
		t.Fatalf("%d clients still kept among the closing after their transports closed", closing())
	}
}

// A revoke closes the link's viewers together: it takes as long as the
// slowest transport's close, not the sum of them, and returns after every one.
func TestARevokeClosesTheLinksViewersTogether(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	const per = 300 * time.Millisecond
	var sinks []*timedCloseSink
	for i := range 3 {
		sink := &timedCloseSink{closeFor: per}
		sinks = append(sinks, sink)
		if _, err := s.AttachWith(AttachOptions{Role: RoleView, LinkID: "revoked", Name: fmt.Sprintf("v%d", i)}, sink); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	s.DisconnectLink("revoked")
	took := time.Since(start)
	returned := time.Now().UnixNano()
	for i, sink := range sinks {
		at := sink.closedAt.Load()
		if at == 0 || at > returned {
			t.Fatalf("viewer %d: the revoke returned before its transport closed", i)
		}
	}
	if took >= 2*per {
		t.Fatalf("three closes of %v each took %v: one after another", per, took)
	}
}

// gateSink writes at once until it is gated, and then holds each write until
// the gate opens; it counts the writes that began after its Close returned.
type gateSink struct {
	gate       chan struct{}
	gated      atomic.Bool
	waiting    atomic.Bool
	closed     atomic.Bool
	afterClose atomic.Int32
	writes     atomic.Int32
}

func (s *gateSink) WriteFrame([]byte) error {
	if s.closed.Load() {
		s.afterClose.Add(1)
	}
	s.writes.Add(1)
	if s.gated.Load() {
		s.waiting.Store(true)
		<-s.gate
	}
	return nil
}
func (s *gateSink) Close(error) { s.closed.Store(true) }

// Once a viewer is revoked, the frames still queued for it are not written,
// and nothing is written after its transport's Close: the frame being written
// as the revoke lands is the last.
func TestNothingIsWrittenToARevokedViewer(t *testing.T) {
	for range 20 {
		s, _ := newLocal(t, t.TempDir())
		sink := &gateSink{gate: make(chan struct{})}
		sub, err := s.AttachWith(AttachOptions{Role: RoleView, LinkID: "L", Name: "v"}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if !waitFor(time.Second, func() bool { return s.hub.Drained() }) {
			t.Fatal("the attach frames were never written")
		}
		sink.gated.Store(true)
		for range 50 {
			s.Send(sub, proto.Encode(proto.TypeOutput, []byte("queued")))
		}
		if !waitFor(time.Second, sink.waiting.Load) {
			t.Fatal("no write began")
		}
		before := sink.writes.Load()
		revoked := make(chan struct{})
		go func() { s.DisconnectLink("L"); close(revoked) }()
		if !waitFor(time.Second, func() bool { return sub.Reason() != nil }) {
			t.Fatal("the viewer was not revoked")
		}
		close(sink.gate)
		select {
		case <-revoked:
		case <-time.After(3 * time.Second):
			t.Fatal("the revoke never returned")
		}
		time.Sleep(10 * time.Millisecond)
		if n := sink.afterClose.Load(); n > 0 {
			t.Fatalf("%d frames were written after the transport's Close", n)
		}
		if n := sink.writes.Load() - before; n > 0 {
			t.Fatalf("%d queued frames were written after the revoke", n)
		}
	}
}

// endSink holds each write, once gated, until the gate opens, and keeps the
// frames it was written, the reason it was closed with, and how many writes
// came after its Close.
type endSink struct {
	gate    chan struct{}
	gated   atomic.Bool
	waiting atomic.Bool

	mu         sync.Mutex
	frames     [][]byte
	closed     bool
	reason     error
	afterClose int
}

func (s *endSink) WriteFrame(f []byte) error {
	if s.gated.Load() {
		s.waiting.Store(true)
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.afterClose++
	}
	s.frames = append(s.frames, f)
	return nil
}

func (s *endSink) Close(reason error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed, s.reason = true, reason
}

// A link's viewer that the session's end closes (its link ends with it) is
// still written what was queued for it, in order, the session's status
// after its output, before its transport's Close, which says the session
// ended; a revoked one is not (TestNothingIsWrittenToARevokedViewer).
func TestTheSessionsEndWritesALinksViewerWhatWasQueued(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	sink := &endSink{gate: make(chan struct{})}
	sub, err := s.AttachWith(AttachOptions{Role: RoleView, LinkID: "L", Name: "v"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if !waitFor(time.Second, func() bool { return s.hub.Drained() }) {
		t.Fatal("the attach frames were never written")
	}
	sink.mu.Lock()
	attached := len(sink.frames)
	sink.mu.Unlock()
	sink.gated.Store(true)
	for i := range 50 {
		s.Send(sub, proto.Encode(proto.TypeOutput, fmt.Appendf(nil, "queued %d", i)))
	}
	if !waitFor(time.Second, sink.waiting.Load) {
		t.Fatal("no write began")
	}
	p.exit()
	select {
	case <-s.Ended():
	case <-time.After(5 * time.Second):
		t.Fatal("the session never ended")
	}
	ended := make(chan struct{})
	go func() { s.DisconnectLink("L"); close(ended) }()
	if !waitFor(time.Second, func() bool { return sub.Reason() != nil }) {
		t.Fatal("the viewer was not closed")
	}
	if !errors.Is(sub.Reason(), ErrSessionEnded) {
		t.Fatalf("closed for %v", sub.Reason())
	}
	close(sink.gate)
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the end's close of the link never returned")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !sink.closed || !errors.Is(sink.reason, ErrSessionEnded) || sink.afterClose != 0 {
		t.Fatalf("closed %v with %v, %d writes after", sink.closed, sink.reason, sink.afterClose)
	}
	got := sink.frames[attached:]
	if len(got) < 51 {
		t.Fatalf("%d frames written after the attach, want the 50 queued and the status", len(got))
	}
	for i := range 50 {
		if f, err := proto.Decode(got[i]); err != nil || string(f.Payload) != fmt.Sprintf("queued %d", i) {
			t.Fatalf("frame %d is %q, want queued %d", i, got[i], i)
		}
	}
	status := false
	for _, f := range got[50:] {
		m := decodeControl(t, f)
		status = status || (m["t"] == proto.CtlStatus && m["status"] == string(StatusExited))
	}
	if !status {
		t.Fatal("the viewer was not written the session's status after its last output")
	}
}

// A client that takes over another's connection (a viewer moving to the
// relay) takes its slot while the old transport still closes; a client that
// replaces nobody, or names one still attached, does not.
func TestAClientReplacingAnotherTakesItsSlot(t *testing.T) {
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, MaxViewers: 1})
	held := newHeldSink()
	old, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "moving"}, held)
	if err != nil {
		t.Fatal(err)
	}
	s.Detach(old)
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "other"}, newChanSink(false)); !errors.Is(err, ErrTooManyViewers) {
		t.Fatalf("a new client while the old transport closes: %v", err)
	}
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "moving", Replaces: old}, newChanSink(false)); err != nil {
		t.Fatalf("the client that replaces it: %v", err)
	}
	close(held.writes)
	close(held.closes)
	<-old.Closed()
}

// A move seen before the old client's detach (its data channel's close
// still on its way): the replacement ends the old one where it stands and
// takes its slot, with no leave and no join, and the old one's own Detach
// after it changes nothing.
func TestAClientReplacingOneStillAttachedEndsItAndTakesItsSlot(t *testing.T) {
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, MaxViewers: 1})
	held := newHeldSink()
	old, err := s.AttachWith(AttachOptions{ID: "viewer1", Role: RoleView, Name: "moving"}, held)
	if err != nil {
		t.Fatal(err)
	}
	repl, err := s.AttachWith(AttachOptions{ID: "viewer1", Role: RoleView, Name: "moving", Replaces: old}, newChanSink(false))
	if err != nil {
		t.Fatalf("replacing a client still attached: %v", err)
	}
	select {
	case <-old.Done():
	default:
		t.Fatal("the old client did not end")
	}
	s.Detach(old)
	if s.Viewers() != 1 {
		t.Fatalf("viewers after the old one's Detach: %d", s.Viewers())
	}
	select {
	case <-repl.Done():
		t.Fatalf("the replacement ended: %v", repl.Reason())
	default:
	}
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "other"}, newChanSink(false)); !errors.Is(err, ErrTooManyViewers) {
		t.Fatalf("a third at the cap: %v", err)
	}
	close(held.writes)
	close(held.closes)
	<-old.Closed()
	if !waitFor(time.Second, func() bool {
		_, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "other"}, newChanSink(false))
		return errors.Is(err, ErrTooManyViewers)
	}) {
		t.Fatal("the cap gave room once the old transport closed: the replacement's slot was counted twice")
	}
}

// Two viewers moving at once at the cap each take over their own slot: a
// slot passed on stays passed for every later check, while the old
// transports still close.
func TestTwoClientsReplacingOthersAtTheCapEachTakeTheirSlot(t *testing.T) {
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, MaxViewers: 2})
	heldA, heldB := newHeldSink(), newHeldSink()
	a, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "a"}, heldA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "b"}, heldB)
	if err != nil {
		t.Fatal(err)
	}
	s.Detach(a)
	s.Detach(b)
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "a", Replaces: a}, newChanSink(false)); err != nil {
		t.Fatalf("a's replacement: %v", err)
	}
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "b", Replaces: b}, newChanSink(false)); err != nil {
		t.Fatalf("b's replacement: %v", err)
	}
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "c"}, newChanSink(false)); !errors.Is(err, ErrTooManyViewers) {
		t.Fatalf("a third at the cap: %v", err)
	}
	// The old ones' own late Detach (the watcher's) leaves the slots passed.
	s.Detach(a)
	s.Detach(b)
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "c"}, newChanSink(false)); !errors.Is(err, ErrTooManyViewers) {
		t.Fatalf("a third at the cap after a second Detach: %v", err)
	}
	// A slot is passed on once: naming a replaced one again gives nothing.
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "a again", Replaces: a}, newChanSink(false)); !errors.Is(err, ErrTooManyViewers) {
		t.Fatalf("a second replacement of a: %v", err)
	}
	for _, h := range []*heldSink{heldA, heldB} {
		close(h.writes)
		close(h.closes)
	}
	<-a.Closed()
	<-b.Closed()
}

// A client that replaced another under the same id (a viewer moving to the
// relay) stays attached when the old one's Detach comes after it.
func TestALateDetachOfTheOldClientLeavesItsReplacement(t *testing.T) {
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, MaxViewers: 2})
	held := newHeldSink()
	old, err := s.AttachWith(AttachOptions{ID: "viewer1", Role: RoleView, Name: "v"}, held)
	if err != nil {
		t.Fatal(err)
	}
	s.Detach(old)
	repl, err := s.AttachWith(AttachOptions{ID: "viewer1", Role: RoleView, Name: "v", Replaces: old}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	s.Detach(old)
	if s.Viewers() != 1 {
		t.Fatalf("viewers after the old one's late Detach: %d", s.Viewers())
	}
	select {
	case <-repl.Done():
		t.Fatalf("the replacement ended: %v", repl.Reason())
	default:
	}
	close(held.writes)
	close(held.closes)
	<-old.Closed()
}

// A Detach that finds the transport closed takes the client out of the
// closing and of the slots passed on at once: a check between it and the
// watcher's cleanup counts neither.
func TestADetachAfterTheTransportClosedClearsItsAccountTogether(t *testing.T) {
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, MaxViewers: 2})
	sub := newSubscription("x", RoleView, "", newChanSink(false))
	sub.closeWith(nil)
	<-sub.Closed()
	// As the books stand when its watcher has not run yet.
	s.mu.Lock()
	s.closing = map[*Subscription]struct{}{sub: {}}
	s.passedOn = map[*Subscription]struct{}{sub: {}}
	s.mu.Unlock()
	s.Detach(sub)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.closing) != 0 || len(s.passedOn) != 0 {
		t.Fatalf("closing %d, passed on %d", len(s.closing), len(s.passedOn))
	}
}

// A controller holding the size that moves (the same id, taken over where it
// stands) keeps the size, and its old subscription's late Detach does not
// hand it to another of the owner's windows.
func TestALateDetachLeavesTheSizeWithTheReplacement(t *testing.T) {
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, MaxViewers: 4})
	held := newHeldSink()
	old, err := s.AttachWith(AttachOptions{ID: "mover", Role: RoleControl, Name: "nate", Cols: 120, Rows: 40}, held)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	sizer := s.sizer
	s.mu.Unlock()
	if sizer != "mover" {
		t.Fatalf("sizer before the move: %q", sizer)
	}
	if _, err := s.AttachWith(AttachOptions{ID: "mover", Role: RoleControl, Name: "nate", Cols: 120, Rows: 40, Replaces: old}, newChanSink(false)); err != nil {
		t.Fatal(err)
	}
	// Another of the owner's windows, the last to attach: a handover would go to it.
	if _, err := s.AttachWith(AttachOptions{ID: "other", Role: RoleControl, Name: "nate", Cols: 100, Rows: 30}, newChanSink(false)); err != nil {
		t.Fatal(err)
	}
	s.Detach(old)
	s.mu.Lock()
	sizer = s.sizer
	s.mu.Unlock()
	if sizer != "mover" {
		t.Fatalf("sizer after the old one's late Detach: %q", sizer)
	}
	close(held.writes)
	close(held.closes)
	<-old.Closed()
}
