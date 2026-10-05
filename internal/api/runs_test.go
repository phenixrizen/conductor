package api

import (
	"net/http"
	"testing"
)

// A stopped run is resumed as a new run of its crew (POST /api/runs/{run}/resume):
// 409 while it runs, 201 {run} with resumedFrom once stopped, the old run
// naming the new in resumedBy; and a member of a stopped run is resumed in
// place, which reopens the run.
func TestResumeRunRoute(t *testing.T) {
	e := newTestEnv(t, nil)
	crewID := e.createCrew(t, map[string]any{"name": "resume run", "goal": "g", "cwd": e.root, "where": "server", "isolation": "none", "openAfterLaunch": false,
		"members": []map[string]any{{"name": "solo", "agentId": "cat", "prompt": "", "start": map[string]any{"when": "immediately"}}}})
	resp, out := e.do("POST", "/api/crews/"+crewID+"/launch", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
	}
	runID := out["run"].(map[string]any)["id"].(string)
	if resp, out := e.do("POST", "/api/runs/"+runID+"/resume", adminToken, nil); resp.StatusCode != http.StatusConflict || out["error"].(map[string]any)["code"] != "run_running" {
		t.Fatalf("running: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("POST", "/api/runs/"+runID+"/stop", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop: %d", resp.StatusCode)
	}
	resp, out = e.do("POST", "/api/runs/"+runID+"/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("resume run: %d %v", resp.StatusCode, out)
	}
	next := out["run"].(map[string]any)
	if next["id"] == runID || next["resumedFrom"] != runID || next["crewId"] != crewID {
		t.Fatalf("next %v", next)
	}
	_, old := e.do("GET", "/api/runs/"+runID, adminToken, nil)
	if old["run"].(map[string]any)["resumedBy"] != next["id"] || old["run"].(map[string]any)["state"] != "stopped" {
		t.Fatalf("old %v", old["run"])
	}
	if resp, _ := e.do("POST", "/api/runs/nope/resume", adminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown: %d", resp.StatusCode)
	}
	// In place: the old run's ended member resumed reopens it.
	resp, out = e.do("POST", "/api/runs/"+runID+"/members/solo/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("resume member of a stopped run: %d %v", resp.StatusCode, out)
	}
	_, old = e.do("GET", "/api/runs/"+runID, adminToken, nil)
	if run := old["run"].(map[string]any); run["state"] != "running" || run["stoppedAt"] != nil {
		t.Fatalf("not reopened: %v", run)
	}
}
