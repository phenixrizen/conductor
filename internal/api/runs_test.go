package api

import (
	"net/http"
	"sync"
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

// A stopped run is resumed once: a second resume is refused with 409
// naming the continuation, for the page to open, and resumes fired together
// make one continuation, the others refused (#126). The old run names the
// one continuation.
func TestResumeRunTwiceAnswersTheOneContinuation(t *testing.T) {
	e := newTestEnv(t, nil)
	crewID := e.createCrew(t, map[string]any{"name": "resume once", "goal": "g", "cwd": e.root, "where": "server", "isolation": "none", "openAfterLaunch": false,
		"members": []map[string]any{{"name": "solo", "agentId": "cat", "prompt": "", "start": map[string]any{"when": "immediately"}}}})
	stoppedRun := func() string {
		t.Helper()
		resp, out := e.do("POST", "/api/crews/"+crewID+"/launch", adminToken, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("launch: %d %v", resp.StatusCode, out)
		}
		runID := out["run"].(map[string]any)["id"].(string)
		if resp, _ := e.do("POST", "/api/runs/"+runID+"/stop", adminToken, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("stop: %d", resp.StatusCode)
		}
		return runID
	}
	runID := stoppedRun()
	resp, out := e.do("POST", "/api/runs/"+runID+"/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("resume: %d %v", resp.StatusCode, out)
	}
	next := out["run"].(map[string]any)["id"].(string)
	resp, out = e.do("POST", "/api/runs/"+runID+"/resume", adminToken, nil)
	if apiErr, _ := out["error"].(map[string]any); resp.StatusCode != http.StatusConflict || apiErr["code"] != "already_resumed" || apiErr["run"] != next {
		t.Fatalf("a second resume: %d %v", resp.StatusCode, out)
	}
	// Fired together: one continuation.
	runID = stoppedRun()
	const n = 6
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			resp, _ := e.do("POST", "/api/runs/"+runID+"/resume", adminToken, nil)
			codes <- resp.StatusCode
		})
	}
	wg.Wait()
	close(codes)
	created, conflicts := 0, 0
	for c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("a resume answered %d", c)
		}
	}
	if created != 1 || conflicts != n-1 {
		t.Fatalf("%d continuations made and %d refused of %d resumes at once", created, conflicts, n)
	}
	_, old := e.do("GET", "/api/runs/"+runID, adminToken, nil)
	if by, _ := old["run"].(map[string]any)["resumedBy"].(string); by == "" {
		t.Fatalf("the old run names no continuation: %v", old["run"])
	}
}
