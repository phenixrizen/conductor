package notify

import (
	"strings"
	"testing"
)

func TestMapCopilotHook(t *testing.T) {
	r, ok := MapCopilotHook([]byte(`{"sessionId":"s","timestamp":1,"cwd":"/x","hook_event_name":"Notification","message":"Allow bash?","notification_type":"permission_prompt"}`))
	if !ok || r.State != "needs_input" || r.Kind != "permission" {
		t.Fatalf("%+v", r)
	}
	r, ok = MapCopilotHook([]byte(`{"sessionId":"s","timestamp":1,"cwd":"/x","toolName":"bash","toolArgs":{},"toolResult":{"resultType":"success","textResultForLlm":"ok"}}`))
	if !ok || r.Event != "tool_use" || r.Tool != "bash" {
		t.Fatalf("%+v", r)
	}
	if _, ok := MapCopilotHook([]byte(`{"sessionId":"s"}`)); ok {
		t.Fatal("empty payload must not map")
	}
}

// Copilot names few of its events: the fields that are there decide.
func TestMapCopilotHookByFields(t *testing.T) {
	cases := []struct {
		in   string
		want Request
	}{
		{`{"sessionId":"s","message":"Allow bash?","notification_type":"permission_prompt"}`, Request{State: "needs_input", Message: "Allow bash?", Kind: "permission"}},
		{`{"sessionId":"s","message":"What next?","notification_type":"idle_prompt"}`, Request{State: "needs_input", Message: "What next?", Kind: "prompt"}},
		{`{"sessionId":"s","notification_type":"idle_prompt"}`, Request{State: "needs_input", Message: "Copilot needs your input", Kind: "prompt"}},
		{`{"sessionId":"s","stopReason":"end_turn"}`, Request{State: "done", Kind: "done"}},
		{`{"sessionId":"s","prompt":"fix the tests"}`, Request{State: "working"}},
		{`{"sessionId":"s","error":{"message":"network timeout","name":"TimeoutError"}}`, Request{Event: "error", Message: "network timeout"}},
		{`{"sessionId":"s","error":"disk full"}`, Request{Event: "error", Message: "disk full"}},
		// An event name, when Copilot sends one, is enough on its own.
		{`{"hook_event_name":"agentStop"}`, Request{State: "done", Kind: "done"}},
		{`{"hook_event_name":"userPromptSubmitted"}`, Request{State: "working"}},
		{`{"hook_event_name":"postToolUse","toolName":"edit"}`, Request{Event: "tool_use", Tool: "edit"}},
		{`{"hook_event_name":"errorOccurred","error":{"message":"boom"}}`, Request{Event: "error", Message: "boom"}},
	}
	for _, c := range cases {
		got, ok := MapCopilotHook([]byte(c.in))
		if !ok || !sameRequest(got, c.want) {
			t.Errorf("%s: %+v %v, want %+v", c.in, got, ok, c.want)
		}
	}
	for _, in := range []string{`{"sessionId":"s","source":"new"}`, `{"toolName":"bash","toolArgs":{}}`, `{"toolResult":null}`, `not json`} {
		if got, ok := MapCopilotHook([]byte(in)); ok {
			t.Errorf("%s mapped to %+v", in, got)
		}
	}
}

func TestMapCursorHook(t *testing.T) {
	r, ok := MapCursorHook([]byte(`{"conversation_id":"c","generation_id":"g","hook_event_name":"stop","status":"completed","workspace_roots":["/x"]}`))
	if !ok || r.State != "done" || r.Kind != "done" {
		t.Fatalf("stop: %+v %v", r, ok)
	}
	// The file's name, not its path: the server keeps 100 bytes of a tool.
	r, ok = MapCursorHook([]byte(`{"hook_event_name":"afterFileEdit","file_path":"/x/` + strings.Repeat("deep/", 30) + `main.go","edits":[{"old_string":"a","new_string":"b"}]}`))
	if !ok || r.Event != "tool_use" || r.Tool != "edit main.go" {
		t.Fatalf("afterFileEdit: %+v %v", r, ok)
	}
	if r, _ := MapCursorHook([]byte(`{"hook_event_name":"afterFileEdit"}`)); r.Tool != "edit" {
		t.Fatalf("afterFileEdit without a file: %+v", r)
	}
	r, ok = MapCursorHook([]byte(`{"hook_event_name":"postToolUse","tool_name":"Shell"}`))
	if !ok || r.Event != "tool_use" || r.Tool != "Shell" {
		t.Fatalf("postToolUse: %+v %v", r, ok)
	}
	for _, in := range []string{`{"hook_event_name":"beforeShellExecution","command":"ls"}`, `{"status":"completed"}`, `[]`} {
		if got, ok := MapCursorHook([]byte(in)); ok {
			t.Errorf("%s mapped to %+v", in, got)
		}
	}
}

