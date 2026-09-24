package catalog

// defaults lists the built-in agents. Operators can override any entry by ID
// or disable the whole set with disableDefaults.
func defaults() []Agent {
	return []Agent{
		{
			ID:          "claude",
			Name:        "Claude Code",
			Description: "Anthropic's terminal coding agent.",
			Command:     []string{"claude"},
			AllowArgs:   true,
			Icon:        "i-lucide-sparkles",
		},
		{
			ID:          "codex",
			Name:        "Codex CLI",
			Description: "OpenAI's terminal coding agent.",
			Command:     []string{"codex"},
			AllowArgs:   true,
			Icon:        "i-lucide-bot",
		},
		{
			ID:          "agy",
			Name:        "Antigravity",
			Description: "Google's Antigravity (Gemini) CLI.",
			Command:     []string{"agy"},
			AllowArgs:   true,
			Icon:        "i-lucide-orbit",
		},
		{
			ID:          "shell",
			Name:        "Shell",
			Description: "Interactive login shell.",
			Command:     []string{"/bin/bash", "-l"},
			AllowArgs:   false,
			Icon:        "i-lucide-terminal",
		},
	}
}
