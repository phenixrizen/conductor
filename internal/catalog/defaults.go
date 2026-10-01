package catalog

// defaults lists the built-in agents: one for each agent Conductor has a hook
// adapter for (internal/agents), with the signal its adapter reports
// through and its website as best known (to be checked in a browser: the
// open verification of round 3 in docs/features.md), then a login shell.
// Operators can override any entry by ID or disable the whole set with
// disableDefaults.
func defaults() []Agent {
	return []Agent{
		{
			ID:          "claude",
			Name:        "Claude Code",
			Description: "Anthropic's terminal coding agent.",
			Command:     []string{"claude"},
			AllowArgs:   true,
			Icon:        "i-lucide-sparkles",
			Site:        "https://claude.com/claude-code",
			Adapter:     "claude",
			Signal:      &Signal{Kind: SignalHook},
		},
		{
			ID:          "codex",
			Name:        "Codex CLI",
			Description: "OpenAI's terminal coding agent.",
			Command:     []string{"codex"},
			AllowArgs:   true,
			Icon:        "i-lucide-code-xml",
			Site:        "https://developers.openai.com/codex/cli",
			Adapter:     "codex",
			Signal:      &Signal{Kind: SignalHook},
		},
		{
			ID:          "agy",
			Name:        "Antigravity",
			Description: "Google's Antigravity (Gemini) CLI.",
			Command:     []string{"agy"},
			AllowArgs:   true,
			Icon:        "i-lucide-rocket",
			Site:        "https://antigravity.google",
			Adapter:     "agy",
			// Antigravity fires no event while it waits for the user.
			Signal: &Signal{Kind: SignalBell},
		},
		{
			ID:          "copilot",
			Name:        "Copilot CLI",
			Description: "GitHub Copilot's coding agent in the terminal.",
			Command:     []string{"copilot"},
			AllowArgs:   true,
			Icon:        "i-lucide-github",
			Site:        "https://github.com/features/copilot/cli",
			Adapter:     "copilot",
			Signal:      &Signal{Kind: SignalHook},
		},
		{
			ID:          "cursor",
			Name:        "Cursor CLI",
			Description: "Cursor's coding agent in the terminal.",
			Command:     []string{"cursor-agent"},
			AllowArgs:   true,
			Icon:        "i-lucide-mouse-pointer-2",
			Site:        "https://cursor.com/cli",
			Adapter:     "cursor",
			// The CLI fires no event while it waits: its prompt on the last
			// line says so.
			Signal: &Signal{Kind: SignalPattern, Pattern: `^› $`},
		},
		{
			ID:          "opencode",
			Name:        "OpenCode",
			Description: "Open source terminal coding agent.",
			Command:     []string{"opencode"},
			AllowArgs:   true,
			Icon:        "i-lucide-braces",
			Site:        "https://opencode.ai",
			Adapter:     "opencode",
			Signal:      &Signal{Kind: SignalHook},
		},
		{
			ID:          "pi",
			Name:        "pi",
			Description: "Minimal, extensible terminal coding agent.",
			Command:     []string{"pi"},
			AllowArgs:   true,
			Icon:        "i-lucide-pi",
			Site:        "https://github.com/badlogic/pi-mono",
			Adapter:     "pi",
			Signal:      &Signal{Kind: SignalHook},
		},
		{
			ID:          "omp",
			Name:        "oh-my-pi",
			Description: "Coding agent built on pi.",
			Command:     []string{"omp"},
			AllowArgs:   true,
			Icon:        "i-lucide-pi-square",
			Site:        "https://github.com/can1357/oh-my-pi",
			Adapter:     "omp",
			Signal:      &Signal{Kind: SignalHook},
		},
		{
			ID:          "aider",
			Name:        "aider",
			Description: "AI pair programming in the terminal.",
			Command:     []string{"aider"},
			AllowArgs:   true,
			Icon:        "i-lucide-git-commit",
			Site:        "https://aider.chat",
			Adapter:     "aider",
			Signal:      &Signal{Kind: SignalHook},
		},
		{
			ID:          "goose",
			Name:        "Goose",
			Description: "Open source AI agent from Block.",
			Command:     []string{"goose"},
			AllowArgs:   true,
			Icon:        "i-lucide-feather",
			Site:        "https://block.github.io/goose/",
			Adapter:     "goose",
			// Goose fires no event while it waits for the user.
			Signal: &Signal{Kind: SignalBell},
		},
		{
			ID:          "amp",
			Name:        "Amp",
			Description: "Amp's coding agent in the terminal.",
			Command:     []string{"amp"},
			AllowArgs:   true,
			Icon:        "i-lucide-zap",
			Site:        "https://ampcode.com",
			Adapter:     "amp",
			Signal:      &Signal{Kind: SignalHook},
		},
		{
			ID:          "dsh",
			Name:        "DeepSeek Harness",
			Description: "DeepSeek's agent harness (developer preview).",
			Command:     []string{"dsh"},
			AllowArgs:   true,
			Icon:        "i-lucide-cpu",
			Site:        "https://github.com/deepseek-ai/dsh",
			Adapter:     "dsh",
			// Its plugin is installed by hand, if at all: nothing is assumed.
			Signal: &Signal{Kind: SignalNone},
		},
		{
			ID:          "shell",
			Name:        "Shell",
			Description: "Interactive login shell.",
			Command:     []string{"/bin/bash", "-l"},
			AllowArgs:   false,
			Icon:        "i-lucide-terminal",
			// Readline rings the bell on a failed completion or a backspace
			// at the start of the line: no call for attention while a person
			// types.
			Signal: &Signal{Kind: SignalNone},
		},
	}
}
