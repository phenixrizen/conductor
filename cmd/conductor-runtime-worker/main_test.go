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
