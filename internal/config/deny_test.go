package config

import (
	"path/filepath"
	"slices"
	"testing"
)

// FileDeny names the data directory and the directories given, then the
// config file and the catalog file, each with the name entry beside it;
// what is not set is left out, and so is a directory named twice.
func TestFileDeny(t *testing.T) {
	dir := t.TempDir()
	data, other := filepath.Join(dir, "data"), filepath.Join(dir, "store")
	cfg, cat := filepath.Join(dir, "conductor.json"), filepath.Join(dir, "agents.json")
	for _, c := range []struct {
		name string
		c    Config
		dirs []string
		want []string
	}{
		{"nothing set", Config{}, nil, nil},
		{"the data directory", Config{DataDir: data}, nil, []string{data}},
		{"the store's directory, the same", Config{DataDir: data}, []string{data}, []string{data}},
		{"the store's directory, another", Config{DataDir: data}, []string{other}, []string{data, other}},
		{"every file", Config{DataDir: data, Path: cfg, CatalogPath: cat}, []string{data}, []string{data, cfg, filepath.Join(dir, "*conductor.json*"), cat, filepath.Join(dir, "*agents.json*")}},
		{"no data directory", Config{Path: cfg}, []string{other}, []string{other, cfg, filepath.Join(dir, "*conductor.json*")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.c.FileDeny(c.dirs...); !slices.Equal(got, c.want) {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
		})
	}
}
