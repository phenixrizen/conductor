package agents

// Cursor CLI reads its hooks from ~/.cursor/hooks.json, which Install merges
// into. The CLI fires no event while it waits for the user: a catalog entry
// for it watches the screen for its prompt instead (a pattern signal).

const (
	cursorMarker = "notify --cursor-hook"
	cursorHooks  = ".cursor/hooks.json"
)

var cursorAssets = map[string]string{
	"cursor-hooks.json": jsonAsset(`{"version":1,"hooks":{` + hookLists(`{"command":"{{BIN}} notify --cursor-hook"}`,
		"stop", "postToolUse", "afterFileEdit") + `}}`),
}

func cursorSteps(hooksDir string) []step {
	return []step{mergeHooksStep(cursorAssets, hooksDir, "cursor-hooks.json", cursorHooks, cursorMarker)}
}

func cursorAdapter() Adapter {
	return Adapter{
		ID:     "cursor",
		Name:   "Cursor CLI",
		Assets: cursorAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, cursorSteps(hooksDir)...)
		},
		Status: func(home string) (bool, string) {
			return statusOf(home, cursorHooks, cursorSteps("")...)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(cursorAssets, hooksDir, "cursor-hooks.json")
		},
		Events: []string{"done", "tool_use"},
	}
}
