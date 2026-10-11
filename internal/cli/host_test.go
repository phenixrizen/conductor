package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/hostagent"
	"github.com/phenixrizen/conductor/internal/session"
)

func runHostWith(t *testing.T, args ...string) (int, string, error) {
	t.Helper()
	var stderr bytes.Buffer
	code, err := runHost(t.Context(), args, strings.NewReader(""), &bytes.Buffer{}, &stderr)
	return code, stderr.String(), err
}

// The pattern is checked before the host dials anything, with the rule the
// catalog holds an agent's pattern to: RE2, at most 200 bytes.
func TestHostRejectsABadSignalPattern(t *testing.T) {
	clearConductorEnv(t)
	for _, c := range []struct{ name, pattern, want string }{
		{"not a regular expression", "(", "missing closing"},
		{"longer than 200 bytes", strings.Repeat("a", 201), "at most 200 bytes"},
		{"matches an empty line", `.*`, "must not match an empty line"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, _, err := runHostWith(t, "--server", "http://127.0.0.1:1", "--token", "t", "--signal-pattern", c.pattern, "--", "sh")
			if code != 2 || err == nil || !strings.Contains(err.Error(), "invalid --signal-pattern") || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("exit %d, error %v, want one about %q", code, err, c.want)
			}
		})
	}
}

