package api

import (
	"bytes"
	"context"
	"strings"
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

// answerTo returns the server's message with requestId id, waiting at most
// d for it.
func (h *fakeHost) answerTo(id string, d time.Duration) map[string]any {
	h.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-h.msgs:
			if m["requestId"] == id {
				return m
			}
		case <-h.done:
			h.t.Fatalf("the host's connection ended: %v", h.err)
		case <-deadline:
			h.t.Fatalf("no answer to %s", id)
		}
	}
}

// pingedOften fails unless pings logs a ping at least every 3
// hostPingEvery over the next n of them.
func pingedOften(t *testing.T, pings *pingLog, n int, why string) {
	t.Helper()
	start := time.Now()
	time.Sleep(time.Duration(n) * hostPingEvery)
	if quiet := pings.longestQuiet(start, time.Now()); quiet > 3*hostPingEvery {
		t.Fatalf("%s: no ping for %v", why, quiet)
	}
}

// pingedSeldom fails if pings logs a ping over the next n hostPingEvery: the
// host is pinged pingEvery (30 s) apart.
func pingedSeldom(t *testing.T, pings *pingLog, n int, why string) {
	t.Helper()
	start := time.Now()
	d := time.Duration(n) * hostPingEvery
	time.Sleep(d)
	if quiet := pings.longestQuiet(start, time.Now()); quiet < d*4/5 {
		t.Fatalf("%s: pinged within %v", why, quiet)
	}
}

// A switchyard pings a host every hostPingEvery while a link a viewer
// joined its session through stands: while the viewer is attached, once it
// has left (a reply box closes its connection once it has sent, its line
// perhaps still under way on the host, which types it only while it has
// heard from the server lately), and also while the bound on what the open
// hosts of its address relay pauses the reading of its connection, which
// holds back the answers to the host's own pings: another open host of the
// address spent the bound, and the host's next message waits for it. With
// no such link, before one is joined and once it is revoked, the pings are
// pingEvery apart.
func TestAHostIsPingedOftenWhileALinkJoinedThroughStands(t *testing.T) {
	var pings pingLog
	yard, host, other := pingedYard(t, &pings)
	pingedSeldom(t, &pings, 5, "no link joined through")

	host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "guest", Role: "control"})
	created := host.answerTo("guest", 5*time.Second)
	u, _ := created["url"].(string)
	linkID, _ := created["linkId"].(string)
	_, tok, _ := strings.Cut(u, "/join/")
	pingedSeldom(t, &pings, 5, "a link minted and not joined through")
	guest := dialViewer(t, yard, host.sessionID, tok)
	guest.hello(80, 24)
	host.expect(proto.HostViewerJoin)
	pingedOften(t, &pings, 10, "a viewer attached through a link")
	guest.c.Close(websocket.StatusNormalClosure, "")
	host.expect(proto.HostViewerLeave)
	pingedOften(t, &pings, 10, "the link's viewer left")

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
	host.answerTo("paused", 20*time.Second)
	answered := time.Now()
	if waited := answered.Sub(paused); waited < 2*time.Second {
		t.Fatalf("the host's reading waited %v only: the bound was not spent", waited)
	}
	if quiet := pings.longestQuiet(paused, answered); quiet > 5*hostPingEvery {
		t.Fatalf("no ping for %v while the host's reading waited %v", quiet, answered.Sub(paused))
	}

	host.send(proto.HostLinkRevokeMsg{T: proto.HostLinkRevoke, RequestID: "revoke", LinkID: linkID})
	if m := host.answerTo("revoke", 20*time.Second); m["t"] != proto.HostLinkRevoked {
		t.Fatalf("the revoke: %v", m)
	}
	time.Sleep(2 * hostPingEvery)
	pingedSeldom(t, &pings, 5, "the link revoked")
}
