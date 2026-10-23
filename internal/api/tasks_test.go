package api

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/crew"
	"github.com/phenixrizen/conductor/internal/session"
)

// A member's process learns its crew, run and member from the environment;
// an attempt also its task, the attempt's number and the task file.
func TestCrewInjectNamesTheAttempt(t *testing.T) {
	if env := crewInject(nil); env != nil {
		t.Fatalf("no ref: %v", env)
	}
	member := crewInject(&session.CrewRef{RunID: "team-00000001", CrewID: "team", Member: "lead"})
	if member["CONDUCTOR_RUN"] != "team-00000001" || member["CONDUCTOR_CREW"] != "team" || member["CONDUCTOR_MEMBER"] != "lead" || len(member) != 3 {
		t.Fatalf("a member: %v", member)
	}
	attempt := crewInject(&session.CrewRef{RunID: "team-00000001", CrewID: "team", Member: "builder", Task: "build", Attempt: 2})
	if attempt["CONDUCTOR_TASK"] != "build" || attempt["CONDUCTOR_ATTEMPT"] != "2" || attempt["CONDUCTOR_TASK_FILE"] != ".conductor/task.md" || len(attempt) != 6 {
		t.Fatalf("an attempt: %v", attempt)
	}
}

// A crew with tasks is saved under its rules (a member starts by task,
// nowhere else), and a managed run's record is not resumed by the legacy
// path (row 8 of #334 continues it).
func TestAManagedCrewIsSavedUnderItsRules(t *testing.T) {
	e := newTestEnv(t, nil)
	c := map[string]any{
		"name": "Managed", "goal": "ship it", "cwd": e.root, "where": "server", "isolation": "worktree",
		"members": []map[string]any{
			{"name": "builder", "agentId": "cat", "prompt": "Build.", "start": map[string]any{"when": "task"}},
		},
		"tasks": []map[string]any{{"id": "build", "member": "builder", "kind": "work", "instructions": "Build it."}},
	}
	resp, out := e.do("POST", "/api/crews", adminToken, c)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	crewOut, _ := out["crew"].(map[string]any)
	id, _ := crewOut["id"].(string)
	if id == "" || len(crewOut["tasks"].([]any)) != 1 {
		t.Fatalf("created: %v", out)
	}
	// A member with a start rule in a crew with tasks is refused as such.
	c["members"].([]map[string]any)[0]["start"] = map[string]any{"when": "immediately"}
	if resp, out := e.do("POST", "/api/crews", adminToken, c); resp.StatusCode != http.StatusBadRequest || !strings.Contains(apiMessage(out), `start.when must be "task"`) {
		t.Fatalf("a start rule: %d %v", resp.StatusCode, out)
	}
	// A managed run's record is not resumed by the legacy path.
	rec := crew.Run{ID: "team-0000aaaa", CrewID: id, Name: "Managed", Goal: "ship it", Cwd: e.root, Isolation: "worktree", StartedAt: time.Now().UTC().Add(-time.Hour), State: crew.RunInterrupted,
		Members: []crew.MemberState{{Name: "builder", AgentID: "cat", Start: crew.Start{When: crew.StartTask}, Status: crew.MemberEnded}}}
	if err := e.srv.records.Save(rec); err != nil {
		t.Fatal(err)
	}
	// A task-started member is refused on a legacy run's add-member route too.
	lead := map[string]any{"name": "Legacy", "goal": "g", "cwd": e.root, "where": "server", "isolation": "none",
		"members": []map[string]any{{"name": "lead", "agentId": "cat", "prompt": "", "start": map[string]any{"when": "immediately"}}}}
	resp, out = e.do("POST", "/api/crews", adminToken, lead)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("legacy crew: %d %v", resp.StatusCode, out)
	}
	legacyID, _ := out["crew"].(map[string]any)["id"].(string)
	resp, out = e.do("POST", "/api/crews/"+legacyID+"/launch", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("legacy launch: %d %v", resp.StatusCode, out)
	}
	runID, _ := out["run"].(map[string]any)["id"].(string)
	t.Cleanup(func() { e.do("POST", "/api/runs/"+runID+"/stop", adminToken, nil) })
	resp, out = e.do("POST", "/api/runs/"+runID+"/members", adminToken, map[string]any{"name": "worker", "agentId": "cat", "prompt": "", "start": map[string]any{"when": "task"}})
	if code, msg := apiErr(out); resp.StatusCode != http.StatusBadRequest || code != "invalid_crew" || !strings.Contains(msg, "only for a crew with tasks") {
		t.Fatalf("add a task member: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("POST", "/api/runs/team-0000aaaa/resume", adminToken, nil)
	if code, _ := apiErr(out); resp.StatusCode != http.StatusConflict || code != "managed_run" {
		t.Fatalf("resume: %d %v", resp.StatusCode, out)
	}
}

// apiErr reads an error reply's code and message.
func apiErr(out map[string]any) (code, message string) {
	e, _ := out["error"].(map[string]any)
	code, _ = e["code"].(string)
	message, _ = e["message"].(string)
	return code, message
}

func apiMessage(out map[string]any) string { _, m := apiErr(out); return m }

// readyEnv is a test environment whose catalog has ready-cat: a cat that
// prints a line first, so an attempt's session is ready for its task line
// at once (a silent cat is typed to after the 60 s readiness cap).
func readyEnv(t *testing.T, mutate func(*config.Config)) *testEnv {
	t.Helper()
	return newTestEnvAgents(t, mutate, slog.New(slog.NewTextHandler(io.Discard, nil)), []catalog.Agent{{ID: "ready-cat", Name: "ready cat", Command: []string{"/bin/sh", "-c", "echo ready; exec cat"}}})
}

// managedCrew is a crew of two tasks, docs after build, each with a ready
// cat for its member.
func managedCrew(root string) crew.Crew {
	return crew.Crew{ID: "routes", Name: "Routes", Goal: "build it", Cwd: root, Where: crew.WhereServer, Isolation: crew.IsolationWorktree,
		Members: []crew.Member{{Name: "lead", AgentID: "ready-cat", Start: crew.Start{When: crew.StartTask}}, {Name: "writer", AgentID: "ready-cat", Start: crew.Start{When: crew.StartTask}}},
		Tasks:   []crew.Task{{ID: "build", Member: "lead", Kind: crew.TaskWork, Instructions: "Build it."}, {ID: "docs", Member: "writer", Kind: crew.TaskWork, Instructions: "Write it.", Needs: []string{"build"}}}}
}

// taskVia polls the run until the task satisfies ok, and returns it.
func taskVia(t *testing.T, e *testEnv, runID, taskID, what string, ok func(task map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, out := e.do("GET", "/api/runs/"+runID, adminToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("get run: %d %v", resp.StatusCode, out)
		}
		r := out["run"].(map[string]any)
		var found map[string]any
		for _, ts := range r["tasks"].([]any) {
			if task := ts.(map[string]any); task["id"] == taskID {
				found = task
			}
		}
		if found != nil && ok(found) {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s not %s after 20 s: %v", taskID, what, found)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// runningAttempt is the task's attempt once it runs, with the attempt
// number when n is not 0.
func runningAttempt(t *testing.T, e *testEnv, runID, taskID string, n int) map[string]any {
	t.Helper()
	task := taskVia(t, e, runID, taskID, "running", func(task map[string]any) bool {
		a, _ := task["attempt"].(map[string]any)
		return a != nil && a["state"] == "running" && (n == 0 || a["n"] == float64(n))
	})
	return task["attempt"].(map[string]any)
}

func receiptOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	r, _ := out["receipt"].(map[string]any)
	if r == nil {
		t.Fatalf("no receipt in %v", out)
	}
	return r
}

// An attempt's session reads its task and submits its result with its own
// token; the admin reads tasks and commands the run with receipts (row 6
// of #334).
func TestAnAttemptReadsItsTaskAndSubmitsItsResultAndTheAdminCommandsTheRun(t *testing.T) {
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
	base := "/api/runs/" + run.ID

	// The task, by its own token and by the admin, paged; refused without a token.
	resp, out := e.do("GET", "/api/sessions/"+sid+"/task", tok, nil)
	if resp.StatusCode != http.StatusOK || out["page"] != float64(1) || out["pages"] != float64(1) {
		t.Fatalf("task: %d %v", resp.StatusCode, out)
	}
	if md, _ := out["markdown"].(string); !containsAll(md, "Build it.", "build") {
		t.Fatalf("task.md: %q", md)
	}
	if tf, _ := out["task"].(map[string]any); tf == nil || tf["task"] != "build" || tf["attempt"] != float64(1) {
		t.Fatalf("task.json: %v", out["task"])
	}
	if resp, _ := e.do("GET", "/api/sessions/"+sid+"/task", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("by the admin: %d", resp.StatusCode)
	}
	if resp, out := e.do("GET", "/api/sessions/"+sid+"/task?page=2", adminToken, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("page 2: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("GET", "/api/sessions/"+sid+"/task", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp, _ := e.do("GET", "/api/sessions/nope/task", adminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no session: %d", resp.StatusCode)
	}

	// The run's task with its attempts, for the admin alone.
	resp, out = e.do("GET", base+"/tasks/build", adminToken, nil)
	if resp.StatusCode != http.StatusOK || len(out["attempts"].([]any)) != 1 || out["task"].(map[string]any)["state"] != "running" {
		t.Fatalf("get task: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("GET", base+"/tasks/nope", adminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no task: %d", resp.StatusCode)
	}
	if resp, _ := e.do("GET", base+"/tasks/build", tok, nil); resp.StatusCode == http.StatusOK {
		t.Fatal("an agent token read the run's task")
	}

	// Commands: a receipt, the same again, a conflict, a stale revision.
	resp, out = e.do("POST", base+"/pause", adminToken, map[string]any{"commandId": "c1"})
	rc := receiptOf(t, out)
	if resp.StatusCode != http.StatusOK || rc["status"] != "done" || rc["intent"] != "pause" || rc["paused"] != true || rc["repeat"] != nil {
		t.Fatalf("pause: %d %v", resp.StatusCode, out)
	}
	rev, _ := rc["revision"].(float64)
	if rev < 1 {
		t.Fatalf("revision %v", rc["revision"])
	}
	resp, out = e.do("POST", base+"/pause", adminToken, map[string]any{"commandId": "c1"})
	if rc := receiptOf(t, out); resp.StatusCode != http.StatusOK || rc["repeat"] != true || rc["status"] != "done" || rc["revision"] != rev {
		t.Fatalf("pause again: %d %v", resp.StatusCode, out)
	}
	if resp, out := e.do("POST", base+"/unpause", adminToken, map[string]any{"commandId": "c1"}); resp.StatusCode != http.StatusConflict || errCodeOf(out) != "command_conflict" {
		t.Fatalf("other content: %d %v", resp.StatusCode, out)
	}
	if resp, out := e.do("POST", base+"/unpause", adminToken, map[string]any{"commandId": "c2", "expectedRevision": rev + 100}); resp.StatusCode != http.StatusConflict || errCodeOf(out) != "stale_revision" {
		t.Fatalf("stale: %d %v", resp.StatusCode, out)
	}
	// The revision expected is the run's now (each command bumps it twice: taken, then settled).
	_, cur := e.do("GET", base, adminToken, nil)
	now, _ := cur["run"].(map[string]any)["managed"].(map[string]any)["revision"].(float64)
	resp, out = e.do("POST", base+"/unpause", adminToken, map[string]any{"commandId": "c3", "expectedRevision": now})
	if rc := receiptOf(t, out); resp.StatusCode != http.StatusOK || rc["paused"] != false || rc["revision"] != now+2 {
		t.Fatalf("unpause: %d %v (revision now %v)", resp.StatusCode, out, now)
	}
	for _, body := range []any{map[string]any{"commandId": "bad id"}, map[string]any{"commandId": "c4", "nope": 1}, nil} {
		if resp, out := e.do("POST", base+"/pause", adminToken, body); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%v: %d %v", body, resp.StatusCode, out)
		}
	}
	// A command the task's state refuses: its failure is the receipt, repeated as such.
	for i := range 2 {
		resp, out = e.do("POST", base+"/tasks/build/accept", adminToken, map[string]any{"commandId": "c5"})
		rc := receiptOf(t, out)
		if resp.StatusCode != http.StatusConflict || errCodeOf(out) != "task_state" || rc["status"] != "failed" || rc["refusal"] != "task_state" || rc["task"].(map[string]any)["id"] != "build" || (i == 1) != (rc["repeat"] == true) {
			t.Fatalf("accept %d: %d %v", i, resp.StatusCode, out)
		}
	}
	if resp, _ := e.do("POST", base+"/tasks/nope/accept", adminToken, map[string]any{"commandId": "c6"}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("accept of no task: %d", resp.StatusCode)
	}

	// The result: never the admin's; the agent's, once. HEAD is the base, which the result may be.
	if resp, out := e.do("POST", "/api/sessions/"+sid+"/task/result", adminToken, map[string]any{"summary": "x"}); resp.StatusCode != http.StatusForbidden || errCodeOf(out) != "agent_only" {
		t.Fatalf("admin result: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("POST", "/api/sessions/"+sid+"/task/result", tok, map[string]any{"summary": "x", "nope": 1}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", resp.StatusCode)
	}
	if resp, _ := e.do("POST", "/api/sessions/"+sid+"/task/result", tok, map[string]any{"verdict": "approved"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a verdict on a work task: %d", resp.StatusCode)
	}
	resp, out = e.do("POST", "/api/sessions/"+sid+"/task/result", tok, map[string]any{"summary": "built", "issues": []string{"none"}})
	if resp.StatusCode != http.StatusCreated || out["task"].(map[string]any)["id"] != "build" {
		t.Fatalf("result: %d %v", resp.StatusCode, out)
	}
	build := taskVia(t, e, run.ID, "build", "succeeded", func(task map[string]any) bool { return task["state"] == "succeeded" })
	// Once decided, the attempt is closed: another result is not its task's active attempt's.
	if resp, out := e.do("POST", "/api/sessions/"+sid+"/task/result", tok, map[string]any{"summary": "built"}); resp.StatusCode != http.StatusConflict || errCodeOf(out) != "stale_attempt" {
		t.Fatalf("result again: %d %v", resp.StatusCode, out)
	}
	commit := build["result"].(map[string]any)["commit"]
	if d := runningAttempt(t, e, run.ID, "docs", 1); d["start"] != commit {
		t.Fatalf("docs starts at %v, build's result is %v", d["start"], commit)
	}
	// Cancel docs: the receipt carries the task; the run ends cancelled. Retry: open again, a second attempt.
	resp, out = e.do("POST", base+"/tasks/docs/cancel", adminToken, map[string]any{"commandId": "c7"})
	if rc := receiptOf(t, out); resp.StatusCode != http.StatusOK || rc["task"].(map[string]any)["state"] != "cancelled" {
		t.Fatalf("cancel: %d %v", resp.StatusCode, out)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, out := e.do("GET", base, adminToken, nil)
		if r := out["run"].(map[string]any); r["outcome"] == "cancelled" && r["state"] == "finished" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run after the cancel: %v", out["run"])
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp, out := e.do("POST", base+"/tasks/docs/retry", adminToken, map[string]any{"commandId": "c8"}); resp.StatusCode != http.StatusOK || receiptOf(t, out)["task"].(map[string]any)["state"] == "cancelled" {
		t.Fatalf("retry: %d %v", resp.StatusCode, out)
	}
	runningAttempt(t, e, run.ID, "docs", 2)
	if resp, _ := e.do("POST", base+"/tasks/docs/cancel", tok, map[string]any{"commandId": "c9"}); resp.StatusCode == http.StatusOK {
		t.Fatal("an agent token commanded the run")
	}
	if resp, out := e.do("POST", base+"/stop", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop: %d %v", resp.StatusCode, out)
	}
}

// The launch route takes a crew with tasks once crews.managed is on (the
// default) and the ledger is the server's; off, it says so.
func TestALaunchOfACrewWithTasksFollowsTheManagedSwitch(t *testing.T) {
	body := func(root string) map[string]any {
		return map[string]any{"name": "Gated", "goal": "g", "cwd": root, "where": "server", "isolation": "worktree",
			"members": []any{map[string]any{"name": "lead", "agentId": "ready-cat", "prompt": "", "start": map[string]any{"when": "task"}}},
			"tasks":   []any{map[string]any{"id": "build", "member": "lead", "kind": "work", "instructions": "Build it."}}}
	}
	off := readyEnv(t, func(c *config.Config) { no := false; c.Crews.Managed = &no })
	off.gitRepo(t)
	resp, out := off.do("POST", "/api/crews", adminToken, body(off.root))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("save: %d %v", resp.StatusCode, out)
	}
	id := out["crew"].(map[string]any)["id"].(string)
	if resp, out := off.do("POST", "/api/crews/"+id+"/launch", adminToken, nil); resp.StatusCode != http.StatusConflict || errCodeOf(out) != "managed_off" {
		t.Fatalf("launch with managed off: %d %v", resp.StatusCode, out)
	}
	on := readyEnv(t, nil)
	on.gitRepo(t)
	_, out = on.do("POST", "/api/crews", adminToken, body(on.root))
	id = out["crew"].(map[string]any)["id"].(string)
	resp, out = on.do("POST", "/api/crews/"+id+"/launch", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
	}
	run := out["run"].(map[string]any)
	if run["managed"] == nil {
		t.Fatalf("not managed: %v", run)
	}
	runningAttempt(t, on, run["id"].(string), "build", 1)
	if resp, out := on.do("POST", "/api/runs/"+run["id"].(string)+"/stop", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop: %d %v", resp.StatusCode, out)
	}
}

func errCodeOf(out map[string]any) string { c, _ := apiErr(out); return c }

func containsAll(s string, words ...string) bool {
	for _, w := range words {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}
