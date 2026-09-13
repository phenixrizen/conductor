package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

const testCommit = "1111111111111111111111111111111111111111"
const testRoot = "2222222222222222222222222222222222222222"
const testSub = "3333333333333333333333333333333333333333"
const tokenCanary = "synthetic-provider-token-canary"

type fixtureFile struct {
	path, mode string
	data       []byte
}
type fixture struct {
	t        *testing.T
	provider string
	server   *httptest.Server
	files    []fixtureFile
	mu       sync.Mutex
	requests []string
	override func(http.ResponseWriter, *http.Request) bool
}

func newFixture(t *testing.T, provider string, files []fixtureFile) (*fixture, *Collector) {
	t.Helper()
	f := &fixture{t: t, provider: provider, files: files}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	cfg := Config{Binding: Binding{Provider: provider, Host: provider + ".com", ProviderID: "42"}, Token: tokenCanary, HTTPClient: f.server.Client(), APIOrigin: f.server.URL, AllowInsecureLoopback: true}
	if provider == "github" {
		cfg.Binding.Locator, cfg.Binding.Profile = "example/synthetic", GitHubProfile
	} else {
		cfg.Binding.Profile = GitLabProfile
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}
func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	if r.Method != http.MethodGet || r.ProtoMajor != 1 || !r.Close || r.Header.Get("Cookie") != "" || strings.Contains(r.URL.String(), tokenCanary) {
		f.t.Error("unexpected method, cookie, or query credential")
	}
	if f.provider == "github" {
		if r.Header.Get("Authorization") != "Bearer "+tokenCanary || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" || r.Header.Get("Accept") != "application/vnd.github+json" {
			f.t.Error("incorrect GitHub headers")
		}
	} else if r.Header.Get("PRIVATE-TOKEN") != tokenCanary || r.Header.Get("Authorization") != "" {
		f.t.Error("incorrect GitLab headers")
	}
	w.Header().Set("Content-Type", "application/json")
	if f.override != nil && f.override(w, r) {
		return
	}
	base := "/repos/example/synthetic"
	if f.provider == "gitlab" {
		base = "/api/v4/projects/42"
	}
	p := strings.TrimPrefix(r.URL.Path, base)
	switch {
	case p == "":
		writeJSON(w, map[string]any{"id": 42})
	case p == "/git/commits/"+testCommit:
		writeJSON(w, map[string]any{"sha": testCommit, "tree": map[string]string{"sha": testRoot}})
	case p == "/repository/commits/"+testCommit:
		if r.URL.Query().Get("stats") != "false" {
			f.t.Error("missing bounded commit options")
		}
		writeJSON(w, map[string]string{"id": testCommit})
	case strings.HasPrefix(p, "/git/trees/"):
		if r.URL.RawQuery != "" {
			f.t.Error("GitHub tree must omit recursive parameter")
		}
		dir := ""
		id := strings.TrimPrefix(p, "/git/trees/")
		if id == testSub {
			dir = "folder%?#[]*"
		} else if id != testRoot {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{"sha": id, "truncated": false, "tree": f.entries(dir)})
	case p == "/repository/tree":
		q := r.URL.Query()
		if q.Get("ref") != testCommit || q.Get("recursive") != "false" || q.Get("pagination") != "keyset" || q.Get("per_page") != "100" {
			f.t.Error("incorrect pinned tree query")
		}
		writeJSON(w, f.entries(q.Get("path")))
	case strings.HasPrefix(p, "/git/blobs/") || strings.HasPrefix(p, "/repository/blobs/"):
		id := p[strings.LastIndex(p, "/")+1:]
		for _, file := range f.files {
			if gitBlobID(file.data) == id {
				writeJSON(w, map[string]any{"sha": id, "size": len(file.data), "encoding": "base64", "content": base64.StdEncoding.EncodeToString(file.data)})
				return
			}
		}
		http.NotFound(w, r)
	default:
		f.t.Errorf("unexpected provider path %q", p)
		http.NotFound(w, r)
	}
}
func (f *fixture) entries(directory string) []map[string]any {
	entries := []map[string]any{}
	directories := make(map[string]bool)
	for _, file := range f.files {
		path := file.path
		if directory != "" {
			var ok bool
			path, ok = strings.CutPrefix(path, directory+"/")
			if !ok {
				continue
			}
		}
		name, _, nested := strings.Cut(path, "/")
		mode, kind, id := file.mode, "blob", gitBlobID(file.data)
		if mode == "" {
			mode = "100644"
		}
		if nested {
			if directories[name] {
				continue
			}
			directories[name] = true
			mode, kind, id = "040000", "tree", testSub
		}
		if mode == "160000" {
			kind = "commit"
		}
		fullPath := name
		if directory != "" {
			fullPath = directory + "/" + name
		}
		if f.provider == "github" {
			entry := map[string]any{"path": name, "mode": mode, "type": kind, "sha": id}
			if kind == "blob" {
				entry["size"] = len(file.data)
			}
			entries = append(entries, entry)
		} else {
			entries = append(entries, map[string]any{"name": name, "path": fullPath, "mode": mode, "type": kind, "id": id})
		}
	}
	return entries
}
func writeJSON(w http.ResponseWriter, value any) { _ = json.NewEncoder(w).Encode(value) }
func allow(context.Context) error                { return nil }
func mustCollect(t *testing.T, c *Collector, paths []string) []domain.ContextArtifact {
	t.Helper()
	got, err := c.Collect(context.Background(), testCommit, paths, allow)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func assertError(t *testing.T, err error, code string, retryable bool) *Error {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) || got.Code != code || got.Retryable != retryable {
		t.Fatalf("error = %v, want %s retryable=%v", err, code, retryable)
	}
	if strings.Contains(err.Error(), tokenCanary) || len(err.Error()) > 100 {
		t.Fatal("unbounded sensitive error")
	}
	return got
}

