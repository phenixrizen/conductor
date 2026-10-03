package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/proto"
)

func switchyardEnv(t *testing.T, relay bool) *testEnv {
	t.Helper()
	return newTestEnv(t, func(c *config.Config) {
		c.Switchyard.Enabled = true
		c.Switchyard.Relay = &relay
	})
}

// A switchyard launches nothing: every route that starts, edits or lists
// what the server launches answers 403 switchyard, the health and whoami
// routes say what it is, and a hosted session's link resolves as anywhere.
func TestSwitchyardLaunchesNothing(t *testing.T) {
	e := switchyardEnv(t, true)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/sessions"}, {"GET", "/api/catalog"}, {"POST", "/api/catalog/check"}, {"GET", "/api/crews"}, {"POST", "/api/crews"},
		{"GET", "/api/runs"}, {"POST", "/api/runs/x-1a2b3c4d/stop"}, {"GET", "/api/integrations"}, {"GET", "/api/paths"}, {"GET", "/api/git/check"},
		{"POST", "/api/sessions/abc/resume"}, {"POST", "/api/sessions/abc/crew"}, {"GET", "/api/sessions/abc/run"},
	} {
		resp, out := e.do(tc.method, tc.path, adminToken, map[string]any{})
		if resp.StatusCode != http.StatusForbidden || errorCode(out) != "switchyard" {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, resp.StatusCode, out)
		}
	}
	resp, out := e.do("GET", "/api/health", "", nil)
	if resp.StatusCode != http.StatusOK || out["switchyard"] != true || out["relay"] != true {
		t.Fatalf("health: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("GET", "/api/whoami", adminToken, nil)
	if resp.StatusCode != http.StatusOK || out["switchyard"] != true || out["host"] == nil {
		t.Fatalf("whoami: %d %v", resp.StatusCode, out)
	}
	// Sessions are listed (the hosted ones), and a plain server says it is no switchyard.
	if resp, _ := e.do("GET", "/api/sessions", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("sessions: %d", resp.StatusCode)
	}
	plain := newTestEnv(t, nil)
	if _, out := plain.do("GET", "/api/health", "", nil); out["switchyard"] != false || out["relay"] != true {
		t.Fatalf("a plain server's health: %v", out)
	}
}

// A host registers on the switchyard, an admin mints a link, a viewer joins
// by it and asks for the relay: relay_ok, and the host is told.
func TestSwitchyardCoordinatesAHostAndAViewer(t *testing.T) {
	e := switchyardEnv(t, true)
	host := dialFakeHost(t, e, "hosted-agent-token")
	resp, out := e.do("POST", "/api/sessions/"+host.sessionID+"/links", adminToken, map[string]any{"role": "view", "label": "a friend"})
	if resp.StatusCode != http.StatusCreated || out["token"] == nil {
		t.Fatalf("link: %d %v", resp.StatusCode, out)
	}
	token := out["token"].(string)
	if resp, out := e.do("GET", "/api/join/"+token, "", nil); resp.StatusCode != http.StatusOK || out["session"] == nil {
		t.Fatalf("join: %d %v", resp.StatusCode, out)
	}
	v := dialViewer(t, e, host.sessionID, token)
	v.hello(80, 24)
	welcome := v.expectControl(proto.CtlWelcome)
	if welcome["transport"] != "webrtc" {
		t.Fatalf("welcome %v", welcome)
	}
	host.expect(proto.HostViewerJoin)
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.Simple{T: proto.SigRelay})
	v.send(relay)
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := v.read()
		if err != nil {
			t.Fatalf("waiting for relay_ok: %v", err)
		}
		if tt, _ := proto.ParseHeader(f.Payload); f.Type == proto.TypeSignal && tt == proto.SigRelayOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no relay_ok")
		}
	}
	host.expect(proto.HostRelayStart)
}

