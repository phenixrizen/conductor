package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestRequestEventTargetsEventsRoute(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path; w.WriteHeader(202) }))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL+"/api/sessions/s1/attention", "tok", Request{Event: "progress", Message: "1/7"}); err != nil {
		t.Fatal(err)
	}
	if path != "/api/sessions/s1/events" {
		t.Fatalf("path %q", path)
	}
}

// sendTo posts req to srv under path and returns the path, the credential and
// the decoded JSON body the server saw.
func sendTo(t *testing.T, path string, req Request) (string, string, map[string]any) {
	t.Helper()
	var gotPath, gotAuth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body is not JSON: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL+path, "tok", req); err != nil {
		t.Fatal(err)
	}
	return gotPath, gotAuth, body
}

// The events route rejects unknown fields, so an event must travel as the
// route's own body: {"type", "message", "url", "to", "tool", …}, not as the
// attention request with an extra "event" key.
func TestRequestEventBodyIsAnEventsRequest(t *testing.T) {
	path, auth, body := sendTo(t, "/api/sessions/s1/attention", Request{
		Event: "artifact", Message: "PR opened", URL: "https://github.com/x/y/pull/1", To: "review", Tool: "gh",
	})
	if path != "/api/sessions/s1/events" || auth != "Bearer tok" {
		t.Fatalf("server saw %q %q", path, auth)
	}
	want := map[string]any{"type": "artifact", "message": "PR opened", "url": "https://github.com/x/y/pull/1", "to": "review", "tool": "gh"}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body %v, want %v", body, want)
	}
}

func TestRequestEventBodyKeepsKindAndOptions(t *testing.T) {
	_, _, body := sendTo(t, "/api/sessions/s1/attention", Request{Event: "needs_input", Message: "Allow?", Kind: "permission", Options: permissionOptions()})
	if body["type"] != "needs_input" || body["kind"] != "permission" || len(body["options"].([]any)) != 3 {
		t.Fatalf("body %v", body)
	}
	for _, key := range []string{"state", "event"} {
		if _, ok := body[key]; ok {
			t.Fatalf("body carries %q: %v", key, body)
		}
	}
}

// Without Event the request goes to /attention exactly as before, and the
// body keeps the attention shape.
func TestRequestWithoutEventKeepsTheAttentionRoute(t *testing.T) {
	path, _, body := sendTo(t, "/api/sessions/s1/attention", Request{State: "needs_input", Message: "m"})
	if path != "/api/sessions/s1/attention" {
		t.Fatalf("path %q", path)
	}
	want := map[string]any{"state": "needs_input", "message": "m"}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body %v, want %v", body, want)
	}
}

// Only a URL that ends in /attention is rewritten; anything else is where the
// caller pointed it.
func TestRequestEventLeavesOtherURLsAlone(t *testing.T) {
	for _, in := range []string{"/api/x", "/api/sessions/s1/attention/more", "/attentions"} {
		if path, _, _ := sendTo(t, in, Request{Event: "progress"}); path != in {
			t.Fatalf("%s was sent to %s", in, path)
		}
	}
}

// An attention word the server refuses with 429 (the session's bucket is
// empty) is tried again after a short wait, a few times, within the 5 s Send
// has. An event is not: a hook must not hold up its agent for one.
func TestSendRetriesARateLimitedAttentionWord(t *testing.T) {
	old := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryDelays = old })
	var calls atomic.Int32
	limited := func(first int32) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= first {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	srv := limited(2)
	for _, req := range []Request{{State: "needs_input"}, {Event: "done"}} {
		calls.Store(0)
		if err := Send(t.Context(), srv.URL+"/api/sessions/s/attention", "tok", req); err != nil || calls.Load() != 3 {
			t.Fatalf("%+v: %v after %d calls", req, err, calls.Load())
		}
	}
	calls.Store(0)
	if err := Send(t.Context(), srv.URL+"/api/sessions/s/attention", "tok", Request{Event: "tool_use", Tool: "Bash"}); err == nil || !strings.Contains(err.Error(), "429") || calls.Load() != 1 {
		t.Fatalf("event: %v after %d calls", err, calls.Load())
	}
	// A server that never lets up: the word is given up after the last wait.
	always := limited(1 << 30)
	calls.Store(0)
	if err := Send(t.Context(), always.URL+"/api/sessions/s/attention", "tok", Request{State: "working"}); err == nil || calls.Load() != int32(len(retryDelays)+1) {
		t.Fatalf("always limited: %v after %d calls", err, calls.Load())
	}
}

// The waits fit the budget, and a deadline that comes first ends them.
func TestSendRetriesWithinItsBudget(t *testing.T) {
	var total time.Duration
	for _, d := range retryDelays {
		total += d
	}
	if total >= sendTimeout {
		t.Fatalf("the waits add up to %v, not within %v", total, sendTimeout)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Send(ctx, srv.URL+"/a/attention", "tok", Request{State: "working"}); err == nil || !strings.Contains(err.Error(), "429") || time.Since(start) > time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}
