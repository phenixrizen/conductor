package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func TestDeploymentReadEvidencePinsCommitEnvironmentAndProvenance(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, mode := range []string{"success", "missing", "truncated", "wrong-commit", "unavailable"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				commit := strings.Repeat("a", 40)
				updated := time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC)
				observed := domain.DeliveryObservation{Commit: commit, State: "draft", ProductionOutcome: "not_observed"}
				target := domain.DeliveryTarget{WorkspaceID: "workspace", RepositoryID: "repo", Provider: provider, Host: provider + ".com", ProviderID: "42", Profile: domain.GitHubDeliveryProfile, Locator: "synthetic/repo"}
				if provider == "gitlab" {
					target.Profile = domain.GitLabDeliveryProfile
					target.Locator = ""
				}
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Fatal("deployment mutation")
					}
					w.Header().Set("Content-Type", "application/json")
					emit := func(v any) { _ = json.NewEncoder(w).Encode(v) }
					if mode == "unavailable" {
						w.WriteHeader(403)
						return
					}
					sha := commit
					if mode == "wrong-commit" {
						sha = strings.Repeat("b", 40)
					}
					if r.URL.Path == "/deployments" {
						if mode == "missing" {
							emit([]any{})
							return
						}
						if mode == "truncated" {
							if provider == "github" {
								w.Header().Set("Link", `<ignored>; rel="next"`)
							} else {
								w.Header().Set("X-Next-Page", "2")
							}
						}
						if provider == "github" {
							if r.URL.Query().Get("sha") != commit {
								t.Error("deployment query not pinned")
							}
							emit([]any{map[string]any{"id": 12, "sha": sha, "environment": "staging", "production_environment": false, "updated_at": updated}})
						} else {
							emit([]any{map[string]any{"id": 12, "sha": sha}})
						}
						return
					}
					if provider == "github" {
						emit([]any{map[string]any{"id": 9, "state": "success", "environment": "staging", "created_at": updated, "updated_at": updated}, map[string]any{"id": 8, "state": "failure", "environment": "staging", "created_at": updated.Add(-time.Minute), "updated_at": updated.Add(-time.Minute)}})
					} else {
						emit(map[string]any{"id": 12, "sha": sha, "status": "success", "environment": map[string]any{"id": 7, "name": "staging"}, "updated_at": updated})
					}
				}))
				defer server.Close()
				p, err := NewProvider(target, "fixture")
				if err != nil {
					t.Fatal(err)
				}
				p.base = server.URL
				p.http.Transport = server.Client().Transport
				b := &callBudget{provider: p, check: func(context.Context) error { return nil }}
				if provider == "github" {
					p.githubDeployments(context.Background(), b, &observed)
				} else {
					p.gitlabDeployments(context.Background(), b, &observed)
				}
				switch mode {
				case "success", "truncated":
					if len(observed.Deployments) != 1 {
						t.Fatalf("missing deployment %+v", observed)
					}
					d := observed.Deployments[0]
					if d.Commit != commit || d.CommitRelation != "published_head" || d.RepositoryID != "repo" || d.Environment != "staging" || d.ProviderProfile != target.Profile || d.State != "success" || !d.ProviderUpdatedAt.Equal(updated) {
						t.Fatalf("unbound observation %+v", d)
					}
					if mode == "truncated" && (!observed.DeploymentsTruncated || observed.Deployment != "truncated") {
						t.Fatal("truncation concealed")
					}
				case "missing":
					if observed.Deployment != "not_observed" || len(observed.Deployments) != 0 {
						t.Fatal("absence became deployment")
					}
				case "wrong-commit":
					if len(observed.Deployments) != 0 {
						t.Fatal("unrelated deployment attached")
					}
				case "unavailable":
					if observed.Deployment != "unavailable" {
						t.Fatal("unavailable concealed")
					}
				}
				if observed.ProductionOutcome != "not_observed" {
					t.Fatal("deployment inferred production outcome")
				}
			})
		}
	}
}
