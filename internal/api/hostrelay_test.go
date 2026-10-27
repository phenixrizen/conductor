package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rocksolidlabs/conductor/internal/config"
	"github.com/rocksolidlabs/conductor/internal/hostagent"
	"github.com/rocksolidlabs/conductor/internal/proto"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// smallReadBuffers is a listener whose connections each take little at a
// time (a small receive buffer): what a host sends waits in its own buffers
// as soon as the server reads it slower than it comes.
type smallReadBuffers struct{ net.Listener }

func (l smallReadBuffers) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(256 << 10)
	}
	return c, err
}

// hostsDoor serves e on a second listener of small receive buffers, for a
// host to connect through, and returns its URL.
func hostsDoor(t *testing.T, e *testEnv) string {
	t.Helper()
	hs := httptest.NewUnstartedServer(e.srv.Handler())
	hs.Listener = smallReadBuffers{hs.Listener}
	hs.Start()
	t.Cleanup(hs.Close)
	return hs.URL
}

// joinRelay has v, welcomed, move to the relay, say hello and wait until it
// is ready there.
func joinRelay(t *testing.T, v *wsClient) {
	t.Helper()
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
}

// readAll reads v until its connection ends or ctx does, counting the
// output it is sent, and reports how it ended on the channel it returns.
func readAll(ctx context.Context, v *wsClient, output *atomic.Int64) <-chan error {
	ended := make(chan error, 1)
	go func() {
		for {
			_, data, err := v.c.Read(ctx)
			if err != nil {
				ended <- err
				return
			}
			if f, err := proto.Decode(data); err == nil && f.Type == proto.TypeOutput && output != nil {
				output.Add(int64(len(f.Payload)))
			}
		}
	}()
	return ended
}

