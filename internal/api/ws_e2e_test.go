package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// wsClient is a minimal viewer used by the end-to-end tests.
type wsClient struct {
	t *testing.T
	c *websocket.Conn
}

func dialViewer(t *testing.T, e *testEnv, sessionID, token string) *wsClient {
	t.Helper()
	url := strings.Replace(e.http.URL, "http://", "ws://", 1) + "/ws/sessions/" + sessionID + "?token=" + token
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.SetReadLimit(proto.MaxFrame)
	t.Cleanup(func() { c.CloseNow() })
	return &wsClient{t: t, c: c}
}

func (w *wsClient) send(frame []byte) {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.c.Write(ctx, websocket.MessageBinary, frame); err != nil {
		w.t.Fatalf("write: %v", err)
	}
}

func (w *wsClient) hello(cols, rows uint16) {
	w.send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: cols, Rows: rows, Client: "test"}))
}

func (w *wsClient) read() (proto.Frame, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := w.c.Read(ctx)
	if err != nil {
		return proto.Frame{}, err
	}
	return proto.Decode(data)
}

// expectControl reads frames until a control message with the given t arrives.
func (w *wsClient) expectControl(t string) map[string]any {
	w.t.Helper()
	for {
		f, err := w.read()
		if err != nil {
			w.t.Fatalf("waiting for %s: %v", t, err)
		}
		if f.Type != proto.TypeControl {
			continue
		}
		var m map[string]any
		json.Unmarshal(f.Payload, &m)
		if m["t"] == t {
			return m
		}
	}
}

// expectOutput reads until the accumulated OUTPUT/SCROLLBACK contains needle.
func (w *wsClient) expectOutput(needle string) {
	w.t.Helper()
	var acc []byte
	for !bytes.Contains(acc, []byte(needle)) {
		f, err := w.read()
		if err != nil {
			w.t.Fatalf("waiting for output %q: %v (have %q)", needle, err, acc)
		}
		if f.Type == proto.TypeOutput || f.Type == proto.TypeScrollback {
			acc = append(acc, f.Payload...)
		}
	}
}

func (w *wsClient) expectClose(code websocket.StatusCode) {
	w.t.Helper()
	for {
		_, err := w.read()
		if err == nil {
			continue
		}
		if got := websocket.CloseStatus(err); got != code {
			w.t.Fatalf("close status %d, want %d (%v)", got, code, err)
		}
		return
	}
}

func TestViewerWebSocketRoundTrip(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")

	ctl := dialViewer(t, e, id, adminToken)
	ctl.hello(100, 30)
	welcome := ctl.expectControl(proto.CtlWelcome)
	if welcome["role"] != "control" || welcome["transport"] != "ws" || welcome["cols"] != float64(100) {
		t.Fatalf("welcome %v", welcome)
	}
	ctl.expectControl(proto.CtlReady)
	ctl.send(proto.Encode(proto.TypeInput, []byte("ping\n")))
	ctl.expectOutput("ping")

	// A view link joins late and receives the scrollback.
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	viewToken := lo["token"].(string)
	linkID := lo["link"].(map[string]any)["id"].(string)
	viewer := dialViewer(t, e, id, viewToken)
	viewer.hello(80, 24)
	w2 := viewer.expectControl(proto.CtlWelcome)
	if w2["role"] != "view" || w2["cols"] != float64(100) {
		t.Fatalf("viewer welcome %v", w2)
	}
	viewer.expectOutput("ping")
	viewer.expectControl(proto.CtlReady)
	viewer.send(proto.Encode(proto.TypeInput, []byte("x")))
	if m := viewer.expectControl(proto.CtlError); m["code"] != proto.ErrCodeReadOnly {
		t.Fatalf("expected read_only, got %v", m)
	}
	// controller resize is broadcast to the viewer
	ctl.send(proto.MustControl(proto.Resize{T: proto.CtlResize, Cols: 120, Rows: 40}))
	if m := viewer.expectControl(proto.CtlResize); m["cols"] != float64(120) {
		t.Fatalf("resize broadcast %v", m)
	}
	// ping/pong
	ctl.send(proto.MustControl(proto.Ping{T: proto.CtlPing, TS: 42}))
	if m := ctl.expectControl(proto.CtlPong); m["ts"] != float64(42) {
		t.Fatalf("pong %v", m)
	}
	// revoke disconnects the viewer with 4403
	resp, _ := e.do("DELETE", "/api/sessions/"+id+"/links/"+linkID, adminToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke %d", resp.StatusCode)
	}
	viewer.expectClose(proto.CloseForbidden)

	// stopping the session notifies the controller
	e.do("DELETE", "/api/sessions/"+id, adminToken, nil)
	if m := ctl.expectControl(proto.CtlStatus); m["status"] != "stopped" {
		t.Fatalf("status %v", m)
	}
}

