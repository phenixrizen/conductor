package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/session"
)

// agentOf gives the session a known agent token and returns it.
func agentOf(t *testing.T, e *testEnv, id string) string {
	t.Helper()
	d, ok := e.srv.Registry().Get(id)
	if !ok {
		t.Fatal("no session")
	}
	tok := "agent-token-" + id
	d.(*session.Local).SetAgentToken(tok)
	return tok
}

func crewBody(self string, members ...map[string]any) map[string]any {
	return map[string]any{"name": "review team", "goal": "review the change", "isolation": "none", "members": members, "self": self, "open": true}
}

func memberDef(name, agent, when string) map[string]any {
	return map[string]any{"name": name, "agentId": agent, "prompt": "do " + name, "start": map[string]any{"when": when}}
}

func TestAgentTokenFormsACrewAndIsItsFirstMember(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	tok := agentOf(t, e, id)
	resp, out := e.do("POST", "/api/sessions/"+id+"/crew", tok, crewBody("lead", memberDef("lead", "cat", "manual"), memberDef("reviewer", "cat", "immediately"), memberDef("tester", "cat", "after")))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("after without a member: %d %v", resp.StatusCode, out)
	}
	body := crewBody("lead", memberDef("lead", "cat", "manual"), memberDef("reviewer", "cat", "immediately"), map[string]any{"name": "tester", "agentId": "cat", "prompt": "test", "start": map[string]any{"when": "after", "member": "lead"}})
	resp, out = e.do("POST", "/api/sessions/"+id+"/crew", tok, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	run := out["run"].(map[string]any)
	if out["member"] != "lead" || !strings.HasSuffix(out["url"].(string), "/runs/"+run["id"].(string)) {
		t.Fatalf("reply %v", out)
	}
	members := map[string]map[string]any{}
	for _, m := range run["members"].([]any) {
		mm := m.(map[string]any)
		members[mm["name"].(string)] = mm
	}
	if members["lead"]["sessionId"] != id || members["lead"]["status"] != "running" {
		t.Fatalf("lead %v", members["lead"])
	}
	if members["reviewer"]["status"] != "starting" && members["reviewer"]["status"] != "running" {
		t.Fatalf("reviewer %v", members["reviewer"])
	}
	if members["tester"]["status"] != "pending" {
		t.Fatalf("tester %v", members["tester"])
	}
	// The session is tagged with the run, and its activity names it.
	_, sess := e.do("GET", "/api/sessions/"+id, adminToken, nil)
	crewRef, _ := sess["session"].(map[string]any)["crew"].(map[string]any)
	if crewRef == nil || crewRef["runId"] != run["id"] || crewRef["member"] != "lead" {
		t.Fatalf("session crew %v", sess["session"].(map[string]any)["crew"])
	}
	d, _ := e.srv.Registry().Get(id)
	var link, status bool
	for _, en := range d.(*session.Local).Activity() {
		if en.Type == session.ActivityLink && strings.Contains(en.Message, "formed crew review team (open)") && strings.HasSuffix(en.URL, "/runs/"+run["id"].(string)) {
			link = true
		}
		if en.Type == session.ActivityStatus && strings.Contains(en.Message, "formed crew review team: 3 members, as lead") {
			status = true
		}
	}
	if !link || !status {
		t.Fatalf("activity %+v", d.(*session.Local).Activity())
	}
	// The crew is saved, with the session's working directory, where server.
	crew := out["crew"].(map[string]any)
	if crew["cwd"] != e.root || crew["where"] != "server" {
		t.Fatalf("crew %v", crew)
	}
	// Its run is read with the session's token, and a member added.
	resp, out = e.do("GET", "/api/sessions/"+id+"/run", tok, nil)
	if resp.StatusCode != http.StatusOK || out["member"] != "lead" || out["run"].(map[string]any)["id"] != run["id"] {
		t.Fatalf("run: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("POST", "/api/sessions/"+id+"/run/members", tok, memberDef("docs", "cat", "manual"))
	if resp.StatusCode != http.StatusCreated || len(out["run"].(map[string]any)["members"].([]any)) != 4 {
		t.Fatalf("add: %d %v", resp.StatusCode, out)
	}
	// Forming another while in a run is refused.
	if resp, out := e.do("POST", "/api/sessions/"+id+"/crew", tok, body); resp.StatusCode != http.StatusConflict || out["error"].(map[string]any)["code"] != "in_a_run" {
		t.Fatalf("second crew: %d %v", resp.StatusCode, out)
	}
}

func TestAgentCrewInheritsCwdAndYoloAndNeverEscalates(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.Yolo = false })
	id := e.createSession("cat")
	tok := agentOf(t, e, id)
	// The body cannot name a cwd, yolo or where: unknown fields are refused.
	resp, out := e.do("POST", "/api/sessions/"+id+"/crew", tok, map[string]any{"name": "x", "goal": "g", "cwd": "/", "members": []any{memberDef("me", "cat", "manual")}, "self": "me"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cwd in the body: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("POST", "/api/sessions/"+id+"/crew", tok, map[string]any{"name": "x", "goal": "g", "yolo": true, "members": []any{memberDef("me", "cat", "manual")}, "self": "me"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("yolo in the body: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("POST", "/api/sessions/"+id+"/crew", tok, crewBody("me", memberDef("me", "cat", "manual"), memberDef("other", "cat", "immediately")))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	if run := out["run"].(map[string]any); run["yolo"] != false || run["cwd"] != e.root {
		t.Fatalf("run %v", run)
	}
	// The self member must be of the session's agent.
	id2 := e.createSession("cat")
	tok2 := agentOf(t, e, id2)
	resp, out = e.do("POST", "/api/sessions/"+id2+"/crew", tok2, crewBody("me", memberDef("me", "sh", "manual")))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(out["error"].(map[string]any)["message"].(string), "session's") {
		t.Fatalf("other agent as self: %d %v", resp.StatusCode, out)
	}
}

// A session forms at most two crews an hour and mints at most five links a
// day: the counters, with a clock of their own.
func TestAgentCrewIsRateLimited(t *testing.T) {
	c := newSelfCounters()
	now := time.Now()
	c.now = func() time.Time { return now }
	for i := range selfCrewsPerHour {
		if !c.take(c.crews, "s1", selfCrewsPerHour, time.Hour) {
			t.Fatalf("crew %d refused", i)
		}
	}
	if c.take(c.crews, "s1", selfCrewsPerHour, time.Hour) {
		t.Fatal("a third crew in the hour")
	}
	if !c.take(c.crews, "s2", selfCrewsPerHour, time.Hour) {
		t.Fatal("another session is its own count")
	}
	now = now.Add(61 * time.Minute)
	if !c.take(c.crews, "s1", selfCrewsPerHour, time.Hour) {
		t.Fatal("an hour later")
	}
	for range selfLinksPerDay {
		c.take(c.links, "s1", selfLinksPerDay, 24*time.Hour)
	}
	if c.take(c.links, "s1", selfLinksPerDay, 24*time.Hour) {
		t.Fatal("a sixth link in the day")
	}
}

func TestSelfServiceRefusesOtherCallers(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	other := e.createSession("cat")
	otherTok := agentOf(t, e, other)
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "control"})
	shareTok := lo["token"].(string)
	for name, tok := range map[string]string{"no token": "", "another session's agent": otherTok, "a share token": shareTok, "a host token": "test-host-token"} {
		resp, _ := e.do("POST", "/api/sessions/"+id+"/crew", tok, crewBody("me", memberDef("me", "cat", "manual")))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
		resp, _ = e.do("GET", "/api/sessions/"+id+"/run", tok, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s run: %d", name, resp.StatusCode)
		}
		resp, _ = e.do("POST", "/api/sessions/"+id+"/links/agent", tok, map[string]any{})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s link: %d", name, resp.StatusCode)
		}
	}
	// The admin may act for the session.
	if resp, _ := e.do("GET", "/api/sessions/"+id+"/run", adminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("admin, no run: %d", resp.StatusCode)
	}
}

func TestSelfServiceOffReturns403(t *testing.T) {
	off := false
	e := newTestEnv(t, func(c *config.Config) { c.Agents.SelfService = &off })
	id := e.createSession("cat")
	tok := agentOf(t, e, id)
	resp, out := e.do("POST", "/api/sessions/"+id+"/crew", tok, crewBody("me", memberDef("me", "cat", "manual")))
	if resp.StatusCode != http.StatusForbidden || out["error"].(map[string]any)["code"] != "self_service_off" {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
}

func TestAgentLinkIsViewOnlyAndBounded(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	tok := agentOf(t, e, id)
	resp, out := e.do("POST", "/api/sessions/"+id+"/links/agent", tok, map[string]any{"label": "for the PR"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	link := out["link"].(map[string]any)
	if link["role"] != "view" || link["label"] != "for the PR" || !strings.Contains(out["url"].(string), "/join/") {
		t.Fatalf("link %v", out)
	}
	exp, _ := time.Parse(time.RFC3339Nano, link["expiresAt"].(string))
	if d := time.Until(exp); d < 110*time.Minute || d > 130*time.Minute {
		t.Fatalf("default ttl: %s", d)
	}
	if resp, out := e.do("POST", "/api/sessions/"+id+"/links/agent", tok, map[string]any{"ttlSeconds": 90000}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a day and more: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("POST", "/api/sessions/"+id+"/links/agent", tok, map[string]any{"role": "control"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a role in the body: %d", resp.StatusCode)
	}
	for range selfLinksPerDay - 1 {
		if resp, _ := e.do("POST", "/api/sessions/"+id+"/links/agent", tok, map[string]any{}); resp.StatusCode != http.StatusCreated {
			t.Fatalf("within the day's five: %d", resp.StatusCode)
		}
	}
	if resp, _ := e.do("POST", "/api/sessions/"+id+"/links/agent", tok, map[string]any{}); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the sixth: %d", resp.StatusCode)
	}
}
