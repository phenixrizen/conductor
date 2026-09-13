package execution

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validRequest() Request {
	return Request{RunID: "run", TaskID: "task", ChangeID: "change", Revision: 1, Digest: strings.Repeat("a", 64), GraphDigest: strings.Repeat("b", 64), Prompt: "Update app.txt within its allowed scope.", TimeoutSeconds: 60, Repositories: []Repository{{ID: "app", Commit: strings.Repeat("c", 40), Bundle: []byte("bundle"), WritablePaths: []string{"app.txt"}}}, Checks: []Check{{ID: "verify", RepositoryID: "app", Argv: []string{"sh", "-c", "test \"$(cat app.txt)\" = new"}, TimeoutSeconds: 10}}}
}

func TestRequestBoundsAndImmutableIdentity(t *testing.T) {
	r := validRequest()
	if err := ValidateRequest(r); err != nil {
		t.Fatal(err)
	}
	first := InputDigest(r)
	r.Repositories[0].Bundle = []byte("different")
	if InputDigest(r) == first {
		t.Fatal("bundle bytes did not bind input digest")
	}
	cases := []struct {
		name   string
		change func(*Request)
	}{
		{"traversal", func(r *Request) { r.Repositories[0].WritablePaths = []string{"../other"} }},
		{"git alias", func(r *Request) { r.Repositories[0].WritablePaths = []string{"src/.Git/config"} }},
		{"absolute", func(r *Request) { r.Repositories[0].WritablePaths = []string{"/work"} }},
		{"NUL", func(r *Request) { r.Checks[0].Argv = []string{"sh\x00"} }},
		{"mutable ref", func(r *Request) { r.Repositories[0].Commit = "main" }},
		{"missing graph", func(r *Request) { r.GraphDigest = "" }},
		{"unknown check scope", func(r *Request) { r.Checks[0].RepositoryID = "other" }},
		{"duplicate repo", func(r *Request) { r.Repositories = append(r.Repositories, r.Repositories[0]) }},
		{"missing deadline", func(r *Request) { r.TimeoutSeconds = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequest()
			tc.change(&r)
			if !errors.Is(ValidateRequest(r), ErrInvalid) {
				t.Fatal("invalid request accepted")
			}
		})
	}
	if allowedPath("src-other/file", []string{"src"}) || !allowedPath("src/file", []string{"src"}) {
		t.Fatal("path boundary mismatch")
	}
}

func TestSandboxFlagsNeverMountHostOrExposeProviderCredentials(t *testing.T) {
	args := dockerArgs("synthetic", "sha256:"+strings.Repeat("a", 64), false)
	joined := strings.Join(args, " ")
	for _, required := range []string{"--read-only", "--cap-drop ALL", "--network none", "--user 10001:10001", "--pids-limit 128", "--memory-swap 1g", "--pull never"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %s", required)
		}
	}
	for _, forbidden := range []string{"--privileged", "--mount", "--volume", "docker.sock", "--network host", "API_KEY"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("unsafe %s", forbidden)
		}
	}
	if validImage("image:latest") || !validImage("registry.invalid/image@sha256:"+strings.Repeat("a", 64)) {
		t.Fatal("image pin validation")
	}
	if err := SandboxMain(context.Background(), strings.NewReader("{}"), &bytes.Buffer{}); !errors.Is(err, ErrSandbox) {
		t.Fatal("host fallback accepted")
	}
}