func TestViewerWebSocketAuthFailures(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	bad := dialViewer(t, e, id, "nope")
	bad.expectClose(proto.CloseUnauthorized)
	missing := dialViewer(t, e, "0000000000000000", adminToken)
	missing.expectClose(proto.CloseNotFound)
	// hello timeout / wrong first frame
	noHello := dialViewer(t, e, id, adminToken)
	noHello.send(proto.Encode(proto.TypeInput, []byte("x")))
	noHello.expectClose(proto.CloseProtocolError)
}

func TestViewerFileGet(t *testing.T) {
	e := newTestEnv(t, nil)
	os.WriteFile(filepath.Join(e.root, "a.go"), []byte("package a\n"), 0o600)
	big := bytes.Repeat([]byte("b"), 300<<10)
	os.WriteFile(filepath.Join(e.root, "big.txt"), big, 0o600)
	id := e.createSession("cat")
	c := dialViewer(t, e, id, adminToken)
	c.hello(80, 24)
	if w := c.expectControl(proto.CtlWelcome); w["fileView"] != true {
		t.Fatalf("fileView %v", w)
	}
	c.expectControl(proto.CtlReady)
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "r1", Path: "a.go"}))
	h, body := c.expectFile("r1")
	if h.Kind != "file" || string(body) != "package a\n" {
		t.Fatalf("file %+v %q", h, body)
	}
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "r2", Path: "big.txt"}))
	h, body = c.expectFile("r2")
	if h.Kind != "file" || len(body) != len(big) {
		t.Fatalf("big %+v %d", h, len(body))
	}
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "r3", Path: "../outside"}))
	if h, _ := c.expectFile("r3"); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("escape %+v", h)
	}
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "r4", Path: ".", Stat: true}))
	if h, _ := c.expectFile("r4"); h.Kind != "dir" || len(h.Entries) != 0 {
		t.Fatalf("stat dir %+v", h)
	}
}

func (w *wsClient) expectFile(reqID string) (proto.FileHeader, []byte) {
	w.t.Helper()
	for {
		f, err := w.read()
		if err != nil {
			w.t.Fatalf("waiting for file %s: %v", reqID, err)
		}
		if f.Type != proto.TypeFile {
			continue
		}
		h, body, err := proto.DecodeFile(f.Payload)
		if err != nil {
			w.t.Fatal(err)
		}
		if h.ReqID == reqID {
			return h, body
		}
	}
}

