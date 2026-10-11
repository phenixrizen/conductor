package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/hostagent"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/signal"
)

// viewerBudget sets what the viewers of one address may send hosts through
// the server: 256 KiB a second, a burst of 512 KiB under noBurstFloor.
func viewerBudget(c *config.Config) { c.ViewerRelayKBps = 256 }

// noBurstFloor has the servers this test makes give each relay bound a burst
// of twice its rate alone, without relayBurstFloor: a test of a rate sends
// past the burst, and need not send 4 MiB first. Called before they are made.
func noBurstFloor(t *testing.T) {
	old := relayBurstFloor
	relayBurstFloor = 0
	t.Cleanup(func() { relayBurstFloor = old })
}

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
	noBurstFloor(t)
	for name, mutate := range map[string]func(*config.Config){
		"switchyard": func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) },
		"serve":      viewerBudget,
	} {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t, mutate)
			host := dialFakeHost(t, e, "hosted-agent-token")
			tok := host.link("control", "")
			tap := tapRelays(t, host)
			v := dialViewer(t, e, host.sessionID, tok)
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
	noBurstFloor(t)
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) })
	host := dialFakeHost(t, e, "hosted-agent-token")
	tok := host.link("control", "")
	tap := tapRelays(t, host)
	a := dialViewer(t, e, host.sessionID, tok)
	idA := joinRelayed(t, host, a)
	b := dialViewer(t, e, host.sessionID, tok)
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

// waitingViewer is a relayed viewer whose frames wait for room in the queue
// of a host that reads nothing: hc is the host's connection, hs its session.
type waitingViewer struct {
	hc       *websocket.Conn
	hs       *signal.HostedSession
	v        *wsClient
	viewerID string
}

// stallWaitingViewer registers a host that then reads nothing more, joins a
// relayed viewer, and has it send until the host's queue holds all it may
// and the server stops reading the viewer.
func stallWaitingViewer(t *testing.T, e *testEnv) waitingViewer {
	t.Helper()
	url := strings.Replace(e.http.URL, "http://", "ws://", 1) + "/ws/host"
	hc, _, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer test-host-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hc.CloseNow() })
	hc.SetReadLimit(proto.MaxHostMessage + proto.MaxFrame)
	reg, _ := json.Marshal(proto.Register{T: proto.HostRegister, Proto: proto.ProtoVersion, Host: proto.HostInfo{Name: "laptop"},
		Session: proto.HostSession{Name: "hosted", AgentID: "cat", Command: []string{"cat"}, Cwd: e.root, Cols: 80, Rows: 24}})
	if err := hc.Write(t.Context(), websocket.MessageText, reg); err != nil {
		t.Fatal(err)
	}
	_, data, err := hc.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var registered proto.Registered
	if err := json.Unmarshal(data, &registered); err != nil || registered.SessionID == "" {
		t.Fatalf("registered %s: %v", data, err)
	}
	hs, _ := e.srv.hosts.Get(registered.SessionID)

	v := dialViewer(t, e, registered.SessionID, adminToken)
	v.hello(80, 24)
	viewerID, _ := v.expectControl(proto.CtlWelcome)["viewerId"].(string)
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
	// The viewer sends until the host's queue holds all it may and the
	// server stops reading it.
	var sent atomic.Int64
	go func() {
		frame := proto.Encode(proto.TypeInput, bytes.Repeat([]byte("f"), proto.MaxInput))
		for v.c.Write(t.Context(), websocket.MessageBinary, frame) == nil {
			sent.Add(int64(len(frame)))
		}
	}()
	for last := int64(-1); last != sent.Load(); {
		last = sent.Load()
		time.Sleep(300 * time.Millisecond)
	}
	if sent.Load() < 7<<20 {
		t.Fatalf("the server stopped reading after %d bytes", sent.Load())
	}
	return waitingViewer{hc: hc, hs: hs, v: v, viewerID: viewerID}
}