// Two viewers of a host's session are relayed through the server while the
// session prints steadily, and one of them stops reading: the server lets
// that one go alone. The host's connection stays, and the other viewer goes
// on being sent the output, past the time a write of the host's may take
// and past the stalled viewer's going.
//
// The session prints about 100 KB/s, which the host relays to two viewers
// with room to spare on a loaded machine (where a session that prints as
// fast as it can outruns the relay, and its host lets a viewer that falls a
// megabyte behind go, by design). The stalled viewer's socket holds little
// at either end, so it is full at once and the server lets the viewer go
// once a write to it has waited its time (15 s), whatever the output's rate.
func TestARelayedViewerThatStopsReadingIsLetGoAlone(t *testing.T) {
	logs := &logBuffer{}
	e := newTestEnvLogging(t, nil, slog.New(slog.NewTextHandler(logs, nil)))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registered := make(chan string, 1)
	ran := make(chan struct{})
	go func() {
		defer close(ran)
		_, _ = hostagent.Run(ctx, hostagent.Options{
			ServerURL: e.http.URL, Token: "test-host-token", Name: "printer", HostName: "laptop", RelayOnly: true,
			// About 100 KB/s.
			Argv: []string{"/bin/sh", "-c", "while :; do head -c 4096 /dev/zero; sleep 0.04; done"},
			Dir:  e.root, Log: slog.New(slog.DiscardHandler),
			Registered: func(id, _ string) { registered <- id },
		})
	}()
	defer func() { cancel(); <-ran }()
	var id string
	select {
	case id = <-registered:
	case <-time.After(10 * time.Second):
		t.Fatal("the host never registered")
	}
	reader := dialViewer(t, e, id, adminToken)
	joinRelay(t, reader)
	// The stalled viewer comes through a link of its own, whose count of
	// viewers says when the server has let it go, and through a door of the
	// server's whose connections have small send buffers, with a small
	// receive buffer of its own.
	resp, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "control"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("link: %d %v", resp.StatusCode, lo)
	}
	stalledLink := lo["link"].(map[string]any)["id"].(string)
	door := httptest.NewUnstartedServer(e.srv.Handler())
	door.Listener = smallBufListener{door.Listener}
	door.Start()
	t.Cleanup(door.Close)
	stalled := dialSmallReader(t, door.URL, id, lo["token"].(string))
	joinRelay(t, stalled)
	hosted, ok := e.srv.hosts.Get(id)
	if !ok {
		t.Fatal("no hosted session")
	}

	var output atomic.Int64
	readerEnded := readAll(ctx, reader, &output)
	// The stalled viewer reads nothing from now on; a write of the host's
	// was given 15 s, so the watch runs past that, and on until the server
	// has let the stalled viewer go (2 minutes at most). Read before then,
	// the stalled viewer's socket would empty, and it might never be let
	// go. The reader is sent output every 2 s all the while.
	watch := time.After(20 * time.Second)
	giveUp := time.After(2 * time.Minute)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	look := time.NewTicker(50 * time.Millisecond)
	defer look.Stop()
	var last int64
	for watched, letGo := false, false; !watched || !letGo; {
		select {
		case err := <-readerEnded:
			t.Fatalf("the reading viewer lost the session: %v (host disconnects: %d)", err, strings.Count(logs.String(), "host disconnected"))
		case <-tick.C:
			n := output.Load()
			if n == last {
				t.Fatalf("the reading viewer was sent no output for 2 s (%d bytes in all)", n)
			}
			last = n
		case <-watch:
			watched = true
		case <-look.C:
			letGo = hosted.LinkViewers()[stalledLink] == 0
		case <-giveUp:
			t.Fatalf("the stalled viewer was never let go (%d bytes of output to the reader)", output.Load())
		}
	}
	if n := strings.Count(logs.String(), "host disconnected"); n != 0 {
		t.Fatalf("the host's connection closed %d times", n)
	}
	// The reader stays, and goes on being sent the output.
	for at, fresh := output.Load(), time.Now().Add(10*time.Second); output.Load() == at; time.Sleep(10 * time.Millisecond) {
		select {
		case err := <-readerEnded:
			t.Fatalf("the reading viewer lost the session as the stalled one was let go: %v", err)
		default:
		}
		if time.Now().After(fresh) {
			t.Fatal("the reading viewer was sent nothing after the stalled one was let go")
		}
	}
	// The stalled viewer was let go: what it reads now ends in a close.
	rctx, rcancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer rcancel()
	for {
		if _, _, err := stalled.c.Read(rctx); err != nil {
			if rctx.Err() != nil {
				t.Fatal("the stalled viewer was never let go")
			}
			break
		}
	}
}

// slowUplink is a listener whose connections each take little at a time (a
// small receive buffer, as smallReadBuffers) and are read at most rate bytes
// a second: a host on an uplink of that rate, as the server sees it.
type slowUplink struct {
	net.Listener
	rate int
}

func (l slowUplink) Accept() (net.Conn, error) {
	c, err := smallReadBuffers{l.Listener}.Accept()
	if err != nil {
		return c, err
	}
	return &slowUplinkConn{Conn: c, rate: newByteBucket(l.rate, 64<<10)}, nil
}

type slowUplinkConn struct {
	net.Conn
	rate *byteBucket
}

func (c *slowUplinkConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p[:min(len(p), 16<<10)])
	if n > 0 {
		_ = c.rate.take(context.Background(), n)
	}
	return n, err
}

