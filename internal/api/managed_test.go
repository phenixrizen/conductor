package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/crew"
)

// A managed run launched through the server's engine makes its attempt a
// server session: a place reserved and taken, the session tagged with its
// task and attempt, its worktree and task file in the repository (row 5 of
// #334; the launch route stays closed until row 6).
func TestAManagedRunsAttemptIsAServerSession(t *testing.T) {
	e := newTestEnv(t, nil)
	e.gitRepo(t)
	c := crew.Crew{ID: "managed", Name: "Managed", Goal: "build it", Cwd: e.root, Where: crew.WhereServer, Isolation: crew.IsolationWorktree,
		Members: []crew.Member{{Name: "lead", AgentID: "cat", Start: crew.Start{When: crew.StartTask}}},
		Tasks:   []crew.Task{{ID: "build", Member: "lead", Kind: crew.TaskWork, Instructions: "Build it."}}}
	run, release, err := e.srv.runs.LaunchNamed(t.Context(), c, "")
	release()
	if err != nil {
		t.Fatal(err)
	}
	var sessionID string
	deadline := time.Now().Add(20 * time.Second)
	for sessionID == "" {
		resp, out := e.do("GET", "/api/runs/"+run.ID, adminToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("get run: %d %v", resp.StatusCode, out)
		}
		r := out["run"].(map[string]any)
		if tasks, _ := r["tasks"].([]any); len(tasks) == 1 {
			task := tasks[0].(map[string]any)
			if a, _ := task["attempt"].(map[string]any); a != nil {
				sessionID, _ = a["sessionId"].(string)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no attempt session after 20 s: %v", r["tasks"])
		}
		time.Sleep(50 * time.Millisecond)
	}
	resp, out := e.do("GET", "/api/sessions/"+sessionID, adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get session: %d %v", resp.StatusCode, out)
	}
	sess := out["session"].(map[string]any)
	ref, _ := sess["crew"].(map[string]any)
	if ref == nil || ref["runId"] != run.ID || ref["member"] != "lead" || ref["task"] != "build" || ref["attempt"] != float64(1) {
		t.Fatalf("the attempt's session: %v", sess)
	}
	tree := filepath.Join(e.root, ".conductor", "worktrees", run.ID, "build.1")
	if sess["cwd"] != tree {
		t.Fatalf("the session's cwd is %v, not %s", sess["cwd"], tree)
	}
	if _, err := os.Stat(filepath.Join(tree, ".conductor", "task.md")); err != nil {
		t.Fatal(err)
	}
	// The run reports it as managed, running, with the member on its task.
	_, out = e.do("GET", "/api/runs/"+run.ID, adminToken, nil)
	r := out["run"].(map[string]any)
	if r["managed"] == nil || r["state"] != "running" || r["outcome"] != nil {
		t.Fatalf("the run: managed=%v state=%v outcome=%v", r["managed"], r["state"], r["outcome"])
	}
	if m := runMember(t, r, "lead"); m["task"] != "build" || m["attempt"] != float64(1) {
		t.Fatalf("lead: %v", m)
	}
	// Stopped: the attempt's session goes with it, before its prompt.
	if resp, out := e.do("POST", "/api/runs/"+run.ID+"/stop", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop: %d %v", resp.StatusCode, out)
	}
	_, out = e.do("GET", "/api/sessions/"+sessionID, adminToken, nil)
	if sess := out["session"].(map[string]any); sess["status"] == "running" {
		t.Fatalf("the attempt's session after the stop: %v", sess["status"])
	}
}
