package signal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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
// messages still go: an answer to the host. A viewer joining is the viewer's
// own and waits its turn, without closing the host's connection; one closed
// while it waits stops waiting, its leave waits for room in turn, and both
// stop when the host's connection closes.
func TestServerMessagesKeepTheirRoomInTheHostQueue(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v := relayedViewer(t, hs, conn, "0123456789abcdef")
	sent := fillFromViewer(hs, v, 2*hostQueueBytes)
	waitQueued(t, conn, hostViewerBytes-(1+proto.ViewerIDLen+1+proto.MaxInput))
	before := len(conn.Send)
	if err := hs.Tell(proto.ErrorMsg{T: proto.HostError, Code: "not_found", Message: "no such link", RequestID: "r1"}); err != nil {
		t.Fatalf("the server's answer to its host: %v", err)
	}
	if len(conn.Send) != before+1 {
		t.Fatalf("%d messages queued, want %d", len(conn.Send), before+1)
	}
	joined := make(chan error, 1)
	go func() {
		_, err := hs.AddViewer("fedcba9876543210", session.RoleView, "", "")
		joined <- err
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-joined:
		t.Fatalf("a viewer joined past a full share: %v", err)
	default:
	}
	select {
	case <-conn.Done():
		t.Fatal("the host's connection was closed")
	default:
	}
	// Closed while its join waits: the join stops waiting; its leave waits.
	w := hs.viewer("fedcba9876543210")
	if w == nil {
		t.Fatal("the joining viewer is not registered")
	}
	removed := make(chan struct{})
	go func() { hs.RemoveViewer(w); close(removed) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("a closed viewer's join kept waiting for room")
	}
	if len(conn.Send) != before+1 {
		t.Fatalf("%d messages queued, want %d", len(conn.Send), before+1)
	}
	// Both stop when the host's connection closes.
	conn.Close()
	select {
	case <-removed:
	case <-time.After(5 * time.Second):
		t.Fatal("a leave kept waiting on a closed connection")
	}
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("a viewer kept waiting on a closed connection")
	}
}

// Viewers waiting for room in the host's queue get it in their order: a large
// frame is not held back for ever by small ones that another viewer keeps
// sending while the host drains slowly.
func TestViewersGetRoomInTheHostQueueInTheirOrder(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	small := relayedViewer(t, hs, conn, "0123456789abcdef")
	large := relayedViewer(t, hs, conn, "fedcba9876543210")
	piece := bytes.Repeat([]byte("s"), 4<<10)
	go func() {
		for hs.RelayToHost(small, proto.Frame{Type: proto.TypeInput, Payload: piece}) == nil {
		}
	}()
	defer hs.RemoveViewer(small)
	waitQueued(t, conn, hostViewerBytes-(1+proto.ViewerIDLen+1+len(piece)))
	// The host takes a message a millisecond.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
			select {
			case o := <-conn.Send:
				conn.Took(o)
			default:
			}
		}
	}()
	sent := make(chan error, 1)
	go func() {
		sent <- hs.RelayToHost(large, proto.Frame{Type: proto.TypeInput, Payload: bytes.Repeat([]byte("L"), proto.MaxInput)})
	}()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a 32 KiB frame waited 3 s for room while 4 KiB frames kept taking it")
	}
}

// A viewer whose connection fails, as a write to it does, is closed: a frame
// of its that waits for room in the host's queue stops waiting, and the
// viewer counts no more.
func TestAViewerWhoseWriteFailsStopsWaitingForTheHost(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v := relayedViewer(t, hs, conn, "0123456789abcdef")
	sent := fillFromViewer(hs, v, 2*hostQueueBytes)
	waitQueued(t, conn, hostViewerBytes-(1+proto.ViewerIDLen+1+proto.MaxInput))
	hs.HostRelayFrame(v.ID, proto.Frame{Type: proto.TypeOutput, Payload: []byte("o")})
	pumped := make(chan struct{})
	go func() { v.Pump(failSink{}); close(pumped) }()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("a viewer whose connection failed kept waiting for room")
	}
	<-pumped
	if v.Reason() == nil {
		t.Fatal("a viewer whose write failed is not closed")
	}
}

// failSink fails every write.
type failSink struct{}

func (failSink) WriteFrame([]byte) error { return errors.New("connection reset") }
func (failSink) Close(error)             {}

