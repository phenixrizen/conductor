package agents

import "regexp"

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

func agySteps(hooksDir string) []step {
	return []step{{agyHooks, func(h *homeDir) (bool, error) {
		asset, err := assetFor(agyAssets, hooksDir, "agy-hooks.json")
		if err != nil {
			return false, err
		}
		return setJSONKey(h, agyHooks, asset, agyKey)
	}}}
}

func agyAdapter() Adapter {
	return Adapter{
		ID:   "agy",
		Name: "Antigravity",
		// Antigravity prints its version (verify).
		Probe:  &Probe{Args: []string{"--version"}, Match: regexp.MustCompile(`(?mi)^\s*(?:antigravity|agy)?\s*v?(\d+\.\d+\.\d+)`)},
		Assets: agyAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, agySteps(hooksDir)...)
		},
		Status: func(home string) (bool, string) {
			return statusOf(home, agyHooks, agySteps("")...)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(agyAssets, hooksDir, "agy-hooks.json")
		},
		Events: []string{"done", "tool_use"},
	}
}
