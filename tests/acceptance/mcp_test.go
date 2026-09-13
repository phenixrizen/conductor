package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/domain"
)

// This is a real compiled stdio server and SDK client, using signed agent tokens,
// the production API authorization path and an isolated migrated PostgreSQL
// schema. The receipt fixture proves shared context, not provider execution.
func TestAuthenticatedMCPStdio(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Skip("set CONDUCTOR_TEST_DATABASE_URL to run signed-token PostgreSQL MCP stdio acceptance")
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
	f := collectionFixture(t)
	collection := requestCollection(t, f, "author", "team", "application", "mcp-shared-source")
	receipt := completeCollectionFixture(t, f, collection)
	humanPackage := f.create("author", "team", "application", domain.Content{"intent": "Shared with coding agents", "futureField": map[string]any{"retained": true}})
	path := "/api/v1/changes/" + humanPackage.ID
	f.request("author", "team", "application", "POST", path+"/review-requests", map[string]any{"revision": 1}, 200, nil)
	f.request("reviewer", "team", "application", "POST", path+"/approvals", map[string]any{"revision": 1, "digest": humanPackage.Revision.Digest}, 201, nil)
	hidden := f.create("author", "team", "private", domain.Content{"intent": "Private synthetic design"})
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
	var stderr bytes.Buffer
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
	tools, err := session.ListTools(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if strings.Contains(tool.Name, "approve") {
			t.Fatal("MCP agent approval exposed")
		}
	}
	var shared domain.Package
	data(call("conductor_get_package", map[string]any{"id": humanPackage.ID}, false), &shared)
	if !shared.Approved || shared.Revision.Digest != humanPackage.Revision.Digest {
		t.Fatal("agent cannot see exact approved shared context")
	}
	for _, injection := range []map[string]any{{"id": humanPackage.ID, "actor": "person-reviewer"}, {"id": humanPackage.ID, "repositoryId": "private"}, {"id": humanPackage.ID, "token": "forged"}} {
		call("conductor_get_package", injection, true)
	}
	call("conductor_get_package", map[string]any{"id": hidden.ID}, true)
	if _, err := session.CallTool(f.ctx, &mcp.CallToolParams{Name: "conductor_approve_package", Arguments: map[string]any{"id": humanPackage.ID, "revision": 1, "digest": humanPackage.Revision.Digest}}); err == nil {
		t.Fatal("unregistered approval tool accepted")
	}
	resource, err := session.ReadResource(f.ctx, &mcp.ReadResourceParams{URI: "conductor://workspace/team/repository/application/collections/" + collection.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resource.Contents[0].Text, receipt.Digest) || !strings.Contains(resource.Contents[0].Text, `"state":"missing"`) {
		t.Fatal("shared scoped receipt lost digest or evidence gaps")
	}

	// Build one graph spanning two independently authorized repositories. Graph
	// source IDs expand evidence only after the API checks every source grant.
	library := domain.AccessConfig{
		Repositories: []domain.RepositoryConfig{{ID: "library", WorkspaceID: "team", Provider: "github", Host: "github.com", ProviderID: "303", Name: "synthetic/library"}},
		Grants:       []domain.GrantConfig{{RepositoryID: "library", PrincipalID: "person-author", CanRead: true, CanAuthor: true}, {RepositoryID: "library", PrincipalID: "person-agent", CanRead: true, CanAuthor: true}},
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "mcp-graph-operator", library); err != nil {
		t.Fatal(err)
	}
	configureCollectionIntegration(t, f, "team", "library", true)
	libraryCollection := requestCollection(t, f, "author", "team", "library", "mcp-library-source")
	libraryReceipt := completeCollectionFixture(t, f, libraryCollection)
	graphArgs := map[string]any{"idempotencyKey": "mcp-graph-retry", "sources": []map[string]any{
		{"repositoryId": "application", "collectionId": collection.ID, "digest": receipt.Digest},
		{"repositoryId": "library", "collectionId": libraryCollection.ID, "digest": libraryReceipt.Digest},
	}}
	var graph, graphReplay domain.RepositoryGraph
	data(call("conductor_create_graph", graphArgs, false), &graph)
	data(call("conductor_create_graph", graphArgs, false), &graphReplay)
	if graph.ID == "" || graphReplay.ID != graph.ID || len(graph.Snapshot.Sources) != 2 || graph.CreatorID != "person-agent" {
		t.Fatal("graph lost shared source tuples, idempotency or attribution")
	}
	var graphQuery domain.GraphQueryResult
	data(call("conductor_query_graph", map[string]any{"id": graph.ID, "limit": 1}, false), &graphQuery)
	if graphQuery.GraphID != graph.ID || graphQuery.Digest != graph.Digest || !graphQuery.Truncated || len(graphQuery.Sources) != 2 {
		t.Fatalf("graph query lost provenance or truncation: %+v", graphQuery)
	}
	if _, err := session.ReadResource(f.ctx, &mcp.ReadResourceParams{URI: "conductor://workspace/team/repository/application/graphs/" + graph.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "mcp-graph-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "library", PrincipalID: "person-agent"}}}); err != nil {
		t.Fatal(err)
	}
	call("conductor_get_graph", map[string]any{"id": graph.ID}, true)
	call("conductor_query_graph", map[string]any{"id": graph.ID}, true)
	var graphPage domain.RepositoryGraphPage
	data(call("conductor_list_graphs", map[string]any{}, false), &graphPage)
	if len(graphPage.Graphs) != 0 {
		t.Fatal("graph listing retained revoked source data")
	}
	call("conductor_get_package", map[string]any{"id": humanPackage.ID}, false)
	var attached domain.Package
	data(call("conductor_attach_collection", map[string]any{"id": humanPackage.ID, "expectedRevision": 1, "collectionId": collection.ID, "digest": receipt.Digest}, false), &attached)
	if attached.Revision.Number != 2 || attached.Approved || attached.Revision.Author != "person-agent" || attached.Revision.Content["futureField"] == nil {
		t.Fatalf("attachment lost version/identity/unknown content: %+v", attached)
	}
	call("conductor_attach_collection", map[string]any{"id": humanPackage.ID, "expectedRevision": 1, "collectionId": collection.ID, "digest": receipt.Digest}, true)
	var history domain.HistoryPage
	data(call("conductor_package_history", map[string]any{"id": humanPackage.ID, "limit": 1}, false), &history)
	if len(history.Revisions) != 1 || history.NextBeforeRevision == 0 {
		t.Fatal("history silently omitted continuation")
	}
	var historical domain.RevisionRecord
	data(call("conductor_get_revision", map[string]any{"id": humanPackage.ID, "revision": 1}, false), &historical)
	if len(historical.Approvals) != 1 || historical.Approvals[0].Digest != humanPackage.Revision.Digest {
		t.Fatal("historical approval lost")
	}
	args := map[string]any{"commit": strings.Repeat("a", 40), "paths": []string{"README.md"}, "idempotencyKey": "mcp-explicit-retry"}
	var first, second domain.Collection
	data(call("conductor_request_collection", args, false), &first)
	data(call("conductor_request_collection", args, false), &second)
	if first.ID != second.ID || first.RequesterID != "person-agent" {
		t.Fatal("same-key retry duplicated request or forged requester")
	}
	var cancelled domain.Collection
	data(call("conductor_cancel_collection", map[string]any{"id": first.ID}, false), &cancelled)
	if cancelled.CancelRequestedAt == nil || cancelled.Execution != nil {
		t.Fatal("cancellation intent invented execution outcome")
	}
	// Replacing the file cannot silently switch this process to a human token.
	if err := os.WriteFile(tokenFile, []byte(f.tokens["reviewer"]), 0600); err != nil {
		t.Fatal(err)
	}
	var discovered struct {
		Principal domain.Principal `json:"principal"`
	}
	data(call("conductor_access", map[string]any{}, false), &discovered)
	if discovered.Principal.ID != "person-agent" {
		t.Fatal("MCP credential changed during its process")
	}
	revoke := domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-agent"}}}
	if err := f.db.ApplyAccessConfig(f.ctx, "mcp-test-operator", revoke); err != nil {
		t.Fatal(err)
	}
	call("conductor_get_package", map[string]any{"id": humanPackage.ID}, true)
	call("conductor_create_package", map[string]any{"content": map[string]any{"intent": "must fail after revocation"}}, true)
	if _, err := session.ListResources(f.ctx, nil); err == nil {
		t.Fatal("SDK resource metadata bypassed revocation")
	}
	if _, err := session.ReadResource(f.ctx, &mcp.ReadResourceParams{URI: "conductor://workspace/team/repository/application/packages/" + humanPackage.ID}); err == nil {
		t.Fatal("resource content bypassed revocation")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close stdio session: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("MCP protocol/source leaked to stderr: %q", stderr.String())
	}
	var latest domain.Package
	f.request("reader", "team", "application", http.MethodGet, path, nil, http.StatusOK, &latest)
	if latest.Revision.Number != 2 {
		t.Fatal("failed commands changed package")
	}
}
