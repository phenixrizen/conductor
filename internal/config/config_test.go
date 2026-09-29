package config

import (
	"os"
	"path/filepath"
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
	path := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(path, []byte(`{"nope":1}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
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
