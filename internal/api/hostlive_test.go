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

// A switchyard pings a host whose session has a viewer attached through a
// link every linkPingEvery, also while the bound on what the open hosts of
// its address relay pauses the reading of its connection: another open host
// of the address spent the bound, and the host's next message waits for it,
// its pongs unread meanwhile. The host types such a viewer's lines only
// while it has heard from the server lately; it hears the pings. Without a
// viewer through a link, the pings are pingEvery apart.
func TestAHostWithALinksViewerIsPingedWhileItsReadingWaits(t *testing.T) {
	noBurstFloor(t)
	defer func(old time.Duration) { linkPingEvery = old }(linkPingEvery)
	linkPingEvery = 200 * time.Millisecond
	yard := newTestEnv(t, func(c *config.Config) {
		c.Switchyard.Enabled = true
		c.Switchyard.OpenHosts = true
		c.Switchyard.OpenHostRelayKBps = 16
	})
	var pings pingLog
	host, _ := dialFakeHostPinged(t, yard, proto.HostInfo{Name: "laptop"}, proto.HostSession{Name: "hosted", AgentID: "cat", Command: []string{"cat"}, Cwd: "hosted", Cols: 80, Rows: 24}, "", pings.ping)
	other := dialFakeHostAuth(t, yard, "", "")

	// Before a viewer joins through a link: pingEvery (30 s) apart.
	start := time.Now()
	time.Sleep(5 * linkPingEvery)
	if quiet := pings.longestQuiet(start, time.Now()); quiet < 4*linkPingEvery {
		t.Fatalf("pinged within %v with no viewer through a link", quiet)
	}

	tok := host.link("control", "guest")
	guest := dialViewer(t, yard, host.sessionID, tok)
	guest.hello(80, 24)
	host.expect(proto.HostViewerJoin)

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
	if quiet := pings.longestQuiet(paused, answered); quiet > 5*linkPingEvery {
		t.Fatalf("no ping for %v while the host's reading waited %v", quiet, answered.Sub(paused))
	}
}
