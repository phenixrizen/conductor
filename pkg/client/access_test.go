package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAuthenticatedDiscoveryAndCommandHeaders(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic.token.signature" || len(r.Header.Values("X-Conductor-Actor")) != 0 || r.Header.Get("X-Conductor-Workspace") != "workspace-1" || r.Header.Get("X-Conductor-Repository") != "repo-1" {
			t.Error("authenticated request did not preserve its credential and scope boundary")
		}
		switch r.URL.Path {
		case "/api/v1/session":
			_, _ = w.Write([]byte(`{"principal":{"id":"person-1","kind":"human"},"workspaces":[{"id":"workspace-1","name":"Synthetic workspace"}],"truncated":false}`))
		case "/api/v1/repositories":
			_, _ = w.Write([]byte(`{"repositories":[{"id":"repo-1","workspaceId":"workspace-1","provider":"github","host":"github.com","providerId":"42","name":"synthetic/repo","canRead":true,"canAuthor":true,"canApprove":false}],"truncated":true}`))
		default:
			_, _ = w.Write([]byte(`{"id":"CHG-test","revision":{"number":1}}`))
		}
	}))
	defer server.Close()
	c, err := NewAuthenticated(server.URL, "synthetic.token.signature", "workspace-1", "repo-1")
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP = server.Client()
	// This legacy public field cannot spoof the authenticated principal.
	c.Actor = "forged-architect"
	workspace, repository, authenticated := c.AuthenticatedScope()
	if !authenticated || workspace != "workspace-1" || repository != "repo-1" {
		t.Fatal("credential-free scope getter changed authenticated scope")
	}
	session, err := c.Session(context.Background())
	if err != nil || session.Principal.ID != "person-1" || len(session.Workspaces) != 1 {
		t.Fatalf("session: %+v %v", session, err)
	}
	repos, err := c.Repositories(context.Background())
	if err != nil || len(repos.Repositories) != 1 || repos.Repositories[0].CanApprove || !repos.Truncated {
		t.Fatalf("repositories: %+v %v", repos, err)
	}
	if _, err := c.Approve(context.Background(), "CHG-test", 1, "inspected-digest"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("unexpected request count: %d", calls.Load())
	}
}

func TestLocalClientHasNoAuthenticatedScope(t *testing.T) {
	workspace, repository, authenticated := New("http://127.0.0.1", "reviewer").AuthenticatedScope()
	if authenticated || workspace != "" || repository != "" {
		t.Fatal("local actor acquired authenticated scope")
	}
}

func TestAuthenticatedClientRejectsUnsafeConfigurationBeforeRequests(t *testing.T) {
	for _, base := range []string{"http://api.example.test", "http://localhost.example.test", "http://10.0.0.1", "https://user:secret@example.test", "https://example.test?token=hidden", "https://example.test?", "https://example.test#fragment", "https://example.test#", "file:///tmp/api", "//example.test", "https://:443"} {
		t.Run(base, func(t *testing.T) {
			if _, err := NewAuthenticated(base, "synthetic.token", "", ""); err == nil {
				t.Fatal("unsafe credential URL accepted")
			}
		})
	}
	for _, base := range []string{"https://api.example.test/prefix", "http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		if _, err := NewAuthenticated(base, "synthetic.token", "", ""); err != nil {
			t.Fatalf("supported URL: %v", err)
		}
	}
	for _, token := range []string{"", "with space", "with\nnewline", "with\rreturn", "with\x00null", "Bearer token", "token=middle", "=", "nonASCIIé", strings.Repeat("x", MaxTokenBytes+1)} {
		if _, err := NewAuthenticated("https://api.example.test", token, "", ""); err == nil {
			t.Fatal("malformed bearer token accepted")
		}
	}
	for _, scope := range []string{"\r\nX-Conductor-Actor:spoofed", " workspace", "workspace\t", "workspace\x1b", strings.Repeat("x", 129)} {
		if _, err := NewAuthenticated("https://api.example.test", "synthetic.token", scope, ""); err == nil {
			t.Fatal("malformed workspace accepted")
		}
		if _, err := NewAuthenticated("https://api.example.test", "synthetic.token", "", scope); err == nil {
			t.Fatal("malformed repository accepted")
		}
	}
}

type countingTransport struct{ calls atomic.Int32 }

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return nil, errors.New("unexpected request")
}

func TestMutableBaseURLCannotDowngradeCredentialTransport(t *testing.T) {
	c, err := NewAuthenticated("https://api.example.test", "synthetic.token", "", "")
	if err != nil {
		t.Fatal(err)
	}
	transport := &countingTransport{}
	c.HTTP = &http.Client{Transport: transport}
	for _, base := range []string{"http://api.example.test", "https://attacker:secret@example.test", "https://api.example.test?unexpected"} {
		c.BaseURL = base
		if _, err := c.Session(context.Background()); err == nil {
			t.Fatal("mutated URL accepted")
		}
	}
	if transport.calls.Load() != 0 {
		t.Fatal("unsafe URL reached the credential transport")
	}
}

func TestAuthenticatedRedirectsNeverForwardBearer(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var destinationCalls atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
			defer destination.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic.token" {
					t.Error("source did not receive credential")
				}
				http.Redirect(w, r, destination.URL, status)
			}))
			defer source.Close()
			c, err := NewAuthenticated(source.URL, "synthetic.token", "", "")
			if err != nil {
				t.Fatal(err)
			}
			c.HTTP = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { t.Error("caller redirect policy used"); return nil }}
			_, err = c.Approve(context.Background(), "CHG-test", 1, "inspected-digest")
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "unexpected_redirect" {
				t.Fatalf("redirect error: %v", err)
			}
			if destinationCalls.Load() != 0 {
				t.Fatal("bearer token was forwarded")
			}
		})
	}
}
