package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/mcpserver"
	"github.com/phenixrizen/conductor/pkg/client"
)

// Connection configuration contains file paths, never provider or Conductor
// credentials. Generation is offline and leaves the host's configuration alone.
type assistantConnection struct {
	Command string            `json:"command"`
	Env     map[string]string `json:"env"`
}

func runAssistantConfig(ctx context.Context, args []string, out, diagnostics io.Writer) error {
	f := flag.NewFlagSet("assistant-config", flag.ContinueOnError)
	f.SetOutput(diagnostics)
	host := f.String("host", "codex", "native host: codex, claude, antigravity")
	binary := f.String("mcp-binary", "", "absolute path to conductor-mcp")
	tokenFile := f.String("token-file", os.Getenv("CONDUCTOR_TOKEN_FILE"), "absolute path to the Conductor agent token file")
	base := f.String("url", os.Getenv("CONDUCTOR_URL"), "Conductor API URL")
	workspace := f.String("workspace", os.Getenv("CONDUCTOR_WORKSPACE"), "workspace ID")
	repository := f.String("repository-id", os.Getenv("CONDUCTOR_REPOSITORY_ID"), "canonical repository ID")
	check := f.Bool("check", false, "check actual Conductor MCP access; does not check provider login or run a model")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("assistant-config accepts flags only")
	}
	if *host != "codex" && *host != "claude" && *host != "antigravity" {
		return errors.New("--host must be codex, claude or antigravity")
	}
	for _, value := range []string{*binary, *tokenFile} {
		if !filepath.IsAbs(value) || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
			return errors.New("--mcp-binary and --token-file require bounded absolute paths without control characters")
		}
	}
	if domain.ValidateAccessID(*workspace) != nil || domain.ValidateAccessID(*repository) != nil {
		return errors.New("workspace and repository ID are required")
	}
	// Validate URL and identifiers through the same client contract, without I/O.
	if _, err := client.NewAuthenticated(*base, "configuration-validation", *workspace, *repository); err != nil {
		return err
	}
	connection := assistantConnection{Command: *binary, Env: map[string]string{
		"CONDUCTOR_URL": *base, "CONDUCTOR_TOKEN_FILE": *tokenFile, "CONDUCTOR_WORKSPACE": *workspace, "CONDUCTOR_REPOSITORY_ID": *repository, "CONDUCTOR_MCP_PROFILE": mcpserver.DesignAssistanceProfile,
	}}
	if *check {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return checkAssistantConnection(ctx, connection, out)
	}
	if *host == "codex" {
		if _, err := fmt.Fprintf(out, "[mcp_servers.conductor]\ncommand = %s\n\n[mcp_servers.conductor.env]\n", strconv.Quote(connection.Command)); err != nil {
			return err
		}
		keys := make([]string, 0, len(connection.Env))
		for key := range connection.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err := fmt.Fprintf(out, "%s = %s\n", key, strconv.Quote(connection.Env[key])); err != nil {
				return err
			}
		}
	} else {
		entry := map[string]any{"command": connection.Command, "env": connection.Env}
		if *host == "claude" {
			entry["type"] = "stdio"
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]any{"mcpServers": map[string]any{"conductor": entry}}); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(diagnostics, "Generated configuration only. Add the conductor entry to your native host settings. Use --check to verify Conductor agent access; provider sign-in stays in the host. See docs/operations/native-design-assistance.md.")
	return err
}

func checkAssistantConnection(ctx context.Context, connection assistantConnection, out io.Writer) error {
	command := exec.CommandContext(ctx, connection.Command)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CONDUCTOR_") {
			command.Env = append(command.Env, entry)
		}
	}
	for key, value := range connection.Env {
		command.Env = append(command.Env, key+"="+value)
	}
	command.Stderr = io.Discard
	command.WaitDelay = time.Second
	writer, err := command.StdinPipe()
	if err != nil {
		return errors.New("cannot open Conductor MCP input")
	}
	reader, err := command.StdoutPipe()
	if err != nil {
		_ = writer.Close()
		return errors.New("cannot open Conductor MCP output")
	}
	if err = command.Start(); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return errors.New("cannot start the selected Conductor MCP binary")
	}
	defer func() {
		_ = writer.Close()
		_ = reader.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	// Reuse the bounded transport before the SDK decodes any child output.
	// The checker reads only metadata; it needs no large artifact response.
	session, err := mcp.NewClient(&mcp.Implementation{Name: "conductor-connection-check", Version: "1"}, nil).Connect(ctx, &mcpserver.Transport{Reader: reader, Writer: writer}, nil)
	if err != nil {
		return errors.New("Conductor MCP connection unavailable; check the binary, token file, API and fixed scope")
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		return errors.New("Conductor MCP tool discovery unavailable")
	}
	names := make([]string, 0, len(list.Tools))
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "conductor_access,conductor_get_design_assistance,conductor_list_design_assistance,conductor_propose_design_sections" || list.NextCursor != "" {
		return errors.New("Conductor MCP must expose the four-tool design-assistance profile")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "conductor_access", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		return errors.New("Conductor agent access could not be established")
	}
	raw, err := json.Marshal(result.StructuredContent)
	var envelope struct {
		Data struct {
			Principal   domain.Principal         `json:"principal"`
			WorkspaceID string                   `json:"workspaceId"`
			Repository  domain.ManagedRepository `json:"repository"`
		} `json:"data"`
	}
	if err != nil || json.Unmarshal(raw, &envelope) != nil {
		return errors.New("Conductor returned invalid access discovery")
	}
	access := envelope.Data
	if access.Principal.Kind != "agent" || domain.ValidateAccessID(access.Principal.ID) != nil || access.WorkspaceID != connection.Env["CONDUCTOR_WORKSPACE"] || access.Repository.ID != connection.Env["CONDUCTOR_REPOSITORY_ID"] || access.Repository.WorkspaceID != access.WorkspaceID || !access.Repository.CanRead || !access.Repository.CanAuthor {
		return errors.New("Design assistance requires an agent principal with read and author permission in the fixed repository")
	}
	_, err = fmt.Fprintln(out, "Conductor MCP connected. Agent read/author access and the four-tool Design assistance profile verified. Provider login and model inference were not checked.")
	return err
}