// A viewer that disconnects while its frames wait for room in the queue of a
// host that reads nothing is let go as soon as a write to it fails: the
// session counts it no more, and the host stays connected.
func TestAViewerThatLeavesWhileWaitingForTheHostIsLetGo(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.ViewerRelayKBps = 0 })
	w := stallWaitingViewer(t, e)
	w.v.c.CloseNow()
	// The host writes to the viewer, which finds it gone.
	start := time.Now()
	for i := 0; i < 5; i++ {
		env, _ := proto.EncodeRelay(w.viewerID, proto.Encode(proto.TypeOutput, []byte("o")))
		if err := w.hc.Write(t.Context(), websocket.MessageBinary, env); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !waitFor(5*time.Second, func() bool { return w.hs.Info().Viewers == 0 }) {
		t.Fatalf("a viewer gone for %v is still counted", time.Since(start))
	}
	if !w.hs.Connected() {
		t.Fatal("the host was disconnected")
	}
}

// The same viewer is let go when nothing is written to it: its pings find
// the connection gone, though its read loop is held.
func TestAViewerThatLeavesWhileWaitingIsFoundByItsPings(t *testing.T) {
	defer func(old time.Duration) { pingEvery = old }(pingEvery)
	pingEvery = 200 * time.Millisecond
	e := newTestEnv(t, func(c *config.Config) { c.ViewerRelayKBps = 0 })
	w := stallWaitingViewer(t, e)
	// Held, it does not answer pings, and stays.
	time.Sleep(3 * pingEvery)
	if w.hs.Info().Viewers != 1 {
		t.Fatal("a viewer held in its read loop was let go")
	}
	w.v.c.CloseNow()
	start := time.Now()
	if !waitFor(5*time.Second, func() bool { return w.hs.Info().Viewers == 0 }) {
		t.Fatalf("a viewer gone for %v is still counted", time.Since(start))
	}
	if !w.hs.Connected() {
		t.Fatal("the host was disconnected")
	}
}

// A viewer that spends its address's burst and connects again finds it still
// spent: leaving buys no fresh burst.
func TestAViewerThatComesBackFindsItsBudgetSpent(t *testing.T) {
	noBurstFloor(t)
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) })
	host := dialFakeHost(t, e, "hosted-agent-token")
	tok := host.link("control", "")
	tap := tapRelays(t, host)
	const burst = 512 << 10
	first := dialViewer(t, e, host.sessionID, tok)
	idFirst := joinRelayed(t, host, first)
	go flood(t.Context(), first, burst)
	if !waitFor(10*time.Second, func() bool { return tap.inputBytes(idFirst) >= burst }) {
		t.Fatalf("the first connection got %d of %d bytes through", tap.inputBytes(idFirst), burst)
	}
	first.c.Close(websocket.StatusNormalClosure, "")
	host.expect(proto.HostViewerLeave)
	again := dialViewer(t, e, host.sessionID, tok)
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
	noBurstFloor(t)
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) })
	host := dialFakeHost(t, e, "hosted-agent-token")
	tok := host.link("control", "")
	tap := tapRelays(t, host)
	b := dialViewerFrom(t, e, "127.0.0.2", host.sessionID, tok)
	idB := joinRelayed(t, host, b)
	a := dialViewer(t, e, host.sessionID, tok)
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
			tok := host.link("control", "")
			tap := tapRelays(t, host)
			v := dialViewer(t, e, host.sessionID, tok)
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

// browserFileDeadline is how long the browser waits for the answer to a file
// request or a save before it gives up on it (FILE_TIMEOUT_MS in
// web/app/utils/transport/base.ts): a save then shows as not saved.
const browserFileDeadline = 20 * time.Second

// A relay bound's burst is twice its rate and never less than the floor,
// which holds three of the largest frames relayed, a file of
// proto.MaxFileBytes read with its header, and a little more: what a
// person's tabs ask for together passes at once, whatever the rate.
func TestRelayBurstHoldsWhatAPersonAsksForAtOnce(t *testing.T) {
	largest := 1 + proto.ViewerIDLen + proto.MaxFrame
	if relayBurstFloor < 3*largest {
		t.Fatalf("a floor of %d holds fewer than three frames of %d", relayBurstFloor, largest)
	}
	e := newTestEnv(t, nil)
	if got := e.srv.relayBurst(128 << 10); got != relayBurstFloor {
		t.Fatalf("the burst at 128 KiB/s is %d", got)
	}
	if got := e.srv.relayBurst(4 << 20); got != 8<<20 {
		t.Fatalf("the burst at 4 MiB/s is %d", got)
	}
}

