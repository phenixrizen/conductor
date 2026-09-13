package execution

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const maxProviderRequests = 64
const maxProviderBody = 4 << 20
const maxProviderResponse = 16 << 20
const maxProviderTotal = 64 << 20

type proxyConfig struct {
	Adapter string `json:"adapter"`
	Model   string `json:"model"`
	Token   string `json:"token"`
	Key     string `json:"key"`
	Seconds int    `json:"seconds"`
}

type providerProxy struct {
	config   proxyConfig
	client   *http.Client
	upstream string
	mu       sync.Mutex
	requests int
	bytes    int64
	active   chan struct{}
}

func newProviderProxy(c proxyConfig) *providerProxy {
	upstream := "https://api.openai.com"
	if c.Adapter == "claude-code/2.1.270" {
		upstream = "https://api.anthropic.com"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.MaxResponseHeaderBytes = 16 << 10
	return &providerProxy{config: c, upstream: upstream, active: make(chan struct{}, 2), client: &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// ProxyMain runs in a separate trusted container with external egress. Its
// private internal network is the producer's only network; no real provider key
// is sent to that producer. No source, key, or provider response is logged.
func ProxyMain(ctx context.Context, input io.Reader) error {
	if os.Getpid() != 1 || os.Getuid() != 10001 {
		return fmt.Errorf("%w: proxy must be unprivileged container PID 1", ErrSandbox)
	}
	if err := noNewPrivileges(); err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(input, 16385))
	if err != nil || len(b) > 16384 {
		return ErrInvalid
	}
	var config proxyConfig
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&config) != nil {
		return ErrInvalid
	}
	if (config.Adapter != "codex/0.154.0" && config.Adapter != "claude-code/2.1.270") || config.Model == "" || len(config.Model) > 128 || strings.ContainsAny(config.Model, "\x00\r\n") || !digest.MatchString(config.Token) || config.Key == "" || len(config.Key) > 8192 || strings.ContainsAny(config.Key, "\x00\r\n") || config.Seconds < 1 || config.Seconds > 3600 {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(config.Seconds)*time.Second)
	defer cancel()
	server := &http.Server{Addr: ":8787", Handler: newProviderProxy(config), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { <-ctx.Done(); _ = server.Close() }()
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (p *providerProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/health" && r.RemoteAddr != "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if p.config.Adapter == "claude-code/2.1.270" {
		token = r.Header.Get("X-Api-Key")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(p.config.Token)) != 1 {
		http.Error(w, "task authentication required", http.StatusUnauthorized)
		return
	}
	validPath := r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/responses/compact"
	if p.config.Adapter == "claude-code/2.1.270" {
		validPath = r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/messages/count_tokens"
	}
	if r.Method != "POST" || !validPath || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.Header.Get("Upgrade") != "" {
		http.Error(w, "provider operation unavailable", http.StatusNotFound)
		return
	}
	select {
	case p.active <- struct{}{}:
		defer func() { <-p.active }()
	default:
		http.Error(w, "task concurrency bound", http.StatusTooManyRequests)
		return
	}
	p.mu.Lock()
	allowed := p.requests < maxProviderRequests && p.bytes < maxProviderTotal
	if allowed {
		p.requests++
	}
	p.mu.Unlock()
	if !allowed {
		http.Error(w, "task provider budget exhausted", http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProviderBody))
	if err != nil {
		http.Error(w, "bounded JSON request required", http.StatusRequestEntityTooLarge)
		return
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil || object == nil {
		http.Error(w, "JSON object required", http.StatusBadRequest)
		return
	}
	if !modelOnlyRequest(p.config.Adapter, p.config.Model, object) {
		http.Error(w, "model-only request required", http.StatusBadRequest)
		return
	}
	// Enforce a finite per-call generation ceiling independently of assistant
	// settings. This is a token/call bound, not a promise of exact currency spend.
	field := "max_output_tokens"
	if p.config.Adapter == "claude-code/2.1.270" {
		field = "max_tokens"
	}
	if !strings.HasSuffix(r.URL.Path, "/count_tokens") && !strings.HasSuffix(r.URL.Path, "/compact") {
		var n int
		if raw, ok := object[field]; !ok {
			object[field] = json.RawMessage("16384")
		} else if json.Unmarshal(raw, &n) != nil || n < 1 || n > 16384 {
			http.Error(w, "generation token bound exceeded", http.StatusBadRequest)
			return
		}
	}
	body, err = json.Marshal(object)
	if err != nil {
		http.Error(w, "JSON request unavailable", http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "POST", p.upstream+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "provider unavailable", http.StatusBadGateway)
		return
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	if p.config.Adapter == "codex/0.154.0" {
		req.Header.Set("Authorization", "Bearer "+p.config.Key)
	} else {
		req.Header.Set("X-Api-Key", p.config.Key)
		for _, name := range []string{"Anthropic-Version", "Anthropic-Beta"} {
			value := r.Header.Get(name)
			if len(value) > 4096 {
				http.Error(w, "header bound exceeded", http.StatusBadRequest)
				return
			}
			if value != "" {
				req.Header.Set(name, value)
			}
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, "provider result unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		http.Error(w, "provider redirect refused", http.StatusBadGateway)
		return
	}
	if resp.ContentLength > maxProviderResponse {
		http.Error(w, "provider response bound exceeded", http.StatusBadGateway)
		return
	}
	typeHeader := resp.Header.Get("Content-Type")
	if len(typeHeader) < 256 {
		w.Header().Set("Content-Type", typeHeader)
	}
	w.WriteHeader(resp.StatusCode)
	reader := io.LimitReader(resp.Body, maxProviderResponse+1)
	buffer := make([]byte, 32<<10)
	written := 0
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			written += n
			p.mu.Lock()
			p.bytes += int64(n)
			over := p.bytes > maxProviderTotal
			p.mu.Unlock()
			if written > maxProviderResponse || over {
				// Abort the stream rather than reporting clean EOF for partial output.
				panic(http.ErrAbortHandler)
			}
			if _, err = w.Write(buffer[:n]); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				// Preserve upstream interruption after headers or partial SSE events.
				// Returning normally would manufacture a clean downstream EOF.
				panic(http.ErrAbortHandler)
			}
			return
		}
	}
}

func ProxyReady() error {
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Get("http://127.0.0.1:8787/health")
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return ErrUnavailable
	}
	return nil
}
