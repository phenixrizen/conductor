package notify

// Mappers turn an agent's hook payload into a Request: an attention state, or an event.

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// permissionOptions mirrors Claude Code's permission dialog. It assumes the
// dialog selects and confirms on the digit key (so no trailing Enter is
// sent) and offers three choices; this is the one place to change if a
// Claude Code release differs. Not yet verified against a live dialog from
// an automated test: see docs/features.md.
func permissionOptions() []Option {
	return []Option{{Label: "Yes", Input: "1"}, {Label: "Always for this session", Input: "2"}, {Label: "No, explain…", Input: "3"}}
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
	SessionID            string          `json:"session_id"`
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
	req, ok = mapClaudeHook(h)
	if ok && req.Event == "" {
		req.AgentSession = truncate(h.SessionID, 128)
		req.Turn = h.HookEventName == "Stop" || h.HookEventName == "UserPromptSubmit"
	}
	return req, ok
}

func mapClaudeHook(h ClaudeHook) (req Request, ok bool) {
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
		case "idle_prompt":
			// Claude Code at rest after its turn ("Claude is waiting for your
			// input", sent after a minute idle) asks nothing: it is done, as
			// its Stop said, and a crew's handoffs and broadcasts reach it.
			return Request{State: "done", Message: truncate(msg, 200), Kind: "done"}, true
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
	Type                 string   `json:"type"`
	LastAssistantMessage string   `json:"last-assistant-message"`
	ThreadID             string   `json:"thread-id"`
	InputMessages        []string `json:"input-messages"`
}

// codexTitlePrompt begins what Codex asks the hidden thread that names a
// conversation (live, codex 0.159): its turn reports through notify like a
// turn of the user's, with a thread id of its own, and is not one.
const codexTitlePrompt = "Generate a concise, single-line task title"

// MapCodex turns a Codex notify payload into an attention update: the end of
// a turn is done, as Claude Code's Stop is (a finished agent is idle, waiting
// for its next line, and a crew types handoffs and broadcasts into it); what
// Codex asks a person comes through its bell and its hooks. The turn of the
// hidden thread that titles a conversation is not the user's, and maps to
// nothing.
func MapCodex(raw []byte) (req Request, ok bool) {
	var p CodexPayload
	if json.Unmarshal(raw, &p) != nil {
		return Request{}, false
	}
	switch p.Type {
	case "agent-turn-complete":
		if len(p.InputMessages) > 0 && strings.HasPrefix(p.InputMessages[0], codexTitlePrompt) {
			return Request{}, false
		}
		msg := truncate(strings.TrimSpace(p.LastAssistantMessage), 200)
		if msg == "" {
			msg = "Codex finished its turn"
		}
		return Request{State: "done", Message: msg, Kind: "done", AgentSession: truncate(p.ThreadID, 128), Turn: true}, true
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
		SessionID            string `json:"session_id"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	switch h.HookEventName {
	case "Stop":
		return Request{State: "done", Message: truncate(strings.TrimSpace(h.LastAssistantMessage), 200), Kind: "done", AgentSession: truncate(h.SessionID, 128), Turn: true}, true
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
		SessionID        string          `json:"sessionId"`
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
		return Request{State: "done", Kind: "done", AgentSession: truncate(h.SessionID, 128), Turn: true}, true
	case "userpromptsubmitted":
		return Request{State: "working", AgentSession: truncate(h.SessionID, 128), Turn: true}, true
	}
	return Request{}, false
}

// MapCursorHook turns a Cursor CLI hook payload into an update: the end of a
// turn, or a tool that ran (a file edit is one, named after the file: its
// name, not its path, which could fill the 100 bytes the server keeps).
func MapCursorHook(raw []byte) (req Request, ok bool) {
	var h struct {
		HookEventName  string `json:"hook_event_name"`
		ToolName       string `json:"tool_name"`
		FilePath       string `json:"file_path"`
		ConversationID string `json:"conversation_id"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	switch h.HookEventName {
	case "stop":
		return Request{State: "done", Kind: "done", AgentSession: truncate(h.ConversationID, 128), Turn: true}, true
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
		ConversationID    string  `json:"conversationId"`
	}
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	switch {
	case h.ToolCall != nil:
		return Request{Event: "tool_use", Tool: h.ToolCall.Name}, true
	case h.TerminationReason != nil:
		return Request{State: "done", Message: truncate(strings.TrimSpace(*h.TerminationReason), 200), Kind: "done", AgentSession: truncate(h.ConversationID, 128), Turn: true}, true
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
