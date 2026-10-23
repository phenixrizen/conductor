package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

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

// A crew with tasks is saved, and its launch refused with the reason, as
// is the resume of a managed run's record: until a run can dispatch
// tasks (#334, rows 5 and 6) and continue (row 8).
func TestAManagedCrewIsSavedAndNotYetRun(t *testing.T) {
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
	resp, out = e.do("POST", "/api/crews/"+id+"/launch", adminToken, nil)
	if code, msg := apiErr(out); resp.StatusCode != http.StatusBadRequest || code != "invalid_crew" || !strings.Contains(msg, "cannot be launched yet") {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
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
