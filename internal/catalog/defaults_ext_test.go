package catalog_test

import (
	"slices"
	"testing"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/catalog"
)

// The built-ins are the thirteen supported agents, in the order the launch
// dialog lists them: every one with an adapter names an adapter that exists
// and reports the way the adapter matrix says, every signal is one the
// catalog accepts, and the shell, last, has no adapter and takes no extra
// arguments. (An external test: internal/agents imports the catalog.)
func TestDefaultsHaveAdaptersAndValidSignals(t *testing.T) {
	want := []struct {
		id      string
		command []string
		adapter string
		signal  catalog.Signal
		icon    string
	}{
		{"claude", []string{"claude"}, "claude", catalog.Signal{Kind: "hook"}, "i-lucide-sparkles"},
		{"codex", []string{"codex"}, "codex", catalog.Signal{Kind: "hook"}, "i-lucide-code-xml"},
		{"agy", []string{"agy"}, "agy", catalog.Signal{Kind: "bell"}, "i-lucide-rocket"},
		{"copilot", []string{"copilot"}, "copilot", catalog.Signal{Kind: "hook"}, "i-lucide-github"},
		{"cursor", []string{"cursor-agent"}, "cursor", catalog.Signal{Kind: "pattern", Pattern: `^› $`}, "i-lucide-mouse-pointer-2"},
		{"opencode", []string{"opencode"}, "opencode", catalog.Signal{Kind: "hook"}, "i-lucide-braces"},
		{"pi", []string{"pi"}, "pi", catalog.Signal{Kind: "hook"}, "i-lucide-pi"},
		{"omp", []string{"omp"}, "omp", catalog.Signal{Kind: "hook"}, "i-lucide-pi-square"},
		{"aider", []string{"aider"}, "aider", catalog.Signal{Kind: "hook"}, "i-lucide-git-commit"},
		{"goose", []string{"goose"}, "goose", catalog.Signal{Kind: "bell"}, "i-lucide-feather"},
		{"amp", []string{"amp"}, "amp", catalog.Signal{Kind: "hook"}, "i-lucide-zap"},
		{"dsh", []string{"dsh"}, "dsh", catalog.Signal{Kind: "none"}, "i-lucide-cpu"},
		{"shell", []string{"/bin/bash", "-l"}, "", catalog.Signal{Kind: "bell"}, "i-lucide-terminal"},
	}
	list := catalog.Default().List()
	if len(list) != len(want) {
		t.Fatalf("%d built-ins, want %d", len(list), len(want))
	}
	adapters := map[string]bool{}
	for i, a := range list {
		w := want[i]
		if a.ID != w.id {
			t.Fatalf("built-in %d is %q, want %q", i, a.ID, w.id)
		}
		if !slices.Equal(a.Command, w.command) || a.Adapter != w.adapter || a.Icon != w.icon || a.Name == "" || a.Description == "" {
			t.Errorf("%s: command %q adapter %q icon %q name %q description %q", a.ID, a.Command, a.Adapter, a.Icon, a.Name, a.Description)
		}
		if a.AllowArgs != (a.ID != "shell") {
			t.Errorf("%s: allowArgs %v", a.ID, a.AllowArgs)
		}
		if a.Signal == nil || *a.Signal != w.signal {
			t.Errorf("%s: signal %+v, want %+v", a.ID, a.Signal, w.signal)
		}
		if a.Adapter != "" {
			if ad, ok := agents.Get(a.Adapter); !ok {
				t.Errorf("%s: adapter %q is not registered", a.ID, a.Adapter)
			} else if ad.Name != a.Name {
				t.Errorf("%s: name %q, the adapter's %q", a.ID, a.Name, ad.Name)
			}
			adapters[a.Adapter] = true
		}
		// Upsert holds an agent to the rules a saved or configured one meets.
		var c catalog.Catalog
		if err := c.Upsert(a); err != nil {
			t.Errorf("%s: built-in fails validation: %v", a.ID, err)
		}
	}
	// Every adapter has its built-in.
	for _, ad := range agents.All() {
		if !adapters[ad.ID] {
			t.Errorf("no built-in uses the %s adapter", ad.ID)
		}
	}
}
