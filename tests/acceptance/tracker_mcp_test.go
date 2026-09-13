package acceptance_test

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/trackerworker"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrackerAuthenticatedMCPStdio(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Skip("set CONDUCTOR_TEST_DATABASE_URL for real tracker MCP acceptance")
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "conductor-mcp")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/conductor-mcp")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build MCP server: %v\n%s", err, output)
	}

	f := newAccessFixture(t)
	config := trackerConfig("linear")
	if err := f.db.ApplyTrackerConfig(f.ctx, "synthetic-operator", config); err != nil {
		t.Fatal(err)
	}
	provider, factory := newTrackerProvider(t, config)
	activity, err := trackerworker.NewActivity(f.db, trackerCredentials, factory)
	if err != nil {
		t.Fatal(err)
	}
	pkg := f.create("author", "team", "application", domain.Content{"intent": "Synthetic shared tracker MCP package"})
	tokenFile := filepath.Join(directory, "agent.token")
	if err := os.WriteFile(tokenFile, []byte(f.tokens["agent"]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(f.ctx, binary)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CONDUCTOR_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "CONDUCTOR_URL="+f.server.URL, "CONDUCTOR_TOKEN_FILE="+tokenFile, "CONDUCTOR_WORKSPACE=team", "CONDUCTOR_REPOSITORY_ID=application")
	var stderr boundedRestartOutput
	command.Stderr = &stderr
	session, err := mcp.NewClient(&mcp.Implementation{Name: "conductor-acceptance", Version: "1"}, nil).Connect(f.ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("connect real stdio MCP: %v", err)
	}
	defer session.Close()
	call := func(name string, args any, wantError bool) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(f.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.IsError != wantError {
			t.Fatalf("%s error=%v wanted=%v: %+v", name, result.IsError, wantError, result)
		}
		return result
	}
	data := func(result *mcp.CallToolResult, target any) {
		t.Helper()
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(envelope.Data, target) != nil {
			t.Fatalf("invalid MCP structured result: %s", raw)
		}
	}

	var settings domain.TrackerSettings
	data(call("conductor_get_tracker", map[string]any{}, false), &settings)
	if settings.Provider != "linear" || settings.CanResolve {
		t.Fatal("agent obtained human conflict-resolution capability")
	}
	input := map[string]any{"idempotencyKey": "tracker-mcp-link", "issueId": provider.issue, "packages": []domain.TrackerPackageRef{{RepositoryID: "application", PackageID: pkg.ID, Revision: pkg.Revision.Number, Digest: pkg.Revision.Digest}}}
	var link, replay domain.TrackerLink
	data(call("conductor_link_tracker_issue", input, false), &link)
	data(call("conductor_link_tracker_issue", input, false), &replay)
	if link.ID == "" || link.ID != replay.ID || link.CreatorID != "person-agent" {
		t.Fatal("lost shared tracker identity or keyed retry")
	}
	runTrackerActivity(t, f, activity, link.LatestSyncID)
	data(call("conductor_get_tracker_link", map[string]any{"id": link.ID}, false), &link)
	if link.Observation == nil || link.Observation.Issue.Title != provider.title {
		t.Fatal("MCP missed tracker-owned shared data")
	}
	call("conductor_sync_tracker_link", map[string]any{"id": link.ID, "idempotencyKey": "cannot-restore", "linkDigest": link.Digest, "mode": "restore"}, true)
	var sync domain.TrackerSync
	data(call("conductor_sync_tracker_link", map[string]any{"id": link.ID, "idempotencyKey": "publish-card", "linkDigest": link.Digest, "mode": "publish"}, false), &sync)
	runTrackerActivity(t, f, activity, sync.ID)
	data(call("conductor_get_tracker_sync", map[string]any{"id": sync.ID}, false), &sync)
	if sync.Observation == nil || sync.Observation.State != "synchronized" {
		t.Fatal("MCP projection did not synchronize")
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-revocation", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-agent"}}}); err != nil {
		t.Fatal(err)
	}
	call("conductor_get_tracker_link", map[string]any{"id": link.ID}, true)
	call("conductor_get_tracker_sync", map[string]any{"id": sync.ID}, true)
	if strings.Contains(stderr.String(), f.tokens["agent"]) || strings.Contains(stderr.String(), trackerToken) {
		t.Fatal("MCP leaked credential")
	}
}
