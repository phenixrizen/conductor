package acceptance_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

const releaseReadToken = "synthetic-release-read-credential"
const releasePublishToken = "synthetic-release-publish-credential"

// This fixture serves the actual provider HTTP shapes while Git owns every
// source/blob/tree/commit. It never pretends a placeholder SHA proves a patch.
// Review, checks and deployment records are explicitly synthetic provider data.
type releaseRepository struct {
	t                                                             *testing.T
	ctx                                                           context.Context
	mu                                                            sync.Mutex
	root, bare, provider, repository, providerID, locator, prefix string
	source                                                        releaseBaseline
	server                                                        *httptest.Server
	delivery                                                      domain.Delivery
	published, description                                        string
	review                                                        bool
	posts, postsAtDrop, reviews, branches, gitRequests            int
	dropReview                                                    bool
	deployed                                                      bool
	deploymentAt                                                  time.Time
}
type releaseBaseline struct{ Commit, Tree string }

type releaseTreeEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int    `json:"size"`
}

func newReleaseRepository(t *testing.T, ctx context.Context, provider, repository, providerID string) *releaseRepository {
	t.Helper()
	p := &releaseRepository{t: t, ctx: ctx, provider: provider, repository: repository, providerID: providerID, locator: "synthetic/" + repository, source: releaseBaseline{}, root: t.TempDir(), dropReview: true}
	p.prefix = "/repos/" + p.locator
	if provider == "gitlab" {
		p.prefix = "/api/v4/projects/" + providerID
	}
	p.bare = filepath.Join(p.root, p.locator+".git")
	if err := os.MkdirAll(filepath.Dir(p.bare), 0700); err != nil {
		t.Fatal(err)
	}
	data := bundleFixture(t)
	p.source = releaseBaseline{Commit: data.Commit, Tree: data.Tree}
	bundle := filepath.Join(p.root, "source.bundle")
	if err := os.WriteFile(bundle, data.Bundle, 0600); err != nil {
		t.Fatal(err)
	}
	p.git("", "clone", "--bare", bundle, p.bare)
	manifest := "module example.invalid/" + repository + "\n\ngo 1.24\n"
	if repository == "application" {
		manifest += "require example.invalid/related v1.0.0\n"
	}
	blob := p.git(manifest, "hash-object", "-w", "--stdin")
	tree := p.tree([]releaseTreeEntry{{Path: "go.mod", Mode: "100644", ID: blob}})
	commit := p.git("Synthetic declared cross-repository dependency\n", "commit-tree", tree, "-p", p.source.Commit)
	p.git("", "update-ref", "refs/heads/main", commit, p.source.Commit)
	p.source = releaseBaseline{Commit: commit, Tree: tree}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	return p
}
func (p *releaseRepository) git(input string, args ...string) string {
	p.t.Helper()
	command := exec.CommandContext(p.ctx, "/usr/bin/git", append([]string{"-c", "core.hooksPath=/dev/null", "--git-dir=" + p.bare}, args...)...)
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + p.root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid"}
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		p.t.Errorf("owned fixture Git %s: %v %s", args[0], err, output)
		panic("owned Git fixture failed")
	}
	return strings.TrimSuffix(string(output), "\n")
}
func (p *releaseRepository) entries(tree string) []releaseTreeEntry {
	rows := strings.Split(p.git("", "ls-tree", tree), "\n")
	out := []releaseTreeEntry{}
	for _, row := range rows {
		fields := strings.Fields(row)
		if len(fields) != 4 {
			panic("fixture tree is not flat")
		}
		size, _ := strconv.Atoi(p.git("", "cat-file", "-s", fields[2]))
		out = append(out, releaseTreeEntry{Mode: fields[0], Type: fields[1], ID: fields[2], SHA: fields[2], Name: fields[3], Path: fields[3], Size: size})
	}
	return out
}
func (p *releaseRepository) tree(changes []releaseTreeEntry) string {
	entries := map[string]releaseTreeEntry{}
	for _, e := range p.entries(p.source.Tree) {
		entries[e.Path] = e
	}
	for _, e := range changes {
		if e.ID == "" {
			delete(entries, e.Path)
		} else {
			entries[e.Path] = e
		}
	}
	keys := []string{}
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var input strings.Builder
	for _, key := range keys {
		e := entries[key]
		fmt.Fprintf(&input, "%s blob %s\t%s\n", e.Mode, e.ID, key)
	}
	return p.git(input.String(), "mktree")
}
func (p *releaseRepository) commit(sha string) map[string]any {
	parent := p.git("", "show", "-s", "--format=%P", sha)
	parents := []any{}
	parentIDs := []string{}
	for _, id := range strings.Fields(parent) {
		parents = append(parents, map[string]string{"sha": id})
		parentIDs = append(parentIDs, id)
	}
	message := p.git("", "show", "-s", "--format=%B", sha)
	// Git's commit object ends its message with one newline. git show appends its
	// own newline; the helper removes exactly that formatting newline.
	return map[string]any{"id": sha, "sha": sha, "tree": map[string]string{"sha": p.git("", "rev-parse", sha+"^{tree}")}, "message": message, "parents": parents, "parent_ids": parentIDs}
}
func (p *releaseRepository) reviewRecord() map[string]any {
	id, _ := strconv.Atoi(p.providerID)
	return map[string]any{"id": 99, "number": 7, "iid": 7, "project_id": id, "source_project_id": id, "target_project_id": id, "source_branch": p.delivery.Branch, "target_branch": "main", "sha": p.published, "description": p.description, "body": p.description, "html_url": "https://github.com/" + p.locator + "/pull/7", "web_url": "https://gitlab.com/" + p.locator + "/-/merge_requests/7", "state": map[string]string{"github": "open", "gitlab": "opened"}[p.provider], "draft": true, "head": map[string]any{"ref": p.delivery.Branch, "sha": p.published, "repo": map[string]int{"id": id}}, "base": map[string]any{"ref": "main", "repo": map[string]int{"id": id}}}
}
func (p *releaseRepository) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/"+p.locator+".git/") {
		user := "x-access-token"
		if p.provider == "gitlab" {
			user = "oauth2"
		}
		if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+releaseReadToken)) {
			p.t.Error("Git exchange lost read-only credential")
			w.WriteHeader(401)
			return
		}
		p.gitRequests++
		backend := cgi.Handler{Path: "/usr/bin/git", Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + p.root, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "PATH=/usr/bin:/bin"}}
		backend.ServeHTTP(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, p.prefix) {
		p.t.Error("provider scope changed")
		w.WriteHeader(400)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if p.provider == "gitlab" {
		token = r.Header.Get("PRIVATE-TOKEN")
	}
	if token != releaseReadToken && token != releasePublishToken {
		p.t.Error("unexpected provider credential")
		w.WriteHeader(401)
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		p.t.Error("unexpected provider mutation")
		w.WriteHeader(405)
		return
	}
	var in map[string]json.RawMessage
	if r.Method == "POST" {
		if token != releasePublishToken {
			p.t.Error("read credential attempted write")
			w.WriteHeader(403)
			return
		}
		p.posts++
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
	}
	text := func(key string) string { var v string; _ = json.Unmarshal(in[key], &v); return v }
	emit := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	path := strings.TrimPrefix(r.URL.Path, p.prefix)
	switch {
	case path == "":
		id, _ := strconv.Atoi(p.providerID)
		emit(map[string]any{"id": id, "full_name": p.locator, "clone_url": "https://github.com/" + p.locator + ".git", "http_url_to_repo": "https://gitlab.com/" + p.locator + ".git"})
	case strings.HasPrefix(path, "/git/ref/heads/") || strings.HasPrefix(path, "/repository/branches/"):
		branch := strings.TrimPrefix(strings.TrimPrefix(path, "/git/ref/heads/"), "/repository/branches/")
		sha := p.source.Commit
		if branch != "main" {
			if branch != p.delivery.Branch || p.published == "" {
				w.WriteHeader(404)
				return
			}
			sha = p.published
		}
		emit(map[string]any{"ref": "refs/heads/" + branch, "name": branch, "object": map[string]string{"type": "commit", "sha": sha}, "commit": map[string]string{"id": sha}})
	case path == "/git/blobs":
		content, err := base64.StdEncoding.DecodeString(text("content"))
		if err != nil {
			p.t.Error(err)
			w.WriteHeader(400)
			return
		}
		emit(map[string]string{"sha": p.git(string(content), "hash-object", "-w", "--stdin")})
	case strings.HasPrefix(path, "/git/blobs/") || strings.HasPrefix(path, "/repository/blobs/"):
		sha := path[strings.LastIndex(path, "/")+1:]
		raw := p.git("", "cat-file", "blob", sha) + "\n"
		size, _ := strconv.Atoi(p.git("", "cat-file", "-s", sha))
		raw = raw[:size]
		emit(map[string]any{"sha": sha, "size": size, "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(raw))})
	case path == "/git/trees":
		if text("base_tree") != p.source.Tree {
			p.t.Error("wrong original tree")
		}
		var values []struct {
			Path, Mode string
			SHA        *string
		}
		_ = json.Unmarshal(in["tree"], &values)
		changes := []releaseTreeEntry{}
		for _, v := range values {
			sha := ""
			if v.SHA != nil {
				sha = *v.SHA
			}
			changes = append(changes, releaseTreeEntry{Path: v.Path, Mode: v.Mode, ID: sha})
		}
		emit(map[string]string{"sha": p.tree(changes)})
	case strings.HasPrefix(path, "/git/trees/"):
		sha := strings.TrimPrefix(path, "/git/trees/")
		emit(map[string]any{"sha": sha, "truncated": false, "tree": p.entries(sha)})
	case path == "/git/commits":
		var parents []string
		_ = json.Unmarshal(in["parents"], &parents)
		if len(parents) != 1 || parents[0] != p.source.Commit {
			p.t.Error("wrong commit parent")
		}
		sha := p.git(text("message"), "commit-tree", text("tree"), "-p", p.source.Commit)
		emit(p.commit(sha))
	case strings.HasPrefix(path, "/git/commits/") || strings.HasPrefix(path, "/repository/commits/"):
		sha := path[strings.LastIndex(path, "/")+1:]
		emit(p.commit(sha))
	case path == "/git/refs":
		if text("ref") != "refs/heads/"+p.delivery.Branch || p.published != "" {
			p.t.Error("branch overwritten")
		}
		p.published = text("sha")
		p.branches++
		p.git("", "update-ref", text("ref"), p.published, strings.Repeat("0", 40))
		emit(map[string]any{"ref": text("ref"), "object": map[string]string{"sha": p.published, "type": "commit"}})
	case path == "/repository/commits":
		if text("start_sha") != p.source.Commit || text("branch") != p.delivery.Branch || !bytes.Equal(in["force"], []byte("false")) || p.published != "" {
			p.t.Error("GitLab write lost exact new-branch scope")
		}
		var actions []struct {
			Action     string `json:"action"`
			Path       string `json:"file_path"`
			Content    string `json:"content"`
			LastCommit string `json:"last_commit_id"`
		}
		_ = json.Unmarshal(in["actions"], &actions)
		changes := []releaseTreeEntry{}
		for _, a := range actions {
			if a.Action != "update" || a.LastCommit != p.source.Commit {
				p.t.Error("unexpected synthetic patch action")
			}
			raw, err := base64.StdEncoding.DecodeString(a.Content)
			if err != nil {
				p.t.Error(err)
			}
			changes = append(changes, releaseTreeEntry{Path: a.Path, Mode: "100644", ID: p.git(string(raw), "hash-object", "-w", "--stdin")})
		}
		tree := p.tree(changes)
		p.published = p.git(text("commit_message"), "commit-tree", tree, "-p", p.source.Commit)
		p.branches++
		p.git("", "update-ref", "refs/heads/"+p.delivery.Branch, p.published, strings.Repeat("0", 40))
		emit(p.commit(p.published))
	case path == "/repository/tree":
		ref := r.URL.Query().Get("ref")
		emit(p.entries(p.git("", "rev-parse", ref+"^{tree}")))
	case path == "/pulls" || path == "/merge_requests":
		if r.Method == "GET" {
			if p.review {
				emit([]any{p.reviewRecord()})
			} else {
				emit([]any{})
			}
			return
		}
		if p.review {
			p.t.Error("review created twice")
		}
		if p.provider == "github" && !bytes.Equal(in["draft"], []byte("true")) {
			p.t.Error("non-draft review")
		}
		if p.provider == "gitlab" && !strings.HasPrefix(text("title"), "Draft: ") {
			p.t.Error("non-draft review")
		}
		p.description = text("body")
		if p.provider == "gitlab" {
			p.description = text("description")
		}
		p.review = true
		p.reviews++
		if p.dropReview {
			p.dropReview = false
			p.postsAtDrop = p.posts
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				p.t.Error(err)
			} else {
				_ = conn.Close()
			}
			return
		}
		emit(p.reviewRecord())
	case path == "/pulls/7" || path == "/merge_requests/7":
		emit(p.reviewRecord())
	case strings.HasSuffix(path, "/check-runs"):
		emit(map[string]any{"total_count": 0, "check_runs": []any{}})
	case strings.HasSuffix(path, "/status"):
		emit(map[string]any{"sha": p.published, "total_count": 0, "statuses": []any{}})
	case path == "/pipelines":
		emit([]any{})
	case path == "/deployments":
		if !p.deployed {
			emit([]any{})
			return
		}
		if p.provider == "github" {
			emit([]any{map[string]any{"id": 1, "sha": p.published, "environment": "synthetic-test", "production_environment": false, "updated_at": p.deploymentAt}})
		} else {
			emit([]any{p.deployment()})
		}
	case path == "/deployments/1/statuses":
		emit([]any{map[string]any{"id": 2, "state": "success", "environment": "synthetic-test", "created_at": p.deploymentAt, "updated_at": p.deploymentAt}})
	case path == "/deployments/1":
		emit(p.deployment())
	default:
		p.t.Errorf("unexpected controlled provider request: %s %s", r.Method, path)
		w.WriteHeader(400)
	}
}
func (p *releaseRepository) deployment() map[string]any {
	return map[string]any{"id": 1, "sha": p.published, "status": "success", "updated_at": p.deploymentAt, "environment": map[string]any{"id": 3, "name": "synthetic-test"}}
}
