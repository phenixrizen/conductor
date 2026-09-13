package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/phenixrizen/conductor/internal/domain"
)

const MaxWebhookBytes = 1 << 20

// WebhookEvent is a reconciliation hint. The authenticated payload never assigns
// a passing check, merge, deployment or package lifecycle state directly.
type WebhookEvent struct{ Key, PayloadDigest, Type, Commit string }

func VerifyWebhook(target domain.DeliveryTarget, secret string, headers http.Header, raw []byte) (WebhookEvent, error) {
	var e WebhookEvent
	if len(raw) == 0 || len(raw) > MaxWebhookBytes || len(secret) < 32 || len(secret) > 256 {
		return e, ErrProvider
	}
	one := func(name string) string {
		v := headers.Values(name)
		if len(v) != 1 {
			return ""
		}
		return v[0]
	}
	if target.Provider == "github" {
		signature := one("X-Hub-Signature-256")
		actual, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
		if err != nil || !strings.HasPrefix(signature, "sha256=") {
			return e, ErrProvider
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(raw)
		if !hmac.Equal(actual, mac.Sum(nil)) {
			return e, ErrProvider
		}
		e.Key, e.Type = one("X-GitHub-Delivery"), one("X-GitHub-Event")
	} else if target.Provider == "gitlab" {
		token := one("X-Gitlab-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
			return e, ErrProvider
		}
		e.Key, e.Type = one("Idempotency-Key"), one("X-Gitlab-Event")
	} else {
		return e, ErrProvider
	}
	if domain.ValidateCollectionKey(e.Key) != nil || len(e.Type) > 64 {
		return e, ErrProvider
	}
	var body struct {
		CommitURL  string `json:"commit_url"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		Project struct {
			ID int64 `json:"id"`
		} `json:"project"`
		ProjectID int64  `json:"project_id"`
		SHA       string `json:"sha"`
		After     string `json:"after"`
		Pull      struct {
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
		Check struct {
			SHA string `json:"head_sha"`
		} `json:"check_run"`
		Deployment struct {
			SHA string `json:"sha"`
		} `json:"deployment"`
		Attributes struct {
			SHA        string `json:"sha"`
			LastCommit struct {
				ID string `json:"id"`
			} `json:"last_commit"`
		} `json:"object_attributes"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return e, ErrProvider
	}
	repoID := body.Repository.ID
	if target.Provider == "github" {
		switch e.Type {
		case "ping":
		case "pull_request":
			e.Commit = body.Pull.Head.SHA
		case "check_run":
			e.Commit = body.Check.SHA
		case "status":
			e.Commit = body.SHA
		case "push":
			e.Commit = body.After
		case "deployment_status":
			e.Commit = body.Deployment.SHA
		default:
			return e, ErrProvider
		}
	} else {
		repoID = body.Project.ID
		if repoID == 0 {
			repoID = body.ProjectID
		}
		switch e.Type {
		case "Merge Request Hook":
			e.Commit = body.Attributes.LastCommit.ID
		case "Pipeline Hook":
			e.Commit = body.Attributes.SHA
		case "Push Hook":
			e.Commit = body.After
		case "Deployment Hook":
			u, err := url.Parse(body.CommitURL)
			if err != nil || u.Scheme != "https" || u.Host != target.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return e, ErrMismatch
			}
			parts := strings.Split(u.Path, "/-/commit/")
			if len(parts) != 2 {
				return e, ErrMismatch
			}
			e.Commit = parts[1]
		default:
			return e, ErrProvider
		}
	}
	if strconv.FormatInt(repoID, 10) != target.ProviderID || (e.Type != "ping" && !domain.IsLowerHex(e.Commit, 40)) {
		return e, ErrMismatch
	}
	digest := sha256.Sum256(raw)
	e.PayloadDigest = hex.EncodeToString(digest[:])
	return e, nil
}
