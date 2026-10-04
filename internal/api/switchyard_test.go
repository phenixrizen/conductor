package api

import (
	"bytes"
	"context"
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

// A switchyard with a relay bound slows a host that streams more than it:
// every frame still arrives, in order, but no faster than the bound.
func TestSwitchyardRelayIsThrottledPerHost(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) {
		c.Switchyard.Enabled = true
		c.Switchyard.RelayKBps = 256
	})
	host := dialFakeHost(t, e, "hosted-agent-token")
	if took := relayOneMiB(t, e, host); took < 1500*time.Millisecond {
		t.Fatalf("1 MiB went through in %v with a bound of 256 KiB/s", took)
	}
}

// relayOneMiB joins host's session as a relayed viewer, streams 1 MiB from
// the host in 64 whole frames and says how long the viewer took to get it all.
func relayOneMiB(t *testing.T, e *testEnv, host *fakeHost) time.Duration {
	t.Helper()
	v := dialViewer(t, e, host.sessionID, adminToken)
	v.hello(80, 24)
	v.expectControl(proto.CtlWelcome)
	viewerID, _ := host.expect(proto.HostViewerJoin)["viewerId"].(string)
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
	// 1 MiB in 64 frames of 16 KiB: past the burst of 512 KiB, the rest
	// drains at 256 KiB/s, so the whole takes two seconds or more.
	const frames = 64
	chunk := bytes.Repeat([]byte("q"), 16<<10)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	start := time.Now()
	go func() {
		for i := 0; i < frames; i++ {
			env, _ := proto.EncodeRelay(viewerID, proto.Encode(proto.TypeOutput, chunk))
			if err := host.c.Write(ctx, websocket.MessageBinary, env); err != nil {
				return
			}
		}
	}()
	got := 0
	for got < frames {
		f, err := v.read()
		if err != nil {
			t.Fatalf("after %d frames: %v", got, err)
		}
		if f.Type != proto.TypeOutput {
			continue
		}
		if !bytes.Equal(f.Payload, chunk) {
			t.Fatalf("frame %d is not whole: %d bytes", got, len(f.Payload))
		}
		got++
	}
	return time.Since(start)
}

// A byte bucket refills at its rate and allows a burst of twice it; a take
// past the burst is cut to the burst (one frame never waits forever).
func TestByteBucket(t *testing.T) {
	b := newByteBucket(1000)
	if b.burst != 2000 || b.tokens != 2000 {
		t.Fatalf("%+v", b)
	}
	ctx := t.Context()
	start := time.Now()
	if err := b.take(ctx, 1500); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("a take within the burst waited")
	}
	// 500 left: 1000 more need half a second.
	if err := b.take(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 400*time.Millisecond {
		t.Fatalf("waited %v, want about 500 ms", d)
	}
	// Cancelled while waiting.
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := b.take(cctx, 2000); err == nil {
		t.Fatal("a cancelled wait returned no error")
	}
}

// dialHostRaw dials the host route with the given Authorization value ("" for
// none) and returns the HTTP status when the upgrade is refused, 101 when it
// went through (the connection is then closed).
func dialHostRaw(t *testing.T, e *testEnv, auth string) int {
	t.Helper()
	url := strings.Replace(e.http.URL, "http://", "ws://", 1) + "/ws/host"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	if auth != "" {
		header.Set("Authorization", auth)
	}
	c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		if resp == nil {
			t.Fatalf("dial: %v", err)
		}
		return resp.StatusCode
	}
	c.CloseNow()
	return http.StatusSwitchingProtocols
}

func openSwitchyardEnv(t *testing.T, mutate func(*config.Config)) *testEnv {
	t.Helper()
	return newTestEnv(t, func(c *config.Config) {
		c.Switchyard.Enabled = true
		c.Switchyard.OpenHosts = true
		if mutate != nil {
			mutate(c)
		}
	})
}

