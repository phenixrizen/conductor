package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
