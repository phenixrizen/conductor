package mcpserver

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDesignAssistanceProfileAndExactSuggestion(t *testing.T) {
	_, session, f := newTestBridgeProfile(t, DesignAssistanceProfile)
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{"conductor_access", "conductor_get_design_assistance", "conductor_list_design_assistance", "conductor_propose_design_sections"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("profile tools: %v", names)
	}
	resources, err := session.ListResources(context.Background(), nil)
	if err == nil && len(resources.Resources) != 0 {
		t.Fatal("assistance profile exposes resources")
	}
	templates, err := session.ListResourceTemplates(context.Background(), nil)
	if err == nil && len(templates.ResourceTemplates) != 0 {
		t.Fatal("assistance profile exposes source templates")
	}
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "conductor_revise_package", Arguments: map[string]any{"id": "CHG-1", "expectedRevision": 1, "content": map[string]any{"title": "bypass"}}}); err == nil {
		t.Fatal("unrelated mutation available")
	}
	id, digest := strings.Repeat("a", 32), strings.Repeat("b", 64)
	before := len(f.snapshot())
	input := map[string]any{"requestDigest": digest, "sections": map[string]any{"design": "Keep this exact suggestion", "verification": ""}, "note": "Unverified author prose"}
	result := call(t, session, "conductor_propose_design_sections", map[string]any{"id": id, "idempotencyKey": "retained-assistance-key", "input": input})
	if result.IsError {
		t.Fatalf("suggestion: %+v", result)
	}
	requests := f.snapshot()[before:]
	if len(requests) != 1 || requests[0].Method != "POST" || requests[0].Path != "/api/v1/design-assistance/"+id+"/suggestion" || requests[0].Key != "retained-assistance-key" || !reflect.DeepEqual(requests[0].Body, input) {
		t.Fatalf("changed command or inserted read: %+v", requests)
	}
	before = len(f.snapshot())
	for _, raw := range []string{
		`{"requestDigest":"` + digest + `","sections":{"design":"a","design":"b"}}`,
		`{"requestDigest":"` + digest + `","sections":{"Design":"a"}}`,
		`{"requestDigest":"` + digest + `","sections":{"design":null}}`,
		`{"requestDigest":"` + digest + `","sections":{"design":"a"},"actor":"human"}`,
		`{"requestDigest":"` + digest + `","sections":{"design":"a"},"note":null}`,
	} {
		if !call(t, session, "conductor_propose_design_sections", map[string]any{"id": id, "idempotencyKey": "key", "input": json.RawMessage(raw)}).IsError {
			t.Fatal("invalid suggestion accepted")
		}
	}
	if len(f.snapshot()) != before {
		t.Fatal("invalid proposal reached API")
	}
	if !call(t, session, "conductor_propose_design_sections", map[string]any{"id": id, "idempotencyKey": "key", "input": map[string]any{"requestDigest": digest, "sections": map[string]string{"design": strings.Repeat("界", 12000)}}}).IsError {
		t.Fatal("UTF-8 byte bound not enforced")
	}
}

func TestDesignAssistanceRejectsUnknownProfile(t *testing.T) {
	if _, err := NewWithProfile(nil, "arbitrary"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}