// A host relays its session to two viewers over an uplink that carries
// 900 KiB a second, and the session prints about 770 KB a second (at most
// 819): the uplink carries it to one viewer, not to two. One viewer reads
// everything; the other reads nothing at all. Once the stalled viewer's
// socket holds what it may hold unsent and the server 512 KiB more, a
// second or two in, the host holds the rest of its frames, and the uplink
// carries the reader's. The reader keeps the session throughout, is sent
// output all along, past the time a write to the stalled viewer may take,
// and after the stalled viewer is let go.
func TestAViewerThatReadsNothingLeavesTheHostsUplinkToTheOthers(t *testing.T) {
	logs := &logBuffer{}
	e := newTestEnvLogging(t, nil, slog.New(slog.NewTextHandler(logs, nil)))
	uplink := httptest.NewUnstartedServer(e.srv.Handler())
	uplink.Listener = slowUplink{uplink.Listener, 900 << 10}
	uplink.Start()
	t.Cleanup(uplink.Close)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registered := make(chan string, 1)
	ran := make(chan struct{})
	go func() {
		defer close(ran)
		_, _ = hostagent.Run(ctx, hostagent.Options{
			ServerURL: uplink.URL, Token: "test-host-token", Name: "printer", HostName: "laptop", RelayOnly: true,
			// About 770 KB/s: 128 KiB, then 160 ms and more.
			Argv: []string{"/bin/sh", "-c", "while :; do head -c 131072 /dev/zero; sleep 0.16; done"},
			Dir:  e.root, Log: slog.New(slog.DiscardHandler),
			Registered: func(id, _ string) { registered <- id },
		})
	}()
	defer func() { cancel(); <-ran }()
	var id string
	select {
	case id = <-registered:
	case <-time.After(10 * time.Second):
		t.Fatal("the host never registered")
	}
	reader := dialViewer(t, e, id, adminToken)
	joinRelay(t, reader)
	// The stalled viewer comes through a link of its own, whose count of
	// viewers says when the server has let it go, on sockets as they come.
	resp, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("link: %d %v", resp.StatusCode, lo)
	}
	stalledLink := lo["link"].(map[string]any)["id"].(string)
	stalled := dialViewer(t, e, id, lo["token"].(string))
	joinRelay(t, stalled)
	hosted, ok := e.srv.hosts.Get(id)
	if !ok {
		t.Fatal("no hosted session")
	}

	// The reader reads everything, noting the last error it is told.
	var output atomic.Int64
	var told atomic.Value
	readerEnded := make(chan error, 1)
	go func() {
		for {
			_, data, err := reader.c.Read(ctx)
			if err != nil {
				readerEnded <- err
				return
			}
			f, err := proto.Decode(data)
			switch {
			case err != nil:
			case f.Type == proto.TypeOutput:
				output.Add(int64(len(f.Payload)))
			case f.Type == proto.TypeControl:
				if tt, _ := proto.ParseHeader(f.Payload); tt == proto.CtlError {
					told.Store(string(f.Payload))
				}
			}
		}
	}()
	lost := func(err error) {
		t.Helper()
		t.Fatalf("the reading viewer lost the session after %d bytes: %v (told %v; host disconnects: %d)", output.Load(), err, told.Load(), strings.Count(logs.String(), "host disconnected"))
	}
	// The stalled viewer reads nothing from now on. The watch runs past the
	// 15 s a write to it may wait, and on until the server has let it go (2
	// minutes at most); the reader is sent output every 2 s all the while.
	watch := time.After(20 * time.Second)
	giveUp := time.After(2 * time.Minute)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	look := time.NewTicker(50 * time.Millisecond)
	defer look.Stop()
	var last int64
	for watched, letGo := false, false; !watched || !letGo; {
		select {
		case err := <-readerEnded:
			lost(err)
		case <-tick.C:
			n := output.Load()
			if n == last {
				t.Fatalf("the reading viewer was sent no output for 2 s (%d bytes in all)", n)
			}
			last = n
		case <-watch:
			watched = true
		case <-look.C:
			letGo = hosted.LinkViewers()[stalledLink] == 0
		case <-giveUp:
			t.Fatalf("the stalled viewer was never let go (%d bytes of output to the reader)", output.Load())
		}
	}
	// The reader stays, and goes on being sent the output.
	for at, fresh := output.Load(), time.Now().Add(10*time.Second); output.Load() == at; time.Sleep(10 * time.Millisecond) {
		select {
		case err := <-readerEnded:
			lost(err)
		default:
		}
		if time.Now().After(fresh) {
			t.Fatal("the reading viewer was sent nothing after the stalled one was let go")
		}
	}
	if n := strings.Count(logs.String(), "host disconnected"); n != 0 {
		t.Fatalf("the host's connection closed %d times", n)
	}
}

