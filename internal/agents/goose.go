package agents

// Goose loads plugins from ~/.agents/plugins/<name>/, hooks from the plugin's
// hooks/hooks.json. Conductor's plugin is a directory of its own there, which
// Install copies whole. Goose fires no event while it waits for the user: the
// bell or a screen pattern covers that.

const gooseDir = ".agents/plugins/conductor/"

var gooseAssets = map[string]string{
	"goose/hooks/hooks.json": jsonAsset(`{"hooks":{` + hookLists(`{"hooks":[{"type":"command","command":"{{BIN}} notify --goose-hook"}]}`,
		"Stop", "PostToolUse") + `}}`),
}

func gooseAdapter() Adapter {
	return Adapter{
		ID:     "goose",
		Name:   "Goose",
		Assets: gooseAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, copyAssetDir(gooseAssets, hooksDir, "goose/", gooseDir)...)
		},
		Status: func(home string) (bool, string) {
			return statusOf(home, gooseDir+"hooks/hooks.json", copyAssetDir(gooseAssets, "", "goose/", gooseDir)...)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(gooseAssets, hooksDir, "goose/hooks/hooks.json")
		},
		Events: []string{"done", "tool_use"},
	}
}
