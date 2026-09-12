package repositorycontext

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is unavailable")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "--initial-branch=main")
	runGit(t, repo, "config", "user.name", "Synthetic Author")
	runGit(t, repo, "config", "user.email", "synthetic@example.invalid")
	return repo
}

func runGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, repo, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, path)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, path), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, repo string) string {
	t.Helper()
	runGit(t, repo, "add", "--all")
	runGit(t, repo, "commit", "-m", "synthetic context")
	return runGit(t, repo, "rev-parse", "HEAD")
}

func TestCollectPinsCommitAndIgnoresDirtyFiles(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "specs/design.md", "# Original design\n")
	writeFile(t, repo, "empty.txt", "")
	baseline := commit(t, repo)
	writeFile(t, repo, "specs/design.md", "# Uncommitted change\n")
	runGit(t, repo, "add", "specs/design.md")
	writeFile(t, repo, "specs/design.md", "# Dirty working tree\n")
	before := time.Now().UTC()
	snapshot, err := Collect(context.Background(), repo, "synthetic-repository", "main", []string{"specs/design.md", "empty.txt", "missing.md"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Commit != baseline || snapshot.Repository != "synthetic-repository" || snapshot.RequestedRef != "main" || snapshot.CollectedAt.Before(before) {
		t.Fatalf("unexpected provenance: %#v", snapshot)
	}
	if artifact := snapshot.Artifacts[0]; artifact.State != "collected" || artifact.Text == nil || *artifact.Text != "# Original design\n" {
		t.Fatalf("did not read pinned content: %#v", artifact)
	}
	if artifact := snapshot.Artifacts[1]; artifact.State != "collected" || artifact.Text == nil || *artifact.Text != "" {
		t.Fatalf("empty text file was not collected: %#v", artifact)
	}
	if artifact := snapshot.Artifacts[2]; artifact.State != "missing" || artifact.Text != nil || artifact.Digest != "" {
		t.Fatalf("missing file was misrepresented: %#v", artifact)
	}
	if err := domain.ValidateRepositoryContext(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestFreshnessObservesRefMovementWithoutChangingSnapshot(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "design.md", "original")
	baseline := commit(t, repo)
	snapshot, err := Collect(context.Background(), repo, "synthetic", "main", []string{"design.md"})
	if err != nil {
		t.Fatal(err)
	}
	if result := CheckFreshness(context.Background(), repo, "main", snapshot); result.State != "current" || result.CurrentCommit != baseline {
		t.Fatalf("unexpected initial freshness: %#v", result)
	}
	writeFile(t, repo, "design.md", "new revision")
	latest := commit(t, repo)
	if result := CheckFreshness(context.Background(), repo, "main", snapshot); result.State != "stale" || result.CurrentCommit != latest || result.InspectedCommit != baseline {
		t.Fatalf("ref movement was not stale: %#v", result)
	}
	if result := CheckFreshness(context.Background(), repo, "unknown-ref", snapshot); result.State != "unavailable" || result.CurrentCommit != "" {
		t.Fatalf("missing ref was misrepresented: %#v", result)
	}
	if snapshot.Commit != baseline || *snapshot.Artifacts[0].Text != "original" {
		t.Fatal("freshness check changed the inspected snapshot")
	}
	pinned, err := Collect(context.Background(), repo, "synthetic", baseline, []string{"design.md"})
	if err != nil || *pinned.Artifacts[0].Text != "original" {
		t.Fatalf("old baseline could not be recovered: %v", err)
	}
}

func TestUnsupportedAndOversizedArtifactsNeverContainPartialText(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "binary.dat", "binary\x00data")
	writeFile(t, repo, "invalid-utf8.dat", "\xff\xfe")
	writeFile(t, repo, "oversize.txt", strings.Repeat("x", domain.MaxContextArtifactBytes+1))
	writeFile(t, repo, "directory/child.txt", "child")
	if err := os.Symlink("directory/child.txt", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	baseline := commit(t, repo)
	runGit(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+baseline+",module")
	runGit(t, repo, "commit", "-m", "synthetic submodule entry")
	snapshot, err := Collect(context.Background(), repo, "synthetic", "HEAD", []string{"binary.dat", "invalid-utf8.dat", "oversize.txt", "link", "directory", "module"})
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range snapshot.Artifacts {
		want := "unavailable"
		if artifact.Path == "oversize.txt" {
			want = "truncated"
		}
		if artifact.State != want || artifact.Text != nil || artifact.Digest != "" || artifact.Message == "" {
			t.Errorf("unsupported file exposed collected content: %#v", artifact)
		}
	}
}

func TestCollectionTotalByteLimit(t *testing.T) {
	repo := newRepo(t)
	paths := []string{"one.txt", "two.txt", "three.txt", "four.txt", "five.txt"}
	for _, path := range paths {
		writeFile(t, repo, path, strings.Repeat("x", domain.MaxContextArtifactBytes))
	}
	commit(t, repo)
	snapshot, err := Collect(context.Background(), repo, "synthetic", "HEAD", paths)
	if err != nil {
		t.Fatal(err)
	}
	for i, artifact := range snapshot.Artifacts {
		if i < 4 && artifact.State != "collected" {
			t.Errorf("file inside byte budget was not collected: %#v", artifact)
		}
		if i == 4 && (artifact.State != "truncated" || artifact.Text != nil || artifact.Digest != "") {
			t.Errorf("file outside byte budget was collected: %#v", artifact)
		}
	}
}

func TestSelectedPathsAreLiteral(t *testing.T) {
	repo := newRepo(t)
	paths := []string{"notes[1].md", "*.md", ":(glob)*.md"}
	for _, path := range paths {
		writeFile(t, repo, path, "literal "+path)
	}
	writeFile(t, repo, "notes1.md", "must not be selected")
	commit(t, repo)
	snapshot, err := Collect(context.Background(), repo, "synthetic", "HEAD", paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range snapshot.Artifacts {
		if artifact.State != "collected" || artifact.Text == nil || *artifact.Text != "literal "+artifact.Path {
			t.Fatalf("literal path was interpreted as a pattern: %#v", artifact)
		}
	}
}

func TestMissingLocalBlobNeverInvokesRemoteHelper(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "design.md", "original")
	commit(t, repo)
	blob := runGit(t, repo, "rev-parse", "HEAD:design.md")
	if err := os.Remove(filepath.Join(repo, ".git", "objects", blob[:2], blob[2:])); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "helper-invoked")
	helperDir := t.TempDir()
	helper := "#!/bin/sh\nprintf invoked > \"$CONDUCTOR_TEST_HELPER_MARKER\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(helperDir, "git-remote-synthetic"), []byte(helper), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "config", "core.repositoryformatversion", "1")
	runGit(t, repo, "config", "extensions.partialclone", "origin")
	runGit(t, repo, "config", "remote.origin.url", "synthetic://not-a-real-remote")
	runGit(t, repo, "config", "remote.origin.promisor", "true")
	runGit(t, repo, "config", "protocol.synthetic.allow", "always")
	t.Setenv("PATH", helperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CONDUCTOR_TEST_HELPER_MARKER", marker)
	snapshot, err := Collect(context.Background(), repo, "synthetic", "HEAD", []string{"design.md"})
	if err != nil {
		t.Fatal(err)
	}
	if artifact := snapshot.Artifacts[0]; artifact.State != "unavailable" || artifact.Text != nil || artifact.Digest != "" {
		t.Fatalf("missing local object was misrepresented: %#v", artifact)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("collector invoked a repository-configured remote helper: %v", err)
	}
}

func TestRejectsUnsafePathsAndOptionRefs(t *testing.T) {
	for _, path := range []string{"", ".", "..", "../secret", "/tmp/secret", "a/../../secret", "a/./b", "a//b", "a/", "--help", "a\\b", "a\nb", "\x00"} {
		t.Run(path, func(t *testing.T) {
			_, err := Collect(context.Background(), "absent-repo", "synthetic", "HEAD", []string{path})
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("unsafe path accepted: %v", err)
			}
		})
	}
	for _, paths := range [][]string{nil, {"a", "a"}, make([]string, domain.MaxContextArtifacts+1)} {
		if _, err := Collect(context.Background(), "absent-repo", "synthetic", "HEAD", paths); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid path selection accepted: %v", err)
		}
	}
	for _, ref := range []string{"", "--help", "HEAD\n--help"} {
		if _, err := Collect(context.Background(), "absent-repo", "synthetic", ref, []string{"a"}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("unsafe ref accepted: %v", err)
		}
	}
}

