// Package notify implements `conductor notify`: a tiny client agents (or
// their hooks) run inside a Conductor session to report whether they are
// waiting for a human. It reads the session's URL and token from the
// environment that Conductor injects into the PTY.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Environment variables injected into every Conductor session.
const (
	EnvSessionID = "CONDUCTOR_SESSION_ID"
	EnvURL       = "CONDUCTOR_NOTIFY_URL"
	EnvToken     = "CONDUCTOR_NOTIFY_TOKEN"
)

// Option is one quick-reply choice; Input is what the browser types for it.
type Option struct {
	Label string `json:"label"`
	Input string `json:"input"`
}

// Request is the update to send: an attention state, or an event when Event
// is set. Kind and Options let the workbench offer one-click answers (see
// docs/protocol.md, Attention).
type Request struct {
	State   string   `json:"state"`
	Message string   `json:"message,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Options []Option `json:"options,omitempty"`
	// Event reports something the agent did instead of a state: one of the
	// six event types of docs/protocol.md (Events), or an attention word
	// (needs_input, working, done, clear). URL, To and Tool go with the events
	// that use them. Send ignores State for an event.
	Event string `json:"event,omitempty"`
	URL   string `json:"url,omitempty"`
	To    string `json:"to,omitempty"`
	Tool  string `json:"tool,omitempty"`
}

// eventBody is what the events route reads: the same report under its own
// names. The route refuses fields it does not know, so an event cannot travel
// as a Request.
type eventBody struct {
	Type    string   `json:"type"`
	Message string   `json:"message,omitempty"`
	URL     string   `json:"url,omitempty"`
	To      string   `json:"to,omitempty"`
	Tool    string   `json:"tool,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Options []Option `json:"options,omitempty"`
}

// permissionOptions mirrors Claude Code's permission dialog. It assumes the
// dialog selects and confirms on the digit key (so no trailing Enter is
// sent) and offers three choices; this is the one place to change if a
// Claude Code release differs. Not yet verified against a live dialog from
// an automated test: see docs/features.md.
func permissionOptions() []Option {
	return []Option{{Label: "Yes", Input: "1"}, {Label: "Always for this session", Input: "2"}, {Label: "No, explain…", Input: "3"}}
}

// ErrNotInSession is returned when the environment is not set. Callers treat
// it as a silent no-op so hooks are safe outside Conductor.
var ErrNotInSession = errors.New("notify: not running inside a conductor session")

// FromEnv builds the target from the environment.
func FromEnv(getenv func(string) string) (url, token string, err error) {
	url, token = getenv(EnvURL), getenv(EnvToken)
	if url == "" || token == "" {
		return "", "", ErrNotInSession
	}
	return url, token, nil
}

// eventsURL turns a session's attention URL, which is the one Conductor puts
// in the environment, into the URL of its events route. Any other URL is
// where the caller pointed it, and stays.
func eventsURL(u string) string {
	if base, ok := strings.CutSuffix(u, "/attention"); ok {
		return base + "/events"
	}
	return u
}