// relayedViewers has local publish its next session to rendezvous (as an
// open host when token is empty) and joins n viewers there, each through
// the relay, as the workbench.
func relayedViewers(t *testing.T, rendezvous, local *testEnv, token string, n int) []*wsClient {
	t.Helper()
	local.srv.SetPublisher(uplinkPublisher{&hostagent.Uplink{ServerURL: rendezvous.http.URL, Token: token, HostName: "office-server", RelayOnly: true}})
	localID := local.createSession("cat")
	var hostedID string
	if !waitFor(10*time.Second, func() bool {
		rendezvous.srv.Registry().Each(func(d session.Driver) {
			if i := d.Info(); i.Kind == session.KindHosted && i.HostName == "office-server" {
				hostedID = i.ID
			}
		})
		return hostedID != ""
	}) {
		t.Fatal("the rendezvous never listed the session")
	}
	// A switchyard's workbench token has no role on a published session: the
	// viewers come by a link the publishing server mints there.
	viewerTok := adminToken
	if rendezvous.srv.cfg.Switchyard.Enabled {
		resp, out := local.do("POST", "/api/sessions/"+localID+"/links", adminToken, map[string]any{"role": "control"})
		u, _ := out["url"].(string)
		_, tok, ok := strings.Cut(u, "/join/")
		if resp.StatusCode != http.StatusCreated || !ok || tok == "" {
			t.Fatalf("a link on the switchyard: %d %v", resp.StatusCode, out)
		}
		viewerTok = tok
	}
	viewers := make([]*wsClient, n)
	for i := range viewers {
		v := dialViewer(t, rendezvous, hostedID, viewerTok)
		v.expectControl(proto.CtlWelcome)
		relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.RelayRequest{T: proto.SigRelay, Reason: "forced"})
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
		v.hello(80, 24)
		v.expectControl(proto.CtlWelcome)
		v.expectControl(proto.CtlReady)
		viewers[i] = v
	}
	return viewers
}

// awaitFile reads v until the FILE frame answering reqID.
func awaitFile(ctx context.Context, v *wsClient, reqID string) (proto.FileHeader, []byte, error) {
	for {
		_, data, err := v.c.Read(ctx)
		if err != nil {
			return proto.FileHeader{}, nil, err
		}
		f, err := proto.Decode(data)
		if err != nil || f.Type != proto.TypeFile {
			continue
		}
		if h, body, err := proto.DecodeFile(f.Payload); err == nil && h.ReqID == reqID {
			return h, body, nil
		}
	}
}

// saveRelayed saves body over path from v as the editor does, in parts of
// proto.MaxWritePart under reqID, and returns the answer.
func saveRelayed(ctx context.Context, v *wsClient, reqID, path, base string, body []byte) (proto.FileHeader, error) {
	for off := 0; off < len(body); off += proto.MaxWritePart {
		end := min(off+proto.MaxWritePart, len(body))
		frame, err := proto.EncodeFileWrite(proto.FileWrite{ReqID: reqID, Path: path, Offset: int64(off), Total: int64(len(body)), BaseSha256: base}, body[off:end])
		if err != nil {
			return proto.FileHeader{}, err
		}
		if err := v.c.Write(ctx, websocket.MessageBinary, frame); err != nil {
			return proto.FileHeader{}, err
		}
	}
	h, _, err := awaitFile(ctx, v, reqID)
	return h, err
}

// allAtOnce runs do(i) for each of n at once, and returns how long each took
// from the start and what it returned.
func allAtOnce(n int, do func(i int) error) ([]time.Duration, []error) {
	took, errs := make([]time.Duration, n), make([]error, n)
	var wg sync.WaitGroup
	start := time.Now()
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = do(i)
			took[i] = time.Since(start)
		}()
	}
	wg.Wait()
	return took, errs
}

