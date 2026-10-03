package agents

import (
	"path/filepath"
	"regexp"
	"slices"

	"github.com/phenixrizen/conductor/internal/catalog"
)

// Claude Code reads extra settings from --settings, so a session launched by
// Conductor gets its hooks without anything written to ~/.claude. Install
// merges the same hooks into ~/.claude/settings.json for Claude Code started
// anywhere else; Claude Code runs a hook command it finds in both only once.
// Install also puts the Conductor skill in ~/.claude/skills.

const (
	claudeMarker   = "notify --claude-hook"
	claudeSettings = ".claude/settings.json"
)

func claudeHooks(events ...string) string {
	return jsonAsset(`{` + claudeHookList(events...) + `}`)
}

// claudeHookList is the "hooks" member of a settings file for events.
func claudeHookList(events ...string) string {
	entry := `{"hooks":[{"type":"command","command":"{{BIN}} notify --claude-hook"}]}`
	return `"hooks":{` + hookLists(entry, events...) + `}`
}

// claudeYoloKey skips the warning Claude Code shows before its first launch
// with --dangerously-skip-permissions (the yolo recipe). Claude Code honours
// only the last --settings, so it goes in the file that carries the hooks.
const claudeYoloKey = `"skipDangerousModePermissionPrompt":true`

var (
	claudeEvents      = []string{"Notification", "Stop", "UserPromptSubmit", "PermissionRequest", "PermissionDenied"}
	claudeToolsEvents = append(slices.Clone(claudeEvents), "PostToolUse", "PostToolUseFailure", "SubagentStop")
)

var claudeAssets = map[string]string{
	"claude.json": claudeHooks(claudeEvents...),
	// Tool events are chatty: a signal asks for them with toolEvents.
	"claude-tools.json": claudeHooks(claudeToolsEvents...),
	// The same with the yolo key, for a launch with the yolo recipe, and the
	// key alone for one whose hooks are not wired.
	"claude-yolo.json":       jsonAsset(`{` + claudeYoloKey + `,` + claudeHookList(claudeEvents...) + `}`),
	"claude-tools-yolo.json": jsonAsset(`{` + claudeYoloKey + `,` + claudeHookList(claudeToolsEvents...) + `}`),
	"claude-yolo-only.json":  jsonAsset(`{` + claudeYoloKey + `}`),
	// Conductor's MCP server, for --mcp-config (Claude Code 2.1.288 takes
	// JSON files or strings; verified with --help).
	"claude-mcp.json": jsonAsset(`{"mcpServers":{"conductor":{"command":"{{BIN}}","args":["mcp"]}}}`),
}

func claudeSteps(hooksDir string) []step {
	return []step{mergeHooksStep(claudeAssets, hooksDir, "claude.json", claudeSettings, claudeMarker), skillStep(hooksDir, claudeSkill)}
}

func claudeAdapter() Adapter {
	return Adapter{
		ID:   "claude",
		Name: "Claude Code",
		// Verified live (2.1.287): `claude --version` prints "2.1.287 (Claude Code)".
		Probe:  &Probe{Args: []string{"--version"}, Match: regexp.MustCompile(`(?m)^\s*(\d+\.\d+\.\d+)\s*\(Claude Code\)`), Verified: true},
		Assets: claudeAssets,
		Inject: func(hooksDir string, sig catalog.Signal) ([]string, map[string]string) {
			settings := "claude.json"
			if sig.ToolEvents {
				settings = "claude-tools.json"
			}
			return []string{"--settings", filepath.Join(hooksDir, settings)}, nil
		},
		YoloInject: func(hooksDir string, sig catalog.Signal) ([]string, map[string]string) {
			settings := "claude-yolo-only.json"
			switch {
			case sig.Kind == catalog.SignalHook && sig.ToolEvents:
				settings = "claude-tools-yolo.json"
			case sig.Kind == catalog.SignalHook:
				settings = "claude-yolo.json"
			}
			return []string{"--settings", filepath.Join(hooksDir, settings)}, nil
		},
		ConfirmsSubmit: true,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, claudeSteps(hooksDir)...)
		},
		SkillPath: claudeSkill,
		MCP: func(hooksDir string) []string {
			return []string{"--mcp-config", filepath.Join(hooksDir, "claude-mcp.json")}
		},
		Status: func(home string) (bool, string) {
			return statusOf(home, claudeSettings, claudeSteps("")...)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(claudeAssets, hooksDir, "claude.json")
		},
		Events: []string{"needs_input", "working", "done", "tool_use", "tool_denied", "error", "progress"},
	}
}
