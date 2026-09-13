package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
	"strings"
	"testing"
)

func TestAuthenticatedWebhookHints(t *testing.T) {
	secret := strings.Repeat("s", 32)
	commit := strings.Repeat("a", 40)
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			target := domain.DeliveryTarget{Provider: provider, ProviderID: "42"}
			headers := http.Header{}
			raw := []byte(`{"repository":{"id":42},"sha":"` + commit + `"}`)
			if provider == "github" {
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write(raw)
				headers.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
				headers.Set("X-GitHub-Delivery", "fixture-event")
				headers.Set("X-GitHub-Event", "status")
			} else {
				raw = []byte(`{"project":{"id":42},"object_attributes":{"sha":"` + commit + `"}}`)
				headers.Set("X-Gitlab-Token", secret)
				headers.Set("Idempotency-Key", "fixture-event")
				headers.Set("X-Gitlab-Event", "Pipeline Hook")
			}
			event, err := VerifyWebhook(target, secret, headers, raw)
			if err != nil || event.Commit != commit || !domain.IsLowerHex(event.PayloadDigest, 64) {
				t.Fatalf("valid event %#v %v", event, err)
			}
			target.ProviderID = "43"
			if _, err = VerifyWebhook(target, secret, headers, raw); err == nil {
				t.Fatal("cross repository payload accepted")
			}
			target.ProviderID = "42"
			if _, err = VerifyWebhook(target, strings.Repeat("x", 32), headers, raw); err == nil {
				t.Fatal("forged event accepted")
			}
			header := "X-Hub-Signature-256"
			if provider == "gitlab" {
				header = "X-Gitlab-Token"
			}
			headers.Add(header, headers.Get(header))
			if _, err = VerifyWebhook(target, secret, headers, raw); err == nil {
				t.Fatal("duplicate authentication accepted")
			}
		})
	}
}