func TestAttentionViaAgentTokenBellAndEvents(t *testing.T) {
	e := newTestEnv(t, nil)
	// A session that prints its own notify token, then behaves like cat.
	cat, _ := catalog.Load(catalog.File{DisableDefaults: true, Agents: []catalog.Agent{
		{ID: "env", Name: "env", Command: []string{"/bin/sh", "-c", "echo TOKEN=$CONDUCTOR_NOTIFY_TOKEN URL=$CONDUCTOR_NOTIFY_URL; exec /bin/cat"}},
	}})
	e.srv.catalog = cat
	id := e.createSession("env")

	// Events stream: subscribe before the changes.
	evReq, _ := http.NewRequest("GET", e.http.URL+"/api/events", nil)
	evReq.Header.Set("Authorization", "Bearer "+adminToken)
	evResp, err := e.client.Do(evReq)
	if err != nil {
		t.Fatal(err)
	}
	defer evResp.Body.Close()
	events := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(evResp.Body)
		var name string
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				name = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				events <- name + " " + strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	waitEvent := func(pred func(string) bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev := <-events:
				if pred(ev) {
					return
				}
			case <-deadline:
				t.Fatal("event not observed")
			}
		}
	}
	waitEvent(func(ev string) bool { return strings.HasPrefix(ev, "snapshot ") && strings.Contains(ev, id) })

	c := dialViewer(t, e, id, adminToken)
	c.hello(80, 24)
	// The token line is usually already in the scrollback replay.
	var acc []byte
	for !bytes.Contains(acc, []byte("URL=")) || !bytes.Contains(acc, []byte("\n")) {
		f, err := c.read()
		if err != nil {
			t.Fatal(err)
		}
		if f.Type == proto.TypeOutput || f.Type == proto.TypeScrollback {
			acc = append(acc, f.Payload...)
		}
	}
	line := string(acc[bytes.Index(acc, []byte("TOKEN=")):])
	line = strings.TrimSpace(strings.SplitN(line, "\n", 2)[0])
	fields := strings.Fields(line)
	token := strings.TrimPrefix(fields[0], "TOKEN=")
	url := strings.TrimPrefix(fields[1], "URL=")
	if len(token) != 43 || !strings.HasSuffix(url, "/api/sessions/"+id+"/attention") {
		t.Fatalf("env line %q", line)
	}
	url = e.http.URL + url[strings.Index(url, "/api/"):]

	// Wrong token is rejected; the agent token works.
	resp, out := e.do("POST", "/api/sessions/"+id+"/attention", "nope", map[string]any{"state": "needs_input"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("POST", "/api/sessions/"+id+"/attention", token, map[string]any{"state": "needs_input", "message": "approve the plan"})
	if resp.StatusCode != 200 {
		t.Fatalf("agent token: %d %v", resp.StatusCode, out)
	}
	if m := c.expectControl(proto.CtlAttention); m["state"] != "needs_input" || m["message"] != "approve the plan" || m["source"] != "api" {
		t.Fatalf("attention frame %v", m)
	}
	waitEvent(func(ev string) bool {
		return strings.HasPrefix(ev, "session ") && strings.Contains(ev, `"state":"needs_input"`)
	})
	_, out = e.do("GET", "/api/sessions/"+id, adminToken, nil)
	if att := out["session"].(map[string]any)["attention"].(map[string]any); att["state"] != "needs_input" {
		t.Fatalf("listing attention %v", att)
	}
	resp, _ = e.do("POST", "/api/sessions/"+id+"/attention", token, map[string]any{"state": "bogus"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bogus state: %d", resp.StatusCode)
	}

	// Typing clears it.
	c.send(proto.Encode(proto.TypeInput, []byte("k")))
	if m := c.expectControl(proto.CtlAttention); m["state"] != "" || m["source"] != "input" {
		t.Fatalf("clear frame %v", m)
	}
	// A bell in the output sets it again with source bell.
	c.send(proto.Encode(proto.TypeInput, []byte("\a\n")))
	if m := c.expectControl(proto.CtlAttention); m["state"] != "needs_input" || m["source"] != "bell" {
		t.Fatalf("bell frame %v", m)
	}
	// Admin can clear.
	resp, _ = e.do("POST", "/api/sessions/"+id+"/attention", adminToken, map[string]any{"state": "clear"})
	if resp.StatusCode != 200 {
		t.Fatalf("admin clear: %d", resp.StatusCode)
	}
	if m := c.expectControl(proto.CtlAttention); m["state"] != "" || m["source"] != "admin" {
		t.Fatalf("admin clear frame %v", m)
	}
	// Events without the admin token, or with it only in the query, are refused.
	resp, _ = e.do("GET", "/api/events", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("events auth: %d", resp.StatusCode)
	}
	resp, _ = e.do("GET", "/api/events?token="+adminToken, "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("events query token must be refused: %d", resp.StatusCode)
	}
}

func (w *wsClient) helloNamed(cols, rows uint16, name string) {
	w.send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: cols, Rows: rows, Client: "test", Name: name}))
}

