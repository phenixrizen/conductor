package acceptance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

// Synthetic prose proves the real native protocol workflow, not a paid model run.
func TestDesignAssistanceMCPWorkflow(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Skip("set CONDUCTOR_TEST_DATABASE_URL for signed native MCP assistance acceptance")
	}
	directory := t.TempDir()
	binaries := map[string]string{}
	for _, name := range []string{"conductor-mcp", "conductor"} {
		path := filepath.Join(directory, name)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		build := exec.CommandContext(ctx, "go", "build", "-o", path, "./cmd/"+name)
		build.Dir = "../.."
		output, err := build.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, output)
		}
		binaries[name] = path
	}
	f := newAccessFixtureWithIssuer(t, nil, nil, 90*time.Second)
	apiFor := func(actor string) *client.Client {
		t.Helper()
		c, err := client.NewAuthenticated(f.server.URL, f.tokens[actor], "team", "application")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	author, reviewer, agent := apiFor("author"), apiFor("reviewer"), apiFor("agent")
	content := domain.Content{"title": "Synthetic assistance", "intent": "Explain the new workflow", "design": "Original design", "tasks": "Retain this plan", "futureField": map[string]any{"preserved": true}}
	change, err := author.Create(f.ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	input := domain.AssistanceInput{ChangeID: change.ID, ExpectedRevision: change.Revision.Number, ExpectedDigest: change.Revision.Digest, Instruction: "Clarify design and suggest a verification approach", Sections: []string{"design", "verification"}}
	request, err := author.RequestDesignAssistance(f.ctx, "native-request", input)
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(directory, "agent.token")
	if err := os.WriteFile(tokenFile, []byte(f.tokens["agent"]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	environment := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CONDUCTOR_") {
			environment = append(environment, entry)
		}
	}
	command := exec.CommandContext(f.ctx, binaries["conductor-mcp"])
	command.Env = append(append([]string{}, environment...), "CONDUCTOR_URL="+f.server.URL, "CONDUCTOR_TOKEN_FILE="+tokenFile, "CONDUCTOR_WORKSPACE=team", "CONDUCTOR_REPOSITORY_ID=application", "CONDUCTOR_MCP_PROFILE=design-assistance")
	session, err := mcp.NewClient(&mcp.Implementation{Name: "assistance-acceptance", Version: "1"}, nil).Connect(f.ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args any, target any) {
		t.Helper()
		result, err := session.CallTool(f.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("MCP %s failed: %v %+v", name, err, result)
		}
		raw, _ := json.Marshal(result.StructuredContent)
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(envelope.Data, target) != nil {
			t.Fatal("invalid MCP response")
		}
	}
	var page domain.AssistancePage
	call("conductor_list_design_assistance", map[string]any{"changeId": change.ID}, &page)
	if len(page.Requests) != 1 || page.Requests[0].ID != request.ID {
		t.Fatal("shared request not discoverable")
	}
	var inspected domain.DesignAssistance
	call("conductor_get_design_assistance", map[string]any{"id": request.ID}, &inspected)
	if !reflect.DeepEqual(inspected.Base.Content, content) || inspected.Digest != request.Digest {
		t.Fatal("native assistant did not read captured Design")
	}
	proposal := domain.SuggestionInput{RequestDigest: inspected.Digest, Sections: map[string]string{"design": "Review section suggestions before creating a new revision.", "verification": "Exercise independent review."}, Note: "Synthetic agent-authored text, not verified evidence."}
	var suggested domain.DesignAssistance
	call("conductor_propose_design_sections", map[string]any{"id": request.ID, "idempotencyKey": "native-suggestion", "input": proposal}, &suggested)
	unchanged, err := author.Get(f.ctx, change.ID)
	if err != nil || unchanged.Revision.Digest != change.Revision.Digest {
		t.Fatal("proposal edited Change")
	}
	shared, err := reviewer.GetDesignAssistance(f.ctx, request.ID)
	if err != nil || shared.Suggestion == nil || shared.Suggestion.AgentID != "person-agent" {
		t.Fatal("suggestion not shared")
	}
	apply := domain.ApplySuggestionInput{RequestDigest: request.Digest, SuggestionDigest: suggested.Suggestion.Digest, ExpectedRevision: change.Revision.Number, ExpectedDigest: change.Revision.Digest, Sections: []string{"design"}}
	if _, err := reviewer.ApplyDesignSuggestion(f.ctx, request.ID, "not-requester", apply); err == nil {
		t.Fatal("other human applied suggestion")
	}
	if _, err := agent.ApplyDesignSuggestion(f.ctx, request.ID, "agent-apply", apply); err == nil {
		t.Fatal("agent applied suggestion")
	}
	applied, err := author.ApplyDesignSuggestion(f.ctx, request.ID, "native-apply", apply)
	if err != nil {
		t.Fatal(err)
	}
	current, err := author.Get(f.ctx, change.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision.Number != 2 || current.Approved || current.Revision.Author != "person-author" || current.Revision.Content["design"] != proposal.Sections["design"] || !reflect.DeepEqual(current.Revision.Content["futureField"], content["futureField"]) || current.Revision.Content["tasks"] != content["tasks"] {
		t.Fatal("incorrect application or attribution")
	}
	if _, present := current.Revision.Content["verification"]; present {
		t.Fatal("unselected proposed section applied")
	}
	if _, err := author.Submit(f.ctx, change.ID, 2); err != nil {
		t.Fatal(err)
	}
	_, err = author.Approve(f.ctx, change.ID, 2, current.Revision.Digest)
	assertAPIError(t, err, http.StatusUnprocessableEntity, "approval_rejected")
	if approved, err := reviewer.Approve(f.ctx, change.ID, 2, current.Revision.Digest); err != nil || !approved.Approved {
		t.Fatal("independent review failed", err)
	}
	// Same exact key recovers the retained fact even after the new revision exists.
	recovered, err := author.ApplyDesignSuggestion(f.ctx, request.ID, "native-apply", apply)
	if err != nil || recovered.Application.Digest != applied.Application.Digest {
		t.Fatal("application recovery changed receipt", err)
	}
	call("conductor_propose_design_sections", map[string]any{"id": request.ID, "idempotencyKey": "native-suggestion", "input": proposal}, &suggested)
	check := exec.CommandContext(f.ctx, binaries["conductor"], "assistant-config", "--check", "--mcp-binary", binaries["conductor-mcp"], "--token-file", tokenFile, "--url", f.server.URL, "--workspace", "team", "--repository-id", "application")
	check.Env = environment
	if output, err := check.CombinedOutput(); err != nil || !strings.Contains(string(output), "four-tool Design assistance profile verified") {
		t.Fatalf("connection checker: %v %s", err, output)
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-assistance-revoke", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-agent"}}}); err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(f.ctx, &mcp.CallToolParams{Name: "conductor_get_design_assistance", Arguments: map[string]any{"id": request.ID}})
	if err != nil || !result.IsError {
		t.Fatal("revoked native agent retained access")
	}
	_ = session.Close()
	f.reopenAPIAndStore()
	retained, err := apiFor("author").GetDesignAssistance(f.ctx, request.ID)
	if err != nil || retained.Application.Digest != applied.Application.Digest || retained.Suggestion.Digest != suggested.Suggestion.Digest {
		t.Fatal("facts did not survive reopen", err)
	}
}
