package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/crew"
	"github.com/rocksolidlabs/conductor/internal/crew/ledger"
)

// raw is do for a route that answers other than JSON: the reply and its
// body, with the headers given set on the request.
func (e *testEnv) raw(path, token string, header map[string]string) (*http.Response, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.http.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// manifestRunCrew is a managed crew of one task, build, with a check, and a
// project manifest: a prerequisite and a setup step that print and pass,
// the setup failing in a worktree while mark exists.
func manifestRunCrew(root, mark string) crew.Crew {
	c := crew.Crew{ID: "receipts", Name: "Receipts", Goal: "build it", Cwd: root, Where: crew.WhereServer, Isolation: crew.IsolationWorktree,
		Members: []crew.Member{{Name: "lead", AgentID: "ready-cat", Start: crew.Start{When: crew.StartTask}}},
		Tasks: []crew.Task{{ID: "build", Member: "lead", Kind: crew.TaskWork, Instructions: "Build it.",
			Checks: []crew.Check{{ID: "unit", Argv: []string{"sh", "-c", "echo checked"}}}}}}
	c.Manifest = &crew.Manifest{
		Prerequisites: []crew.Check{{ID: "tools", Argv: []string{"sh", "-c", "echo tools-ok"}}},
		Setup:         []crew.Check{{ID: "deps", Argv: []string{"sh", "-c", `case "$(pwd)" in *.check) echo set-up; exit 0;; esac; if [ -e "` + mark + `" ]; then echo no registry; exit 1; fi; echo set-up`}}},
	}
	return c
}

// stepsOf reads an attempt's receipts through the route.
func stepsOf(t *testing.T, e *testEnv, runID, taskID string, n int) []map[string]any {
	t.Helper()
	resp, out := e.do("GET", "/api/runs/"+runID+"/tasks/"+taskID+"/attempts/"+strconv.Itoa(n)+"/steps", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("steps of %s/%d: %d %v", taskID, n, resp.StatusCode, out)
	}
	var steps []map[string]any
	for _, s := range out["steps"].([]any) {
		steps = append(steps, s.(map[string]any))
	}
	return steps
}

// The receipt routes answer from the ledger: an attempt's steps (its
// trees' setup and its check, by when each started) and each one's log
// with its SHA-256 in a header the app's script can read from a dev
// origin; the run's prerequisites the same way; 404 for what the ledger
// has not, 410 for a log dropped, admin only; and a run the engine has
// let go answers the same, its task read from the ledger.
func TestTheReceiptRoutesAnswerFromTheLedger(t *testing.T) {
	e := readyEnv(t, nil)
	e.gitRepo(t)
	run, release, err := e.srv.runs.LaunchNamed(t.Context(), manifestRunCrew(e.root, filepath.Join(t.TempDir(), "down")), "")
	release()
	if err != nil {
		t.Fatal(err)
	}
	a := runningAttempt(t, e, run.ID, "build", 1)
	sid := a["sessionId"].(string)
	if resp, out := e.do("POST", "/api/sessions/"+sid+"/task/result", e.agentToken(sid), map[string]any{"summary": "built"}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("result: %d %v", resp.StatusCode, out)
	}
	taskVia(t, e, run.ID, "build", "succeeded", func(task map[string]any) bool { return task["state"] == "succeeded" })
	base := "/api/runs/" + run.ID
	check := func(when string) {
		steps := stepsOf(t, e, run.ID, "build", 1)
		if len(steps) != 3 || steps[0]["phase"] != "setup" || steps[0]["k"] != float64(1) || steps[1]["phase"] != "setup" || steps[1]["k"] != float64(2) ||
			steps[2]["phase"] != "check" || steps[2]["id"] != "unit" || steps[0]["manifest"] != "crew:0" || steps[0]["exit"] != float64(0) || steps[0]["dir"] == steps[1]["dir"] {
			t.Fatalf("%s: the steps: %v", when, steps)
		}
		resp, body := e.raw(base+"/tasks/build/attempts/1/steps/deps/1/log", adminToken, nil)
		if resp.StatusCode != http.StatusOK || string(body) != "set-up\n" || resp.Header.Get(logHashHeader) != shaHex(body) || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") ||
			steps[0]["logSha256"] != shaHex(body) || steps[0]["logBytes"] != float64(len(body)) {
			t.Fatalf("%s: the log: %d %q %s", when, resp.StatusCode, body, resp.Header.Get(logHashHeader))
		}
		resp, out := e.do("GET", base+"/prerequisites", adminToken, nil)
		prereqs, _ := out["steps"].([]any)
		if resp.StatusCode != http.StatusOK || len(prereqs) != 1 || prereqs[0].(map[string]any)["phase"] != "prerequisite" || prereqs[0].(map[string]any)["id"] != "tools" {
			t.Fatalf("%s: the prerequisites: %d %v", when, resp.StatusCode, out)
		}
		resp, body = e.raw(base+"/prerequisites/tools/1/log", adminToken, nil)
		if resp.StatusCode != http.StatusOK || string(body) != "tools-ok\n" || resp.Header.Get(logHashHeader) != shaHex(body) {
			t.Fatalf("%s: the prerequisite's log: %d %q", when, resp.StatusCode, body)
		}
		for _, path := range []string{base + "/tasks/build/attempts/1/steps/deps/9/log", base + "/tasks/build/attempts/7/steps", base + "/prerequisites/tools/2/log", "/api/runs/nope-00000000/prerequisites"} {
			if resp, body := e.raw(path, adminToken, nil); resp.StatusCode != http.StatusNotFound {
				t.Fatalf("%s: %s: %d %s", when, path, resp.StatusCode, body)
			}
		}
		for _, path := range []string{base + "/tasks/build/attempts/0/steps", base + "/tasks/build/attempts/1/steps/Deps/1/log", base + "/prerequisites/tools/01/log"} {
			if resp, body := e.raw(path, adminToken, nil); resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: %s: %d %s", when, path, resp.StatusCode, body)
			}
		}
		for _, path := range []string{base + "/tasks/build/attempts/1/steps", base + "/tasks/build/attempts/1/steps/deps/1/log", base + "/prerequisites", base + "/prerequisites/tools/1/log"} {
			if resp, _ := e.raw(path, "", nil); resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s: %s without a token: %d", when, path, resp.StatusCode)
			}
		}
		// The hash header reaches a dev origin's script.
		resp, _ = e.raw(base+"/tasks/build/attempts/1/steps/deps/1/log", adminToken, map[string]string{"Origin": "http://localhost:3000"})
		if !strings.Contains(resp.Header.Get("Access-Control-Expose-Headers"), logHashHeader) {
			t.Fatalf("%s: exposed headers %q", when, resp.Header.Get("Access-Control-Expose-Headers"))
		}
		// The task's read carries the receipts.
		resp, out = e.do("GET", base+"/tasks/build", adminToken, nil)
		attempts, _ := out["attempts"].([]any)
		if resp.StatusCode != http.StatusOK || len(attempts) != 1 {
			t.Fatalf("%s: the task: %d %v", when, resp.StatusCode, out)
		}
		if got, _ := attempts[0].(map[string]any)["steps"].([]any); len(got) != 3 {
			t.Fatalf("%s: the task's receipts: %v", when, attempts[0])
		}
	}
	check("held")
	if resp, out := e.do("POST", base+"/stop", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop: %d %v", resp.StatusCode, out)
	}
	e.srv.runs.ForgetForTest(run.ID)
	if _, ok := e.srv.runs.Get(run.ID); ok {
		t.Fatal("the run is still held")
	}
	check("let go")
	// A log the ledger dropped past the run's bound answers 410; a step
	// still running has no log yet.
	if _, err := e.srv.ledger.Transition(context.Background(), run.ID, func(tx *ledger.Tx) error {
		if err := tx.PutCheck(ledger.CheckRow{RunID: run.ID, TaskID: "build", N: 1, ID: "big", K: 1, Phase: ledger.PhaseSetup, State: ledger.CheckFinished, StartedAt: tx.Now(), LogDropped: true, LogSHA256: "h"}); err != nil {
			return err
		}
		return tx.PutCheck(ledger.CheckRow{RunID: run.ID, TaskID: "build", N: 1, ID: "slow", K: 1, Phase: ledger.PhaseSetup, State: ledger.CheckRunning, StartedAt: tx.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	if resp, body := e.raw(base+"/tasks/build/attempts/1/steps/big/1/log", adminToken, nil); resp.StatusCode != http.StatusGone || !strings.Contains(string(body), "log_dropped") {
		t.Fatalf("a dropped log: %d %s", resp.StatusCode, body)
	}
	if resp, body := e.raw(base+"/tasks/build/attempts/1/steps/slow/1/log", adminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a running step's log: %d %s", resp.StatusCode, body)
	}
}

// A task's read for a run the engine does not hold answers from the
// ledger: after a restart (a second server on the same data directory,
// the first shut down), a task with two attempts, the first uncounted at
// its setup step and the second its result, is answered whole: each
// attempt's state, commits, what it submitted and its receipts as the
// rows have them, no preview, each log read through the log route; a task
// the ledger has not is 404.
func TestATasksReadAfterARestartAnswersFromTheLedger(t *testing.T) {
	e := readyEnv(t, nil)
	e.gitRepo(t)
	mark := filepath.Join(t.TempDir(), "down")
	if err := os.WriteFile(mark, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	run, release, err := e.srv.runs.LaunchNamed(t.Context(), manifestRunCrew(e.root, mark), "")
	release()
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/runs/" + run.ID
	taskVia(t, e, run.ID, "build", "paused at attempt 1", func(task map[string]any) bool {
		a, _ := task["attempt"].(map[string]any)
		return a != nil && a["state"] == "failed" && a["stage"] == "setup deps"
	})
	if err := os.Remove(mark); err != nil {
		t.Fatal(err)
	}
	if resp, out := e.do("POST", base+"/unpause", adminToken, map[string]any{"commandId": "go-on"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("unpause: %d %v", resp.StatusCode, out)
	}
	a2 := runningAttempt(t, e, run.ID, "build", 2)
	sid := a2["sessionId"].(string)
	if resp, out := e.do("POST", "/api/sessions/"+sid+"/task/result", e.agentToken(sid), map[string]any{"summary": "built at last"}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("result: %d %v", resp.StatusCode, out)
	}
	taskVia(t, e, run.ID, "build", "succeeded", func(task map[string]any) bool { return task["state"] == "succeeded" })
	_, held := e.do("GET", base+"/tasks/build", adminToken, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e.srv.Shutdown(ctx)
	again := e.restart()
	resp, out := again.do("GET", base+"/tasks/build", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the task after a restart: %d %v", resp.StatusCode, out)
	}
	task := out["task"].(map[string]any)
	attempts := out["attempts"].([]any)
	if task["state"] != "succeeded" || task["attempts"] != float64(2) || len(attempts) != 2 {
		t.Fatalf("the task after a restart: %v\n%v", task, attempts)
	}
	heldAttempts := held["attempts"].([]any)
	for i, raw := range attempts {
		got, was := raw.(map[string]any), heldAttempts[i].(map[string]any)
		for _, k := range []string{"n", "state", "stage", "error", "start", "result", "worktree", "branch"} {
			if got[k] != was[k] {
				t.Fatalf("attempt %d's %s after a restart: %v, held %v", i+1, k, got[k], was[k])
			}
		}
		if got["preview"] != nil {
			t.Fatalf("attempt %d has a preview after a restart: %v", i+1, got)
		}
		steps, _ := got["steps"].([]any)
		if len(steps) != len(was["steps"].([]any)) || len(steps) == 0 {
			t.Fatalf("attempt %d's receipts after a restart: %v, held %v", i+1, steps, was["steps"])
		}
		first := steps[0].(map[string]any)
		resp, body := again.raw(base+"/tasks/build/attempts/"+strconv.Itoa(i+1)+"/steps/"+first["id"].(string)+"/"+strconv.Itoa(int(first["k"].(float64)))+"/log", adminToken, nil)
		if resp.StatusCode != http.StatusOK || resp.Header.Get(logHashHeader) != shaHex(body) || first["logSha256"] != shaHex(body) {
			t.Fatalf("attempt %d's first log after a restart: %d %q", i+1, resp.StatusCode, body)
		}
	}
	if a1 := attempts[0].(map[string]any); a1["stage"] != "setup deps" || a1["submitted"] != nil {
		t.Fatalf("attempt 1 after a restart: %v", a1)
	}
	if a2 := attempts[1].(map[string]any); a2["submitted"] == nil || a2["submitted"].(map[string]any)["summary"] != "built at last" {
		t.Fatalf("attempt 2 after a restart: %v", a2)
	}
	if resp, _ := again.do("GET", base+"/tasks/nope", adminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a task the ledger has not: %d", resp.StatusCode)
	}
	if resp, _ := again.do("GET", "/api/runs/nope-00000000/tasks/build", adminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a run the ledger has not: %d", resp.StatusCode)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	again.srv.Shutdown(ctx2)
}
