package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/config"
)

// syncBuffer collects what the server logs while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// clearConductorEnv keeps the CONDUCTOR_* variables of whoever runs the tests
// out of the configuration: config.Load ignores empty values.
func clearConductorEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "CONDUCTOR_") {
			t.Setenv(k, "")
		}
	}
	// A home of the test's own: nothing a test starts reads or writes the
	// user's ~/.conductor.
	t.Setenv("HOME", t.TempDir())
}

// writeServeConfig writes body as conductor.json in dir and returns its path.
func writeServeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "conductor.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// serveUntilListening runs `conductor serve` on a free loopback port until it
// reports that it is serving, stops it, and returns everything it logged.
func serveUntilListening(t *testing.T, args ...string) string {
	t.Helper()
	// Serve records the binary it writes the hook assets for, for the whole
	// process: forget it when the test ends.
	t.Cleanup(agents.ForgetBinary())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs syncBuffer
	done := make(chan error, 1)
	go func() {
		code, err := runServe(ctx, append([]string{"--listen", "127.0.0.1:0"}, args...), io.Discard, &logs)
		if err == nil && code != 0 {
			err = fmt.Errorf("exit code %d", code)
		}
		done <- err
	}()
	deadline := time.After(10 * time.Second)
	for !strings.Contains(logs.String(), "conductor serving") {
		select {
		case err := <-done:
			t.Fatalf("serve returned before serving: %v\n%s", err, logs.String())
		case <-deadline:
			t.Fatalf("serve did not start:\n%s", logs.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v\n%s", err, logs.String())
	}
	return logs.String()
}

// logLines returns the lines of logs that contain every one of parts.
func logLines(logs string, parts ...string) []string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		match := true
		for _, p := range parts {
			match = match && strings.Contains(line, p)
		}
		if match {
			out = append(out, line)
		}
	}
	return out
}

func TestServeLogsTheDataDirectory(t *testing.T) {
	clearConductorEnv(t)
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "state")
	cfg := writeServeConfig(t, dir, fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q, "dataDir": %q}`, work, work, data))
	logs := serveUntilListening(t, "--config", cfg)
	if lines := logLines(logs, "conductor serving", "dataDir="+data); len(lines) != 1 {
		t.Fatalf("the serving line does not name the data directory:\n%s", logs)
	}
	if fi, err := os.Stat(data); err != nil || !fi.IsDir() {
		t.Fatalf("data directory not created: %v", err)
	}
	// Outside every allowed root, so no warning about it.
	if lines := logLines(logs, "level=WARN", "dataDir="); len(lines) != 0 {
		t.Fatalf("unexpected data directory warning: %v", lines)
	}
}

// Without dataDir the directory is ~/.conductor. Here the home is the
// allowed root: agents work there, so the server says so.
func TestServeWarnsWhenTheDataDirOverlapsAnAllowedRoot(t *testing.T) {
	clearConductorEnv(t)
	home := os.Getenv("HOME")
	cfg := writeServeConfig(t, t.TempDir(), fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q}`, home, home))
	logs := serveUntilListening(t, "--config", cfg)
	data := filepath.Join(home, ".conductor")
	if lines := logLines(logs, "level=WARN", "dataDir="+data, "allowedRoot="+home); len(lines) != 1 {
		t.Fatalf("no warning that %s is inside the allowed root %s:\n%s", data, home, logs)
	}
}

