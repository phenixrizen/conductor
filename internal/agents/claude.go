package agents

import (
	"path/filepath"

	"github.com/phenixrizen/conductor/internal/catalog"
)

// Claude Code reads extra settings from --settings, so a session launched by
// Conductor gets its hooks without anything written to ~/.claude. Install
// merges the same hooks into ~/.claude/settings.json for Claude Code started
// anywhere else; Claude Code runs a hook command it finds in both only once.

const (
	claudeMarker   = "notify --claude-hook"
	claudeSettings = ".claude/settings.json"
)

func claudeHooks(events ...string) string {
	entry := `{"hooks":[{"type":"command","command":"{{BIN}} notify --claude-hook"}]}`
	return jsonAsset(`{"hooks":{` + hookLists(entry, events...) + `}}`)
}

var claudeAssets = map[string]string{
	"claude.json": claudeHooks("Notification", "Stop", "UserPromptSubmit", "PermissionRequest", "PermissionDenied"),
	// Tool events are chatty: a signal asks for them with toolEvents.
	"claude-tools.json": claudeHooks("Notification", "Stop", "UserPromptSubmit", "PermissionRequest", "PermissionDenied",
		"PostToolUse", "PostToolUseFailure", "SubagentStop"),
}

func claudeAdapter() Adapter {
	return Adapter{
		ID:     "claude",
		Name:   "Claude Code",
		Assets: claudeAssets,
		Inject: func(hooksDir string, sig catalog.Signal) ([]string, map[string]string) {
			settings := "claude.json"
			if sig.ToolEvents {
				settings = "claude-tools.json"
			}
			return []string{"--settings", filepath.Join(hooksDir, settings)}, nil
		},
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, step{claudeSettings, func(h *homeDir) (bool, error) {
				asset, err := assetFor(claudeAssets, hooksDir, "claude.json")
				if err != nil {
					return false, err
				}
				return mergeJSONHooks(h, claudeSettings, asset, claudeMarker)
			}})
		},
		Status: func(home string) (bool, string) {
			return hooksMention(home, claudeSettings, claudeMarker)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(claudeAssets, hooksDir, "claude.json")
		},
		Events: []string{"needs_input", "working", "done", "tool_use", "tool_denied", "error", "progress"},
	}
}