// A relayed viewer that reads nothing is behind once the server holds
// 512 KiB of what its host relays to it, on top of what its socket holds
// unsent, which the server bounds (hostedViewerUnsent): the host is told
// (viewer_behind) before it has relayed the viewer 2 MiB, where a socket's
// buffer on its own grows to megabytes first, and once the viewer reads,
// that it caught up. The viewer is sent every frame, in order.
func TestAHostIsToldSoonThatARelayedViewerReadsNothing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("a socket's unsent bytes are bounded on Linux alone (limitUnsent)")
	}
	e := newTestEnv(t, nil)
	host := dialFakeHost(t, e, "")
	v := dialViewer(t, e, host.sessionID, adminToken)
	viewerID := joinRelayed(t, host, v)
	// The viewer reads nothing from now on; the host relays it output, a
	// frame at a time with a moment for the server to take each, until it
	// is told the viewer is behind. (A server slower than that is only
	// counted a few frames late.)
	out := bytes.Repeat([]byte("o"), proto.MaxOutput)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	sent := 0
	var told map[string]any
	for told == nil {
		if sent > 4<<20 {
			t.Fatalf("relayed %d bytes to a viewer that reads nothing, and it is not behind", sent)
		}
		env, _ := proto.EncodeRelay(viewerID, proto.Encode(proto.TypeOutput, out))
		if err := host.c.Write(ctx, websocket.MessageBinary, env); err != nil {
			t.Fatal(err)
		}
		sent += len(out)
		select {
		case m := <-host.msgs:
			if m["t"] == proto.HostViewerBehind {
				told = m
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if told["viewerId"] != viewerID || told["behind"] != true {
		t.Fatalf("the host was told %v", told)
	}
	if sent > 2<<20 {
		t.Fatalf("the host was told the viewer is behind once it had relayed %d bytes", sent)
	}
	// The viewer reads: it is sent every frame, and caught up.
	got := 0
	for got < sent {
		f, err := v.read()
		if err != nil {
			t.Fatalf("after %d of %d bytes: %v", got, sent, err)
		}
		if f.Type == proto.TypeOutput {
			got += len(f.Payload)
		}
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-host.msgs:
			if m["t"] != proto.HostViewerBehind {
				continue
			}
			if m["viewerId"] != viewerID || m["behind"] != false {
				t.Fatalf("the host was told %v", m)
			}
		case <-deadline:
			t.Fatal("the host was never told the viewer caught up")
		}
		break
	}
}

// A relayed viewer that is behind as its host's session ends is still sent
// the session's last output: what its host held for it goes as the session
// ends, ahead of the end, and the server writes it what it holds once the
// end closes it (the session's links end with it). The viewer reads nothing
// while the session prints 800 KB, past the server's mark, and its last
// line; it reads only once the server shows the session ended.
func TestAViewerBehindAsTheSessionEndsIsSentItsLastOutput(t *testing.T) {
	e := newTestEnv(t, nil)
	// The viewer reads what it is owed once the end has closed it, which
	// is cut 5 s on: the test is of what it is sent, not of how fast.
	e.srv.viewerCloseWait = 2 * time.Minute
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registered := make(chan string, 1)
	ran := make(chan struct{})
	go func() {
		defer close(ran)
		_, _ = hostagent.Run(ctx, hostagent.Options{
			ServerURL: e.http.URL, Token: "test-host-token", Name: "printer", HostName: "laptop", RelayOnly: true,
			// Waits for the test, prints 800 KB and a last line, and ends.
			Argv: []string{"/bin/sh", "-c", "while [ ! -e go ]; do sleep 0.05; done; head -c 800000 /dev/zero; printf 'the last line\\n'"},
			Dir:  e.root, Log: slog.New(slog.DiscardHandler),
			Registered: func(id, _ string) { registered <- id },
		})
	}()
	defer func() { cancel(); <-ran }()
	var id string
	select {
	case id = <-registered:
	case <-time.After(10 * time.Second):
		t.Fatal("the host never registered")
	}
	resp, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("link: %d %v", resp.StatusCode, lo)
	}
	door := httptest.NewUnstartedServer(e.srv.Handler())
	door.Listener = smallBufListener{door.Listener}
	door.Start()
	t.Cleanup(door.Close)
	v := dialSmallReader(t, door.URL, id, lo["token"].(string))
	joinRelay(t, v)
	hosted, ok := e.srv.hosts.Get(id)
	if !ok {
		t.Fatal("no hosted session")
	}
	if err := os.WriteFile(filepath.Join(e.root, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !waitFor(30*time.Second, func() bool { return hosted.Info().Status.Ended() }) {
		t.Fatal("the session never ended")
	}
	rctx, rcancel := context.WithTimeout(t.Context(), time.Minute)
	defer rcancel()
	var out []byte
	for {
		_, data, err := v.c.Read(rctx)
		if err != nil {
			if rctx.Err() != nil {
				t.Fatal("the viewer was never closed")
			}
			break
		}
		if f, err := proto.Decode(data); err == nil && f.Type == proto.TypeOutput {
			out = append(out, f.Payload...)
		}
	}
	if !bytes.Contains(out, []byte("the last line")) {
		t.Fatalf("the viewer was sent %d bytes of output, without the last line", len(out))
	}
}

// dialSmallReader is dialViewer through the server at base, the client's
// socket given a small receive buffer.
func dialSmallReader(t *testing.T, base, sessionID, token string) *wsClient {
	t.Helper()
	dialer := &net.Dialer{Control: func(_, _ string, rc syscall.RawConn) error {
		var serr error
		_ = rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4096) })
		return serr
	}}
	client := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}
	url := strings.Replace(base, "http://", "ws://", 1) + "/ws/sessions/" + sessionID + "?token=" + token
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.SetReadLimit(proto.MaxFrame)
	t.Cleanup(func() { c.CloseNow() })
	return &wsClient{t: t, c: c}
}

