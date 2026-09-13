package main

import "testing"

func TestPublisherRequiresExplicitLocalTemporalAndSeparateCredentialFile(t *testing.T) {
	valid := map[string]string{"DATABASE_URL": "postgres://fixture", "CONDUCTOR_TEMPORAL_MODE": "local", "CONDUCTOR_TEMPORAL_NAMESPACE": "publication", "CONDUCTOR_RUNTIME_CREDENTIALS_FILE": "/operator/publication.json"}
	for _, tc := range []struct {
		key, value string
		ok         bool
	}{{"", "", true}, {"CONDUCTOR_TEMPORAL_MODE", "", false}, {"CONDUCTOR_RUNTIME_CREDENTIALS_FILE", "relative.json", false}, {"CONDUCTOR_TEMPORAL_ADDRESS", "github.com:7233", false}, {"CONDUCTOR_TEMPORAL_NAMESPACE", "bad namespace", false}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			_, err := loadConfig(func(k string) string {
				if k == tc.key {
					return tc.value
				}
				return valid[k]
			})
			if (err == nil) != tc.ok {
				t.Fatalf("configuration %v", err)
			}
		})
	}
}

func TestWorkerSharedRemoteTLSProfile(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "postgres://fixture", "CONDUCTOR_TEMPORAL_MODE": "remote-tls", "CONDUCTOR_TEMPORAL_ADDRESS": "temporal.example.invalid:7233", "CONDUCTOR_TEMPORAL_SERVER_NAME": "temporal.example.invalid", "CONDUCTOR_TEMPORAL_NAMESPACE": "explicit-namespace", "CONDUCTOR_CONTEXT_CREDENTIALS_FILE": "/operator/context.json", "CONDUCTOR_EXECUTION_PROFILES_FILE": "/operator/profiles.json", "CONDUCTOR_PUBLICATION_CREDENTIALS_FILE": "/operator/publication.json", "CONDUCTOR_RUNTIME_CREDENTIALS_FILE": "/operator/runtime.json"}
	c, err := loadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	options := c.temporal.Options("test-worker", safeLogger{})
	if options.HostPort != env["CONDUCTOR_TEMPORAL_ADDRESS"] || options.Namespace != env["CONDUCTOR_TEMPORAL_NAMESPACE"] || options.ConnectionOptions.TLS == nil || options.ConnectionOptions.TLS.ServerName != env["CONDUCTOR_TEMPORAL_SERVER_NAME"] || options.ConnectionOptions.MaxPayloadSize != 1<<20 {
		t.Fatal("shared transport configuration was discarded")
	}
	env["CONDUCTOR_TEMPORAL_MODE"] = "local"
	if _, err := loadConfig(func(key string) string { return env[key] }); err == nil {
		t.Fatal("remote profile silently downgraded")
	}
}