// A switchyard that admits open hosts registers one that presents no token
// and shares it as any other; without openHosts, on a plain server, or
// with a wrong token, the route still answers 401.
func TestSwitchyardAdmitsOpenHosts(t *testing.T) {
	e := openSwitchyardEnv(t, nil)
	host := dialFakeHostAuth(t, e, "hosted-agent-token", "")
	resp, out := e.do("POST", "/api/sessions/"+host.sessionID+"/links", adminToken, map[string]any{"role": "view", "label": "open"})
	if resp.StatusCode != http.StatusCreated || out["token"] == nil {
		t.Fatalf("link: %d %v", resp.StatusCode, out)
	}
	if resp, out := e.do("GET", "/api/join/"+out["token"].(string), "", nil); resp.StatusCode != http.StatusOK || out["session"] == nil {
		t.Fatalf("join: %d %v", resp.StatusCode, out)
	}
	if got := dialHostRaw(t, e, "Bearer nope"); got != http.StatusUnauthorized {
		t.Fatalf("wrong token on an open switchyard: %d", got)
	}
	if got := dialHostRaw(t, switchyardEnv(t, true), ""); got != http.StatusUnauthorized {
		t.Fatalf("no token without openHosts: %d", got)
	}
	if got := dialHostRaw(t, newTestEnv(t, nil), ""); got != http.StatusUnauthorized {
		t.Fatalf("no token on a plain server: %d", got)
	}
}

// One address holds at most openHostSessions live open sessions; a place
// frees when its connection ends; a host with a token is never counted.
func TestOpenHostSessionsPerAddressAreCapped(t *testing.T) {
	e := openSwitchyardEnv(t, func(c *config.Config) {
		c.Switchyard.OpenHostSessions = 2
		c.Switchyard.OpenHostRegistrationsPerMinute = 600
	})
	first := dialFakeHostAuth(t, e, "a1", "")
	dialFakeHostAuth(t, e, "a2", "")
	if got := dialHostRaw(t, e, ""); got != http.StatusTooManyRequests {
		t.Fatalf("third open host: %d, want 429", got)
	}
	for i := 0; i < 3; i++ {
		dialFakeHost(t, e, "tokened"+string(rune('a'+i)))
	}
	first.c.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for dialHostRaw(t, e, "") != http.StatusSwitchingProtocols {
		if time.Now().After(deadline) {
			t.Fatal("the place of a closed open host was not freed")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Open registrations from one address are rate limited; tokened ones are not.
func TestOpenHostRegistrationsAreRateLimitedPerAddress(t *testing.T) {
	e := openSwitchyardEnv(t, func(c *config.Config) {
		c.Switchyard.OpenHostRegistrationsPerMinute = 2
		c.Switchyard.OpenHostSessions = 100
	})
	for i := 0; i < 2; i++ {
		if got := dialHostRaw(t, e, ""); got != http.StatusSwitchingProtocols {
			t.Fatalf("open registration %d: %d", i+1, got)
		}
	}
	if got := dialHostRaw(t, e, ""); got != http.StatusTooManyRequests {
		t.Fatalf("third open registration in a minute: %d, want 429", got)
	}
	if got := dialHostRaw(t, e, "Bearer test-host-token"); got != http.StatusSwitchingProtocols {
		t.Fatalf("a tokened host after the open limit: %d", got)
	}
}

// An open host relays under openHostRelayKBps, its own bound.
func TestOpenHostsRelayUnderTheirOwnBound(t *testing.T) {
	e := openSwitchyardEnv(t, func(c *config.Config) { c.Switchyard.OpenHostRelayKBps = 256; c.Switchyard.RelayKBps = 0 })
	host := dialFakeHostAuth(t, e, "hosted-agent-token", "")
	if took := relayOneMiB(t, e, host); took < 1500*time.Millisecond {
		t.Fatalf("an open host sent 1 MiB in %v under a bound of 256 KiB/s", took)
	}
}

// A hosted session takes its viewer cap from the config, as a server session does.
func TestHostedSessionsTakeMaxViewersFromTheConfig(t *testing.T) {
	e := switchyardEnv(t, true)
	e2 := newTestEnv(t, func(c *config.Config) { c.Switchyard.Enabled = true; c.MaxViewersPerSession = 1 })
	_ = e
	host := dialFakeHost(t, e2, "hosted-agent-token")
	v1 := dialViewer(t, e2, host.sessionID, adminToken)
	v1.hello(80, 24)
	v1.expectControl(proto.CtlWelcome)
	v2 := dialViewer(t, e2, host.sessionID, adminToken)
	v2.hello(80, 24)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := v2.read()
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusCode(proto.CloseTooManyViewers) {
				t.Fatalf("second viewer: %v", err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the second viewer was let in past a cap of one")
		}
	}
}
