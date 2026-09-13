package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/phenixrizen/conductor/pkg/client"
)

func cleanCredentialEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"CONDUCTOR_TOKEN", "CONDUCTOR_TOKEN_FILE", "CONDUCTOR_WORKSPACE", "CONDUCTOR_REPOSITORY_ID", "CONDUCTOR_URL"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCredentialModeSelection(t *testing.T) {
	for _, tc := range []struct {
		name, command, actor, token, tokenFile, workspace string
		actorSet, configured, fileConfigured, valid       bool
	}{
		{name: "local review", command: "show", actor: "reviewer", actorSet: true, valid: true},
		{name: "local terminal", command: "tui", actor: "reviewer", actorSet: true, valid: true},
		{name: "authenticated review", command: "show", token: "synthetic.token", configured: true, workspace: "workspace-1", valid: true},
		{name: "session discovery without scope", command: "session", token: "synthetic.token", configured: true, valid: true},
		{name: "repository discovery", command: "repositories", token: "synthetic.token", configured: true, workspace: "workspace-1", valid: true},
		{name: "mixed actor", command: "show", actor: "forged", actorSet: true, token: "synthetic.token", configured: true},
		{name: "empty actor flag still mixed", command: "show", actorSet: true, token: "synthetic.token", configured: true},
		{name: "authenticated terminal unavailable", command: "tui", token: "synthetic.token", configured: true},
		{name: "local session unavailable", command: "session", actor: "reviewer", actorSet: true},
		{name: "local repositories unavailable", command: "repositories", actor: "reviewer", actorSet: true},
		{name: "local scope cannot masquerade as authorization", command: "show", actor: "reviewer", actorSet: true, workspace: "workspace-1"},
		{name: "empty configured token", command: "show", configured: true},
		{name: "no identity", command: "show"},
		{name: "local context ignores malformed credential environment", command: "context", token: "bad token", configured: true, tokenFile: "/missing-token-file", fileConfigured: true, valid: true},
		{name: "local freshness ignores malformed credential environment", command: "context-check", token: "bad token", configured: true, tokenFile: "/missing-token-file", fileConfigured: true, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanCredentialEnvironment(t)
			if tc.configured {
				t.Setenv("CONDUCTOR_TOKEN", tc.token)
			}
			if tc.fileConfigured {
				t.Setenv("CONDUCTOR_TOKEN_FILE", tc.tokenFile)
			}
			c, err := clientForCommand(tc.command, tc.actor, tc.actorSet, tc.workspace, "")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && c != nil && tc.configured && c.Actor != "" {
				t.Fatal("authenticated client retained local actor")
			}
			if err != nil && tc.token != "" && strings.Contains(err.Error(), tc.token) {
				t.Fatal("credential appeared in an error")
			}
		})
	}
}

func TestTokenFileBoundsAndFormatting(t *testing.T) {
	for _, tc := range []struct {
		name, contents string
		valid          bool
	}{
		{"token", "synthetic.token", true},
		{"one newline", "synthetic.token\n", true},
		{"one CRLF", "synthetic.token\r\n", true},
		{"exact file limit", strings.Repeat("a", client.MaxTokenBytes), true},
		{"oversized file", strings.Repeat("a", client.MaxTokenBytes+1), false},
		{"empty", "", false},
		{"spaces", "synthetic token", false},
		{"extra newline", "synthetic.token\n\n", false},
		{"bare CR", "synthetic.token\r", false},
		{"header injection", "synthetic.token\r\nX-Conductor-Actor:forged", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanCredentialEnvironment(t)
			path := filepath.Join(t.TempDir(), "access-token")
			if err := os.WriteFile(path, []byte(tc.contents), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CONDUCTOR_TOKEN_FILE", path)
			_, err := clientForCommand("session", "", false, "", "")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic.token") {
				t.Fatal("token contents leaked into an error")
			}
		})
	}
}

func TestTokenFileRejectsNonRegularFiles(t *testing.T) {
	cleanCredentialEnvironment(t)
	directory := t.TempDir()
	fifo := filepath.Join(directory, "token-pipe")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, fifo, filepath.Join(directory, "missing"), "-", ""} {
		t.Setenv("CONDUCTOR_TOKEN_FILE", path)
		if _, _, err := accessTokenFromEnvironment(); err == nil {
			t.Fatal("non-regular token source accepted")
		}
	}
}

func TestTokenSourcesAreMutuallyExclusiveIncludingEmptyValues(t *testing.T) {
	cleanCredentialEnvironment(t)
	t.Setenv("CONDUCTOR_TOKEN", "")
	t.Setenv("CONDUCTOR_TOKEN_FILE", "/missing-token-file")
	if _, _, err := accessTokenFromEnvironment(); err == nil || !strings.Contains(err.Error(), "only one") {
		t.Fatalf("mutually exclusive sources: %v", err)
	}
}
