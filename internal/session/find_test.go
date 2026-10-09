package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The find walks the working directory for names holding the words, in any
// case, skipping .git, node_modules and what the deny list refuses, and
// stops at its bounds saying so.
func TestFindPathFindsNamesBelowAndSkipsWhatItShould(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"internal/api/users.go", "internal/api/Users_test.go", "docs/users.md", "README.md", ".git/users.lock", "node_modules/users/index.js", "secret/users.key"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte("x"), 0o644)
	}
	h := FindPath(root, "USERS", []string{filepath.Join(root, "secret")})
	if h.Kind != "find" || h.Truncated {
		t.Fatalf("find %+v", h)
	}
	got := append([]string(nil), h.Matches...)
	sort.Strings(got)
	want := []string{"docs/users.md", "internal/api/Users_test.go", "internal/api/users.go"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("matches %v, want %v", got, want)
	}
	if h := FindPath(root, "  ", nil); h.Kind != "error" || h.Error.Code != "bad_request" {
		t.Fatalf("no words: %+v", h)
	}
	if h := FindPath(root, "users", []string{root}); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("a denied root: %+v", h)
	}
	many := t.TempDir()
	for i := 0; i < MaxFindMatches+20; i++ {
		os.WriteFile(filepath.Join(many, fmt.Sprintf("match-%03d.txt", i)), []byte("x"), 0o644)
	}
	if h := FindPath(many, "match", nil); len(h.Matches) != MaxFindMatches || !h.Truncated {
		t.Fatalf("bounded: %d matches, truncated %v", len(h.Matches), h.Truncated)
	}
}