func TestProviderLiteralPathsAndCoverage(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			files := []fixtureFile{
				{"folder%?#[]*/source%?#[]*.go", "100755", []byte("synthetic source\r\n\tvalue\n")},
				{"binary", "", []byte{0, 1, 2}}, {"bad-utf8", "", []byte{255}}, {"link", "120000", []byte("target")}, {"submodule", "160000", []byte("ignored")},
				{"lfs", "", []byte("version https://git-lfs.github.com/spec/v1\noid sha256:synthetic\nsize 99\n")},
				{"empty", "", nil}, {"large", "", []byte(strings.Repeat("x", domain.MaxContextArtifactBytes+1))},
			}
			f, c := newFixture(t, provider, files)
			paths := []string{"submodule", "missing", "link/child", "link", "lfs", "large", "folder%?#[]*/source%?#[]*.go", "folder%?#[]*/missing", "empty", "binary", "bad-utf8"}
			got := mustCollect(t, c, paths)
			sort.Strings(paths)
			for i, a := range got {
				if a.Path != paths[i] {
					t.Fatal("paths not sorted")
				}
				expected := "unavailable"
				switch a.Path {
				case "missing", "folder%?#[]*/missing":
					expected = "missing"
				case "lfs", "empty", "folder%?#[]*/source%?#[]*.go":
					expected = "collected"
				case "large":
					expected = "truncated"
				}
				if a.State != expected {
					t.Fatalf("%s = %+v, want %s", a.Path, a, expected)
				}
				if expected != "collected" && (a.Text != nil || a.Digest != "") {
					t.Fatal("partial text retained")
				}
				if a.Path == "lfs" && !strings.Contains(a.Message, "pointer text") {
					t.Fatal("unlabelled LFS pointer")
				}
				if a.Path == "folder%?#[]*/source%?#[]*.go" && *a.Text != string(files[0].data) {
					t.Fatal("source bytes changed")
				}
			}
			if err := domain.ValidateRepositoryContext(domain.RepositoryContext{SchemaVersion: 1, Repository: "synthetic", RequestedRef: testCommit, Commit: testCommit, Collector: domain.RepositoryContextCollector, CollectedAt: time.Now().UTC(), Artifacts: got}); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, request := range f.requests {
				if strings.Contains(request, "/contents/") || strings.Contains(request, "/raw") {
					t.Fatal("unexpected alternate content endpoint")
				}
			}
		})
	}
}

