package api

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rocksolidlabs/conductor/internal/catalog"
	"github.com/rocksolidlabs/conductor/internal/proto"
)

// A Claude Code session's plan, written outside its folder (under the
// home the agent's process is given, ~/.claude/plans), opens over the
// viewer's connection once the agent's hook names it, lists under the
// `plans` request, and rides the question its approval asks (the
// attention route's `plan`); the credentials beside the plans stay
// refused, and a plan past the bound is a bad request (#289).
func TestPlansOutsideTheFolder(t *testing.T) {
	home := realRoot(t, t.TempDir())
	t.Setenv("HOME", home)
	plans := filepath.Join(home, ".claude", "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(plans, "plan-creating-a-file.md")
	os.WriteFile(plan, []byte("# Plan\n\nCreate hello.txt containing hi.\n"), 0o644)
	os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"token":"secret"}`), 0o600)
	// An agent of Claude Code's adapter (its plans directory), running a
	// shell that reads lines: the adapter's flags land after the command
	// and change nothing for it.
	e := newTestEnvAgents(t, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), []catalog.Agent{
		{ID: "planner", Name: "planner", Command: []string{"/bin/sh", "-c", "cat"}, Adapter: "claude"},
	})
	id := e.createSession("planner")
	c := dialViewer(t, e, id, adminToken)
	c.hello(80, 24)
	c.expectControl(proto.CtlWelcome)
	c.expectControl(proto.CtlReady)
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "r1", Path: plan}))
	if h, _ := c.expectFile("r1"); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("before the agent names it: %+v", h)
	}
	// The agent's hook reports the plan written.
	if resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "file", "op": "write", "path": plan, "tool": "Write"}); resp.StatusCode >= 300 {
		t.Fatalf("event %d %v", resp.StatusCode, out)
	}
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "r2", Path: plan}))
	if h, body := c.expectFile("r2"); h.Kind != "file" || !strings.HasPrefix(string(body), "# Plan") || h.Sha256 == "" {
		t.Fatalf("the plan: %+v %q", h, body)
	}
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "r3", Op: proto.FileOpPlans}))
	if h, _ := c.expectFile("r3"); h.Kind != "plans" || h.Path != plans || len(h.Entries) != 1 || h.Entries[0].Name != "plan-creating-a-file.md" {
		t.Fatalf("plans: %+v", h)
	}
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "r4", Path: filepath.Join(home, ".claude", ".credentials.json")}))
	if h, _ := c.expectFile("r4"); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("the credentials: %+v", h)
	}
	// The approval names the plan: the question carries it, to the API and over the connection.
	resp, out := e.do("POST", "/api/sessions/"+id+"/attention", adminToken, map[string]any{"state": "needs_input", "kind": "permission", "message": "Claude Code has a plan for you to approve", "plan": plan})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attention %d %v", resp.StatusCode, out)
	}
	if att, _ := out["attention"].(map[string]any); att["plan"] != plan {
		t.Fatalf("the question's plan: %v", out)
	}
	// Over the connection: the question, then the plan set on it (the report taken first, the plan after it).
	var carried bool
	for i := 0; i < 2 && !carried; i++ {
		m := c.expectControl(proto.CtlAttention)
		if m["state"] != "needs_input" {
			t.Fatalf("over the connection: %v", m)
		}
		carried = m["plan"] == plan
	}
	if !carried {
		t.Fatal("the question's plan never reached the connection")
	}
	if resp, _ := e.do("POST", "/api/sessions/"+id+"/attention", adminToken, map[string]any{"state": "working"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("working %d", resp.StatusCode)
	}
	if m := c.expectControl(proto.CtlAttention); m["plan"] != nil {
		t.Fatalf("working carries no plan: %v", m)
	}
	if resp, out := e.do("POST", "/api/sessions/"+id+"/attention", adminToken, map[string]any{"state": "needs_input", "plan": "/" + strings.Repeat("p", proto.MaxPlanPath) + ".md"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a plan past the bound: %d %v", resp.StatusCode, out)
	}
	// A report refused names no plan into the session: another plan, named by a report with a kind the route refuses.
	other := filepath.Join(plans, "another.md")
	os.WriteFile(other, []byte("# Another\n"), 0o644)
	if resp, out := e.do("POST", "/api/sessions/"+id+"/attention", adminToken, map[string]any{"state": "needs_input", "kind": "bogus", "plan": other}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a refused report: %d %v", resp.StatusCode, out)
	}
	c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "r5", Path: other}))
	if h, _ := c.expectFile("r5"); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("not learnt from a refused report: %+v", h)
	}
	if resp, out := e.do("POST", "/api/sessions/"+id+"/attention", adminToken, map[string]any{"state": "needs_input", "kind": "permission", "message": "Allow Bash?"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("attention %d %v", resp.StatusCode, out)
	} else if att, _ := out["attention"].(map[string]any); att["plan"] != nil {
		t.Fatalf("the next question carries none: %v", out)
	}
}
