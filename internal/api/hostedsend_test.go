package api

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/proto"
)

// viewerBudget sets what the viewers of one address may send hosts through
// the server: 256 KiB a second, a burst of 512 KiB.
func viewerBudget(c *config.Config) { c.ViewerRelayKBps = 256 }

// relayTap records the frames the server relays to a fake host, by viewer.
type relayTap struct {
	mu     sync.Mutex
	frames []tapped
}

type tapped struct {
	viewer string
	frame  proto.Frame
}

func tapRelays(t *testing.T, h *fakeHost) *relayTap {
	tap := &relayTap{}
	go func() {
		for {
			select {
			case data := <-h.bin:
				f, err := proto.Decode(data)
				if err != nil || f.Type != proto.TypeRelay {
					continue
				}
				id, inner, err := proto.DecodeRelay(f.Payload)
				if err != nil {
					continue
				}
				inner.Payload = bytes.Clone(inner.Payload)
				tap.mu.Lock()
				tap.frames = append(tap.frames, tapped{id, inner})
				tap.mu.Unlock()
			case <-h.done:
				return
			case <-t.Context().Done():
				return
			}
		}
	}()
	return tap
}

// inputBytes counts the INPUT bytes relayed from viewer.
func (tap *relayTap) inputBytes(viewer string) int {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	n := 0
	for _, f := range tap.frames {
		if f.viewer == viewer && f.frame.Type == proto.TypeInput {
			n += len(f.frame.Payload)
		}
	}
	return n
}

// from returns the frames relayed from viewer, in order.
func (tap *relayTap) from(viewer string) []proto.Frame {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	var out []proto.Frame
	for _, f := range tap.frames {
		if f.viewer == viewer {
			out = append(out, f.frame)
		}
	}
	return out
}

// waitFor polls until cond holds or d has passed, and says whether it held.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

// joinRelayed says hello on v, asks for the relay and waits until the viewer
// has relay_ok and the host relay_start; it returns the viewer's id.
func joinRelayed(t *testing.T, host *fakeHost, v *wsClient) string {
	t.Helper()
	v.hello(80, 24)
	id, _ := v.expectControl(proto.CtlWelcome)["viewerId"].(string)
	host.expect(proto.HostViewerJoin)
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.Simple{T: proto.SigRelay})
	v.send(relay)
	for {
		f, err := v.read()
		if err != nil {
			t.Fatalf("waiting for relay_ok: %v", err)
		}
		if tt, _ := proto.ParseHeader(f.Payload); f.Type == proto.TypeSignal && tt == proto.SigRelayOK {
			break
		}
	}
	host.expect(proto.HostRelayStart)
	return id
}

// flood sends INPUT frames of proto.MaxInput bytes from v until ctx ends, a
// write fails or total bytes are sent (0: no end).
func flood(ctx context.Context, v *wsClient, total int) {
	chunk := bytes.Repeat([]byte("f"), proto.MaxInput)
	frame := proto.Encode(proto.TypeInput, chunk)
	for sent := 0; total == 0 || sent < total; sent += len(chunk) {
		if v.c.Write(ctx, websocket.MessageBinary, frame) != nil {
			return
		}
	}
}

// dialViewerFrom is dialViewer from another loopback address, local.
func dialViewerFrom(t *testing.T, e *testEnv, local, sessionID, token string) *wsClient {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("another loopback address to connect from is Linux's alone")
	}
	d := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(local)}, Timeout: 5 * time.Second}
	client := &http.Client{Transport: &http.Transport{DialContext: d.DialContext}}
	url := strings.Replace(e.http.URL, "http://", "ws://", 1) + "/ws/sessions/" + sessionID + "?token=" + token
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Skipf("cannot connect from %s: %v", local, err)
	}
	c.SetReadLimit(proto.MaxFrame)
	t.Cleanup(func() { c.CloseNow() })
	return &wsClient{t: t, c: c}
}