// The flag reaches the hosted session: the prompt the process leaves on its
// last line marks the session needs_input on the server, with source pattern.
func TestHostSignalPatternReachesTheSession(t *testing.T) {
	clearConductorEnv(t)
	cfg := config.Defaults()
	cfg.WorkbenchToken = "admin-token"
	cfg.HostTokens = []string{"host-token"}
	cfg.AllowedRoots = []string{t.TempDir()}
	cfg.Dev = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := api.New(cfg, catalog.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var stderr bytes.Buffer
		args := []string{"--server", hs.URL, "--token", "host-token", "--no-local", "--relay-only", "--name", "asker",
			"--signal-pattern", `\(y/n\) $`, "--", "/bin/sh", "-c", `printf 'Continue? (y/n) '; exec /bin/cat`}
		if code, err := runHost(ctx, args, strings.NewReader(""), &bytes.Buffer{}, &stderr); err != nil {
			t.Errorf("host: exit %d, %v, %s", code, err, stderr.String())
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var att session.Attention
		for _, info := range srv.Registry().List() {
			if info.Name == "asker" {
				att = info.Attention
			}
		}
		if att.State == session.AttentionNeedsInput {
			if att.Source != session.SourcePattern || att.Message != "prompt: Continue? (y/n)" {
				t.Fatalf("attention %+v", att)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the hosted session never needed input: %+v", srv.Registry().List())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("host did not stop")
	}
}

// registrationWatch is a host's stderr. registered is closed once the host has
// printed hostingBanner, which it prints after reading its registration reply,
// which the server sends after listing the session.
type registrationWatch struct {
	registered chan struct{}

	mu   sync.Mutex
	buf  bytes.Buffer
	seen bool
}

func newRegistrationWatch() *registrationWatch {
	return &registrationWatch{registered: make(chan struct{})}
}

func (w *registrationWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if !w.seen && strings.Contains(w.buf.String(), hostingBanner) {
		w.seen = true
		close(w.registered)
	}
	return n, err
}

func (w *registrationWatch) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// --agent picks the adapter: the hooks go to the host's hooks dir,
// ~/.conductor/hooks (XDG_STATE_HOME holds no older one, so it is ignored),
// and the session is launched with the adapter's flags.
func TestHostAgentFlagInjectsTheAdapter(t *testing.T) {
	clearConductorEnv(t)
	// The host writes its hook assets, which records their binary for the
	// whole process: forget it when the test ends.
	t.Cleanup(agents.ForgetBinary())
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	cfg := config.Defaults()
	cfg.WorkbenchToken = "admin-token"
	cfg.HostTokens = []string{"host-token"}
	cfg.AllowedRoots = []string{t.TempDir()}
	cfg.Dev = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := api.New(cfg, catalog.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stderr := newRegistrationWatch()
	done := make(chan struct{})
	go func() {
		defer close(done)
		args := []string{"--server", hs.URL, "--token", "host-token", "--no-local", "--relay-only", "--name", "hooked",
			"--agent", "claude", "--", "/bin/cat"}
		if code, err := runHost(ctx, args, strings.NewReader(""), &bytes.Buffer{}, stderr); err != nil {
			t.Errorf("host: exit %d, %v, %s", code, err, stderr)
		}
	}()
	// The server lists the session before it sends the registration reply, so
	// the listing alone does not say the host is registered: cancelled before
	// it reads the reply, the host fails. Its banner comes after the reply.
	select {
	case <-stderr.registered:
	case <-done:
		t.Fatalf("the host stopped before it registered: %s", stderr)
	case <-time.After(10 * time.Second):
		t.Fatalf("the hosted session never registered: %+v %s", srv.Registry().List(), stderr)
	}
	settings := filepath.Join(os.Getenv("HOME"), ".conductor", "hooks", "claude.json")
	var command []string
	var agentID string
	for _, info := range srv.Registry().List() {
		if info.Name == "hooked" {
			command, agentID = info.Command, info.AgentID
		}
	}
	if !slices.Equal(command, []string{"/bin/cat", "--settings", settings}) || agentID != "claude" {
		t.Fatalf("server lists %q for agent %q", command, agentID)
	}
	if b, err := os.ReadFile(settings); err != nil || !strings.Contains(string(b), "notify --claude-hook") {
		t.Fatalf("asset: %v %s", err, b)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("host did not stop")
	}
}

// No token is no longer refused up front: an open switchyard admits such a
// host, and any other server answers 401 itself. Here the dial fails, which
// is the server's answer, not the flag check's.
func TestHostNeedsNoToken(t *testing.T) {
	clearConductorEnv(t)
	code, stderr, err := runHostWith(t, "--server", "http://127.0.0.1:1", "--no-local", "--", "sh")
	if err == nil || code == 2 || strings.Contains(err.Error()+stderr, "host token is required") {
		t.Fatalf("code %d, err %v, stderr %q", code, err, stderr)
	}
}

// conductor host hands its session the deny list of the server on its
// machine (config.LocalFileDenyFunc): ~/.conductor and the desktop app's
// directories, and with --server-config that file, its dataDir and its
// catalogPath, the copies beside the two files included. A config file it
// cannot read or parse stops it before it dials.
func TestHostRefusesTheServersFilesToItsSession(t *testing.T) {
	clearConductorEnv(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	home := os.Getenv("HOME")
	local := []string{filepath.Join(home, ".conductor"), filepath.Join(home, ".local", "share", "conductor", "data"), filepath.Join(home, ".config", "conductor-desktop"), filepath.Join(home, ".config", "Conductor")}
	var desktopSettings []string
	for _, name := range []string{"conductor-desktop", "Conductor"} {
		desktopSettings = append(desktopSettings, filepath.Join(home, ".config", name, "settings.json"), filepath.Join(home, ".config", name, "*settings.json*"))
	}
	var got []hostagent.Options
	runHostAgent = func(_ context.Context, opts hostagent.Options) (hostagent.Result, error) {
		got = append(got, opts)
		return hostagent.Result{}, nil
	}
	t.Cleanup(func() { runHostAgent = hostagent.Run })

	if code, stderr, err := runHostWith(t, "--server", "http://127.0.0.1:1", "--no-local", "--", "sh"); code != 0 || err != nil {
		t.Fatalf("exit %d, %v, %s", code, err, stderr)
	}
	if want := slices.Concat(local, desktopSettings); len(got) != 1 || !slices.Equal(got[0].FileDeny(), want) {
		t.Fatalf("without --server-config: %+v, want the deny list %q", got, want)
	}

	dir := t.TempDir()
	data, catalogFile := filepath.Join(dir, "state"), filepath.Join(dir, "agents.json")
	cfg := writeServeConfig(t, dir, `{"workbenchToken":"w","dataDir":"`+data+`","catalogPath":"`+catalogFile+`"}`)
	got = nil
	if code, stderr, err := runHostWith(t, "--server", "http://127.0.0.1:1", "--no-local", "--server-config", cfg, "--", "sh"); code != 0 || err != nil {
		t.Fatalf("exit %d, %v, %s", code, err, stderr)
	}
	want := slices.Concat([]string{data}, local, []string{cfg, filepath.Join(dir, "*conductor.json*"), catalogFile, filepath.Join(dir, "*agents.json*")}, desktopSettings)
	if len(got) != 1 || !slices.Equal(got[0].FileDeny(), want) {
		t.Fatalf("with --server-config: %+v\nwant the deny list %q", got, want)
	}

	// A file it cannot read or parse, and a relative dataDir or
	// catalogPath, which serve resolves against a directory the host
	// cannot know, stop it: what they name would go unrefused.
	got = nil
	for _, c := range []struct{ path, want string }{
		{filepath.Join(dir, "missing.json"), "read config"},
		{writeServeConfig(t, t.TempDir(), `{"dataDirectory":"x"}`), "parse config"},
		{writeServeConfig(t, t.TempDir(), `{"dataDir":"state"}`), "dataDir in "},
		{writeServeConfig(t, t.TempDir(), `{"catalogPath":"agents.json"}`), "catalogPath in "},
	} {
		code, _, err := runHostWith(t, "--server", "http://127.0.0.1:1", "--no-local", "--server-config", c.path, "--", "sh")
		if code != 2 || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: exit %d, %v; want an error about %q", c.path, code, err, c.want)
		}
	}
	t.Setenv("CONDUCTOR_DATA_DIR", "state")
	if code, _, err := runHostWith(t, "--server", "http://127.0.0.1:1", "--no-local", "--", "sh"); code != 2 || err == nil || !strings.Contains(err.Error(), "CONDUCTOR_DATA_DIR") {
		t.Errorf("a relative CONDUCTOR_DATA_DIR: exit %d, %v", code, err)
	}
	t.Setenv("CONDUCTOR_DATA_DIR", "")
	// The desktop app's settings there but not readable for their data directory.
	settings := filepath.Join(home, ".config", "conductor-desktop", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{oops`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, err := runHostWith(t, "--server", "http://127.0.0.1:1", "--no-local", "--", "sh"); code != 2 || err == nil || !strings.Contains(err.Error(), settings) {
		t.Errorf("unreadable desktop settings: exit %d, %v", code, err)
	}
	if len(got) != 0 {
		t.Fatalf("hosted with server files it could not place: %+v", got)
	}
}

// --server-is-mine is the person's word, and only for a server on this
// machine; without it no server's window is taken for the owner's.
func TestServerIsMineOnlyForAServerHere(t *testing.T) {
	for _, tc := range []struct {
		mine   bool
		server string
		ok     bool
	}{
		{false, "http://localhost:8080", true},
		{false, "https://switchyard.example", true},
		{true, "http://localhost:8080", true},
		{true, "http://127.0.0.1:8080", true},
		{true, "https://switchyard.example", false},
		{true, "https://192.168.1.20:8080", false},
	} {
		if err := serverIsMineOK(tc.mine, tc.server); (err == nil) != tc.ok {
			t.Errorf("serverIsMineOK(%v, %q) = %v", tc.mine, tc.server, err)
		}
	}
}