// Without a relay the switchyard tells a viewer who asks (relay_off) and
// keeps the connection for its WebRTC attempt; a host that would use the
// relay alone is refused at registration.
func TestSwitchyardRelayOff(t *testing.T) {
	e := switchyardEnv(t, false)
	if _, out := e.do("GET", "/api/health", "", nil); out["relay"] != false {
		t.Fatalf("health: %v", out)
	}
	host := dialFakeHost(t, e, "hosted-agent-token")
	v := dialViewer(t, e, host.sessionID, adminToken)
	v.hello(80, 24)
	v.expectControl(proto.CtlWelcome)
	host.expect(proto.HostViewerJoin)
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.Simple{T: proto.SigRelay})
	v.send(relay)
	for {
		f, err := v.read()
		if err != nil {
			t.Fatalf("waiting for the refusal: %v", err)
		}
		if f.Type == proto.TypeControl && bytes.Contains(f.Payload, []byte(`"relay_off"`)) {
			break
		}
	}
	if msgs := host.drain(200 * time.Millisecond); len(msgs) != 0 {
		t.Fatalf("the host heard %v", msgs)
	}
	// Still connected: an offer still reaches the host.
	offer, _ := proto.EncodeJSON(proto.TypeSignal, map[string]any{"t": proto.SigOffer, "sdp": "v=0 fake"})
	v.send(offer)
	host.expect(proto.HostOffer)

	// A relay-only host is refused.
	ctx := t.Context()
	url := strings.Replace(e.http.URL, "http://", "ws://", 1) + "/ws/host"
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer test-host-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	b, _ := json.Marshal(proto.Register{T: proto.HostRegister, Proto: proto.ProtoVersion, Host: proto.HostInfo{Name: "laptop"},
		Session: proto.HostSession{Name: "hosted", AgentID: "cat", Command: []string{"cat"}, Cwd: e.root, Cols: 80, Rows: 24, RelayOnly: true}})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("a relay-only host was registered on a switchyard without a relay")
	} else if ce := websocket.CloseStatus(err); ce != websocket.StatusCode(proto.CloseProtocolError) {
		t.Fatalf("close %v: %v", ce, err)
	}
}

// The join route answers the desktop app's own workbench across origins
// (loopback by default), and no other origin; a plain server answers none.
func TestSwitchyardAnswersLoopbackOriginsOnTheJoinRoute(t *testing.T) {
	// Outside dev mode: dev allows every localhost origin on every route.
	e := newTestEnv(t, func(c *config.Config) {
		c.Dev = false
		c.Switchyard.Enabled = true
	})
	host := dialFakeHost(t, e, "hosted-agent-token")
	_, out := e.do("POST", "/api/sessions/"+host.sessionID+"/links", adminToken, map[string]any{"role": "view"})
	token := out["token"].(string)
	get := func(env *testEnv, path, origin, method string) *http.Response {
		req, _ := http.NewRequest(method, env.http.URL+path, nil)
		req.Header.Set("Origin", origin)
		resp, err := env.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if resp := get(e, "/api/join/"+token, "http://127.0.0.1:43123", "GET"); resp.Header.Get("Access-Control-Allow-Origin") != "http://127.0.0.1:43123" || resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback origin: %d %v", resp.StatusCode, resp.Header)
	}
	if resp := get(e, "/api/join/"+token, "http://localhost:5173", "OPTIONS"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight: %d", resp.StatusCode)
	}
	if resp := get(e, "/api/join/"+token, "https://evil.example", "GET"); resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("a stranger's origin was allowed: %v", resp.Header)
	}
	if resp := get(e, "/api/health", "http://127.0.0.1:43123", "GET"); resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("another route was allowed: %v", resp.Header)
	}
	plain := newTestEnv(t, func(c *config.Config) { c.Dev = false })
	if resp := get(plain, "/api/health", "http://127.0.0.1:43123", "GET"); resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("a plain server allowed an origin: %v", resp.Header)
	}
	// Configured origins replace the loopback ones.
	custom := newTestEnv(t, func(c *config.Config) {
		c.Dev = false
		c.Switchyard.Enabled = true
		c.Switchyard.AllowedOrigins = []string{"app.example:*"}
	})
	h2 := dialFakeHost(t, custom, "hosted-agent-token")
	_, out = custom.do("POST", "/api/sessions/"+h2.sessionID+"/links", adminToken, map[string]any{"role": "view"})
	tok2 := out["token"].(string)
	if resp := get(custom, "/api/join/"+tok2, "https://app.example:8443", "GET"); resp.Header.Get("Access-Control-Allow-Origin") != "https://app.example:8443" {
		t.Fatalf("configured origin: %v", resp.Header)
	}
	if resp := get(custom, "/api/join/"+tok2, "http://127.0.0.1:43123", "GET"); resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("loopback allowed beside configured origins: %v", resp.Header)
	}
}

// originMatches takes host patterns, as the WebSocket accept does.
func TestOriginMatches(t *testing.T) {
	patterns := []string{"127.0.0.1:*", "localhost:*", "app.example"}
	for origin, want := range map[string]bool{
		"http://127.0.0.1:1234": true, "http://localhost:3000": true, "https://app.example": true, "https://app.example:443": false,
		"http://127.0.0.1": false, "https://evil.example": false, "": false, "not a url": false,
	} {
		if got := originMatches(origin, patterns); got != want {
			t.Errorf("%q: %v", origin, got)
		}
	}
}