// A data directory that cannot be made stops the server, which says which
// settings choose another.
func TestServeNamesTheSettingWhenTheDataDirIsNotUsable(t *testing.T) {
	clearConductorEnv(t)
	cfg := writeServeConfig(t, t.TempDir(), `{"adminToken": "t"}`)
	data := filepath.Join(os.Getenv("HOME"), ".conductor")
	// A file where the directory should be defeats MkdirAll even for root.
	if err := os.WriteFile(data, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A server that starts anyway is stopped by the deadline, so the test
	// fails instead of waiting for the package timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var logs syncBuffer
	code, err := runServe(ctx, []string{"--config", cfg, "--listen", "127.0.0.1:0"}, io.Discard, &logs)
	if strings.Contains(logs.String(), "conductor serving") {
		t.Fatalf("serve listened with an unusable data directory:\n%s", logs.String())
	}
	if code != 1 || err == nil {
		t.Fatalf("serve started with an unusable data directory: %d %v", code, err)
	}
	for _, want := range []string{data, "dataDir", "CONDUCTOR_DATA_DIR", "not a directory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// An upgraded server whose data directory an older Conductor put next to the
// config keeps using it while ~/.conductor holds no server data, and says
// once, at warn, where it is, where the default is and how to move.
func TestServeKeepsAnOldDataDirectoryWithANotice(t *testing.T) {
	clearConductorEnv(t)
	dir := t.TempDir()
	work, old := filepath.Join(dir, "work"), filepath.Join(dir, "conductor.d")
	for _, d := range []string{work, old} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := writeServeConfig(t, dir, fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q}`, work, work))
	logs := serveUntilListening(t, "--config", cfg)
	def := filepath.Join(os.Getenv("HOME"), ".conductor")
	if lines := logLines(logs, "level=WARN", old, def); len(lines) != 1 {
		t.Fatalf("no notice naming %s and %s:\n%s", old, def, logs)
	}
	if lines := logLines(logs, "conductor serving", "dataDir="+old); len(lines) != 1 {
		t.Fatalf("the server does not use %s:\n%s", old, logs)
	}
	if _, err := os.Stat(def); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s was made: %v", def, err)
	}
}

// Without dataDir and without an old directory the server makes ~/.conductor,
// 0700, names it on its serving line and warns about nothing it holds. (The
// test binary embeds no web UI, which is a warning of its own.)
func TestServeUsesTheHomeDataDirectory(t *testing.T) {
	clearConductorEnv(t)
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := writeServeConfig(t, dir, fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q}`, work, work))
	logs := serveUntilListening(t, "--config", cfg)
	data := filepath.Join(os.Getenv("HOME"), ".conductor")
	if lines := logLines(logs, "conductor serving", "dataDir="+data); len(lines) != 1 {
		t.Fatalf("the serving line does not name %s:\n%s", data, logs)
	}
	if fi, err := os.Stat(data); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("%s: %v %v", data, fi, err)
	}
	if lines := logLines(logs, "level=WARN", ".conductor"); len(lines) != 0 {
		t.Fatalf("unexpected data directory warnings: %v", lines)
	}
}

// The server writes the hook assets into its data directory at startup, naming
// its own binary, before it serves: a launch that injects them finds them.
func TestServeWritesTheHookAssets(t *testing.T) {
	clearConductorEnv(t)
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "state")
	cfg := writeServeConfig(t, dir, fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q, "dataDir": %q}`, work, work, data))
	serveUntilListening(t, "--config", cfg)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(data, "hooks", "claude.json"))
	if err != nil || !strings.Contains(string(b), exe+" notify --claude-hook") {
		t.Fatalf("claude.json: %v\n%s", err, b)
	}
	if fi, err := os.Stat(filepath.Join(data, "hooks")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("hooks dir: %v %v", fi.Mode(), err)
	}
}

// When the conductor on PATH is this binary through a link, as package
// managers install it, the assets name the link: it survives an upgrade that
// replaces the binary it points to.
func TestServeNamesTheConductorOnPATH(t *testing.T) {
	clearConductorEnv(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(bin, "conductor")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "state")
	cfg := writeServeConfig(t, dir, fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q, "dataDir": %q}`, work, work, data))
	serveUntilListening(t, "--config", cfg)
	b, err := os.ReadFile(filepath.Join(data, "hooks", "claude.json"))
	if err != nil || !strings.Contains(string(b), `"`+filepath.Join(bin, "conductor")+` notify --claude-hook"`) {
		t.Fatalf("claude.json: %v\n%s", err, b)
	}
}

// A webhook host that does not resolve while the server starts is not a
// reason to refuse to start: the server logs a warning and checks the host
// again before every delivery. A host that resolves to a private address
// still refuses the start.
func TestServeWarnsAboutAWebhookHostThatDoesNotResolve(t *testing.T) {
	clearConductorEnv(t)
	old := config.LookupWebhookHost
	config.LookupWebhookHost = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "internal.example" {
			return []netip.Addr{netip.MustParseAddr("10.0.0.7")}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { config.LookupWebhookHost = old })
	dir := t.TempDir()
	body := fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q, "dataDir": %q,
		"webhooks": [{"url": "https://nowhere.example/hook?token=t0k3n", "events": ["error"], "secret": "s3cret"}]}`, dir, dir, filepath.Join(t.TempDir(), "data"))
	logs := serveUntilListening(t, "--config", writeServeConfig(t, t.TempDir(), body))
	if lines := logLines(logs, "level=WARN", "webhooks[0]", "nowhere.example did not resolve"); len(lines) != 1 {
		t.Fatalf("no warning about the webhook host:\n%s", logs)
	}
	if strings.Contains(logs, "t0k3n") || strings.Contains(logs, "s3cret") {
		t.Fatalf("the log holds the query or the secret:\n%s", logs)
	}

	private := strings.Replace(body, "nowhere.example", "internal.example", 1)
	var stderr bytes.Buffer
	code, err := runServe(t.Context(), []string{"--listen", "127.0.0.1:0", "--config", writeServeConfig(t, t.TempDir(), private)}, io.Discard, &stderr)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "10.0.0.7 is a private address") {
		t.Fatalf("a private webhook host: exit %d %v\n%s", code, err, stderr.String())
	}
}
