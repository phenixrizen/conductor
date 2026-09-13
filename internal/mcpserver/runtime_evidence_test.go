package mcpserver

import (
	"strings"
	"testing"
	"time"
)

func TestRuntimeMCPExactRequestAndNoInjectedQueryOrAuthority(t *testing.T) {
	_, session, f := newTestBridge(t)
	now := time.Now().UTC().Truncate(time.Second)
	id := strings.Repeat("a", 32)
	digest := strings.Repeat("b", 64)
	input := map[string]any{"deliveryId": id, "deliveryDigest": digest, "observationSequence": 1, "deploymentId": "deployment", "commit": strings.Repeat("c", 40), "environment": "production", "start": now.Add(-time.Minute).Format(time.RFC3339), "end": now.Add(-30 * time.Second).Format(time.RFC3339), "requirements": []any{}}
	before := len(f.snapshot())
	result := call(t, session, "conductor_collect_runtime_evidence", map[string]any{"idempotencyKey": "captured-key", "input": input})
	if result.IsError {
		t.Fatalf("runtime tool failed %+v", result)
	}
	requests := f.snapshot()[before:]
	if len(requests) != 1 || requests[0].Method != "POST" || requests[0].Path != "/api/v1/runtime-evidence" || requests[0].Key != "captured-key" || requests[0].Body["deliveryDigest"] != digest {
		t.Fatal("request changed or preflight inserted")
	}
	for _, field := range []string{"actor", "workspaceId", "repositoryId", "token", "query", "backendId", "threshold"} {
		bad := map[string]any{}
		for k, v := range input {
			bad[k] = v
		}
		bad[field] = "forged"
		if result := call(t, session, "conductor_collect_runtime_evidence", map[string]any{"idempotencyKey": "captured-key", "input": bad}); !result.IsError {
			t.Fatalf("accepted %s", field)
		}
	}
	if result := call(t, session, "conductor_get_runtime_evidence", map[string]any{"id": id}); result.IsError {
		t.Fatal("runtime inspection tool absent")
	}
	if result := call(t, session, "conductor_list_runtime_evidence", map[string]any{"limit": 1}); result.IsError {
		t.Fatal("runtime discovery tool absent")
	}
}