func TestCollectionIgnoresGitEnvironmentAndReplacementObjects(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "design.md", "original")
	baseline := commit(t, repo)
	writeFile(t, repo, "design.md", "replacement")
	replacement := commit(t, repo)
	runGit(t, repo, "replace", baseline, replacement)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "nonexistent"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "alias.unused")
	t.Setenv("GIT_CONFIG_VALUE_0", "!false")
	snapshot, err := Collect(context.Background(), repo, "synthetic", baseline, []string{"design.md"})
	if err != nil || *snapshot.Artifacts[0].Text != "original" {
		t.Fatalf("Git environment or replacement changed collected baseline: %v", err)
	}
}

func TestCanceledCollectionAndBoundedGitOutput(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "design.md", strings.Repeat("x", 1024))
	commit(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Collect(ctx, repo, "synthetic", "HEAD", []string{"design.md"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was not propagated: %v", err)
	}
	if result := CheckFreshness(ctx, repo, "HEAD", domain.RepositoryContext{Commit: strings.Repeat("a", 40)}); result.State != "unavailable" {
		t.Fatalf("canceled freshness check returned success: %#v", result)
	}
	if out, err := git(context.Background(), repo, 8, "show", "HEAD:design.md"); err == nil || len(out) > 8 {
		t.Fatalf("Git output exceeded bound: %d bytes, %v", len(out), err)
	}
}

func TestValidateRepositoryContextPreservesExtensionsAndRejectsFalseEvidence(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "design.md", "original")
	commit(t, repo)
	snapshot, err := Collect(context.Background(), repo, "synthetic", "HEAD", []string{"design.md"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	fresh := func() map[string]any {
		var value map[string]any
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	value := fresh()
	value["futureField"] = map[string]any{"retained": true}
	before, _ := json.Marshal(value)
	if err := domain.ValidateRepositoryContext(value); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(value)
	if string(before) != string(after) {
		t.Fatal("validation rewrote unknown fields")
	}
	cases := map[string]func(map[string]any){
		"digest mismatch":  func(v map[string]any) { v["artifacts"].([]any)[0].(map[string]any)["digest"] = strings.Repeat("0", 64) },
		"missing text":     func(v map[string]any) { delete(v["artifacts"].([]any)[0].(map[string]any), "text") },
		"missing blob":     func(v map[string]any) { delete(v["artifacts"].([]any)[0].(map[string]any), "blobOID") },
		"partial content":  func(v map[string]any) { v["artifacts"].([]any)[0].(map[string]any)["state"] = "truncated" },
		"fake missing":     func(v map[string]any) { v["artifacts"].([]any)[0].(map[string]any)["state"] = "missing" },
		"unknown state":    func(v map[string]any) { v["artifacts"].([]any)[0].(map[string]any)["state"] = "passing" },
		"short commit":     func(v map[string]any) { v["commit"] = "abc123" },
		"wrong schema":     func(v map[string]any) { v["schemaVersion"] = 2 },
		"wrong collector":  func(v map[string]any) { v["collector"] = "unverified" },
		"non UTC":          func(v map[string]any) { v["collectedAt"] = "2026-09-12T12:00:00-05:00" },
		"absent timestamp": func(v map[string]any) { delete(v, "collectedAt") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := fresh()
			mutate(v)
			if err := domain.ValidateRepositoryContext(v); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("invalid context accepted: %v", err)
			}
			if err := domain.ValidateContent(domain.Content{"repositoryContext": v}); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("reserved content field bypassed validation: %v", err)
			}
		})
	}
}
