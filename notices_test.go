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