// Viewers coming and going while the viewers' share of the host's queue is
// full never close the host's connection: each join and leave waits for room
// in its turn, as the viewer's other messages do, and every one reaches the
// host, each viewer's join before its leave, once the host reads.
func TestViewersComingAndGoingNeverCloseTheHost(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v := relayedViewer(t, hs, conn, "0123456789abcdef")
	flooded := fillFromViewer(hs, v, 2*hostQueueBytes)
	waitQueued(t, conn, hostViewerBytes-(1+proto.ViewerIDLen+1+proto.MaxInput))
	// More joins and leaves than the rest of the queue holds, by as many
	// viewers at a time as the session takes.
	const churn = 4500
	var wg sync.WaitGroup
	var next, joined atomic.Int64
	for range hs.maxViewers - 1 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := next.Add(1)
				if i > churn {
					return
				}
				w, err := hs.AddViewer(fmt.Sprintf("c%015d", i), session.RoleView, "", "")
				if err != nil {
					t.Errorf("viewer %d: %v", i, err)
					return
				}
				joined.Add(1)
				hs.RemoveViewer(w)
			}
		}()
	}
	time.Sleep(500 * time.Millisecond)
	select {
	case <-conn.Done():
		t.Fatal("viewers coming and going closed the host's connection")
	default:
	}
	// The host reads: every join and leave arrives, the join first.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	seen := map[string]string{}
	finished := false
	deadline := time.After(30 * time.Second)
	for !finished {
		select {
		case o := <-conn.Send:
			conn.Took(o)
			if o.Text == nil {
				continue
			}
			var m struct{ T, ViewerID string }
			if json.Unmarshal(o.Text, &m) != nil || !strings.HasPrefix(m.ViewerID, "c") {
				continue
			}
			switch m.T {
			case proto.HostViewerJoin:
				seen[m.ViewerID] = "joined"
			case proto.HostViewerLeave:
				if seen[m.ViewerID] != "joined" {
					t.Fatalf("%s left before it joined", m.ViewerID)
				}
				seen[m.ViewerID] = "left"
			}
		case <-done:
			done = nil
			hs.RemoveViewer(v)
		case <-deadline:
			t.Fatalf("%d of %d viewers came and went", joined.Load(), churn)
		default:
			if done == nil && len(conn.Send) == 0 {
				finished = true
			}
		}
	}
	<-flooded
	select {
	case <-conn.Done():
		t.Fatal("viewers coming and going closed the host's connection")
	default:
	}
	left := 0
	for _, s := range seen {
		if s == "left" {
			left++
		}
	}
	if joined.Load() != churn || left != churn {
		t.Fatalf("%d joined, %d told the host they left, of %d", joined.Load(), left, churn)
	}
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

// A viewer's Charge is told what each of its messages brings the host's
// connection: its join, a relayed frame in its envelope (eighteen bytes for
// an empty INPUT of one), an offer as encoded for the host (six bytes for
// each '<' of the viewer's); a Charge that fails drops the message, and its
// error comes back.
func TestAViewersChargeIsToldWhatGoesToTheHost(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	var told []int
	var refuse error
	v, err := hs.AddViewerWith(ViewerOptions{ID: "0123456789abcdef", Role: session.RoleControl, Charge: func(n int) error {
		told = append(told, n)
		return refuse
	}})
	if err != nil {
		t.Fatal(err)
	}
	took := func(what string) Outbound {
		t.Helper()
		select {
		case o := <-conn.Send:
			conn.Took(o)
			if len(told) == 0 || told[len(told)-1] != o.size() {
				t.Fatalf("%s: %d bytes went to the host, Charge was told %v", what, o.size(), told)
			}
			return o
		case <-time.After(time.Second):
			t.Fatalf("%s: nothing went to the host", what)
		}
		return Outbound{}
	}
	if tt, _ := proto.ParseHeader(took("the join").Text); tt != proto.HostViewerJoin {
		t.Fatalf("the first message to the host is %q", tt)
	}
	if err := hs.StartRelay(v); err != nil {
		t.Fatal(err)
	}
	took("relay_start")
	if err := hs.RelayToHost(v, proto.Frame{Type: proto.TypeInput}); err != nil {
		t.Fatal(err)
	}
	if o := took("an empty input"); o.size() != 1+proto.ViewerIDLen+1 {
		t.Fatalf("an empty input took %d bytes to the host", o.size())
	}
	sdp := strings.Repeat("<", 1000)
	if err := hs.ForwardOffer(v, sdp); err != nil {
		t.Fatal(err)
	}
	if o := took("an offer"); o.size() < 6*len(sdp) {
		t.Fatalf("an offer of %d bytes took %d to the host", len(sdp), o.size())
	}
	if err := hs.ForwardICE(v, proto.ICECandidate{Candidate: "candidate:1 1 udp 1 127.0.0.1 9 typ host"}); err != nil {
		t.Fatal(err)
	}
	took("a candidate")
	refuse = errors.New("over budget")
	if err := hs.RelayToHost(v, proto.Frame{Type: proto.TypeInput, Payload: []byte("x")}); !errors.Is(err, refuse) {
		t.Fatalf("relay past a failing Charge: %v", err)
	}
	if err := hs.ForwardOffer(v, "v=0"); !errors.Is(err, refuse) {
		t.Fatalf("offer past a failing Charge: %v", err)
	}
	if q := conn.queued(); q != 0 || len(conn.Send) != 0 {
		t.Fatalf("%d bytes in %d messages went past a failing Charge", q, len(conn.Send))
	}
}
