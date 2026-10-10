package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// LocalFileDeny finds the files of the conductor serve that would start on
// this machine as serve does: the config file named, if any, the
// environment over it, the data directory as ResolveDataDir finds it, and
// ~/.conductor whichever that is; and the directories of the desktop app's
// server. It writes nothing, and a config file it cannot read or parse, or
// a relative path it cannot place, is an error.
func TestLocalFileDeny(t *testing.T) {
	// setup gives the test a home and a working directory of its own, the
	// config directory in the home, and no CONDUCTOR_DATA_DIR or
	// CONDUCTOR_CATALOG_PATH; it returns both.
	setup := func(t *testing.T) (home, cwd string) {
		t.Helper()
		home, cwd = t.TempDir(), t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("CONDUCTOR_DATA_DIR", "")
		t.Setenv("CONDUCTOR_CATALOG_PATH", "")
		t.Chdir(cwd)
		return home, cwd
	}
	// local is what is refused on every machine with a home: ~/.conductor,
	// the desktop app's data directory inside WSL and its own directory.
	local := func(home string) []string {
		return []string{filepath.Join(home, ".conductor"), filepath.Join(home, ".local", "share", "conductor", "data"), filepath.Join(home, ".config", "Conductor")}
	}
	writeFile := func(t *testing.T, path, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	deny := func(t *testing.T, path string) []string {
		t.Helper()
		got, err := LocalFileDeny(path)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	t.Run("no config file: ~/.conductor", func(t *testing.T) {
		home, _ := setup(t)
		if got, want := deny(t, ""), local(home); !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
		for _, d := range local(home) {
			if _, err := os.Lstat(d); err == nil {
				t.Errorf("%s was made", d)
			}
		}
	})

	t.Run("the config file's dataDir and catalogPath, and the copies beside the two files", func(t *testing.T) {
		home, _ := setup(t)
		etc := t.TempDir()
		data := filepath.Join(t.TempDir(), "data")
		catalogFile := filepath.Join(etc, "agents", "..", "agents.json")
		cfg := writeFile(t, filepath.Join(etc, "conductor.json"), `{"workbenchToken":"w","dataDir":"`+data+`","catalogPath":"`+catalogFile+`"}`)
		want := slices.Concat([]string{data}, local(home), []string{cfg, filepath.Join(etc, "*conductor.json*"), filepath.Join(etc, "agents.json"), filepath.Join(etc, "*agents.json*")})
		if got := deny(t, cfg); !slices.Equal(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("a relative path to the config file", func(t *testing.T) {
		home, cwd := setup(t)
		writeFile(t, filepath.Join(cwd, "conf", "conductor.json"), `{}`)
		want := slices.Concat(local(home), []string{filepath.Join(cwd, "conf", "conductor.json"), filepath.Join(cwd, "conf", "*conductor.json*")})
		if got := deny(t, filepath.Join("conf", "conductor.json")); !slices.Equal(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	// A relative dataDir or catalogPath is resolved by conductor serve
	// against the directory it runs in, which need not be this one: the
	// server below runs in its own directory, the session in a home that
	// holds a directory of the same name. Neither is guessed at.
	t.Run("a relative dataDir or catalogPath is refused", func(t *testing.T) {
		for _, c := range []struct{ name, body, env, value, want string }{
			{"dataDir in the file", `{"dataDir":"state"}`, "", "", "dataDir in "},
			{"catalogPath in the file", `{"catalogPath":"agents.json"}`, "", "", "catalogPath in "},
			{"dataDir in the file, a dot path", `{"dataDir":"./state"}`, "", "", "dataDir in "},
			{"CONDUCTOR_DATA_DIR", `{}`, "CONDUCTOR_DATA_DIR", "state", "CONDUCTOR_DATA_DIR"},
			{"CONDUCTOR_CATALOG_PATH", `{}`, "CONDUCTOR_CATALOG_PATH", "agents.json", "CONDUCTOR_CATALOG_PATH"},
		} {
			t.Run(c.name, func(t *testing.T) {
				setup(t)
				service := t.TempDir()
				cfg := writeFile(t, filepath.Join(service, "conductor.json"), c.body)
				if c.env != "" {
					t.Setenv(c.env, c.value)
				}
				got, err := LocalFileDeny(cfg)
				if err == nil || got != nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "absolute") {
					t.Fatalf("%q, %v; want an error naming %q", got, err, c.want)
				}
				if c.env != "" {
					// Without a config file too.
					if got, err := LocalFileDeny(""); err == nil || got != nil || !strings.Contains(err.Error(), c.env) {
						t.Fatalf("without a config file: %q, %v", got, err)
					}
				}
			})
		}
	})

	t.Run("an older conductor.d beside the config file, and ~/.conductor", func(t *testing.T) {
		home, _ := setup(t)
		service := t.TempDir()
		cfg := writeFile(t, filepath.Join(service, "conductor.json"), `{}`)
		old := filepath.Join(service, "conductor.d")
		if err := os.Mkdir(old, 0o700); err != nil {
			t.Fatal(err)
		}
		want := slices.Concat([]string{old}, local(home), []string{cfg, filepath.Join(service, "*conductor.json*")})
		if got := deny(t, cfg); !slices.Equal(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("CONDUCTOR_DATA_DIR and CONDUCTOR_CATALOG_PATH over the file", func(t *testing.T) {
		home, _ := setup(t)
		dir := t.TempDir()
		cfg := writeFile(t, filepath.Join(dir, "conductor.json"), `{"dataDir":"/file/data","catalogPath":"/file/agents.json"}`)
		envData, envCat := filepath.Join(dir, "env-data"), filepath.Join(dir, "env-agents.json")
		t.Setenv("CONDUCTOR_DATA_DIR", envData)
		t.Setenv("CONDUCTOR_CATALOG_PATH", envCat)
		want := slices.Concat([]string{envData}, local(home), []string{cfg, filepath.Join(dir, "*conductor.json*"), envCat, filepath.Join(dir, "*env-agents.json*")})
		if got := deny(t, cfg); !slices.Equal(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
		// Without a config file too.
		want = slices.Concat([]string{envData}, local(home), []string{envCat, filepath.Join(dir, "*env-agents.json*")})
		if got := deny(t, ""); !slices.Equal(got, want) {
			t.Errorf("without a config file: %q", got)
		}
	})

	t.Run("a config file that is a link: the copies beside its target too", func(t *testing.T) {
		home, _ := setup(t)
		target := writeFile(t, filepath.Join(t.TempDir(), "prod.json"), `{}`)
		dir := t.TempDir()
		link := filepath.Join(dir, "conductor.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		want := slices.Concat(local(home), []string{link, filepath.Join(dir, "*conductor.json*"), filepath.Join(filepath.Dir(target), "*prod.json*")})
		if got := deny(t, link); !slices.Equal(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("an older ./conductor.d the server would keep, and ~/.conductor", func(t *testing.T) {
		home, cwd := setup(t)
		old := filepath.Join(cwd, "conductor.d")
		if err := os.Mkdir(old, 0o700); err != nil {
			t.Fatal(err)
		}
		if got, want := deny(t, ""), slices.Concat([]string{old}, local(home)); !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	// The desktop app starts its server with CONDUCTOR_DATA_DIR from its
	// settings, which a shell elsewhere does not have: its own directory is
	// refused, and the data directory its settings name.
	t.Run("the desktop app's directories", func(t *testing.T) {
		home, _ := setup(t)
		config := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", config)
		app := filepath.Join(config, "Conductor")
		base := []string{filepath.Join(home, ".conductor"), filepath.Join(home, ".local", "share", "conductor", "data"), app}
		if got := deny(t, ""); !slices.Equal(got, base) {
			t.Errorf("no settings: %q, want %q", got, base)
		}
		data := filepath.Join(t.TempDir(), "elsewhere")
		writeFile(t, filepath.Join(app, "settings.json"), `{"dataDir":"`+data+`/","allowedRoots":["`+home+`"],"switchyardToken":"t","zoomLevel":0}`)
		if got, want := deny(t, ""), append(slices.Clone(base), data); !slices.Equal(got, want) {
			t.Errorf("settings naming a data directory: %q, want %q", got, want)
		}
		for _, body := range []string{`{oops`, `{"dataDir":"relative/data"}`, `{"dataDir":7}`, `[]`, ``} {
			writeFile(t, filepath.Join(app, "settings.json"), body)
			if got := deny(t, ""); !slices.Equal(got, base) {
				t.Errorf("settings %q: %q, want %q", body, got, base)
			}
		}
	})

	t.Run("no home directory", func(t *testing.T) {
		setup(t)
		t.Setenv("HOME", "")
		if got := deny(t, ""); len(got) != 0 {
			t.Errorf("got %q, want nothing", got)
		}
		data := t.TempDir()
		t.Setenv("CONDUCTOR_DATA_DIR", data)
		if got := deny(t, ""); !slices.Equal(got, []string{data}) {
			t.Errorf("with CONDUCTOR_DATA_DIR: %q", got)
		}
	})

	t.Run("a config file that cannot be read or parsed", func(t *testing.T) {
		setup(t)
		dir := t.TempDir()
		for _, c := range []struct{ name, path, want string }{
			{"missing", filepath.Join(dir, "missing.json"), "read config"},
			{"a directory", dir, "read config"},
			{"not JSON", writeFile(t, filepath.Join(dir, "bad.json"), `{oops`), "parse config"},
			{"an unknown key", writeFile(t, filepath.Join(dir, "unknown.json"), `{"dataDirectory":"/x"}`), "parse config"},
			{"trailing data", writeFile(t, filepath.Join(dir, "trailing.json"), `{} {}`), "parse config"},
		} {
			if got, err := LocalFileDeny(c.path); err == nil || !strings.Contains(err.Error(), c.want) || got != nil {
				t.Errorf("%s: %q, %v; want an error about %q", c.name, got, err, c.want)
			}
		}
	})
}
