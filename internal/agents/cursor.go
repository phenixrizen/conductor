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

func cursorAdapter() Adapter {
	return Adapter{
		ID:     "cursor",
		Name:   "Cursor CLI",
		Assets: cursorAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, step{cursorHooks, func(h *homeDir) (bool, error) {
				asset, err := assetFor(cursorAssets, hooksDir, "cursor-hooks.json")
				if err != nil {
					return false, err
				}
				return mergeJSONHooks(h, cursorHooks, asset, cursorMarker)
			}})
		},
		Status: func(home string) (bool, string) {
			return hooksMention(home, cursorHooks, cursorMarker)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(cursorAssets, hooksDir, "cursor-hooks.json")
		},
		Events: []string{"done", "tool_use"},
	}
}