func TestViewerRosterCarriesNamesAndLinkLabels(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")

	owner := dialViewer(t, e, id, adminToken)
	owner.helloNamed(80, 24, "  Jordan\x07 ")
	owner.expectControl(proto.CtlReady)

	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "control", "label": "pairing"})
	guest := dialViewer(t, e, id, lo["token"].(string))
	guest.helloNamed(80, 24, "Priya Shah")
	guest.expectControl(proto.CtlReady)

	// The owner receives the roster broadcast when the guest joins.
	var roster map[string]any
	for i := 0; i < 5; i++ {
		roster = owner.expectControl(proto.CtlViewers)
		if roster["count"] == float64(2) {
			break
		}
	}
	list, _ := roster["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("roster %v", roster)
	}
	names := map[string]map[string]any{}
	for _, v := range list {
		m := v.(map[string]any)
		names[m["name"].(string)] = m
	}
	if names["Jordan"] == nil || names["Priya Shah"] == nil {
		t.Fatalf("names %v", names)
	}
	if names["Priya Shah"]["link"] != "pairing" || names["Priya Shah"]["role"] != "control" {
		t.Fatalf("guest entry %v", names["Priya Shah"])
	}
	if names["Jordan"]["link"] != nil {
		t.Fatalf("owner should carry no link label %v", names["Jordan"])
	}

	// An absurdly long name is a protocol error, not a crash.
	long := dialViewer(t, e, id, adminToken)
	long.helloNamed(80, 24, strings.Repeat("n", 1000))
	long.expectClose(proto.CloseProtocolError)
}

// fakeHost is a `conductor host` reduced to its control connection: it
// registers a session and then sends and reads the JSON messages by hand, so
// a test decides exactly what the server hears and checks exactly what it
// says back.
type fakeHost struct {
	t         *testing.T
	c         *websocket.Conn
	sessionID string
}

func dialFakeHost(t *testing.T, e *testEnv, agentToken string) *fakeHost {
	t.Helper()
	url := strings.Replace(e.http.URL, "http://", "ws://", 1) + "/ws/host"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer test-host-token"}}})
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	c.SetReadLimit(proto.MaxHostMessage + proto.MaxFrame)
	t.Cleanup(func() { c.CloseNow() })
	h := &fakeHost{t: t, c: c}
	h.send(proto.Register{
		T: proto.HostRegister, Proto: proto.ProtoVersion, Host: proto.HostInfo{Name: "laptop"},
		Session: proto.HostSession{Name: "hosted", AgentID: "cat", Command: []string{"cat"}, Cwd: e.root, Cols: 80, Rows: 24, AgentToken: agentToken},
	})
	h.sessionID, _ = h.expect(proto.HostRegistered)["sessionId"].(string)
	if h.sessionID == "" {
		t.Fatal("the server registered no session")
	}
	return h
}

func (h *fakeHost) send(v any) {
	h.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(h.t.Context(), 5*time.Second)
	defer cancel()
	if err := h.c.Write(ctx, websocket.MessageText, b); err != nil {
		h.t.Fatalf("host write: %v", err)
	}
}

// expect reads messages from the server until one with the given t arrives.
func (h *fakeHost) expect(t string) map[string]any {
	h.t.Helper()
	for {
		ctx, cancel := context.WithTimeout(h.t.Context(), 5*time.Second)
		typ, data, err := h.c.Read(ctx)
		cancel()
		if err != nil {
			h.t.Fatalf("host waiting for %s: %v", t, err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			h.t.Fatalf("host read %q: %v", data, err)
		}
		if m["t"] == t {
			return m
		}
	}
}

func hostActivity(typ string, mutate func(*proto.Activity)) proto.HostActivityMsg {
	a := proto.Activity{T: proto.CtlActivity, At: time.Now().UTC().Format(time.RFC3339Nano), Type: typ}
	if mutate != nil {
		mutate(&a)
	}
	return proto.HostActivityMsg{T: proto.HostActivity, Entry: a}
}

// quiet fails if an event whose text starts with prefix arrives within d.
func quiet(t *testing.T, events <-chan string, d time.Duration, prefix string) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev := <-events:
			if strings.HasPrefix(ev, prefix) {
				t.Fatalf("unexpected %.200s", ev)
			}
		case <-deadline:
			return
		}
	}
}

