package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTaskServer answers the task routes of session s1: a task of two
// pages, and a result route that refuses a summary of "refuse" as the
// server refuses a dirty worktree.
func fakeTaskServer(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var seen []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/s1/task", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, map[string]any{"method": r.Method, "path": r.URL.Path, "page": r.URL.Query().Get("page"), "auth": r.Header.Get("Authorization")})
		switch r.URL.Query().Get("page") {
		case "", "1":
			_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"task": map[string]any{"id": "build"}}, "markdown": "# Task build\n\nBuild it.\n", "page": 1, "pages": 2, "bytes": 30})
		case "2":
			_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"task": map[string]any{"id": "build"}}, "markdown": "More.\n", "page": 2, "pages": 2, "bytes": 30})
		default:
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"the task has 2 pages"}}`))
		}
	})
	mux.HandleFunc("POST /api/sessions/s1/task/result", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen = append(seen, map[string]any{"method": r.Method, "path": r.URL.Path, "auth": r.Header.Get("Authorization"), "body": body})
		if body["summary"] == "refuse" {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":{"code":"dirty_worktree","message":"the worktree has changes not committed: commit them, then submit (a.txt)"}}`))
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"id": "build", "state": "verifying"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &seen
}

func task(t *testing.T, stdin string, args ...string) (int, string, error) {
	t.Helper()
	var out strings.Builder
	code, err := runTask(context.Background(), args, strings.NewReader(stdin), &out, io.Discard)
	return code, out.String(), err
}

// task context prints every page of the task in order, one page when
// asked, or the task's JSON, with the session's token.
func TestTaskContextPrintsTheTask(t *testing.T) {
	srv, seen := fakeTaskServer(t)
	inSession(t, srv)
	code, out, err := task(t, "", "context")
	if code != 0 || err != nil || out != "# Task build\n\nBuild it.\nMore.\n" {
		t.Fatalf("context: %d %v %q", code, err, out)
	}
	if len(*seen) != 2 || (*seen)[0]["auth"] != "Bearer agent-tok" || (*seen)[1]["page"] != "2" {
		t.Fatalf("seen %v", *seen)
	}
	if code, out, _ := task(t, "", "context", "--page", "2"); code != 0 || out != "More.\n" {
		t.Fatalf("page 2: %d %q", code, out)
	}
	if code, out, _ := task(t, "", "context", "--json"); code != 0 || !strings.Contains(out, `"id": "build"`) {
		t.Fatalf("json: %d %q", code, out)
	}
	if code, _, err := task(t, "", "context", "--page", "3"); code != 1 || err == nil || !strings.Contains(err.Error(), "task: the task has 2 pages (invalid_request)") {
		t.Fatalf("page 3: %d %v", code, err)
	}
}

// task result posts the flags as the result, findings parsed from their
// words, or the JSON of --file; the server's refusal is the exit.
func TestTaskResultSubmitsTheFlagsOrTheFile(t *testing.T) {
	srv, seen := fakeTaskServer(t)
	inSession(t, srv)
	commit := strings.Repeat("ab", 20)
	code, out, err := task(t, "", "result", "--summary", "did it", "--issue", "a", "--issue", "b", "--commit", commit)
	if code != 0 || err != nil || out != "submitted: task build is verifying\n" {
		t.Fatalf("result: %d %v %q", code, err, out)
	}
	body := (*seen)[0]["body"].(map[string]any)
	if body["summary"] != "did it" || body["commit"] != commit || len(body["issues"].([]any)) != 2 || body["verdict"] != nil || body["findings"] != nil {
		t.Fatalf("body %v", body)
	}
	code, _, err = task(t, "", "result", "--verdict", "needs_changes", "--finding", "major:x.go:12 fix it", "--finding", "nit say more", "--finding", "minor:README no line")
	if code != 0 || err != nil {
		t.Fatalf("review: %d %v", code, err)
	}
	body = (*seen)[1]["body"].(map[string]any)
	findings := body["findings"].([]any)
	if body["verdict"] != "needs_changes" || body["summary"] != nil || len(findings) != 3 {
		t.Fatalf("body %v", body)
	}
	if f := findings[0].(map[string]any); f["severity"] != "major" || f["path"] != "x.go" || f["line"] != float64(12) || f["message"] != "fix it" {
		t.Fatalf("finding 1: %v", f)
	}
	if f := findings[1].(map[string]any); f["severity"] != "nit" || f["path"] != nil || f["line"] != nil || f["message"] != "say more" {
		t.Fatalf("finding 2: %v", f)
	}
	if f := findings[2].(map[string]any); f["severity"] != "minor" || f["path"] != "README" || f["line"] != nil {
		t.Fatalf("finding 3: %v", f)
	}
	code, _, err = task(t, `{"summary":"from the file","issues":["x"]}`, "result", "--file", "-")
	if code != 0 || err != nil {
		t.Fatalf("file: %d %v", code, err)
	}
	if body = (*seen)[2]["body"].(map[string]any); body["summary"] != "from the file" {
		t.Fatalf("body %v", body)
	}
	// Usage errors, before anything is sent.
	for _, args := range [][]string{
		{"result", "--file", "-", "--summary", "both"},
		{"result", "--finding", "nowords"},
		{"result", "--finding", "huge:x.go:1 severity unknown"},
		{"result", "--finding", "major:x.go:zero bad line"},
		{"result", "--file", "-"}, // stdin empty below
		{"result", "extra"},
		{"nope"},
		{},
	} {
		if code, _, _ := task(t, "", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, _, err := task(t, `{"summary":"x","nope":1}`, "result", "--file", "-"); code != 2 || err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("unknown field: %d %v", code, err)
	}
	if n := len(*seen); n != 3 {
		t.Fatalf("%d requests after the usage errors, want 3", n)
	}
	// The server's refusal is exit 1 with its words.
	if code, _, err := task(t, "", "result", "--summary", "refuse"); code != 1 || err == nil || !strings.Contains(err.Error(), "task: the worktree has changes not committed: commit them, then submit (a.txt) (dirty_worktree)") {
		t.Fatalf("refused: %d %v", code, err)
	}
	if code, _, _ := task(t, "", "-h"); code != 0 {
		t.Fatal("-h")
	}
}

// Outside a Conductor session the task commands say so and exit 1: an
// agent that calls them by mistake must see it, unlike crew's silence.
func TestTaskSaysSoOutsideASession(t *testing.T) {
	for _, k := range []string{"CONDUCTOR_SESSION_ID", "CONDUCTOR_NOTIFY_URL", "CONDUCTOR_NOTIFY_TOKEN"} {
		t.Setenv(k, "")
	}
	for _, args := range [][]string{{"context"}, {"result", "--summary", "x"}} {
		code, out, err := task(t, "", args...)
		if code != 1 || out != "" || err == nil || !strings.Contains(err.Error(), "not in a Conductor session") {
			t.Errorf("%v: %d %q %v", args, code, out, err)
		}
	}
}