// readFiles has v read path over and over, inflight requests at a time,
// until ctx ends or its connection does, counting the files it is sent; how
// it ended goes to ended.
func readFiles(ctx context.Context, v *wsClient, path string, inflight int, files *atomic.Int32, ended chan<- error) {
	n := 0
	ask := func() error {
		n++
		get := proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: fmt.Sprintf("%s-%d", path, n), Path: path})
		return v.c.Write(ctx, websocket.MessageBinary, get)
	}
	for range inflight {
		if err := ask(); err != nil {
			ended <- err
			return
		}
	}
	// told is the last error the viewer was told, for a test's failure.
	told := "none"
	for {
		_, data, err := v.c.Read(ctx)
		if err != nil {
			ended <- fmt.Errorf("%w (told: %s)", err, told)
			return
		}
		f, err := proto.Decode(data)
		if err != nil {
			continue
		}
		if f.Type == proto.TypeControl {
			if tt, _ := proto.ParseHeader(f.Payload); tt == proto.CtlError {
				told = string(f.Payload)
			}
			continue
		}
		if f.Type != proto.TypeFile {
			continue
		}
		h, _, err := proto.DecodeFile(f.Payload)
		switch {
		case err != nil:
		case h.Kind == "file":
			files.Add(1)
			if err := ask(); err != nil {
				ended <- err
				return
			}
		case h.Error != nil:
			told = "file " + h.Error.Code + ": " + h.Error.Message
		}
	}
}

