package agents

// Goose loads plugins from ~/.agents/plugins/<name>/, hooks from the plugin's
// hooks/hooks.json. Conductor's plugin is a directory of its own there, which
// Install copies whole, with the Conductor skill in ~/.agents/skills. Goose
// fires no event while it waits for the user: the bell or a screen pattern
// covers that.

const gooseDir = ".agents/plugins/conductor/"

var gooseAssets = map[string]string{
	"goose/hooks/hooks.json": jsonAsset(`{"hooks":{` + hookLists(`{"hooks":[{"type":"command","command":"{{BIN}} notify --goose-hook"}]}`,
		"Stop", "PostToolUse") + `}}`),
}

func gooseSteps(hooksDir string) []step {
	return append(copyAssetDir(gooseAssets, hooksDir, "goose/", gooseDir), skillStep(hooksDir, agentsSkill))
}

func gooseAdapter() Adapter {
	return Adapter{
		ID:     "goose",
		Name:   "Goose",
		Assets: gooseAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, gooseSteps(hooksDir)...)
		},
		Status: func(home string) (bool, string) {
			return statusOf(home, gooseDir+"hooks/hooks.json", gooseSteps("")...)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(gooseAssets, hooksDir, "goose/hooks/hooks.json")
		},
		Events: []string{"done", "tool_use"},
	}
}
