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
	writeConfig := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "conductor.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("without a config file it is conductor.d in the current directory", func(t *testing.T) {
		cfg := Defaults()
		cfg.ResolveDataDir("")
		if want := "conductor.d"; cfg.DataDir != want {
			t.Fatalf("DataDir = %q, want %q", cfg.DataDir, want)
		}
	})

	t.Run("with a config file it sits next to it", func(t *testing.T) {
		cfg := Defaults()
		cfg.ResolveDataDir("/etc/x/conductor.json")
		if want := filepath.Join("/etc/x", "conductor.d"); cfg.DataDir != want {
			t.Fatalf("DataDir = %q, want %q", cfg.DataDir, want)
		}
	})

	t.Run("a value from the config file is left alone", func(t *testing.T) {
		t.Setenv("CONDUCTOR_DATA_DIR", "")
		path := writeConfig(t, `{"dataDir":"/srv/from-file"}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.ResolveDataDir(path)
		if cfg.DataDir != "/srv/from-file" {
			t.Fatalf("DataDir = %q", cfg.DataDir)
		}
	})

	t.Run("the environment override wins and is left alone", func(t *testing.T) {
		t.Setenv("CONDUCTOR_DATA_DIR", "/srv/from-env")
		path := writeConfig(t, `{"dataDir":"/srv/from-file"}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DataDir != "/srv/from-env" {
			t.Fatalf("after Load DataDir = %q, want the env value", cfg.DataDir)
		}
		cfg.ResolveDataDir(path)
		if cfg.DataDir != "/srv/from-env" {
			t.Fatalf("after ResolveDataDir DataDir = %q", cfg.DataDir)
		}
		// The same holds without a config file at all.
		cfg, err = Load("")
		if err != nil {
			t.Fatal(err)
		}
		cfg.ResolveDataDir("")
		if cfg.DataDir != "/srv/from-env" {
			t.Fatalf("no config file: DataDir = %q", cfg.DataDir)
		}
	})
}
