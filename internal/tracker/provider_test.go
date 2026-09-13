package tracker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
)

func providerConfig() domain.TrackerConfig {
	return domain.TrackerConfig{WorkspaceID: "workspace", Provider: "jira", Host: "synthetic.atlassian.net", ScopeID: "123", Profile: domain.JiraTrackerProfile, CredentialID: "api", WebhookCredentialID: "hook", ConductorOrigin: "https://conductor.example.invalid", Enabled: true, Statuses: []domain.TrackerStatusMapping{{ID: "1", Display: "Planning"}}}
}
func TestTrackerProviderRejectsRedirectsBoundsAndRevocation(t *testing.T) {
	for _, scenario := range []string{"redirect", "oversized", "malformed", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			followed := 0
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed++; w.WriteHeader(200) }))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch scenario {
				case "redirect":
					http.Redirect(w, r, target.URL, 307)
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", (512<<10)+1)))
				default:
					_, _ = w.Write([]byte(`{"id":"wrong"}`))
				}
			}))
			defer source.Close()
			adapter, err := New(providerConfig(), Credential{Token: "synthetic", Email: "synthetic@example.invalid"}, func(context.Context) error {
				if scenario == "revoked" {
					return domain.ErrForbidden
				}
				return nil
			}, Options{HTTPClient: &http.Client{}, Origin: source.URL, AllowInsecureLoopback: true})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = adapter.Read(context.Background(), "200", "https://conductor.example.invalid/link")
			if err == nil || followed != 0 || scenario == "revoked" && (calls != 0 || !errors.Is(err, domain.ErrForbidden)) {
				t.Fatalf("unsafe provider result err=%v calls=%d followed=%d", err, calls, followed)
			}
		})
	}
	for _, origin := range []string{"http://localhost:123", "http://192.0.2.1", "https://127.0.0.1", "http://127.0.0.1/path"} {
		if _, err := New(providerConfig(), Credential{Token: "synthetic", Email: "a"}, func(context.Context) error { return nil }, Options{Origin: origin, AllowInsecureLoopback: true}); err == nil {
			t.Errorf("accepted unsafe fixture origin %s", origin)
		}
	}
}
