package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/notify"
	"github.com/phenixrizen/conductor/internal/version"
)

// conductor mcp is Conductor's MCP server on stdio: the same reports and
// crew actions the skill teaches as commands, as tools an agent calls
// directly. The server launches it for agents that take an MCP server at
// launch (Claude Code's --mcp-config, Codex's -c mcp_servers.…). Outside a
// Conductor session every tool answers that it is outside one, so a
// registration that outlives the session does no harm. The protocol is
// JSON-RPC 2.0, one message a line, with initialize, ping, tools/list and
// tools/call; everything else is a method-not-found.
const mcpUsage = `Usage: conductor mcp

Serves Conductor's MCP tools on stdin and stdout (JSON-RPC 2.0, one message a line):
report, set_state, ask, form_crew, add_member, run_status and link, the skill's
commands as tools. The server registers it with agents that take an MCP server at
launch; run it by hand only to try it.
`

// mcpProtocolVersion is the MCP revision answered when the client's is unknown.
const mcpProtocolVersion = "2025-06-18"

// maxMCPLine bounds one message.
const maxMCPLine = 1 << 20

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

// mcpTool is one tool as tools/list describes it.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func mcpTools() []mcpTool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	obj := func(props map[string]any, required ...string) map[string]any {
		out := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	return []mcpTool{
		{Name: "report", Description: "Report something you did to the people watching this session: progress on a long task (at milestones), an artifact such as a pull request (with its url), a handoff to another crew member (with to), a tool refused or an error. One short line.",
			InputSchema: obj(map[string]any{
				"event":   map[string]any{"type": "string", "enum": []string{"progress", "artifact", "handoff", "tool_use", "tool_denied", "error", "file"}},
				"message": str("one line, at most 500 characters"),
				"url":     str("artifact: where it lives"),
				"to":      str("handoff: the member the work goes to"),
				"tool":    str("tool_use, tool_denied, error, file: the tool involved"),
				"op":      map[string]any{"type": "string", "enum": []string{"read", "edit", "write", "delete"}, "description": "file: what you did to it"},
				"path":    str("file: the file's path"),
			}, "event")},
		{Name: "set_state", Description: "Tell the people watching that you are working, done, or clear the state; use ask for a question.",
			InputSchema: obj(map[string]any{"state": map[string]any{"type": "string", "enum": []string{"working", "done", "clear"}}, "message": str("one line")}, "state")},
		{Name: "ask", Description: "Ask the people watching for a decision you cannot make yourself; the session shows as needing input until someone answers in the terminal. With choices (at most 6, each at most 40 characters), name them in the terminal too: a click types the choice as a line.",
			InputSchema: obj(map[string]any{"message": str("the question, one line"), "choices": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 6}}, "message")},
		{Name: "form_crew", Description: "Form a crew around this session when the work splits into parts that can run side by side: you stay as the member named by self (its agentId and prompt may be empty), the others get sessions of their own. At most 2 a session; never for work you can finish alone.",
			InputSchema: obj(map[string]any{
				"name":      str("the crew's name"),
				"goal":      str("the goal, reaching prompts as $GOAL"),
				"members":   map[string]any{"type": "array", "description": "{name, agentId, prompt, start: {when: immediately|after|manual, member?}}", "items": map[string]any{"type": "object"}},
				"self":      str("the member this session becomes"),
				"isolation": map[string]any{"type": "string", "enum": []string{"none", "worktree"}},
				"open":      map[string]any{"type": "boolean", "description": "offer the run in the workbench"},
			}, "name", "goal", "members", "self")},
		{Name: "add_member", Description: "Add a member to this session's run: {name, agentId, prompt, start}.",
			InputSchema: obj(map[string]any{"member": map[string]any{"type": "object"}}, "member")},
		{Name: "run_status", Description: "This session's run and its members, with their states.", InputSchema: obj(map[string]any{})},
		{Name: "link", Description: "A view-only link to this session, for a pull request or a message; a day at most.",
			InputSchema: obj(map[string]any{"ttlSeconds": map[string]any{"type": "integer"}, "label": str("what it is for")})},
	}
}

// mcpServer serves one agent.
type mcpServer struct {
	out    io.Writer
	getenv func(string) string
}

func runMcp(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if len(args) > 0 {
		if args[0] == "-h" || args[0] == "--help" {
			fmt.Fprint(stderr, mcpUsage)
			return 0, nil
		}
		fmt.Fprint(stderr, mcpUsage)
		return 2, errors.New("mcp takes no arguments")
	}
	s := &mcpServer{out: stdout, getenv: os.Getenv}
	return s.serve(ctx, stdin)
}

