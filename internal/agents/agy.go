package agents

// Antigravity reads hooks from ~/.gemini/config/hooks.json, grouped under
// named top-level keys; Conductor's group is "conductor". It has no launch
// flag for them and no event for waiting on the user (the bell covers that),
// so Install writes the group and nothing is injected.

const (
	agyHooks = ".gemini/config/hooks.json"
	agyKey   = "conductor"
)

var agyAssets = map[string]string{
	"agy-hooks.json": jsonAsset(`{"conductor":{"enabled":true,` +
		hookLists(`{"matcher":"*","hooks":[{"type":"command","command":"{{BIN}} notify --agy-hook"}]}`, "Stop", "PostToolUse") + `}}`),
}

func agyAdapter() Adapter {
	return Adapter{
		ID:     "agy",
		Name:   "Antigravity",
		Assets: agyAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, step{agyHooks, func(h *homeDir) (bool, error) {
				asset, err := assetFor(agyAssets, hooksDir, "agy-hooks.json")
				if err != nil {
					return false, err
				}
				return setJSONKey(h, agyHooks, asset, agyKey)
			}})
		},
		Status: func(home string) (bool, string) {
			return hasJSONKey(home, agyHooks, agyKey)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(agyAssets, hooksDir, "agy-hooks.json")
		},
		Events: []string{"done", "tool_use"},
	}
}
