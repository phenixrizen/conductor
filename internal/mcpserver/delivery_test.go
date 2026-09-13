package mcpserver

import (
	"strings"
	"testing"
)

func TestDeliveryMCPPreservesExactInputAndExcludesAuthority(t *testing.T) {
	_, session, f := newTestBridge(t)
	id := strings.Repeat("a", 32)
	digest := strings.Repeat("b", 64)
	input := map[string]any{"runId": id, "taskId": id, "artifactDigest": digest, "baseBranch": "main", "title": "Fixture", "description": "source is data"}
	before := len(f.snapshot())
	result := call(t, session, "conductor_propose_delivery", map[string]any{"idempotencyKey": "fixed-key", "input": input})
	if result.IsError {
		t.Fatalf("proposal failed %+v", result)
	}
	requests := f.snapshot()[before:]
	if len(requests) != 1 || requests[0].Method != "POST" || requests[0].Path != "/api/v1/repository-deliveries" || requests[0].Key != "fixed-key" || requests[0].Body["artifactDigest"] != digest {
		t.Fatalf("changed proposal or preflight refresh %+v", requests)
	}
	for _, field := range []string{"actor", "workspaceId", "repositoryId", "token"} {
		bad := map[string]any{}
		for k, v := range input {
			bad[k] = v
		}
		bad[field] = "forged"
		if result := call(t, session, "conductor_propose_delivery", map[string]any{"idempotencyKey": "fixed-key", "input": bad}); !result.IsError {
			t.Fatalf("accepted %s", field)
		}
	}
}

func (f *apiFixture) snapshot() []observedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]observedRequest(nil), f.requests...)
}