func TestCredentialFileAndAdapterBoundaries(t *testing.T) {
	name := filepath.Join(t.TempDir(), "provider.key")
	if err := os.WriteFile(name, []byte("synthetic-only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if key, err := readCredential(name); err != nil || key != "synthetic-only" {
		t.Fatal("private credential not read")
	}
	if err := os.Chmod(name, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredential(name); !errors.Is(err, ErrInvalid) {
		t.Fatal("public credential permissions accepted")
	}
	link := filepath.Join(t.TempDir(), "key")
	if err := os.Symlink(name, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredential(link); err == nil {
		t.Fatal("credential symlink accepted")
	}
	for _, profile := range []Profile{{Adapter: "codex/0.154.0"}, {Adapter: "claude-code/2.1.270", MaxBudgetUSD: "0.50"}} {
		argv, env, version, err := adapterCommand(profile, "synthetic-key")
		if err != nil || version == "" {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(argv, " "), "synthetic-key") || !strings.Contains(strings.Join(env, " "), "synthetic-key") {
			t.Fatal("credential delivery contract")
		}
		if _, _, _, err := adapterCommand(profile, ""); !errors.Is(err, ErrUnavailable) {
			t.Fatal("missing credential accepted")
		}
	}
	if _, _, _, err := adapterCommand(Profile{Adapter: "command/v1", Command: []string{"true"}}, "key"); !errors.Is(err, ErrInvalid) {
		t.Fatal("command received credentials")
	}
	for _, p := range []Profile{{Adapter: "codex/latest"}, {Adapter: "claude-code/2.1.270"}, {Adapter: "codex/0.154.0", MaxBudgetUSD: "1"}, {Adapter: "claude-code/2.1.270", MaxBudgetUSD: "0.00"}} {
		if p.Validate() == nil {
			t.Fatal("unsupported profile accepted")
		}
	}
}

func TestNativeAssistantRequiresTerminalResult(t *testing.T) {
	for _, tc := range []struct {
		adapter, output string
		complete        bool
	}{
		{"codex/0.154.0", `{"type":"turn.completed","usage":{}}`, true},
		{"codex/0.154.0", `{"type":"item.completed"}`, false},
		{"codex/0.154.0", "{\"type\":\"turn.completed\"}\n{\"type\":\"turn.failed\"}", false},
		{"claude-code/2.1.270", `{"type":"result","subtype":"success","is_error":false}`, true},
		{"claude-code/2.1.270", `{"type":"result","subtype":"error_max_budget_usd","is_error":true}`, false},
		{"claude-code/2.1.270", `{"type":"result","subtype":"success"}`, false},
		{"claude-code/2.1.270", `not JSON`, false},
	} {
		if assistantCompleted(tc.adapter, []byte(tc.output)) != tc.complete {
			t.Fatalf("terminal protocol %s %s", tc.adapter, tc.output)
		}
	}
}

func fixtureBundle(t *testing.T, files map[string]string) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = environment()
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Git: %v %s", err, b)
		}
		return strings.TrimSpace(string(b))
	}
	run("init", "--quiet")
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "--all")
	run("-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "--quiet", "--no-gpg-sign", "-m", "Synthetic fixture")
	commit := run("rev-parse", "HEAD")
	bundle := filepath.Join(t.TempDir(), "source.bundle")
	run("bundle", "create", bundle, "--all")
	b, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return b, commit
}

func dockerFixture(t *testing.T) (Runner, Request) {
	t.Helper()
	if os.Getenv("CONDUCTOR_TEST_EXECUTION") != "1" {
		t.Skip("set CONDUCTOR_TEST_EXECUTION=1 for actual isolated Docker worker acceptance")
	}
	image := os.Getenv("CONDUCTOR_TEST_WORKER_IMAGE")
	if !validImage(image) {
		t.Fatal("opt-in requires CONDUCTOR_TEST_WORKER_IMAGE immutable image digest")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("opt-in requires Docker")
	}
	r := validRequest()
	r.Repositories[0].Bundle, r.Repositories[0].Commit = fixtureBundle(t, map[string]string{"app.txt": "old\n", "untouched.txt": "preserved\n"})
	return Runner{Image: image, Profile: Profile{Adapter: "command/v1", Command: []string{"sh", "-c", "printf 'new\\n' > app.txt"}}}, r
}

