package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/session"
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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg, cat, log, nil)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		hs.Close()
	})
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
