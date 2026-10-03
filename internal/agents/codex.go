package agents

import (
	"regexp"
	"strings"

	"github.com/phenixrizen/conductor/internal/catalog"
)

// Codex CLI takes config overrides with -c, so a session launched by Conductor
// gets a notify program and the terminal bell without anything written to
// ~/.codex. Install puts the same notify line in a marked block of
// ~/.codex/config.toml and writes ~/.codex/hooks.json, Codex's experimental
// hooks, when there is none; Codex runs those only with features.hooks on.
// Install also puts the Conductor skill in ~/.codex/skills.

const (
	codexConfig = ".codex/config.toml"
	codexHooks  = ".codex/hooks.json"
	codexBegin  = "# >>> conductor"
	codexEnd    = "# <<< conductor"
	codexMarker = "notify --codex-hook"
	// codexHooksHint is what to do about a hooks.json Conductor did not write.
	codexHooksHint = "add Conductor's hooks from the snippet to it by hand (Codex runs the hooks in hooks.json only with features.hooks enabled)"
)

var codexAssets = map[string]string{
	"codex-hooks.json": jsonAsset(`{"hooks":{` + hookLists(`{"hooks":[{"type":"command","command":"{{BIN}} notify --codex-hook"}]}`,
		"PreToolUse", "PostToolUse", "Stop", "PermissionRequest") + `}}`),
}

// codexNotify is the notify setting of config.toml, a TOML array: Codex runs
// the program with the event's JSON as the last argument.
func codexNotify(bin string) string {
	return "notify = [" + tomlString(bin) + `, "notify", "--codex"]`
}

func codexSteps(hooksDir string) []step {
	return []step{
		{codexConfig, func(h *homeDir) (bool, error) {
			bin, err := binPath()
			if err != nil {
				return false, err
			}
			cur, _, err := h.read(codexConfig)
			if err != nil {
				return false, err
			}
			line := codexNotify(bin)
			doc, err := readTOML(string(cur))
			if err != nil {
				return false, byHand("%s cannot be read line by line (%v); put %s at its top by hand", h.path(codexConfig), err, line)
			}
			// A second notify at the root would make config.toml invalid.
			if doc.setsRootKey("notify", codexBegin, codexEnd) {
				return false, byHand("%s sets notify already; make it %s by hand", h.path(codexConfig), line)
			}
			next, err := doc.withBlock(codexBegin, codexEnd, line)
			if err != nil {
				return false, byHand("%s: %v", h.path(codexConfig), err)
			}
			return h.write(codexConfig, []byte(next))
		}},
		{codexHooks, func(h *homeDir) (bool, error) {
			asset, err := assetFor(codexAssets, hooksDir, "codex-hooks.json")
			if err != nil {
				return false, err
			}
			return createJSON(h, codexHooks, asset, codexMarker, codexHooksHint)
		}},
		skillStep(hooksDir, codexSkill),
	}
}

func codexAdapter() Adapter {
	return Adapter{
		ID:   "codex",
		Name: "Codex CLI",
		// Verified live (0.159.0): `codex --version` prints "codex-cli 0.159.0".
		Probe:  &Probe{Args: []string{"--version"}, Match: regexp.MustCompile(`(?m)^\s*codex-cli\s+(\d+\.\d+\.\d+)`), Verified: true},
		Assets: codexAssets,
		// The per-launch trust override Codex takes in place of the trust
		// saved in config.toml: -c projects={"<dir>"={trust_level="trusted"}}
		// (the dotted -c form does not skip the question). Applied only with
		// the yolo recipe.
		TrustArgs: func(dirs []string) []string {
			parts := make([]string, 0, len(dirs))
			for _, d := range dirs {
				parts = append(parts, tomlString(d)+`={trust_level="trusted"}`)
			}
			return []string{"-c", "projects={" + strings.Join(parts, ",") + "}"}
		},
		Inject: func(hooksDir string, sig catalog.Signal) ([]string, map[string]string) {
			bin, err := binPath()
			if err != nil {
				return nil, nil
			}
			return []string{"-c", "notify=[" + tomlString(bin) + `,"notify","--codex"]`, "-c", `tui.notification_method="bel"`}, nil
		},
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, codexSteps(hooksDir)...)
		},
		SkillPath: codexSkill,
		Status: func(home string) (bool, string) {
			return statusOf(home, codexConfig, codexSteps("")...)
		},
		Snippet: func(hooksDir string) string {
			bin, err := binPath()
			if err != nil {
				bin = "conductor"
			}
			return "# ~/.codex/config.toml, at the top:\n" + codexBegin + "\n" + codexNotify(bin) + "\n" + codexEnd + "\n\n" +
				"# ~/.codex/hooks.json (Codex runs these hooks only with features.hooks enabled):\n" +
				snippetOf(codexAssets, hooksDir, "codex-hooks.json")
		},
		Events: []string{"needs_input", "working", "done", "tool_use"},
	}
}