func TestProviderIdentityAndObjectBinding(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, kind := range []string{"identity", "quoted_identity", "commit", "blob_sha", "blob_bytes", "blob_size", "blob_encoding", "blob_base64"} {
			t.Run(provider+"/"+kind, func(t *testing.T) {
				f, c := newFixture(t, provider, []fixtureFile{{"source", "", []byte("source-canary")}})
				f.override = func(w http.ResponseWriter, r *http.Request) bool {
					p := r.URL.Path
					if strings.HasSuffix(p, "synthetic") || strings.HasSuffix(p, "/42") {
						if kind == "identity" {
							writeJSON(w, map[string]any{"id": 43})
							return true
						}
						if kind == "quoted_identity" {
							writeJSON(w, map[string]any{"id": "42"})
							return true
						}
					}
					if strings.Contains(p, "/commits/") && kind == "commit" {
						writeJSON(w, map[string]any{"sha": testSub, "id": testSub, "tree": map[string]string{"sha": testRoot}})
						return true
					}
					if strings.Contains(p, "/blobs/") {
						doc := map[string]any{"sha": gitBlobID([]byte("source-canary")), "size": 13, "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("source-canary"))}
						switch kind {
						case "blob_sha":
							doc["sha"] = testSub
						case "blob_bytes":
							doc["content"] = base64.StdEncoding.EncodeToString([]byte("forged-canary"))
						case "blob_size":
							doc["size"] = 12
						case "blob_encoding":
							doc["encoding"] = "utf-8"
						case "blob_base64":
							doc["content"] = "not-base64!"
						default:
							return false
						}
						writeJSON(w, doc)
						return true
					}
					return false
				}
				got, err := c.Collect(context.Background(), testCommit, []string{"source"}, allow)
				if kind == "identity" || kind == "quoted_identity" {
					assertError(t, err, "identity_mismatch", false)
				} else if kind == "commit" {
					assertError(t, err, "object_mismatch", false)
				} else if err != nil || len(got) != 1 || got[0].State != "unavailable" || got[0].Text != nil {
					t.Fatalf("invalid object became evidence: %+v %v", got, err)
				}
			})
		}
	}
}

func TestGitHubRechecksLocatorBeforeAcceptingText(t *testing.T) {
	f, c := newFixture(t, "github", []fixtureFile{{"source", "", []byte("private-source-canary")}})
	lookups := 0
	f.override = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/repos/example/synthetic" {
			lookups++
			if lookups == 2 {
				writeJSON(w, map[string]int{"id": 43})
				return true
			}
		}
		return false
	}
	got, err := c.Collect(context.Background(), testCommit, []string{"source"}, allow)
	assertError(t, err, "identity_mismatch", false)
	if got != nil || lookups != 2 {
		t.Fatal("rebound locator retained source")
	}
}

func TestAuthorizationCheckedBeforeEveryRequestAndRetryStartsFresh(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			f, c := newFixture(t, provider, []fixtureFile{{"source", "", []byte("private-source-canary")}})
			checks := 0
			got, err := c.Collect(context.Background(), testCommit, []string{"source"}, func(context.Context) error {
				checks++
				if checks == 4 {
					return fmt.Errorf("%s: %w", tokenCanary, domain.ErrForbidden)
				}
				return nil
			})
			assertError(t, err, "access_revoked", false)
			if got != nil || checks != 4 || len(f.requests) != 3 {
				t.Fatal("provider request after revoked authorization")
			}
			checks = 0
			_, err = c.Collect(context.Background(), testCommit, []string{"source"}, func(context.Context) error { checks++; return nil })
			if err != nil || checks != len(f.requests)-3 {
				t.Fatalf("not every new provider operation was checked: %d %d %v", checks, len(f.requests), err)
			}
		})
	}
}