// serve reads messages until stdin ends.
func (s *mcpServer) serve(ctx context.Context, in io.Reader) (int, error) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), maxMCPLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil || req.JSONRPC != "2.0" {
			s.reply(mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{Code: -32700, Message: "parse error: one JSON-RPC 2.0 message a line"}})
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			// A notification (initialized, cancelled, …): nothing to answer.
			continue
		}
		result, rpcErr := s.handle(ctx, req)
		if rpcErr != nil {
			s.reply(mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr})
		} else {
			s.reply(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return 1, err
	}
	return 0, nil
}

func (s *mcpServer) reply(r mcpResponse) {
	b, _ := json.Marshal(r)
	_, _ = s.out.Write(append(b, '\n'))
}

func (s *mcpServer) handle(ctx context.Context, req mcpRequest) (any, *mcpError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		pv := p.ProtocolVersion
		if pv == "" {
			pv = mcpProtocolVersion
		}
		return map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "conductor", "version": version.Version},
			"instructions":    "Conductor shows this session to people in a browser. Report milestones, artifacts and handoffs with report; ask with ask when you need a decision; form a crew with form_crew when the work splits.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return nil, &mcpError{Code: -32602, Message: "tools/call needs a name"}
		}
		text, err := s.call(ctx, p.Name, p.Arguments)
		if err != nil {
			var unknown unknownTool
			if errors.As(err, &unknown) {
				return nil, &mcpError{Code: -32602, Message: err.Error()}
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil
	}
	return nil, &mcpError{Code: -32601, Message: "method not found: " + req.Method}
}

type unknownTool string

func (u unknownTool) Error() string { return "unknown tool " + string(u) }

var errNotInSession = errors.New("not running inside a Conductor session: nothing to report to")

// call runs one tool; what it returns is the text the agent reads.
func (s *mcpServer) call(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	switch name {
	case "report", "set_state", "ask":
		url, token, err := notify.FromEnv(s.getenv)
		if err != nil {
			return "", errNotInSession
		}
		var req notify.Request
		switch name {
		case "report":
			var a struct {
				Event, Message, URL, To, Tool, Op, Path string
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", fmt.Errorf("report: %w", err)
			}
			if a.Event == "" {
				return "", errors.New("report needs an event")
			}
			req = notify.Request{Event: a.Event, Message: a.Message, URL: a.URL, To: a.To, Tool: a.Tool, Op: a.Op, Path: a.Path}
		case "set_state":
			var a struct{ State, Message string }
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", fmt.Errorf("set_state: %w", err)
			}
			if a.State != "working" && a.State != "done" && a.State != "clear" {
				return "", errors.New("set_state: state must be working, done or clear")
			}
			req = notify.Request{State: a.State, Message: a.Message}
		case "ask":
			var a struct {
				Message string
				Choices []string
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", fmt.Errorf("ask: %w", err)
			}
			if strings.TrimSpace(a.Message) == "" {
				return "", errors.New("ask needs a message")
			}
			req = notify.Request{State: "needs_input", Message: a.Message}
			if len(a.Choices) > 0 {
				opts, err := parseChoices(strings.Join(a.Choices, "|"))
				if err != nil {
					return "", err
				}
				req.Kind = "prompt"
				req.Options = opts
			}
		}
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := notify.Send(sctx, url, token, req); err != nil {
			return "", err
		}
		return "reported", nil
	case "form_crew", "add_member", "run_status", "link":
		c, err := crewClientFromEnv(s.getenv)
		if err != nil {
			return "", errNotInSession
		}
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		switch name {
		case "form_crew":
			if s, _ := body["self"].(string); s == "" {
				return "", errors.New("form_crew: say which member this session becomes (self)")
			}
			out, err := c.call(cctx, http.MethodPost, "/crew", body)
			if err != nil {
				return "", err
			}
			run, _ := out["run"].(map[string]any)
			return fmt.Sprintf("crew formed: run %v at %v; you are %v\n%s", run["id"], out["url"], out["member"], membersText(run)), nil
		case "add_member":
			m, _ := body["member"].(map[string]any)
			if m == nil {
				return "", errors.New("add_member needs a member")
			}
			out, err := c.call(cctx, http.MethodPost, "/run/members", m)
			if err != nil {
				return "", err
			}
			run, _ := out["run"].(map[string]any)
			return fmt.Sprintf("added %v\n%s", m["name"], membersText(run)), nil
		case "run_status":
			out, err := c.call(cctx, http.MethodGet, "/run", nil)
			if err != nil {
				return "", err
			}
			run, _ := out["run"].(map[string]any)
			return fmt.Sprintf("run %v (%v) %v; you are %v\n%s", run["id"], run["state"], run["name"], out["member"], membersText(run)), nil
		case "link":
			out, err := c.call(cctx, http.MethodPost, "/links/agent", body)
			if err != nil {
				return "", err
			}
			return fmt.Sprint(out["url"]), nil
		}
	}
	return "", unknownTool(name)
}

// membersText lists a run's members as printMembers does, as text.
func membersText(run map[string]any) string {
	var b strings.Builder
	printMembers(&b, run)
	return strings.TrimRight(b.String(), "\n")
}