// A relayed viewer's request for an editor it was not offered, naming an
// editor id as long as a control message allows, is refused naming no
// editor: the refusal fits a control frame, and the host's connection stays,
// the viewer and the others served after it.
func TestARefusedEditorRequestKeepsTheHostConnected(t *testing.T) {
	logs := &logBuffer{}
	yard := newTestEnvLogging(t, func(c *config.Config) {
		c.Switchyard.Enabled = true
		c.Switchyard.OpenHosts = true
	}, slog.New(slog.NewTextHandler(logs, nil)))
	local := newTestEnv(t, nil)
	viewers := relayedViewersVia(t, yard, hostsDoor(t, yard), local, "", 2)
	guest, other := viewers[0], viewers[1]
	keys := proto.MustControl(proto.NvimInput{T: proto.CtlNvimInput, ID: strings.Repeat("a", proto.MaxControl-100), Keys: "x", Seq: 7})
	if len(keys)-1 > proto.MaxControl {
		t.Fatalf("the request is %d bytes", len(keys)-1)
	}
	guest.send(keys)
	ev := guest.expectNvim(proto.NvimError)
	if ev["ack"] != float64(7) || ev["id"] != nil || ev["code"] != proto.ErrCodeNvimUnavailable {
		t.Fatalf("the refusal: %v", ev)
	}
	guest.send(proto.Encode(proto.TypeInput, []byte("still here\n")))
	other.expectOutput("still here")
	guest.expectOutput("still here")
	if n := strings.Count(logs.String(), "host disconnected"); n != 0 {
		t.Fatalf("the host's connection closed %d times", n)
	}
}

// A Conductor publishes a session to a switchyard as an open host, under the
// switchyard's default bound on what open hosts relay, past its burst. Six
// relayed viewers read a large file over and over, one read at a time each
// (two would let a viewer's queue on the host pass its bound, and the
// viewer be let go as a slow consumer, by design), and the switchyard reads
// the host's connection only as fast as the bound refills, pausing between
// its messages: the host's frames wait their turn, longer than a write of
// the host's was given, and the switchyard's pings (every second here) come
// while it pauses. A seventh viewer only watches. Nobody loses the session:
// the host's connection stays, and the readers are sent their files.
func TestLargeReadsPastTheRelayBoundKeepTheHostConnected(t *testing.T) {
	noBurstFloor(t)
	defer func(old time.Duration) { pingEvery = old }(pingEvery)
	pingEvery = time.Second
	logs := &logBuffer{}
	yard := newTestEnvLogging(t, func(c *config.Config) {
		c.Switchyard.Enabled = true
		c.Switchyard.OpenHosts = true
	}, slog.New(slog.NewTextHandler(logs, nil)))
	if yard.srv.cfg.Switchyard.OpenHostRelayKBps != config.Defaults().Switchyard.OpenHostRelayKBps {
		t.Fatal("the switchyard is not on the default bound")
	}
	local := newTestEnv(t, nil)
	if err := os.WriteFile(filepath.Join(local.root, "big.txt"), bytes.Repeat([]byte("b"), session.MaxFileFrameBody), 0o600); err != nil {
		t.Fatal(err)
	}
	const readers = 6
	viewers := relayedViewersVia(t, yard, hostsDoor(t, yard), local, "", readers+1)
	watcher := viewers[readers]

	// The host's writes were given 15 s each, and the library's answer to a
	// ping 5 s: the watch runs well past both.
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	watcherEnded := readAll(ctx, watcher, nil)
	files := make([]atomic.Int32, readers)
	readerEnded := make(chan error, readers)
	for i, v := range viewers[:readers] {
		go readFiles(ctx, v, "big.txt", 1, &files[i], readerEnded)
	}
	counts := func() []int32 {
		out := make([]int32, len(files))
		for i := range files {
			out[i] = files[i].Load()
		}
		return out
	}
	select {
	case err := <-watcherEnded:
		if ctx.Err() == nil {
			t.Fatalf("the watching viewer lost the session: %v (host disconnects: %d; files %v)", err, strings.Count(logs.String(), "host disconnected"), counts())
		}
	case err := <-readerEnded:
		if ctx.Err() == nil {
			t.Fatalf("a reader lost the session: %v (host disconnects: %d; files %v)", err, strings.Count(logs.String(), "host disconnected"), counts())
		}
	case <-ctx.Done():
	}
	if n := strings.Count(logs.String(), "host disconnected"); n != 0 {
		t.Fatalf("the host's connection closed %d times (files %v)", n, counts())
	}
	total := int32(0)
	for _, n := range counts() {
		total += n
	}
	// Past the burst, the bound lets about four such files through in the
	// watch.
	if total < 2 {
		t.Fatalf("the readers were sent %d files (%v)", total, counts())
	}
}
