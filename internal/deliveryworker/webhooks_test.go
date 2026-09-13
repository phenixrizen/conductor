package deliveryworker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/phenixrizen/conductor/internal/delivery"
	"github.com/phenixrizen/conductor/internal/domain"
)

type webhookDB struct {
	mu     sync.Mutex
	events []delivery.WebhookEvent
}

func (d *webhookDB) RecordDeliveryWebhook(_ context.Context, target domain.DeliveryTarget, event delivery.WebhookEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if target.RepositoryID != "repo" {
		return domain.ErrForbidden
	}
	d.events = append(d.events, event)
	return nil
}
func TestWebhookHTTPUsesFixedScopeAndVerifiedPayload(t *testing.T) {
	secret := strings.Repeat("s", 32)
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			db := &webhookDB{}
			handler, err := NewWebhookHandler(db, []WebhookBinding{{WorkspaceID: "team", RepositoryID: "repo", Provider: provider, Host: provider + ".com", ProviderID: "42", SecretFile: path}})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			commit := strings.Repeat("a", 40)
			raw := []byte(`{"repository":{"id":42},"sha":"` + commit + `"}`)
			if provider == "gitlab" {
				raw = []byte(`{"project":{"id":42},"commit_url":"https://gitlab.com/synthetic/repo/-/commit/` + commit + `"}`)
			}
			send := func(forged bool) int {
				t.Helper()
				req, err := http.NewRequest("POST", server.URL+"/webhooks/"+provider+"/repo", bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				key := secret
				if forged {
					key = strings.Repeat("x", 32)
				}
				if provider == "github" {
					mac := hmac.New(sha256.New, []byte(key))
					mac.Write(raw)
					req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
					req.Header.Set("X-GitHub-Delivery", "fixture-event")
					req.Header.Set("X-GitHub-Event", "status")
				} else {
					req.Header.Set("X-Gitlab-Token", key)
					req.Header.Set("Idempotency-Key", "fixture-event")
					req.Header.Set("X-Gitlab-Event", "Deployment Hook")
				}
				response, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, _ := io.ReadAll(response.Body)
				if bytes.Contains(body, []byte(secret)) {
					t.Fatal("credential leaked")
				}
				return response.StatusCode
			}
			if send(false) != 202 || send(true) != 401 {
				t.Fatal("webhook authentication failed")
			}
			db.mu.Lock()
			defer db.mu.Unlock()
			if len(db.events) != 1 || db.events[0].Commit != commit {
				t.Fatal("unverified or missing event")
			}
		})
	}
}