// A relayed viewer that sends more than its address's budget is slowed: what
// it sends all reaches the host, whole and in order, but no faster than the
// budget, on a switchyard as on a plain server.
func TestRelayedViewerIsSlowedToItsAddressBudget(t *testing.T) {
	for name, mutate := range map[string]func(*config.Config){
		"switchyard": func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) },
		"serve":      viewerBudget,
	} {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t, mutate)
			host := dialFakeHost(t, e, "hosted-agent-token")
			tap := tapRelays(t, host)
			v := dialViewer(t, e, host.sessionID, adminToken)
			id := joinRelayed(t, host, v)
			// 1 MiB: past the burst of 512 KiB the rest goes at 256 KiB/s, two
			// seconds or more.
			const total = 1 << 20
			start := time.Now()
			go flood(t.Context(), v, total)
			if !waitFor(20*time.Second, func() bool { return tap.inputBytes(id) >= total }) {
				t.Fatalf("the host got %d of %d bytes", tap.inputBytes(id), total)
			}
			if took := time.Since(start); took < 1500*time.Millisecond {
				t.Fatalf("1 MiB went through in %v under a budget of 256 KiB/s", took)
			}
			for i, f := range tap.from(id) {
				if f.Type == proto.TypeInput && len(f.Payload) != proto.MaxInput {
					t.Fatalf("frame %d is not whole: %d bytes", i, len(f.Payload))
				}
			}
		})
	}
}

// Two viewers of one address send under one budget: together they wait as
// one would, where a budget for each would let both through at once.
func TestViewersOfOneAddressShareOneBudget(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) })
	host := dialFakeHost(t, e, "hosted-agent-token")
	tap := tapRelays(t, host)
	a := dialViewer(t, e, host.sessionID, adminToken)
	idA := joinRelayed(t, host, a)
	b := dialViewer(t, e, host.sessionID, adminToken)
	idB := joinRelayed(t, host, b)
	const each = 512 << 10
	start := time.Now()
	go flood(t.Context(), a, each)
	go flood(t.Context(), b, each)
	if !waitFor(20*time.Second, func() bool { return tap.inputBytes(idA) >= each && tap.inputBytes(idB) >= each }) {
		t.Fatalf("the host got %d and %d of %d bytes", tap.inputBytes(idA), tap.inputBytes(idB), each)
	}
	if took := time.Since(start); took < 1500*time.Millisecond {
		t.Fatalf("two viewers of one address sent 1 MiB in %v under a budget of 256 KiB/s", took)
	}
}

// A viewer that spends its address's burst and connects again finds it still
// spent: leaving buys no fresh burst.
func TestAViewerThatComesBackFindsItsBudgetSpent(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) })
	host := dialFakeHost(t, e, "hosted-agent-token")
	tap := tapRelays(t, host)
	const burst = 512 << 10
	first := dialViewer(t, e, host.sessionID, adminToken)
	idFirst := joinRelayed(t, host, first)
	go flood(t.Context(), first, burst)
	if !waitFor(10*time.Second, func() bool { return tap.inputBytes(idFirst) >= burst }) {
		t.Fatalf("the first connection got %d of %d bytes through", tap.inputBytes(idFirst), burst)
	}
	first.c.Close(websocket.StatusNormalClosure, "")
	host.expect(proto.HostViewerLeave)
	again := dialViewer(t, e, host.sessionID, adminToken)
	idAgain := joinRelayed(t, host, again)
	start := time.Now()
	go flood(t.Context(), again, burst)
	if !waitFor(10*time.Second, func() bool { return tap.inputBytes(idAgain) >= burst }) {
		t.Fatalf("the second connection got %d of %d bytes through", tap.inputBytes(idAgain), burst)
	}
	if took := time.Since(start); took < 1500*time.Millisecond {
		t.Fatalf("a connection that came back sent a whole burst in %v", took)
	}
}

// A viewer of another address sends under its own budget: one that drained
// its address's does not slow it.
func TestAnotherAddressKeepsItsOwnViewerBudget(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) })
	host := dialFakeHost(t, e, "hosted-agent-token")
	tap := tapRelays(t, host)
	b := dialViewerFrom(t, e, "127.0.0.2", host.sessionID, adminToken)
	idB := joinRelayed(t, host, b)
	a := dialViewer(t, e, host.sessionID, adminToken)
	idA := joinRelayed(t, host, a)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go flood(ctx, a, 0)
	// The first address's burst is spent, and it goes on sending.
	if !waitFor(10*time.Second, func() bool { return tap.inputBytes(idA) >= 640<<10 }) {
		t.Fatalf("the flood got %d bytes through", tap.inputBytes(idA))
	}
	// The other address's whole burst goes at once; one bucket for both
	// would take two seconds over it.
	const burst = 512 << 10
	start := time.Now()
	go flood(t.Context(), b, burst)
	if !waitFor(10*time.Second, func() bool { return tap.inputBytes(idB) >= burst }) {
		t.Fatalf("the other address got %d of %d bytes through", tap.inputBytes(idB), burst)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("another address's burst took %v behind the first address's flood", took)
	}
}

