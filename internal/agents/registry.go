package agents

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/phenixrizen/conductor/internal/catalog"
)

// registry lists every adapter in the order the Events page shows them.
var registry = []Adapter{
	claudeAdapter(),
	codexAdapter(),
	agyAdapter(),
	copilotAdapter(),
	cursorAdapter(),
	opencodeAdapter(),
	piAdapter(),
	ompAdapter(),
	aiderAdapter(),
	gooseAdapter(),
	ampAdapter(),
	dshAdapter(),
}

// Get returns the adapter with the given ID.
func Get(id string) (Adapter, bool) {
	for _, a := range registry {
		if a.ID == id {
			return a.clone(), true
		}
	}
	return Adapter{}, false
}

// CheckAdapter reports an error when id names no adapter Conductor has; ""
// (no adapter) passes. The error lists the adapters there are. The catalog
// cannot check this itself, since the adapters import it: the config's agents
// are checked when it is loaded (config.LoadCatalog), saved ones when they are
// saved and at startup (internal/api).
func CheckAdapter(id string) error {
	if id == "" {
		return nil
	}
	if _, ok := Get(id); ok {
		return nil
	}
	ids := make([]string, 0, len(registry))
	for _, a := range registry {
		ids = append(ids, a.ID)
	}
	return fmt.Errorf("unknown adapter %q (one of %s)", id, strings.Join(ids, ", "))
}

// All returns every adapter, in a stable order.
func All() []Adapter {
	out := make([]Adapter, 0, len(registry))
	for _, a := range registry {
		out = append(out, a.clone())
	}
	return out
}

// clone returns a copy of a that shares no map or slice with the registry.
func (a Adapter) clone() Adapter {
	a.Assets = maps.Clone(a.Assets)
	a.Events = slices.Clone(a.Events)
	return a
}

// InjectFor returns the flags to append to an agent's command and the
// environment to add to its session, for the adapter agentID and the agent's
// signal. Only the "hook" signal is wired at launch; any other signal, an
// unknown or empty adapter, an adapter with no launch route and a hooks dir
// that is not absolute (the flags name files in it, and a relative path would
// be read from the session's working directory) all give nil, nil.
func InjectFor(agentID string, hooksDir string, sig catalog.Signal) ([]string, map[string]string) {
	if sig.Kind != catalog.SignalHook || !filepath.IsAbs(hooksDir) {
		return nil, nil
	}
	for _, a := range registry {
		if a.ID != agentID || a.Inject == nil {
			continue
		}
		argv, env := a.Inject(hooksDir, sig)
		if len(argv) == 0 {
			argv = nil
		}
		if len(env) == 0 {
			env = nil
		}
		return argv, env
	}
	return nil, nil
}
