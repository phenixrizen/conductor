package trackerworker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type InboxStore interface {
	TrackerWebhookConfig(context.Context, string) (domain.TrackerConfig, int64, error)
	AcceptTrackerEvent(context.Context, string, int64, string, string) error
}

// WebhookHandler accepts only signed raw bodies. Events enqueue scoped refreshes;
// no ticket field from the payload becomes a trusted current snapshot.
func WebhookHandler(db InboxStore, credentials Credentials) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		reject := func() { http.Error(w, "tracker webhook unavailable", http.StatusUnauthorized) }
		if r.Method != "POST" || r.URL.RawQuery != "" || r.URL.RawPath != "" || !strings.HasPrefix(r.URL.Path, "/webhooks/tracker/") {
			reject()
			return
		}
		workspace := strings.TrimPrefix(r.URL.Path, "/webhooks/tracker/")
		if domain.ValidateAccessID(workspace) != nil {
			reject()
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		config, version, e := db.TrackerWebhookConfig(ctx, workspace)
		if e != nil || !config.Enabled {
			reject()
			return
		}
		secret, e := credentials(ctx, config, config.WebhookCredentialID)
		if e != nil || len(secret.Token) < 16 {
			reject()
			return
		}
		body, e := io.ReadAll(io.LimitReader(r.Body, (256<<10)+1))
		if e != nil || len(body) > 256<<10 {
			reject()
			return
		}
		name := "Linear-Signature"
		if config.Provider == "jira" {
			name = "X-Hub-Signature"
		}
		values := r.Header.Values(name)
		if len(values) != 1 {
			reject()
			return
		}
		signature := values[0]
		if config.Provider == "jira" {
			if !strings.HasPrefix(signature, "sha256=") {
				reject()
				return
			}
			signature = strings.TrimPrefix(signature, "sha256=")
		}
		provided, e := hex.DecodeString(signature)
		mac := hmac.New(sha256.New, []byte(secret.Token))
		_, _ = mac.Write(body)
		if e != nil || !hmac.Equal(provided, mac.Sum(nil)) {
			reject()
			return
		}
		var payload struct {
			Type             string `json:"type"`
			Action           string `json:"action"`
			OrganizationID   string `json:"organizationId"`
			WebhookTimestamp int64  `json:"webhookTimestamp"`
			Timestamp        int64  `json:"timestamp"`
			WebhookEvent     string `json:"webhookEvent"`
			Data             struct {
				ID string `json:"id"`
			} `json:"data"`
			Issue struct {
				ID     string `json:"id"`
				Fields struct {
					Project struct {
						ID string `json:"id"`
					} `json:"project"`
				} `json:"fields"`
			} `json:"issue"`
		}
		if json.Unmarshal(body, &payload) != nil {
			reject()
			return
		}
		issueID := payload.Data.ID
		stamp := payload.WebhookTimestamp
		maxAge := time.Minute
		if config.Provider == "linear" {
			if payload.Type != "Issue" || payload.OrganizationID != config.OrganizationID {
				reject()
				return
			}
		} else {
			issueID = payload.Issue.ID
			stamp = payload.Timestamp
			maxAge = 24 * time.Hour
			if payload.WebhookEvent != "jira:issue_created" && payload.WebhookEvent != "jira:issue_updated" && payload.WebhookEvent != "jira:issue_deleted" {
				reject()
				return
			}
			if p := payload.Issue.Fields.Project.ID; p != "" && p != config.ScopeID {
				reject()
				return
			}
		}
		age := time.Since(time.UnixMilli(stamp))
		if age > maxAge || age < -time.Minute || !domain.TrackerRecordID(config.Provider, issueID) {
			reject()
			return
		}
		digest := sha256.Sum256(body)
		if e = db.AcceptTrackerEvent(ctx, workspace, version, issueID, hex.EncodeToString(digest[:])); e != nil {
			http.Error(w, "tracker webhook reconciliation unavailable", 503)
			return
		}
		w.WriteHeader(200)
	})
}