// Antigravity's payload names no event: a toolCall is PostToolUse, a
// terminationReason is Stop.
func TestMapAgyHook(t *testing.T) {
	r, ok := MapAgyHook([]byte(`{"terminationReason":"completed","conversationId":"c"}`))
	if !ok || r.State != "done" || r.Message != "completed" || r.Kind != "done" {
		t.Fatalf("stop: %+v %v", r, ok)
	}
	r, ok = MapAgyHook([]byte(`{"toolCall":{"name":"run_command","args":{"command":"ls"}}}`))
	if !ok || r.Event != "tool_use" || r.Tool != "run_command" {
		t.Fatalf("tool: %+v %v", r, ok)
	}
	for _, in := range []string{`{}`, `{"toolCall":null}`, `{"other":1}`, `nope`} {
		if got, ok := MapAgyHook([]byte(in)); ok {
			t.Errorf("%s mapped to %+v", in, got)
		}
	}
}

func TestMapGooseHook(t *testing.T) {
	r, ok := MapGooseHook([]byte(`{"event":"Stop","session_id":"s"}`))
	if !ok || r.State != "done" || r.Kind != "done" {
		t.Fatalf("stop: %+v %v", r, ok)
	}
	r, ok = MapGooseHook([]byte(`{"event":"PostToolUse","tool_name":"developer__shell"}`))
	if !ok || r.Event != "tool_use" || r.Tool != "developer__shell" {
		t.Fatalf("tool: %+v %v", r, ok)
	}
	for _, in := range []string{`{"event":"SessionStart"}`, `{}`, `nope`} {
		if got, ok := MapGooseHook([]byte(in)); ok {
			t.Errorf("%s mapped to %+v", in, got)
		}
	}
}

func TestMapCodexHook(t *testing.T) {
	r, ok := MapCodexHook([]byte(`{"session_id":"s","hook_event_name":"Stop","last_assistant_message":"All set."}`))
	if !ok || r.State != "done" || r.Kind != "done" || r.Message != "All set." {
		t.Fatalf("stop: %+v %v", r, ok)
	}
	r, ok = MapCodexHook([]byte(`{"hook_event_name":"PostToolUse","tool_name":"shell","tool_input":{"command":["ls"]}}`))
	if !ok || r.Event != "tool_use" || r.Tool != "shell" {
		t.Fatalf("tool: %+v %v", r, ok)
	}
	// Codex's permission keys are not verified yet: no quick-reply options.
	r, ok = MapCodexHook([]byte(`{"hook_event_name":"PermissionRequest","tool_name":"shell"}`))
	if !ok || r.State != "needs_input" || r.Kind != "permission" || r.Message != "Allow shell?" || len(r.Options) != 0 {
		t.Fatalf("permission: %+v %v", r, ok)
	}
	if r, ok := MapCodexHook([]byte(`{"hook_event_name":"PermissionRequest"}`)); !ok || r.Message != "Codex asks for permission" {
		t.Fatalf("permission without a tool: %+v %v", r, ok)
	}
	if r, ok := MapCodexHook([]byte(`{"hook_event_name":"PreToolUse","tool_name":"shell"}`)); !ok || r.State != "working" || r.Event != "" {
		t.Fatalf("pre tool use: %+v %v", r, ok)
	}
	// The payload of Codex's notify program is MapCodex's, not this.
	if _, ok := MapCodexHook([]byte(`{"type":"agent-turn-complete"}`)); ok {
		t.Fatal("a notify payload mapped")
	}
}

