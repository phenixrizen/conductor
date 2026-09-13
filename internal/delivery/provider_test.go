package delivery

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func gitBlob(data []byte) string {
	hash := sha1.New()
	fmt.Fprintf(hash, "blob %d%c", len(data), 0)
	hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}
func TestProvidersPublishExactDraftAndReconcileLostAcknowledgment(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			bundle, patch := fixturePatch(t)
			prepared, err := Prepare(context.Background(), bundle, patch)
			if err != nil {
				t.Fatal(err)
			}
			target := domain.DeliveryTarget{WorkspaceID: "workspace", RepositoryID: "repo", Provider: provider, Host: provider + ".com", ProviderID: "42", IntegrationVersion: 1, Profile: domain.GitHubDeliveryProfile, Locator: "synthetic/repo"}
			if provider == "gitlab" {
				target.Profile = domain.GitLabDeliveryProfile
				target.Locator = ""
			}
			d := domain.Delivery{ID: strings.Repeat("a", 32), Digest: strings.Repeat("b", 64), Input: domain.DeliveryInput{BaseBranch: "main", Title: "Verified fixture", ArtifactDigest: strings.Repeat("c", 64)}, Target: target, BaseCommit: prepared.BaseCommit, BaseTree: prepared.BaseTree, ResultTree: prepared.ResultTree, PatchDigest: prepared.PatchDigest, Branch: "conductor/publication/" + strings.Repeat("a", 32), CreatedAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)}
			sha := strings.Repeat("d", 40)
			branch, review := false, false
			posts, reviews := 0, 0
			lose := true
			wrongTree := false
			commit := func() map[string]any {
				tree := d.ResultTree
				if wrongTree {
					tree = strings.Repeat("f", 40)
				}
				return map[string]any{"sha": sha, "id": sha, "message": commitMessage(d), "tree": map[string]string{"sha": tree}, "parents": []any{map[string]string{"sha": d.BaseCommit}}, "parent_ids": []string{d.BaseCommit}}
			}
			pull := func() map[string]any {
				return map[string]any{"id": 99, "number": 7, "iid": 7, "project_id": 42, "source_project_id": 42, "target_project_id": 42, "source_branch": d.Branch, "target_branch": "main", "sha": sha, "description": body(d), "body": body(d), "html_url": "https://github.com/synthetic/repo/pull/7", "web_url": "https://gitlab.com/synthetic/repo/-/merge_requests/7", "state": map[string]string{"github": "open", "gitlab": "opened"}[provider], "draft": true, "head": map[string]any{"ref": d.Branch, "sha": sha, "repo": map[string]int{"id": 42}}, "base": map[string]any{"ref": "main", "repo": map[string]int{"id": 42}}}
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if provider == "github" && (r.Header.Get("Authorization") != "Bearer fixture-secret" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10") {
					t.Error("missing GitHub credential/version binding")
				}
				if provider == "gitlab" && r.Header.Get("PRIVATE-TOKEN") != "fixture-secret" {
					t.Error("missing GitLab credential")
				}
				if r.Method != "GET" && r.Method != "POST" {
					t.Errorf("forbidden mutation %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				var in map[string]any
				if r.Method == "POST" {
					posts++
					if json.NewDecoder(r.Body).Decode(&in) != nil {
						t.Error("invalid JSON command")
					}
				}
				emit := func(v any) {
					if err := json.NewEncoder(w).Encode(v); err != nil {
						t.Error(err)
					}
				}
				path := r.URL.Path
				switch {
				case path == "":
					emit(map[string]any{"id": 42, "full_name": "synthetic/repo"})
				case path == "/":
					emit(map[string]any{"id": 42, "full_name": "synthetic/repo"})
				case strings.HasPrefix(path, "/git/ref/heads/") || strings.HasPrefix(path, "/repository/branches/"):
					name := strings.TrimPrefix(strings.TrimPrefix(path, "/git/ref/heads/"), "/repository/branches/")
					value := d.BaseCommit
					if name == d.Branch {
						if !branch {
							w.WriteHeader(404)
							return
						}
						value = sha
					}
					emit(map[string]any{"ref": "refs/heads/" + name, "name": name, "object": map[string]string{"type": "commit", "sha": value}, "commit": map[string]string{"id": value}})
				case path == "/git/blobs":
					raw, err := base64.StdEncoding.DecodeString(in["content"].(string))
					if err != nil {
						t.Error(err)
					}
					emit(map[string]string{"sha": gitBlob(raw)})
				case path == "/git/trees":
					if in["base_tree"] != d.BaseTree {
						t.Error("wrong base tree")
					}
					emit(map[string]string{"sha": d.ResultTree})
				case path == "/git/commits" || strings.HasPrefix(path, "/git/commits/"):
					emit(commit())
				case path == "/git/refs":
					if in["ref"] != "refs/heads/"+d.Branch || in["sha"] != sha {
						t.Error("wrong create ref")
					}
					branch = true
					emit(map[string]any{"ref": "refs/heads/" + d.Branch, "object": map[string]string{"type": "commit", "sha": sha}})
				case path == "/repository/commits":
					if in["start_sha"] != d.BaseCommit || in["force"] != false || in["branch"] != d.Branch || len(in["actions"].([]any)) != 4 {
						t.Errorf("unsafe GitLab commit %#v", in)
					}
					if branch {
						t.Error("replayed GitLab commit")
					}
					branch = true
					emit(commit())
				case strings.HasPrefix(path, "/repository/commits/"):
					emit(commit())
				case path == "/repository/tree":
					entries := []gitlabTreeEntry{{ID: gitBlob([]byte("kept\n")), Name: "keep.txt", Path: "keep.txt", Mode: "100644", Type: "blob"}}
					for _, f := range prepared.Files {
						if f.Mode != "000000" {
							entries = append(entries, gitlabTreeEntry{ID: f.Blob, Name: f.Path, Path: f.Path, Mode: f.Mode, Type: "blob"})
						}
					}
					if wrongTree {
						entries[0].ID = strings.Repeat("f", 40)
					}
					emit(entries)
				case path == "/pulls" || path == "/merge_requests":
					if r.Method == "GET" {
						if review {
							emit([]any{pull()})
						} else {
							emit([]any{})
						}
						return
					}
					reviews++
					if provider == "github" && in["draft"] != true {
						t.Error("non-draft GitHub creation")
					}
					if provider == "gitlab" && !strings.HasPrefix(in["title"].(string), "Draft: ") {
						t.Error("non-draft GitLab creation")
					}
					review = true
					if lose {
						lose = false
						w.WriteHeader(500)
						return
					}
					emit(pull())
				case path == "/pulls/7" || path == "/merge_requests/7":
					emit(pull())
				case strings.HasSuffix(path, "/check-runs"):
					emit(map[string]any{"total_count": 0, "check_runs": []any{}})
				case strings.HasSuffix(path, "/status"):
					emit(map[string]any{"sha": sha, "total_count": 0, "statuses": []any{}})
				case path == "/deployments":
					emit([]any{})
				case path == "/pipelines":
					emit([]any{})
				default:
					t.Errorf("unexpected provider request %s %s", r.Method, r.URL)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			p, err := NewProvider(target, "fixture-secret")
			if err != nil {
				t.Fatal(err)
			}
			p.base = server.URL
			p.http.Transport = server.Client().Transport
			calls := 0
			check := func(context.Context) error { calls++; return nil }
			if _, err = p.Publish(context.Background(), d, prepared, check); err != ErrUnknown {
				t.Fatalf("lost acknowledgment concealed: %v", err)
			}
			firstPosts := posts
			observed, err := p.Publish(context.Background(), d, prepared, check)
			if err != nil {
				t.Fatal(err)
			}
			if posts != firstPosts || reviews != 1 || observed.State != "draft" || observed.ChecksState != "unknown" || observed.Tree != d.ResultTree || calls == 0 {
				t.Fatalf("bad reconciliation: posts %d/%d reviews %d observation %+v", posts, firstPosts, reviews, observed)
			}
			d.Observation = &observed
			wrongTree = true
			if _, err = p.Observe(context.Background(), d, check); err != ErrMismatch {
				t.Fatalf("accepted changed tree %v", err)
			}
		})
	}
}
func TestProviderRejectsRedirectAndRevokedAccess(t *testing.T) {
	target := domain.DeliveryTarget{WorkspaceID: "workspace", RepositoryID: "repo", Provider: "github", Host: "github.com", ProviderID: "42", Profile: domain.GitHubDeliveryProfile, Locator: "synthetic/repo"}
	p, err := NewProvider(target, "secret")
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; http.Redirect(w, r, "/elsewhere", 307) }))
	defer server.Close()
	p.base = server.URL
	p.http.Transport = server.Client().Transport
	b := &callBudget{provider: p, check: func(context.Context) error { return nil }}
	if _, err = b.call(context.Background(), "POST", "/test", map[string]string{"value": "fixture"}, new(any)); err != ErrProvider || hits != 1 {
		t.Fatalf("redirect replay %d %v", hits, err)
	}
	b.check = func(context.Context) error { return domain.ErrForbidden }
	if _, err = b.call(context.Background(), "GET", "", nil, new(any)); err != domain.ErrForbidden || hits != 1 {
		t.Fatal("revoked request sent")
	}
}

var _ = strconv.Itoa
