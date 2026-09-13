package delivery

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

func fixturePatch(t *testing.T) ([]byte, execution.Patch) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Git %v: %v %s", args, err, out)
		}
		return out
	}
	run("init", "-q")
	run("config", "user.name", "Fixture")
	run("config", "user.email", "fixture@example.invalid")
	for path, text := range map[string]string{"keep.txt": "kept\n", "remove.txt": "remove\n", "change.txt": "old\n"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-qm", "fixture base")
	commit := strings.TrimSpace(string(run("rev-parse", "HEAD")))
	tree := strings.TrimSpace(string(run("rev-parse", "HEAD^{tree}")))
	bundlePath := filepath.Join(t.TempDir(), "source.bundle")
	run("bundle", "create", bundlePath, "HEAD")
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "remove.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "change.txt"), []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "binary.bin"), []byte{0, 1, 2, 255}, 0755); err != nil {
		t.Fatal(err)
	}
	run("add", "--all")
	run("update-index", "--chmod=+x", "binary.bin")
	patch := run("diff", "--cached", "--binary", "--full-index", "--no-renames", "--no-ext-diff", "--no-textconv")
	return bundle, execution.Patch{RepositoryID: "repo", BaseCommit: commit, BaseTree: tree, ResultTree: strings.TrimSpace(string(run("write-tree"))), Patch: patch, Digest: execution.Sum(patch), Paths: []string{"binary.bin", "change.txt", "remove.txt"}}
}
func TestPrepareExactCumulativeTree(t *testing.T) {
	bundle, p := fixturePatch(t)
	got, err := Prepare(context.Background(), bundle, p)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResultTree != p.ResultTree || len(got.Files) != 3 || got.Files[0].Mode != "100755" || len(got.Files[0].Content) != 4 || got.Files[2].Mode != "000000" {
		t.Fatalf("wrong prepared change %#v", got)
	}
	for name, mutate := range map[string]func(*execution.Patch){"tree": func(p *execution.Patch) { p.ResultTree = strings.Repeat("a", 40) }, "base": func(p *execution.Patch) { p.BaseTree = strings.Repeat("a", 40) }, "digest": func(p *execution.Patch) { p.Digest = strings.Repeat("a", 64) }, "path": func(p *execution.Patch) { p.Paths = []string{"change.txt"} }} {
		t.Run(name, func(t *testing.T) {
			q := p
			mutate(&q)
			if _, err := Prepare(context.Background(), bundle, q); err == nil {
				t.Fatal("accepted mismatched artifact")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Prepare(ctx, bundle, p); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestSelectedPatchRequiresExactPassedChecks(t *testing.T) {
	_, patch := fixturePatch(t)
	zero := 0
	task := domain.CoordinationTask{ProfileDigest: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("a", 64), Checks: []domain.VerificationCommand{{ID: "test", RepositoryID: "repo", Argv: []string{"go", "test", "./..."}}}}
	task.Checks[0].Requirements = []domain.VerificationRequirement{{ChangeID: "synthetic-design", Revision: 1, Digest: strings.Repeat("b", 64), CriterionID: "exact-output"}}
	patches := []execution.Patch{patch}
	data, _ := json.Marshal(patches)
	result := execution.Result{CleanupConfirmed: true, ProfileDigest: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("a", 64), Adapter: "command/v1", AdapterVersion: "1", InputDigest: strings.Repeat("a", 64), Patches: patches, Producer: execution.Evidence{State: "passed", ExitCode: &zero, SourceDigest: strings.Repeat("a", 64), OutputDigest: execution.Sum(nil)}, Checks: []execution.Evidence{{ID: "test", RepositoryID: "repo", Argv: task.Checks[0].Argv, Requirements: task.Checks[0].Requirements, State: "passed", ExitCode: &zero, SourceDigest: execution.Sum(data), OutputDigest: execution.Sum(nil)}}}
	selectResult := func(result execution.Result, task domain.CoordinationTask) error {
		raw, _ := json.Marshal(result)
		digest, _ := domain.JSONDigest(result)
		_, err := SelectedPatch(raw, digest, "repo", task)
		return err
	}
	if err := selectResult(result, task); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "source", "skipped", "truncated", "argv", "producer", "no-check", "profile", "image", "requirements"} {
		t.Run(mode, func(t *testing.T) {
			r := result
			r.Checks = append([]execution.Evidence(nil), result.Checks...)
			task := task
			switch mode {
			case "requirements":
				r.Checks[0].Requirements = nil
			case "profile":
				r.ProfileDigest = strings.Repeat("b", 64)
			case "image":
				r.Image = "sha256:" + strings.Repeat("b", 64)
			case "missing":
				r.Checks = nil
			case "source":
				r.Checks[0].SourceDigest = strings.Repeat("b", 64)
			case "skipped":
				r.Checks[0].State = "skipped"
			case "truncated":
				r.Checks[0].Truncated = true
			case "argv":
				r.Checks[0].Argv = []string{"true"}
			case "producer":
				r.Producer.SourceDigest = strings.Repeat("b", 64)
			case "no-check":
				r.Checks = nil
				task.Checks = nil
			}
			if selectResult(r, task) == nil {
				t.Fatal("accepted unverified artifact")
			}
		})
	}
}
