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
