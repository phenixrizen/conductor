package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadFileAndEnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	if err := os.WriteFile(path, []byte(`{"listen":":9000","maxSessions":5,"exitedRetention":"1m","allowedRoots":["`+dir+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUCTOR_MAX_SESSIONS", "7")
	t.Setenv("CONDUCTOR_ADMIN_TOKEN", "secret")
	t.Setenv("CONDUCTOR_HOST_TOKENS", "a, b")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9000" {
		t.Fatalf("listen: %s", cfg.Listen)
	}
	if cfg.MaxSessions != 7 {
		t.Fatalf("env override lost: %d", cfg.MaxSessions)
	}
	if time.Duration(cfg.ExitedRetention) != time.Minute {
		t.Fatalf("retention: %v", time.Duration(cfg.ExitedRetention))
	}
	if cfg.AdminToken != "secret" || len(cfg.HostTokens) != 2 || cfg.HostTokens[1] != "b" {
		t.Fatalf("tokens: %q %v", cfg.AdminToken, cfg.HostTokens)
	}
	if cfg.AllowedRoots[0] != dir {
		t.Fatalf("roots: %v", cfg.AllowedRoots)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	// Config.Path is filled by Load, not read from the file: a "path" key, in
	// any case, is as unknown as any other.
	for _, body := range []string{`{"nope":1}`, `{"path":"/etc/other.json"}`, `{"Path":"/etc/other.json"}`} {
		path := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("%s: got %v, want an unknown field error", body, err)
		}
	}
}