func TestHostActivityReachesTheEventStream(t *testing.T) {
	e := newTestEnv(t, nil)
	host := dialFakeHost(t, e, "hosted-agent-token")
	events := e.sse(t)

	// The entry is the host's; the session id it names is not believed.
	msg := hostActivity(session.ActivityToolUse, func(a *proto.Activity) {
		a.By, a.ByName, a.Message, a.Tool = "0123456789abcdef", "Ada", "ran\x00 it", "Bash"
	})
	msg.SessionID = "someoneelse00000"
	host.send(msg)
	got := activityPayload(t, e.waitEvent(t, events, isActivity("tool_use", host.sessionID)))
	if got["sessionId"] != host.sessionID || got["by"] != "0123456789abcdef" || got["byName"] != "Ada" || got["message"] != "ran it" || got["tool"] != "Bash" {
		t.Fatalf("entry %v", got)
	}

	// What the server cannot use is dropped without costing the host its
	// connection: an entry of a type nobody has, a message from a newer host,
	// and an entry far too big.
	host.send(hostActivity("bogus", func(a *proto.Activity) { a.Message = "bogus entry" }))
	host.send(map[string]any{"t": "from_the_future", "n": 1})
	host.send(hostActivity(session.ActivityError, func(a *proto.Activity) {
		a.Message, a.URL, a.Tool, a.ByName = strings.Repeat("m", 8000), "https://x/"+strings.Repeat("u", 8000), strings.Repeat("k", 500), strings.Repeat("n", 500)
	}))
	host.send(hostActivity(session.ActivityProgress, func(a *proto.Activity) { a.Message = "marker" }))
	sawBogus := false
	var big map[string]any
	e.waitEvent(t, events, func(ev string) bool {
		if strings.Contains(ev, "bogus") {
			sawBogus = true
		}
		if isActivity(session.ActivityError, host.sessionID)(ev) {
			big = activityPayload(t, ev)
		}
		return strings.Contains(ev, `"message":"marker"`)
	})
	if sawBogus {
		t.Fatal("an entry of an unknown type was streamed")
	}
	if big == nil {
		t.Fatal("the big entry was not streamed")
	}
	if len(big["message"].(string)) != session.MaxAttentionMessage || len(big["url"].(string)) > session.MaxEventURL || len(big["tool"].(string)) != session.MaxEventTool || len([]rune(big["byName"].(string))) != proto.MaxNameLen {
		t.Fatalf("the big entry was not cut: message %d, url %d, tool %d, byName %d", len(big["message"].(string)), len(big["url"].(string)), len(big["tool"].(string)), len(big["byName"].(string)))
	}
}

// A message that cannot be read is the host's mistake, as for every other
// host message, and ends the connection with a protocol error.
func TestHostActivityThatIsNotAnEntryClosesTheConnection(t *testing.T) {
	e := newTestEnv(t, nil)
	host := dialFakeHost(t, e, "hosted-agent-token")
	host.send(map[string]any{"t": proto.HostActivity, "entry": "not an entry"})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := host.c.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != proto.CloseProtocolError {
				t.Fatalf("closed with %v, want %d", err, proto.CloseProtocolError)
			}
			return
		}
	}
}

