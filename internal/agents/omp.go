package agents

import "regexp"

// oh-my-pi runs pi's extensions. Its --extension flag is not reliable, so
// nothing is injected: Install copies pi's extension into
// ~/.omp/agent/extensions and lists it under extensions: in
// ~/.omp/agent/config.yml, below a "# conductor" marker.

const (
	ompExtension = ".omp/agent/extensions/conductor.ts"
	ompConfig    = ".omp/agent/config.yml"
	ompMarker    = "# conductor"
)

var ompAssets = map[string]string{"omp-conductor.ts": piScript}

func ompSteps(hooksDir string) []step {
	return []step{
		copyAsset(ompAssets, hooksDir, "omp-conductor.ts", ompExtension),
		{ompConfig, func(h *homeDir) (bool, error) {
			cur, _, err := h.read(ompConfig)
			if err != nil {
				return false, err
			}
			// The entry is a YAML double-quoted string, whose escapes are
			// JSON's; it is looked for as written.
			ext := jsonEscape(h.path(ompExtension))
			next, err := withYAMLListItem(string(cur), "extensions", ompMarker, `"`+ext+`"`, ext)
			if err != nil {
				return false, byHand("%s: %v", h.path(ompConfig), err)
			}
			if next == string(cur) {
				return false, nil
			}
			return h.write(ompConfig, []byte(next))
		}},
	}
}

func ompAdapter() Adapter {
	return Adapter{
		ID:   "omp",
		Name: "oh-my-pi",
		// oh-my-pi prints its version (verify); it must not pass as pi.
		Probe:  &Probe{Args: []string{"--version"}, Match: regexp.MustCompile(`(?mi)^\s*(?:omp|oh-my-pi)\s+v?(\d+\.\d+\.\d+)`)},
		Assets: ompAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home, ompSteps(hooksDir)...)
		},
		Status: func(home string) (bool, string) {
			return statusOf(home, ompExtension, ompSteps("")...)
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(ompAssets, hooksDir, "omp-conductor.ts")
		},
		Events: []string{"done", "tool_use"},
	}
}
