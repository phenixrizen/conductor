package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/proto"
)

// sessionEvent decodes the data of a "session <json>" event of GET
// /api/events; nil for any other event.
func sessionEvent(ev string) map[string]any {
	data, ok := strings.CutPrefix(ev, "session ")
	if !ok {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(data), &m) != nil {
		return nil
	}
	return m
}

// listedName is the name GET /api/sessions lists the session id under.
func (e *testEnv) listedName(id string) any {
	e.t.Helper()
	_, out := e.do("GET", "/api/sessions", adminToken, nil)
	list, _ := out["sessions"].([]any)
	for _, s := range list {
		if s := s.(map[string]any); s["id"] == id {
			return s["name"]
		}
	}
	e.t.Fatalf("session %s is not listed: %v", id, out)
	return nil
}

// A server session is renamed, running or ended: the reply is its Info under
// the new name, the listings show it, and the events stream tells it as a
// session event. The name loses its control characters and the space around
// it, and is cut to proto.MaxSessionName bytes at a character boundary.
func TestRenameASession(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	events := e.sse(t)
	resp, out := e.do("PATCH", "/api/sessions/"+id, adminToken, map[string]any{"name": " \tdocs\x1b sweep\u0085 \n"})
	if resp.StatusCode != http.StatusOK || out["id"] != id || out["name"] != "docs sweep" || out["status"] != "running" || out["kind"] != "server" {
		t.Fatalf("rename: %d %v", resp.StatusCode, out)
	}
	e.waitEvent(t, events, func(ev string) bool {
		s := sessionEvent(ev)
		return s != nil && s["id"] == id && s["name"] == "docs sweep"
	})
	if _, out := e.do("GET", "/api/sessions/"+id, adminToken, nil); out["session"].(map[string]any)["name"] != "docs sweep" {
		t.Fatalf("GET: %v", out)
	}
	if got := e.listedName(id); got != "docs sweep" {
		t.Fatalf("listed as %v", got)
	}

	// Cut at a character boundary: the forty-first three-byte character would end past 120 bytes.
	resp, out = e.do("PATCH", "/api/sessions/"+id, adminToken, map[string]any{"name": strings.Repeat("名", 50)})
	if want := strings.Repeat("名", proto.MaxSessionName/3); resp.StatusCode != http.StatusOK || out["name"] != want {
		t.Fatalf("long: %d %v, want %q", resp.StatusCode, out["name"], want)
	}
	// A name sent long, in a body within its 4 KiB, is cut too.
	resp, out = e.do("PATCH", "/api/sessions/"+id, adminToken, map[string]any{"name": strings.Repeat("a", 4000)})
	if resp.StatusCode != http.StatusOK || out["name"] != strings.Repeat("a", proto.MaxSessionName) {
		t.Fatalf("near the bound: %d %v", resp.StatusCode, out["name"])
	}

	// An ended session is renamed as well, and stays ended.
	e.stopAndWait(id)
	resp, out = e.do("PATCH", "/api/sessions/"+id, adminToken, map[string]any{"name": "after the end"})
	if resp.StatusCode != http.StatusOK || out["name"] != "after the end" || (out["status"] != "stopped" && out["status"] != "exited") {
		t.Fatalf("ended: %d %v", resp.StatusCode, out)
	}
	e.waitEvent(t, events, func(ev string) bool {
		s := sessionEvent(ev)
		return s != nil && s["id"] == id && s["name"] == "after the end"
	})
}

// A rename refuses an unknown session (404), a hosted one, which its host
// names (400 hosted_session), a name with nothing left once its control
// characters and space are gone (400 invalid_name), a body that is not one
// {name} or is past its 4 KiB (400 invalid_request), and a caller without the
// workbench token (401); a switchyard launches nothing and renames nothing
// (403 switchyard). A refusal leaves the name as it was.
func TestRenameRefusals(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	name := e.local(id).Info().Name
	for _, tc := range []struct {
		what, path, token string
		body              any
		status            int
		code              string
	}{
		{"unknown", "/api/sessions/nope", adminToken, map[string]any{"name": "x"}, http.StatusNotFound, "not_found"},
		{"empty", "/api/sessions/" + id, adminToken, map[string]any{"name": ""}, http.StatusBadRequest, "invalid_name"},
		{"blank", "/api/sessions/" + id, adminToken, map[string]any{"name": " \t\x1b\u0085\u009b "}, http.StatusBadRequest, "invalid_name"},
		{"no name", "/api/sessions/" + id, adminToken, map[string]any{}, http.StatusBadRequest, "invalid_name"},
		{"no body", "/api/sessions/" + id, adminToken, nil, http.StatusBadRequest, "invalid_request"},
		{"unknown field", "/api/sessions/" + id, adminToken, map[string]any{"name": "x", "cwd": "/"}, http.StatusBadRequest, "invalid_request"},
		{"not a string", "/api/sessions/" + id, adminToken, map[string]any{"name": 5}, http.StatusBadRequest, "invalid_request"},
		{"past the bound", "/api/sessions/" + id, adminToken, map[string]any{"name": strings.Repeat("a", maxRenameBody)}, http.StatusBadRequest, "invalid_request"},
		{"no token", "/api/sessions/" + id, "", map[string]any{"name": "x"}, http.StatusUnauthorized, "unauthorized"},
		{"another token", "/api/sessions/" + id, "not-the-workbench-token", map[string]any{"name": "x"}, http.StatusUnauthorized, "unauthorized"},
	} {
		resp, out := e.do("PATCH", tc.path, tc.token, tc.body)
		if resp.StatusCode != tc.status || errorCode(out) != tc.code {
			t.Errorf("%s: %d %v, want %d %s", tc.what, resp.StatusCode, out, tc.status, tc.code)
		}
	}
	if got := e.local(id).Info().Name; got != name {
		t.Fatalf("the refusals renamed it to %q", got)
	}

	host := dialFakeHost(t, e, "hosted-agent-token")
	resp, out := e.do("PATCH", "/api/sessions/"+host.sessionID, adminToken, map[string]any{"name": "mine now"})
	if resp.StatusCode != http.StatusBadRequest || errorCode(out) != "hosted_session" {
		t.Fatalf("hosted: %d %v", resp.StatusCode, out)
	}
	if got := e.listedName(host.sessionID); got != "hosted" {
		t.Fatalf("the hosted session is listed as %v", got)
	}

	sy := switchyardEnv(t, true)
	if resp, out := sy.do("PATCH", "/api/sessions/abc", adminToken, map[string]any{"name": "x"}); resp.StatusCode != http.StatusForbidden || errorCode(out) != "switchyard" {
		t.Fatalf("switchyard: %d %v", resp.StatusCode, out)
	}
}

