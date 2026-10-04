package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/catalog"
)

func TestLoadFileAndEnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	if err := os.WriteFile(path, []byte(`{"listen":":9000","maxSessions":5,"exitedRetention":"1m","allowedRoots":["`+dir+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUCTOR_MAX_SESSIONS", "7")
	t.Setenv("CONDUCTOR_WORKBENCH_TOKEN", "secret")
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
	if cfg.WorkbenchToken != "secret" || len(cfg.HostTokens) != 2 || cfg.HostTokens[1] != "b" {
		t.Fatalf("tokens: %q %v", cfg.WorkbenchToken, cfg.HostTokens)
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

	// In a shared working directory anyone may make ./conductor.d (or the
	// config's directory may be shared), and the catalog.json in it would
	// choose the commands the server launches. An old directory is kept only
	// when it is a real directory, not a link, of the user running conductor
	// serve; anything else under that name stops the server with an error
	// naming it, its owner and the settings that choose a data directory,
	// rather than leaving the old data behind without a word. The places it
	// is looked for, and a home that is unknown, change nothing.
	refusedCases := []struct {
		name           string
		noHome, config bool
	}{
		{"in the working directory", false, false},
		{"next to the config file", false, true},
		{"without a home directory", true, false},
	}
	// refused resolves the data directory with the old directory old, in
	// dir, made by the caller, and checks that it is refused, naming want.
	refused := func(t *testing.T, dir, old string, noHome, config bool, want ...string) {
		t.Helper()
		if noHome {
			t.Setenv("HOME", "")
		}
		t.Chdir(dir)
		configPath := ""
		if config {
			t.Chdir(t.TempDir())
			configPath = filepath.Join(dir, "conductor.json")
		}
		cfg := Defaults()
		notice, err := cfg.ResolveDataDir(configPath)
		if err == nil {
			t.Fatalf("no error: DataDir %q, notice %q", cfg.DataDir, notice)
		}
		for _, w := range append([]string{old, "dataDir", "CONDUCTOR_DATA_DIR"}, want...) {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("error %q does not mention %q", err, w)
			}
		}
		if errors.Is(err, ErrNoHome) || cfg.DataDir != "" {
			t.Errorf("DataDir %q, error %v", cfg.DataDir, err)
		}
	}
	useEuid := func(t *testing.T, uid int) {
		t.Helper()
		old := geteuid
		geteuid = func() int { return uid }
		t.Cleanup(func() { geteuid = old })
	}

	t.Run("a conductor.d that is a link is refused", func(t *testing.T) {
		for _, tc := range refusedCases {
			t.Run(tc.name, func(t *testing.T) {
				home(t)
				dir := t.TempDir()
				old := filepath.Join(dir, "conductor.d")
				// A directory of the user's own, through a link.
				if err := os.Symlink(t.TempDir(), old); err != nil {
					t.Fatal(err)
				}
				want := []string{"symbolic link"}
				if fi, err := os.Lstat(old); err != nil {
					t.Fatal(err)
				} else if owner, ok := agents.FileOwner(fi); ok {
					want = append(want, fmt.Sprintf("uid %d", owner))
				}
				refused(t, dir, old, tc.noHome, tc.config, want...)
			})
		}
	})

	// Another user's directory is refused whoever runs the server, root too:
	// root could read it, but its owner could change what root launches.
	t.Run("a conductor.d of another user is refused", func(t *testing.T) {
		for _, tc := range refusedCases {
			t.Run(tc.name, func(t *testing.T) {
				home(t)
				dir := t.TempDir()
				old := filepath.Join(dir, "conductor.d")
				mkdir(t, old)
				fi, err := os.Lstat(old)
				if err != nil {
					t.Fatal(err)
				}
				owner, ok := agents.FileOwner(fi)
				if !ok {
					t.Skip("this platform does not report who owns a file")
				}
				if owner == 0 {
					// The tests run as root: make it another user's.
					if err := os.Chown(old, 65534, 65534); err != nil {
						t.Skipf("cannot give the directory to another user: %v", err)
					}
					owner = 65534
				}
				for _, euid := range []int{owner + 1, 0} {
					t.Run(fmt.Sprintf("euid %d", euid), func(t *testing.T) {
						useEuid(t, euid)
						refused(t, dir, old, tc.noHome, tc.config, fmt.Sprintf("uid %d", owner), fmt.Sprintf("uid %d", euid))
					})
				}
				// Its owner keeps it, as before.
				useEuid(t, owner)
				if tc.noHome {
					t.Setenv("HOME", "")
				}
				t.Chdir(dir)
				cfg := Defaults()
				if notice := resolve(t, cfg, ""); cfg.DataDir != old || notice == "" {
					t.Fatalf("its owner: DataDir %q, notice %q", cfg.DataDir, notice)
				}
			})
		}
	})

	// Once ~/.conductor holds server data, an old directory is not used,
	// and one that would be refused is no reason to stop.
	t.Run("a conductor.d that would be refused does not matter once ~/.conductor holds server data", func(t *testing.T) {
		h := home(t)
		dir := t.TempDir()
		t.Chdir(dir)
		old := filepath.Join(dir, "conductor.d")
		target := t.TempDir()
		mkdir(t, filepath.Join(target, "crews"))
		if err := os.Symlink(target, old); err != nil {
			t.Fatal(err)
		}
		def := filepath.Join(h, ".conductor")
		mkdir(t, def)
		if err := os.WriteFile(filepath.Join(def, "catalog.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := Defaults()
		if notice := resolve(t, cfg, ""); cfg.DataDir != def || notice != "" {
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

// An agent of the config that names an adapter Conductor does not have stops
// startup, as the Agents page refuses it.
func TestLoadCatalogChecksTheAdapter(t *testing.T) {
	cfg := Defaults()
	cfg.Catalog = catalog.File{Agents: []catalog.Agent{{ID: "g", Name: "g", Command: []string{"g"}, Adapter: "gemini"}}}
	if _, err := cfg.LoadCatalog(); err == nil || !strings.Contains(err.Error(), `config catalog: agent g: unknown adapter "gemini"`) {
		t.Fatalf("inline: %v", err)
	}
	cfg.Catalog.Agents[0].Adapter = "claude"
	if _, err := cfg.LoadCatalog(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agents.json")
	if err := os.WriteFile(path, []byte(`{"agents":[{"id":"h","name":"h","command":["h"],"adapter":"nope"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = Defaults()
	cfg.CatalogPath = path
	if _, err := cfg.LoadCatalog(); err == nil || !strings.Contains(err.Error(), "catalog "+path+`: agent h: unknown adapter "nope"`) {
		t.Fatalf("catalog file: %v", err)
	}
	// With both, each agent is named with the catalog it comes from.
	cfg.Catalog.Agents = []catalog.Agent{{ID: "g", Name: "g", Command: []string{"g"}, Adapter: "gemini"}}
	_, err := cfg.LoadCatalog()
	if err == nil || !strings.Contains(err.Error(), `config catalog: agent g: unknown adapter "gemini"`) || !strings.Contains(err.Error(), "catalog "+path+`: agent h: unknown adapter "nope"`) {
		t.Fatalf("both: %v", err)
	}
}

// CONDUCTOR_EXAMPLES turns the example-crew seeding on; it is not a config-file key.
func TestExamplesComesFromTheEnvironmentOnly(t *testing.T) {
	for _, v := range []string{"1", "true"} {
		t.Setenv("CONDUCTOR_EXAMPLES", v)
		if cfg, err := Load(""); err != nil || !cfg.Examples {
			t.Fatalf("CONDUCTOR_EXAMPLES=%s: %+v %v", v, cfg, err)
		}
	}
	for _, v := range []string{"", "0", "yes"} {
		t.Setenv("CONDUCTOR_EXAMPLES", v)
		if cfg, err := Load(""); err != nil || cfg.Examples {
			t.Fatalf("CONDUCTOR_EXAMPLES=%q: %+v %v", v, cfg, err)
		}
	}
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"examples": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "examples") {
		t.Fatalf("a config file with examples should be rejected as an unknown field: %v", err)
	}
}

// yolo comes from the config file, and CONDUCTOR_YOLO overrides it either way.
func TestYoloFromTheFileAndTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"yolo": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		file, env string
		want      bool
	}{
		{"", "", false},
		{path, "", true},
		{"", "1", true},
		{"", "true", true},
		{path, "0", false},
		{path, "false", false},
		{"", "yes", false},
	} {
		t.Setenv("CONDUCTOR_YOLO", tc.env)
		if cfg, err := Load(tc.file); err != nil || cfg.Yolo != tc.want {
			t.Errorf("file %q, CONDUCTOR_YOLO=%q: %v %v", tc.file, tc.env, cfg.Yolo, err)
		}
	}
}

// publicUrl names this machine alone by default and in the example config:
// a share link built on it would reach no one else, so it reads as local.
func TestPublicURLIsLocal(t *testing.T) {
	for u, want := range map[string]bool{
		"http://localhost:8080": true, "http://127.0.0.1:8080": true, "http://[::1]:8080": true, "HTTP://LOCALHOST": true,
		"https://conductor.example.com": false, "http://192.168.1.20:8080": false, "http://conductor.lan": false,
	} {
		if got := (&Config{PublicURL: u}).PublicURLIsLocal(); got != want {
			t.Errorf("%s: local %v, want %v", u, got, want)
		}
	}
	cfg := Defaults()
	cfg.PublicURL = "localhost:8080"
	if err := cfg.Validate(); err == nil {
		t.Fatal("a publicUrl without a scheme passed")
	}
}

func TestReachConfigBoundsAndEnv(t *testing.T) {
	cfg := Defaults()
	if cfg.Reach.Mode != ReachAuto || cfg.Reach.PublicPort != 443 || cfg.STUNServer() != "stun:stun.l.google.com:19302" {
		t.Fatalf("defaults %+v stun=%q", cfg.Reach, cfg.STUNServer())
	}
	env := map[string]string{"CONDUCTOR_REACH": "manual", "CONDUCTOR_REACH_PUBLIC_PORT": "8443", "CONDUCTOR_REACH_STUN": "stun:stun.example.net:3478", "CONDUCTOR_REACH_PUBLIC_PORT_80": "true"}
	if err := applyEnv(cfg, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Reach.Mode != ReachManual || cfg.Reach.PublicPort != 8443 || !cfg.Reach.PublicPort80 || cfg.STUNServer() != "stun:stun.example.net:3478" {
		t.Fatalf("env %+v", cfg.Reach)
	}
	for name, mutate := range map[string]func(*Config){
		"mode": func(c *Config) { c.Reach.Mode = "always" },
		"port": func(c *Config) { c.Reach.PublicPort = 0 },
		"no stun": func(c *Config) {
			c.Reach.STUNServer = ""
			c.ICEServers = []ICEServer{{URLs: []string{"turn:relay.example.net"}}}
		},
	} {
		c := Defaults()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	off := Defaults()
	off.Reach.Mode = ReachOff
	off.ICEServers = nil
	if err := off.Validate(); err != nil {
		t.Fatalf("off needs no STUN server: %v", err)
	}
}

func TestTLSConfigValidation(t *testing.T) {
	ok := func(name string, mutate func(*Config)) *Config {
		t.Helper()
		c := Defaults()
		mutate(c)
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return c
	}
	bad := func(name, want string, mutate func(*Config)) {
		t.Helper()
		c := Defaults()
		mutate(c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", name, err, want)
		}
	}
	if c := Defaults(); c.TLSEnabled() || c.TLS.Listen != "" {
		t.Fatalf("defaults %+v", c.TLS)
	}
	c := ok("acme for the public address", func(c *Config) { c.TLS.ACME = &ACME{Email: "me@example.net"} })
	if c.TLS.Listen != ":8443" || c.TLS.ACME.Challenge != ChallengeTLSALPN || c.TLS.ACME.Profile != "shortlived" || !c.ACMEIdentifiersAreIPs() {
		t.Fatalf("filled %+v %+v", c.TLS, c.TLS.ACME)
	}
	c = ok("acme for a domain", func(c *Config) {
		c.TLS.ACME = &ACME{Domains: []string{"home.example.net"}, Challenge: ChallengeDNS, DNSProvider: "cloudflare"}
	})
	if c.TLS.ACME.Profile != "" || c.ACMEIdentifiersAreIPs() {
		t.Fatalf("domain %+v", c.TLS.ACME)
	}
	ok("files", func(c *Config) { c.TLS.CertFile = "cert.pem"; c.TLS.KeyFile = "key.pem" })
	ok("literal ip", func(c *Config) { c.TLS.ACME = &ACME{Domains: []string{"203.0.113.9"}} })
	ok("http-01 with port 80 mapped", func(c *Config) { c.TLS.ACME = &ACME{Challenge: ChallengeHTTP}; c.Reach.PublicPort80 = true })
	ok("acme with reach manual", func(c *Config) { c.TLS.ACME = &ACME{}; c.Reach.Mode = ReachManual })
	bad("files and acme", "exclusive", func(c *Config) { c.TLS.CertFile = "a"; c.TLS.KeyFile = "b"; c.TLS.ACME = &ACME{} })
	bad("cert without key", "go together", func(c *Config) { c.TLS.CertFile = "a" })
	bad("same listen", "differ", func(c *Config) { c.TLS.ACME = &ACME{}; c.TLS.Listen = ":8080" })
	bad("listen without a source", "need tls.acme", func(c *Config) { c.TLS.Listen = ":8443" })
	bad("dns-01 for an ip", "cannot prove an IP", func(c *Config) { c.TLS.ACME = &ACME{Challenge: ChallengeDNS, DNSProvider: "exec"} })
	bad("wrong profile for an ip", "shortlived", func(c *Config) { c.TLS.ACME = &ACME{Profile: "classic"} })
	bad("no domains and reach off", "needs reach", func(c *Config) { c.TLS.ACME = &ACME{}; c.Reach.Mode = ReachOff })
	bad("public port not 443", "443", func(c *Config) { c.TLS.ACME = &ACME{}; c.Reach.PublicPort = 8443 })
	bad("http-01 without port 80", "publicPort80", func(c *Config) { c.TLS.ACME = &ACME{Challenge: ChallengeHTTP} })
	bad("dns-01 without a provider", "dnsProvider", func(c *Config) { c.TLS.ACME = &ACME{Domains: []string{"a.example"}, Challenge: ChallengeDNS} })
	bad("unknown provider", "cloudflare, exec or httpreq", func(c *Config) {
		c.TLS.ACME = &ACME{Domains: []string{"a.example"}, Challenge: ChallengeDNS, DNSProvider: "route53"}
	})
	bad("unknown challenge", "challenge must be", func(c *Config) { c.TLS.ACME = &ACME{Challenge: "dns-02"} })
	bad("bad ca", "https", func(c *Config) { c.TLS.ACME = &ACME{CADirectory: "http://ca.example/dir"} })
	bad("bad domain", "not a DNS name", func(c *Config) { c.TLS.ACME = &ACME{Domains: []string{"a b"}} })
}

func TestTLSEnvOverrides(t *testing.T) {
	cfg := Defaults()
	env := map[string]string{
		"CONDUCTOR_TLS_LISTEN": ":9443", "CONDUCTOR_TLS_ACME_EMAIL": "me@example.net", "CONDUCTOR_TLS_ACME_DOMAINS": "home.example.net, alt.example.net",
		"CONDUCTOR_TLS_ACME_CHALLENGE": "dns-01", "CONDUCTOR_TLS_ACME_DNS_PROVIDER": "cloudflare", "CONDUCTOR_TLS_ACME_DNS_ENV": "CLOUDFLARE_DNS_API_TOKEN=tok,X=1",
		"CONDUCTOR_TLS_ACME_CA": "https://acme-staging-v02.api.letsencrypt.org/directory", "CONDUCTOR_TLS_ACME_PROFILE": "classic",
	}
	if err := applyEnv(cfg, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	a := cfg.TLS.ACME
	if cfg.TLS.Listen != ":9443" || a == nil || a.Email != "me@example.net" || len(a.Domains) != 2 || a.Domains[1] != "alt.example.net" || a.Challenge != "dns-01" || a.DNSProvider != "cloudflare" || a.DNSEnv["CLOUDFLARE_DNS_API_TOKEN"] != "tok" || a.DNSEnv["X"] != "1" || a.Profile != "classic" || !strings.Contains(a.CADirectory, "staging") {
		t.Fatalf("%+v %+v", cfg.TLS, a)
	}
	plain := Defaults()
	if err := applyEnv(plain, func(k string) string { return map[string]string{"CONDUCTOR_TLS_ACME": "1"}[k] }); err != nil {
		t.Fatal(err)
	}
	if err := plain.Validate(); err != nil || plain.TLS.ACME == nil || plain.TLS.Listen != ":8443" {
		t.Fatalf("CONDUCTOR_TLS_ACME=1: %v %+v", err, plain.TLS)
	}
}

func TestRendezvousConfig(t *testing.T) {
	cfg := Defaults()
	env := map[string]string{"CONDUCTOR_RENDEZVOUS_SERVER": "https://team.example.net/", "CONDUCTOR_RENDEZVOUS_TOKEN": "tok", "CONDUCTOR_RENDEZVOUS_HOST_NAME": "office", "CONDUCTOR_RENDEZVOUS_RELAY_ONLY": "1"}
	if err := applyEnv(cfg, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if r := cfg.Rendezvous; r.Server != "https://team.example.net" || r.Token != "tok" || r.HostName != "office" || !r.RelayOnly {
		t.Fatalf("%+v", r)
	}
	for name, mutate := range map[string]func(*Config){
		"no token":   func(c *Config) { c.Rendezvous.Server = "https://team.example.net" },
		"bad url":    func(c *Config) { c.Rendezvous = Rendezvous{Server: "team.example.net", Token: "t"} },
		"itself":     func(c *Config) { c.Rendezvous = Rendezvous{Server: c.PublicURL, Token: "t"} },
		"token only": func(c *Config) { c.Rendezvous.Token = "t" },
	} {
		c := Defaults()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// The agents' switches: self-service and the launch-time skill install are
// on unless the config or the environment turns them off.
func TestAgentSwitchesAndEnv(t *testing.T) {
	cfg := Defaults()
	if !cfg.SelfService() || !cfg.InstallSkill() || !cfg.MCP() {
		t.Fatal("off by default")
	}
	mcpOff := Defaults()
	if err := applyEnv(mcpOff, func(k string) string { return map[string]string{"CONDUCTOR_AGENT_MCP": "0"}[k] }); err != nil || mcpOff.MCP() {
		t.Fatalf("CONDUCTOR_AGENT_MCP=0: %v %v", err, mcpOff.MCP())
	}
	for _, tc := range []struct {
		env     map[string]string
		self    bool
		install bool
	}{
		{map[string]string{"CONDUCTOR_AGENT_SELF_SERVICE": "0"}, false, true},
		{map[string]string{"CONDUCTOR_AGENT_INSTALL_SKILL": "false"}, true, false},
		{map[string]string{"CONDUCTOR_AGENT_SELF_SERVICE": "true", "CONDUCTOR_AGENT_INSTALL_SKILL": "1"}, true, true},
		{map[string]string{"CONDUCTOR_AGENT_SELF_SERVICE": "maybe"}, true, true},
	} {
		cfg := Defaults()
		if err := applyEnv(cfg, func(k string) string { return tc.env[k] }); err != nil {
			t.Fatal(err)
		}
		if cfg.SelfService() != tc.self || cfg.InstallSkill() != tc.install {
			t.Errorf("%v: self %v install %v", tc.env, cfg.SelfService(), cfg.InstallSkill())
		}
	}
	off := false
	cfg = Defaults()
	cfg.Agents.InstallSkill = &off
	cfg.Agents.SelfService = &off
	if cfg.SelfService() || cfg.InstallSkill() {
		t.Fatal("the config does not turn them off")
	}
}

// The switchyard mode and its relay and origins come from the config or
// the environment; origins are host patterns, never URLs.
func TestSwitchyardConfig(t *testing.T) {
	cfg := Defaults()
	if cfg.Switchyard.Enabled || !cfg.SwitchyardRelay() || strings.Join(cfg.SwitchyardOrigins(), ",") != "127.0.0.1:*,localhost:*" {
		t.Fatalf("defaults: %+v %v", cfg.Switchyard, cfg.SwitchyardOrigins())
	}
	env := map[string]string{"CONDUCTOR_SWITCHYARD": "1", "CONDUCTOR_SWITCHYARD_RELAY": "0", "CONDUCTOR_SWITCHYARD_ORIGINS": "app.example:*, other.example"}
	if err := applyEnv(cfg, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if !cfg.Switchyard.Enabled || cfg.SwitchyardRelay() || strings.Join(cfg.SwitchyardOrigins(), ",") != "app.example:*,other.example" {
		t.Fatalf("env: %+v %v", cfg.Switchyard, cfg.SwitchyardOrigins())
	}
	cfg = Defaults()
	cfg.Switchyard.AllowedOrigins = []string{"http://app.example"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "switchyard.allowedOrigins") {
		t.Fatalf("a URL passed as an origin pattern: %v", err)
	}
}

// The token's old name, adminToken in the file and CONDUCTOR_ADMIN_TOKEN in
// the environment, still works and is reported as renamed; the new name wins
// when both are given.
func TestTheOldAdminTokenNameStillWorks(t *testing.T) {
	cfg := Defaults()
	cfg.AdminToken = "old"
	if err := applyEnv(cfg, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if cfg.WorkbenchToken != "old" || !cfg.WorkbenchTokenRenamed || cfg.AdminToken != "" {
		t.Fatalf("file: %q renamed=%v old=%q", cfg.WorkbenchToken, cfg.WorkbenchTokenRenamed, cfg.AdminToken)
	}
	cfg = Defaults()
	env := map[string]string{"CONDUCTOR_ADMIN_TOKEN": "old-env"}
	if err := applyEnv(cfg, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if cfg.WorkbenchToken != "old-env" || !cfg.WorkbenchTokenRenamed {
		t.Fatalf("env: %q renamed=%v", cfg.WorkbenchToken, cfg.WorkbenchTokenRenamed)
	}
	cfg = Defaults()
	env = map[string]string{"CONDUCTOR_ADMIN_TOKEN": "old-env", "CONDUCTOR_WORKBENCH_TOKEN": "new-env"}
	if err := applyEnv(cfg, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if cfg.WorkbenchToken != "new-env" || cfg.WorkbenchTokenRenamed {
		t.Fatalf("both: %q renamed=%v", cfg.WorkbenchToken, cfg.WorkbenchTokenRenamed)
	}
}

// Open hosts: off by default with the limits set; the environment turns it
// on and sets each limit; the bounds and the need for switchyard.enabled.
func TestSwitchyardOpenHostsConfigAndEnv(t *testing.T) {
	cfg := Defaults()
	if cfg.Switchyard.OpenHosts || cfg.Switchyard.OpenHostSessions != 4 || cfg.Switchyard.OpenHostRegistrationsPerMinute != 6 || cfg.Switchyard.OpenHostRelayKBps != 128 {
		t.Fatalf("defaults: %+v", cfg.Switchyard)
	}
	env := map[string]string{"CONDUCTOR_SWITCHYARD": "1", "CONDUCTOR_SWITCHYARD_OPEN_HOSTS": "true", "CONDUCTOR_SWITCHYARD_OPEN_HOST_SESSIONS": "9", "CONDUCTOR_SWITCHYARD_OPEN_HOST_REGISTRATIONS": "30", "CONDUCTOR_SWITCHYARD_OPEN_HOST_RELAY_KBPS": "64"}
	if err := applyEnv(cfg, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if !cfg.Switchyard.OpenHosts || cfg.Switchyard.OpenHostSessions != 9 || cfg.Switchyard.OpenHostRegistrationsPerMinute != 30 || cfg.Switchyard.OpenHostRelayKBps != 64 {
		t.Fatalf("env: %+v", cfg.Switchyard)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"open hosts without a switchyard": func(c *Config) { c.Switchyard.Enabled = false; c.Switchyard.OpenHosts = true },
		"no sessions":                     func(c *Config) { c.Switchyard.OpenHostSessions = 0 },
		"no registrations":                func(c *Config) { c.Switchyard.OpenHostRegistrationsPerMinute = 0 },
		"negative relay":                  func(c *Config) { c.Switchyard.OpenHostRelayKBps = -1 },
	} {
		c := Defaults()
		c.Switchyard.Enabled = true
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
