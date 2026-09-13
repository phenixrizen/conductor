package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/sourcebundle"
)

// FullSource keeps the credential in a bounded proxy; the Git subprocess sees
// only a random loopback URL. Every Git HTTP exchange rechecks current authority.
func (c *Collector) FullSource(ctx context.Context, commit string, authorize func(context.Context) error) (domain.SourceBundleData, error) {
	var zero domain.SourceBundleData
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if !oid(commit) || authorize == nil {
		return zero, failure("invalid_input")
	}
	session := &collection{c: c, authorize: authorize, commit: commit, trees: map[string]treeResult{}}
	target, err := session.gitTarget(ctx)
	if err != nil {
		return zero, err
	}
	expectedTree, treeErr := session.commitRoot(ctx)
	if treeErr != nil {
		err = treeErr
		return zero, err
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return zero, failure("unavailable")
	}
	var nonce [24]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		listener.Close()
		return zero, failure("unavailable")
	}
	prefix := "/" + hex.EncodeToString(nonce[:])
	var lock sync.Mutex
	calls, received := 0, 0
	failed := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		// Serializing forwarding bounds cumulative source bytes and ensures a failed
		// exchange cannot be retried behind a successful-looking Git response.
		deny := func() { failed = true; http.Error(w, "source unavailable", http.StatusBadGateway) }
		suffix := strings.TrimPrefix(r.URL.Path, prefix)
		if failed || !strings.HasPrefix(r.URL.Path, prefix) || r.URL.RawPath != "" || calls >= 16 || r.Header.Get("Authorization") != "" || (r.Method != "GET" || suffix != "/info/refs" || r.URL.RawQuery != "service=git-upload-pack") && (r.Method != "POST" || suffix != "/git-upload-pack" || r.URL.RawQuery != "") {
			deny()
			return
		}
		if e := authorize(ctx); e != nil {
			deny()
			return
		}
		calls++
		body, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if e != nil || len(body) > 1<<20 {
			deny()
			return
		}
		request, e := http.NewRequestWithContext(ctx, r.Method, target+suffix, bytes.NewReader(body))
		if e != nil {
			deny()
			return
		}
		request.URL.RawQuery = r.URL.RawQuery
		protocol := r.Header.Get("Git-Protocol")
		if protocol != "" && protocol != "version=2" {
			deny()
			return
		}
		request.Header.Set("Git-Protocol", protocol)
		if r.Method == "POST" {
			if r.Header.Get("Content-Type") != "application/x-git-upload-pack-request" {
				deny()
				return
			}
			request.Header.Set("Content-Type", "application/x-git-upload-pack-request")
		}
		user := "x-access-token"
		if c.binding.Provider == "gitlab" {
			user = "oauth2"
		}
		request.SetBasicAuth(user, c.token)
		client := *c.client
		client.Timeout = 35 * time.Second
		response, e := client.Do(request)
		if e != nil {
			deny()
			return
		}
		defer response.Body.Close()
		expected := "application/x-git-upload-pack-result"
		if r.Method == "GET" {
			expected = "application/x-git-upload-pack-advertisement"
		}
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != expected {
			deny()
			return
		}
		// Buffer before forwarding so a cap violation never looks like a complete
		// pack. Neither upstream response text nor transport errors are exposed.
		remaining := domain.MaxSourceBundleBytes - received
		data, e := io.ReadAll(io.LimitReader(response.Body, int64(remaining)+1))
		if e != nil || len(data) > remaining {
			deny()
			return
		}
		received += len(data)
		w.Header().Set("Content-Type", expected)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 40 * time.Second, WriteTimeout: 40 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	result, e := sourcebundle.Fetch(ctx, "http://"+listener.Addr().String()+prefix, commit)
	lock.Lock()
	wasFailed := failed
	lock.Unlock()
	if e != nil || wasFailed || expectedTree != "" && result.Tree != expectedTree {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		return zero, failure("unavailable")
	}
	final, err := session.gitTarget(ctx)
	if err != nil || final != target {
		return zero, failure("identity_mismatch")
	}
	if _, err = session.commitRoot(ctx); err != nil {
		return zero, err
	}
	return result, nil
}
func (s *collection) gitTarget(ctx context.Context) (string, *Error) {
	var v struct {
		ID       json.RawMessage `json:"id"`
		CloneURL string          `json:"clone_url"`
		HTTPURL  string          `json:"http_url_to_repo"`
	}
	if _, err := s.get(ctx, s.c.prefix, &v); err != nil {
		return "", err
	}
	if string(v.ID) != s.c.binding.ProviderID {
		return "", failure("identity_mismatch")
	}
	target := v.CloneURL
	if s.c.binding.Provider == "gitlab" {
		target = v.HTTPURL
	}
	u, err := url.Parse(target)
	if err != nil || u == nil || u.Scheme != "https" || u.Host != s.c.binding.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasSuffix(u.Path, ".git") || len(u.Path) > 2048 {
		return "", failure("invalid_response")
	}
	for _, part := range strings.Split(strings.TrimPrefix(u.Path, "/"), "/") {
		if !validName(part) {
			return "", failure("invalid_response")
		}
	}
	if s.c.binding.Provider == "github" && u.Path != "/"+s.c.binding.Locator+".git" {
		return "", failure("identity_mismatch")
	}
	if s.c.gitOrigin != "" {
		target = s.c.gitOrigin + u.Path
	}
	return strings.TrimSuffix(target, "/"), nil
}