// What a person sends goes through at once under the default budget, in
// order and whole: typing, a paste as large as an INPUT frame holds, a
// resize and a ping.
func TestOrdinaryViewerTrafficPassesUnderTheDefaultBudget(t *testing.T) {
	if config.Defaults().ViewerRelayKBps == 0 {
		t.Fatal("no viewer budget by default")
	}
	for name, mutate := range map[string]func(*config.Config){
		"switchyard": func(c *config.Config) { c.Switchyard.Enabled = true },
		"serve":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t, mutate)
			host := dialFakeHost(t, e, "hosted-agent-token")
			tap := tapRelays(t, host)
			v := dialViewer(t, e, host.sessionID, adminToken)
			id := joinRelayed(t, host, v)
			var want [][]byte
			for i := 0; i < 200; i++ {
				want = append(want, proto.Encode(proto.TypeInput, []byte{byte('a' + i%26)}))
			}
			want = append(want,
				proto.Encode(proto.TypeInput, bytes.Repeat([]byte("p"), proto.MaxInput)),
				proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: 120, Rows: 40}),
				proto.MustControl(proto.Ping{T: proto.CtlPing, TS: 7}))
			for i := 0; i < 3; i++ {
				want = append(want, proto.Encode(proto.TypeInput, bytes.Repeat([]byte("q"), proto.MaxInput)))
			}
			start := time.Now()
			for _, f := range want {
				v.send(f)
			}
			if !waitFor(5*time.Second, func() bool { return len(tap.from(id)) >= len(want) }) {
				t.Fatalf("the host got %d of %d frames", len(tap.from(id)), len(want))
			}
			if took := time.Since(start); took > time.Second {
				t.Fatalf("ordinary traffic took %v", took)
			}
			for i, f := range tap.from(id) {
				if !bytes.Equal(proto.Encode(f.Type, f.Payload), want[i]) {
					t.Fatalf("frame %d differs", i)
				}
			}
		})
	}
}

// While one viewer sends past its budget, the host is answered at once and
// another viewer's ping goes through: the flood waits in its own read loop
// alone.
func TestHostAndOtherViewersStayResponsiveDuringAFlood(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) })
	host := dialFakeHost(t, e, "hosted-agent-token")
	tap := tapRelays(t, host)
	a := dialViewer(t, e, host.sessionID, adminToken)
	idA := joinRelayed(t, host, a)
	b := dialViewer(t, e, host.sessionID, adminToken)
	idB := joinRelayed(t, host, b)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go flood(ctx, a, 0)
	if !waitFor(10*time.Second, func() bool { return tap.inputBytes(idA) >= 640<<10 }) {
		t.Fatalf("the flood got %d bytes through", tap.inputBytes(idA))
	}
	for i := 0; i < 3; i++ {
		start := time.Now()
		// The host asks for a link and is answered.
		host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "r" + string(rune('0'+i)), Role: "view"})
		host.expect(proto.HostLinkCreated)
		// The other viewer, of the same address, pings the host through the
		// relay, and the host's pong comes back.
		seen := len(tap.from(idB))
		b.send(proto.MustControl(proto.Ping{T: proto.CtlPing, TS: int64(i)}))
		if !waitFor(5*time.Second, func() bool { return len(tap.from(idB)) > seen }) {
			t.Fatal("the other viewer's ping did not reach the host")
		}
		pong, _ := proto.EncodeRelay(idB, proto.MustControl(proto.Ping{T: proto.CtlPong, TS: int64(i)}))
		if err := host.c.Write(t.Context(), websocket.MessageBinary, pong); err != nil {
			t.Fatal(err)
		}
		b.expectControl(proto.CtlPong)
		if took := time.Since(start); took > time.Second {
			t.Fatalf("round %d took %v during the flood", i, took)
		}
	}
	// A viewer joining is told to the host as ever.
	c := dialViewer(t, e, host.sessionID, adminToken)
	c.hello(80, 24)
	c.expectControl(proto.CtlWelcome)
	host.expect(proto.HostViewerJoin)
}
