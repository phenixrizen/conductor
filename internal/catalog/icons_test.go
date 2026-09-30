package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// The workbench fetches no icon at runtime: it bundles the ones it shows, and
// the built-in agents' icons, which reach it from the server, are listed in
// web/nuxt.config.ts (icon.clientBundle.icons) for that. The list and the
// built-ins name the same icons: one missing from the list shows nothing, and
// one the built-ins do not use only weighs on the bundle.
func TestBundledIconsAreTheBuiltInIcons(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "nuxt.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	list := regexp.MustCompile(`(?s)\bicons:\s*\[(.*?)\]`).FindSubmatch(b)
	if list == nil {
		t.Fatal("web/nuxt.config.ts has no icons: [...] list")
	}
	lucide := regexp.MustCompile(`^lucide:([a-z0-9-]+)$`)
	var bundled []string
	for _, quoted := range regexp.MustCompile(`'([^']*)'`).FindAllSubmatch(list[1], -1) {
		name := lucide.FindSubmatch(quoted[1])
		if name == nil {
			t.Errorf("the icons list holds %q, not a lucide:<name> icon", quoted[1])
			continue
		}
		bundled = append(bundled, "i-lucide-"+string(name[1]))
	}
	var builtIn []string
	for _, a := range defaults() {
		if a.Icon != "" {
			builtIn = append(builtIn, a.Icon)
		}
	}
	slices.Sort(bundled)
	slices.Sort(builtIn)
	if !slices.Equal(slices.Compact(bundled), slices.Compact(builtIn)) {
		t.Fatalf("web/nuxt.config.ts bundles %q\nthe built-in agents use %q", bundled, builtIn)
	}
}
