package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/store"
)

const adminToken = "test-admin-token"

type testEnv struct {
	t      *testing.T
	srv    *Server
	http   *httptest.Server
	root   string
	client *http.Client
}

func newTestEnv(t *testing.T, mutate func(*config.Config)) *testEnv {
	t.Helper()
	return newTestEnvLogging(t, mutate, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newTestEnvLogging is newTestEnv with the server logging to log.
func newTestEnvLogging(t *testing.T, mutate func(*config.Config), log *slog.Logger) *testEnv {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.AdminToken = adminToken
	cfg.HostTokens = []string{"test-host-token"}
	cfg.AllowedRoots = []string{root}
	cfg.DefaultCwd = root
	cfg.PublicURL = "http://example.test"
	cfg.Dev = true
	if mutate != nil {
		mutate(cfg)
	}
	// The store opens at cfg.DataDir, as in `conductor serve`; unless a test
	// places it, that is a directory outside the session root.
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(t.TempDir(), "data")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(catalog.File{DisableDefaults: true, Agents: []catalog.Agent{
		{ID: "cat", Name: "cat", Command: []string{"/bin/cat"}},
		{ID: "sh", Name: "sh", Command: []string{"/bin/sh"}, AllowArgs: true},
		{ID: "exit", Name: "exit", Command: []string{"/bin/sh", "-c", "exit 4"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(cfg, cat, log, nil, st)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		hs.Close()
	})
	// Webhooks are delivered by goroutines of their own: they stop before the
	// endpoints a test started for them close.
	t.Cleanup(func() { srv.webhooks.close(context.Background()) })
	return &testEnv{t: t, srv: srv, http: hs, root: root, client: hs.Client()}
}

func (e *testEnv) do(method, path, token string, body any) (*http.Response, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.http.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	data, _ := io.ReadAll(resp.Body)
	if len(data) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(data, &out)
	}
	return resp, out
}

func (e *testEnv) createSession(agent string) string {
	e.t.Helper()
	resp, out := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": agent})
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("create session: %d %v", resp.StatusCode, out)
	}
	id, _ := out["id"].(string)
	e.t.Cleanup(func() {
		if d, ok := e.srv.registry.Get(id); ok {
			_ = d.Stop(e.t.Context())
		}
	})
	return id
}

// agentToken gives a server session a known agent token, as the process in it
// receives one, and returns it.
func (e *testEnv) agentToken(id string) string {
	e.t.Helper()
	d, ok := e.srv.registry.Get(id)
	local, isLocal := d.(*session.Local)
	if !ok || !isLocal {
		e.t.Fatalf("session %s is not a server session", id)
	}
	tok := "agent-token-" + id
	local.SetAgentToken(tok)
	return tok
}

// sse opens GET /api/events as the admin and returns what it streams as
// "<event> <data>" strings, the snapshot first. The subscription is in place
// when sse returns, and the stream is read for as long as the test runs, so a
// test that posts a great many events cannot stall it.
func (e *testEnv) sse(t *testing.T) <-chan string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET", e.http.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events stream: %d", resp.StatusCode)
	}
	t.Cleanup(func() { resp.Body.Close() })
	events := make(chan string, 4096)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				select {
				case events <- name + " " + strings.TrimPrefix(line, "data: "):
				case <-t.Context().Done():
					return
				}
			}
		}
	}()
	return events
}

// waitEvent reads events until match accepts one and returns it. Events
// before it are discarded.
func (e *testEnv) waitEvent(t *testing.T, events <-chan string, match func(string) bool) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("event not observed")
		}
	}
}

// activityPayload decodes the data of an "activity <json>" event.
func activityPayload(t *testing.T, ev string) map[string]any {
	t.Helper()
	data, ok := strings.CutPrefix(ev, "activity ")
	if !ok {
		t.Fatalf("not an activity event: %.200s", ev)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("activity data: %v in %.200s", err, data)
	}
	return m
}

func isActivity(typ, sessionID string) func(string) bool {
	return func(ev string) bool {
		return strings.HasPrefix(ev, "activity ") && strings.Contains(ev, `"type":"`+typ+`"`) && strings.Contains(ev, sessionID)
	}
}

// waitEnded blocks until the server session has ended.
func (e *testEnv) waitEnded(id string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d, ok := e.srv.registry.Get(id); ok && d.Info().Status.Ended() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("session %s did not end", id)
}

func errorCode(out map[string]any) any {
	if m, ok := out["error"].(map[string]any); ok {
		return m["code"]
	}
	return nil
}

func TestAuthAndHealth(t *testing.T) {
	e := newTestEnv(t, nil)
	resp, out := e.do("GET", "/api/health", "", nil)
	if resp.StatusCode != 200 || out["ok"] != true {
		t.Fatalf("health %d %v", resp.StatusCode, out)
	}
	resp, _ = e.do("GET", "/api/sessions", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	resp, _ = e.do("GET", "/api/sessions", "wrong", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", resp.StatusCode)
	}
	resp, out = e.do("GET", "/api/catalog", adminToken, nil)
	if resp.StatusCode != 200 || len(out["agents"].([]any)) != 3 {
		t.Fatalf("catalog %d %v", resp.StatusCode, out)
	}
	if resp.Header.Get("Referrer-Policy") != "no-referrer" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("security headers missing: %v", resp.Header)
	}
}

func TestCreateSessionValidation(t *testing.T) {
	e := newTestEnv(t, nil)
	cases := []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"agentId": "nope"}, "invalid_agent"},
		{map[string]any{"agentId": "cat", "args": []string{"x"}}, "args_not_allowed"},
		{map[string]any{"agentId": "cat", "cwd": "/"}, "invalid_cwd"},
		{map[string]any{"agentId": "cat", "cwd": filepath.Join(e.root, "missing")}, "invalid_cwd"},
		{map[string]any{"agentId": "cat", "cols": 9999}, "invalid_request"},
		{map[string]any{"agentId": "cat", "extra": true}, "invalid_request"},
	}
	for i, c := range cases {
		resp, out := e.do("POST", "/api/sessions", adminToken, c.body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("case %d: status %d %v", i, resp.StatusCode, out)
		}
		if got := out["error"].(map[string]any)["code"]; got != c.code {
			t.Fatalf("case %d: code %v want %s", i, got, c.code)
		}
	}
	// symlink escaping the root is rejected even though it resolves
	outside := t.TempDir()
	link := filepath.Join(e.root, "escape")
	os.Symlink(outside, link)
	resp, out := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": "cat", "cwd": link})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("symlink escape: %d %v", resp.StatusCode, out)
	}
}

func TestSessionLifecycle(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.MaxSessions = 2 })
	id := e.createSession("cat")
	resp, out := e.do("GET", "/api/sessions/"+id, adminToken, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get: %d %v", resp.StatusCode, out)
	}
	sess := out["session"].(map[string]any)
	if sess["status"] != "running" || sess["kind"] != "server" || sess["cwd"] != e.root || out["role"] != "control" {
		t.Fatalf("session %v", out)
	}
	if _, ok := out["links"]; !ok {
		t.Fatal("admin view must include links")
	}
	resp, out = e.do("GET", "/api/sessions", adminToken, nil)
	if resp.StatusCode != 200 || len(out["sessions"].([]any)) != 1 {
		t.Fatalf("list %v", out)
	}
	e.createSession("cat")
	resp, out = e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": "cat"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("limit: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("DELETE", "/api/sessions/"+id, adminToken, nil)
	if resp.StatusCode != 200 || out["status"] != "stopped" {
		t.Fatalf("stop: %d %v", resp.StatusCode, out)
	}
	resp, _ = e.do("DELETE", "/api/sessions/"+id, adminToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove: %d", resp.StatusCode)
	}
	resp, _ = e.do("GET", "/api/sessions/"+id, adminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after remove: %d", resp.StatusCode)
	}
}

func TestExitedSessionReportsCode(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("exit")
	d, _ := e.srv.registry.Get(id)
	select {
	case <-d.(*session.Local).Ended():
	case <-time.After(5 * time.Second):
		t.Fatal("did not exit")
	}
	info := d.Info()
	if info.Status != session.StatusExited || info.ExitCode == nil || *info.ExitCode != 4 {
		t.Fatalf("info %+v", info)
	}
}

func TestLinksAndJoin(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	resp, out := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "admin"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad role: %d", resp.StatusCode)
	}
	resp, out = e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view", "label": "qa"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create link: %d %v", resp.StatusCode, out)
	}
	token := out["token"].(string)
	link := out["link"].(map[string]any)
	if !strings.HasPrefix(out["url"].(string), "http://example.test/join/") {
		t.Fatalf("url %v", out["url"])
	}
	resp, out = e.do("GET", "/api/sessions/"+id+"/links", adminToken, nil)
	links := out["links"].([]any)
	if len(links) != 1 || links[0].(map[string]any)["label"] != "qa" {
		t.Fatalf("list links %v", out)
	}
	if _, has := links[0].(map[string]any)["token"]; has {
		t.Fatal("token must not be listed")
	}
	resp, out = e.do("GET", "/api/join/"+token, "", nil)
	if resp.StatusCode != 200 || out["role"] != "view" || out["session"].(map[string]any)["id"] != id {
		t.Fatalf("join %d %v", resp.StatusCode, out)
	}
	// share token can read its own session but nothing else
	resp, out = e.do("GET", "/api/sessions/"+id, token, nil)
	if resp.StatusCode != 200 || out["role"] != "view" {
		t.Fatalf("share get %d %v", resp.StatusCode, out)
	}
	if _, has := out["links"]; has {
		t.Fatal("share token must not see links")
	}
	resp, _ = e.do("GET", "/api/sessions", token, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("share token listing: %d", resp.StatusCode)
	}
	other := e.createSession("cat")
	resp, _ = e.do("GET", "/api/sessions/"+other, token, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("share token cross-session: %d", resp.StatusCode)
	}
	resp, _ = e.do("DELETE", "/api/sessions/"+other+"/links/"+link["id"].(string), adminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoke via wrong session: %d", resp.StatusCode)
	}
	resp, _ = e.do("DELETE", "/api/sessions/"+id+"/links/"+link["id"].(string), adminToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	resp, out = e.do("GET", "/api/join/"+token, "", nil)
	if resp.StatusCode != http.StatusNotFound || out["error"].(map[string]any)["code"] != "revoked" {
		t.Fatalf("join after revoke %d %v", resp.StatusCode, out)
	}
}

func TestFilesEndpoint(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.FileView = config.FileViewControl })
	os.WriteFile(filepath.Join(e.root, "notes.md"), []byte("# hi\n"), 0o600)
	id := e.createSession("cat")
	resp, out := e.do("GET", "/api/sessions/"+id+"/files?path=notes.md", adminToken, nil)
	if resp.StatusCode != 200 || out["content"] != "# hi\n" {
		t.Fatalf("file %d %v", resp.StatusCode, out)
	}
	resp, _ = e.do("GET", "/api/sessions/"+id+"/files?path=../etc/passwd", adminToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("escape: %d", resp.StatusCode)
	}
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	resp, _ = e.do("GET", "/api/sessions/"+id+"/files?path=notes.md", lo["token"].(string), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("view role under control policy: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", e.http.URL+"/api/sessions/"+id+"/files?path=notes.md&raw=1", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	raw, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(raw.Body)
	raw.Body.Close()
	if string(body) != "# hi\n" || !strings.HasPrefix(raw.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("raw %q %s", body, raw.Header.Get("Content-Type"))
	}
}

// The data directory holds catalog.json, whose env values are secrets. When it
// sits inside a session's working directory, as it did by default in the
// Docker image and with conductor.example.json, no file read may reach it: not
// the HTTP route and not the in-band file_get, not through a view link and not
// with the admin token. The rest of the working directory stays readable.
func TestFileReadsNeverReachTheDataDirectory(t *testing.T) {
	const secret = "sk-live-do-not-share"
	e := newTestEnv(t, func(c *config.Config) { c.DataDir = filepath.Join(c.DefaultCwd, "conductor.d") })
	body := agentBody("keyed")
	body["env"] = map[string]string{"OPENAI_API_KEY": secret}
	e.save(body)
	if err := os.WriteFile(filepath.Join(e.root, "notes.md"), []byte("# hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A symlink inside the working directory is no way around the rule.
	if err := os.Symlink(filepath.Join(e.root, "conductor.d"), filepath.Join(e.root, "state")); err != nil {
		t.Fatal(err)
	}
	id := e.createSession("keyed")
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	view := lo["token"].(string)

	denied := []string{
		"conductor.d/catalog.json",
		filepath.Join(e.root, "conductor.d", "catalog.json"),
		"state/catalog.json",
		"conductor.d",
		"conductor.d/no-such-file.json", // denied, not not_found: nothing about its contents leaks
	}
	for _, token := range []string{view, adminToken} {
		for _, p := range denied {
			for _, mode := range []string{"raw", "stat", ""} {
				if status, got, code := e.getFile(id, token, p, mode); status != http.StatusForbidden || code != "denied" || strings.Contains(got, secret) {
					t.Errorf("HTTP %q (mode %q): %d %s", p, mode, status, got)
				}
			}
		}
		if status, got, _ := e.getFile(id, token, "notes.md", "raw"); status != http.StatusOK || got != "# hi\n" {
			t.Fatalf("ordinary file over HTTP: %d %q", status, got)
		}
	}

	c := dialViewer(t, e, id, view)
	c.hello(80, 24)
	c.expectControl(proto.CtlReady)
	for i, p := range denied {
		for _, stat := range []bool{false, true} {
			reqID := fmt.Sprintf("d%d-%t", i, stat)
			c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: reqID, Path: p, Stat: stat}))
			if h, b := c.expectFile(reqID); h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || len(b) != 0 {
				t.Errorf("file_get %q (stat %t): %+v %q", p, stat, h, b)
			}
		}
	}
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "ok", Path: "notes.md"}))
	if h, b := c.expectFile("ok"); h.Kind != "file" || string(b) != "# hi\n" {
		t.Fatalf("ordinary file in-band: %+v %q", h, b)
	}
}

// getFile reads path from session id over HTTP with mode "raw", "stat" or ""
// (JSON) and returns the status, the body and, for a JSON reply, the error
// code.
func (e *testEnv) getFile(id, token, path, mode string) (int, string, string) {
	e.t.Helper()
	q := url.Values{"path": {path}}
	if mode != "" {
		q.Set(mode, "1")
	}
	req, _ := http.NewRequest("GET", e.http.URL+"/api/sessions/"+id+"/files?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out struct {
		File proto.FileHeader `json:"file"`
	}
	code := ""
	if json.Unmarshal(b, &out) == nil && out.File.Error != nil {
		code = out.File.Error.Code
	}
	return resp.StatusCode, string(b), code
}

