package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"

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
	return c.fileDeny(dirs, nil)
}

// fileDeny is FileDeny with more files, each denied as the two are.
func (c *Config) fileDeny(dirs, files []string) []string {
	return session.DenyList(append([]string{c.DataDir}, dirs...), append([]string{c.Path, c.CatalogPath}, files...))
}

// LocalFileDeny is the deny list of a session that runs on this machine but
// not in conductor serve, as conductor host's do: what a session of the
// server that would start here refuses (FileDeny). That server reads the
// config file at path ("" for none: conductor serve reads one only when
// told), with CONDUCTOR_DATA_DIR and CONDUCTOR_CATALOG_PATH over it, and
// finds its data directory as ResolveDataDir does. The default data
// directory, ~/.conductor, is refused whichever directory that is, as are
// the copies beside the config file and the catalog file, and the
// directories and settings of the desktop app's server (desktopFiles). A config file that
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
	desktopDirs, desktopSettings := desktopFiles()
	return c.fileDeny(append(dirs, desktopDirs...), desktopSettings), nil
}

// LocalFileDenyFunc is LocalFileDeny(path) for a session that runs a long
// time (session.Options.FileDenyFunc): the function it returns computes the
// list again at each call, so that what the config file or the desktop
// app's settings come to name while the session runs is refused from then
// on, and keeps every entry it has named before, so that what was refused
// once stays refused until the session ends. The first computation's error
// is returned; a later one leaves the list as it was. It is safe to call from
// many goroutines, and the slices it returns are never changed.
func LocalFileDenyFunc(path string) (func() []string, error) {
	first, err := LocalFileDeny(path)
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	list := first
	return func() []string {
		next, err := LocalFileDeny(path)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			return list
		}
		var added []string
		for _, e := range next {
			if !slices.Contains(list, e) {
				added = append(added, e)
			}
		}
		if len(added) > 0 {
			list = slices.Concat(list, added)
		}
		return list
	}, nil
}

// desktopAppNames name the desktop app's own directory, Electron's userData:
// the user's config directory (os.UserConfigDir, as Electron's appData) and
// the app's name. Electron takes that name from desktop/package.json, which
// gives only its name, conductor-desktop; Conductor, the name its packages
// carry (productName in desktop/electron-builder.yml), is the one it would
// take should package.json ever give a productName. Both are refused.
var desktopAppNames = []string{"conductor-desktop", "Conductor"}

// maxDesktopSettings bounds what is read of the desktop app's settings.
const maxDesktopSettings = 1 << 20

// desktopFiles are the directories and files of the server the desktop app
// runs on this machine, which it starts with CONDUCTOR_DATA_DIR from its own
// settings rather than with a config file (desktop/src/env.ts). The
// directories: the app's own (desktopAppNames), which holds its settings
// (desktop/src/main.ts) and, unless they say otherwise, the data directory
// (conductor in it); the data directory its settings name, when that is an
// absolute path; and, inside WSL, where the app on Windows runs the server,
// the default it gives a server there, ~/.local/share/conductor/data
// (desktop/src/settings.ts, wslDefaults). The files: each settings.json, so
// that one which is a symbolic link is refused at its target too, with the
// copies beside it, as a config file is. The settings of the app on Windows
// are not on this side, so a data directory they choose for the server
// inside WSL is not known here. Only dataDir is read of the settings, which
// hold many other keys; settings that cannot be read or parsed name nothing,
// as the app then takes its defaults. Each path is named whether it exists or
// not: one that does not denies nothing until it does (session.ResolvePath).
func desktopFiles() (dirs, files []string) {
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "conductor", "data"))
	}
	base, err := os.UserConfigDir()
	if err != nil || !filepath.IsAbs(base) {
		return dirs, nil
	}
	for _, name := range desktopAppNames {
		app := filepath.Join(base, name)
		settings := filepath.Join(app, "settings.json")
		dirs = append(dirs, app)
		files = append(files, settings)
		if d := desktopDataDir(settings); d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs, files
}

// desktopDataDir is the data directory the desktop app's settings file
// names, or "" when it names none that is an absolute path or cannot be read.
func desktopDataDir(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	var settings struct {
		DataDir string `json:"dataDir"`
	}
	b, err := io.ReadAll(io.LimitReader(f, maxDesktopSettings))
	if err != nil || json.Unmarshal(b, &settings) != nil || !filepath.IsAbs(settings.DataDir) {
		return ""
	}
	return filepath.Clean(settings.DataDir)
}