// Three tabs behind one address save a file of the largest size the editor
// saves, all at once, into a session another server publishes here and
// relays to them: under the default budget each save is written and
// answered well inside the browser's deadline.
func TestThreeLargestSavesFromOneAddressAnswerWithinTheBrowsersDeadline(t *testing.T) {
	rendezvous := newTestEnv(t, nil)
	if rendezvous.srv.cfg.ViewerRelayKBps != config.Defaults().ViewerRelayKBps {
		t.Fatal("the rendezvous is not on the default budget")
	}
	local := newTestEnv(t, nil)
	names := []string{"a.txt", "b.txt", "c.txt"}
	bases := make([]string, len(names))
	bodies := make([][]byte, len(names))
	for i, n := range names {
		old := []byte("old " + n + "\n")
		if err := os.WriteFile(filepath.Join(local.root, n), old, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(old)
		bases[i] = hex.EncodeToString(sum[:])
		bodies[i] = bytes.Repeat([]byte{byte('a' + i)}, proto.MaxFileBytes)
	}
	viewers := relayedViewers(t, rendezvous, local, "test-host-token", len(names))
	ctx, cancel := context.WithTimeout(t.Context(), 2*browserFileDeadline)
	defer cancel()
	answers := make([]proto.FileHeader, len(names))
	took, errs := allAtOnce(len(names), func(i int) (err error) {
		answers[i], err = saveRelayed(ctx, viewers[i], "w"+names[i], names[i], bases[i], bodies[i])
		return err
	})
	for i, n := range names {
		t.Logf("%s answered after %v", n, took[i])
		switch {
		case errs[i] != nil:
			t.Fatalf("%s: no answer after %v: %v", n, took[i], errs[i])
		case answers[i].Kind != "written":
			t.Fatalf("%s: answered %+v", n, answers[i])
		case took[i] > browserFileDeadline/2:
			t.Errorf("%s: answered after %v, the browser gives up after %v", n, took[i], browserFileDeadline)
		}
		if got, err := os.ReadFile(filepath.Join(local.root, n)); err != nil || !bytes.Equal(got, bodies[i]) {
			t.Errorf("%s on disk: %d bytes, %v", n, len(got), err)
		}
	}
}

// Three tabs read a large file each, all at once, from a session an open
// host publishes on a switchyard and relays to them: under the default
// bound on what open hosts relay, each answer comes well inside the
// browser's deadline. Each file is the largest a FILE frame carries whole
// (session.MaxFileFrameBody).
func TestThreeLargeReadsFromAnOpenHostAnswerWithinTheBrowsersDeadline(t *testing.T) {
	switchyard := openSwitchyardEnv(t, nil)
	if switchyard.srv.cfg.Switchyard.OpenHostRelayKBps != config.Defaults().Switchyard.OpenHostRelayKBps {
		t.Fatal("the switchyard is not on the default bound")
	}
	local := newTestEnv(t, nil)
	names := []string{"a.txt", "b.txt", "c.txt"}
	const size = session.MaxFileFrameBody
	for i, n := range names {
		if err := os.WriteFile(filepath.Join(local.root, n), bytes.Repeat([]byte{byte('a' + i)}, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	viewers := relayedViewers(t, switchyard, local, "", len(names))
	ctx, cancel := context.WithTimeout(t.Context(), 2*browserFileDeadline)
	defer cancel()
	answers := make([]proto.FileHeader, len(names))
	bodies := make([][]byte, len(names))
	took, errs := allAtOnce(len(names), func(i int) error {
		get := proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "f" + names[i], Path: names[i]})
		if err := viewers[i].c.Write(ctx, websocket.MessageBinary, get); err != nil {
			return err
		}
		var err error
		answers[i], bodies[i], err = awaitFile(ctx, viewers[i], "f"+names[i])
		return err
	})
	for i, n := range names {
		t.Logf("%s answered after %v", n, took[i])
		switch {
		case errs[i] != nil:
			t.Fatalf("%s: no answer after %v: %v", n, took[i], errs[i])
		case answers[i].Kind != "file" || len(bodies[i]) != size:
			t.Fatalf("%s: answered %+v with %d bytes", n, answers[i], len(bodies[i]))
		case took[i] > browserFileDeadline/2:
			t.Errorf("%s: answered after %v, the browser gives up after %v", n, took[i], browserFileDeadline)
		}
	}
}

// Viewers that come and go are charged to their address the join and the
// leave the server sends the host for each, whether or not the join went:
// the next viewer of the address waits for what they owe.
func TestViewersComingAndGoingArePaidForByTheirAddress(t *testing.T) {
	noBurstFloor(t)
	const rate = 1024 // viewerRelayKBps 1: a burst of 2 KiB
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; c.ViewerRelayKBps = 1 })
	host := dialFakeHost(t, e, "hosted-agent-token")
	tok := host.link("control", "")
	// What each owes at least: a join (its link's id besides) and a leave.
	join, _ := json.Marshal(proto.ViewerJoin{T: proto.HostViewerJoin, ViewerID: "0123456789abcdef", Role: "control"})
	leave, _ := json.Marshal(proto.ViewerRef{T: proto.HostViewerLeave, ViewerID: "0123456789abcdef"})
	// A hosted viewer is welcomed without a hello: the viewers send nothing,
	// and what their address owes is their joins and leaves alone.
	const churn = 40
	start := time.Now()
	for range churn {
		v := dialViewer(t, e, host.sessionID, tok)
		v.expectControl(proto.CtlWelcome)
		v.c.Close(websocket.StatusNormalClosure, "")
	}
	// One more of the address joins: its join goes once the address has
	// paid for the others' joins and leaves, past the burst.
	last := dialViewer(t, e, host.sessionID, tok)
	id, _ := last.expectControl(proto.CtlWelcome)["viewerId"].(string)
	deadline := time.After(30 * time.Second)
	for got := false; !got; {
		select {
		case m := <-host.msgs:
			got = m["t"] == proto.HostViewerJoin && m["viewerId"] == id
		case <-deadline:
			t.Fatal("the last viewer's join never reached the host")
		}
	}
	took := time.Since(start)
	owed := float64(churn*(len(join)+len(leave))-2*rate) / rate
	if took.Seconds() < 0.8*owed {
		t.Fatalf("%d viewers came and went, and the next joined, in %v: their joins and leaves owe %.1f s at 1 KiB/s", churn, took, owed)
	}
}

