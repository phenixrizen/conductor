package signal

import (
	"bytes"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// relayedViewer adds a controller to hs and switches it to the relay, with
// the host told of both.
func relayedViewer(t *testing.T, hs *HostedSession, conn *HostConn, id string) *Viewer {
	t.Helper()
	v, err := hs.AddViewer(id, session.RoleControl, "", "")
	if err != nil {
		t.Fatal(err)
	}
	drainText(t, conn, proto.HostViewerJoin)
	if err := hs.StartRelay(v); err != nil {
		t.Fatal(err)
	}
	drainText(t, conn, proto.HostRelayStart)
	return v
}

// fillFromViewer relays MaxInput frames from v until total bytes have gone,
// or a relay returns an error, and sends what went on the channel it returns.
func fillFromViewer(hs *HostedSession, v *Viewer, total int) <-chan int {
	chunk := bytes.Repeat([]byte("x"), proto.MaxInput)
	sent := make(chan int, 1)
	go func() {
		n := 0
		for n < total {
			if err := hs.RelayToHost(v, proto.Frame{Type: proto.TypeInput, Payload: chunk}); err != nil {
				break
			}
			n += len(chunk)
		}
		sent <- n
	}()
	return sent
}

// waitQueued waits until conn's queue holds at least n bytes.
func waitQueued(t *testing.T, conn *HostConn, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for conn.queued() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d bytes queued, want %d", conn.queued(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A viewer that sends more than the host reads fills the viewers' share of
// the host's queue and then waits for room: the queue's bytes never pass
// their bound, the host's connection stays open, and what the writer takes
// lets the viewer go on, every frame whole and in order.
func TestRelayedFramesWaitForRoomInTheHostQueue(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v := relayedViewer(t, hs, conn, "0123456789abcdef")
	const total = 2 * hostQueueBytes
	sent := fillFromViewer(hs, v, total)
	envSize := 1 + proto.ViewerIDLen + 1 + proto.MaxInput
	waitQueued(t, conn, hostViewerBytes-envSize)
	time.Sleep(100 * time.Millisecond)
	select {
	case n := <-sent:
		t.Fatalf("%d bytes were queued for a host that reads nothing", n)
	default:
	}
	if q := conn.queued(); q > hostViewerBytes || len(conn.Send)*envSize != q {
		t.Fatalf("%d bytes queued in %d messages, at most %d", q, len(conn.Send), hostViewerBytes)
	}
	select {
	case <-conn.Done():
		t.Fatal("a viewer that sends more than the host reads closed the host's connection")
	default:
	}
	// The writer takes what is queued: the viewer goes on to the end.
	got := 0
	for got < total {
		select {
		case o := <-conn.Send:
			conn.Took(o)
			if q := conn.queued(); q > hostViewerBytes {
				t.Fatalf("%d bytes queued", q)
			}
			id, inner, err := proto.DecodeRelay(o.Binary[1:])
			if err != nil || id != v.ID || inner.Type != proto.TypeInput || len(inner.Payload) != proto.MaxInput {
				t.Fatalf("envelope %q %d %d %v", id, inner.Type, len(inner.Payload), err)
			}
			got += len(inner.Payload)
		case <-time.After(5 * time.Second):
			t.Fatalf("the viewer stopped after %d of %d bytes", got, total)
		}
	}
	if n := <-sent; n != total {
		t.Fatalf("the viewer sent %d of %d bytes", n, total)
	}
	if conn.queued() != 0 {
		t.Fatalf("%d bytes left counted in an empty queue", conn.queued())
	}
}

// While the viewers' share of the host's queue is full, the server's own
// messages still go: a viewer joining, an answer to the host. A viewer that
// is closed while it waits stops waiting, and so does one whose host's
// connection closes.
func TestServerMessagesKeepTheirRoomInTheHostQueue(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v := relayedViewer(t, hs, conn, "0123456789abcdef")
	sent := fillFromViewer(hs, v, 2*hostQueueBytes)
	waitQueued(t, conn, hostViewerBytes-(1+proto.ViewerIDLen+1+proto.MaxInput))
	before := len(conn.Send)
	if _, err := hs.AddViewer("fedcba9876543210", session.RoleView, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := hs.Tell(proto.ErrorMsg{T: proto.HostError, Code: "not_found", Message: "no such link", RequestID: "r1"}); err != nil {
		t.Fatalf("the server's answer to its host: %v", err)
	}
	if len(conn.Send) != before+2 {
		t.Fatalf("%d messages queued, want %d", len(conn.Send), before+2)
	}
	select {
	case <-conn.Done():
		t.Fatal("the host's connection was closed")
	default:
	}
	// A second viewer waits as well, and stops waiting when it is closed.
	w := relayedViewerNoDrain(t, hs, "aaaaaaaaaaaaaaaa")
	other := fillFromViewer(hs, w, proto.MaxInput)
	time.Sleep(50 * time.Millisecond)
	hs.RemoveViewer(w)
	select {
	case n := <-other:
		if n != 0 {
			t.Fatalf("a closed viewer sent %d bytes", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a closed viewer kept waiting for room")
	}
	// The first stops when the host's connection closes.
	conn.Close()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("a viewer kept waiting on a closed connection")
	}
}

// relayedViewerNoDrain is relayedViewer for a host queue that is full: the
// join and relay_start are queued among the rest.
func relayedViewerNoDrain(t *testing.T, hs *HostedSession, id string) *Viewer {
	t.Helper()
	v, err := hs.AddViewer(id, session.RoleControl, "", "")
	if err != nil {
		t.Fatal(err)
	}
	v.relay.Store(true)
	return v
}

// The frames queued to a viewer are bounded in bytes as well as in number:
// one that falls further behind is closed as too slow, and the frames it
// was owed leave the count as Pump takes them.
func TestViewerQueueIsBoundedInBytes(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v, err := hs.AddViewer("0123456789abcdef", session.RoleView, "", "")
	if err != nil {
		t.Fatal(err)
	}
	drainText(t, conn, proto.HostViewerJoin)
	out := bytes.Repeat([]byte("o"), proto.MaxOutput)
	pushed := 0
	for v.Reason() == nil && pushed < 2*viewerQueue {
		hs.HostRelayFrame(v.ID, proto.Frame{Type: proto.TypeOutput, Payload: out})
		pushed++
	}
	if !errors.Is(v.Reason(), ErrSlowViewer) {
		t.Fatalf("after %d frames: %v", pushed, v.Reason())
	}
	if limit := viewerQueueBytes/(1+len(out)) + 1; pushed > limit {
		t.Fatalf("closed after %d frames of %d bytes, want at most %d", pushed, 1+len(out), limit)
	}
	if q := v.queued.Load(); q > viewerQueueBytes || q != int64(len(v.Out)*(1+len(out))) {
		t.Fatalf("%d bytes counted for %d frames queued", q, len(v.Out))
	}

	// A viewer that reads keeps up: frames written leave the count.
	w, err := hs.AddViewer("fedcba9876543210", session.RoleView, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sink := &countSink{}
	done := make(chan struct{})
	go func() { w.Pump(sink); close(done) }()
	for i := 0; i < 2*viewerQueueBytes/(1+len(out)); i++ {
		hs.HostRelayFrame(w.ID, proto.Frame{Type: proto.TypeOutput, Payload: out})
		for w.queued.Load() > viewerQueueBytes/2 && w.Reason() == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if w.Reason() != nil {
		t.Fatalf("a viewer that keeps up was closed: %v", w.Reason())
	}
	want := int64(2 * viewerQueueBytes / (1 + len(out)))
	deadline := time.Now().Add(5 * time.Second)
	for sink.frames.Load() < want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := sink.frames.Load(); got != want || w.queued.Load() != 0 {
		t.Fatalf("%d of %d frames written, %d bytes left counted", got, want, w.queued.Load())
	}
	hs.RemoveViewer(w)
	<-done
}

// countSink counts the frames written to it and keeps none.
type countSink struct{ frames atomic.Int64 }

func (s *countSink) WriteFrame([]byte) error { s.frames.Add(1); return nil }
func (s *countSink) Close(error)             {}
