package execution

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderProxyBoundedAuthority(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer real-synthetic-key" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Conductor-Actor") != "" {
			t.Error("gateway did not replace authority")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["max_output_tokens"] != float64(16384) {
			t.Error("generation ceiling missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: synthetic\n\n")
	}))
	defer upstream.Close()
	p := newProviderProxy(proxyConfig{Adapter: "codex/0.154.0", Token: strings.Repeat("a", 64), Key: "real-synthetic-key"})
	p.upstream = upstream.URL
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Cookie", "must-not-forward")
		r.Header.Set("X-Conductor-Actor", "forged")
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		return w
	}
	if got := request("POST", "/v1/responses", p.config.Token, `{"model":"synthetic"}`); got.Code != 200 || !strings.Contains(got.Body.String(), "synthetic") {
		t.Fatal("bounded provider stream unavailable")
	}
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"POST", "/v1/responses", "wrong", `{}`, 401},
		{"POST", "/v1/files", p.config.Token, `{}`, 404},
		{"CONNECT", "/v1/responses", p.config.Token, `{}`, 404},
		{"POST", "/v1/responses?url=https://example.invalid", p.config.Token, `{}`, 404},
		{"POST", "/v1/responses", p.config.Token, `{"max_output_tokens":16385}`, 400},
		{"POST", "/v1/responses", p.config.Token, `{"max_output_tokens":-1}`, 400},
		{"POST", "/v1/responses", p.config.Token, strings.Repeat("x", maxProviderBody+1), 413},
	} {
		got := request(tc.method, tc.path, tc.token, tc.body)
		if got.Code != tc.status {
			t.Errorf("%s %s got %d", tc.method, tc.path, got.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("rejected request reached provider")
	}
	p.requests = maxProviderRequests
	if request("POST", "/v1/responses", p.config.Token, `{}`).Code != 429 {
		t.Fatal("call budget bypass")
	}
}

func TestProviderProxyRejectsRedirectsAndBoundsResponses(t *testing.T) {
	var calls atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer trap.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "real-key" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Error("Anthropic profile authority")
		}
		http.Redirect(w, r, trap.URL, 307)
	}))
	defer upstream.Close()
	p := newProviderProxy(proxyConfig{Adapter: "claude-code/2.1.270", Token: strings.Repeat("a", 64), Key: "real-key"})
	p.upstream = upstream.URL
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"max_tokens":10}`))
	r.Header.Set("X-Api-Key", p.config.Token)
	r.Header.Set("Anthropic-Version", "2023-06-01")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 502 || calls.Load() != 0 {
		t.Fatal("redirect followed with provider authority")
	}
	oversize := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(maxProviderResponse+1))
		w.WriteHeader(200)
	}))
	defer oversize.Close()
	p.upstream = oversize.URL
	r = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{}`))
	r.Header.Set("X-Api-Key", p.config.Token)
	w = httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 502 {
		t.Fatal("oversize response accepted")
	}
}

func TestDockerIsolatedProviderNetwork(t *testing.T) {
	runner, r := dockerFixture(t)
	runner.Profile = Profile{Adapter: "codex/0.154.0"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	network, endpoint, _, cleanup, err := runner.startProxy(ctx, "synthetic-never-sent-upstream", 30)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	// This command does not call a model. It tests the actual private Docker
	// topology: the proxy is reachable, direct external/host addresses are not.
	command := `const http=require('http');const net=require('net');(async()=>{await new Promise((resolve,reject)=>http.get('` + endpoint + `/health',r=>r.statusCode===204?resolve():reject(Error('health'))).on('error',reject));for(const host of ['1.1.1.1','172.17.0.1']){await new Promise((resolve,reject)=>{let s=net.connect({host,port:80});s.setTimeout(500);s.on('connect',()=>{s.destroy();reject(Error('external network reachable'))});s.on('error',resolve);s.on('timeout',()=>{s.destroy();resolve()})})}console.log('isolated proxy reachable; arbitrary egress blocked')})().catch(()=>process.exit(1))`
	profile := Profile{Adapter: "command/v1", Command: []string{"node", "-e", command}}
	result, err := runner.sandbox(ctx, wireRequest{Stage: "produce", Request: r, Profile: profile}, network)
	if err != nil {
		t.Fatal(err)
	}
	if result.Producer.State != "passed" {
		t.Fatalf("network boundary: %+v", result.Producer)
	}
}
