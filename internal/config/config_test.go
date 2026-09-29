package config

import (
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
	writeConfig := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "conductor.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// wantDataDir insists on an absolute result: the path is handed to agent
	// processes that start in other working directories.
	wantDataDir := func(t *testing.T, got, want string) {
		t.Helper()
		if !filepath.IsAbs(got) {
			t.Fatalf("DataDir %q is not absolute", got)
		}
		if got != want {
			t.Fatalf("DataDir = %q, want %q", got, want)
		}
	}

	for _, tc := range []struct{ name, configPath, want string }{
		{"no config file", "", filepath.Join(cwd, "conductor.d")},
		{"absolute config path", "/etc/x/conductor.json", filepath.Join("/etc/x", "conductor.d")},
		{"relative config path", "conf/conductor.json", filepath.Join(cwd, "conf", "conductor.d")},
		{"config file in the current directory", "conductor.json", filepath.Join(cwd, "conductor.d")},
	} {
		t.Run("derived from "+tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.ResolveDataDir(tc.configPath)
			wantDataDir(t, cfg.DataDir, tc.want)
		})
	}

	t.Run("an absolute value from the config file is kept", func(t *testing.T) {
		t.Setenv("CONDUCTOR_DATA_DIR", "")
		path := writeConfig(t, `{"dataDir":"/srv/from-file"}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.ResolveDataDir(path)
		wantDataDir(t, cfg.DataDir, "/srv/from-file")
	})

	t.Run("a relative value from the config file is made absolute", func(t *testing.T) {
		t.Setenv("CONDUCTOR_DATA_DIR", "")
		path := writeConfig(t, `{"dataDir":"state/data"}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.ResolveDataDir(path)
		// Relative to the current directory like allowedRoots and defaultCwd,
		// not to the config file.
		wantDataDir(t, cfg.DataDir, filepath.Join(cwd, "state", "data"))
	})

	t.Run("the environment override wins and is kept", func(t *testing.T) {
		t.Setenv("CONDUCTOR_DATA_DIR", "/srv/from-env")
		path := writeConfig(t, `{"dataDir":"/srv/from-file"}`)
		for _, configPath := range []string{path, ""} {
			cfg, err := Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DataDir != "/srv/from-env" {
				t.Fatalf("after Load DataDir = %q, want the env value", cfg.DataDir)
			}
			cfg.ResolveDataDir(configPath)
			wantDataDir(t, cfg.DataDir, "/srv/from-env")
		}
	})

	t.Run("a relative environment value is made absolute", func(t *testing.T) {
		t.Setenv("CONDUCTOR_DATA_DIR", "rel/dir")
		path := writeConfig(t, `{"dataDir":"/srv/from-file"}`)
		for _, configPath := range []string{path, ""} {
			cfg, err := Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			cfg.ResolveDataDir(configPath)
			wantDataDir(t, cfg.DataDir, filepath.Join(cwd, "rel", "dir"))
		}
	})

	// The one case that stays relative on purpose: filepath.Abs needs the working
	// directory, and without it the chosen value is better than an empty one.
	t.Run("the chosen value is kept when the working directory is gone", func(t *testing.T) {
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
		cfg.ResolveDataDir("")
		if cfg.DataDir != "rel/dir" {
			t.Fatalf("explicit value: DataDir = %q, want it unchanged", cfg.DataDir)
		}
		cfg = Defaults()
		cfg.ResolveDataDir("")
		if cfg.DataDir != "conductor.d" {
			t.Fatalf("derived value: DataDir = %q, want conductor.d", cfg.DataDir)
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
