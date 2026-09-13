package main

import "testing"

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
