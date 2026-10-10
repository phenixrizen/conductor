package session

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// DenyList keeps each directory, and each file with the name entry beside
// it and, for a symbolic link, the one beside its target; it leaves out
// empty paths and repeats, and keeps the order given.
func TestDenyList(t *testing.T) {
	// Its own path without links, so that a file's target is the file.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	etc := filepath.Join(root, "etc")
	elsewhere := filepath.Join(root, "real")
	for _, d := range []string{data, etc, elsewhere} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(etc, "conductor.json")
	os.WriteFile(cfg, []byte("{}"), 0o600)
	target := filepath.Join(elsewhere, "prod.json")
	os.WriteFile(target, []byte("{}"), 0o600)
	link := filepath.Join(etc, "agents.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	// A file that is no link is its own target: one name entry.
	same := filepath.Join(etc, "same.json")
	os.WriteFile(same, []byte("{}"), 0o600)
	for _, c := range []struct {
		name        string
		dirs, files []string
		want        []string
	}{
		{"nothing", nil, nil, nil},
		{"empty paths", []string{"", ""}, []string{""}, nil},
		{"a directory", []string{data}, nil, []string{data}},
		{"a directory twice", []string{data, data, ""}, nil, []string{data}},
		{"two directories", []string{data, etc}, nil, []string{data, etc}},
		{"a file", nil, []string{cfg}, []string{cfg, filepath.Join(etc, "*conductor.json*")}},
		{"a file that is not there yet", nil, []string{filepath.Join(etc, "missing.json")}, []string{filepath.Join(etc, "missing.json"), filepath.Join(etc, "*missing.json*")}},
		{"a link", nil, []string{link}, []string{link, filepath.Join(etc, "*agents.json*"), filepath.Join(elsewhere, "*prod.json*")}},
		{"a file that is no link", nil, []string{same}, []string{same, filepath.Join(etc, "*same.json*")}},
		{"the same file twice", nil, []string{cfg, cfg}, []string{cfg, filepath.Join(etc, "*conductor.json*")}},
		{"all of it", []string{data, ""}, []string{cfg, "", link}, []string{data, cfg, filepath.Join(etc, "*conductor.json*"), link, filepath.Join(etc, "*agents.json*"), filepath.Join(elsewhere, "*prod.json*")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := DenyList(c.dirs, c.files); !slices.Equal(got, c.want) {
				t.Errorf("DenyList(%q, %q)\n got %q\nwant %q", c.dirs, c.files, got, c.want)
			}
		})
	}
	if got, want := NameEntry("/etc/conductor/conductor.json"), filepath.FromSlash("/etc/conductor/*conductor.json*"); got != want {
		t.Errorf("NameEntry: %q, want %q", got, want)
	}
	// What the list names is refused, and what it does not name is served.
	deny := DenyList([]string{data}, []string{cfg, link})
	os.WriteFile(filepath.Join(data, "catalog.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(etc, "conductor.json.bak"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(elsewhere, ".prod.json.swp"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(etc, "plain.json"), []byte("{}"), 0o600)
	for _, p := range []string{"data", "data/catalog.json", "etc/conductor.json", "etc/conductor.json.bak", "etc/agents.json", "real/prod.json", "real/.prod.json.swp"} {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%s: resolved to %s, want it refused", p, r)
		}
	}
	for _, p := range []string{".", "etc", "etc/plain.json", "etc/same.json", "real"} {
		if _, err := ResolvePath(root, p, deny); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}
