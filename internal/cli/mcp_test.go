package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// mcpRun feeds lines to the MCP server and returns its replies, one per request.
func mcpRun(t *testing.T, getenv func(string) string, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	s := &mcpServer{out: &out, getenv: getenv}
	if code, err := s.serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n")); code != 0 || err != nil {
		t.Fatalf("serve: %d %v", code, err)
	}
	var replies []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("reply %q: %v", l, err)
		}
		replies = append(replies, m)
	}
	return replies
}

func toolText(t *testing.T, reply map[string]any) (string, bool) {
	t.Helper()
	res, _ := reply["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content in %v", reply)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

// The handshake, the tool list, and tool calls that reach the session's
// routes: a report through the notify route, a crew formed through the
// self-service route, the run read back, a link minted.
func TestMcpServesTheSkillsCommandsAsTools(t *testing.T) {
	ns := startNotifyServer(t)
	srv, seen := fakeSelf(t)
	t.Setenv("CONDUCTOR_SESSION_ID", "s1")
	t.Setenv("CONDUCTOR_NOTIFY_URL", srv.URL+"/api/sessions/s1/attention")
	t.Setenv("CONDUCTOR_NOTIFY_TOKEN", "agent-tok")
	_ = ns
	env := func(k string) string {
		switch k {
		case "CONDUCTOR_SESSION_ID":
			return "s1"
		case "CONDUCTOR_NOTIFY_URL":
			return srv.URL + "/api/sessions/s1/attention"
		case "CONDUCTOR_NOTIFY_TOKEN":
			return "agent-tok"
		}
		return ""
	}
	replies := mcpRun(t, env,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"form_crew","arguments":{"name":"review team","goal":"review","members":[{"name":"lead","agentId":"","prompt":"","start":{"when":"manual"}}],"self":"lead","open":true}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"run_status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"link","arguments":{"ttlSeconds":3600,"label":"for the PR"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"resources/list"}`,
		`not json`,
	)
	if len(replies) != 9 {
		t.Fatalf("%d replies: %v", len(replies), replies)
	}
	init, _ := replies[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["serverInfo"].(map[string]any)["name"] != "conductor" || init["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("initialize %v", replies[0])
	}
	tools, _ := replies[1]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "report,set_state,ask,form_crew,add_member,run_status,link" {
		t.Fatalf("tools %v", names)
	}
	for _, tl := range tools {
		if tl.(map[string]any)["inputSchema"].(map[string]any)["type"] != "object" {
			t.Fatalf("tool schema %v", tl)
		}
	}
	if r, _ := replies[2]["result"].(map[string]any); r == nil {
		t.Fatalf("ping %v", replies[2])
	}
	if text, isErr := toolText(t, replies[3]); isErr || !strings.HasPrefix(text, "crew formed: run team-1a2b3c4d at http://example.test/runs/team-1a2b3c4d; you are lead") {
		t.Fatalf("form_crew %q %v", text, isErr)
	}
	body := (*seen)[0]["body"].(map[string]any)
	if body["self"] != "lead" || body["open"] != true || (*seen)[0]["auth"] != "Bearer agent-tok" {
		t.Fatalf("form_crew request %v", (*seen)[0])
	}
	if text, isErr := toolText(t, replies[4]); isErr || !strings.HasPrefix(text, "run team-1a2b3c4d (running) review team; you are lead") || !strings.Contains(text, "reviewer") {
		t.Fatalf("run_status %q %v", text, isErr)
	}
	if text, isErr := toolText(t, replies[5]); isErr || text != "http://example.test/join/tok" {
		t.Fatalf("link %q %v", text, isErr)
	}
	if e, _ := replies[6]["error"].(map[string]any); e == nil || e["code"] != -32602.0 {
		t.Fatalf("unknown tool %v", replies[6])
	}
	if e, _ := replies[7]["error"].(map[string]any); e == nil || e["code"] != -32601.0 {
		t.Fatalf("unknown method %v", replies[7])
	}
	if e, _ := replies[8]["error"].(map[string]any); e == nil || e["code"] != -32700.0 {
		t.Fatalf("parse error %v", replies[8])
	}
}

// report, set_state and ask post to the session's attention and events
// routes with the words notify would use; ask carries its choices.
func TestMcpReportsThroughNotify(t *testing.T) {
	ns := startNotifyServer(t)
	replies := mcpRun(t, os.Getenv,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ask","arguments":{"message":"Which database?","choices":["Postgres","SQLite"]}}}`,
	)
	if text, isErr := toolText(t, replies[0]); isErr || text != "reported" {
		t.Fatalf("ask %q %v", text, isErr)
	}
	if ns.body["state"] != "needs_input" || ns.body["kind"] != "prompt" || ns.body["message"] != "Which database?" {
		t.Fatalf("ask body %v", ns.body)
	}
	opts, _ := ns.body["options"].([]any)
	if len(opts) != 2 || opts[1].(map[string]any)["input"] != "SQLite\r" {
		t.Fatalf("options %v", opts)
	}
	replies = mcpRun(t, os.Getenv,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"report","arguments":{"event":"handoff","to":"tests","message":"the handlers are in"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"set_state","arguments":{"state":"sleepy"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ask","arguments":{"message":"x","choices":["a","b","c","d","e","f","g"]}}}`,
	)
	if text, isErr := toolText(t, replies[0]); isErr || text != "reported" {
		t.Fatalf("report %q %v", text, isErr)
	}
	if ns.path != "/api/sessions/s1/events" || ns.body["type"] != "handoff" || ns.body["to"] != "tests" {
		t.Fatalf("report body %s %v", ns.path, ns.body)
	}
	if text, isErr := toolText(t, replies[1]); !isErr || !strings.Contains(text, "working, done or clear") {
		t.Fatalf("set_state %q %v", text, isErr)
	}
	if text, isErr := toolText(t, replies[2]); !isErr || !strings.Contains(text, "at most 6") {
		t.Fatalf("too many choices %q %v", text, isErr)
	}
}

// Outside a session every tool answers so, as an error the agent reads; the
// handshake still works, so a registration that outlives the session is
// harmless.
func TestMcpOutsideASessionSaysSo(t *testing.T) {
	replies := mcpRun(t, func(string) string { return "" },
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"report","arguments":{"event":"progress","message":"x"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_status","arguments":{}}}`,
	)
	if init, _ := replies[0]["result"].(map[string]any); init["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("initialize %v", replies[0])
	}
	for _, r := range replies[1:] {
		if text, isErr := toolText(t, r); !isErr || !strings.Contains(text, "not running inside a Conductor session") {
			t.Fatalf("%q %v", text, isErr)
		}
	}
	var stderr bytes.Buffer
	if code, err := runMcp(context.Background(), []string{"-h"}, strings.NewReader(""), io.Discard, &stderr); code != 0 || err != nil || !strings.Contains(stderr.String(), "Usage: conductor mcp") {
		t.Fatalf("mcp -h: %d %v %s", code, err, stderr.String())
	}
	if code, err := runMcp(context.Background(), []string{"extra"}, strings.NewReader(""), io.Discard, &stderr); code != 2 || err == nil {
		t.Fatalf("mcp extra: %d %v", code, err)
	}
}