func TestRedirectRateLimitsAndSanitizedErrors(t *testing.T) {
	var escaped atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { escaped.Add(1) }))
	defer target.Close()
	for _, provider := range []string{"github", "gitlab"} {
		for _, status := range []int{301, 302, 307, 308, 401, 403, 404, 429, 500, 503} {
			t.Run(fmt.Sprintf("%s/%d", provider, status), func(t *testing.T) {
				f, c := newFixture(t, provider, nil)
				f.override = func(w http.ResponseWriter, r *http.Request) bool {
					w.Header().Set("Location", target.URL+"/"+tokenCanary)
					if status == 429 {
						w.Header().Set("Retry-After", "999999999999")
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(tokenCanary))
					return true
				}
				_, err := c.Collect(context.Background(), testCommit, []string{"source"}, allow)
				code, retryable := "redirect", false
				switch status {
				case 401, 403:
					code = "provider_denied"
				case 404:
					code = "not_found"
				case 429:
					code, retryable = "rate_limited", true
				case 500, 503:
					code, retryable = "provider_unavailable", true
				}
				e := assertError(t, err, code, retryable)
				if status == 429 && e.RetryAfter != 5*time.Minute {
					t.Fatal("retry delay not bounded")
				}
				if len(f.requests) != 1 {
					t.Fatal("adapter retried internally")
				}
			})
		}
	}
	if escaped.Load() != 0 {
		t.Fatal("redirect target received credentials")
	}
}

func TestGitLabPaginationBindingAndCompleteness(t *testing.T) {
	for _, kind := range []string{"valid", "external", "commit", "directory", "duplicate_query", "unknown_query", "repeated_cursor", "duplicate_entry", "too_many", "offset", "404", "null"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newFixture(t, "gitlab", []fixtureFile{{"source", "", []byte("source")}})
			pages := 0
			f.override = func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasSuffix(r.URL.Path, "/repository/tree") {
					return false
				}
				pages++
				if kind == "404" {
					http.NotFound(w, r)
					return true
				}
				if kind == "null" {
					writeJSON(w, nil)
					return true
				}
				q := r.URL.Query()
				entries := []map[string]any{}
				if pages == 1 || kind == "repeated_cursor" || kind == "duplicate_entry" || kind == "too_many" {
					entries = append(entries, map[string]any{"id": testSub, "name": "other", "path": "other", "mode": "100644", "type": "blob"})
					if kind == "too_many" {
						entries = nil
						for i := 0; i < 100; i++ {
							name := fmt.Sprintf("entry%04d", (pages-1)*100+i)
							entries = append(entries, map[string]any{"id": testSub, "name": name, "path": name, "mode": "100644", "type": "blob"})
						}
					}
					q.Set("page_token", fmt.Sprint(pages))
					if kind == "repeated_cursor" {
						q.Set("page_token", "same")
					}
					u := f.server.URL + r.URL.Path
					switch kind {
					case "external":
						u = "https://example.invalid" + r.URL.Path
					case "commit":
						q.Set("ref", testSub)
					case "directory":
						q.Set("path", "other")
					case "duplicate_query":
						q.Add("ref", testCommit)
					case "unknown_query":
						q.Set("token", tokenCanary)
					}
					w.Header().Set("Link", "<"+u+"?"+q.Encode()+">; rel=\"next\"")
				} else {
					entries = f.entries("")
				}
				if kind == "offset" {
					w.Header().Del("Link")
					w.Header().Set("X-Next-Page", "2")
				}
				writeJSON(w, entries)
				return true
			}
			got := mustCollect(t, c, []string{"source"})
			want := "unavailable"
			if kind == "valid" {
				want = "collected"
			}
			if got[0].State != want {
				t.Fatalf("%+v", got)
			}
			if pages > 10 {
				t.Fatal("directory pagination bound exceeded")
			}
		})
	}
}

func TestIncompleteTreeNeverEstablishesMissing(t *testing.T) {
	for _, kind := range []string{"truncated", "missing_field", "oversized", "duplicate", "bad_path", "wrong_tree", "404"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newFixture(t, "github", nil)
			f.override = func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.Contains(r.URL.Path, "/trees/") {
					return false
				}
				if kind == "404" {
					http.NotFound(w, r)
					return true
				}
				doc := map[string]any{"sha": testRoot, "truncated": false, "tree": []any{}}
				switch kind {
				case "truncated":
					doc["truncated"] = true
				case "missing_field":
					delete(doc, "tree")
				case "oversized":
					doc["extra"] = strings.Repeat("x", maxResponseBytes)
				case "wrong_tree":
					doc["sha"] = testSub
				case "duplicate", "bad_path":
					name := "other"
					if kind == "bad_path" {
						name = "other/nested"
					}
					entry := map[string]any{"sha": testSub, "path": name, "mode": "040000", "type": "tree"}
					rows := []any{entry}
					if kind == "duplicate" {
						rows = append(rows, entry)
					}
					doc["tree"] = rows
				}
				writeJSON(w, doc)
				return true
			}
			got := mustCollect(t, c, []string{"missing"})
			if got[0].State != "unavailable" || got[0].Text != nil {
				t.Fatalf("incomplete tree claimed absence: %+v", got)
			}
		})
	}
}

