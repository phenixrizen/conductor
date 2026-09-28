package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMapClaudeHook(t *testing.T) {
	cases := []struct {
		in    string
		state string
		msg   string
		ok    bool
	}{
		{`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Allow Bash?"}`, "needs_input", "Allow Bash?", true},
		{`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`, "needs_input", "idle prompt", true},
		{`{"hook_event_name":"Notification","notification_type":"auth_success"}`, "working", "auth success", true},
		{`{"hook_event_name":"Stop","last_assistant_message":"All done."}`, "done", "All done.", true},
		{`{"hook_event_name":"UserPromptSubmit"}`, "working", "", true},
		{`{"hook_event_name":"SessionEnd"}`, "", "", false},
		{`not json`, "", "", false},
	}
	for i, c := range cases {
		req, ok := MapClaudeHook([]byte(c.in))
		if ok != c.ok || req.State != c.state || req.Message != c.msg {
			t.Fatalf("case %d: got %+v %v", i, req, ok)
		}
	}
}

func TestMapCodex(t *testing.T) {
	req, ok := MapCodex([]byte(`{"type":"agent-turn-complete","last-assistant-message":"Need a decision"}`))
	if !ok || req.State != "needs_input" || req.Message != "Need a decision" {
		t.Fatalf("%+v %v", req, ok)
	}
	if _, ok := MapCodex([]byte(`{"type":"other"}`)); ok {
		t.Fatal("unknown type must not map")
	}
}

func TestFromEnvAndSend(t *testing.T) {
	if _, _, err := FromEnv(func(string) string { return "" }); !errors.Is(err, ErrNotInSession) {
		t.Fatalf("expected ErrNotInSession, got %v", err)
	}
	var got Request
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL+"/api/x", "tok", Request{State: "needs_input", Message: "m"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" || got.State != "needs_input" || got.Message != "m" {
		t.Fatalf("server saw %q %+v", auth, got)
	}
	if err := Send(context.Background(), srv.URL+"/redirect", "tok", Request{State: "done"}); err == nil {
		t.Fatal("redirect must be an error")
	}
}

func TestMapClaudeHookPermissionOptions(t *testing.T) {
	req, ok := MapClaudeHook([]byte(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf x"}}`))
	if !ok || req.State != "needs_input" || req.Kind != "permission" {
		t.Fatalf("%+v %v", req, ok)
	}
	if req.Message != "Allow Bash?" {
		t.Fatalf("message %q", req.Message)
	}
	if len(req.Options) != 3 || req.Options[0].Input != "1" || req.Options[2].Input != "3" || req.Options[0].Label != "Yes" {
		t.Fatalf("options %+v", req.Options)
	}
}

func TestMapClaudeHookPermissionPromptNotification(t *testing.T) {
	req, ok := MapClaudeHook([]byte(`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`))
	if !ok || req.Kind != "permission" || len(req.Options) != 3 || req.Message != "Claude needs your permission to use Bash" {
		t.Fatalf("%+v", req)
	}
}

func TestMapClaudeHookUnknownNotificationIsPlainPrompt(t *testing.T) {
	req, ok := MapClaudeHook([]byte(`{"hook_event_name":"Notification","notification_type":"something_new","message":"hi"}`))
	if !ok || req.Kind != "prompt" || len(req.Options) != 0 || req.Message != "hi" {
		t.Fatalf("%+v", req)
	}
}

func TestMapClaudeHookPermissionWithoutToolName(t *testing.T) {
	req, _ := MapClaudeHook([]byte(`{"hook_event_name":"PermissionRequest"}`))
	if req.Message != "Allow this action?" || len(req.Options) != 3 {
		t.Fatalf("%+v", req)
	}
}

func TestMapClaudeHookStopIsDoneKind(t *testing.T) {
	req, _ := MapClaudeHook([]byte(`{"hook_event_name":"Stop","last_assistant_message":"All done."}`))
	if req.Kind != "done" {
		t.Fatalf("%+v", req)
	}
}

func TestMapCodexIsPromptKind(t *testing.T) {
	req, _ := MapCodex([]byte(`{"type":"agent-turn-complete","last-assistant-message":"Need a decision"}`))
	if req.Kind != "prompt" {
		t.Fatalf("%+v", req)
	}
}

func TestRequestJSONCarriesKindAndOptions(t *testing.T) {
	b, _ := json.Marshal(Request{State: "needs_input", Kind: "permission", Options: []Option{{Label: "Yes", Input: "1"}}})
	if !strings.Contains(string(b), `"kind":"permission"`) || !strings.Contains(string(b), `"input":"1"`) {
		t.Fatalf("json %s", b)
	}
}
