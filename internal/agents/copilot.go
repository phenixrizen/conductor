package agents

// GitHub Copilot CLI loads every hook file in ~/.copilot/hooks/, so Conductor
// keeps its hooks in a file of its own there and merges nothing.

const copilotFile = ".copilot/hooks/conductor.json"

var copilotAssets = map[string]string{
	"copilot.json": jsonAsset(`{"version":1,"hooks":{` + hookLists(`{"type":"command","bash":"{{BIN}} notify --copilot-hook"}`,
		"notification", "agentStop", "userPromptSubmitted", "postToolUse", "errorOccurred") + `}}`),
}

func copilotAdapter() Adapter {
	return Adapter{
		ID:     "copilot",
		Name:   "Copilot CLI",
		Assets: copilotAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, copyAsset(copilotAssets, hooksDir, "copilot.json", copilotFile))
		},
		Status: func(home string) (bool, string) {
			return fileExists(home, copilotFile)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(copilotAssets, hooksDir, "copilot.json")
		},
		Events: []string{"needs_input", "working", "done", "tool_use", "error"},
	}
}
