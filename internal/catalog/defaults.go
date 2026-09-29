package catalog

// defaults lists the built-in agents: one for each agent Conductor has a hook
// adapter for (internal/agents), with the signal its adapter reports
// through, then a login shell. Operators can override any entry by ID or
// disable the whole set with disableDefaults.
func defaults() []Agent {
	return []Agent{
		{
			ID:          "claude",
			Name:        "Claude Code",
			Description: "Anthropic's terminal coding agent.",
			Command:     []string{"claude"},
			AllowArgs:   true,
			Icon:        "i-lucide-sparkles",
			Adapter:     "claude",
			Signal:      &Signal{Kind: "hook"},
		},
		{
			ID:          "codex",
			Name:        "Codex CLI",
			Description: "OpenAI's terminal coding agent.",
			Command:     []string{"codex"},
			AllowArgs:   true,
			Icon:        "i-lucide-code-xml",
			Adapter:     "codex",
			Signal:      &Signal{Kind: "hook"},
		},
		{
			ID:          "agy",
			Name:        "Antigravity",
			Description: "Google's Antigravity (Gemini) CLI.",
			Command:     []string{"agy"},
			AllowArgs:   true,
			Icon:        "i-lucide-rocket",
			Adapter:     "agy",
			// Antigravity fires no event while it waits for the user.
			Signal: &Signal{Kind: "bell"},
		},
		{
			ID:          "copilot",
			Name:        "Copilot CLI",
			Description: "GitHub Copilot's coding agent in the terminal.",
			Command:     []string{"copilot"},
			AllowArgs:   true,
			Icon:        "i-lucide-github",
			Adapter:     "copilot",
			Signal:      &Signal{Kind: "hook"},
		},
		{
			ID:          "cursor",
			Name:        "Cursor CLI",
			Description: "Cursor's coding agent in the terminal.",
			Command:     []string{"cursor-agent"},
			AllowArgs:   true,
			Icon:        "i-lucide-mouse-pointer-2",
			Adapter:     "cursor",
			// The CLI fires no event while it waits: its prompt on the last
			// line says so.
			Signal: &Signal{Kind: "pattern", Pattern: `^› $`},
		},
		{
			ID:          "opencode",
			Name:        "OpenCode",
			Description: "Open source terminal coding agent.",
			Command:     []string{"opencode"},
			AllowArgs:   true,
			Icon:        "i-lucide-braces",
			Adapter:     "opencode",
			Signal:      &Signal{Kind: "hook"},
		},
		{
			ID:          "pi",
			Name:        "pi",
			Description: "Minimal, extensible terminal coding agent.",
			Command:     []string{"pi"},
			AllowArgs:   true,
			Icon:        "i-lucide-pi",
			Adapter:     "pi",
			Signal:      &Signal{Kind: "hook"},
		},
		{
			ID:          "omp",
			Name:        "oh-my-pi",
			Description: "Coding agent built on pi.",
			Command:     []string{"omp"},
			AllowArgs:   true,
			Icon:        "i-lucide-pi-square",
			Adapter:     "omp",
			Signal:      &Signal{Kind: "hook"},
		},
		{
			ID:          "aider",
			Name:        "aider",
			Description: "AI pair programming in the terminal.",
			Command:     []string{"aider"},
			AllowArgs:   true,
			Icon:        "i-lucide-git-commit",
			Adapter:     "aider",
			Signal:      &Signal{Kind: "hook"},
		},
		{
			ID:          "goose",
			Name:        "Goose",
			Description: "Open source AI agent from Block.",
			Command:     []string{"goose"},
			AllowArgs:   true,
			Icon:        "i-lucide-feather",
			Adapter:     "goose",
			// Goose fires no event while it waits for the user.
			Signal: &Signal{Kind: "bell"},
		},
		{
			ID:          "amp",
			Name:        "Amp",
			Description: "Amp's coding agent in the terminal.",
			Command:     []string{"amp"},
			AllowArgs:   true,
			Icon:        "i-lucide-zap",
			Adapter:     "amp",
			Signal:      &Signal{Kind: "hook"},
		},
		{
			ID:          "dsh",
			Name:        "DeepSeek Harness",
			Description: "DeepSeek's agent harness (developer preview).",
			Command:     []string{"dsh"},
			AllowArgs:   true,
			Icon:        "i-lucide-cpu",
			Adapter:     "dsh",
			// Its plugin is installed by hand, if at all: nothing is assumed.
			Signal: &Signal{Kind: "none"},
		},
		{
			ID:          "shell",
			Name:        "Shell",
			Description: "Interactive login shell.",
			Command:     []string{"/bin/bash", "-l"},
			AllowArgs:   false,
			Icon:        "i-lucide-terminal",
			Signal:      &Signal{Kind: "bell"},
		},
	}
}
