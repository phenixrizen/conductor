package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/proto"
)

const (
	adminToken = "admin-token"
	hostToken  = "host-token"
)

func startServer(t *testing.T) (*api.Server, *httptest.Server) {
	t.Helper()
	cfg := config.Defaults()
	cfg.AdminToken = adminToken
	cfg.HostTokens = []string{hostToken}
	cfg.AllowedRoots = []string{t.TempDir()}
	cfg.Dev = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := api.New(cfg, catalog.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return srv, hs
}

type viewer struct {
	t *testing.T
	c *websocket.Conn
}

func dialViewer(t *testing.T, base, sessionID, token string) *viewer {
	t.Helper()
	url := strings.Replace(base, "http://", "ws://", 1) + "/ws/sessions/" + sessionID + "?token=" + token
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial viewer: %v", err)
	}
	c.SetReadLimit(proto.MaxFrame)
	t.Cleanup(func() { c.CloseNow() })
	return &viewer{t: t, c: c}
}

func (v *viewer) send(frame []byte) {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := v.c.Write(ctx, websocket.MessageBinary, frame); err != nil {
		v.t.Fatalf("write: %v", err)
	}
}

func (v *viewer) read() (proto.Frame, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, data, err := v.c.Read(ctx)
	if err != nil {
		return proto.Frame{}, err
	}
	return proto.Decode(data)
}

func (v *viewer) expectJSON(typ byte, t string) map[string]any {
	v.t.Helper()
	for {
		f, err := v.read()
		if err != nil {
			v.t.Fatalf("waiting for %s: %v", t, err)
		}
		if f.Type != typ {
			continue
		}
		var m map[string]any
		json.Unmarshal(f.Payload, &m)
		if m["t"] == t {
			return m
		}
	}
}

func (v *viewer) expectOutput(needle string) {
	v.t.Helper()
	var acc []byte
	for !bytes.Contains(acc, []byte(needle)) {
		f, err := v.read()
		if err != nil {
			v.t.Fatalf("waiting for %q: %v (have %q)", needle, err, acc)
		}
		if f.Type == proto.TypeOutput || f.Type == proto.TypeScrollback {
			acc = append(acc, f.Payload...)
		}
	}
}

func TestHostRelayEndToEnd(t *testing.T) {
	srv, hs := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registered := make(chan string, 1)
	done := make(chan Result, 1)
	go func() {
		res, err := Run(ctx, Options{
			ServerURL: hs.URL,
			Token:     hostToken,
			Name:      "relay test",
			HostName:  "laptop",
			Argv:      []string{"/bin/cat"},
			RelayOnly: true,
			Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			Registered: func(id, base string) {
				registered <- id
			},
		})
		if err != nil {
			t.Errorf("run: %v", err)
		}
		done <- res
	}()
	var sessionID string
	select {
	case sessionID = <-registered:
	case <-time.After(10 * time.Second):
		t.Fatal("host did not register")
	}
	d, ok := srv.Registry().Get(sessionID)
	if !ok || d.Info().Kind != "hosted" || d.Info().HostName != "laptop" {
		t.Fatalf("hosted session not listed: %v %+v", ok, d)
	}

	v := dialViewer(t, hs.URL, sessionID, adminToken)
	welcome := v.expectJSON(proto.TypeControl, proto.CtlWelcome)
	if welcome["transport"] != "webrtc" || welcome["viewerId"] == "" {
		t.Fatalf("welcome %v", welcome)
	}
	// Terminal frames before relay mode are rejected.
	v.send(proto.Encode(proto.TypeInput, []byte("x")))
	if m := v.expectJSON(proto.TypeControl, proto.CtlError); m["code"] != proto.ErrCodeBadFrame {
		t.Fatalf("pre-relay input: %v", m)
	}
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.RelayRequest{T: proto.SigRelay, Reason: "forced"})
	v.send(relay)
	v.expectJSON(proto.TypeSignal, proto.SigRelayOK)
	v.send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 90, Rows: 25}))
	w2 := v.expectJSON(proto.TypeControl, proto.CtlWelcome)
	if w2["transport"] != "relay" || w2["role"] != "control" {
		t.Fatalf("relay welcome %v", w2)
	}
	v.expectJSON(proto.TypeControl, proto.CtlReady)
	v.send(proto.Encode(proto.TypeInput, []byte("through the relay\n")))
	v.expectOutput("through the relay")

	// A view-role link cannot type through the relay.
	link, tok, err := srv.Links().Create(sessionID, "view", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	ro := dialViewer(t, hs.URL, sessionID, tok)
	ro.expectJSON(proto.TypeControl, proto.CtlWelcome)
	ro.send(relay)
	ro.expectJSON(proto.TypeSignal, proto.SigRelayOK)
	ro.send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 80, Rows: 24}))
	ro.expectOutput("through the relay") // scrollback replay precedes ready
	ro.expectJSON(proto.TypeControl, proto.CtlReady)
	ro.send(proto.Encode(proto.TypeInput, []byte("nope")))
	if m := ro.expectJSON(proto.TypeControl, proto.CtlError); m["code"] != proto.ErrCodeReadOnly {
		t.Fatalf("read-only relay: %v", m)
	}
	srv.Links().Revoke(sessionID, link.ID)
	for {
		if _, err := ro.read(); err != nil {
			if websocket.CloseStatus(err) != proto.CloseForbidden {
				t.Fatalf("revoked viewer close: %v", err)
			}
			break
		}
	}

	// Stopping through the API reaches the host.
	req, _ := http.NewRequest("DELETE", hs.URL+"/api/sessions/"+sessionID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := hs.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case res := <-done:
		if res.SessionID != sessionID {
			t.Fatalf("result %+v", res)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("host did not exit after stop")
	}
	if m := v.expectJSON(proto.TypeControl, proto.CtlStatus); m["status"] != "stopped" {
		t.Fatalf("status %v", m)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if info := d.Info(); info.Status == "stopped" || info.Status == "exited" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not observe the exit: %+v", d.Info())
}

func TestHostDisconnectClosesViewers(t *testing.T) {
	srv, hs := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	registered := make(chan string, 1)
	go Run(ctx, Options{
		ServerURL: hs.URL, Token: hostToken, Argv: []string{"/bin/cat"}, RelayOnly: true,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Registered: func(id, _ string) { registered <- id },
	})
	sessionID := <-registered
	v := dialViewer(t, hs.URL, sessionID, adminToken)
	v.expectJSON(proto.TypeControl, proto.CtlWelcome)
	cancel() // host goes away
	for {
		f, err := v.read()
		if err != nil {
			if websocket.CloseStatus(err) != proto.CloseSessionEnded {
				t.Fatalf("expected 4410, got %v", err)
			}
			break
		}
		_ = f
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d, ok := srv.Registry().Get(sessionID); ok && (d.Info().Status == "host_disconnected" || d.Info().Status == "stopped") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("session status not updated after host disconnect")
}