// Send posts the request to url with the agent token. A request with Event
// set goes to the session's events route (url with /attention replaced by
// /events) as an event; any other goes to url as an attention update, as it
// always did. Redirects are refused so a token never follows a rewrite, and
// only one attempt is made.
func Send(ctx context.Context, url, token string, req Request) error {
	var payload any = req
	if req.Event != "" {
		url = eventsURL(url)
		payload = eventBody{Type: req.Event, Message: req.Message, URL: req.URL, To: req.To, Tool: req.Tool, Kind: req.Kind, Options: req.Options}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("notify: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// ClaudeHook is the subset of the Claude Code hook stdin payload we use.
// Field names follow the documented common fields; message/title/
// notification_type are read when present.
type ClaudeHook struct {
	HookEventName        string          `json:"hook_event_name"`
	NotificationType     string          `json:"notification_type"`
	Message              string          `json:"message"`
	Title                string          `json:"title"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	ToolName             string          `json:"tool_name"`
	Error                json.RawMessage `json:"error"`
}

// MapClaudeHook turns a Claude Code hook payload into an attention update, or
// into an event for the tool hooks: a denied permission, a tool that ran or
// failed, a subagent that finished. ok is false for hooks that mean nothing
// to Conductor.
func MapClaudeHook(raw []byte) (req Request, ok bool) {
	var h ClaudeHook
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	switch h.HookEventName {
	case "PermissionRequest":
		msg := strings.TrimSpace(h.Message)
		if msg == "" && h.ToolName != "" {
			msg = "Allow " + h.ToolName + "?"
		}
		if msg == "" {
			msg = "Allow this action?"
		}
		return Request{State: "needs_input", Message: truncate(msg, 200), Kind: "permission", Options: permissionOptions()}, true
	case "Notification":
		msg := strings.TrimSpace(h.Message)
		if msg == "" {
			msg = strings.TrimSpace(h.Title)
		}
		if msg == "" {
			msg = strings.ReplaceAll(h.NotificationType, "_", " ")
		}
		if msg == "" {
			msg = "Claude Code needs your input"
		}
		switch h.NotificationType {
		case "auth_success", "elicitation_complete", "elicitation_response", "agent_completed":
			return Request{State: "working", Message: truncate(msg, 200)}, true
		case "permission_prompt":
			return Request{State: "needs_input", Message: truncate(msg, 200), Kind: "permission", Options: permissionOptions()}, true
		}
		return Request{State: "needs_input", Message: truncate(msg, 200), Kind: "prompt"}, true
	case "Stop":
		return Request{State: "done", Message: truncate(strings.TrimSpace(h.LastAssistantMessage), 200), Kind: "done"}, true
	case "UserPromptSubmit", "PreToolUse", "SessionStart":
		return Request{State: "working"}, true
	case "PermissionDenied":
		return Request{Event: "tool_denied", Tool: h.ToolName}, true
	case "PostToolUse":
		return Request{Event: "tool_use", Tool: h.ToolName}, true
	case "PostToolUseFailure":
		return Request{Event: "error", Message: truncate(errorText(h.Error), 200), Tool: h.ToolName}, true
	case "SubagentStop":
		return Request{Event: "progress", Message: "subagent finished"}, true
	}
	return Request{}, false
}

// CodexPayload is the subset of the Codex notify payload we use.
type CodexPayload struct {
	Type                 string `json:"type"`
	LastAssistantMessage string `json:"last-assistant-message"`
}

// MapCodex turns a Codex notify payload into an attention update.
func MapCodex(raw []byte) (req Request, ok bool) {
	var p CodexPayload
	if json.Unmarshal(raw, &p) != nil {
		return Request{}, false
	}
	switch p.Type {
	case "agent-turn-complete":
		msg := truncate(strings.TrimSpace(p.LastAssistantMessage), 200)
		if msg == "" {
			msg = "Codex finished its turn"
		}
		return Request{State: "needs_input", Message: msg, Kind: "prompt"}, true
	}
	return Request{}, false
}

// MapCodexHook turns a payload of Codex's hooks (hooks.json, run with
// features.hooks on) into an update. MapCodex reads the payload of Codex's
// notify program instead. A permission request has no quick-reply options:
// the keys Codex's dialog takes are not verified yet.
func MapCodexHook(raw []byte) (req Request, ok bool) {
	var h struct {
		HookEventName        string `json:"hook_event_name"`
		ToolName             string `json:"tool_name"`
		LastAssistantMessage string `json:"last_assistant_message"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	switch h.HookEventName {
	case "Stop":
		return Request{State: "done", Message: truncate(strings.TrimSpace(h.LastAssistantMessage), 200), Kind: "done"}, true
	case "PostToolUse":
		return Request{Event: "tool_use", Tool: h.ToolName}, true
	case "PermissionRequest":
		msg := "Codex asks for permission"
		if h.ToolName != "" {
			msg = "Allow " + h.ToolName + "?"
		}
		return Request{State: "needs_input", Message: truncate(msg, 200), Kind: "permission"}, true
	case "PreToolUse":
		return Request{State: "working"}, true
	}
	return Request{}, false
}

// MapCopilotHook turns a GitHub Copilot CLI hook payload into an update.
// Copilot names few of its events: a hook_event_name it knows decides, and
// otherwise the fields that are there do. notification_type is a
// notification, error an error, toolResult a tool that ran, stopReason the end
// of a turn and prompt a prompt the user sent. A permission prompt has no
// quick-reply options: the keys Copilot's dialog takes are not verified yet.
func MapCopilotHook(raw []byte) (req Request, ok bool) {
	var h struct {
		HookEventName    string          `json:"hook_event_name"`
		NotificationType *string         `json:"notification_type"`
		Message          string          `json:"message"`
		Title            string          `json:"title"`
		ToolName         string          `json:"toolName"`
		ToolResult       json.RawMessage `json:"toolResult"`
		StopReason       *string         `json:"stopReason"`
		Prompt           *string         `json:"prompt"`
		Error            json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	event := strings.ToLower(h.HookEventName)
	if !slices.Contains([]string{"notification", "erroroccurred", "posttooluse", "agentstop", "userpromptsubmitted"}, event) {
		// No name, or one this mapper does not know: the fields decide.
		event = ""
		switch {
		case h.NotificationType != nil:
			event = "notification"
		case present(h.Error):
			event = "erroroccurred"
		case present(h.ToolResult):
			event = "posttooluse"
		case h.StopReason != nil:
			event = "agentstop"
		case h.Prompt != nil:
			event = "userpromptsubmitted"
		}
	}
	switch event {
	case "notification":
		msg := truncate(firstOf(h.Message, h.Title, "Copilot needs your input"), 200)
		if h.NotificationType != nil && *h.NotificationType == "permission_prompt" {
			return Request{State: "needs_input", Message: msg, Kind: "permission"}, true
		}
		return Request{State: "needs_input", Message: msg, Kind: "prompt"}, true
	case "erroroccurred":
		return Request{Event: "error", Message: truncate(errorText(h.Error), 200), Tool: h.ToolName}, true
	case "posttooluse":
		return Request{Event: "tool_use", Tool: h.ToolName}, true
	case "agentstop":
		return Request{State: "done", Kind: "done"}, true
	case "userpromptsubmitted":
		return Request{State: "working"}, true
	}
	return Request{}, false
}

// MapCursorHook turns a Cursor CLI hook payload into an update: the end of a
// turn, or a tool that ran (a file edit is one, named after the file: its
// name, not its path, which could fill the 100 bytes the server keeps).
func MapCursorHook(raw []byte) (req Request, ok bool) {
	var h struct {
		HookEventName string `json:"hook_event_name"`
		ToolName      string `json:"tool_name"`
		FilePath      string `json:"file_path"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	switch h.HookEventName {
	case "stop":
		return Request{State: "done", Kind: "done"}, true
	case "postToolUse":
		return Request{Event: "tool_use", Tool: h.ToolName}, true
	case "afterFileEdit":
		if h.FilePath == "" {
			return Request{Event: "tool_use", Tool: "edit"}, true
		}
		return Request{Event: "tool_use", Tool: "edit " + path.Base(h.FilePath)}, true
	}
	return Request{}, false
}

// MapAgyHook turns an Antigravity hook payload into an update. The payload
// names no event, and the flag that ran the mapper says only that it is
// Antigravity's: a toolCall is a tool that ran (PostToolUse), a
// terminationReason the end of a turn (Stop).
func MapAgyHook(raw []byte) (req Request, ok bool) {
	var h struct {
		ToolCall *struct {
			Name string `json:"name"`
		} `json:"toolCall"`
		TerminationReason *string `json:"terminationReason"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	switch {
	case h.ToolCall != nil:
		return Request{Event: "tool_use", Tool: h.ToolCall.Name}, true
	case h.TerminationReason != nil:
		return Request{State: "done", Message: truncate(strings.TrimSpace(*h.TerminationReason), 200), Kind: "done"}, true
	}
	return Request{}, false
}

// MapGooseHook turns a Goose hook payload, which names its event in "event",
// into an update: the end of a turn, or a tool that ran.
func MapGooseHook(raw []byte) (req Request, ok bool) {
	var h struct {
		Event    string `json:"event"`
		ToolName string `json:"tool_name"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	switch h.Event {
	case "Stop":
		return Request{State: "done", Kind: "done"}, true
	case "PostToolUse":
		return Request{Event: "tool_use", Tool: h.ToolName}, true
	}
	return Request{}, false
}

// present reports whether a JSON field was sent with a value other than null.
func present(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

// errorText reads an error a hook reports, which is a string or an object
// with a message.
func errorText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var e struct {
		Message string `json:"message"`
		Name    string `json:"name"`
	}
	if json.Unmarshal(raw, &e) == nil {
		return strings.TrimSpace(firstOf(e.Message, e.Name, ""))
	}
	return ""
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// truncate cuts s to at most n bytes, on a character boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ReadAllBounded reads at most 1 MiB from r.
func ReadAllBounded(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 1<<20))
}

// Getenv is os.Getenv, exposed for tests.
var Getenv = os.Getenv
