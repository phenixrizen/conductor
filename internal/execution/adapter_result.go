package execution

import (
	"bytes"
	"encoding/json"
)

// A zero process exit alone does not establish an assistant completed its turn.
// Require the pinned CLI's terminal protocol event; missing/unknown output stays
// unavailable. These events establish generation only, never passing checks.
func assistantCompleted(adapter string, output []byte) bool {
	if adapter == "command/v1" {
		return true
	}
	completed := false
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		var event struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			IsError *bool  `json:"is_error"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if adapter == "codex/0.154.0" {
			if event.Type == "turn.failed" || event.Type == "error" {
				return false
			}
			if event.Type == "turn.completed" {
				completed = true
			}
		} else if event.Type == "result" {
			if event.IsError == nil || *event.IsError || event.Subtype != "success" {
				return false
			}
			completed = true
		}
	}
	return completed
}
