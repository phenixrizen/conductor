package agents

import "regexp"

// GitHub Copilot CLI loads every hook file in ~/.copilot/hooks/, so Conductor
// keeps its hooks in a file of its own there and merges nothing.

const copilotFile = ".copilot/hooks/conductor.json"

var copilotAssets = map[string]string{
	"copilot.json": jsonAsset(`{"version":1,"hooks":{` + hookLists(`{"type":"command","bash":"{{BIN}} notify --copilot-hook"}`,
		"notification", "agentStop", "userPromptSubmitted", "postToolUse", "errorOccurred") + `}}`),
}

func copilotSteps(hooksDir string) []step {
	return []step{copyAsset(copilotAssets, hooksDir, "copilot.json", copilotFile), skillStep(hooksDir, agentsSkill)}
}

func copilotAdapter() Adapter {
	return Adapter{
		ID:   "copilot",
		Name: "Copilot CLI",
		// The Copilot CLI names itself in its version line (verify).
		Probe:  &Probe{Args: []string{"--version"}, Match: regexp.MustCompile(`(?mi)copilot.*?(\d+\.\d+\.\d+)`)},
		Assets: copilotAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, copilotSteps(hooksDir)...)
		},
		Status: func(home string) (bool, string) {
			return statusOf(home, copilotFile, copilotSteps("")...)
		},
		// The Copilot CLI reads ~/.agents/skills (and ~/.copilot/skills; docs.github.com, "About agent skills").
		SkillPath: agentsSkill,
		Snippet: func(hooksDir string) string {
			return snippetOf(copilotAssets, hooksDir, "copilot.json")
		},
		Events: []string{"needs_input", "working", "done", "tool_use", "error"},
	}
}
