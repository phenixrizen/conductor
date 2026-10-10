package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/store"
)

// FileDeny returns what no file read or write of a session may reach, even
// inside its working directory (session.Options.FileDeny): the data
// directory, whose catalog.json holds the agents' env secrets, and dirs, such
// as the directory the store writes to should it ever differ; the config file
// (Path), which holds the workbench token and host tokens; and the catalog
// file (CatalogPath), which can hold env secrets. Each of the two files comes
// with its name entries (session.DenyList), so an editor's or a backup's
// copy beside it is refused too. A directory is denied with everything in
// it, a file on its own.
func (c *Config) FileDeny(dirs ...string) []string {
	return session.DenyList(append([]string{c.DataDir}, dirs...), []string{c.Path, c.CatalogPath})
}

// LocalFileDeny is the deny list of a session that runs on this machine but
// not in conductor serve, as conductor host's do: what a session of the
// server that would start here refuses (FileDeny). That server reads the
// config file at path ("" for none: conductor serve reads one only when
// told), with CONDUCTOR_DATA_DIR and CONDUCTOR_CATALOG_PATH over it, and
// finds its data directory as ResolveDataDir does. The default data
// directory, ~/.conductor, is refused whichever directory that is, as are
// the copies beside the config file and the catalog file. A config file that
// cannot be read or parsed is an error, and so is a data directory or a
// catalog file given as a relative path: conductor serve resolves it against
// the directory it runs in, which a session elsewhere cannot know, and what
// it names would go unrefused. Nothing is created or written.
func LocalFileDeny(path string) ([]string, error) {
	c := &Config{}
	dataFrom, catalogFrom := "", ""
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := store.DecodeStrict(b, c); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
		c.Path = path
		if abs, err := filepath.Abs(path); err == nil {
			c.Path = abs
		}
		dataFrom, catalogFrom = "dataDir in "+c.Path, "catalogPath in "+c.Path
	}
	if v := os.Getenv("CONDUCTOR_DATA_DIR"); v != "" {
		c.DataDir, dataFrom = v, "CONDUCTOR_DATA_DIR"
	}
	if v := os.Getenv("CONDUCTOR_CATALOG_PATH"); v != "" {
		c.CatalogPath, catalogFrom = v, "CONDUCTOR_CATALOG_PATH"
	}
	for _, f := range []struct{ from, p string }{{dataFrom, c.DataDir}, {catalogFrom, c.CatalogPath}} {
		if f.p != "" && !filepath.IsAbs(f.p) {
			return nil, fmt.Errorf("%s is %q, a path relative to the directory conductor serve runs in, which is not known here: give it as an absolute path", f.from, f.p)
		}
	}
	if c.CatalogPath != "" {
		c.CatalogPath = filepath.Clean(c.CatalogPath)
	}
	// The directory chosen, or none where ResolveDataDir finds none (no home
	// directory, an older directory it refuses): ~/.conductor stands below.
	// An older conductor.d is looked for beside the config file, as serve
	// looks for it, or without one in this process's working directory.
	if _, err := c.ResolveDataDir(c.Path); err != nil {
		c.DataDir = ""
	}
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		dirs = append(dirs, filepath.Join(home, ".conductor"))
	}
	return c.FileDeny(dirs...), nil
}
