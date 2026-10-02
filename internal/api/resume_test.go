package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// resumable saves an agent that prints its arguments and waits, with a
// session recipe that names the session at launch and resumes it.
func (e *testEnv) saveResumable(id string) {
	e.t.Helper()
	body := agentBody(id)
	body["command"] = []string{"/bin/sh", "-c", `printf 'ARGS[%s]\n' "$*"; exec /bin/cat`, "sh"}
	body["allowArgs"] = true
	body["session"] = map[string]any{
		"startArgs": []string{"--session-id", "{id}"}, "idFrom": "hook",
		"resumeArgs": []string{"--resume", "{id}"},
		"idPattern":  `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
	}
	e.save(body)
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// stopAndWait stops a session and waits until it shows ended.
func (e *testEnv) stopAndWait(id string) {
	e.t.Helper()
	l := e.local(id)
	_ = l.Stop(e.t.Context())
	<-l.Ended()
}

// A session named at launch is resumed with the recipe's arguments once the
// agent has had a turn; before one, it is relaunched plainly and the reply
// says so; one conversation is resumed by one session at a time.
func TestResumeASession(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	info := e.launch("talker", []string{"--verbose"})
	as, _ := info["agentSession"].(map[string]any)
	cmd := commandOf(info)
	if as == nil || !uuidRe.MatchString(as["id"].(string)) || as["resumable"] != false || as["source"] != "set" ||
		!slices.Equal(cmd[4:], []string{"--session-id", as["id"].(string), "--verbose"}) {
		t.Fatalf("launch: %v %q", as, cmd)
	}
	id := info["id"].(string)

	// No turn yet: a plain relaunch.
	e.stopAndWait(id)
	resp, out := e.do("POST", "/api/sessions/"+id+"/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated || out["resumed"] != false || !strings.Contains(out["notice"].(string), "started anew") {
		t.Fatalf("plain: %d %v", resp.StatusCode, out)
	}
	plain := out["session"].(map[string]any)
	t.Cleanup(func() { e.stopAndWait(plain["id"].(string)) })
	if plain["resumedFrom"] != id || slices.Contains(commandOf(plain), "--resume") {
		t.Fatalf("plain: %v", plain)
	}

	// A turn, reported by the agent's hook with its id: resumable.
	tok := e.agentToken(plain["id"].(string))
	pid := plain["agentSession"].(map[string]any)["id"].(string)
	resp, out = e.do("POST", "/api/sessions/"+plain["id"].(string)+"/attention", tok, map[string]any{"state": "done", "agentSession": pid, "turn": true})
	if resp.StatusCode != http.StatusOK || !e.local(plain["id"].(string)).Info().AgentSession.Resumable {
		t.Fatalf("report: %d %v", resp.StatusCode, out)
	}
	e.stopAndWait(plain["id"].(string))
	resp, out = e.do("POST", "/api/sessions/"+plain["id"].(string)+"/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated || out["resumed"] != true {
		t.Fatalf("resume: %d %v", resp.StatusCode, out)
	}
	next := out["session"].(map[string]any)
	t.Cleanup(func() { e.stopAndWait(next["id"].(string)) })
	if got := commandOf(next); !slices.Equal(got[4:], []string{"--resume", pid, "--verbose"}) || next["name"] != info["name"] {
		t.Fatalf("resumed: %q %v", got, next["name"])
	}
	// The same conversation again, while it runs: refused.
	if resp, out := e.do("POST", "/api/sessions/"+plain["id"].(string)+"/resume", adminToken, nil); resp.StatusCode != http.StatusConflict || errorCode(out) != "already_resumed" {
		t.Fatalf("twice: %d %v", resp.StatusCode, out)
	}
	if resp, out := e.do("POST", "/api/sessions/"+next["id"].(string)+"/resume", adminToken, nil); resp.StatusCode != http.StatusConflict || errorCode(out) != "still_running" {
		t.Fatalf("running: %d %v", resp.StatusCode, out)
	}
}

// An id that does not have the recipe's shape, or that would read as a flag,
// is never kept.
func TestAReportedIDOfTheWrongShapeIsDropped(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	info := e.launch("talker", nil)
	id := info["id"].(string)
	set := info["agentSession"].(map[string]any)["id"].(string)
	tok := e.agentToken(id)
	for _, bad := range []string{"--dangerously-skip-permissions", "not-a-uuid", strings.Repeat("a", 129)} {
		resp, _ := e.do("POST", "/api/sessions/"+id+"/attention", tok, map[string]any{"state": "working", "agentSession": bad, "turn": true})
		if bad == strings.Repeat("a", 129) {
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("long: %d", resp.StatusCode)
			}
			continue
		}
		if as := e.local(id).Info().AgentSession; as.ID != set || as.Resumable {
			t.Fatalf("%q was kept: %+v", bad, as)
		}
	}
}

// A crew member resumes in its worktree as the member, with no prompt typed.
func TestResumeACrewMember(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	runID := e.launchCrew(t, "Talk", map[string]any{"name": "a", "agentId": "talker", "prompt": "", "start": map[string]any{"when": "immediately"}})
	sid := e.waitRunning(t, runID, "a")
	as := e.local(sid).Info().AgentSession
	tok := e.agentToken(sid)
	e.do("POST", "/api/sessions/"+sid+"/attention", tok, map[string]any{"state": "done", "agentSession": as.ID, "turn": true})
	e.stopAndWait(sid)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, out := e.do("GET", "/api/runs/"+runID, adminToken, nil)
		if runMember(t, out["run"].(map[string]any), "a")["status"] == "ended" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp, out := e.do("POST", "/api/runs/"+runID+"/members/a/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated || out["resumed"] != true {
		t.Fatalf("resume: %d %v", resp.StatusCode, out)
	}
	next := out["session"].(map[string]any)
	t.Cleanup(func() { e.stopAndWait(next["id"].(string)) })
	crew := next["crew"].(map[string]any)
	if crew["runId"] != runID || crew["member"] != "a" || !slices.Contains(commandOf(next), "--resume") {
		t.Fatalf("resumed member: %v", next)
	}
	if got := e.waitRunning(t, runID, "a"); got != next["id"] {
		t.Fatalf("member's session %s, want %s", got, next["id"])
	}
}

// A session whose directory is gone resumes in the default directory, unless
// its agent resumes only where it ran: then the resume is refused.
func TestResumeWhenTheDirectoryIsGone(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	needs := agentBody("strict")
	needs["command"] = []string{"/bin/sh", "-c", "exec /bin/cat"}
	needs["session"] = map[string]any{"startArgs": []string{"--id", "{id}"}, "idFrom": "hook", "resumeArgs": []string{"--resume", "{id}"},
		"idPattern": `^[0-9a-f-]{36}$`, "resumeNeedsCwd": true}
	e.save(needs)
	for _, tc := range []struct {
		agent string
		code  int
	}{{"talker", http.StatusCreated}, {"strict", http.StatusBadRequest}} {
		dir := filepath.Join(e.root, "gone-"+tc.agent)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		resp, info := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": tc.agent, "cwd": dir})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("launch: %d %v", resp.StatusCode, info)
		}
		id := info["id"].(string)
		as := info["agentSession"].(map[string]any)["id"].(string)
		e.do("POST", "/api/sessions/"+id+"/attention", e.agentToken(id), map[string]any{"state": "done", "agentSession": as, "turn": true})
		e.stopAndWait(id)
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		resp, out := e.do("POST", "/api/sessions/"+id+"/resume", adminToken, nil)
		if resp.StatusCode != tc.code {
			t.Fatalf("%s: %d %v", tc.agent, resp.StatusCode, out)
		}
		if tc.code == http.StatusCreated {
			next := out["session"].(map[string]any)
			t.Cleanup(func() { e.stopAndWait(next["id"].(string)) })
			if next["cwd"] != realRoot(t, e.root) {
				t.Fatalf("resumed in %v", next["cwd"])
			}
		} else if errorCode(out) != "invalid_cwd" {
			t.Fatalf("%s: %v", tc.agent, out)
		}
	}
}
