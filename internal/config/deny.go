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
// cannot be read or parsed is an error: what it names would go unrefused.
// Nothing is created or written.
func LocalFileDeny(path string) ([]string, error) {
	c := &Config{}
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
	}
	if v := os.Getenv("CONDUCTOR_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("CONDUCTOR_CATALOG_PATH"); v != "" {
		c.CatalogPath = v
	}
	if c.CatalogPath != "" {
		if abs, err := filepath.Abs(c.CatalogPath); err == nil {
			c.CatalogPath = abs
		}
	}
	// The directory chosen, or none where ResolveDataDir finds none (no home
	// directory, an older directory it refuses): ~/.conductor stands below.
	if _, err := c.ResolveDataDir(path); err != nil {
		c.DataDir = ""
	}
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		dirs = append(dirs, filepath.Join(home, ".conductor"))
	}
	return c.FileDeny(dirs...), nil
}