func TestSortedTotalTextBudget(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			files := []fixtureFile{}
			for _, name := range []string{"a", "b", "c", "d", "e"} {
				files = append(files, fixtureFile{name, "", []byte(strings.Repeat(name, domain.MaxContextArtifactBytes))})
			}
			_, c := newFixture(t, provider, files)
			got := mustCollect(t, c, []string{"e", "d", "c", "b", "a"})
			for i, a := range got {
				want := "collected"
				if i == 4 {
					want = "truncated"
				}
				if a.State != want {
					t.Fatalf("%+v", a)
				}
			}
		})
	}
}

func TestInvalidInputsNeverContactProvider(t *testing.T) {
	f, c := newFixture(t, "github", nil)
	for _, commit := range []string{"main", "1234", strings.Repeat("a", 64), strings.Repeat("A", 40)} {
		_, err := c.Collect(context.Background(), commit, []string{"source"}, allow)
		assertError(t, err, "invalid_input", false)
	}
	for _, paths := range [][]string{nil, {"../source"}, {"/source"}, {"a/../b"}, {"a\\b"}, {"a", "a"}, {"-file"}, make([]string, 33)} {
		_, err := c.Collect(context.Background(), testCommit, paths, allow)
		assertError(t, err, "invalid_input", false)
	}
	_, err := c.Collect(context.Background(), testCommit, []string{"source"}, nil)
	assertError(t, err, "invalid_input", false)
	if len(f.requests) != 0 {
		t.Fatal("invalid input contacted provider")
	}
}

func TestConfigurationBoundary(t *testing.T) {
	base := Config{Binding: Binding{Provider: "github", Host: "github.com", ProviderID: "42", Locator: "example/synthetic", Profile: GitHubProfile}, Token: tokenCanary}
	for _, kind := range []string{"host", "profile", "id", "locator", "token", "origin", "origin_path", "origin_user", "origin_nonliteral", "cookies", "tls_skip"} {
		t.Run(kind, func(t *testing.T) {
			cfg := base
			switch kind {
			case "host":
				cfg.Binding.Host = "github.example.com"
			case "profile":
				cfg.Binding.Profile = "latest"
			case "id":
				cfg.Binding.ProviderID = "042"
			case "locator":
				cfg.Binding.Locator = "other/../../secret"
			case "token":
				cfg.Token += "\n"
			case "origin":
				cfg.APIOrigin = "https://example.com"
			case "origin_path":
				cfg.APIOrigin, cfg.AllowInsecureLoopback = "http://127.0.0.1:1234/api/v4", true
			case "origin_user":
				cfg.APIOrigin, cfg.AllowInsecureLoopback = "http://user@127.0.0.1:1234", true
			case "origin_nonliteral":
				cfg.APIOrigin, cfg.AllowInsecureLoopback = "http://localhost:1234", true
			case "cookies":
				jar, _ := cookiejar.New(nil)
				cfg.HTTPClient = &http.Client{Jar: jar}
			case "tls_skip":
				server := httptest.NewTLSServer(http.NotFoundHandler())
				defer server.Close()
				client := server.Client()
				tr := client.Transport.(*http.Transport).Clone()
				tr.TLSClientConfig.InsecureSkipVerify = true
				client.Transport = tr
				cfg.HTTPClient = client
			}
			_, err := New(cfg)
			assertError(t, err, "invalid_config", false)
		})
	}
}

func TestCancellationStopsProviderRead(t *testing.T) {
	f, c := newFixture(t, "github", nil)
	started := make(chan struct{})
	f.override = func(w http.ResponseWriter, r *http.Request) bool { close(started); <-r.Context().Done(); return true }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Collect(ctx, testCommit, []string{"source"}, allow); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		assertError(t, err, "cancelled", false)
	case <-time.After(time.Second):
		t.Fatal("HTTP read ignored cancellation")
	}
}