// A dev UI on another origin (NUXT_PUBLIC_API_BASE) may send the rename,
// the first PATCH route.
func TestDevCORSAllowsPatch(t *testing.T) {
	e := newTestEnv(t, nil) // Dev is on
	req, _ := http.NewRequest("OPTIONS", e.http.URL+"/api/sessions/x", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	methods := strings.Split(resp.Header.Get("Access-Control-Allow-Methods"), ", ")
	if resp.StatusCode != http.StatusNoContent || !slices.Contains(methods, "PATCH") {
		t.Fatalf("preflight: %d %q", resp.StatusCode, methods)
	}
}

// A renamed session resumes under its new name, its conversation with it; a
// resume of the resumed session keeps that name.
func TestARenamedSessionResumesUnderItsName(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	info := e.launch("talker", nil)
	id := info["id"].(string)
	as := info["agentSession"].(map[string]any)["id"].(string)
	e.do("POST", "/api/sessions/"+id+"/attention", e.agentToken(id), map[string]any{"state": "done", "agentSession": as, "turn": true})
	if resp, out := e.do("PATCH", "/api/sessions/"+id, adminToken, map[string]any{"name": "auth refactor"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("rename: %d %v", resp.StatusCode, out)
	}
	e.stopAndWait(id)
	resp, out := e.do("POST", "/api/sessions/"+id+"/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated || out["resumed"] != true {
		t.Fatalf("resume: %d %v", resp.StatusCode, out)
	}
	next := out["session"].(map[string]any)
	nextID := next["id"].(string)
	t.Cleanup(func() { e.stopAndWait(nextID) })
	if next["name"] != "auth refactor" || next["resumedFrom"] != id || !slices.Contains(commandOf(next), "--resume") {
		t.Fatalf("resumed: %v", next)
	}
	// Once more, from the resumed one: the name carries on.
	e.stopAndWait(nextID)
	resp, out = e.do("POST", "/api/sessions/"+nextID+"/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("resume again: %d %v", resp.StatusCode, out)
	}
	again := out["session"].(map[string]any)
	t.Cleanup(func() { e.stopAndWait(again["id"].(string)) })
	if again["name"] != "auth refactor" {
		t.Fatalf("resumed again as %v", again["name"])
	}
}

// A crew member's session renamed resumes under that name, as the member: the
// member keeps its own name in the run.
func TestARenamedMemberResumesUnderItsName(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	runID := e.launchCrew(t, "Talk", map[string]any{"name": "a", "agentId": "talker", "prompt": "", "start": map[string]any{"when": "immediately"}})
	sid := e.waitRunning(t, runID, "a")
	as := e.local(sid).Info().AgentSession
	e.do("POST", "/api/sessions/"+sid+"/attention", e.agentToken(sid), map[string]any{"state": "done", "agentSession": as.ID, "turn": true})
	if resp, out := e.do("PATCH", "/api/sessions/"+sid, adminToken, map[string]any{"name": "API lead"}); resp.StatusCode != http.StatusOK || out["name"] != "API lead" {
		t.Fatalf("rename: %d %v", resp.StatusCode, out)
	}
	e.stopAndWait(sid)
	e.waitMember(t, runID, "a", "ended")
	resp, out := e.do("POST", "/api/sessions/"+sid+"/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated || out["resumed"] != true {
		t.Fatalf("resume: %d %v", resp.StatusCode, out)
	}
	next := out["session"].(map[string]any)
	t.Cleanup(func() { e.stopAndWait(next["id"].(string)) })
	crew := next["crew"].(map[string]any)
	if next["name"] != "API lead" || crew["member"] != "a" || crew["runId"] != runID {
		t.Fatalf("resumed member: %v", next)
	}
	if got := e.waitRunning(t, runID, "a"); got != next["id"] {
		t.Fatalf("member's session %s, want %s", got, next["id"])
	}
}

// waitMember waits until a member of a run has the status, as GET
// /api/runs/{run} reports it.
func (e *testEnv) waitMember(t *testing.T, runID, name, status string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, out := e.do("GET", "/api/runs/"+runID, adminToken, nil)
		m := runMember(t, out["run"].(map[string]any), name)
		if m["status"] == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is %v, want %s", name, m, status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
