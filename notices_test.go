package conductor

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The embedded notices name every module go.mod requires directly, at the
// version it requires: a bump without `make notices` fails here as well as
// in CI's check.
func TestNoticesNameTheDirectModulesAtTheirVersions(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)require \((.*?)\)`).FindSubmatch(mod)
	if block == nil {
		t.Fatal("no require block in go.mod")
	}
	n := 0
	for _, line := range strings.Split(string(block[1]), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		n++
		if !strings.Contains(ThirdPartyNotices, "\n  "+f[0]+" "+f[1]+" ") {
			t.Errorf("THIRD_PARTY_NOTICES does not list %s %s: run make notices", f[0], f[1])
		}
	}
	if n < 5 {
		t.Fatalf("read %d direct modules from go.mod", n)
	}
	for _, want := range []string{"THIRD-PARTY NOTICES", "Go (the standard library and runtime)", "vue ", "@xterm/xterm ", "electron ", "Inter (font)", "SIL OPEN FONT LICENSE"} {
		if !strings.Contains(ThirdPartyNotices, want) {
			t.Errorf("THIRD_PARTY_NOTICES lacks %q", want)
		}
	}
}

// Notices no package-level licence carries: a file's own (the runtime's memmove from Inferno, keywrap's author, one after the
// package clause, one in an unstarred block) and what a package carries from other projects (Shiki's Oniguruma, compiled into
// the wasm the web bundle inlines; its themes and grammars).
func TestNoticesCarryWhatThePackageLicencesDoNot(t *testing.T) {
	for _, want := range []string{
		"runtime/memmove_amd64.s (the file's own notice)", "Vita Nuova Holdings Limited",
		"openpgp/aes/keywrap/keywrap.go (the file's own notice)", "Matthew Endsley",
		"Oniguruma's COPYING (compiled into its onig.wasm)", "K.Kosako",
		"bitcurves/bitcurve.go (the file's own notice)", "ThePiachu",
		"osfs/os_bound.go (the file's own notice)", "The Flux authors",
		"math/log.go (the file's own notice)", "Sun Microsystems",
		"lib/xterm.mjs (the file's own notice)", "Fabrice Bellard",
		"utils/glob.js (the file's own notice)", "Nick Fitzgerald",
		"dompurify/dompurify.js (the file's own notice)", "Cure53", "DOMPurify 3.4.15's LICENSE (vendored as esm/vs/base/browser/dompurify)",
		"tm-themes 1.12.3's NOTICE (the themes it carries)", "tm-grammars 1.32.3's NOTICE (the grammars it carries)",
		"in the app's resources as LICENSES.chromium.html",
	} {
		if !strings.Contains(ThirdPartyNotices, want) {
			t.Errorf("THIRD_PARTY_NOTICES lacks %q: run make notices", want)
		}
	}
}
