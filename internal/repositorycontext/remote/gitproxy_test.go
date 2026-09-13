package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/sourcebundle"
)

func sourceFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("/usr/bin/git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null"}
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture Git: %v %s", err, data)
	}
	return strings.TrimSpace(string(data))
}
func sourceFixture(t *testing.T, provider string) (*Collector, string, string, *atomic.Int32) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	sourceFixtureGit(t, work, "init")
	for p, text := range map[string]string{"fixture.go": "package fixture\nfunc Called(){}\nfunc Caller(){Called()}\n", "README.md": "synthetic source\n", ".codegraph/config.json": "{\"untrusted\":true}\n", "link.txt": "version https://git-lfs.github.com/spec/v1\noid sha256:synthetic\nsize 1\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(work, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, p), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sourceFixtureGit(t, work, "add", ".")
	sourceFixtureGit(t, work, "commit", "-m", "Synthetic source fixture")
	commit := sourceFixtureGit(t, work, "rev-parse", "HEAD")
	tree := sourceFixtureGit(t, work, "rev-parse", "HEAD^{tree}")
	if err := os.Mkdir(filepath.Join(root, "synthetic"), 0700); err != nil {
		t.Fatal(err)
	}
	sourceFixtureGit(t, root, "clone", "--bare", work, filepath.Join(root, "synthetic/source.git"))
	calls := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/synthetic/source.git/") {
			user := "x-access-token"
			if provider == "gitlab" {
				user = "oauth2"
			}
			if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":synthetic-token")) {
				t.Error("Git proxy lost bound authentication")
				w.WriteHeader(401)
				return
			}
			calls.Add(1)
			handler := cgi.Handler{Path: "/usr/bin/git", Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "PATH=/usr/bin:/bin"}}
			handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/synthetic/source" || r.URL.Path == "/api/v4/projects/1" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "clone_url": "https://github.com/synthetic/source.git", "http_url_to_repo": "https://gitlab.com/synthetic/source.git"})
			return
		}
		if strings.Contains(r.URL.Path, "/commits/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": commit, "id": commit, "tree": map[string]string{"sha": tree}})
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(server.Close)
	binding := Binding{Provider: provider, Host: provider + ".com", ProviderID: "1", Profile: GitHubProfile, Locator: "synthetic/source"}
	if provider == "gitlab" {
		binding.Profile = GitLabProfile
		binding.Locator = ""
	}
	collector, err := New(Config{Binding: binding, Token: "synthetic-token", APIOrigin: server.URL, GitOrigin: server.URL, AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return collector, commit, tree, calls
}
func TestFullSourceProxyPreservesExactCommitAndBundleForBothProviders(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			collector, commit, tree, calls := sourceFixture(t, provider)
			checks := 0
			data, err := collector.FullSource(context.Background(), commit, func(context.Context) error { checks++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			if data.Commit != commit || data.Tree != tree || data.FileCount != 4 || len(data.Artifacts) != 4 || sourcebundle.Validate(data) != nil || calls.Load() < 2 || checks < int(calls.Load())+4 {
				t.Fatalf("full source facts: %+v checks%d calls%d", data, checks, calls.Load())
			}
			for _, a := range data.Artifacts {
				if (a.Path == "link.txt" || a.Path == ".codegraph/config.json") && (a.State != "unavailable" || a.Text != nil) {
					t.Fatal("LFS pointer or parser metadata was followed or indexed as source")
				}
			}
			target := t.TempDir()
			bundle := filepath.Join(target, "source.bundle")
			if err = os.WriteFile(bundle, data.Bundle, 0600); err != nil {
				t.Fatal(err)
			}
			sourceFixtureGit(t, target, "clone", bundle, "recovered")
			got := sourceFixtureGit(t, filepath.Join(target, "recovered"), "rev-parse", "HEAD^{tree}")
			if got != tree {
				t.Fatalf("bundle changed commit tree: %s", got)
			}
		})
	}
}
func TestFullSourceProxyRechecksRevocationBeforeGitExchange(t *testing.T) {
	collector, commit, _, calls := sourceFixture(t, "github")
	checks := 0
	_, err := collector.FullSource(context.Background(), commit, func(context.Context) error {
		checks++
		if checks > 2 {
			return domain.ErrForbidden
		}
		return nil
	})
	if err == nil || calls.Load() != 0 {
		t.Fatalf("revoked Git exchange leaked: %v calls%d checks%d", err, calls.Load(), checks)
	}
}

func TestFullSourceLiveGitHub(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_LIVE_GITHUB_SOURCE") != "1" {
		t.Skip("set CONDUCTOR_TEST_LIVE_GITHUB_SOURCE=1 with explicit repository, commit and token file for live GitHub read acceptance")
	}
	token, err := os.ReadFile(os.Getenv("CONDUCTOR_LIVE_GITHUB_TOKEN_FILE"))
	if err != nil {
		t.Fatal("selected token file unavailable")
	}
	c, err := New(Config{Binding: Binding{Provider: "github", Host: "github.com", ProviderID: os.Getenv("CONDUCTOR_LIVE_GITHUB_REPOSITORY_ID"), Locator: os.Getenv("CONDUCTOR_LIVE_GITHUB_LOCATOR"), Profile: GitHubProfile}, Token: strings.TrimSpace(string(token))})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.FullSource(context.Background(), os.Getenv("CONDUCTOR_LIVE_GITHUB_COMMIT"), func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != os.Getenv("CONDUCTOR_LIVE_GITHUB_COMMIT") || sourcebundle.Validate(got) != nil {
		t.Fatal("live source identity or bound validation failed")
	}
	t.Logf("verified exact commit/tree bundle: files=%d bytes=%d index-coverage-truncated=%v", got.FileCount, len(got.Bundle), got.Truncated)
}
