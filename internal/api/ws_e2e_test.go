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
