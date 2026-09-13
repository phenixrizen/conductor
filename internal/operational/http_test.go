package operational

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixture struct{ fail bool }

func (f *fixture) OperationalSnapshot(context.Context) (Snapshot, error) {
	if f.fail {
		return Snapshot{}, errors.New("secret source and database URL")
	}
	return Snapshot{true, []Queue{{"publication", 2, 1, 30}, {"source-secret", 99, 0, 0}}}, nil
}
func TestDiagnosticsKeepLivenessReadinessAndAuthoritySeparate(t *testing.T) {
	source := &fixture{}
	m := New(source)
	for _, address := range []string{"0.0.0.0:9090", "localhost:9090", "example.invalid:9090", ":9090"} {
		if ValidAddress(address) {
			t.Fatal(address)
		}
	}
	if !ValidAddress("127.0.0.1:9090") {
		t.Fatal("loopback rejected")
	}
	api := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unauthorized", 401) }))
	r := httptest.NewRecorder()
	api.ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/session?token=secret", nil))
	if r.Code != 401 {
		t.Fatal("authentication changed")
	}
	r = httptest.NewRecorder()
	m.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	body := r.Body.String()
	if !strings.Contains(body, `status_class="4xx"} 1`) || !strings.Contains(body, `kind="publication"} 2`) || strings.Contains(body, "secret") {
		t.Fatal(body)
	}
	source.fail = true
	for _, tc := range []struct {
		path   string
		status int
	}{{"/readyz", 503}, {"/metrics", 503}, {"/healthz", 200}, {"/api/v1/session", 404}} {
		r = httptest.NewRecorder()
		m.Handler().ServeHTTP(r, httptest.NewRequest("GET", tc.path, nil))
		if r.Code != tc.status || strings.Contains(r.Body.String(), "secret") {
			t.Fatalf("%s: %d %s", tc.path, r.Code, r.Body.String())
		}
	}
}
