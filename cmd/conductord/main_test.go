package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestAuthenticationConfigurationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, mode, address, issuer, audience string
		valid                                 bool
	}{
		{name: "mode is explicit"},
		{name: "unknown mode", mode: "automatic"},
		{name: "local default loopback", mode: "local", valid: true},
		{name: "local IPv6 loopback", mode: "local", address: "[::1]:8080", valid: true},
		{name: "local wildcard rejected", mode: "local", address: ":8080"},
		{name: "local remote rejected", mode: "local", address: "192.0.2.1:8080"},
		{name: "local mixed credentials rejected", mode: "local", issuer: "https://issuer.example.invalid"},
		{name: "OIDC missing issuer", mode: "oidc", audience: "conductor-api"},
		{name: "OIDC missing audience", mode: "oidc", issuer: "https://issuer.example.invalid"},
		{name: "OIDC configured", mode: "oidc", issuer: "https://issuer.example.invalid", audience: "conductor-api", valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{"DATABASE_URL": "synthetic", "CONDUCTOR_AUTH_MODE": tc.mode, "CONDUCTOR_ADDR": tc.address,
				"CONDUCTOR_OIDC_ISSUER": tc.issuer, "CONDUCTOR_OIDC_AUDIENCE": tc.audience}
			_, err := loadConfig(func(key string) string { return values[key] })
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}

func TestBrowserConfigurationIsCompleteAndExplicit(t *testing.T) {
	for _, tc := range []struct {
		name, mode, origin, client, file string
		valid                            bool
	}{
		{"complete", "oidc", "https://conductor.example.test", "browser", "secret", true},
		{"missing origin", "oidc", "", "browser", "secret", false},
		{"missing client", "oidc", "https://conductor.example.test", "", "secret", false},
		{"missing secret", "oidc", "https://conductor.example.test", "browser", "", false},
		{"local browser settings", "local", "https://conductor.example.test", "browser", "secret", false},
		{"remote plaintext", "oidc", "http://conductor.example.test", "browser", "secret", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{"DATABASE_URL": "synthetic", "CONDUCTOR_AUTH_MODE": tc.mode, "CONDUCTOR_PUBLIC_ORIGIN": tc.origin,
				"CONDUCTOR_OIDC_CLIENT_ID": tc.client, "CONDUCTOR_OIDC_CLIENT_SECRET_FILE": tc.file}
			if tc.mode == "oidc" {
				values["CONDUCTOR_OIDC_ISSUER"] = "https://issuer.example.test"
				values["CONDUCTOR_OIDC_AUDIENCE"] = "api"
			}
			_, err := loadConfig(func(key string) string { return values[key] })
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}

func TestBrowserClientSecretFilesAreBounded(t *testing.T) {
	dir := t.TempDir()
	for _, content := range []string{"synthetic-secret\n", "synthetic-secret\r\n", "", strings.Repeat("s", 4097), "secret\nextra", "secret\x00suffix"} {
		path := filepath.Join(dir, "client-secret")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		value, err := browserClientSecret(path)
		valid := content == "synthetic-secret\n" || content == "synthetic-secret\r\n"
		if (err == nil) != valid || (valid && value != "synthetic-secret") {
			t.Fatalf("unexpected result for %d-byte input: err=%v", len(content), err)
		}
		if err != nil && strings.Contains(err.Error(), content) && content != "" {
			t.Fatal("secret was included in an error")
		}
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := browserClientSecret(fifo); err == nil {
		t.Fatal("named pipe accepted as secret")
	}
	if _, err := browserClientSecret(dir); err == nil {
		t.Fatal("directory accepted as secret")
	}
}

func TestContextCollectionCapabilityRequiresExplicitOIDCConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, mode, setting string
		issuer, audience    string
		valid, enabled      bool
	}{
		{name: "local default disabled", mode: "local", valid: true},
		{name: "local explicit disabled", mode: "local", setting: "0", valid: true},
		{name: "local cannot enable provider access", mode: "local", setting: "1"},
		{name: "OIDC default disabled", mode: "oidc", issuer: "https://issuer.example.test", audience: "api", valid: true},
		{name: "OIDC explicit disabled", mode: "oidc", setting: "0", issuer: "https://issuer.example.test", audience: "api", valid: true},
		{name: "OIDC explicit enabled", mode: "oidc", setting: "1", issuer: "https://issuer.example.test", audience: "api", valid: true, enabled: true},
		{name: "no automatic authentication", setting: "1"},
		{name: "issuer still required", mode: "oidc", setting: "1", audience: "api"},
		{name: "audience still required", mode: "oidc", setting: "1", issuer: "https://issuer.example.test"},
		{name: "boolean spelling rejected", mode: "oidc", setting: "true", issuer: "https://issuer.example.test", audience: "api"},
		{name: "false spelling rejected", mode: "local", setting: "false"},
		{name: "leading whitespace rejected", mode: "oidc", setting: " 1", issuer: "https://issuer.example.test", audience: "api"},
		{name: "trailing whitespace rejected", mode: "local", setting: "0 "},
		{name: "unknown setting rejected", mode: "local", setting: "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{"DATABASE_URL": "synthetic", "CONDUCTOR_AUTH_MODE": tc.mode,
				"CONDUCTOR_CONTEXT_COLLECTIONS": tc.setting, "CONDUCTOR_OIDC_ISSUER": tc.issuer, "CONDUCTOR_OIDC_AUDIENCE": tc.audience}
			c, err := loadConfig(func(name string) string { return values[name] })
			if (err == nil) != tc.valid || (err == nil && c.contextCollections != tc.enabled) {
				t.Fatalf("enabled=%v want=%v valid=%v error=%v", c.contextCollections, tc.enabled, tc.valid, err)
			}
		})
	}
}

func TestCoordinationRequiresExplicitOIDCCapability(t *testing.T) {
	for _, test := range []struct {
		mode, setting  string
		valid, enabled bool
	}{{"local", "", true, false}, {"local", "1", false, false}, {"oidc", "", true, false}, {"oidc", "0", true, false}, {"oidc", "1", true, true}, {"oidc", "true", false, false}, {"oidc", " 1", false, false}} {
		values := map[string]string{"DATABASE_URL": "synthetic", "CONDUCTOR_AUTH_MODE": test.mode, "CONDUCTOR_COORDINATION": test.setting}
		if test.mode == "oidc" {
			values["CONDUCTOR_OIDC_ISSUER"] = "https://issuer.example.test"
			values["CONDUCTOR_OIDC_AUDIENCE"] = "api"
		}
		c, err := loadConfig(func(key string) string { return values[key] })
		if (err == nil) != test.valid || err == nil && c.coordination != test.enabled {
			t.Fatalf("mode=%s setting=%q config=%+v err=%v", test.mode, test.setting, c, err)
		}
	}
}
