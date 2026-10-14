package api

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/proto"
)

// pingLog keeps when a fake host's pings arrived.
type pingLog struct {
	mu sync.Mutex
	at []time.Time
}

func (l *pingLog) ping() {
	l.mu.Lock()
	l.at = append(l.at, time.Now())
	l.mu.Unlock()
}

// longestQuiet is the longest time in [from, to] with no ping.
func (l *pingLog) longestQuiet(from, to time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	longest, last := time.Duration(0), from
	for _, at := range l.at {
		if at.Before(from) || at.After(to) {
			continue
		}
		longest = max(longest, at.Sub(last))
		last = at
	}
	return max(longest, to.Sub(last))
}

// sendRelayed writes a relay envelope of n bytes for viewer on h's
// connection.
func (h *fakeHost) sendRelayed(viewer string, n int) {
	h.t.Helper()
	env, err := proto.EncodeRelay(viewer, proto.Encode(proto.TypeOutput, bytes.Repeat([]byte("o"), n)))
	if err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(h.t.Context(), 5*time.Second)
	defer cancel()
	if err := h.c.Write(ctx, websocket.MessageBinary, env); err != nil {
		h.t.Fatalf("host write: %v", err)
	}
}

// pingedYard is a switchyard admitting open hosts, the open hosts of an
// address relaying 16 KiB a second together (a burst of 32 KiB), with an
// open host whose pings pings logs, its session the one a viewer joins, and
// another open host of the same address. hostPingEvery is 200 ms.
func pingedYard(t *testing.T, pings *pingLog) (yard *testEnv, host, other *fakeHost) {
	t.Helper()
	noBurstFloor(t)
	old := hostPingEvery
	hostPingEvery = 200 * time.Millisecond
	t.Cleanup(func() { hostPingEvery = old })
	yard = newTestEnv(t, func(c *config.Config) {
		c.Switchyard.Enabled = true
		c.Switchyard.OpenHosts = true
		c.Switchyard.OpenHostRelayKBps = 16
	})
	host, _ = dialFakeHostPinged(t, yard, proto.HostInfo{Name: "laptop"}, proto.HostSession{Name: "hosted", AgentID: "cat", Command: []string{"cat"}, Cwd: "hosted", Cols: 80, Rows: 24}, "", pings.ping)
	other = dialFakeHostAuth(t, yard, "", "")
	return yard, host, other
}

// quietFor fails unless pings logs no ping for d from now: the host is
// pinged pingEvery (30 s) apart.
func quietFor(t *testing.T, pings *pingLog, d time.Duration, why string) {
	t.Helper()
	start := time.Now()
	time.Sleep(d)
	if quiet := pings.longestQuiet(start, time.Now()); quiet < d*4/5 {
		t.Fatalf("%s: pinged within %v", why, quiet)
	}
}

// A switchyard pings a host every hostPingEvery while the bound on what
// the open hosts of its address relay pauses the reading of its
// connection: another open host of the address spent the bound, and the
// host's next message waits for it, its pongs and the answers to its own
// pings unread meanwhile. The host types the line of a viewer who joined
// through a link only while it has heard from the server lately, also once
// that viewer has left (a reply box closes its connection once it has
// sent): it hears the pings. Otherwise, with no viewer through a link,
// the pings are pingEvery apart.
func TestAHostIsPingedWhileItsReadingWaits(t *testing.T) {
	var pings pingLog
	yard, host, other := pingedYard(t, &pings)
	tok := host.link("control", "guest")
	guest := dialViewer(t, yard, host.sessionID, tok)
	guest.hello(80, 24)
	host.expect(proto.HostViewerJoin)
	guest.c.Close(websocket.StatusNormalClosure, "")
	host.expect(proto.HostViewerLeave)
	quietFor(t, &pings, 5*hostPingEvery, "no viewer through a link, the reading not paused")

	// The other host spends the address's bound: 32 KiB of burst, then 64
	// KiB at 16 KiB a second, about 4 s, which the host's next message
	// waits for.
	other.sendRelayed("0123456789abcdef", 96<<10)
	time.Sleep(200 * time.Millisecond)
	paused := time.Now()
	host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "paused", Role: "view"})
	// Meanwhile the host's own ping is not answered: its pong waits for
	// the reader.
	pctx, cancel := context.WithTimeout(t.Context(), time.Second)
	err := host.c.Ping(pctx)
	cancel()
	if err == nil {
		t.Fatal("the switchyard answered the host's ping while its reading waited")
	}
	var answered time.Time
	deadline := time.After(20 * time.Second)
	for answered.IsZero() {
		select {
		case m := <-host.msgs:
			if m["t"] == proto.HostLinkCreated && m["requestId"] == "paused" {
				answered = time.Now()
			}
		case <-host.done:
			t.Fatalf("the host's connection ended: %v", host.err)
		case <-deadline:
			t.Fatal("the host's message was never read")
		}
	}
	if waited := answered.Sub(paused); waited < 2*time.Second {
		t.Fatalf("the host's reading waited %v only: the bound was not spent", waited)
	}
	if quiet := pings.longestQuiet(paused, answered); quiet > 5*hostPingEvery {
		t.Fatalf("no ping for %v while the host's reading waited %v", quiet, answered.Sub(paused))
	}
	// Read again, the host's pings are answered, and the switchyard's are
	// pingEvery apart.
	pctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
	err = host.c.Ping(pctx)
	cancel()
	if err != nil {
		t.Fatalf("the host's ping once read again: %v", err)
	}
	quietFor(t, &pings, 5*hostPingEvery, "read again")
}

// A switchyard pings a host every hostPingEvery while a viewer is attached
// to its session through a link, and pingEvery apart again once it has
// left.
func TestAHostWithALinksViewerIsPingedOften(t *testing.T) {
	var pings pingLog
	yard, host, _ := pingedYard(t, &pings)
	quietFor(t, &pings, 5*hostPingEvery, "no viewer")
	tok := host.link("control", "guest")
	guest := dialViewer(t, yard, host.sessionID, tok)
	guest.hello(80, 24)
	host.expect(proto.HostViewerJoin)
	start := time.Now()
	time.Sleep(10 * hostPingEvery)
	if quiet := pings.longestQuiet(start, time.Now()); quiet > 3*hostPingEvery {
		t.Fatalf("no ping for %v with a viewer attached through a link", quiet)
	}
	guest.c.Close(websocket.StatusNormalClosure, "")
	host.expect(proto.HostViewerLeave)
	time.Sleep(2 * hostPingEvery)
	quietFor(t, &pings, 5*hostPingEvery, "the viewer left")
}