func TestDockerIsolatedPatchAndVerification(t *testing.T) {
	runner, r := dockerFixture(t)
	t.Setenv("GITHUB_TOKEN", "synthetic-must-not-cross")
	t.Setenv("CONDUCTOR_TOKEN", "synthetic-must-not-cross")
	t.Setenv("ANTHROPIC_API_KEY", "synthetic-must-not-cross")
	r.Checks = append(r.Checks, Check{ID: "boundary", RepositoryID: "app", TimeoutSeconds: 10, Argv: []string{"sh", "-c", `test "$(id -u)" = 10001 && test "$(sed -n "s/^NoNewPrivs:[[:space:]]*//p" /proc/self/status)" = 1 && test ! -e /var/run/docker.sock && test -z "$GITHUB_TOKEN$CONDUCTOR_TOKEN$ANTHROPIC_API_KEY" && test "$(cat untouched.txt)" = preserved && test ! -w /usr/local/bin/conductor-sandbox && test "$(ls /sys/class/net | wc -l)" = 1`}})
	result, err := runner.Run(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if result.InputDigest != InputDigest(r) || result.Producer.State != "passed" || len(result.Patches) != 1 || len(result.Checks) != 2 {
		t.Fatalf("incomplete result: %+v", result)
	}
	p := result.Patches[0]
	if p.BaseCommit != r.Repositories[0].Commit || p.BaseTree == p.ResultTree || p.Digest != Sum(p.Patch) || len(p.Paths) != 1 || p.Paths[0] != "app.txt" || !bytes.Contains(p.Patch, []byte("+new")) {
		t.Fatalf("unbound patch: %+v", p)
	}
	for _, check := range result.Checks {
		if check.State != "passed" || check.ExitCode == nil || *check.ExitCode != 0 || check.SourceDigest == "" {
			t.Fatalf("verification evidence: %+v", check)
		}
	}
}

func TestDockerPathEscapeAndWrongBaseline(t *testing.T) {
	runner, r := dockerFixture(t)
	for _, command := range []string{"printf changed > untouched.txt", "ln -s /etc/passwd app-link", "printf secret > ../outside"} {
		t.Run(command, func(t *testing.T) {
			runner.Profile.Command = []string{"sh", "-c", command}
			_, err := runner.Run(context.Background(), r)
			if command == "printf secret > ../outside" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrSandbox) {
				t.Fatalf("out-of-scope patch accepted: %v", err)
			}
		})
	}
	runner.Profile.Command = []string{"true"}
	r.Repositories[0].Commit = strings.Repeat("0", 40)
	if _, err := runner.Run(context.Background(), r); !errors.Is(err, ErrSandbox) {
		t.Fatal("absent baseline accepted")
	}
}

