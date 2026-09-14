package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAssistantConfigOfflineAndEscaped(t *testing.T) {
	// None of these paths exist. Generating config must not open credentials or
	// mutate an installed native host, even if ambient credentials are invalid.
	t.Setenv("CONDUCTOR_TOKEN", "never-print-this-credential")
	binary := `/tmp/a "quoted" directory/conductor-mcp`
	token := `/tmp/agent.token`
	for _, host := range []string{"codex", "claude", "antigravity"} {
		var out, diagnostics bytes.Buffer
		err := runAssistantConfig(context.Background(), []string{"--host", host, "--mcp-binary", binary, "--token-file", token, "--url", "https://conductor.example.invalid", "--workspace", "team", "--repository-id", "application"}, &out, &diagnostics)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String()+diagnostics.String(), "never-print-this-credential") {
			t.Fatal("credential escaped into generated configuration")
		}
		if host == "codex" {
			if !strings.Contains(out.String(), `command = "/tmp/a \"quoted\" directory/conductor-mcp"`) || !strings.Contains(out.String(), `CONDUCTOR_MCP_PROFILE = "design-assistance"`) {
				t.Fatalf("unsafe TOML: %s", out.String())
			}
		} else {
			var generated struct {
				Servers map[string]assistantConnection `json:"mcpServers"`
			}
			if json.Unmarshal(out.Bytes(), &generated) != nil || generated.Servers["conductor"].Command != binary || generated.Servers["conductor"].Env["CONDUCTOR_TOKEN_FILE"] != token || generated.Servers["conductor"].Env["CONDUCTOR_MCP_PROFILE"] != "design-assistance" {
				t.Fatal("invalid native host configuration")
			}
		}
	}
}

func TestAssistantCheckBoundsChildOutputAndCancellation(t *testing.T) {
	for _, script := range []string{"#!/bin/sh\nexec head -c 1200000 /dev/zero\n", "#!/bin/sh\nexec sleep 30\n"} {
		path := filepath.Join(t.TempDir(), "synthetic-mcp")
		if err := os.WriteFile(path, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		var out bytes.Buffer
		started := time.Now()
		err := checkAssistantConnection(ctx, assistantConnection{Command: path, Env: map[string]string{}}, &out)
		cancel()
		if err == nil || out.Len() != 0 || time.Since(started) > 2*time.Second {
			t.Fatal("unbounded or stalled child did not fail promptly with no output")
		}
	}
}

func TestAssistantConfigRejectsUnsafeTransportAndPaths(t *testing.T) {
	base := []string{"--mcp-binary", "/tmp/conductor-mcp", "--token-file", "/tmp/agent.token", "--url", "https://conductor.example.invalid", "--workspace", "team", "--repository-id", "application"}
	for _, extra := range [][]string{{"--url", "http://remote.example.invalid"}, {"--url", "https://user:secret@example.invalid"}, {"--mcp-binary", "relative"}, {"--token-file", "/tmp/path\nnew-key"}, {"--host", "invented"}, {"--workspace", ""}, {"trailing"}} {
		var out, diagnostics bytes.Buffer
		args := append(append([]string{}, base...), extra...)
		if err := runAssistantConfig(context.Background(), args, &out, &diagnostics); err == nil || out.Len() != 0 {
			t.Fatal("unsafe config emitted")
		}
	}
}
