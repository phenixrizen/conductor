package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rocksolidlabs/conductor/internal/config"
	"github.com/rocksolidlabs/conductor/internal/hostagent"
	"github.com/rocksolidlabs/conductor/internal/proto"
)

// A host's connection carries the server's own messages ahead of the
// frames it relays (signal.HostConn's two lanes): a link's going reaches
// the host ahead of what its viewers queued, and nothing of a viewer closed
// for its link that was still queued reaches the host after it.

// noViewerBudget lifts the bound on what viewers send hosts: what waits for
// a host here waits in its queue alone.
func noViewerBudget(c *config.Config) { c.ViewerRelayKBps = 0 }

// smallReceiver dials with a small receive buffer: what the server sends
// and its reader has not taken waits on the server, not in megabytes of
// the socket's.
func smallReceiver() *net.Dialer {
	return &net.Dialer{Control: func(_, _ string, rc syscall.RawConn) error {
		var serr error
		_ = rc.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 8<<10) })
		return serr
	}}
}

// dialRawHostVia is dialRawHost through base (a door of e's), its socket's
// receive buffer small.
func dialRawHostVia(t *testing.T, e *testEnv, base string) (*websocket.Conn, string) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DialContext: smallReceiver().DialContext}}
	url := "ws" + strings.TrimPrefix(base, "http") + "/ws/host"
	hc, _, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Authorization": {"Bearer test-host-token"}}})
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
	return hc, registered.SessionID
}

// controlLink mints a control link to session id of e and returns its id
// and token.
func controlLink(t *testing.T, e *testEnv, id string) (string, string) {
	t.Helper()
	resp, out := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "control", "label": "guest"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("link: %d %v", resp.StatusCode, out)
	}
	return out["link"].(map[string]any)["id"].(string), out["token"].(string)
}

// revokeLink revokes link linkID of session id of e.
func revokeLink(t *testing.T, e *testEnv, id, linkID string) {
	t.Helper()
	resp, out := e.do("DELETE", "/api/sessions/"+id+"/links/"+linkID, adminToken, nil)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("revoke: %d %v", resp.StatusCode, out)
	}
}

// askRelay has v ask for the relay and waits for relay_ok.
func askRelay(t *testing.T, v *wsClient) {
	t.Helper()
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.Simple{T: proto.SigRelay})
	v.send(relay)
	for {
		f, err := v.read()
		if err != nil {
			t.Fatalf("waiting for relay_ok: %v", err)
		}
		if tt, _ := proto.ParseHeader(f.Payload); f.Type == proto.TypeSignal && tt == proto.SigRelayOK {
			return
		}
	}
}

// sendInput sends n bytes of INPUT from v in frames of proto.MaxInput, each
// a run of short lines.
func sendInput(t *testing.T, v *wsClient, n int) {
	t.Helper()
	frame := proto.Encode(proto.TypeInput, bytes.Repeat([]byte(strings.Repeat("f", 63)+"\n"), proto.MaxInput/64))
	for sent := 0; sent < n; sent += proto.MaxInput {
		v.send(frame)
	}
}

// A link's going reaches the host ahead of the frames queued for it: with a
// host that reads nothing and a viewer that floods it until the viewers'
// lane is full, the revoke's link_gone is read within the few messages the
// sockets on the way already hold, not behind the queue's megabytes.
func TestLinkGoneReachesHostAheadOfQueuedFrames(t *testing.T) {
	e := newTestEnv(t, noViewerBudget)
	hc, sessionID := dialRawHostVia(t, e, slowDoor(t, e))
	hs, _ := e.srv.hosts.Get(sessionID)
	linkID, token := controlLink(t, e, sessionID)
	guest := dialViewer(t, e, sessionID, token)
	guestID, _ := guest.expectControl(proto.CtlWelcome)["viewerId"].(string)
	expectHost(t, hc, proto.HostViewerJoin, guestID)
	owner, ownerID := joinRawRelayed(t, e, hc, sessionID)
	go flood(t.Context(), owner, 0)
	deadline := time.Now().Add(30 * time.Second)
	for hs.ViewersWaiting() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the owner's frames never came to wait for the host")
		}
		time.Sleep(10 * time.Millisecond)
	}
	revokeLink(t, e, sessionID, linkID)
	relayed := 0
	for {
		m := readHost(t, hc, 10*time.Second)
		if m.t == proto.HostLinkGone {
			break
		}
		if m.t == "relay" && m.viewer == ownerID {
			relayed++
		}
		if relayed > maxRelayedBeforeLinkGone() {
			t.Fatalf("the host read %d of the owner's frames after the revoke and no link_gone", relayed)
		}
	}
	t.Logf("link_gone read after %d of the owner's frames", relayed)
}

// maxRelayedBeforeLinkGone is how many of the owner's frames the host may
// read after the revoke before link_gone: what the sockets on the way held.
// On Linux the small buffers (smallBufListener, smallReceiver) hold a few.
// On macOS a receive buffer set before connect is replaced by TCP's
// autotuned size at connect, and loopback keeps a few hundred KiB in flight
// whatever the sockets ask for (340-545 KiB measured, up to about 13
// relayed messages of 32 KiB input each): 5 to 10 are read there, run to
// run. Both bounds are well under the viewers' lane's 7 MiB, some 160 such
// messages, which is what the test pins.
func maxRelayedBeforeLinkGone() int {
	if runtime.GOOS == "darwin" {
		return 32
	}
	return 4
}

