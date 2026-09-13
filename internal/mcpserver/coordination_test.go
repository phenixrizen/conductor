package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/domain"
	"reflect"
	"strings"
	"testing"
)

func TestCoordinationProposalPreservesPinsAndCannotAuthorize(t *testing.T) {
	_, session, f := newTestBridge(t)
	digest := strings.Repeat("a", 64)
	plan := domain.CoordinationPlan{SchemaVersion: 1, GraphID: strings.Repeat("b", 32), GraphDigest: digest, Packages: []domain.PackagePin{{ChangeID: "design", RepositoryID: "application", Revision: 2, Digest: digest}}, Repositories: []domain.CoordinationRepository{{RepositoryID: "application", Commit: strings.Repeat("c", 40), CollectionID: strings.Repeat("d", 32), ReceiptDigest: digest, FullSourceDigest: digest}}, Tasks: []domain.CoordinationTask{{ID: "inspect", Perspective: "architect", Profile: "reviewed-profile", ProfileDigest: digest, Image: "sha256:" + digest, Prompt: "Inspect retained synthetic source", Scopes: []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{}}}, Checks: []domain.VerificationCommand{{ID: "echo", RepositoryID: "application", Argv: []string{"/bin/echo", ""}, TimeoutSeconds: 30}}, TimeoutSeconds: 60}}, MaxParallel: 1}
	plan.Tasks[0].Checks[0].Requirements = []domain.VerificationRequirement{{ChangeID: "design", Revision: 2, Digest: digest, CriterionID: "source-readable"}}
	input := map[string]any{"idempotencyKey": "same-plan", "plan": plan}
	f.mu.Lock()
	before := len(f.requests)
	f.mu.Unlock()
	for range 2 {
		if r := call(t, session, "conductor_propose_run", input); r.IsError {
			t.Fatalf("proposal: %+v", r)
		}
	}
	f.mu.Lock()
	writes := append([]observedRequest(nil), f.requests[before:]...)
	f.mu.Unlock()
	if len(writes) != 2 || writes[0].Method != "POST" || writes[0].Path != "/api/v1/coordination-runs" || writes[0].Key != "same-plan" || !reflect.DeepEqual(writes[0], writes[1]) {
		t.Fatalf("proposal refreshed or altered retry: %+v", writes)
	}
	normalized, _ := domain.NormalizeCoordinationPlan(plan)
	raw, _ := json.Marshal(normalized)
	var wanted map[string]any
	_ = json.Unmarshal(raw, &wanted)
	if !reflect.DeepEqual(writes[0].Body, wanted) {
		t.Fatal("proposal pins changed")
	}
	for _, name := range []string{"conductor_authorize_run", "conductor_cancel_run"} {
		if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"id": strings.Repeat("b", 32), "digest": digest}}); err == nil {
			t.Fatalf("human authority exposed: %s", name)
		}
	}
	for _, bad := range []map[string]any{
		{"idempotencyKey": "scope-injection", "plan": plan, "workspaceId": "other"},
		{"idempotencyKey": "nested-injection", "plan": map[string]any{"schemaVersion": 1, "actor": "human"}},
	} {
		if r := call(t, session, "conductor_propose_run", bad); !r.IsError {
			t.Fatal("injected proposal accepted")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != before+2 {
		t.Fatal("unregistered or invalid command reached API")
	}
}
