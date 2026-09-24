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
	"strings"
	"time"
)

// Environment variables injected into every Conductor session.
const (
	EnvSessionID = "CONDUCTOR_SESSION_ID"
	EnvURL       = "CONDUCTOR_NOTIFY_URL"
	EnvToken     = "CONDUCTOR_NOTIFY_TOKEN"
)

// Request is the attention update to send.
type Request struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
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

// Send posts the request to url with the agent token. Redirects are refused
// so a token never follows a rewrite, and only one attempt is made.
func Send(ctx context.Context, url, token string, req Request) error {
	body, err := json.Marshal(req)
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
	HookEventName        string `json:"hook_event_name"`
	NotificationType     string `json:"notification_type"`
	Message              string `json:"message"`
	Title                string `json:"title"`
	LastAssistantMessage string `json:"last_assistant_message"`
	ToolName             string `json:"tool_name"`
}

// MapClaudeHook turns a Claude Code hook payload into an attention update.
// ok is false for events that carry no attention meaning.
func MapClaudeHook(raw []byte) (req Request, ok bool) {
	var h ClaudeHook
	if json.Unmarshal(raw, &h) != nil {
		return Request{}, false
	}
	switch h.HookEventName {
	case "Notification", "PermissionRequest":
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
		}
		return Request{State: "needs_input", Message: truncate(msg, 200)}, true
	case "Stop":
		return Request{State: "done", Message: truncate(strings.TrimSpace(h.LastAssistantMessage), 200)}, true
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "SessionStart":
		return Request{State: "working"}, true
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
		return Request{State: "needs_input", Message: msg}, true
	}
	return Request{}, false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ReadAllBounded reads at most 1 MiB from r.
func ReadAllBounded(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 1<<20))
}

// Getenv is os.Getenv, exposed for tests.
var Getenv = os.Getenv