// A revoke that lands while a viewer's join, its relay_start and a frame of
// its wait in the viewers' lane reaches the host as the link's going alone:
// the viewer is closed before link_gone is queued, and what it still had
// queued is dropped as the writer takes it, so a host, older or newer,
// never hears of the viewer, nor its leave, and acts on nothing of its. The
// host here reads everything in order, as an older host does.
func TestRevokeOvertakingAQueuedJoinReachesNoHost(t *testing.T) {
	e := newTestEnv(t, noViewerBudget)
	hc, sessionID := dialRawHostVia(t, e, slowDoor(t, e))
	owner, ownerID := joinRawRelayed(t, e, hc, sessionID)
	// A megabyte the host does not read: the server's writer waits on the
	// socket, the rest in the viewers' lane, which has room.
	sendInput(t, owner, 1<<20)
	linkID, token := controlLink(t, e, sessionID)
	guest := dialViewer(t, e, sessionID, token)
	guestID, _ := guest.expectControl(proto.CtlWelcome)["viewerId"].(string)
	askRelay(t, guest) // relay_ok: the join and relay_start are queued
	guest.send(proto.Encode(proto.TypeInput, []byte("typed through the link\n")))
	time.Sleep(300 * time.Millisecond)
	revokeLink(t, e, sessionID, linkID)
	marker := proto.Encode(proto.TypeInput, []byte("marker\n"))
	owner.send(marker)
	gone := false
	for {
		m := readHost(t, hc, 15*time.Second)
		if m.viewer == guestID {
			t.Fatalf("the host was sent %s of a viewer closed for its link", m.t)
		}
		if m.t == proto.HostLinkGone {
			gone = true
		}
		if m.t == "relay" && m.viewer == ownerID && bytes.Equal(proto.Encode(m.frame.Type, m.frame.Payload), marker) {
			break
		}
	}
	if !gone {
		t.Fatal("no link_gone")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	for {
		typ, data, err := hc.Read(ctx)
		if err != nil {
			break
		}
		if typ == websocket.MessageText && bytes.Contains(data, []byte(guestID)) {
			t.Fatalf("then the host was sent %s", data)
		}
	}
}

// throttle carries TCP connections to target, what target sends at rate
// bytes a second, its socket to target with a small receive buffer: a slow
// path from a server to its host.
func throttle(t *testing.T, target string, rate int) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s, err := smallReceiver().Dial("tcp", target)
			if err != nil {
				c.Close()
				continue
			}
			t.Cleanup(func() { c.Close(); s.Close() })
			go func() { _, _ = io.Copy(s, c) }()
			go func() {
				buf := make([]byte, 8<<10)
				for {
					n, err := s.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
						time.Sleep(time.Duration(n) * time.Second / time.Duration(rate))
					}
					if err != nil {
						c.Close()
						return
					}
				}
			}()
		}
	}()
	return "http://" + ln.Addr().String()
}

// A line a link's viewer sent, still queued behind relayed frames for a
// host on a slow path when the link is revoked, is never typed: the going
// reaches the host ahead of the queue, and the line is dropped with the
// viewer. The owner's lines, queued before and after it, reach the
// session.
func TestLinkLineRefusedAfterRevokeBehindQueue(t *testing.T) {
	e := newTestEnv(t, noViewerBudget)
	out := filepath.Join(t.TempDir(), "lines")
	ctx, cancel := context.WithCancel(t.Context())
	registered := make(chan string, 1)
	ran := make(chan struct{})
	go func() {
		defer close(ran)
		_, _ = hostagent.Run(ctx, hostagent.Options{
			ServerURL: throttle(t, strings.TrimPrefix(slowDoor(t, e), "http://"), 1<<20), Token: "test-host-token", Name: "slow path", HostName: "laptop", RelayOnly: true,
			Argv: []string{"/bin/sh", "-c", `stty -echo; exec grep --line-buffered -e guest -e marker > "$1"`, "sh", out}, Dir: e.root, Log: slog.New(slog.DiscardHandler),
			Registered: func(id, _ string) { registered <- id },
		})
	}()
	t.Cleanup(func() { cancel(); <-ran })
	var id string
	select {
	case id = <-registered:
	case <-time.After(10 * time.Second):
		t.Fatal("the host never registered")
	}
	owner := dialViewer(t, e, id, adminToken)
	joinRelay(t, owner)
	linkID, token := controlLink(t, e, id)
	guest := dialViewer(t, e, id, token)
	joinRelay(t, guest)

	sendInput(t, owner, 3<<20)
	guest.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "guest line"}))
	time.Sleep(300 * time.Millisecond)
	sendInput(t, owner, 2<<20)
	time.Sleep(300 * time.Millisecond)
	revokeLink(t, e, id, linkID)

	// The owner's marker comes after all of it: once the session has it,
	// everything queued before it has been typed, or dropped.
	marker := proto.Encode(proto.TypeInput, []byte("marker\n"))
	deadline := time.Now().Add(60 * time.Second)
	for {
		owner.send(marker)
		b, _ := os.ReadFile(out)
		if bytes.Contains(b, []byte("marker")) {
			if bytes.Contains(b, []byte("guest line")) {
				t.Fatalf("the revoked link's line was typed: %q", b)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the owner's marker never reached the session: %q", b)
		}
		time.Sleep(2 * time.Second)
	}
}
