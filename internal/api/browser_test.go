package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserOriginIsFixedHTTPS(t *testing.T) {
	for _, tc := range []struct{ input, expected string }{
		{"https://CONDUCTOR.example.test:443/", "https://conductor.example.test"},
		{"https://[::1]:443", "https://[::1]"},
		{"https://127.0.0.1:8443", "https://127.0.0.1:8443"},
		{"http://localhost:8080", ""}, {"https://user:secret@example.test", ""},
		{"https://example.test/subpath", ""}, {"https://example.test?redirect=other", ""},
		{"https://example.test#", ""}, {"https://example.test?", ""}, {"//example.test", ""},
	} {
		actual, err := BrowserOrigin(tc.input)
		if (err == nil) != (tc.expected != "") || actual != tc.expected {
			t.Fatalf("input=%q origin=%q error=%v", tc.input, actual, err)
		}
	}
}

func TestBrowserCookieFlagsAndDuplicateRejection(t *testing.T) {
	w := httptest.NewRecorder()
	secret := newBrowserSecret()
	setBrowserCookie(w, sessionCookie, secret, sessionLifetime)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	c := cookies[0]
	if c.Name != sessionCookie || c.Path != "/" || c.Domain != "" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 3600 {
		t.Fatalf("unsafe cookie flags: %+v", c)
	}
	r := httptest.NewRequest("GET", "https://conductor.example.test", nil)
	r.AddCookie(c)
	if actual, ok := selectedCookie(r, sessionCookie); !ok || actual != secret {
		t.Fatal("valid cookie rejected")
	}
	r.AddCookie(c)
	if _, ok := selectedCookie(r, sessionCookie); ok {
		t.Fatal("ambiguous cookie accepted")
	}
}

func TestLocalAuthenticationMetadataDoesNotClaimLogin(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/config", "/api/v1/auth/config?mode=oidc"} {
		w := httptest.NewRecorder()
		New(&queryGuardService{}).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if strings.Contains(path, "?") {
			if w.Code != 400 {
				t.Fatal("metadata silently accepted a mode override")
			}
		} else if w.Code != 200 || !strings.Contains(w.Body.String(), `"browserLogin":false`) || !strings.Contains(w.Body.String(), `"mode":"local"`) {
			t.Fatalf("local metadata: %d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("authentication mode may be cached")
		}
	}
}