// Load records the config file it read as an absolute path, so the server can
// keep it away from file reads (it holds the admin token).
func TestLoadRecordsTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("conf", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("conf", "conductor.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "conf", "conductor.json")
	for _, path := range []string{"conf/conductor.json", "./conf/../conf/conductor.json", want} {
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Path != want {
			t.Errorf("Load(%q): Path = %q, want %q", path, cfg.Path, want)
		}
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != "" {
		t.Errorf("without a config file Path = %q, want it empty", cfg.Path)
	}
}

// A catalogPath from the file or the environment is made absolute, relative to
// the current directory like allowedRoots, and still loads.
func TestLoadMakesTheCatalogPathAbsolute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	agents := `{"agents":[{"id":"extra","name":"Extra","command":["/bin/cat"]}]}`
	if err := os.WriteFile("agents.json", []byte(agents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("conductor.json", []byte(`{"catalogPath":"agents.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "agents.json")
	for _, tc := range []struct{ name, env string }{
		{"from the config file", ""},
		{"from the environment", "./sub/../agents.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CONDUCTOR_CATALOG_PATH", tc.env)
			cfg, err := Load("conductor.json")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.CatalogPath != want {
				t.Fatalf("CatalogPath = %q, want %q", cfg.CatalogPath, want)
			}
			cat, err := cfg.LoadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := cat.Get("extra"); !ok {
				t.Fatal("the catalog file was not loaded")
			}
		})
	}
	t.Run("unset stays unset", func(t *testing.T) {
		t.Setenv("CONDUCTOR_CATALOG_PATH", "")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CatalogPath != "" {
			t.Fatalf("CatalogPath = %q, want it empty", cfg.CatalogPath)
		}
	})
}

func TestValidateBounds(t *testing.T) {
	cfg := Defaults()
	cfg.ScrollbackBytes = 1
	cfg.FileView = "maybe"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestResolveDataDirDefaults(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// home gives the test a home directory of its own.
	home := func(t *testing.T) string {
		t.Helper()
		h := t.TempDir()
		t.Setenv("HOME", h)
		return h
	}
	resolve := func(t *testing.T, cfg *Config, configPath string) string {
		t.Helper()
		notice, err := cfg.ResolveDataDir(configPath)
		if err != nil {
			t.Fatal(err)
		}
		// Agent processes started in other working directories are handed
		// paths under it.
		if !filepath.IsAbs(cfg.DataDir) {
			t.Fatalf("DataDir %q is not absolute", cfg.DataDir)
		}
		return notice
	}
	mkdir := func(t *testing.T, p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "conductor.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("unset, it is ~/.conductor wherever the config is", func(t *testing.T) {
		h := home(t)
		t.Chdir(t.TempDir())
		for _, configPath := range []string{"", "/etc/x/conductor.json", "conf/conductor.json"} {
			cfg := Defaults()
			if notice := resolve(t, cfg, configPath); notice != "" || cfg.DataDir != filepath.Join(h, ".conductor") {
				t.Fatalf("config %q: DataDir %q, notice %q", configPath, cfg.DataDir, notice)
			}
		}
	})

	t.Run("an old ./conductor.d is kept while ~/.conductor does not exist", func(t *testing.T) {
		h := home(t)
		dir := t.TempDir()
		t.Chdir(dir)
		old := filepath.Join(dir, "conductor.d")
		mkdir(t, old)
		cfg := Defaults()
		notice := resolve(t, cfg, "")
		if cfg.DataDir != old {
			t.Fatalf("DataDir %q, want the old %q", cfg.DataDir, old)
		}
		for _, want := range []string{old, filepath.Join(h, ".conductor"), "dataDir", "CONDUCTOR_DATA_DIR"} {
			if !strings.Contains(notice, want) {
				t.Errorf("notice %q does not mention %q", notice, want)
			}
		}
	})

	t.Run("an old conductor.d next to the config file is kept", func(t *testing.T) {
		home(t)
		t.Chdir(t.TempDir())
		dir := t.TempDir()
		old := filepath.Join(dir, "conductor.d")
		mkdir(t, old)
		cfg := Defaults()
		if notice := resolve(t, cfg, filepath.Join(dir, "conductor.json")); cfg.DataDir != old || notice == "" {
			t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
		}
	})

	// conductor host writes ~/.conductor/hooks for itself. That is no data of
	// a server's: an upgraded server keeps its old directory, and says so,
	// until the operator moves it.
	t.Run("host created ~/.conductor/hooks, legacy conductor.d present: legacy kept with the notice", func(t *testing.T) {
		h := home(t)
		dir := t.TempDir()
		t.Chdir(dir)
		old := filepath.Join(dir, "conductor.d")
		mkdir(t, old)
		mkdir(t, filepath.Join(h, ".conductor", "hooks"))
		cfg := Defaults()
		notice := resolve(t, cfg, "")
		if cfg.DataDir != old {
			t.Fatalf("DataDir %q, want the old %q", cfg.DataDir, old)
		}
		for _, want := range []string{old, filepath.Join(h, ".conductor"), "dataDir", "CONDUCTOR_DATA_DIR"} {
			if !strings.Contains(notice, want) {
				t.Errorf("notice %q does not mention %q", notice, want)
			}
		}
		// Without an old directory, that ~/.conductor is the data directory.
		if err := os.Remove(old); err != nil {
			t.Fatal(err)
		}
		cfg = Defaults()
		if notice := resolve(t, cfg, ""); cfg.DataDir != filepath.Join(h, ".conductor") || notice != "" {
			t.Fatalf("without the old directory: DataDir %q, notice %q", cfg.DataDir, notice)
		}
	})

	t.Run("~/.conductor wins once it holds server data", func(t *testing.T) {
		for _, data := range []string{"catalog.json", "crews", "crews.json"} {
			t.Run(data, func(t *testing.T) {
				h := home(t)
				dir := t.TempDir()
				t.Chdir(dir)
				mkdir(t, filepath.Join(dir, "conductor.d"))
				def := filepath.Join(h, ".conductor")
				mkdir(t, filepath.Join(def, "hooks"))
				if data == "crews" {
					mkdir(t, filepath.Join(def, data))
				} else if err := os.WriteFile(filepath.Join(def, data), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg := Defaults()
				if notice := resolve(t, cfg, ""); cfg.DataDir != def || notice != "" {
					t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
				}
			})
		}
	})

	t.Run("a file called conductor.d is no old data directory", func(t *testing.T) {
		h := home(t)
		dir := t.TempDir()
		t.Chdir(dir)
		if err := os.WriteFile(filepath.Join(dir, "conductor.d"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := Defaults()
		if notice := resolve(t, cfg, ""); cfg.DataDir != filepath.Join(h, ".conductor") || notice != "" {
			t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
		}
	})

	t.Run("without a home directory the error names the settings", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Chdir(t.TempDir()) // no old directory either
		cfg := Defaults()
		_, err := cfg.ResolveDataDir("")
		if err == nil || !strings.Contains(err.Error(), "dataDir") || !strings.Contains(err.Error(), "CONDUCTOR_DATA_DIR") || !errors.Is(err, ErrNoHome) {
			t.Fatalf("no home: %v", err)
		}
	})

	// An upgraded service started without HOME keeps working: the home is
	// needed only when there is no old directory to keep.
	t.Run("without a home directory an old conductor.d is kept with the notice", func(t *testing.T) {
		t.Setenv("HOME", "")
		dir := t.TempDir()
		t.Chdir(dir)
		old := filepath.Join(dir, "conductor.d")
		mkdir(t, old)
		for _, configPath := range []string{"", filepath.Join(dir, "conductor.json")} {
			cfg := Defaults()
			notice := resolve(t, cfg, configPath)
			if cfg.DataDir != old {
				t.Fatalf("config %q: DataDir %q, want the old %q", configPath, cfg.DataDir, old)
			}
			for _, want := range []string{old, "~/.conductor", "home directory is unknown", "dataDir", "CONDUCTOR_DATA_DIR"} {
				if !strings.Contains(notice, want) {
					t.Errorf("notice %q does not mention %q", notice, want)
				}
			}
		}
	})

	// ~/.conductor wins, and the old directory's own server data would be
	// lost from sight without a word: the notice names it.
	t.Run("an old directory that holds server data too is named when ~/.conductor wins", func(t *testing.T) {
		h := home(t)
		dir := t.TempDir()
		t.Chdir(dir)
		old := filepath.Join(dir, "conductor.d")
		mkdir(t, filepath.Join(old, "crews"))
		def := filepath.Join(h, ".conductor")
		mkdir(t, def)
		if err := os.WriteFile(filepath.Join(def, "catalog.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := Defaults()
		notice := resolve(t, cfg, "")
		if cfg.DataDir != def {
			t.Fatalf("DataDir %q, want %q", cfg.DataDir, def)
		}
		for _, want := range []string{old, def, "dataDir", "CONDUCTOR_DATA_DIR"} {
			if !strings.Contains(notice, want) {
				t.Errorf("notice %q does not mention %q", notice, want)
			}
		}
	})

	// A ~/.conductor that cannot be looked into is reported as what it is,
	// never as a directory that holds no server data yet.
	t.Run("a ~/.conductor that cannot be checked is an error", func(t *testing.T) {
		for _, tc := range []struct {
			name, want string
			make       func(t *testing.T, def string)
		}{
			{"a file in its place", "not a directory", func(t *testing.T, def string) {
				if err := os.WriteFile(def, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			}},
			{"no permission", "permission denied", func(t *testing.T, def string) {
				if os.Geteuid() == 0 {
					t.Skip("root looks into a directory of mode 0000")
				}
				mkdir(t, def)
				if err := os.Chmod(def, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(def, 0o700) })
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := home(t)
				dir := t.TempDir()
				t.Chdir(dir)
				mkdir(t, filepath.Join(dir, "conductor.d"))
				def := filepath.Join(h, ".conductor")
				tc.make(t, def)
				cfg := Defaults()
				notice, err := cfg.ResolveDataDir("")
				if err == nil {
					t.Fatalf("no error: DataDir %q, notice %q", cfg.DataDir, notice)
				}
				for _, want := range []string{def, tc.want, "dataDir", "CONDUCTOR_DATA_DIR"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not mention %q", err, want)
					}
				}
				if strings.Contains(err.Error(), "no server data") {
					t.Errorf("error %q says there is no server data", err)
				}
			})
		}
	})

	t.Run("an absolute value from the config file is kept", func(t *testing.T) {
		home(t)
		t.Setenv("CONDUCTOR_DATA_DIR", "")
		path := writeConfig(t, `{"dataDir":"/srv/from-file"}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if notice := resolve(t, cfg, path); cfg.DataDir != "/srv/from-file" || notice != "" {
			t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
		}
	})

	t.Run("a relative value from the config file is made absolute", func(t *testing.T) {
		home(t)
		t.Setenv("CONDUCTOR_DATA_DIR", "")
		path := writeConfig(t, `{"dataDir":"state/data"}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		resolve(t, cfg, path)
		// Relative to the current directory like allowedRoots and defaultCwd,
		// not to the config file.
		if cfg.DataDir != filepath.Join(cwd, "state", "data") {
			t.Fatalf("DataDir %q", cfg.DataDir)
		}
	})

	t.Run("CONDUCTOR_DATA_DIR wins and is kept", func(t *testing.T) {
		home(t)
		t.Setenv("CONDUCTOR_DATA_DIR", "/srv/from-env")
		path := writeConfig(t, `{"dataDir":"/srv/from-file"}`)
		for _, configPath := range []string{path, ""} {
			cfg, err := Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if notice := resolve(t, cfg, configPath); cfg.DataDir != "/srv/from-env" || notice != "" {
				t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
			}
		}
	})

	t.Run("a relative CONDUCTOR_DATA_DIR is made absolute", func(t *testing.T) {
		home(t)
		t.Setenv("CONDUCTOR_DATA_DIR", "rel/dir")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if resolve(t, cfg, ""); cfg.DataDir != filepath.Join(cwd, "rel", "dir") {
			t.Fatalf("DataDir %q", cfg.DataDir)
		}
	})

	// A value that was set stays relative when the working directory is gone:
	// filepath.Abs needs it, and the chosen value beats an empty one. The
	// default comes from the home directory and is absolute regardless.
	t.Run("the working directory is gone", func(t *testing.T) {
		h := home(t)
		gone := t.TempDir()
		t.Chdir(gone)
		if err := os.Remove(gone); err != nil {
			t.Skipf("cannot remove the working directory: %v", err)
		}
		if _, err := filepath.Abs("x"); err == nil {
			t.Skip("the working directory is still resolvable on this platform")
		}
		cfg := Defaults()
		cfg.DataDir = "rel/dir"
		if _, err := cfg.ResolveDataDir(""); err != nil || cfg.DataDir != "rel/dir" {
			t.Fatalf("explicit value: DataDir %q %v", cfg.DataDir, err)
		}
		cfg = Defaults()
		if _, err := cfg.ResolveDataDir(""); err != nil || cfg.DataDir != filepath.Join(h, ".conductor") {
			t.Fatalf("default: DataDir %q %v", cfg.DataDir, err)
		}
	})
}

func TestDataDirOverlap(t *testing.T) {
	base := t.TempDir()
	mkdir := func(parts ...string) string {
		t.Helper()
		p := filepath.Join(append([]string{base}, parts...)...)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	proj := mkdir("proj")
	projData := mkdir("proj", "conductor.d")
	sibling := mkdir("proj-data") // shares a name prefix with proj, nothing else
	elsewhere := mkdir("var", "lib", "conductor")
	nested := mkdir("var", "lib", "conductor", "workspaces")
	link := filepath.Join(base, "link-to-proj")
	if err := os.Symlink(proj, link); err != nil {
		t.Fatal(err)
	}
	// A root reached through a symlinked parent, as every temp directory is on
	// macOS (/var is a symlink to /private/var).
	mkdir("real", "root")
	viaLink := filepath.Join(base, "link", "root")
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		roots   []string
		dataDir string
		want    string
	}{
		{"data directory inside a root", []string{proj}, projData, proj},
		{"data directory not created yet inside a root", []string{proj}, filepath.Join(proj, "later"), proj},
		{"data directory is a root", []string{proj}, proj, proj},
		{"root inside the data directory", []string{nested}, elsewhere, nested},
		{"the second root overlaps", []string{elsewhere + "-x", proj}, projData, proj},
		{"root given through a symlink", []string{link}, projData, link},
		{"data directory given through a symlink", []string{proj}, filepath.Join(link, "conductor.d"), proj},
		// The data directory does not exist yet, so only its existing ancestors
		// resolve; the missing part is joined back on to compare.
		{"data directory not created yet under a symlinked root", []string{viaLink}, filepath.Join(viaLink, "conductor.d"), viaLink},
		{"data directory two levels below a symlinked root, not created yet", []string{viaLink}, filepath.Join(viaLink, "state", "conductor.d"), viaLink},
		{"data directory not created yet beside a symlinked root", []string{viaLink}, filepath.Join(base, "link", "root-data", "conductor.d"), ""},
		{"sibling with a common name prefix", []string{proj}, sibling, ""},
		{"separate directories", []string{proj}, elsewhere, ""},
		{"the file system root holds everything", []string{"/"}, elsewhere, "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.AllowedRoots = tc.roots
			cfg.DataDir = tc.dataDir
			if got := cfg.DataDirOverlap(); got != tc.want {
				t.Fatalf("DataDirOverlap() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The config file is held to what the store holds its files to: one JSON
// value and nothing after it.
func TestLoadRejectsTrailingDataAndEmptyFiles(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"listen":":9000"} oops`, "after the JSON value"},
		{`{"listen":":9000"}{"listen":":9001"}`, "more than one JSON value"},
		{"", "empty document"},
	} {
		path := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
			t.Errorf("%q: %v, want an error naming %s and saying %q", tc.body, err, path, tc.want)
		}
	}
}
