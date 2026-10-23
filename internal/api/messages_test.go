package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// A member's session sends messages, reads its inbox and acknowledges;
// an attempt reports a blocker, which its session asks and a working
// report clears, and saves a checkpoint (rows 9 and 10 of #334).
func TestMessagesBlockersAndCheckpointsThroughTheRoutes(t *testing.T) {
	e := readyEnv(t, nil)
	e.gitRepo(t)
	run, release, err := e.srv.runs.LaunchNamed(t.Context(), managedCrew(e.root), "")
	release()
	if err != nil {
		t.Fatal(err)
	}
	a := runningAttempt(t, e, run.ID, "build", 1)
	sid := a["sessionId"].(string)
	tok := e.agentToken(sid)
	base := "/api/sessions/" + sid

	// Messages: to a member, kept; refusals; the inbox full at fifty.
	resp, out := e.do("POST", base+"/messages", tok, map[string]any{"to": "writer", "text": "the API is final"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("send: %d %v", resp.StatusCode, out)
	}
	if m := out["message"].(map[string]any); m["seq"] != float64(1) || m["from"] != "lead" || m["to"] != "writer" || m["deliveredAt"] != nil {
		t.Fatalf("the message: %v", m)
	}
	if resp, out := e.do("POST", base+"/messages", tok, map[string]any{"to": "nobody", "text": "x"}); resp.StatusCode != http.StatusNotFound || errCodeOf(out) != "no_member" {
		t.Fatalf("to nobody: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("POST", base+"/messages", tok, map[string]any{"to": "writer", "text": ""}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty: %d", resp.StatusCode)
	}
	if resp, _ := e.do("POST", base+"/messages", "", map[string]any{"to": "writer", "text": "x"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	for i := 2; i <= 50; i++ {
		if resp, out := e.do("POST", base+"/messages", adminToken, map[string]any{"to": "writer", "text": fmt.Sprintf("m%d", i)}); resp.StatusCode != http.StatusCreated {
			t.Fatalf("message %d: %d %v", i, resp.StatusCode, out)
		}
	}
	if resp, out := e.do("POST", base+"/messages", tok, map[string]any{"to": "writer", "text": "one too many"}); resp.StatusCode != http.StatusTooManyRequests || errCodeOf(out) != "inbox_full" {
		t.Fatalf("the 51st: %d %v", resp.StatusCode, out)
	}
	if resp, out := e.do("GET", "/api/runs/"+run.ID+"/messages", adminToken, nil); resp.StatusCode != http.StatusOK || len(out["messages"].([]any)) != 50 {
		t.Fatalf("the run's messages: %d %v", resp.StatusCode, out)
	}
	// lead's own inbox is empty; a bad after is refused.
	if resp, out := e.do("GET", base+"/inbox", tok, nil); resp.StatusCode != http.StatusOK || len(out["messages"].([]any)) != 0 {
		t.Fatalf("lead's inbox: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("GET", base+"/inbox?after=x", tok, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad after: %d", resp.StatusCode)
	}
	if resp, out := e.do("POST", base+"/inbox/ack", tok, map[string]any{"upTo": 5}); resp.StatusCode != http.StatusOK || out["acked"] != float64(0) {
		t.Fatalf("ack of nothing: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("POST", base+"/inbox/ack", tok, map[string]any{"upTo": 0}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ack 0: %d", resp.StatusCode)
	}

	// A blocker: the task shows it, the session asks the message; a working report clears it.
	resp, out = e.do("POST", base+"/task/blocked", tok, map[string]any{"reason": "no token", "message": "Set NPM_TOKEN, then answer here"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("blocked: %d %v", resp.StatusCode, out)
	}
	if b, _ := out["task"].(map[string]any)["blocker"].(map[string]any); b == nil || b["reason"] != "no token" || b["by"] != "lead" {
		t.Fatalf("the task's blocker: %v", out)
	}
	_, out = e.do("GET", base, adminToken, nil)
	if att, _ := out["session"].(map[string]any)["attention"].(map[string]any); att == nil || att["state"] != "needs_input" || att["message"] != "Set NPM_TOKEN, then answer here" {
		t.Fatalf("the session's attention: %v", out["session"].(map[string]any)["attention"])
	}
	if resp, _ := e.do("POST", base+"/task/blocked", tok, map[string]any{"reason": ""}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no reason: %d", resp.StatusCode)
	}
	if resp, out := e.do("POST", base+"/attention", tok, map[string]any{"state": "working"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("working: %d %v", resp.StatusCode, out)
	}
	taskVia(t, e, run.ID, "build", "unblocked", func(task map[string]any) bool { return task["blocker"] == nil })

	// A checkpoint, tied to the worktree's commit (the base: nothing committed yet).
	resp, out = e.do("POST", base+"/task/checkpoint", tok, map[string]any{"done": "the handlers", "remaining": "the docs", "notes": "docs/api.md"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkpoint: %d %v", resp.StatusCode, out)
	}
	cp, _ := out["attempt"].(map[string]any)["checkpoint"].(map[string]any)
	if cp == nil || cp["done"] != "the handlers" || cp["commit"] != a["start"] {
		t.Fatalf("the checkpoint: %v (start %v)", out, a["start"])
	}
	if resp, _ := e.do("POST", base+"/task/checkpoint", tok, map[string]any{"done": "", "remaining": "x"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no done: %d", resp.StatusCode)
	}
	_, out = e.do("GET", "/api/runs/"+run.ID+"/tasks/build", adminToken, nil)
	if attempts := out["attempts"].([]any); len(attempts) != 1 || attempts[0].(map[string]any)["checkpoint"] == nil {
		t.Fatalf("the task's attempts: %v", out)
	}
	// A session that is no attempt has none of these.
	for _, path := range []string{"/messages", "/inbox/ack", "/task/blocked", "/task/checkpoint"} {
		if resp, out := e.do("POST", "/api/sessions/nope"+path, adminToken, map[string]any{"to": "x", "text": "y", "upTo": 1, "reason": "r", "done": "d", "remaining": "r"}); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s on no session: %d %v", path, resp.StatusCode, out)
		}
	}
	if resp, out := e.do("POST", "/api/runs/"+run.ID+"/stop", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop: %d %v", resp.StatusCode, out)
	}
	_ = time.Second
}
