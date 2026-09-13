package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func TestCollectionCommandDoesNotReplayAfterLostResponseOnReusedConnection(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/context-collections" ||
			r.Header.Get("Idempotency-Key") != "explicit-recovery-key" ||
			r.Header.Get("Authorization") != "Bearer synthetic.token" ||
			r.Header.Get("X-Conductor-Workspace") != "team" ||
			r.Header.Get("X-Conductor-Repository") != "application" {
			t.Error("collection command changed its method, path, key, identity, or scope")
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("read command body: %v", err)
		}
		if posts.Add(1) == 1 {
			// Model a command accepted by the server whose acknowledgment is lost.
			// A replayable POST on this reused connection is retried by Go's
			// standard Transport when it encounters the response EOF.
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("take ownership of response connection: %v", err)
				return
			}
			_ = connection.Close()
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	c, err := NewAuthenticated(server.URL, "synthetic.token", "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP = &http.Client{Transport: transport, Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Warm the actual HTTP/1 connection: a fresh connection would not exercise
	// the standard transport's implicit retry path.
	if _, err := c.GetCollection(ctx, "warmup"); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	var firstConnectionReused atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if attempts.Add(1) == 1 {
			firstConnectionReused.Store(info.Reused)
		}
	}})
	_, err = c.CreateCollection(ctx, "explicit-recovery-key", domain.CollectionInput{
		Commit: strings.Repeat("a", 40), Paths: []string{"README.md"},
	})
	if !firstConnectionReused.Load() {
		t.Fatal("test did not exercise a reused connection")
	}
	if err == nil {
		t.Error("lost acknowledgment was hidden; an uncertain command must require explicit recovery")
	}
	if got := posts.Load(); got != 1 {
		t.Errorf("one collection command sent %d POST requests after the lost acknowledgment", got)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("one collection command made %d connection attempts", got)
	}
}
