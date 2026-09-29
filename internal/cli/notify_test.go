package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// notifyServer records the one request `conductor notify` makes and points
// the session environment at it.
type notifyServer struct {
	path  string
	auth  string
	body  map[string]any
	calls int
}

func startNotifyServer(t *testing.T) *notifyServer {
	t.Helper()
	ns := &notifyServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ns.calls++
		ns.path, ns.auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&ns.body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONDUCTOR_NOTIFY_URL", srv.URL+"/api/sessions/s1/attention")
	t.Setenv("CONDUCTOR_NOTIFY_TOKEN", "agent-token")
	return ns
}

func runNotifyWith(t *testing.T, stdin string, args ...string) (int, string, error) {
	t.Helper()
	var stderr bytes.Buffer
	code, err := runNotify(context.Background(), args, strings.NewReader(stdin), &bytes.Buffer{}, &stderr)
	return code, stderr.String(), err
}

func TestNotifyEventFlagsPostToTheEventsRoute(t *testing.T) {
	ns := startNotifyServer(t)
	code, stderr, err := runNotifyWith(t, "", "--event", "artifact", "--message", "PR opened", "--url", "https://github.com/x/y/pull/1", "--to", "review", "--tool", "gh")
	if code != 0 || err != nil {
		t.Fatalf("exit %d %v %q", code, err, stderr)
	}
	if ns.path != "/api/sessions/s1/events" || ns.auth != "Bearer agent-token" {
		t.Fatalf("server saw %q %q", ns.path, ns.auth)
	}
	want := map[string]any{"type": "artifact", "message": "PR opened", "url": "https://github.com/x/y/pull/1", "to": "review", "tool": "gh"}
	if !reflect.DeepEqual(ns.body, want) {
		t.Fatalf("body %v, want %v", ns.body, want)
	}
}

func TestNotifyEventNeedsNoOtherFlag(t *testing.T) {
	ns := startNotifyServer(t)
	if code, stderr, err := runNotifyWith(t, "", "--event", "progress"); code != 0 || err != nil {
		t.Fatalf("exit %d %v %q", code, err, stderr)
	}
	if want := map[string]any{"type": "progress"}; !reflect.DeepEqual(ns.body, want) {
		t.Fatalf("body %v, want %v", ns.body, want)
	}
}

// Without --event the command reports an attention state as it always did.
func TestNotifyWithoutEventStillPostsAttention(t *testing.T) {
	ns := startNotifyServer(t)
	if code, stderr, err := runNotifyWith(t, "", "--state", "done", "--message", "finished"); code != 0 || err != nil {
		t.Fatalf("exit %d %v %q", code, err, stderr)
	}
	want := map[string]any{"state": "done", "message": "finished"}
	if ns.path != "/api/sessions/s1/attention" || !reflect.DeepEqual(ns.body, want) {
		t.Fatalf("server saw %q %v", ns.path, ns.body)
	}
}

// --event and a hook payload are two ways to say what happened; taking one
// silently would hide a wrong install.
func TestNotifyEventRefusesHookFlags(t *testing.T) {
	ns := startNotifyServer(t)
	for _, args := range [][]string{
		{"--event", "progress", "--claude-hook"},
		{"--event", "progress", "--codex", "{}"},
	} {
		code, _, err := runNotifyWith(t, `{"hook_event_name":"Stop"}`, args...)
		if code != 2 || err == nil || !strings.Contains(err.Error(), "--event") {
			t.Fatalf("%v: exit %d, err %v", args, code, err)
		}
	}
	if ns.calls != 0 {
		t.Fatalf("%d requests were sent", ns.calls)
	}
}

// --state has a default, so an explicit one is the only way to know the
// caller meant it; giving both says two different things and one would be
// dropped without a word.
func TestNotifyEventRefusesAnExplicitState(t *testing.T) {
	ns := startNotifyServer(t)
	for _, args := range [][]string{
		{"--event", "progress", "--state", "done"},
		{"--state", "needs_input", "--event", "progress"}, // the default, said out loud, still counts
		{"--event", "progress", "--message", "m", "--state=clear"},
	} {
		code, _, err := runNotifyWith(t, "", args...)
		if code != 2 || err == nil || !strings.Contains(err.Error(), "--event") || !strings.Contains(err.Error(), "--state") {
			t.Fatalf("%v: exit %d, err %v", args, code, err)
		}
	}
	if ns.calls != 0 {
		t.Fatalf("%d requests were sent", ns.calls)
	}
	// The default alone is not a conflict.
	if code, stderr, err := runNotifyWith(t, "", "--event", "progress"); code != 0 || err != nil {
		t.Fatalf("--event alone: exit %d %v %q", code, err, stderr)
	}
	if ns.calls != 1 {
		t.Fatalf("%d requests were sent, want 1", ns.calls)
	}
}

// Outside a session the command stays a silent no-op, --event or not.
func TestNotifyEventOutsideASessionIsSilent(t *testing.T) {
	t.Setenv("CONDUCTOR_NOTIFY_URL", "")
	t.Setenv("CONDUCTOR_NOTIFY_TOKEN", "")
	code, stderr, err := runNotifyWith(t, "", "--event", "progress", "--message", "x")
	if code != 0 || err != nil || stderr != "" {
		t.Fatalf("exit %d %v %q", code, err, stderr)
	}
}
