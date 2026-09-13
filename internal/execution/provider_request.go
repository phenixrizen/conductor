package execution

import (
	"encoding/json"
	"strings"
)

// The gateway is a model capability, not an account or delegated-network proxy.
// Keep an explicit request profile: hosted tools, remote source URLs, stored
// objects and background work would outlive or expand the task's authority.
func modelOnlyRequest(adapter, model string, object map[string]json.RawMessage) bool {
	var requested string
	if model == "" || json.Unmarshal(object["model"], &requested) != nil || requested != model {
		return false
	}
	fields := "model input instructions tools tool_choice parallel_tool_calls stream stream_options max_output_tokens reasoning text include store metadata prompt_cache_key prompt_cache_retention service_tier truncation temperature top_p top_logprobs safety_identifier max_tool_calls context_management client_metadata access_programs"
	if adapter == "claude-code/2.1.270" {
		fields = "model messages max_tokens system tools tool_choice metadata stop_sequences stream temperature thinking top_k top_p output_config context_management service_tier"
	}
	allowed := map[string]bool{}
	for _, field := range strings.Fields(fields) {
		allowed[field] = true
	}
	for field := range object {
		if !allowed[field] {
			return false
		}
	}
	if raw, ok := object["access_programs"]; ok {
		var program map[string]string
		if json.Unmarshal(raw, &program) != nil || len(program) != 1 || program["cyber"] != "standard" {
			return false
		}
	}
	if raw, ok := object["tools"]; ok {
		var list []map[string]json.RawMessage
		if json.Unmarshal(raw, &list) != nil || len(list) > 128 {
			return false
		}
		for _, tool := range list {
			if !localTool(adapter, tool, 0) {
				return false
			}
		}
	}
	if raw, ok := object["tool_choice"]; ok {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			if text != "auto" && text != "none" && text != "required" {
				return false
			}
		} else {
			var choice map[string]json.RawMessage
			var kind string
			if json.Unmarshal(raw, &choice) != nil || json.Unmarshal(choice["type"], &kind) != nil {
				return false
			}
			switch kind {
			case "auto", "any", "none", "tool", "function", "custom":
			default:
				return false
			}
		}
	}
	for _, field := range []string{"input", "messages", "system"} {
		if raw, ok := object[field]; ok {
			var value any
			if json.Unmarshal(raw, &value) != nil || !inlineModelInput(value, 0) {
				return false
			}
		}
	}
	if adapter == "codex/0.154.0" {
		object["store"] = json.RawMessage("false")
	}
	return true
}
func localTool(adapter string, tool map[string]json.RawMessage, depth int) bool {
	if depth > 2 {
		return false
	}
	var kind string
	if raw, ok := tool["type"]; ok && json.Unmarshal(raw, &kind) != nil {
		return false
	}
	if adapter == "claude-code/2.1.270" {
		return kind == "" || kind == "custom"
	}
	switch kind {
	case "function", "custom", "local_shell":
		return true
	case "namespace":
		var nested []map[string]json.RawMessage
		if json.Unmarshal(tool["tools"], &nested) != nil || len(nested) > 128 {
			return false
		}
		for _, child := range nested {
			if !localTool(adapter, child, depth+1) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
func inlineModelInput(value any, depth int) bool {
	if depth > 64 {
		return false
	}
	switch v := value.(type) {
	case []any:
		for _, child := range v {
			if !inlineModelInput(child, depth+1) {
				return false
			}
		}
	case map[string]any:
		kind, _ := v["type"].(string)
		switch kind {
		case "", "message", "input_text", "input_image", "input_audio", "output_text", "reasoning", "summary_text", "reasoning_text", "text", "thinking", "redacted_thinking", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "local_shell_call", "local_shell_call_output", "compaction", "image", "document", "base64", "content", "tool_use", "tool_result", "search_result":
		default:
			return false
		}
		// An untyped provider item ID is a lookup rather than inline source.
		if kind == "" && v["id"] != nil {
			return false
		}
		for key, child := range v {
			// Local tool arguments are inert JSON in this request. They are interpreted
			// by repository-side tools, never dereferenced by the model provider.
			if kind == "tool_use" && key == "input" {
				continue
			}
			if key == "file_id" || key == "file_url" || key == "server_url" || key == "previous_response_id" || key == "conversation" {
				if child != nil {
					return false
				}
			}
			if key == "image_url" || key == "audio_url" {
				url, ok := child.(string)
				if !ok || !strings.HasPrefix(url, "data:") {
					return false
				}
			}
			if !inlineModelInput(child, depth+1) {
				return false
			}
		}
	}
	return true
}
