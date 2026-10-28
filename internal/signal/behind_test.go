package signal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/proto"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// toHostNow is the message the host's queue holds now, or nil.
func toHostNow(t *testing.T, conn *HostConn) map[string]any {
	t.Helper()
	select {
	case o := <-conn.Send:
		conn.Took(o)
		var m map[string]any
		if err := json.Unmarshal(o.Text, &m); err != nil {
			t.Fatal(err)
		}
		return m
	default:
		return nil
	}
}

// A relayed viewer the server holds more than relayBehindBytes for is
// behind, and the host is told so once; as the viewer takes its frames it
// catches up at relayCaughtUpBytes, and the host is told that once; then
// again each time it falls behind and catches up. A viewer not relayed is
// never named, nor one closed.
func TestTheHostIsToldWhenARelayedViewerFallsBehindAndCatchesUp(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v := relayedViewer(t, hs, conn, "0123456789abcdef")
	out := bytes.Repeat([]byte("o"), proto.MaxOutput)
	frame := int64(1 + len(out))
	relay := func() { hs.HostRelayFrame(v.ID, proto.Frame{Type: proto.TypeOutput, Payload: out}) }
	expectBehind := func(want bool) {
		t.Helper()
		m := toHostNow(t, conn)
		if m == nil || m["t"] != proto.HostViewerBehind || m["viewerId"] != v.ID || m["behind"] != want {
			t.Fatalf("with %d bytes queued the host was told %v, want behind %v", v.queued.Load(), m, want)
		}
	}
	expectNothing := func() {
		t.Helper()
		if m := toHostNow(t, conn); m != nil {
			t.Fatalf("with %d bytes queued the host was told %v", v.queued.Load(), m)
		}
	}
	for round := range 2 {
		// Up to the mark, nothing; past it, behind, once.
		for v.queued.Load()+frame <= relayBehindBytes {
			relay()
		}
		expectNothing()
		relay()
		expectBehind(true)
		for range 8 {
			relay()
		}
		expectNothing()
		// The frames are taken one at a time, as Pump takes them: caught up
		// as the queue is down to relayCaughtUpBytes, and not before; what
		// is left is taken without another word to the host.
		caughtUp := false
		for len(v.Out) > 0 {
			f := <-v.Out
			v.queued.Add(-int64(len(f)))
			v.noteBacklog()
			switch {
			case v.queued.Load() > relayCaughtUpBytes || caughtUp:
				expectNothing()
			default:
				expectBehind(false)
				caughtUp = true
			}
		}
		if !caughtUp {
			t.Fatal("the viewer never caught up")
		}
		if round == 0 && v.queued.Load() != 0 {
			t.Fatalf("%d bytes left counted", v.queued.Load())
		}
	}

	// Not relayed: never named.
	w, err := hs.AddViewer("fedcba9876543210", session.RoleView, "", "")
	if err != nil {
		t.Fatal(err)
	}
	drainText(t, conn, proto.HostViewerJoin)
	for w.queued.Load() <= 2*relayBehindBytes {
		hs.HostRelayFrame(w.ID, proto.Frame{Type: proto.TypeOutput, Payload: out})
	}
	expectNothing()

	// Closed: never named again.
	for v.queued.Load() <= relayBehindBytes/2 {
		relay()
	}
	v.close(ErrHostGone)
	for range 64 {
		v.queued.Add(frame)
		v.noteBacklog()
	}
	expectNothing()
}

// Pump itself says the viewer caught up as it takes the frames, once.
func TestPumpTellsTheHostTheViewerCaughtUp(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v := relayedViewer(t, hs, conn, "0123456789abcdef")
	out := bytes.Repeat([]byte("o"), proto.MaxOutput)
	for v.queued.Load() <= relayBehindBytes {
		hs.HostRelayFrame(v.ID, proto.Frame{Type: proto.TypeOutput, Payload: out})
	}
	if m := nextToHost(t, conn); m["t"] != proto.HostViewerBehind || m["behind"] != true {
		t.Fatalf("the host was told %v", m)
	}
	sink := &countSink{}
	done := make(chan struct{})
	go func() { v.Pump(sink); close(done) }()
	if m := nextToHost(t, conn); m["t"] != proto.HostViewerBehind || m["behind"] != false || m["viewerId"] != v.ID {
		t.Fatalf("the host was told %v", m)
	}
	deadline := time.Now().Add(5 * time.Second)
	for v.queued.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m := toHostNow(t, conn); m != nil {
		t.Fatalf("the host was told %v as well", m)
	}
	hs.RemoveViewer(v)
	<-done
}

