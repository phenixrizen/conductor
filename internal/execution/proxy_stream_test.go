package execution

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A valid prefix is still an incomplete provider result. The gateway must retain
// the upstream transport failure instead of manufacturing a clean downstream EOF.
func TestProviderProxyPreservesInterruptedResponse(t *testing.T) {
	const prefix = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic partial\"}\n\n"
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "4096")
		_, _ = io.WriteString(w, prefix)
		// Returning closes this short body before its declared length is satisfied.
	}))
	defer upstream.Close()
	proxy := newProviderProxy(proxyConfig{Adapter: "codex/0.154.0", Model: "synthetic-model", Token: strings.Repeat("a", 64), Key: "synthetic-provider-key"})
	proxy.upstream = upstream.URL
	gateway := httptest.NewServer(proxy)
	defer gateway.Close()
	req, _ := http.NewRequest("POST", gateway.URL+"/v1/responses", strings.NewReader(`{"model":"synthetic-model","input":"synthetic","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
	resp, err := gateway.Client().Do(req)
	if err != nil {
		t.Fatal("response prefix was not delivered", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if string(body) != prefix {
		t.Fatalf("wrong bounded prefix: %q", body)
	}
	if err == nil {
		t.Fatal("gateway changed interrupted upstream response into successful EOF")
	}
	if calls.Load() != 1 {
		t.Fatal("interrupted request was replayed")
	}
}
