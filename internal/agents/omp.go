package agents

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

func ompAdapter() Adapter {
	return Adapter{
		ID:     "omp",
		Name:   "oh-my-pi",
		Assets: ompAssets,
		Install: func(home, hooksDir string) ([]string, error) {
			return install(home,
				copyAsset(ompAssets, hooksDir, "omp-conductor.ts", ompExtension),
				step{ompConfig, func(h *homeDir) (bool, error) {
					cur, _, err := h.read(ompConfig)
					if err != nil {
						return false, err
					}
					ext := h.path(ompExtension)
					next, err := withYAMLListItem(string(cur), "extensions", ompMarker, `"`+jsonEscape(ext)+`"`, ext)
					if err != nil {
						return false, byHand("%s: %v", h.path(ompConfig), err)
					}
					if next == string(cur) {
						return false, nil
					}
					return h.write(ompConfig, []byte(next))
				}},
			)
		},
		Status: func(home string) (bool, string) {
			ok, where := fileExists(home, ompExtension)
			if !ok {
				return false, where
			}
			listed, _ := fileMentions(home, ompConfig, where)
			return listed, where
		},
		Snippet: func(hooksDir string) string {
			return snippetOf(ompAssets, hooksDir, "omp-conductor.ts")
		},
		Events: []string{"done", "tool_use"},
	}
}