// A viewer is charged what its messages bring the host, not only what it
// sent: offers the server encodes anew for the host, six bytes for each '<'
// the viewer sent as one, go no faster than the budget either.
func TestAViewersOffersAreChargedAsTheHostGetsThem(t *testing.T) {
	noBurstFloor(t)
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) })
	host := dialFakeHost(t, e, "hosted-agent-token")
	v := dialViewer(t, e, host.sessionID, host.link("control", ""))
	v.hello(80, 24)
	v.expectControl(proto.CtlWelcome)
	host.expect(proto.HostViewerJoin)
	// Four offers of 60 KiB of '<', as JSON allows it: 240 KiB from the
	// viewer, inside its burst of 512 KiB, but about 1.4 MiB to the host,
	// which past the burst goes at 256 KiB/s, three seconds or more.
	const offers = 4
	frame := proto.Encode(proto.TypeSignal, []byte(`{"t":"offer","sdp":"`+strings.Repeat("<", 60<<10)+`"}`))
	start := time.Now()
	go func() {
		for range offers {
			if v.c.Write(t.Context(), websocket.MessageBinary, frame) != nil {
				return
			}
		}
	}()
	for range offers {
		host.expect(proto.HostOffer)
	}
	if took := time.Since(start); took < 2*time.Second {
		t.Fatalf("four offers of 360 KiB each reached the host in %v under a budget of 256 KiB/s", took)
	}
}

// While one viewer sends past its budget, the host is answered at once and
// another viewer's ping goes through: the flood waits in its own read loop
// alone.
func TestHostAndOtherViewersStayResponsiveDuringAFlood(t *testing.T) {
	noBurstFloor(t)
	e := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; viewerBudget(c) })
	host := dialFakeHost(t, e, "hosted-agent-token")
	tok := host.link("control", "")
	tap := tapRelays(t, host)
	a := dialViewer(t, e, host.sessionID, tok)
	idA := joinRelayed(t, host, a)
	b := dialViewer(t, e, host.sessionID, tok)
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
	c := dialViewer(t, e, host.sessionID, tok)
	c.hello(80, 24)
	c.expectControl(proto.CtlWelcome)
	host.expect(proto.HostViewerJoin)
}
