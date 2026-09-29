package agents

import (
	"github.com/phenixrizen/conductor/internal/catalog"
)

// Codex CLI takes config overrides with -c, so a session launched by Conductor
// gets a notify program and the terminal bell without anything written to
// ~/.codex. Install puts the same notify line in a marked block of
// ~/.codex/config.toml and writes ~/.codex/hooks.json, Codex's experimental
// hooks (they run only with features.hooks on), when there is none.

const (
	codexConfig = ".codex/config.toml"
	codexHooks  = ".codex/hooks.json"
	codexBegin  = "# >>> conductor"
	codexEnd    = "# <<< conductor"
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

func codexAdapter() Adapter {
	return Adapter{
		ID:     "codex",
		Name:   "Codex CLI",
		Assets: codexAssets,
		Inject: func(hooksDir string, sig catalog.Signal) ([]string, map[string]string) {
			bin, err := binPath()
			if err != nil {
				return nil, nil
			}
			return []string{"-c", "notify=[" + tomlString(bin) + `,"notify","--codex"]`, "-c", `tui.notification_method="bel"`}, nil
		},
		Install: func(home, hooksDir string) ([]string, error) {
			bin, err := binPath()
			if err != nil {
				return nil, err
			}
			return install(home,
				step{codexConfig, func(h *homeDir) (bool, error) {
					cur, _, err := h.read(codexConfig)
					if err != nil {
						return false, err
					}
					// A second notify at the root would make config.toml invalid.
					if tomlSetsRootKey(string(cur), "notify", codexBegin, codexEnd) {
						return false, byHand("%s sets notify already; make it %s by hand", h.path(codexConfig), codexNotify(bin))
					}
					next, err := withMarkedBlock(string(cur), codexBegin, codexEnd, codexNotify(bin))
					if err != nil {
						return false, byHand("%s: %v", h.path(codexConfig), err)
					}
					return h.write(codexConfig, []byte(next))
				}},
				step{codexHooks, func(h *homeDir) (bool, error) {
					asset, err := assetFor(codexAssets, hooksDir, "codex-hooks.json")
					if err != nil {
						return false, err
					}
					return createJSON(h, codexHooks, asset)
				}},
			)
		},
		Status: func(home string) (bool, string) {
			return fileHasLine(home, codexConfig, codexBegin)
		},
		Snippet: func(hooksDir string) string {
			bin, err := binPath()
			if err != nil {
				bin = "conductor"
			}
			return codexBegin + "\n" + codexNotify(bin) + "\n" + codexEnd + "\n"
		},
		Events: []string{"needs_input", "working", "done", "tool_use"},
	}
}
