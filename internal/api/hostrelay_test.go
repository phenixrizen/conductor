package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/hostagent"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
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
// session prints fast, and one of them stops reading: the server lets that
// one go alone. The host's connection stays, and the other viewer goes on
// being sent the output, past the time a write of the host's may take.
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
			// About 3 MB/s.
			Argv: []string{"/bin/sh", "-c", "while :; do head -c 32768 /dev/zero; sleep 0.01; done"},
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
	stalled := dialViewer(t, e, id, adminToken)
	joinRelay(t, stalled)

	var output atomic.Int64
	readerEnded := readAll(ctx, reader, &output)
	// The stalled viewer reads nothing from now on; a write of the host's
	// was given 15 s, so the watch runs past that.
	watch := time.After(20 * time.Second)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	var last int64
	for watching := true; watching; {
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
			watching = false
		}
	}
	if n := strings.Count(logs.String(), "host disconnected"); n != 0 {
		t.Fatalf("the host's connection closed %d times", n)
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