// The config file holds the admin token and host tokens, and a catalogPath
// file can hold agents' env secrets. `make run` serves from the directory that
// holds conductor.example.json, an allowed root, so a view link could read the
// admin token with GET /api/sessions/{id}/files?path=conductor.example.json.
// Like the data directory, neither file is readable over HTTP or in-band, by
// name, by absolute path or through a symlink; the files beside them are.
func TestFileReadsNeverReachTheConfigOrCatalogFile(t *testing.T) {
	const secret = "sk-live-from-the-catalog-file"
	var configFile, catalogFile string
	e := newTestEnv(t, func(c *config.Config) {
		// What config.Load and Validate leave for a config file and a catalog
		// file inside the session root.
		configFile = filepath.Join(c.DefaultCwd, "conductor.json")
		catalogFile = filepath.Join(c.DefaultCwd, "agents.json")
		c.Path, c.CatalogPath = configFile, catalogFile
	})
	for path, body := range map[string]string{
		configFile:  fmt.Sprintf(`{"adminToken": %q, "hostTokens": ["test-host-token"], "catalogPath": "agents.json"}`, adminToken),
		catalogFile: fmt.Sprintf(`{"agents": [{"id": "keyed", "name": "keyed", "command": ["/bin/cat"], "env": {"OPENAI_API_KEY": %q}}]}`, secret),
		filepath.Join(e.root, "conductor.example.json"): `{"listen": ":8080"}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(configFile, filepath.Join(e.root, "settings.json")); err != nil {
		t.Fatal(err)
	}
	id := e.createSession("cat")
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	view := lo["token"].(string)

	denied := []string{"conductor.json", configFile, "settings.json", "agents.json", catalogFile}
	leaks := func(body string) bool {
		return strings.Contains(body, adminToken) || strings.Contains(body, "test-host-token") || strings.Contains(body, secret)
	}
	for _, token := range []string{view, adminToken} {
		for _, p := range denied {
			for _, mode := range []string{"raw", "stat", ""} {
				if status, got, code := e.getFile(id, token, p, mode); status != http.StatusForbidden || code != "denied" || leaks(got) {
					t.Errorf("HTTP %q (mode %q): %d %s", p, mode, status, got)
				}
			}
		}
		if status, got, _ := e.getFile(id, token, "conductor.example.json", "raw"); status != http.StatusOK || got != `{"listen": ":8080"}` {
			t.Fatalf("the sibling file over HTTP: %d %q", status, got)
		}
	}

	c := dialViewer(t, e, id, view)
	c.hello(80, 24)
	c.expectControl(proto.CtlReady)
	for i, p := range denied {
		for _, stat := range []bool{false, true} {
			reqID := fmt.Sprintf("d%d-%t", i, stat)
			c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: reqID, Path: p, Stat: stat}))
			if h, b := c.expectFile(reqID); h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || len(b) != 0 {
				t.Errorf("file_get %q (stat %t): %+v %q", p, stat, h, b)
			}
		}
	}
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "ok", Path: "conductor.example.json"}))
	if h, b := c.expectFile("ok"); h.Kind != "file" || string(b) != `{"listen": ":8080"}` {
		t.Fatalf("the sibling file in-band: %+v %q", h, b)
	}
}

func TestUnknownRoutesAndNoUI(t *testing.T) {
	e := newTestEnv(t, nil)
	resp, _ := e.do("GET", "/api/nope", adminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("api 404: %d", resp.StatusCode)
	}
	resp, _ = e.do("GET", "/anything", "", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing UI: %d", resp.StatusCode)
	}
}

func TestAttentionKindAndOptions(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	opts := []map[string]any{}
	for i := 0; i < 7; i++ {
		opts = append(opts, map[string]any{"label": "Option " + strings.Repeat("x", i), "input": strings.Repeat("1", i+1)})
	}
	resp, out := e.do("POST", "/api/sessions/"+id+"/attention", adminToken, map[string]any{"state": "needs_input", "message": "Allow Bash?", "kind": "permission", "options": opts})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set attention: %d %v", resp.StatusCode, out)
	}
	_, got := e.do("GET", "/api/sessions/"+id, adminToken, nil)
	att := got["session"].(map[string]any)["attention"].(map[string]any)
	if att["kind"] != "permission" {
		t.Fatalf("kind %v", att)
	}
	if list, _ := att["options"].([]any); len(list) != 6 {
		t.Fatalf("options capped at 6, got %d", len(list))
	}
	resp, out = e.do("POST", "/api/sessions/"+id+"/attention", adminToken, map[string]any{"state": "needs_input", "kind": "bogus"})
	if resp.StatusCode != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "invalid_kind" {
		t.Fatalf("bogus kind: %d %v", resp.StatusCode, out)
	}
}

// An agent whose signal is a pattern gets the detector when it is launched: a
// prompt left on the last line marks its session needs_input. The same prompt
// from an agent with no pattern does nothing.
func TestSessionUsesTheAgentsSignalPattern(t *testing.T) {
	e := newTestEnv(t, nil)
	script := []string{"/bin/sh", "-c", `printf 'Proceed? (y/n) '; exec /bin/cat`}
	watched := agentBody("watched")
	watched["command"] = script
	watched["signal"] = map[string]any{"kind": "pattern", "pattern": `\(y/n\) $`}
	e.save(watched)
	plain := agentBody("plain")
	plain["command"] = script
	e.save(plain)
	watchedID := e.createSession("watched")
	plainID := e.createSession("plain")

	attention := func(id string) map[string]any {
		_, got := e.do("GET", "/api/sessions/"+id, adminToken, nil)
		att, _ := got["session"].(map[string]any)["attention"].(map[string]any)
		return att
	}
	deadline := time.Now().Add(5 * time.Second)
	for attention(watchedID)["state"] != "needs_input" {
		if time.Now().After(deadline) {
			t.Fatalf("attention %v, want needs_input", attention(watchedID))
		}
		time.Sleep(20 * time.Millisecond)
	}
	att := attention(watchedID)
	if att["source"] != "pattern" || att["kind"] != "prompt" || att["message"] != "prompt: Proceed? (y/n)" {
		t.Fatalf("attention %v", att)
	}
	time.Sleep(300 * time.Millisecond) // the other session has been as quiet as long
	if att := attention(plainID); att["state"] != nil && att["state"] != "" {
		t.Fatalf("an agent with no pattern got attention %v", att)
	}
}

// launch starts an agent with the given arguments and returns the session.
func (e *testEnv) launch(agentID string, args []string) map[string]any {
	e.t.Helper()
	resp, out := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": agentID, "args": args})
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("launch %s: %d %v", agentID, resp.StatusCode, out)
	}
	id, _ := out["id"].(string)
	e.t.Cleanup(func() {
		if d, ok := e.srv.registry.Get(id); ok {
			_ = d.Stop(e.t.Context())
		}
	})
	return out
}

// An agent with a hook adapter and the hook signal is launched with the
// adapter's flags after its command and the user's arguments: Claude Code
// reads Conductor's hooks from the hooks directory in the data directory, the
// set with tool events when the signal asks for them. With another signal the
// same agent gets nothing.
func TestCreateSessionInjectsTheAdapter(t *testing.T) {
	e := newTestEnv(t, nil)
	hooks := filepath.Join(e.srv.cfg.DataDir, "hooks")
	script := []string{"/bin/sh", "-c", `printf 'ARGS[%s]\n' "$*"; exec /bin/cat`, "sh"}
	for _, tc := range []struct {
		id     string
		signal map[string]any
		args   []string
		extra  []string
	}{
		{"hooked", map[string]any{"kind": "hook"}, []string{"--resume"}, []string{"--settings", filepath.Join(hooks, "claude.json")}},
		{"chatty", map[string]any{"kind": "hook", "toolEvents": true}, nil, []string{"--settings", filepath.Join(hooks, "claude-tools.json")}},
		{"belled", map[string]any{"kind": "bell"}, nil, nil},
		{"unset", nil, nil, nil},
	} {
		body := agentBody(tc.id)
		body["command"] = script
		body["allowArgs"] = true
		body["adapter"] = "claude"
		if tc.signal != nil {
			body["signal"] = tc.signal
		}
		e.save(body)
		info := e.launch(tc.id, tc.args)
		want := append(append(slices.Clone(script), tc.args...), tc.extra...)
		var got []string
		for _, a := range info["command"].([]any) {
			got = append(got, a.(string))
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: command %q, want %q", tc.id, got, want)
		}
		c := dialViewer(t, e, info["id"].(string), adminToken)
		c.hello(80, 24)
		c.expectOutput("ARGS[" + strings.Join(append(slices.Clone(tc.args), tc.extra...), " ") + "]")
	}
}

// The adapter's environment reaches the session, under the agent's own: a
// variable the agent sets itself keeps its value.
func TestCreateSessionAdapterEnvYieldsToTheAgent(t *testing.T) {
	t.Setenv("AIDER_NOTIFICATIONS", "from-the-server")
	e := newTestEnv(t, nil)
	_, env := agents.InjectFor("aider", filepath.Join(e.srv.cfg.DataDir, "hooks"), catalog.Signal{Kind: "hook"})
	command := env["AIDER_NOTIFICATIONS_COMMAND"]
	if !strings.Contains(command, " notify --state needs_input") {
		t.Fatalf("aider's command %q", command)
	}
	script := []string{"/bin/sh", "-c", `echo "N=[$AIDER_NOTIFICATIONS] C=[$AIDER_NOTIFICATIONS_COMMAND] END"; exec /bin/cat`}
	for _, tc := range []struct {
		id   string
		env  map[string]string
		want string
	}{
		{"aider-plain", nil, "N=[true] C=[" + command + "] END"},
		{"aider-own", map[string]string{"AIDER_NOTIFICATIONS": "false"}, "N=[false] C=[" + command + "] END"},
	} {
		body := agentBody(tc.id)
		body["command"] = script
		body["adapter"] = "aider"
		body["signal"] = map[string]any{"kind": "hook"}
		if tc.env != nil {
			body["env"] = tc.env
		}
		e.save(body)
		id := e.createSession(tc.id)
		c := dialViewer(t, e, id, adminToken)
		c.hello(80, 24)
		c.expectOutput(tc.want)
	}
}

// An adapter adds flags, not a new way to fail: a command the server does not
// have still answers start_failed.
func TestCreateSessionWithAnAdapterAndAMissingCommand(t *testing.T) {
	e := newTestEnv(t, nil)
	e.save(map[string]any{"id": "ghost", "name": "Ghost", "command": []string{"definitely-not-a-real-binary-xyz"}, "adapter": "claude", "signal": map[string]any{"kind": "hook"}})
	resp, out := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": "ghost"})
	if resp.StatusCode != http.StatusBadGateway || errorCode(out) != "start_failed" {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
	}
}

func TestLinksListReportsActiveViewers(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view", "label": "standup"})
	linkID := lo["link"].(map[string]any)["id"].(string)

	_, before := e.do("GET", "/api/sessions/"+id+"/links", adminToken, nil)
	if l := before["links"].([]any)[0].(map[string]any); l["active"] != float64(0) {
		t.Fatalf("active before join: %v", l)
	}

	guest := dialViewer(t, e, id, lo["token"].(string))
	guest.hello(80, 24)
	guest.expectControl(proto.CtlReady)

	_, after := e.do("GET", "/api/sessions/"+id+"/links", adminToken, nil)
	var found bool
	for _, raw := range after["links"].([]any) {
		l := raw.(map[string]any)
		if l["id"] == linkID {
			found = true
			if l["active"] != float64(1) || l["label"] != "standup" {
				t.Fatalf("active after join: %v", l)
			}
		}
	}
	if !found {
		t.Fatalf("link missing: %v", after)
	}
}

func TestCreateSessionRecordsGitBranch(t *testing.T) {
	e := newTestEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(e.root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := e.createSession("cat")
	_, got := e.do("GET", "/api/sessions/"+id, adminToken, nil)
	if got["session"].(map[string]any)["branch"] != "main" {
		t.Fatalf("branch: %v", got["session"])
	}
}

func TestWhoAmIReportsServerUser(t *testing.T) {
	e := newTestEnv(t, nil)
	resp, _ := e.do("GET", "/api/whoami", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous whoami: %d", resp.StatusCode)
	}
	resp, out := e.do("GET", "/api/whoami", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("whoami: %d %v", resp.StatusCode, out)
	}
	if u, _ := out["user"].(string); u == "" || len(u) > 64 {
		t.Fatalf("user %q", u)
	}
}

func TestCatalogEditingPersistsOverlay(t *testing.T) {
	e := newTestEnv(t, nil) // newTestEnv opens a store in t.TempDir()
	body := map[string]any{"id": "aider", "name": "Aider", "command": []string{"aider", "--no-auto-commits"}, "allowArgs": true, "signal": map[string]any{"kind": "pattern", "pattern": `^> $`}}
	resp, out := e.do("POST", "/api/catalog", adminToken, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %v", resp.StatusCode, out)
	}
	_, list := e.do("GET", "/api/catalog", adminToken, nil)
	var found bool
	for _, raw := range list["agents"].([]any) {
		a := raw.(map[string]any)
		if a["id"] == "aider" && a["signal"].(map[string]any)["kind"] == "pattern" {
			found = true
		}
	}
	if !found {
		t.Fatalf("saved agent missing from catalog: %v", list)
	}
	// Persisted: the overlay file names the agent.
	var ov catalog.Overlay
	if ok, err := e.srv.store.Load("catalog.json", &ov); !ok || err != nil || len(ov.Agents) != 1 || ov.Agents[0].ID != "aider" {
		t.Fatalf("overlay: %v %v %+v", ok, err, ov)
	}
	// Hiding a built-in.
	if resp, _ := e.do("DELETE", "/api/catalog/cat", adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("hide: %d", resp.StatusCode)
	}
	if resp, _ := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": "cat"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("hidden agent still launchable: %d", resp.StatusCode)
	}
	// Validation and unknown fields.
	if resp, out := e.do("POST", "/api/catalog", adminToken, map[string]any{"id": "bad id", "name": "x", "command": []string{"x"}}); resp.StatusCode != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "invalid_agent" {
		t.Fatalf("bad id: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("POST", "/api/catalog", adminToken, map[string]any{"id": "x", "name": "x", "command": []string{"x"}, "bogus": 1}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", resp.StatusCode)
	}
	// Auth.
	if resp, _ := e.do("POST", "/api/catalog", "", body); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous save: %d", resp.StatusCode)
	}
}

func TestCatalogCheckCommand(t *testing.T) {
	e := newTestEnv(t, nil)
	_, out := e.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": []string{"/bin/sh", "-c", "x"}})
	if out["found"] != true || out["path"] != "/bin/sh" {
		t.Fatalf("sh: %v", out)
	}
	_, out = e.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": []string{"definitely-not-a-real-binary-xyz"}})
	if out["found"] != false {
		t.Fatalf("missing: %v", out)
	}
	if resp, _ := e.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": []string{}}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty argv: %d", resp.StatusCode)
	}
}

// serve exposes srv over HTTP next to e's server, sharing its temp root.
func (e *testEnv) serve(srv *Server) *testEnv {
	e.t.Helper()
	hs := httptest.NewServer(srv.Handler())
	e.t.Cleanup(hs.Close)
	return &testEnv{t: e.t, srv: srv, http: hs, root: e.root, client: hs.Client()}
}

// restart starts a second server over the same store and configured catalog,
// the way `conductor serve` comes up again after a restart.
func (e *testEnv) restart() *testEnv {
	e.t.Helper()
	srv, err := New(e.srv.cfg, e.srv.base, e.srv.log, nil, e.srv.store)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.serve(srv)
}

// catalogList returns the agents GET /api/catalog lists, in order.
func (e *testEnv) catalogList() []any {
	e.t.Helper()
	resp, out := e.do("GET", "/api/catalog", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("catalog: %d %v", resp.StatusCode, out)
	}
	return out["agents"].([]any)
}

// catalogAgent returns the agent GET /api/catalog lists under id, or nil.
func (e *testEnv) catalogAgent(id string) map[string]any {
	e.t.Helper()
	for _, raw := range e.catalogList() {
		if a := raw.(map[string]any); a["id"] == id {
			return a
		}
	}
	return nil
}

// catalogIDs returns the agent IDs GET /api/catalog lists, in order.
func (e *testEnv) catalogIDs() []string {
	e.t.Helper()
	var ids []string
	for _, raw := range e.catalogList() {
		ids = append(ids, raw.(map[string]any)["id"].(string))
	}
	return ids
}

// catalogHidden returns the IDs GET /api/catalog lists as hidden. The list
// must be there, as an array, even when nothing is hidden.
func (e *testEnv) catalogHidden() []string {
	e.t.Helper()
	resp, out := e.do("GET", "/api/catalog", adminToken, nil)
	raw, ok := out["hidden"].([]any)
	if resp.StatusCode != http.StatusOK || !ok {
		e.t.Fatalf("catalog hidden list: %d %v", resp.StatusCode, out)
	}
	ids := []string{}
	for _, id := range raw {
		ids = append(ids, id.(string))
	}
	return ids
}

// overlayFile returns the text of catalog.json in the data directory.
func (e *testEnv) overlayFile() string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.srv.store.Dir(), "catalog.json"))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

// save posts an agent to /api/catalog and requires it to be accepted.
func (e *testEnv) save(body map[string]any) map[string]any {
	e.t.Helper()
	resp, out := e.do("POST", "/api/catalog", adminToken, body)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("save %v: %d %v", body["id"], resp.StatusCode, out)
	}
	return out
}

// del sends DELETE /api/catalog/{id} and returns the status.
func (e *testEnv) del(id string) int {
	e.t.Helper()
	resp, _ := e.do("DELETE", "/api/catalog/"+id, adminToken, nil)
	return resp.StatusCode
}

func agentBody(id string) map[string]any {
	return map[string]any{"id": id, "name": id, "command": []string{"/bin/cat"}}
}

func TestCatalogCheckLooksUpWithoutRunning(t *testing.T) {
	e := newTestEnv(t, nil)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "agent")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("not a program"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	cases := []struct {
		name    string
		command []string
		found   bool
		path    string
	}{
		{"absolute path, later elements ignored", []string{script, "--no-such-flag", "no-such-binary"}, true, script},
		{"bare name found through PATH", []string{"agent"}, true, script},
		{"file that is not executable", []string{plain}, false, ""},
		{"bare name of a file that is not executable", []string{"plain"}, false, ""},
		{"missing absolute path", []string{filepath.Join(dir, "missing")}, false, ""},
	}
	for _, tc := range cases {
		resp, out := e.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": tc.command})
		path, hasPath := out["path"]
		if resp.StatusCode != http.StatusOK || out["found"] != tc.found || (tc.found && path != tc.path) || (!tc.found && hasPath) {
			t.Errorf("%s: %d %v", tc.name, resp.StatusCode, out)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the check ran the command")
	}
	if resp, _ := e.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": []string{"sh"}, "bogus": 1}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", resp.StatusCode)
	}
}

func TestCatalogSaveRejectsInvalidAgents(t *testing.T) {
	e := newTestEnv(t, nil)
	with := func(k string, v any) map[string]any {
		b := agentBody("ok")
		b[k] = v
		return b
	}
	cases := []struct {
		name string
		body any
		code string
		msg  string // part of the message
	}{
		{"bad id", with("id", "Bad Id"), "invalid_agent", "must match"},
		{"blank name", with("name", "  "), "invalid_agent", "name must not be empty"},
		{"name over 60 characters", with("name", strings.Repeat("n", 61)), "invalid_agent", "at most 60"},
		{"empty command", with("command", []string{}), "invalid_agent", "at least one element"},
		{"33 command elements", with("command", slices.Repeat([]string{"x"}, 33)), "invalid_agent", "at most 32"},
		{"unknown signal kind", with("signal", map[string]any{"kind": "smoke"}), "invalid_agent", "unknown kind"},
		{"pattern on a bell signal", with("signal", map[string]any{"kind": "bell", "pattern": "x"}), "invalid_agent", "pattern only applies"},
		{"pattern that does not compile", with("signal", map[string]any{"kind": "pattern", "pattern": "("}), "invalid_agent", "error parsing regexp"},
		{"bad envPassthrough name", with("envPassthrough", []string{"NOT OK"}), "invalid_agent", "envPassthrough"},
		{"unknown field", with("bogus", 1), "invalid_request", `"bogus"`},
		{"no body", nil, "invalid_request", ""},
		{"body over 64 KiB", with("description", strings.Repeat("d", 70<<10)), "invalid_request", "too large"},
	}
	for _, tc := range cases {
		resp, out := e.do("POST", "/api/catalog", adminToken, tc.body)
		apiErr, _ := out["error"].(map[string]any)
		msg, _ := apiErr["message"].(string)
		if resp.StatusCode != http.StatusBadRequest || apiErr["code"] != tc.code || !strings.Contains(msg, tc.msg) {
			t.Errorf("%s: %d %v", tc.name, resp.StatusCode, out)
		}
	}
	// A rejected agent is not persisted.
	if ok, err := e.srv.store.Load("catalog.json", &catalog.Overlay{}); ok || err != nil {
		t.Fatalf("overlay written for invalid agents: %v %v", ok, err)
	}
}

func TestCatalogRoutesNeedTheAdminToken(t *testing.T) {
	e := newTestEnv(t, nil)
	routes := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/catalog", agentBody("intruder")},
		{"DELETE", "/api/catalog/cat", nil},
		{"POST", "/api/catalog/check", map[string]any{"command": []string{"sh"}}},
		{"POST", "/api/catalog/cat/unhide", nil},
	}
	for _, r := range routes {
		for _, token := range []string{"", "wrong", "test-host-token"} {
			if resp, _ := e.do(r.method, r.path, token, r.body); resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s with token %q: %d", r.method, r.path, token, resp.StatusCode)
			}
		}
	}
	if ids := e.catalogIDs(); !slices.Equal(ids, []string{"cat", "sh", "exit"}) {
		t.Fatalf("an unauthorized call changed the catalog: %v", ids)
	}
	if ok, err := e.srv.store.Load("catalog.json", &catalog.Overlay{}); ok || err != nil {
		t.Fatalf("an unauthorized call wrote the overlay: %v %v", ok, err)
	}
}

// A running server and one started from the saved overlay must list the same
// agents in the same order, including after a built-in was hidden and saved again.
func TestCatalogEditsMatchARestart(t *testing.T) {
	e := newTestEnv(t, nil)
	snapshot := e.srv.Catalog()

	e.save(agentBody("aider"))
	e.save(map[string]any{"id": "aider", "name": "Aider v2", "command": []string{"aider"}}) // replaces, does not repeat
	if c := e.del("cat"); c != http.StatusNoContent {
		t.Fatalf("hide: %d", c)
	}
	e.save(map[string]any{"id": "cat", "name": "Cat again", "command": []string{"/bin/cat"}}) // back, replacing the built-in
	e.save(map[string]any{"id": "sh", "name": "Shell", "command": []string{"/bin/sh"}})       // replaced in place

	if got, want := e.catalogIDs(), []string{"cat", "sh", "exit", "aider"}; !slices.Equal(got, want) {
		t.Fatalf("live order %v, want %v", got, want)
	}
	if a := e.catalogAgent("aider"); a["name"] != "Aider v2" {
		t.Fatalf("aider: %v", a)
	}
	var ov catalog.Overlay
	if ok, err := e.srv.store.Load("catalog.json", &ov); !ok || err != nil || len(ov.Agents) != 3 || len(ov.Hidden) != 0 {
		t.Fatalf("overlay: %v %v %+v", ok, err, ov)
	}

	again := e.restart()
	if live, restarted := e.catalogList(), again.catalogList(); !reflect.DeepEqual(live, restarted) {
		t.Fatalf("restart differs:\n live      %v\n restarted %v", live, restarted)
	}
	// Snapshots are never edited, so readers need no lock.
	if got := snapshot.List(); len(got) != 3 || got[0].Name != "cat" {
		t.Fatalf("snapshot taken before the edits changed: %+v", got)
	}
}

func TestCatalogDeleteRestoresOrHides(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")

	// The first edit ever is a hide: the file still holds "agents": [], not null.
	if c := e.del("cat"); c != http.StatusNoContent {
		t.Fatalf("hide: %d", c)
	}
	if raw := e.overlayFile(); !strings.Contains(raw, `"agents": []`) || strings.Contains(raw, "null") || !strings.Contains(raw, `"hidden"`) {
		t.Fatalf("overlay after hiding: %s", raw)
	}
	if e.catalogAgent("cat") != nil {
		t.Fatal("hidden agent still listed")
	}
	// Hiding does not stop a session that is already running.
	if resp, out := e.do("GET", "/api/sessions/"+id, adminToken, nil); resp.StatusCode != http.StatusOK || out["session"].(map[string]any)["status"] != "running" {
		t.Fatalf("running session after hide: %d %v", resp.StatusCode, out)
	}

	// Saving the ID again shows it again, replacing the built-in.
	e.save(map[string]any{"id": "cat", "name": "Cat mk2", "command": []string{"/bin/cat"}})
	if a := e.catalogAgent("cat"); a == nil || a["name"] != "Cat mk2" {
		t.Fatalf("saved over the hidden built-in: %v", a)
	}
	if strings.Contains(e.overlayFile(), `"hidden"`) {
		t.Fatalf("saved agent is still hidden: %s", e.overlayFile())
	}

	// Deleting the override brings the built-in back and leaves an empty list.
	if c := e.del("cat"); c != http.StatusNoContent {
		t.Fatalf("remove override: %d", c)
	}
	if a := e.catalogAgent("cat"); a == nil || a["name"] != "cat" {
		t.Fatalf("built-in not restored: %v", a)
	}
	if raw := e.overlayFile(); !strings.Contains(raw, `"agents": []`) || strings.Contains(raw, "null") || strings.Contains(raw, `"hidden"`) {
		t.Fatalf("overlay after removing the override: %s", raw)
	}

	// With no override left the next delete hides it, and then it is unknown.
	if c := e.del("cat"); c != http.StatusNoContent || e.catalogAgent("cat") != nil {
		t.Fatalf("hide again: %d", c)
	}
	for _, unknown := range []string{"cat", "no-such-agent"} {
		resp, out := e.do("DELETE", "/api/catalog/"+unknown, adminToken, nil)
		if resp.StatusCode != http.StatusNotFound || out["error"].(map[string]any)["code"] != "not_found" {
			t.Fatalf("delete %s: %d %v", unknown, resp.StatusCode, out)
		}
	}
}

// Hiding is reversible: GET /api/catalog lists what is hidden, and unhiding
// an ID brings its agent back as it was, launchable and in its old place,
// also after a restart.
func TestCatalogUnhideRestoresAHiddenAgent(t *testing.T) {
	e := newTestEnv(t, nil)
	if hidden := e.catalogHidden(); len(hidden) != 0 {
		t.Fatalf("hidden before any edit: %v", hidden)
	}
	if c := e.del("cat"); c != http.StatusNoContent {
		t.Fatalf("hide: %d", c)
	}
	if hidden := e.catalogHidden(); !slices.Equal(hidden, []string{"cat"}) {
		t.Fatalf("hidden after hiding: %v", hidden)
	}
	if resp, _ := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": "cat"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("hidden agent launched: %d", resp.StatusCode)
	}

	resp, out := e.do("POST", "/api/catalog/cat/unhide", adminToken, nil)
	agent, _ := out["agent"].(map[string]any)
	if resp.StatusCode != http.StatusOK || agent["id"] != "cat" || agent["name"] != "cat" || !reflect.DeepEqual(agent["command"], []any{"/bin/cat"}) {
		t.Fatalf("unhide: %d %v", resp.StatusCode, out)
	}
	if hidden := e.catalogHidden(); len(hidden) != 0 {
		t.Fatalf("hidden after unhiding: %v", hidden)
	}
	if ids := e.catalogIDs(); !slices.Equal(ids, []string{"cat", "sh", "exit"}) {
		t.Fatalf("catalog after unhiding: %v", ids)
	}
	e.createSession("cat")
	if raw := e.overlayFile(); strings.Contains(raw, `"hidden"`) {
		t.Fatalf("catalog.json still hides it: %s", raw)
	}
	if hidden := e.restart().catalogHidden(); len(hidden) != 0 {
		t.Fatalf("hidden after a restart: %v", hidden)
	}

	// Only a hidden ID can be unhidden; anything else is 404 and changes nothing.
	before := e.overlayFile()
	for _, id := range []string{"cat", "no-such-agent"} {
		resp, out := e.do("POST", "/api/catalog/"+id+"/unhide", adminToken, nil)
		if resp.StatusCode != http.StatusNotFound || out["error"].(map[string]any)["code"] != "not_found" {
			t.Fatalf("unhide %s: %d %v", id, resp.StatusCode, out)
		}
	}
	if got := e.overlayFile(); got != before {
		t.Fatalf("a refused unhide changed catalog.json:\n%s\nwas:\n%s", got, before)
	}
}

// A hand-edited catalog.json may hide an ID that no agent has, or repeat one.
// The list shows each ID once, and unhiding such an ID clears it with no agent
// in the reply.
func TestCatalogUnhideClearsAnIDWithNoAgent(t *testing.T) {
	e := newTestEnv(t, nil)
	file := `{"agents": [], "hidden": ["ghost", "sh", "ghost"]}`
	if err := os.WriteFile(filepath.Join(e.srv.store.Dir(), "catalog.json"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	e = e.restart()
	if hidden := e.catalogHidden(); !slices.Equal(hidden, []string{"ghost", "sh"}) {
		t.Fatalf("hidden: %v", hidden)
	}
	resp, out := e.do("POST", "/api/catalog/ghost/unhide", adminToken, nil)
	if _, has := out["agent"]; resp.StatusCode != http.StatusOK || has {
		t.Fatalf("unhide ghost: %d %v", resp.StatusCode, out)
	}
	if hidden := e.catalogHidden(); !slices.Equal(hidden, []string{"sh"}) {
		t.Fatalf("hidden after clearing ghost: %v", hidden)
	}
	if ids := e.catalogIDs(); !slices.Equal(ids, []string{"cat", "exit"}) {
		t.Fatalf("catalog: %v", ids)
	}
}

func TestCatalogEditingNeedsAStore(t *testing.T) {
	e := newTestEnv(t, nil)
	srv, err := New(e.srv.cfg, e.srv.base, e.srv.log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ro := e.serve(srv)
	for _, r := range []struct {
		method, path string
		body         any
	}{{"POST", "/api/catalog", agentBody("x")}, {"DELETE", "/api/catalog/cat", nil}, {"POST", "/api/catalog/cat/unhide", nil}} {
		resp, out := ro.do(r.method, r.path, adminToken, r.body)
		if resp.StatusCode != http.StatusServiceUnavailable || out["error"].(map[string]any)["code"] != "store_unavailable" {
			t.Errorf("%s %s: %d %v", r.method, r.path, resp.StatusCode, out)
		}
	}
	if ids := ro.catalogIDs(); !slices.Equal(ids, []string{"cat", "sh", "exit"}) {
		t.Fatalf("read-only catalog: %v", ids)
	}
	if hidden := ro.catalogHidden(); len(hidden) != 0 {
		t.Fatalf("read-only catalog hides %v", hidden)
	}
	if resp, out := ro.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": []string{"/bin/sh"}}); resp.StatusCode != http.StatusOK || out["found"] != true {
		t.Fatalf("check needs no store: %d %v", resp.StatusCode, out)
	}
}

// A catalog.json that cannot be used must stop startup: if the server ran with
// the configured catalog alone, the next save would overwrite the file.
func TestNewRefusesAnUnusableOverlay(t *testing.T) {
	cases := []struct{ name, file, want string }{
		{"not JSON", `{oops`, "parse catalog.json"},
		{"empty file", ``, "empty document"},
		{"unknown field", `{"agents": [], "bogus": true}`, `"bogus"`},
		{"invalid agent", `{"agents": [{"id": "Bad Id", "name": "x", "command": ["x"]}]}`, "agents[0]"},
		{"invalid signal", `{"agents": [{"id": "ok", "name": "x", "command": ["x"], "signal": {"kind": "smoke"}}]}`, "unknown kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t, nil)
			path := filepath.Join(e.srv.store.Dir(), "catalog.json")
			if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			srv, err := New(e.srv.cfg, e.srv.base, e.srv.log, nil, e.srv.store)
			if err == nil || srv != nil {
				t.Fatalf("New accepted the file: %v", err)
			}
			if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should name %s and contain %q", err, path, tc.want)
			}
			if b, _ := os.ReadFile(path); string(b) != tc.file {
				t.Fatalf("New changed the file: %q", b)
			}
		})
	}
}

func TestCatalogFailedSaveChangesNothing(t *testing.T) {
	e := newTestEnv(t, nil)
	e.save(agentBody("kept"))
	if c := e.del("sh"); c != http.StatusNoContent { // hidden, to try unhiding it below
		t.Fatalf("hide: %d", c)
	}
	before := e.catalogIDs()

	// The data directory disappears, so no save can succeed.
	dir := e.srv.store.Dir()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	attempts := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/catalog", agentBody("lost")},
		{"DELETE", "/api/catalog/cat", nil},
		{"DELETE", "/api/catalog/kept", nil},
		{"POST", "/api/catalog/sh/unhide", nil},
	}
	for _, a := range attempts {
		resp, out := e.do(a.method, a.path, adminToken, a.body)
		apiErr, _ := out["error"].(map[string]any)
		if resp.StatusCode != http.StatusInternalServerError || apiErr["code"] != "store_failed" || strings.Contains(fmt.Sprint(apiErr["message"]), dir) {
			t.Errorf("%s %s: %d %v", a.method, a.path, resp.StatusCode, out)
		}
	}
	if got := e.catalogIDs(); !slices.Equal(got, before) {
		t.Fatalf("a failed save changed the catalog: %v, want %v", got, before)
	}
	if hidden := e.catalogHidden(); !slices.Equal(hidden, []string{"sh"}) {
		t.Fatalf("a failed unhide changed the hidden list: %v", hidden)
	}

	// Once the directory is back, the next save writes what the server still
	// holds plus the new agent, and nothing from the failed attempts.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	e.save(agentBody("back"))
	var ov catalog.Overlay
	if ok, err := e.srv.store.Load("catalog.json", &ov); !ok || err != nil || len(ov.Agents) != 2 || ov.Agents[0].ID != "kept" || ov.Agents[1].ID != "back" || !slices.Equal(ov.Hidden, []string{"sh"}) {
		t.Fatalf("overlay: %v %v %+v", ok, err, ov)
	}
}

func TestCatalogConcurrentEditsAreAllKept(t *testing.T) {
	e := newTestEnv(t, nil)
	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Go(func() {
			b, _ := json.Marshal(agentBody(fmt.Sprintf("agent-%d", i)))
			req, _ := http.NewRequest("POST", e.http.URL+"/api/catalog", bytes.NewReader(b))
			req.Header.Set("Authorization", "Bearer "+adminToken)
			resp, err := e.client.Do(req)
			if err != nil {
				errs <- err
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("agent-%d: status %d", i, resp.StatusCode)
			}
		})
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	// Meanwhile readers see whole catalogs: no duplicates and no torn lists,
	// and launches keep working.
	launched := false
	for reading := true; reading; {
		select {
		case <-done:
			reading = false
		default:
		}
		ids := e.catalogIDs()
		if unique := slices.Compact(slices.Sorted(slices.Values(ids))); len(unique) != len(ids) || len(ids) < 3 {
			t.Fatalf("inconsistent catalog: %v", ids)
		}
		if !launched {
			e.createSession("cat")
			launched = true
		}
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var ov catalog.Overlay
	if ok, err := e.srv.store.Load("catalog.json", &ov); !ok || err != nil || len(ov.Agents) != writers {
		t.Fatalf("overlay keeps %d of %d agents: %v %v", len(ov.Agents), writers, ok, err)
	}
	if ids := e.catalogIDs(); len(ids) != 3+writers {
		t.Fatalf("catalog lists %d agents, want %d: %v", len(ids), 3+writers, ids)
	}
}

func TestCatalogSavedAgentsRun(t *testing.T) {
	e := newTestEnv(t, nil)
	body := agentBody("mycat")
	body["env"] = map[string]string{"TOKEN": "s3cret"}
	out := e.save(body)
	// The response and the listing mask env values; the overlay keeps them so
	// the agent still gets its real environment.
	if env := out["agent"].(map[string]any)["env"].(map[string]any); env["TOKEN"] != "***" {
		t.Fatalf("save response env: %v", env)
	}
	if env := e.catalogAgent("mycat")["env"].(map[string]any); env["TOKEN"] != "***" {
		t.Fatalf("catalog env: %v", env)
	}
	var ov catalog.Overlay
	if ok, err := e.srv.store.Load("catalog.json", &ov); !ok || err != nil || ov.Agents[0].Env["TOKEN"] != "s3cret" {
		t.Fatalf("overlay: %v %v %+v", ok, err, ov)
	}
	e.createSession("mycat")

	// A command that is missing on the server can still be saved (a host may
	// have it); launching it here fails with the usual start_failed.
	e.save(map[string]any{"id": "ghost", "name": "Ghost", "command": []string{"definitely-not-a-real-binary-xyz"}})
	if _, out := e.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": []string{"definitely-not-a-real-binary-xyz"}}); out["found"] != false {
		t.Fatalf("check: %v", out)
	}
	resp, out := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": "ghost"})
	if resp.StatusCode != http.StatusBadGateway || out["error"].(map[string]any)["code"] != "start_failed" {
		t.Fatalf("launch of a missing command: %d %v", resp.StatusCode, out)
	}
}

// A saved agent's envPassthrough lets its sessions inherit those server
// variables on top of the server-wide list. Other agents do not get them, and
// CONDUCTOR_* variables stay out even when listed. Every session gets
// CONDUCTOR_BIN, the binary the hooks run, from Conductor itself: neither the
// server's environment nor the agent's env can set it.
func TestCatalogEnvPassthroughReachesTheSession(t *testing.T) {
	t.Setenv("PASS_PROBE_AGENT", "from-agent-list")
	t.Setenv("PASS_PROBE_SERVER", "from-server-list")
	t.Setenv("PASS_PROBE_NONE", "never-listed")
	t.Setenv("CONDUCTOR_PROBE_SECRET", "server-only")
	t.Setenv("CONDUCTOR_BIN", "/from/the/server/env")
	e := newTestEnv(t, func(c *config.Config) { c.EnvPassthrough = []string{"PASS_PROBE_SERVER", "CONDUCTOR_BIN"} })
	bin, err := agents.Binary()
	if err != nil || !filepath.IsAbs(bin) {
		t.Fatalf("binary %q %v", bin, err)
	}
	script := `echo "A=[$PASS_PROBE_AGENT] S=[$PASS_PROBE_SERVER] N=[$PASS_PROBE_NONE] C=[$CONDUCTOR_PROBE_SECRET] B=[$CONDUCTOR_BIN] END"; exec /bin/cat`
	with := agentBody("with")
	with["command"] = []string{"/bin/sh", "-c", script}
	with["envPassthrough"] = []string{"PASS_PROBE_AGENT", "CONDUCTOR_PROBE_SECRET", "CONDUCTOR_BIN"}
	with["env"] = map[string]string{"CONDUCTOR_BIN": "/from/the/agent"}
	e.save(with)
	without := agentBody("without")
	without["command"] = []string{"/bin/sh", "-c", script}
	e.save(without)

	// "without" runs after "with", so a launch that grew the server-wide list
	// would show here.
	for _, tc := range []struct{ agent, want string }{
		{"with", "A=[from-agent-list] S=[from-server-list] N=[] C=[] B=[" + bin + "] END"},
		{"without", "A=[] S=[from-server-list] N=[] C=[] B=[" + bin + "] END"},
	} {
		id := e.createSession(tc.agent)
		c := dialViewer(t, e, id, adminToken)
		c.hello(80, 24)
		c.expectOutput(tc.want)
	}
}

// GET /api/catalog masks env values, so a client that edits an agent it read
// there sends the mask back to mean "unchanged". Saving that must keep the
// stored value and never write the mask into catalog.json.
func TestCatalogSaveKeepsMaskedEnvValues(t *testing.T) {
	e := newTestEnv(t, nil)
	body := agentBody("keyed")
	body["env"] = map[string]string{"API_KEY": "secret", "REGION": "eu"}
	e.save(body)

	// The listing masks every value.
	listed := e.catalogAgent("keyed")
	if env := listed["env"].(map[string]any); len(env) != 2 || env["API_KEY"] != "***" || env["REGION"] != "***" {
		t.Fatalf("listed env: %v", env)
	}
	// wantEnv checks the stored overlay, the running catalog and the file.
	wantEnv := func(want map[string]string) {
		t.Helper()
		var ov catalog.Overlay
		if ok, err := e.srv.store.Load("catalog.json", &ov); !ok || err != nil || len(ov.Agents) != 1 || !reflect.DeepEqual(ov.Agents[0].Env, want) {
			t.Fatalf("stored env, want %v: %v %v %+v", want, ok, err, ov)
		}
		if a, _ := e.srv.Catalog().Get("keyed"); !reflect.DeepEqual(a.Env, want) {
			t.Fatalf("running env, want %v: %v", want, a.Env)
		}
		if strings.Contains(e.overlayFile(), "***") {
			t.Fatalf("the mask reached catalog.json: %s", e.overlayFile())
		}
	}

	// Edit the description and send the agent back exactly as it was read.
	listed["description"] = "edited"
	out := e.save(listed)
	if env := out["agent"].(map[string]any)["env"].(map[string]any); env["API_KEY"] != "***" || env["REGION"] != "***" {
		t.Fatalf("save response env: %v", env)
	}
	wantEnv(map[string]string{"API_KEY": "secret", "REGION": "eu"})
	if a, _ := e.srv.Catalog().Get("keyed"); a.Description != "edited" {
		t.Fatalf("the edit was not saved: %+v", a)
	}

	// A real value replaces the old one, a masked one keeps its own, and a key
	// that is left out is dropped.
	listed["env"] = map[string]any{"API_KEY": "rotated", "REGION": "***"}
	e.save(listed)
	wantEnv(map[string]string{"API_KEY": "rotated", "REGION": "eu"})
	listed["env"] = map[string]any{"API_KEY": "***"}
	e.save(listed)
	wantEnv(map[string]string{"API_KEY": "rotated"})

	// A masked value has nothing to keep for a key the agent never had, or for
	// an agent that is not stored yet: nothing is saved, and with several such
	// keys the first by name is reported. The requests are repeated because a
	// wrong answer here would depend on map order.
	withEnv := func(id string, env map[string]any) map[string]any {
		b := agentBody(id)
		b["env"] = env
		return b
	}
	cases := []struct {
		name string
		body map[string]any
		key  string
	}{
		{"new key on a stored agent", withEnv("keyed", map[string]any{"API_KEY": "***", "NEW_KEY": "***"}), "NEW_KEY"},
		{"agent that is not stored yet", withEnv("fresh", map[string]any{"API_KEY": "***"}), "API_KEY"},
		{"several keys", withEnv("keyed", map[string]any{"ZED": "***", "MID": "***", "ALPHA": "***", "OMEGA": "***", "BETA": "***", "GAMMA": "***", "DELTA": "***", "SIGMA": "***"}), "ALPHA"},
	}
	before := e.overlayFile()
	for range 20 {
		for _, tc := range cases {
			resp, out := e.do("POST", "/api/catalog", adminToken, tc.body)
			apiErr, _ := out["error"].(map[string]any)
			if resp.StatusCode != http.StatusBadRequest || apiErr["code"] != "invalid_agent" || apiErr["message"] != "env "+tc.key+": value is redacted; set a real value" {
				t.Fatalf("%s: %d %v", tc.name, resp.StatusCode, out)
			}
		}
	}
	if got := e.overlayFile(); got != before {
		t.Fatalf("a rejected save changed catalog.json:\n%s\nwas:\n%s", got, before)
	}
	wantEnv(map[string]string{"API_KEY": "rotated"})
	if e.catalogAgent("fresh") != nil {
		t.Fatal("a rejected agent was added")
	}
}

// A hand-edited catalog.json may repeat an ID; the last entry wins at startup,
// so a save has to replace every copy or a restart would bring an old one back.
func TestCatalogSaveCollapsesDuplicateOverlayEntries(t *testing.T) {
	e := newTestEnv(t, nil)
	dup := `{"agents": [
  {"id": "dup", "name": "first", "command": ["a"]},
  {"id": "dup", "name": "second", "command": ["b"]}
]}`
	if err := os.WriteFile(filepath.Join(e.srv.store.Dir(), "catalog.json"), []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	e = e.restart()
	if a := e.catalogAgent("dup"); a == nil || a["name"] != "second" {
		t.Fatalf("startup: %v", a)
	}
	e.save(map[string]any{"id": "dup", "name": "third", "command": []string{"c"}})
	if a := e.catalogAgent("dup"); a["name"] != "third" {
		t.Fatalf("after save: %v", a)
	}
	if a := e.restart().catalogAgent("dup"); a["name"] != "third" {
		t.Fatalf("after restart: %v", a)
	}
	var ov catalog.Overlay
	if ok, err := e.srv.store.Load("catalog.json", &ov); !ok || err != nil || len(ov.Agents) != 1 {
		t.Fatalf("overlay: %v %v %+v", ok, err, ov)
	}
}

func TestEventsRouteRecordsAndStreams(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	events := e.sse(t) // the admin stream, subscribed before anything is posted
	resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "artifact", "message": "PR opened", "url": "https://github.com/x/y/pull/1"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	if out["accepted"] != true {
		t.Fatalf("reply %v", out)
	}
	e.waitEvent(t, events, func(ev string) bool {
		return strings.HasPrefix(ev, "activity ") && strings.Contains(ev, `"type":"artifact"`) && strings.Contains(ev, id)
	})
	if resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "bogus"}); resp.StatusCode != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "invalid_type" {
		t.Fatalf("bogus: %d %v", resp.StatusCode, out)
	}
	// attention types still update attention
	e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "needs_input", "message": "?"})
	_, got := e.do("GET", "/api/sessions/"+id, adminToken, nil)
	if got["session"].(map[string]any)["attention"].(map[string]any)["state"] != "needs_input" {
		t.Fatal("needs_input via /events did not apply")
	}
}

// A hook that floods the route costs the session its excess (the bucket
// answers 429) and nobody else anything: the admin stream stays connected
// and keeps delivering.
func TestEventsFloodKeepsSSEClient(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	events := e.sse(t)
	limited := 0
	for i := 0; i < 1000; i++ {
		resp, _ := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "progress", "message": "n"})
		if resp.StatusCode == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("1000 events in a row were never rate limited")
	}
	// The client must still receive a later event. The flood emptied the
	// session's bucket, which earns a token every 50 ms, so the next event is
	// admitted after a moment.
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "error", "message": "last"})
		if resp.StatusCode == http.StatusAccepted {
			break
		}
		if resp.StatusCode != http.StatusTooManyRequests || time.Now().After(deadline) {
			t.Fatalf("later event: %d %v", resp.StatusCode, out)
		}
		time.Sleep(25 * time.Millisecond)
	}
	e.waitEvent(t, events, func(ev string) bool { return strings.Contains(ev, `"message":"last"`) })
}

func TestEventsRouteAnswersRateLimitedWhenTheBucketIsEmpty(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	d, _ := e.srv.registry.Get(id)
	local := d.(*session.Local)
	var last map[string]any
	for i := 0; i < 5*session.EventBurst; i++ {
		resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "tool_use", "tool": "Bash"})
		if resp.StatusCode == http.StatusTooManyRequests {
			last = out
			break
		}
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("event %d: %d %v", i, resp.StatusCode, out)
		}
	}
	if errorCode(last) != "rate_limited" {
		t.Fatalf("never limited, or not as rate_limited: %v", last)
	}
	if local.Dropped() == 0 {
		t.Fatal("the session did not count the dropped event")
	}
}

// A server session's attention words spend the token a hosted session's do
// (TestAttentionWordsAreLimitedOnBothRoutesForAHostedSession): through either
// route, a word the bucket has no token for is refused whole with a 429, and
// leaves neither its state nor its entry behind. Every word accepted leaves
// exactly one entry.
func TestAttentionWordsAreLimitedOnBothRoutesForAServerSession(t *testing.T) {
	routes := []struct {
		name, suffix, field string
		ok                  int
	}{
		{"attention route", "/attention", "state", http.StatusOK},
		{"events route", "/events", "type", http.StatusAccepted},
	}
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			e := newTestEnv(t, nil)
			id := e.createSession("cat")
			d, _ := e.srv.registry.Get(id)
			local := d.(*session.Local)
			agent := e.agentToken(id)
			path := "/api/sessions/" + id + r.suffix
			states := []string{"working", "done"}

			accepted := 0
			var refused map[string]any
			for i := 0; i < 5*session.EventBurst; i++ {
				resp, out := e.do("POST", path, agent, map[string]any{r.field: states[i%2], "message": fmt.Sprint("n", i)})
				if resp.StatusCode == http.StatusTooManyRequests {
					refused = out
					break
				}
				if resp.StatusCode != r.ok {
					t.Fatalf("word %d: %d %v", i, resp.StatusCode, out)
				}
				accepted++
			}
			if errorCode(refused) != "rate_limited" {
				t.Fatalf("%d attention words in a row were never refused as rate_limited: %v", accepted, refused)
			}
			if accepted < session.EventBurst || accepted > session.EventBurst+session.EventRatePerSecond {
				t.Fatalf("accepted %d attention words before the first 429, want about %d", accepted, session.EventBurst)
			}
			if att := d.Info().Attention; att.Message != fmt.Sprint("n", accepted-1) {
				t.Fatalf("attention %+v: the refused word must not be applied", att)
			}
			n := 0
			for _, en := range local.Activity() {
				if en.Type != session.ActivityAttention {
					continue
				}
				if en.Message == fmt.Sprint("n", accepted) {
					t.Fatalf("the refused word left its entry %+v", en)
				}
				n++
			}
			if n != accepted {
				t.Fatalf("%d attention entries for %d accepted words, want one each", n, accepted)
			}
		})
	}
}

// Claude Code with tool events on spends the burst on a fast subagent, then
// asks for permission. The report is refused whole, with a 429 on either
// route, until the bucket has a token for it; then its state and its entry
// arrive together, in the session's log and on the admin stream, which feeds
// the Events page and the webhooks. A badge never shows a prompt they missed.
func TestAToolBurstCannotHideThePromptAfterIt(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	d, _ := e.srv.registry.Get(id)
	local := d.(*session.Local)
	agent := e.agentToken(id)
	events := e.sse(t)
	base := "/api/sessions/" + id

	burst := func() {
		t.Helper()
		for i := 0; ; i++ {
			resp, out := e.do("POST", base+"/events", agent, map[string]any{"type": "tool_use", "tool": "Bash"})
			if resp.StatusCode == http.StatusTooManyRequests {
				return
			}
			if resp.StatusCode != http.StatusAccepted || i > 5*session.EventBurst {
				t.Fatalf("tool event %d: %d %v", i, resp.StatusCode, out)
			}
		}
	}
	attentionEntries := func() []session.ActivityEntry {
		var entries []session.ActivityEntry
		for _, en := range local.Activity() {
			if en.Type == session.ActivityAttention {
				entries = append(entries, en)
			}
		}
		return entries
	}
	// The bucket refills continuously, so the report that follows a burst may
	// find the token the round trip earned. Either answer keeps the invariant
	// this test guards: a refused report changes nothing, and an accepted one
	// applies its state and records its entry together. A state without its
	// entry is the failure.
	var applied []string
	for _, r := range []struct{ suffix, field string }{{"/attention", "state"}, {"/events", "type"}} {
		burst()
		message := "via " + r.suffix
		before := len(applied)
		resp, out := e.do("POST", base+r.suffix, agent, map[string]any{r.field: "needs_input", "message": message, "kind": "permission"})
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			if errorCode(out) != "rate_limited" {
				t.Fatalf("needs_input via %s after a burst: %d %v, want rate_limited", r.suffix, resp.StatusCode, out)
			}
			if att := d.Info().Attention; att.Message == message {
				t.Fatalf("a report refused via %s was applied: %+v", r.suffix, att)
			}
		case http.StatusOK, http.StatusAccepted:
			applied = append(applied, message)
			if att := d.Info().Attention; att.State != session.AttentionNeedsInput || att.Message != message || att.Kind != session.KindPermission {
				t.Fatalf("a report accepted via %s did not apply: %+v", r.suffix, att)
			}
		default:
			t.Fatalf("needs_input via %s after a burst: %d %v", r.suffix, resp.StatusCode, out)
		}
		if entries := attentionEntries(); len(entries) != len(applied) || (len(applied) > before && entries[len(entries)-1].Message != message) {
			t.Fatalf("after the report via %s the attention entries are %+v, want one per applied report %v", r.suffix, entries, applied)
		}
	}

	// The bucket earns a token every 50 ms.
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, out := e.do("POST", base+"/attention", agent, map[string]any{"state": "needs_input", "message": "Allow Bash?", "kind": "permission"})
		if resp.StatusCode == http.StatusOK {
			break
		}
		if resp.StatusCode != http.StatusTooManyRequests || time.Now().After(deadline) {
			t.Fatalf("needs_input once the bucket refills: %d %v", resp.StatusCode, out)
		}
		time.Sleep(25 * time.Millisecond)
	}
	applied = append(applied, "Allow Bash?")
	if att := d.Info().Attention; att.State != session.AttentionNeedsInput || att.Message != "Allow Bash?" || att.Kind != session.KindPermission {
		t.Fatalf("attention %+v", att)
	}
	entries := attentionEntries()
	if len(entries) != len(applied) {
		t.Fatalf("attention entries %+v, want one per applied report %v", entries, applied)
	}
	for i, en := range entries {
		if en.Message != applied[i] {
			t.Fatalf("attention entry %d is %+v, want %q", i, en, applied[i])
		}
	}
	// The stream carries exactly the applied reports, in order: the refused
	// ones sent none.
	for _, want := range applied {
		if got := activityPayload(t, e.waitEvent(t, events, isActivity("attention", id))); got["message"] != want {
			t.Fatalf("the admin stream's next attention entry is %v, want %q", got, want)
		}
	}
}

func TestEventsRouteAuthentication(t *testing.T) {
	e := newTestEnv(t, nil)
	id, other := e.createSession("cat"), e.createSession("cat")
	agent, otherAgent := e.agentToken(id), e.agentToken(other)
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "control"})
	linkToken := lo["token"].(string)
	body := map[string]any{"type": "progress", "message": "1/7"}
	path := "/api/sessions/" + id + "/events"

	for name, token := range map[string]string{
		"no token":             "",
		"wrong token":          "nope",
		"another session's":    otherAgent,
		"a host token":         "test-host-token",
		"a share link's token": linkToken,
	} {
		resp, out := e.do("POST", path, token, body)
		if resp.StatusCode != http.StatusUnauthorized || errorCode(out) != "unauthorized" {
			t.Errorf("%s: %d %v", name, resp.StatusCode, out)
		}
	}
	// The credential is read from the Authorization header alone.
	req, _ := http.NewRequest("POST", e.http.URL+path+"?token="+agent, strings.NewReader(`{"type":"progress"}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := e.client.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token in the query: %v %v", err, resp)
	}
	for name, token := range map[string]string{"the session's agent token": agent, "the admin token": adminToken} {
		if resp, out := e.do("POST", path, token, body); resp.StatusCode != http.StatusAccepted {
			t.Errorf("%s: %d %v", name, resp.StatusCode, out)
		}
	}
	if resp, out := e.do("POST", "/api/sessions/nosuchsession0000/events", adminToken, body); resp.StatusCode != http.StatusNotFound || errorCode(out) != "not_found" {
		t.Errorf("unknown session: %d %v", resp.StatusCode, out)
	}
}

func TestEventsRouteRejectsWhatItDoesNotAccept(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	path := "/api/sessions/" + id + "/events"

	// Only what an agent reports: the entries a session records itself skip
	// the rate bucket, so an agent must not be able to write them.
	for _, typ := range []string{"", "bogus", "PROGRESS", "attention", "input", "join", "leave", "link", "status", "state"} {
		resp, out := e.do("POST", path, adminToken, map[string]any{"type": typ})
		if resp.StatusCode != http.StatusBadRequest || errorCode(out) != "invalid_type" {
			t.Errorf("type %q: %d %v", typ, resp.StatusCode, out)
		}
	}
	for name, body := range map[string]any{
		"an unknown field":         map[string]any{"type": "progress", "state": "done"},
		"a missing type":           map[string]any{"message": "no type"},
		"a state, not a type":      map[string]any{"state": "needs_input"},
		"a type of the wrong kind": map[string]any{"type": 7},
	} {
		if resp, out := e.do("POST", path, adminToken, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, resp.StatusCode, out)
		}
	}
	req, _ := http.NewRequest("POST", e.http.URL+path, strings.NewReader(`{"type":"progress"} {"type":"progress"}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	if resp, err := e.client.Do(req); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Errorf("two objects: %v %v", err, resp)
	}

	// Attention types are held to what /attention holds them to.
	tooMany := make([]map[string]any, 33)
	for i := range tooMany {
		tooMany[i] = map[string]any{"label": "x", "input": "1"}
	}
	for name, c := range map[string]struct {
		body map[string]any
		code string
	}{
		"an unknown kind":    {map[string]any{"type": "needs_input", "kind": "bogus"}, "invalid_kind"},
		"too many options":   {map[string]any{"type": "needs_input", "options": tooMany}, "invalid_request"},
		"a message too long": {map[string]any{"type": "done", "message": strings.Repeat("m", 4097)}, "invalid_request"},
	} {
		resp, out := e.do("POST", path, adminToken, c.body)
		if resp.StatusCode != http.StatusBadRequest || errorCode(out) != c.code {
			t.Errorf("%s: %d %v", name, resp.StatusCode, out)
		}
	}
}

func TestEventsRouteRefusesAnEndedSession(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("exit")
	e.waitEnded(id)
	for _, typ := range []string{"progress", "needs_input"} {
		resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": typ})
		if resp.StatusCode != http.StatusConflict || errorCode(out) != "session_ended" {
			t.Errorf("%s on an ended session: %d %v", typ, resp.StatusCode, out)
		}
	}
}

// /events and /attention are two doors to one room for the attention types:
// the same reports leave the same attention behind, whoever makes them.
func TestEventsRouteAttentionTypesMatchTheAttentionRoute(t *testing.T) {
	e := newTestEnv(t, nil)
	viaEvents, viaAttention := e.createSession("cat"), e.createSession("cat")
	tokens := map[string][2]string{ // who reports: the token on each session
		"admin":       {adminToken, adminToken},
		"agent token": {e.agentToken(viaEvents), e.agentToken(viaAttention)},
	}
	options := []map[string]any{{"label": "Yes", "input": "1"}, {"label": "No, explain", "input": "3"}}
	steps := []struct {
		state string
		body  map[string]any
	}{
		{"needs_input", map[string]any{"message": "Allow Bash?", "kind": "permission", "options": options}},
		{"working", map[string]any{"message": "on it"}},
		{"done", map[string]any{"message": "finished", "kind": "done"}},
		{"needs_input", map[string]any{"message": "again", "kind": "prompt"}},
		{"clear", map[string]any{}},
	}
	attention := func(id string) map[string]any {
		_, got := e.do("GET", "/api/sessions/"+id, adminToken, nil)
		att := got["session"].(map[string]any)["attention"].(map[string]any)
		delete(att, "since")
		return att
	}
	for who, tok := range tokens {
		for _, st := range steps {
			evBody, atBody := map[string]any{"type": st.state}, map[string]any{"state": st.state}
			for k, v := range st.body {
				evBody[k], atBody[k] = v, v
			}
			if resp, out := e.do("POST", "/api/sessions/"+viaEvents+"/events", tok[0], evBody); resp.StatusCode != http.StatusAccepted {
				t.Fatalf("%s %s via /events: %d %v", who, st.state, resp.StatusCode, out)
			}
			if resp, out := e.do("POST", "/api/sessions/"+viaAttention+"/attention", tok[1], atBody); resp.StatusCode != http.StatusOK {
				t.Fatalf("%s %s via /attention: %d %v", who, st.state, resp.StatusCode, out)
			}
			if a, b := attention(viaEvents), attention(viaAttention); !reflect.DeepEqual(a, b) {
				t.Fatalf("%s %s: /events left %v, /attention left %v", who, st.state, a, b)
			}
		}
	}
	// And the source says who reported.
	e.do("POST", "/api/sessions/"+viaEvents+"/events", tokens["agent token"][0], map[string]any{"type": "working"})
	if src := attention(viaEvents)["source"]; src != session.SourceAPI {
		t.Fatalf("agent token source %v", src)
	}
	e.do("POST", "/api/sessions/"+viaEvents+"/events", adminToken, map[string]any{"type": "done"})
	if src := attention(viaEvents)["source"]; src != session.SourceAdmin {
		t.Fatalf("admin source %v", src)
	}
}

// What the route stores and streams is bounded and clean, and says the agent
// reported it.
func TestEventsRouteRecordsCleanedEntriesReportedByTheAgent(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	events := e.sse(t)
	resp, out := e.do("POST", "/api/sessions/"+id+"/events", e.agentToken(id), map[string]any{
		"type":    "handoff",
		"message": "to\x00 review\nplease " + strings.Repeat("m", 4000),
		"url":     "https://github.com/x/y/pull/1\x07",
		"to":      strings.Repeat("t", 100),
		"tool":    strings.Repeat("k", 300),
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	got := activityPayload(t, e.waitEvent(t, events, isActivity("handoff", id)))
	msg, _ := got["message"].(string)
	if got["sessionId"] != id || got["byName"] != "agent" || got["by"] != nil {
		t.Fatalf("attribution: %v", got)
	}
	if len(msg) != session.MaxAttentionMessage || !strings.HasPrefix(msg, "to review\nplease m") {
		t.Fatalf("message %d bytes %.40q", len(msg), msg)
	}
	if got["url"] != "https://github.com/x/y/pull/1" || len([]rune(got["to"].(string))) != session.MaxEventTo || len(got["tool"].(string)) != session.MaxEventTool {
		t.Fatalf("fields %v", got)
	}
	if _, err := time.Parse(time.RFC3339Nano, got["at"].(string)); err != nil {
		t.Fatalf("at %v", got["at"])
	}
	// The session keeps the same entry in its own log, so viewers replay it.
	d, _ := e.srv.registry.Get(id)
	log := d.(*session.Local).Activity()
	last := log[len(log)-1]
	if last.Type != session.ActivityHandoff || last.ByName != "agent" || last.Message != msg || last.To != got["to"] {
		t.Fatalf("session log ends with %+v", last)
	}
}

// Every entry a server session records reaches the admin stream, not just the
// ones sent to the events route.
func TestSessionOwnEntriesReachTheEventStream(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	events := e.sse(t)
	c := dialViewer(t, e, id, adminToken)
	c.helloNamed(80, 24, "Ada")
	c.expectControl(proto.CtlReady)
	got := activityPayload(t, e.waitEvent(t, events, isActivity("join", id)))
	if got["byName"] != "Ada" || got["sessionId"] != id {
		t.Fatalf("join entry %v", got)
	}
	c.c.CloseNow()
	e.waitEvent(t, events, isActivity("leave", id))
}

// integrations returns what GET /api/integrations lists, keyed by id, and the
// ids in their order.
func (e *testEnv) integrations() (map[string]map[string]any, []string) {
	e.t.Helper()
	resp, out := e.do("GET", "/api/integrations", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("list integrations: %d %v", resp.StatusCode, out)
	}
	raw, ok := out["integrations"].([]any)
	if !ok {
		e.t.Fatalf("integrations: %v", out)
	}
	byID := map[string]map[string]any{}
	var ids []string
	for _, r := range raw {
		it := r.(map[string]any)
		id, _ := it["id"].(string)
		byID[id] = it
		ids = append(ids, id)
	}
	return byID, ids
}

// GET /api/integrations lists every adapter with what it reports, whether it
// is wired at launch, whether its install brings the Conductor skill and
// whether its hooks are installed in the server user's home, and names the
// machine the server runs on; POST /api/integrations/{id}/install puts them
// there, once. An agent whose hooks cannot be installed from a file answers
// no_file_route, with the snippet to do it by hand.
func TestIntegrationsListAndInstall(t *testing.T) {
	e := newTestEnv(t, nil)
	home := t.TempDir()
	e.srv.home = home

	resp, out := e.do("GET", "/api/integrations", adminToken, nil)
	if host, _ := os.Hostname(); resp.StatusCode != http.StatusOK || out["host"] != host {
		t.Fatalf("host %v, want %q", out["host"], host)
	}
	list, ids := e.integrations()
	var want []string
	for _, a := range agents.All() {
		want = append(want, a.ID)
	}
	if len(ids) != 12 || !slices.Equal(ids, want) {
		t.Fatalf("integrations %v, want %v", ids, want)
	}
	fields := []string{"events", "experimental", "id", "installed", "installsSkill", "launchInjection", "name", "snippet", "where"}
	skillReaders := []string{"claude", "codex", "pi", "goose"}
	for _, id := range ids {
		it := list[id]
		if keys := slices.Sorted(maps.Keys(it)); !slices.Equal(keys, fields) {
			t.Errorf("%s: fields %v, want %v", id, keys, fields)
		}
		a, _ := agents.Get(id)
		if it["name"] != a.Name || it["launchInjection"] != (a.Inject != nil) || it["experimental"] != a.Experimental || it["installed"] != false {
			t.Errorf("%s: %v", id, it)
		}
		if it["installsSkill"] != slices.Contains(skillReaders, id) {
			t.Errorf("%s: installsSkill %v", id, it["installsSkill"])
		}
		if events, _ := it["events"].([]any); len(events) != len(a.Events) || events[0] != a.Events[0] {
			t.Errorf("%s: events %v, want %v", id, it["events"], a.Events)
		}
		if s, _ := it["snippet"].(string); s == "" {
			t.Errorf("%s: no snippet", id)
		}
	}
	copilotFile := filepath.Join(home, ".copilot", "hooks", "conductor.json")
	if c := list["copilot"]; c["where"] != copilotFile || !strings.Contains(c["snippet"].(string), " notify --copilot-hook") || c["launchInjection"] != false {
		t.Fatalf("copilot: %v", c)
	}
	if c := list["claude"]; c["launchInjection"] != true || c["where"] != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("claude: %v", c)
	}
	// Nothing to install for aider (its environment is set at launch) or
	// dsh (by hand): no place to report.
	for _, id := range []string{"aider", "dsh"} {
		if list[id]["where"] != "" {
			t.Fatalf("%s: %v", id, list[id])
		}
	}
	if list["dsh"]["experimental"] != true {
		t.Fatalf("dsh: %v", list["dsh"])
	}

	resp, out = e.do("POST", "/api/integrations/copilot/install", adminToken, nil)
	if resp.StatusCode != http.StatusOK || !reflect.DeepEqual(out["changed"], []any{copilotFile}) {
		t.Fatalf("install: %d %v", resp.StatusCode, out)
	}
	if b, err := os.ReadFile(copilotFile); err != nil || !strings.Contains(string(b), " notify --copilot-hook") {
		t.Fatalf("%s: %v\n%s", copilotFile, err, b)
	}
	resp, out = e.do("POST", "/api/integrations/copilot/install", adminToken, nil)
	if changed, ok := out["changed"].([]any); resp.StatusCode != http.StatusOK || !ok || len(changed) != 0 {
		t.Fatalf("second install: %d %v", resp.StatusCode, out)
	}
	if list, _ = e.integrations(); list["copilot"]["installed"] != true || list["copilot"]["where"] != copilotFile {
		t.Fatalf("copilot after install: %v", list["copilot"])
	}

	for _, tc := range []struct{ id, message, snippet string }{
		{"dsh", "developer preview", "permission-requested"},
		{"aider", "at launch", "AIDER_NOTIFICATIONS=true"},
	} {
		resp, out = e.do("POST", "/api/integrations/"+tc.id+"/install", adminToken, nil)
		apiErr, _ := out["error"].(map[string]any)
		msg, _ := apiErr["message"].(string)
		snippet, _ := apiErr["snippet"].(string)
		if resp.StatusCode != http.StatusBadRequest || apiErr["code"] != "no_file_route" || !strings.Contains(msg, tc.message) || !strings.Contains(snippet, tc.snippet) {
			t.Fatalf("%s: %d %v", tc.id, resp.StatusCode, out)
		}
	}
	resp, out = e.do("POST", "/api/integrations/gemini/install", adminToken, nil)
	if resp.StatusCode != http.StatusNotFound || errorCode(out) != "not_found" {
		t.Fatalf("unknown integration: %d %v", resp.StatusCode, out)
	}
	// Only the server user's home was written, and only copilot's file.
	var written []string
	filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			written = append(written, p)
		}
		return err
	})
	if !slices.Equal(written, []string{copilotFile}) {
		t.Fatalf("home holds %q", written)
	}
}

// What Install does before a step it leaves to the admin is kept and
// reported with no_file_route; an install that fails answers install_failed.
func TestIntegrationsInstallByHandAndFailure(t *testing.T) {
	e := newTestEnv(t, nil)
	home := t.TempDir()
	e.srv.home = home
	hooksJSON := filepath.Join(home, ".codex", "hooks.json")
	os.MkdirAll(filepath.Dir(hooksJSON), 0o700)
	os.WriteFile(hooksJSON, []byte(`{"hooks":{}}`), 0o600)
	resp, out := e.do("POST", "/api/integrations/codex/install", adminToken, nil)
	apiErr, _ := out["error"].(map[string]any)
	msg, _ := apiErr["message"].(string)
	snippet, _ := apiErr["snippet"].(string)
	config := filepath.Join(home, ".codex", "config.toml")
	skill := filepath.Join(home, ".codex", "skills", "conductor", "SKILL.md")
	if resp.StatusCode != http.StatusBadRequest || apiErr["code"] != "no_file_route" || !strings.Contains(msg, hooksJSON) ||
		!strings.Contains(snippet, "# >>> conductor") || !reflect.DeepEqual(apiErr["changed"], []any{config, skill}) {
		t.Fatalf("codex: %d %v", resp.StatusCode, out)
	}
	if b, _ := os.ReadFile(hooksJSON); string(b) != `{"hooks":{}}` {
		t.Fatalf("hooks.json changed: %s", b)
	}

	// A home that is a file cannot hold the agent's directories.
	file := filepath.Join(t.TempDir(), "home")
	os.WriteFile(file, nil, 0o600)
	e.srv.home = file
	resp, out = e.do("POST", "/api/integrations/copilot/install", adminToken, nil)
	if resp.StatusCode != http.StatusInternalServerError || errorCode(out) != "install_failed" {
		t.Fatalf("install into a file: %d %v", resp.StatusCode, out)
	}
	if list, _ := e.integrations(); list["copilot"]["installed"] != false {
		t.Fatalf("copilot: %v", list["copilot"])
	}
}

func TestIntegrationsRoutesNeedTheAdminToken(t *testing.T) {
	e := newTestEnv(t, nil)
	home := t.TempDir()
	e.srv.home = home
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/integrations"},
		{"POST", "/api/integrations/copilot/install"},
	} {
		for _, token := range []string{"", "wrong", "test-host-token"} {
			if resp, _ := e.do(r.method, r.path, token, nil); resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s with token %q: %d", r.method, r.path, token, resp.StatusCode)
			}
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("an unauthorized call wrote %v", entries)
	}
}

// An agent's adapter must be one Conductor has: every registered adapter
// saves, and an unknown one is rejected with a message that names it.
func TestCatalogSaveChecksTheAdapter(t *testing.T) {
	e := newTestEnv(t, nil)
	for _, a := range agents.All() {
		body := agentBody("with-" + a.ID)
		body["adapter"] = a.ID
		e.save(body)
	}
	body := agentBody("ghost")
	body["adapter"] = "gemini"
	resp, out := e.do("POST", "/api/catalog", adminToken, body)
	apiErr, _ := out["error"].(map[string]any)
	msg, _ := apiErr["message"].(string)
	if resp.StatusCode != http.StatusBadRequest || apiErr["code"] != "invalid_agent" || !strings.Contains(msg, `"gemini"`) {
		t.Fatalf("unknown adapter: %d %v", resp.StatusCode, out)
	}
	if slices.Contains(e.catalogIDs(), "ghost") {
		t.Fatal("an agent with an unknown adapter was saved")
	}
}

// A home that is not an absolute path is no home: nothing is checked or
// written there, and the install says the home is unknown.
func TestIntegrationsWithoutAKnownHome(t *testing.T) {
	t.Setenv("HOME", "relative/home")
	e := newTestEnv(t, nil)
	if e.srv.home != "" {
		t.Fatalf("home %q", e.srv.home)
	}
	list, _ := e.integrations()
	if c := list["copilot"]; c["installed"] != false || c["where"] != "" {
		t.Fatalf("copilot: %v", c)
	}
	resp, out := e.do("POST", "/api/integrations/copilot/install", adminToken, nil)
	apiErr, _ := out["error"].(map[string]any)
	if msg, _ := apiErr["message"].(string); resp.StatusCode != http.StatusInternalServerError || apiErr["code"] != "install_failed" || !strings.Contains(msg, "unknown") {
		t.Fatalf("install: %d %v", resp.StatusCode, out)
	}
}

// crewBody is a crew as the web client sends it: two members of the test
// catalog, the second starting once the first is done.
func (e *testEnv) crewBody(name string) map[string]any {
	return map[string]any{
		"name": name, "goal": "ship /v1/users", "cwd": e.root, "where": "server", "isolation": "worktree",
		"openAfterLaunch": true, "viewLinkTtlSeconds": 28800,
		"members": []any{
			map[string]any{"name": "lead", "agentId": "sh", "prompt": "Own the plan for $GOAL.", "args": []any{"-i"}, "start": map[string]any{"when": "immediately"}},
			map[string]any{"name": "tests", "agentId": "cat", "prompt": "Write the tests.", "start": map[string]any{"when": "after", "member": "lead"}},
		},
	}
}

// crewMember returns member i of a crew, as sent or as received.
func crewMember(c map[string]any, i int) map[string]any {
	return c["members"].([]any)[i].(map[string]any)
}

// crews returns the crews GET /api/crews lists, in order.
func (e *testEnv) crews() []map[string]any {
	e.t.Helper()
	resp, out := e.do("GET", "/api/crews", adminToken, nil)
	raw, ok := out["crews"].([]any)
	if resp.StatusCode != http.StatusOK || !ok {
		e.t.Fatalf("crews: %d %v", resp.StatusCode, out)
	}
	list := []map[string]any{}
	for _, c := range raw {
		list = append(list, c.(map[string]any))
	}
	return list
}

// crewIDs returns the IDs GET /api/crews lists, in order.
func (e *testEnv) crewIDs() []string {
	e.t.Helper()
	ids := []string{}
	for _, c := range e.crews() {
		ids = append(ids, c["id"].(string))
	}
	return ids
}

// sendCrew sends body to a crew route as the admin, requires status, and
// returns the crew in the reply.
func (e *testEnv) sendCrew(method, path string, body any, status int) map[string]any {
	e.t.Helper()
	resp, out := e.do(method, path, adminToken, body)
	c, _ := out["crew"].(map[string]any)
	if resp.StatusCode != status || c == nil {
		e.t.Fatalf("%s %s: %d %v", method, path, resp.StatusCode, out)
	}
	return c
}

// storedCrew returns the crew with the given ID as crews.json in the data
// directory holds it, or nil.
func (e *testEnv) storedCrew(id string) map[string]any {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.srv.store.Dir(), "crews.json"))
	if err != nil {
		e.t.Fatal(err)
	}
	var doc map[string][]map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		e.t.Fatalf("crews.json: %v %s", err, b)
	}
	for _, c := range doc["crews"] {
		if c["id"] == id {
			return c
		}
	}
	return nil
}

// wantAPIError requires an error reply with status and code whose message
// contains msg.
func wantAPIError(t *testing.T, what string, resp *http.Response, out map[string]any, status int, code, msg string) {
	t.Helper()
	apiErr, _ := out["error"].(map[string]any)
	message, _ := apiErr["message"].(string)
	if resp.StatusCode != status || apiErr["code"] != code || !strings.Contains(message, msg) {
		t.Errorf("%s: %d %v, want %d %s with %q", what, resp.StatusCode, out, status, code, msg)
	}
}

func TestCrewsCRUD(t *testing.T) {
	e := newTestEnv(t, nil)

	// Create: the server derives the ID from the name, trims the name and
	// stamps the times, in UTC.
	created := e.sendCrew("POST", "/api/crews", e.crewBody("  API sweep  "), http.StatusCreated)
	if created["id"] != "api-sweep" || created["name"] != "API sweep" || created["goal"] != "ship /v1/users" || created["cwd"] != e.root ||
		created["where"] != "server" || created["isolation"] != "worktree" || created["openAfterLaunch"] != true || created["viewLinkTtlSeconds"] != 28800.0 {
		t.Fatalf("created %v", created)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, fmt.Sprint(created["createdAt"]))
	if err != nil || time.Since(createdAt) > time.Minute || created["updatedAt"] != created["createdAt"] || !strings.HasSuffix(fmt.Sprint(created["createdAt"]), "Z") {
		t.Fatalf("times %v %v: %v", created["createdAt"], created["updatedAt"], err)
	}
	wantMembers := []any{
		map[string]any{"name": "lead", "agentId": "sh", "prompt": "Own the plan for $GOAL.", "args": []any{"-i"}, "start": map[string]any{"when": "immediately"}},
		map[string]any{"name": "tests", "agentId": "cat", "prompt": "Write the tests.", "start": map[string]any{"when": "after", "member": "lead"}},
	}
	if !reflect.DeepEqual(created["members"], wantMembers) {
		t.Fatalf("members %v", created["members"])
	}
	if again := e.sendCrew("POST", "/api/crews", e.crewBody("API sweep"), http.StatusCreated); again["id"] != "api-sweep-2" {
		t.Fatalf("same name again: %v", again["id"])
	}
	if list := e.crews(); len(list) != 2 || !reflect.DeepEqual(list[0], created) || list[1]["id"] != "api-sweep-2" {
		t.Fatalf("list %v", list)
	}

	// Update the lead's prompt and rename the crew: the ID and the creation
	// time stay.
	body := e.crewBody("Users sweep")
	crewMember(body, 0)["prompt"] = "New plan for $GOAL."
	updated := e.sendCrew("PUT", "/api/crews/api-sweep", body, http.StatusOK)
	updatedAt, err := time.Parse(time.RFC3339Nano, fmt.Sprint(updated["updatedAt"]))
	if updated["id"] != "api-sweep" || updated["name"] != "Users sweep" || crewMember(updated, 0)["prompt"] != "New plan for $GOAL." ||
		updated["createdAt"] != created["createdAt"] || err != nil || updatedAt.Before(createdAt) || !strings.HasSuffix(fmt.Sprint(updated["updatedAt"]), "Z") {
		t.Fatalf("updated %v", updated)
	}
	if stored := e.storedCrew("api-sweep"); stored == nil || !reflect.DeepEqual(stored, updated) {
		t.Fatalf("crews.json holds %v", stored)
	}

	// Duplicate: a new crew under <id>-copy.
	dup := e.sendCrew("POST", "/api/crews/api-sweep/duplicate", nil, http.StatusCreated)
	if dup["id"] != "api-sweep-copy" || dup["name"] != "Users sweep copy" || !reflect.DeepEqual(dup["members"], updated["members"]) || dup["cwd"] != e.root {
		t.Fatalf("duplicate %v", dup)
	}
	if ids := e.crewIDs(); !slices.Equal(ids, []string{"api-sweep-2", "api-sweep", "api-sweep-copy"}) { // by name
		t.Fatalf("ids %v", ids)
	}

	// Delete.
	if resp, _ := e.do("DELETE", "/api/crews/api-sweep-copy", adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if e.storedCrew("api-sweep-copy") != nil {
		t.Fatal("deleted crew still in crews.json")
	}
	resp, out := e.do("DELETE", "/api/crews/api-sweep-copy", adminToken, nil)
	wantAPIError(t, "delete again", resp, out, http.StatusNotFound, "not_found", "")
	resp, out = e.do("PUT", "/api/crews/nope", adminToken, e.crewBody("Nope"))
	wantAPIError(t, "update an unknown crew", resp, out, http.StatusNotFound, "not_found", "")
	resp, out = e.do("POST", "/api/crews/nope/duplicate", adminToken, nil)
	wantAPIError(t, "duplicate an unknown crew", resp, out, http.StatusNotFound, "not_found", "")

	// Invalid crews are refused with the reason.
	bad := e.crewBody("Bad")
	crewMember(bad, 0)["name"] = "Lead!"
	resp, out = e.do("POST", "/api/crews", adminToken, bad)
	wantAPIError(t, "member name Lead!", resp, out, http.StatusBadRequest, "invalid_crew", `"Lead!"`)
	unknown := e.crewBody("Unknown")
	crewMember(unknown, 1)["agentId"] = "nope"
	resp, out = e.do("PUT", "/api/crews/api-sweep", adminToken, unknown)
	wantAPIError(t, "unknown agent", resp, out, http.StatusBadRequest, "invalid_crew", `"nope"`)

	// Nobody but the admin reaches a crew route.
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/crews", nil},
		{"POST", "/api/crews", e.crewBody("Intruder")},
		{"PUT", "/api/crews/api-sweep", e.crewBody("Intruder")},
		{"DELETE", "/api/crews/api-sweep", nil},
		{"POST", "/api/crews/api-sweep/duplicate", nil},
	} {
		for _, token := range []string{"", "wrong", "test-host-token"} {
			if resp, _ := e.do(r.method, r.path, token, r.body); resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s with token %q: %d", r.method, r.path, token, resp.StatusCode)
			}
		}
	}

	// A restarted server lists the same crews, unchanged by what was refused.
	live := e.crews()
	if ids := e.crewIDs(); !slices.Equal(ids, []string{"api-sweep-2", "api-sweep"}) || !reflect.DeepEqual(live[1], updated) {
		t.Fatalf("after the refusals: %v", live)
	}
	if restarted := e.restart().crews(); !reflect.DeepEqual(live, restarted) {
		t.Fatalf("restart differs:\n live      %v\n restarted %v", live, restarted)
	}
}

func TestCrewSaveRejectsInvalidCrews(t *testing.T) {
	e := newTestEnv(t, nil)
	kept := e.sendCrew("POST", "/api/crews", e.crewBody("Kept"), http.StatusCreated)
	with := func(change func(b map[string]any)) map[string]any {
		b := e.crewBody("Crew")
		change(b)
		return b
	}
	cases := []struct {
		name string
		body any
		code string
		msg  string // part of the message
	}{
		{"member name Lead!", with(func(b map[string]any) { crewMember(b, 0)["name"] = "Lead!" }), "invalid_crew", "must match"},
		{"unknown agent", with(func(b map[string]any) { crewMember(b, 1)["agentId"] = "nope" }), "invalid_crew", `"nope"`},
		{"after a missing member", with(func(b map[string]any) {
			crewMember(b, 1)["start"] = map[string]any{"when": "after", "member": "ghost"}
		}), "invalid_crew", `"ghost"`},
		{"13 members", with(func(b map[string]any) {
			for i := 2; i < 13; i++ {
				b["members"] = append(b["members"].([]any), map[string]any{"name": fmt.Sprintf("m%d", i), "agentId": "cat", "prompt": "", "start": map[string]any{"when": "manual"}})
			}
		}), "invalid_crew", "at most 12"},
		{"where elsewhere", with(func(b map[string]any) { b["where"] = "cloud" }), "invalid_crew", `"cloud"`},
		{"args over 8 KiB in all", with(func(b map[string]any) {
			crewMember(b, 0)["args"] = []any{strings.Repeat("a", 4096), strings.Repeat("b", 4096), "c"}
		}), "invalid_crew", "8192 bytes"},
		{"members starting after each other", with(func(b map[string]any) {
			crewMember(b, 0)["start"] = map[string]any{"when": "after", "member": "tests"}
		}), "invalid_crew", "cycle"},
		{"member name git refuses", with(func(b map[string]any) {
			crewMember(b, 1)["name"] = "tests.lock"
		}), "invalid_crew", "git"},
		{"name with a control character", with(func(b map[string]any) { b["name"] = "API\x1b[2Jsweep" }), "invalid_crew", "control"},
		{"id sent by the client", with(func(b map[string]any) { b["id"] = "mine" }), "invalid_request", `"id"`},
		{"createdAt sent by the client", with(func(b map[string]any) { b["createdAt"] = "2026-09-29T10:00:00Z" }), "invalid_request", `"createdAt"`},
		{"unknown member field", with(func(b map[string]any) { crewMember(b, 0)["model"] = "x" }), "invalid_request", `"model"`},
		{"no body", nil, "invalid_request", ""},
		{"body over 1 MiB", with(func(b map[string]any) { b["goal"] = strings.Repeat("g", 1<<20) }), "invalid_request", "too large"},
	}
	for _, tc := range cases {
		for _, path := range []string{"POST /api/crews", "PUT /api/crews/kept"} {
			method, route, _ := strings.Cut(path, " ")
			resp, out := e.do(method, route, adminToken, tc.body)
			wantAPIError(t, tc.name+" ("+path+")", resp, out, http.StatusBadRequest, tc.code, tc.msg)
		}
	}
	if list := e.crews(); len(list) != 1 || !reflect.DeepEqual(list[0], kept) {
		t.Fatalf("a refused save changed the crews: %v", list)
	}
}

// A value quoted in an error comes back cut short, however long it was sent.
// A crew that breaks a rule is refused for that before its agents are looked
// up, before the 50-crew limit and before its id is; an agent the catalog
// does not have is 400 whatever else holds.
func TestCrewErrorsQuoteShortAndComeInOrder(t *testing.T) {
	e := newTestEnv(t, nil)
	e.sendCrew("POST", "/api/crews", e.crewBody("Kept"), http.StatusCreated)
	message := func(out map[string]any) string {
		apiErr, _ := out["error"].(map[string]any)
		msg, _ := apiErr["message"].(string)
		return msg
	}
	huge := e.crewBody("Huge")
	crewMember(huge, 1)["agentId"] = strings.Repeat("a", 900<<10)
	invalid := e.crewBody("Invalid")
	crewMember(invalid, 0)["name"] = "Lead!"
	unknown := e.crewBody("Unknown")
	crewMember(unknown, 1)["agentId"] = "nope"
	both := e.crewBody("Both")
	crewMember(both, 0)["name"] = "Lead!"
	crewMember(both, 1)["agentId"] = "nope"
	for _, path := range []string{"POST /api/crews", "PUT /api/crews/kept", "PUT /api/crews/missing"} {
		method, route, _ := strings.Cut(path, " ")
		resp, out := e.do(method, route, adminToken, huge)
		wantAPIError(t, "a 900 KiB agentId ("+path+")", resp, out, http.StatusBadRequest, "invalid_crew", "agentId")
		if msg := message(out); len(msg) > 300 {
			t.Errorf("%s: a message of %d bytes", path, len(msg))
		}
		resp, out = e.do(method, route, adminToken, both)
		wantAPIError(t, "a rule broken and an unknown agent ("+path+")", resp, out, http.StatusBadRequest, "invalid_crew", "must match")
		resp, out = e.do(method, route, adminToken, invalid)
		wantAPIError(t, "a rule broken ("+path+")", resp, out, http.StatusBadRequest, "invalid_crew", "must match")
		resp, out = e.do(method, route, adminToken, unknown)
		wantAPIError(t, "an unknown agent ("+path+")", resp, out, http.StatusBadRequest, "invalid_crew", `"nope"`)
	}
	for i := len(e.crews()); i < 50; i++ {
		e.sendCrew("POST", "/api/crews", e.crewBody(fmt.Sprintf("Crew %02d", i)), http.StatusCreated)
	}
	resp, out := e.do("POST", "/api/crews", adminToken, invalid)
	wantAPIError(t, "a rule broken at 50 crews", resp, out, http.StatusBadRequest, "invalid_crew", "must match")
	resp, out = e.do("POST", "/api/crews", adminToken, unknown)
	wantAPIError(t, "an unknown agent at 50 crews", resp, out, http.StatusBadRequest, "invalid_crew", `"nope"`)
	resp, out = e.do("POST", "/api/crews", adminToken, e.crewBody("Valid"))
	wantAPIError(t, "a valid crew at 50 crews", resp, out, http.StatusConflict, "too_many_crews", "50")
}

// The largest crew the limits allow goes through the crew routes, whose
// bodies may reach 1 MiB where others stop at 64 KiB: twelve members, each
// with a prompt of 4000 four-byte and control characters (JSON writes a
// control character as six bytes) and 8 KiB of args, and a goal of 2000
// four-byte characters.
func TestCrewRoutesTakeTheLargestCrews(t *testing.T) {
	e := newTestEnv(t, nil)
	body := e.crewBody("Largest")
	prompt := strings.Repeat("\U0001F600\x01", 2000)
	var members []any
	for i := range 12 {
		members = append(members, map[string]any{
			"name": fmt.Sprintf("m%02d", i), "agentId": "sh", "prompt": prompt,
			"args":  []any{strings.Repeat("a", 4096), strings.Repeat("b", 4096)},
			"start": map[string]any{"when": "manual"},
		})
	}
	body["members"] = members
	body["goal"] = strings.Repeat("\U0001F600", 2000)
	if b, _ := json.Marshal(body); len(b) < 256<<10 {
		t.Fatalf("the body is only %d bytes", len(b))
	}
	created := e.sendCrew("POST", "/api/crews", body, http.StatusCreated)
	if created["goal"] != body["goal"] || crewMember(created, 11)["prompt"] != prompt {
		t.Fatalf("the crew came back changed: goal %d bytes", len(fmt.Sprint(created["goal"])))
	}
	e.sendCrew("PUT", "/api/crews/largest", body, http.StatusOK)
	// One byte more of args is one byte too many.
	crewMember(body, 0)["args"] = []any{strings.Repeat("a", 4096), strings.Repeat("b", 4096), "c"}
	resp, out := e.do("POST", "/api/crews", adminToken, body)
	wantAPIError(t, "8 KiB and a byte of args", resp, out, http.StatusBadRequest, "invalid_crew", "8192 bytes")
	if ids := e.crewIDs(); !slices.Equal(ids, []string{"largest"}) {
		t.Fatalf("ids %v", ids)
	}
}

// The agents a crew names must be in the catalog when it is saved or copied.
func TestCrewRoutesCheckTheAgents(t *testing.T) {
	e := newTestEnv(t, nil)
	e.sendCrew("POST", "/api/crews", e.crewBody("API sweep"), http.StatusCreated)
	// The tests member runs cat, which the catalog then hides.
	if c := e.del("cat"); c != http.StatusNoContent {
		t.Fatalf("hide cat: %d", c)
	}
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/crews", e.crewBody("Another")},
		{"PUT", "/api/crews/api-sweep", e.crewBody("API sweep")},
		{"POST", "/api/crews/api-sweep/duplicate", nil},
	} {
		resp, out := e.do(r.method, r.path, adminToken, r.body)
		wantAPIError(t, r.method+" "+r.path, resp, out, http.StatusBadRequest, "invalid_crew", `"cat"`)
	}
	if ids := e.crewIDs(); !slices.Equal(ids, []string{"api-sweep"}) {
		t.Fatalf("ids %v", ids)
	}
}

func TestCrewRoutesNeedAStore(t *testing.T) {
	e := newTestEnv(t, nil)
	srv, err := New(e.srv.cfg, e.srv.base, e.srv.log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ro := e.serve(srv)
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/crews", e.crewBody("Crew")},
		{"PUT", "/api/crews/crew", e.crewBody("Crew")},
		{"DELETE", "/api/crews/crew", nil},
		{"POST", "/api/crews/crew/duplicate", nil},
	} {
		resp, out := ro.do(r.method, r.path, adminToken, r.body)
		wantAPIError(t, r.method+" "+r.path, resp, out, http.StatusServiceUnavailable, "store_unavailable", "")
	}
	if list := ro.crews(); len(list) != 0 {
		t.Fatalf("crews without a store: %v", list)
	}
}

func TestCrewRoutesStopAt50Crews(t *testing.T) {
	e := newTestEnv(t, nil)
	for i := range 50 {
		e.sendCrew("POST", "/api/crews", e.crewBody(fmt.Sprintf("Crew %02d", i)), http.StatusCreated)
	}
	resp, out := e.do("POST", "/api/crews", adminToken, e.crewBody("One too many"))
	wantAPIError(t, "create", resp, out, http.StatusConflict, "too_many_crews", "50")
	resp, out = e.do("POST", "/api/crews/crew-00/duplicate", adminToken, nil)
	wantAPIError(t, "duplicate", resp, out, http.StatusConflict, "too_many_crews", "50")
	e.sendCrew("PUT", "/api/crews/crew-00", e.crewBody("Crew 00 again"), http.StatusOK)
	if n := len(e.crews()); n != 50 {
		t.Fatalf("%d crews", n)
	}
}

func TestCrewFailedSaveChangesNothing(t *testing.T) {
	e := newTestEnv(t, nil)
	e.sendCrew("POST", "/api/crews", e.crewBody("Kept"), http.StatusCreated)
	before := e.crews()
	// The data directory disappears, so no save can succeed.
	dir := e.srv.store.Dir()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/crews", e.crewBody("Lost")},
		{"PUT", "/api/crews/kept", e.crewBody("Changed")},
		{"POST", "/api/crews/kept/duplicate", nil},
		{"DELETE", "/api/crews/kept", nil},
	} {
		resp, out := e.do(r.method, r.path, adminToken, r.body)
		wantAPIError(t, r.method+" "+r.path, resp, out, http.StatusInternalServerError, "store_failed", "")
		if strings.Contains(fmt.Sprint(out), dir) {
			t.Errorf("%s %s names the data directory: %v", r.method, r.path, out)
		}
	}
	if after := e.crews(); !reflect.DeepEqual(after, before) {
		t.Fatalf("a failed save changed the crews:\n before %v\n after  %v", before, after)
	}
}

// A crews.json that cannot be used stops startup, as a bad catalog.json does.
func TestNewRefusesAMalformedCrewsFile(t *testing.T) {
	e := newTestEnv(t, nil)
	path := filepath.Join(e.srv.store.Dir(), "crews.json")
	if err := os.WriteFile(path, []byte(`{"crews": [{"id": "x", "bogus": 1}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := New(e.srv.cfg, e.srv.base, e.srv.log, nil, e.srv.store)
	if err == nil || srv != nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("New: %v", err)
	}
}

// PUT is the crews' first route of its kind: a dev UI on another origin
// (NUXT_PUBLIC_API_BASE) must be allowed to send it.
func TestDevCORSAllowsPut(t *testing.T) {
	e := newTestEnv(t, nil) // Dev is on
	req, _ := http.NewRequest("OPTIONS", e.http.URL+"/api/crews/x", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	methods := strings.Split(resp.Header.Get("Access-Control-Allow-Methods"), ", ")
	if resp.StatusCode != http.StatusNoContent || !slices.Contains(methods, "PUT") {
		t.Fatalf("preflight: %d %q", resp.StatusCode, methods)
	}
}

// gitRepo makes e.root a git repository with one commit, and skips the test
// when git is not installed. HOME is a temporary directory, so that no
// configuration of the user running the tests applies.
func (e *testEnv) gitRepo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(e.root, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "README"}, {"-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", e.root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// runCrewBody is a crew whose lead, an interactive sh, is typed a prompt that
// prints the crew's variables, and whose other member starts by hand.
func (e *testEnv) runCrewBody(isolation string) map[string]any {
	return map[string]any{
		"name": "API sweep", "goal": "ship /v1/users", "cwd": e.root, "where": "server", "isolation": isolation,
		"openAfterLaunch": false,
		"members": []any{
			map[string]any{"name": "lead", "agentId": "sh", "args": []any{"-i"},
				"prompt": `echo "C=$CONDUCTOR_CREW R=$CONDUCTOR_RUN M=$CONDUCTOR_MEMBER G=$GOAL T=${CONDUCTOR_NOTIFY_TOKEN:+set}"`,
				"start":  map[string]any{"when": "immediately"}},
			map[string]any{"name": "tests", "agentId": "sh", "args": []any{"-i"}, "prompt": "echo tests-here",
				"start": map[string]any{"when": "manual"}},
		},
	}
}

// stopEverything stops every session of e's server when the test ends.
func (e *testEnv) stopEverything(t *testing.T) {
	t.Cleanup(func() {
		e.srv.registry.Each(func(d session.Driver) { _ = d.Stop(context.Background()) })
	})
}

// runMember returns a member of a run as a reply carries it.
func runMember(t *testing.T, run map[string]any, name string) map[string]any {
	t.Helper()
	for _, m := range run["members"].([]any) {
		if m := m.(map[string]any); m["name"] == name {
			return m
		}
	}
	t.Fatalf("run has no member %q: %v", name, run)
	return nil
}

// A crew launches as a run of ordinary server sessions: each member in a
// worktree of its own, tagged with the run, with the crew's variables in its
// environment, and its prompt typed once it is ready. The run routes list it,
// report the members' diffs, start a member by hand, add one and stop it all.
func TestCrewRunLifecycle(t *testing.T) {
	e := newTestEnv(t, nil)
	e.gitRepo(t)
	e.stopEverything(t)
	e.sendCrew("POST", "/api/crews", e.runCrewBody("worktree"), http.StatusCreated)

	resp, out := e.do("POST", "/api/crews/api-sweep/launch", adminToken, nil)
	run, _ := out["run"].(map[string]any)
	if resp.StatusCode != http.StatusCreated || run == nil {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
	}
	runID, _ := run["id"].(string)
	lead := runMember(t, run, "lead")
	wantPath := filepath.Join(e.root, ".conductor", "worktrees", runID, "lead")
	// The launch answers once the sessions exist; the prompts come as they are ready.
	if !strings.HasPrefix(runID, "api-sweep-") || run["crewId"] != "api-sweep" || lead["status"] != "starting" ||
		lead["worktree"] != wantPath || lead["branch"] != "crew/"+runID+"/lead" || runMember(t, run, "tests")["status"] != "pending" {
		t.Fatalf("run %v", run)
	}
	leadID, _ := lead["sessionId"].(string)
	_, got := e.do("GET", "/api/sessions/"+leadID, adminToken, nil)
	info := got["session"].(map[string]any)
	if !reflect.DeepEqual(info["crew"], map[string]any{"runId": runID, "crewId": "api-sweep", "member": "lead"}) ||
		info["cwd"] != wantPath || info["branch"] != "crew/"+runID+"/lead" || info["name"] != "lead" {
		t.Fatalf("lead's session %v", info)
	}
	c := dialViewer(t, e, leadID, adminToken)
	c.hello(80, 24)
	c.expectOutput("C=api-sweep R=" + runID + " M=lead G=ship /v1/users T=set")
	// The worktrees stay out of the repository's status.
	if out, err := exec.Command("git", "-C", e.root, "status", "--porcelain").CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if d, _ := e.srv.registry.Get(leadID); !slices.ContainsFunc(d.(*session.Local).Activity(), func(a session.ActivityEntry) bool {
		return a.Type == session.ActivityInput && a.ByName == "crew"
	}) {
		t.Fatal("the prompt was not recorded as typed by the crew")
	}

	// Listed, and with diffs.
	_, out = e.do("GET", "/api/runs", adminToken, nil)
	if runs, _ := out["runs"].([]any); len(runs) != 1 || runs[0].(map[string]any)["id"] != runID {
		t.Fatalf("runs %v", out)
	}
	resp, out = e.do("GET", "/api/runs/"+runID, adminToken, nil)
	if d := runMember(t, out["run"].(map[string]any), "lead")["diff"]; resp.StatusCode != http.StatusOK ||
		!reflect.DeepEqual(d, map[string]any{"added": 0.0, "removed": 0.0}) {
		t.Fatalf("get: %d %v", resp.StatusCode, out)
	}

	// Started by hand, then again: 409.
	resp, out = e.do("POST", "/api/runs/"+runID+"/members/tests/start", adminToken, nil)
	if resp.StatusCode != http.StatusOK || runMember(t, out["run"].(map[string]any), "tests")["status"] != "starting" {
		t.Fatalf("start: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("POST", "/api/runs/"+runID+"/members/tests/start", adminToken, nil)
	wantAPIError(t, "start again", resp, out, http.StatusConflict, "member_started", "")
	resp, out = e.do("POST", "/api/runs/"+runID+"/members/ghost/start", adminToken, nil)
	wantAPIError(t, "start a stranger", resp, out, http.StatusNotFound, "not_found", "")

	// Added mid-run.
	docs := map[string]any{"name": "docs", "agentId": "sh", "prompt": "", "start": map[string]any{"when": "manual"}}
	resp, out = e.do("POST", "/api/runs/"+runID+"/members", adminToken, docs)
	if resp.StatusCode != http.StatusCreated || runMember(t, out["run"].(map[string]any), "docs")["status"] != "pending" {
		t.Fatalf("add: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("POST", "/api/runs/"+runID+"/members", adminToken, docs)
	wantAPIError(t, "add the same name", resp, out, http.StatusBadRequest, "invalid_crew", `"docs"`)
	nope := map[string]any{"name": "nope", "agentId": "nope", "prompt": "", "start": map[string]any{"when": "manual"}}
	resp, out = e.do("POST", "/api/runs/"+runID+"/members", adminToken, nope)
	wantAPIError(t, "add an unknown agent", resp, out, http.StatusBadRequest, "invalid_crew", `"nope"`)
	withArgs := map[string]any{"name": "cats", "agentId": "cat", "args": []any{"-n"}, "prompt": "", "start": map[string]any{"when": "manual"}}
	resp, out = e.do("POST", "/api/runs/"+runID+"/members", adminToken, withArgs)
	wantAPIError(t, "add arguments to an agent that takes none", resp, out, http.StatusBadRequest, "invalid_crew", "arguments")

	// Stopped: every session, and nothing starts any more.
	resp, out = e.do("POST", "/api/runs/"+runID+"/stop", adminToken, nil)
	if resp.StatusCode != http.StatusOK || out["run"].(map[string]any)["stoppedAt"] == nil {
		t.Fatalf("stop: %d %v", resp.StatusCode, out)
	}
	for _, name := range []string{"lead", "tests"} {
		id := runMember(t, out["run"].(map[string]any), name)["sessionId"].(string)
		if d, _ := e.srv.registry.Get(id); d.Info().Status != session.StatusStopped {
			t.Errorf("%s: %v", name, d.Info().Status)
		}
	}
	resp, out = e.do("POST", "/api/runs/"+runID+"/members/docs/start", adminToken, nil)
	wantAPIError(t, "start in a stopped run", resp, out, http.StatusConflict, "run_stopped", "")
	if _, err := os.Stat(filepath.Join(wantPath, "README")); err != nil {
		t.Fatalf("the worktree went: %v", err)
	}

	for _, path := range []string{"/api/runs/nope", "/api/runs/nope/stop", "/api/runs/nope/members/lead/start"} {
		method := "POST"
		if path == "/api/runs/nope" {
			method = "GET"
		}
		resp, out = e.do(method, path, adminToken, nil)
		wantAPIError(t, path, resp, out, http.StatusNotFound, "not_found", "")
	}
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/crews/api-sweep/launch"}, {"GET", "/api/runs"}, {"GET", "/api/runs/" + runID},
		{"POST", "/api/runs/" + runID + "/members"}, {"POST", "/api/runs/" + runID + "/members/docs/start"}, {"POST", "/api/runs/" + runID + "/stop"},
	} {
		for _, token := range []string{"", "wrong", "test-host-token"} {
			if resp, _ := e.do(r.method, r.path, token, nil); resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s with %q: %d", r.method, r.path, token, resp.StatusCode)
			}
		}
	}
}

// A launch that cannot go ahead is refused before any session starts.
func TestCrewLaunchRefusals(t *testing.T) {
	e := newTestEnv(t, nil)
	e.stopEverything(t)
	resp, out := e.do("POST", "/api/crews/nope/launch", adminToken, nil)
	wantAPIError(t, "unknown crew", resp, out, http.StatusNotFound, "not_found", "")

	save := func(name string, change func(b map[string]any)) string {
		b := e.runCrewBody("none")
		b["name"] = name
		change(b)
		return e.sendCrew("POST", "/api/crews", b, http.StatusCreated)["id"].(string)
	}
	cases := []struct {
		name, id  string
		status    int
		code, msg string
	}{
		{"no members", save("Empty", func(b map[string]any) { b["members"] = []any{} }), http.StatusBadRequest, "invalid_crew", "no members"},
		{"on a host", save("Hosted", func(b map[string]any) { b["where"] = "host" }), http.StatusBadRequest, "invalid_crew", "host"},
		{"arguments to cat", save("Cats", func(b map[string]any) { crewMember(b, 1)["agentId"] = "cat" }), http.StatusBadRequest, "invalid_crew", "arguments"},
		{"worktrees without a repository", save("Trees", func(b map[string]any) { b["isolation"] = "worktree" }), http.StatusConflict, "not_a_repo", "git"},
		{"a cwd outside the roots", save("Outside", func(b map[string]any) { b["cwd"] = t.TempDir() }), http.StatusBadRequest, "invalid_cwd", "allowed roots"},
	}
	hidden := save("Hidden", func(b map[string]any) {
		crewMember(b, 1)["agentId"] = "cat"
		delete(crewMember(b, 1), "args")
	})
	for _, tc := range cases {
		resp, out := e.do("POST", "/api/crews/"+tc.id+"/launch", adminToken, nil)
		wantAPIError(t, tc.name, resp, out, tc.status, tc.code, tc.msg)
	}
	// An agent the catalog no longer has.
	if c := e.del("cat"); c != http.StatusNoContent {
		t.Fatalf("hide cat: %d", c)
	}
	resp, out = e.do("POST", "/api/crews/"+hidden+"/launch", adminToken, nil)
	wantAPIError(t, "a hidden agent", resp, out, http.StatusBadRequest, "invalid_crew", `"cat"`)
	if n := e.srv.registry.Count(); n != 0 {
		t.Fatalf("%d sessions started", n)
	}
	if entries, _ := os.ReadDir(filepath.Join(e.root, ".conductor")); len(entries) != 0 {
		t.Fatalf("made %v", entries)
	}
	if _, out := e.do("GET", "/api/runs", adminToken, nil); len(out["runs"].([]any)) != 0 {
		t.Fatalf("runs %v", out)
	}

	// Without a data directory there are no crews to launch.
	srv, err := New(e.srv.cfg, e.srv.base, e.srv.log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, out = e.serve(srv).do("POST", "/api/crews/api-sweep/launch", adminToken, nil)
	wantAPIError(t, "no store", resp, out, http.StatusServiceUnavailable, "store_unavailable", "")
}

// A session launched on its own gets none of the crew's variables.
func TestPlainSessionHasNoCrewVariables(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("sh")
	c := dialViewer(t, e, id, adminToken)
	c.hello(80, 24)
	c.send(proto.Encode(proto.TypeInput, []byte("echo \"X=${CONDUCTOR_CREW-no}${CONDUCTOR_RUN-no}${CONDUCTOR_MEMBER-no}${GOAL-no}\"\n")))
	c.expectOutput("X=nononono")
	if _, got := e.do("GET", "/api/sessions/"+id, adminToken, nil); got["session"].(map[string]any)["crew"] != nil {
		t.Fatalf("session %v", got["session"])
	}
}

// The run engine takes the server's activity and changes: a handoff one
// member reports through the events route is typed into the member it names,
// once that member's prompt is cleared when it waits on one, and the done a
// member reports starts the members after it.
func TestCrewHandoffReachesTheOtherMember(t *testing.T) {
	e := newTestEnv(t, nil)
	e.stopEverything(t)
	member := func(name string, start map[string]any) map[string]any {
		return map[string]any{"name": name, "agentId": "cat", "prompt": "", "start": start}
	}
	e.sendCrew("POST", "/api/crews", map[string]any{
		"name": "Relay", "goal": "ship", "cwd": e.root, "where": "server", "isolation": "none",
		"members": []any{
			member("lead", map[string]any{"when": "immediately"}),
			member("tests", map[string]any{"when": "immediately"}),
			member("docs", map[string]any{"when": "after", "member": "lead"}),
		},
	}, http.StatusCreated)
	resp, out := e.do("POST", "/api/crews/relay/launch", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
	}
	runID := out["run"].(map[string]any)["id"].(string)
	// memberNow reads a member of the run as GET /api/runs/{run} reports it.
	memberNow := func(name string) map[string]any {
		_, out := e.do("GET", "/api/runs/"+runID, adminToken, nil)
		return runMember(t, out["run"].(map[string]any), name)
	}
	waitRunning := func(name string) string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			m := memberNow(name)
			if m["status"] == "running" {
				return m["sessionId"].(string)
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s is %v", name, m)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	leadID, testsID := waitRunning("lead"), waitRunning("tests")
	agent := e.agentToken(leadID)

	resp, out = e.do("POST", "/api/sessions/"+leadID+"/events", agent, map[string]any{"type": "handoff", "to": "tests", "message": "run the suite"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("handoff: %d %v", resp.StatusCode, out)
	}
	d, _ := e.srv.registry.Get(testsID)
	tests := d.(*session.Local)
	waitTyped := func(text string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !slices.ContainsFunc(tests.Activity(), func(a session.ActivityEntry) bool {
			return a.Type == session.ActivityInput && a.ByName == "crew" && a.Message == text
		}) {
			if time.Now().After(deadline) {
				t.Fatalf("tests records %+v", tests.Activity())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitTyped("Handoff from lead: run the suite")
	c := dialViewer(t, e, testsID, adminToken)
	c.hello(80, 24)
	c.expectOutput("Handoff from lead: run the suite")
	waitLog := func(text string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, out := e.do("GET", "/api/runs/"+runID, adminToken, nil)
			log, _ := json.Marshal(out["run"].(map[string]any)["log"])
			if strings.Contains(string(log), text) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("run log %s", log)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitLog("handoff delivered from lead to tests")

	// tests waits on a prompt: the next handoff waits for it, and goes once
	// the agent clears the prompt, which records no entry.
	testsAgent := e.agentToken(testsID)
	if resp, out := e.do("POST", "/api/sessions/"+testsID+"/events", testsAgent, map[string]any{"type": "needs_input", "message": "Allow edit?"}); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("needs_input: %d %v", resp.StatusCode, out)
	}
	if resp, out := e.do("POST", "/api/sessions/"+leadID+"/events", agent, map[string]any{"type": "handoff", "to": "tests", "message": "and the docs"}); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("handoff: %d %v", resp.StatusCode, out)
	}
	waitLog("handoff queued from lead to tests: tests is waiting for input")
	if resp, out := e.do("POST", "/api/sessions/"+testsID+"/events", testsAgent, map[string]any{"type": "clear"}); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("clear: %d %v", resp.StatusCode, out)
	}
	waitTyped("Handoff from lead: and the docs")

	// lead is done: docs starts.
	if m := memberNow("docs"); m["status"] != "pending" {
		t.Fatalf("docs before lead is done: %v", m)
	}
	if resp, out := e.do("POST", "/api/sessions/"+leadID+"/events", agent, map[string]any{"type": "done"}); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("done: %d %v", resp.StatusCode, out)
	}
	waitRunning("docs")
}