func TestEventsRouteForwardsAHostedSessionsEventsToItsHost(t *testing.T) {
	e := newTestEnv(t, nil)
	host := dialFakeHost(t, e, "hosted-agent-token")
	events := e.sse(t)
	path := "/api/sessions/" + host.sessionID + "/events"

	resp, out := e.do("POST", path, "hosted-agent-token", map[string]any{
		"type": "artifact", "message": "PR opened\x00", "url": "https://github.com/x/y/pull/1", "to": "review",
	})
	if resp.StatusCode != http.StatusAccepted || out["accepted"] != true {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	m := host.expect(proto.HostActivity)
	entry, _ := m["entry"].(map[string]any)
	if _, named := m["sessionId"]; named {
		t.Fatalf("the server named the session: %v", m)
	}
	if entry["t"] != proto.CtlActivity || entry["type"] != "artifact" || entry["byName"] != "agent" || entry["message"] != "PR opened" ||
		entry["url"] != "https://github.com/x/y/pull/1" || entry["to"] != "review" || entry["at"] != "" {
		t.Fatalf("the host was sent %v", m)
	}

	// The server has no session of its own to record in: nothing streams
	// until the host has recorded the entry and reports it.
	quiet(t, events, 200*time.Millisecond, "activity ")
	entry["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	host.send(map[string]any{"t": proto.HostActivity, "sessionId": host.sessionID, "entry": entry})
	got := activityPayload(t, e.waitEvent(t, events, isActivity("artifact", host.sessionID)))
	if got["url"] != "https://github.com/x/y/pull/1" || got["byName"] != "agent" {
		t.Fatalf("streamed %v", got)
	}

	// What the host reports is not sent back to it, or the two would echo
	// each other for ever: the next thing the host hears is the next event.
	if resp, out := e.do("POST", path, "hosted-agent-token", map[string]any{"type": "progress", "message": "marker"}); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	if next, _ := host.expect(proto.HostActivity)["entry"].(map[string]any); next["message"] != "marker" {
		t.Fatalf("the host was sent %v before the marker", next)
	}
}

// Attention types are applied to a hosted session as /attention applies
// them: recorded on the server and told to the host.
func TestEventsRouteAttentionTypesReachAHostLikeTheAttentionRoute(t *testing.T) {
	e := newTestEnv(t, nil)
	host := dialFakeHost(t, e, "hosted-agent-token")
	d, _ := e.srv.registry.Get(host.sessionID)
	options := []map[string]any{{"label": "Yes", "input": "1"}}

	resp, out := e.do("POST", "/api/sessions/"+host.sessionID+"/events", "hosted-agent-token", map[string]any{"type": "needs_input", "message": "Allow Bash?", "kind": "permission", "options": options})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	m := host.expect(proto.HostAttention)
	if m["state"] != "needs_input" || m["message"] != "Allow Bash?" || m["kind"] != "permission" || m["source"] != session.SourceAPI || len(m["options"].([]any)) != 1 {
		t.Fatalf("the host was told %v", m)
	}
	if att := d.Info().Attention; att.State != session.AttentionNeedsInput || att.Kind != "permission" {
		t.Fatalf("server attention %+v", att)
	}

	resp, out = e.do("POST", "/api/sessions/"+host.sessionID+"/attention", "hosted-agent-token", map[string]any{"state": "working"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	if m := host.expect(proto.HostAttention); m["state"] != "working" || m["source"] != session.SourceAPI {
		t.Fatalf("the host was told %v", m)
	}
}

// A report about a hosted session whose host is away has nowhere to be
// recorded; the caller is told, not given a 202.
func TestEventsRouteTellsWhenTheHostIsAway(t *testing.T) {
	e := newTestEnv(t, nil)
	host := dialFakeHost(t, e, "hosted-agent-token")
	d, _ := e.srv.registry.Get(host.sessionID)
	host.c.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for d.Info().Status != session.StatusHostDisconnected {
		if time.Now().After(deadline) {
			t.Fatalf("status %s", d.Info().Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	path := "/api/sessions/" + host.sessionID + "/events"
	resp, out := e.do("POST", path, "hosted-agent-token", map[string]any{"type": "progress", "message": "1/7"})
	if resp.StatusCode != http.StatusConflict || errorCode(out) != "host_disconnected" {
		t.Fatalf("event for a hostless session: %d %v", resp.StatusCode, out)
	}
	// Attention is held on the server meanwhile, as /attention holds it.
	if resp, out := e.do("POST", path, "hosted-agent-token", map[string]any{"type": "needs_input", "message": "?"}); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("attention for a hostless session: %d %v", resp.StatusCode, out)
	}
	if d.Info().Attention.State != session.AttentionNeedsInput {
		t.Fatalf("attention %+v", d.Info().Attention)
	}
}