func TestRequestAndResponseBudgets(t *testing.T) {
	f, c := newFixture(t, "github", nil)
	for _, kind := range []string{"requests", "bytes"} {
		t.Run(kind, func(t *testing.T) {
			s := &collection{c: c, authorize: allow}
			if kind == "requests" {
				s.requests = maxRequests - 1
			} else {
				s.responseBytes = maxTotalResponseBytes - maxResponseBytes
			}
			var doc map[string]any
			_, err := s.get(context.Background(), c.prefix, &doc)
			want := "request_limit"
			if kind == "bytes" {
				want = "response_limit"
			}
			assertError(t, err, want, false)
			s.finalIdentity = true
			if err := s.identity(context.Background()); err != nil {
				t.Fatal("final identity capacity not preserved", err)
			}
		})
	}
	if len(f.requests) != 2 {
		t.Fatal("budget exhaustion made provider request")
	}
}

func TestGitHubSecondaryRateLimitAndDeadline(t *testing.T) {
	h := http.Header{"Retry-After": {"2"}}
	e := statusFailure(403, h)
	if !e.Retryable || e.Code != "rate_limited" || e.RetryAfter != 2*time.Second {
		t.Fatalf("%+v", e)
	}
	_, c := newFixture(t, "github", nil)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := c.Collect(ctx, testCommit, []string{"source"}, allow)
	assertError(t, err, "deadline", false)
}

func TestGitLabLiteralCursorIsReencoded(t *testing.T) {
	f, c := newFixture(t, "gitlab", nil)
	s := &collection{c: c}
	q := url.Values{"ref": {testCommit}, "pagination": {"keyset"}, "recursive": {"false"}, "per_page": {"100"}}
	q.Set("page_token", "cursor%?#[]*")
	headers := http.Header{"Link": {"<" + f.server.URL + c.prefix + "/repository/tree?" + q.Encode() + ">; rel=\"next\""}}
	q.Del("page_token")
	got, err := s.nextPage(headers, c.prefix+"/repository/tree", q)
	if err != nil || got != "cursor%?#[]*" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestDeepTraversalStopsAtRequestBudget(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			f, c := newFixture(t, provider, nil)
			depth := 0
			f.override = func(w http.ResponseWriter, r *http.Request) bool {
				if provider == "github" && strings.Contains(r.URL.Path, "/trees/") {
					depth++
					id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					writeJSON(w, map[string]any{"sha": id, "truncated": false, "tree": []any{map[string]any{"sha": fmt.Sprintf("%040x", depth), "path": "d", "mode": "040000", "type": "tree"}}})
					return true
				}
				if provider == "gitlab" && strings.HasSuffix(r.URL.Path, "/repository/tree") {
					path := r.URL.Query().Get("path")
					if path != "" {
						path += "/"
					}
					writeJSON(w, []any{map[string]any{"id": testSub, "name": "d", "path": path + "d", "mode": "040000", "type": "tree"}})
					return true
				}
				return false
			}
			checks := 0
			got, err := c.Collect(context.Background(), testCommit, []string{strings.Repeat("d/", 130) + "source"}, func(context.Context) error { checks++; return nil })
			if err != nil || len(got) != 1 || got[0].State != "unavailable" {
				t.Fatalf("deep traversal = %+v, %v", got, err)
			}
			if checks != 128 || len(f.requests) != 128 {
				t.Fatalf("provider requests/checks = %d/%d", len(f.requests), checks)
			}
		})
	}
}

func TestHTTPResponseAndHeaderBounds(t *testing.T) {
	for _, kind := range []string{"body", "headers", "encoding", "invalid_utf8"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newFixture(t, "gitlab", nil)
			f.override = func(w http.ResponseWriter, r *http.Request) bool {
				switch kind {
				case "body":
					writeJSON(w, map[string]any{"id": 42, "extra": strings.Repeat("x", maxResponseBytes)})
				case "headers":
					w.Header().Set("X-Large", strings.Repeat("x", 33<<10))
					writeJSON(w, map[string]any{"id": 42})
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
					writeJSON(w, map[string]any{"id": 42})
				case "invalid_utf8":
					_, _ = w.Write(append([]byte(`{"id":42,"extra":"`), 255, '"', '}'))
				}
				return true
			}
			_, err := c.Collect(context.Background(), testCommit, []string{"source"}, allow)
			code, retry := "invalid_response", false
			if kind == "body" {
				code = "response_limit"
			}
			if kind == "headers" {
				code, retry = "transport", true
			}
			assertError(t, err, code, retry)
		})
	}
}

