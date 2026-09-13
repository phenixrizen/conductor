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
	p := newProviderProxy(proxyConfig{Adapter: "codex/0.154.0", Model: "synthetic", Token: strings.Repeat("a", 64), Key: "real-synthetic-key"})
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
	p := newProviderProxy(proxyConfig{Adapter: "claude-code/2.1.270", Model: "synthetic", Token: strings.Repeat("a", 64), Key: "real-key"})
	p.upstream = upstream.URL
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"synthetic","max_tokens":10}`))
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
	r = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"synthetic"}`))
	r.Header.Set("X-Api-Key", p.config.Token)
	w = httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 502 {
		t.Fatal("oversize response accepted")
	}
}

func TestDockerIsolatedProviderNetwork(t *testing.T) {
	runner, r := dockerFixture(t)
	runner.Profile = Profile{Adapter: "codex/0.154.0", Model: "synthetic"}
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

func TestProviderGatewayRejectsDelegatedNetworkAndAccountAuthority(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("body")
		}
		if body["model"] != "synthetic" {
			t.Error("operator model changed")
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	for _, adapter := range []string{"codex/0.154.0", "claude-code/2.1.270"} {
		p := newProviderProxy(proxyConfig{Adapter: adapter, Model: "synthetic", Token: strings.Repeat("a", 64), Key: "synthetic-key"})
		p.upstream = upstream.URL
		cases := []string{
			`{"model":"different"}`,
			`{"model":"synthetic","tools":[{"type":"web_search"}]}`,
			`{"model":"synthetic","tools":[{"type":"mcp","server_url":"https://example.invalid"}]}`,
			`{"model":"synthetic","tools":[{"type":"namespace","tools":[{"type":"file_search","vector_store_ids":["private"]}]}]}`,
			`{"model":"synthetic","previous_response_id":"private-response"}`,
			`{"model":"synthetic","background":true}`,
			`{"model":"synthetic","container":"private-container"}`,
			`{"model":"synthetic","tool_choice":{"type":"web_search"}}`,
		}
		for _, input := range []string{
			`[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://example.invalid/source"}]}]`,
			`[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"private-file"}]}]`,
			`[{"role":"user","content":[{"type":"document","source":{"type":"url","url":"https://example.invalid"}}]}]`,
			`[{"type":"additional_tools","tools":[{"type":"web_search"}]}]`,
			`[{"type":"item_reference","id":"private-item"}]`,
			`[{"id":"private-item","status":"completed"}]`,
		} {
			field := "input"
			if adapter == "claude-code/2.1.270" {
				field = "messages"
			}
			cases = append(cases, `{"model":"synthetic","`+field+`":`+input+`}`)
		}
		for _, body := range cases {
			path := "/v1/responses"
			if adapter == "claude-code/2.1.270" {
				path = "/v1/messages"
			}
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+p.config.Token)
			r.Header.Set("X-Api-Key", p.config.Token)
			w := httptest.NewRecorder()
			p.ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatalf("unsafe delegated request accepted for %s: %s (%d)", adapter, body, w.Code)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected model authority reached upstream")
	}
	for _, raw := range []string{
		`{"model":"synthetic","input":[{"role":"user","content":"Source contains https://example.invalid as ordinary text."}],"tools":[{"type":"function","name":"exec","parameters":{"type":"object","properties":{"file_id":{"type":"string"}}}}]}`,
		`{"model":"synthetic","input":[{"type":"function_call_output","call_id":"local-call","output":"https://example.invalid is local tool text"}],"tools":[{"type":"namespace","tools":[{"type":"custom","name":"apply_patch"}]}]}`,
	} {
		var body map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &body) != nil || !modelOnlyRequest("codex/0.154.0", "synthetic", body) {
			t.Fatal("inline local tool request rejected")
		}
		if string(body["store"]) != "false" {
			t.Fatal("provider storage not disabled")
		}
	}
}