// The frame that puts a viewer behind can be taken as the server decides
// so, before it tells the host, as Pump takes a frame of a file larger
// than the mark: the take's look comes after the decision, and the host is
// told the viewer caught up, behind the word that it fell behind.
func TestACatchUpAsTheViewerFallsBehindReachesTheHost(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v := relayedViewer(t, hs, conn, "0123456789abcdef")
	big := bytes.Repeat([]byte("f"), relayBehindBytes+64<<10)
	looked := make(chan struct{})
	t.Cleanup(func() { behindSeam = nil })
	behindSeam = func(sv *Viewer) {
		behindSeam = nil
		// Taken as Pump takes it: off the queue, then a look, which is
		// given its time to finish before the decision goes on (it waits
		// for the decision, which is looked at under the same lock).
		f := <-sv.Out
		sv.queued.Add(-int64(len(f)))
		go func() {
			defer close(looked)
			sv.noteBacklog()
		}()
		select {
		case <-looked:
		case <-time.After(100 * time.Millisecond):
		}
	}
	hs.HostRelayFrame(v.ID, proto.Frame{Type: proto.TypeOutput, Payload: big})
	<-looked
	if m := nextToHost(t, conn); m["t"] != proto.HostViewerBehind || m["behind"] != true {
		t.Fatalf("the host was told %v, want behind", m)
	}
	if m := nextToHost(t, conn); m["t"] != proto.HostViewerBehind || m["behind"] != false {
		t.Fatalf("the host was told %v, want caught up", m)
	}
}

// Many viewers falling behind while the viewers' share of the host's queue
// is full take none of the room kept for the server's own messages: those
// still fill all of it without closing the connection, and each viewer's
// word goes once there is room in the share again.
func TestViewersBehindLeaveTheServersRoomInTheHostQueue(t *testing.T) {
	hub := NewHub(session.NewRegistry(4), nil)
	hs, conn := register(t, hub)
	const viewers = 8
	vs := make([]*Viewer, viewers)
	for i := range vs {
		vs[i] = relayedViewer(t, hs, conn, fmt.Sprintf("%016d", i))
	}
	// The viewers' share, full.
	filler := Outbound{Text: []byte(`{"t":"filler"}`)}
	for len(conn.Send) < hostViewerSlots {
		if err := conn.sendFrom(filler, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	big := bytes.Repeat([]byte("f"), relayBehindBytes+1)
	for _, v := range vs {
		hs.HostRelayFrame(v.ID, proto.Frame{Type: proto.TypeOutput, Payload: big})
	}
	if n := len(conn.Send); n != hostViewerSlots {
		t.Fatalf("%d messages queued, want the %d of the viewers' share", n, hostViewerSlots)
	}
	// The server's own, all the room kept for them.
	for i := range hostQueue - hostViewerSlots {
		if !conn.send(Outbound{Text: []byte(`{"t":"own"}`)}) {
			t.Fatalf("the server's own message %d was refused", i)
		}
	}
	select {
	case <-conn.Done():
		t.Fatal("the host's connection was closed")
	default:
	}
	told := map[string]int{}
	for len(conn.Send) > 0 {
		o := <-conn.Send
		conn.Took(o)
		var m map[string]any
		if json.Unmarshal(o.Text, &m) == nil && m["t"] == proto.HostViewerBehind {
			if m["behind"] != true {
				t.Fatalf("the host was told %v", m)
			}
			told[m["viewerId"].(string)]++
		}
	}
	for _, v := range vs {
		if told[v.ID] != 1 {
			t.Fatalf("viewer %s was said behind %d times", v.ID, told[v.ID])
		}
	}
}

// However often a viewer passes the marks while the host's connection
// takes nothing, it has at most one viewer_behind in the host's queue; one
// that finds the queue full never closes the connection, and goes once the
// writer takes something; and the host ends with the latest.
func TestViewerBehindNeverFloodsNorClosesTheHostQueue(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	v := relayedViewer(t, hs, conn, "0123456789abcdef")
	big := bytes.Repeat([]byte("f"), relayBehindBytes+1)
	// Behind with the frame, caught up as it is taken.
	cycle := func() {
		hs.HostRelayFrame(v.ID, proto.Frame{Type: proto.TypeOutput, Payload: big})
		f := <-v.Out
		v.queued.Add(-int64(len(f)))
		v.noteBacklog()
	}
	open := func() {
		t.Helper()
		select {
		case <-conn.Done():
			t.Fatal("the host's connection was closed")
		default:
		}
	}
	behindOf := func(o Outbound) (behind, ok bool) {
		var m map[string]any
		if json.Unmarshal(o.Text, &m) != nil || m["t"] != proto.HostViewerBehind {
			return false, false
		}
		return m["behind"] == true, true
	}
	for range 100 {
		cycle()
	}
	open()
	if n := len(conn.Send); n != 1 {
		t.Fatalf("%d messages queued for the host, want the one viewer_behind", n)
	}
	// The writer takes the viewer's message as the viewers' lane fills to
	// the brim: the latest finds no room, and waits for some.
	first := <-conn.Send
	if b, ok := behindOf(first); !ok || !b {
		t.Fatalf("the host's first message: %s", first.Text)
	}
	filler := Outbound{Text: []byte(`{"t":"filler"}`)}
	for len(conn.Send) < cap(conn.Send) {
		if err := conn.sendFrom(filler, nil, nil); err != nil {
			t.Fatalf("the lane refused a message with room for it: %v", err)
		}
	}
	conn.Took(first)
	for range 100 {
		cycle()
	}
	open()
	// Room: the latest goes, once, and it says caught up.
	conn.Took(<-conn.Send)
	var told []bool
	for len(conn.Send) > 0 {
		o := <-conn.Send
		conn.Took(o)
		if b, ok := behindOf(o); ok {
			told = append(told, b)
		}
	}
	open()
	if len(told) != 1 || told[0] {
		t.Fatalf("the host was then told %v, want caught up once", told)
	}
}