func TestTransientFailureDiscardsEarlierArtifacts(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			files := []fixtureFile{{"a", "", []byte("source-first-canary")}, {"b", "", []byte("source-second-canary")}}
			f, c := newFixture(t, provider, files)
			f.override = func(w http.ResponseWriter, r *http.Request) bool {
				if strings.Contains(r.URL.Path, "/blobs/") && strings.HasSuffix(r.URL.Path, gitBlobID(files[1].data)) {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(tokenCanary))
					return true
				}
				return false
			}
			got, err := c.Collect(context.Background(), testCommit, []string{"a", "b"}, allow)
			assertError(t, err, "provider_unavailable", true)
			if got != nil {
				t.Fatal("retryable failure retained a final partial observation")
			}
		})
	}
}

func TestAuthorizationOutageIsRetryableWithoutProviderIO(t *testing.T) {
	f, c := newFixture(t, "github", nil)
	got, err := c.Collect(context.Background(), testCommit, []string{"source"}, func(context.Context) error { return errors.New(tokenCanary) })
	assertError(t, err, "authorization_unavailable", true)
	if got != nil || len(f.requests) != 0 {
		t.Fatal("authorization outage allowed provider I/O")
	}
}

func TestStoppedOrUnknownCollectionPreventsProviderIO(t *testing.T) {
	for _, reason := range []error{domain.ErrCollectionStopped, domain.ErrNotFound, domain.ErrUnauthenticated} {
		t.Run(reason.Error(), func(t *testing.T) {
			f, c := newFixture(t, "gitlab", nil)
			got, err := c.Collect(context.Background(), testCommit, []string{"source"}, func(context.Context) error { return fmt.Errorf("%s: %w", tokenCanary, reason) })
			assertError(t, err, "access_revoked", false)
			if got != nil || len(f.requests) != 0 {
				t.Fatal("stopped or inaccessible collection allowed provider I/O")
			}
		})
	}
}

func TestOrdinaryTransientStatusUsesTemporalBackoffUnlessProviderSetsDelay(t *testing.T) {
	for _, status := range []int{408, 500, 502, 503} {
		for _, row := range []struct {
			value string
			delay time.Duration
		}{{"", 0}, {"2", 2 * time.Second}, {"90", 90 * time.Second}} {
			t.Run(fmt.Sprintf("%d/retry-after=%s", status, row.value), func(t *testing.T) {
				headers := http.Header{}
				if row.value != "" {
					headers.Set("Retry-After", row.value)
				}
				got := statusFailure(status, headers)
				if got.Code != "provider_unavailable" || !got.Retryable || got.RetryAfter != row.delay {
					t.Fatalf("transient provider policy changed: %+v", got)
				}
			})
		}
	}
}

func TestGitLabMalformedOrUnsupportedLinkCannotEstablishMissing(t *testing.T) {
	for _, relation := range []string{`title="next"`, `rel="prev"`, `rel=""`, `rel="next`, `rel="next prev"`, `rel="next"; rel="next"`} {
		t.Run(relation, func(t *testing.T) {
			f, c := newFixture(t, "gitlab", nil)
			f.override = func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasSuffix(r.URL.Path, "/repository/tree") {
					return false
				}
				query := r.URL.Query()
				query.Set("page_token", "next-page")
				w.Header().Set("Link", "<"+f.server.URL+r.URL.Path+"?"+query.Encode()+">; "+relation)
				writeJSON(w, []any{})
				return true
			}
			got := mustCollect(t, c, []string{"source.txt"})
			if got[0].State != "unavailable" || got[0].Text != nil {
				t.Fatalf("unsupported pagination metadata established absence: %+v", got)
			}
		})
	}
}