// Claude Code's tool hooks become events; the rest keep their states.
func TestMapClaudeHookToolEvents(t *testing.T) {
	cases := []struct {
		in   string
		want Request
	}{
		{`{"hook_event_name":"PermissionDenied","tool_name":"Bash"}`, Request{Event: "tool_denied", Tool: "Bash"}},
		{`{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{}}`, Request{Event: "tool_use", Tool: "Edit"}},
		{`{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","error":"exit status 1"}`, Request{Event: "error", Message: "exit status 1", Tool: "Bash"}},
		{`{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","error":{"message":"timed out"}}`, Request{Event: "error", Message: "timed out", Tool: "Bash"}},
		{`{"hook_event_name":"SubagentStop"}`, Request{Event: "progress", Message: "subagent finished"}},
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`, Request{State: "working"}},
	}
	for _, c := range cases {
		got, ok := MapClaudeHook([]byte(c.in))
		if !ok || !sameRequest(got, c.want) {
			t.Errorf("%s: %+v %v, want %+v", c.in, got, ok, c.want)
		}
	}
}

// Long messages are cut to 200 bytes, on a character boundary.
func TestMappedMessagesAreShortAndValid(t *testing.T) {
	long := strings.Repeat("é", 150) // 300 bytes
	r, _ := MapAgyHook([]byte(`{"terminationReason":"` + long + `"}`))
	if len(r.Message) > 200 || !strings.HasPrefix(long, r.Message) || len(r.Message) < 199 {
		t.Fatalf("message of %d bytes: %q", len(r.Message), r.Message)
	}
}

func sameRequest(a, b Request) bool {
	return a.State == b.State && a.Message == b.Message && a.Kind == b.Kind && len(a.Options) == len(b.Options) &&
		a.Event == b.Event && a.URL == b.URL && a.To == b.To && a.Tool == b.Tool
}

// The mappers carry the agent's own session id on an attention state, and
// say when the payload is of a turn: Claude Code's session_id, Codex's
// thread-id. Codex's title thread reports nothing.
func TestMappersCarryTheAgentSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  func() (Request, bool)
		id   string
		turn bool
	}{
		{"claude stop", func() (Request, bool) {
			return MapClaudeHook([]byte(`{"hook_event_name":"Stop","session_id":"3f80c8bd-0000-4000-8000-000000000001"}`))
		}, "3f80c8bd-0000-4000-8000-000000000001", true},
		{"claude notification", func() (Request, bool) {
			return MapClaudeHook([]byte(`{"hook_event_name":"Notification","message":"hi","session_id":"s1"}`))
		}, "s1", false},
		{"codex turn", func() (Request, bool) {
			return MapCodex([]byte(`{"type":"agent-turn-complete","thread-id":"01a0fc72-f99b-7331-aec3-818001309536","input-messages":["reply READY"],"last-assistant-message":"READY"}`))
		}, "01a0fc72-f99b-7331-aec3-818001309536", true},
	} {
		req, ok := tc.got()
		if !ok || req.AgentSession != tc.id || req.Turn != tc.turn {
			t.Errorf("%s: %+v %v", tc.name, req, ok)
		}
	}
	for name, got := range map[string]func() (Request, bool){
		"codex hook": func() (Request, bool) { return MapCodexHook([]byte(`{"hook_event_name":"Stop","session_id":"t1"}`)) },
		"copilot": func() (Request, bool) {
			return MapCopilotHook([]byte(`{"hook_event_name":"agentStop","sessionId":"t1"}`))
		},
		"cursor": func() (Request, bool) {
			return MapCursorHook([]byte(`{"hook_event_name":"stop","conversation_id":"t1"}`))
		},
		"agy": func() (Request, bool) {
			return MapAgyHook([]byte(`{"terminationReason":"done","conversationId":"t1"}`))
		},
	} {
		if req, ok := got(); !ok || req.AgentSession != "t1" || !req.Turn {
			t.Errorf("%s: %+v %v", name, req, ok)
		}
	}
	if req, _ := MapClaudeHook([]byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash","session_id":"s1"}`)); req.AgentSession != "" {
		t.Errorf("an event carried the id: %+v", req)
	}
	if _, ok := MapCodex([]byte(`{"type":"agent-turn-complete","thread-id":"01a0fc76-4817","input-messages":["Generate a concise, single-line task title for this conversation"]}`)); ok {
		t.Error("the title thread's turn was mapped")
	}
}