func TestDockerFailureTimeoutAndFreshVerifier(t *testing.T) {
	runner, r := dockerFixture(t)
	runner.Profile.Command = []string{"sh", "-c", "printf 'new\\n' > app.txt; exit 7"}
	result, err := runner.Run(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if result.Producer.State != "failed" || result.Checks[0].State != "unexecuted" {
		t.Fatal("failed producer established passing verification")
	}
	runner.Profile.Command = []string{"sh", "-c", "printf 'new\\n' > app.txt; (sleep 2; printf 'bad\\n' > app.txt) >/dev/null 2>&1 &"}
	r.Checks[0].Argv = []string{"sh", "-c", "sleep 3; test \"$(cat app.txt)\" = new"}
	result, err = runner.Run(context.Background(), r)
	if err != nil || result.Checks[0].State != "passed" {
		t.Fatalf("producer survived into verifier: %v %+v", err, result)
	}
	r.Checks[0].Argv = []string{"sleep", "10"}
	r.Checks[0].TimeoutSeconds = 1
	result, err = runner.Run(context.Background(), r)
	if err != nil || result.Checks[0].State != "timed_out" {
		t.Fatalf("timeout not explicit: %v %+v", err, result)
	}
	runner.Profile.Command = []string{"sleep", "60"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = runner.Run(ctx, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestDockerPinnedNativeAdapters(t *testing.T) {
	runner, r := dockerFixture(t)
	r.Checks = nil
	runner.Profile.Command = []string{"sh", "-c", `codex --version && claude --version && codex exec --help && claude --help`}
	result, err := runner.Run(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{CodexVersion, ClaudeVersion, "--ephemeral", "--ignore-user-config", "--bare", "--strict-mcp-config", "--safe-mode", "--max-budget-usd"} {
		if !strings.Contains(result.Producer.Output, expected) {
			t.Fatalf("native CLI contract lacks %s", expected)
		}
	}
	if result.Producer.State != "passed" {
		t.Fatal("native CLI discovery failed")
	}
}

func TestDockerDependentAgentsAndSharedAncestor(t *testing.T) {
	runner, base := dockerFixture(t)
	run := func(task, command string, allowed []string, deps []Patch) Result {
		t.Helper()
		r := base
		r.TaskID = task
		r.Repositories = append([]Repository(nil), base.Repositories...)
		r.Repositories[0].WritablePaths = allowed
		r.Repositories[0].Dependencies = deps
		r.Checks = nil
		runner.Profile.Command = []string{"sh", "-c", command}
		result, err := runner.Run(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	a := run("ancestor", "printf 'new\\n' > app.txt", []string{"app.txt"}, nil)
	b := run("left", "test \"$(cat app.txt)\" = new && printf left > left.txt", []string{"left.txt"}, a.Patches)
	c := run("right", "test \"$(cat app.txt)\" = new && printf right > right.txt", []string{"right.txt"}, a.Patches)
	if b.Producer.State != "passed" || c.Producer.State != "passed" {
		t.Fatal("dependent producer did not see ancestor patch")
	}
	deps := append(append([]Patch{}, b.Patches...), c.Patches...)
	combined := run("combined", `test "$(cat app.txt)" = new && test "$(cat left.txt)" = left && test "$(cat right.txt)" = right`, nil, deps)
	if combined.Producer.State != "passed" || len(combined.Patches) != 1 || len(combined.Patches[0].Paths) != 3 {
		t.Fatalf("shared ancestor was lost/doubled: %+v", combined)
	}
	if combined.Patches[0].BaseCommit != base.Repositories[0].Commit {
		t.Fatal("cumulative patch lost original baseline")
	}
	other := run("conflicting", "printf incompatible > app.txt", []string{"app.txt"}, nil)
	blocked := run("blocked", "exit 99", []string{"app.txt"}, append(append([]Patch{}, a.Patches...), other.Patches...))
	if blocked.Producer.State != "blocked" || len(blocked.Patches) != 0 {
		t.Fatal("conflicting prerequisites ran producer or fabricated patch")
	}
	forged := a.Patches[0]
	forged.ResultTree = strings.Repeat("0", 40)
	r := base
	r.Repositories[0].Dependencies = []Patch{forged}
	if _, err := runner.Run(context.Background(), r); !errors.Is(err, ErrSandbox) {
		t.Fatal("unverified dependency tree accepted")
	}
}

func TestDockerCanonicalRepositoryIdentityAndCleanup(t *testing.T) {
	runner, request := dockerFixture(t)
	id := "team/工程.repo"
	request.Repositories[0].ID = id
	request.Checks[0].RepositoryID = id
	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CleanupConfirmed || result.Patches[0].RepositoryID != id || result.Checks[0].State != "passed" {
		t.Fatalf("identity or cleanup lost: %+v", result)
	}
	if !strings.HasPrefix(RepositoryDirectory(id), "/work/repos/r-") || strings.Contains(RepositoryDirectory(id), "工程") {
		t.Fatal("canonical identity used as filesystem path")
	}
	// Simulate a process death after the immutable attempt was admitted. Recovery
	// removes this exact orphan and leaves unrelated Docker resources untouched.
	name := "conductor-task-" + InputDigest(request)[:32] + "-produce"
	b, err := runner.docker(context.Background(), "run", "--detach", "--name", name, "--network", "none", "--entrypoint", "/bin/sleep", runner.Image, "30")
	if err != nil {
		t.Fatalf("create owned orphan: %v %s", err, b)
	}
	t.Cleanup(func() { _, _ = runner.docker(context.Background(), "rm", "--force", name) })
	clean, err := runner.CleanupAttempt(context.Background(), InputDigest(request))
	if err != nil || !clean {
		t.Fatalf("cleanup recovery: %v %v", clean, err)
	}
}
