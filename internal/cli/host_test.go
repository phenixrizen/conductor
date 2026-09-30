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
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/config"
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
	cfg.AdminToken = "admin-token"
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

// --agent picks the adapter: the hooks go to the host's hooks dir under
// XDG_STATE_HOME and the session is launched with the adapter's flags.
func TestHostAgentFlagInjectsTheAdapter(t *testing.T) {
	clearConductorEnv(t)
	// The host writes its hook assets, which records their binary for the
	// whole process: forget it when the test ends.
	t.Cleanup(agents.ForgetBinary())
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	cfg := config.Defaults()
	cfg.AdminToken = "admin-token"
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
		args := []string{"--server", hs.URL, "--token", "host-token", "--no-local", "--relay-only", "--name", "hooked",
			"--agent", "claude", "--", "/bin/cat"}
		if code, err := runHost(ctx, args, strings.NewReader(""), &bytes.Buffer{}, &stderr); err != nil {
			t.Errorf("host: exit %d, %v, %s", code, err, stderr.String())
		}
	}()
	settings := filepath.Join(state, "conductor", "hooks", "claude.json")
	deadline := time.Now().Add(10 * time.Second)
	for {
		var command []string
		var agentID string
		for _, info := range srv.Registry().List() {
			if info.Name == "hooked" {
				command, agentID = info.Command, info.AgentID
			}
		}
		if command != nil {
			if !slices.Equal(command, []string{"/bin/cat", "--settings", settings}) || agentID != "claude" {
				t.Fatalf("server lists %q for agent %q", command, agentID)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the hosted session never registered: %+v", srv.Registry().List())
		}
		time.Sleep(20 * time.Millisecond)
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
