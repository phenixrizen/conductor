package main

import (
	"bytes"
	"errors"
	"log"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const workerLogCanary = "synthetic-source-and-provider-token-canary"

func validWorkerEnvironment(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"DATABASE_URL":                       "postgres://synthetic-user:synthetic-secret@127.0.0.1:5432/synthetic",
		"CONDUCTOR_TEMPORAL_MODE":            "local",
		"CONDUCTOR_TEMPORAL_NAMESPACE":       "synthetic-context",
		"CONDUCTOR_CONTEXT_CREDENTIALS_FILE": filepath.Join(t.TempDir(), "credentials.json"),
	}
}
func TestWorkerConfigurationRequiresExplicitLocalModeAndScope(t *testing.T) {
	for _, row := range []struct{ name, key, value string }{
		{"missing_database", "DATABASE_URL", ""},
		{"missing_mode", "CONDUCTOR_TEMPORAL_MODE", ""},
		{"unsupported_remote_mode", "CONDUCTOR_TEMPORAL_MODE", "remote"},
		{"mode_case", "CONDUCTOR_TEMPORAL_MODE", "LOCAL"},
		{"missing_namespace", "CONDUCTOR_TEMPORAL_NAMESPACE", ""},
		{"namespace_space", "CONDUCTOR_TEMPORAL_NAMESPACE", "synthetic context"},
		{"namespace_control", "CONDUCTOR_TEMPORAL_NAMESPACE", workerLogCanary + "\n"},
		{"namespace_too_long", "CONDUCTOR_TEMPORAL_NAMESPACE", strings.Repeat("a", 129)},
		{"missing_credentials", "CONDUCTOR_CONTEXT_CREDENTIALS_FILE", ""},
		{"relative_credentials", "CONDUCTOR_CONTEXT_CREDENTIALS_FILE", "credentials.json"},
		{"credential_path_too_long", "CONDUCTOR_CONTEXT_CREDENTIALS_FILE", "/" + strings.Repeat("a", 4096)},
		{"hostname", "CONDUCTOR_TEMPORAL_ADDRESS", "localhost:7233"},
		{"external_ip", "CONDUCTOR_TEMPORAL_ADDRESS", "192.0.2.1:7233"},
		{"unspecified_ipv4", "CONDUCTOR_TEMPORAL_ADDRESS", "0.0.0.0:7233"},
		{"unspecified_ipv6", "CONDUCTOR_TEMPORAL_ADDRESS", "[::]:7233"},
		{"external_ipv6", "CONDUCTOR_TEMPORAL_ADDRESS", "[2001:db8::1]:7233"},
		{"url", "CONDUCTOR_TEMPORAL_ADDRESS", "http://127.0.0.1:7233"},
		{"userinfo", "CONDUCTOR_TEMPORAL_ADDRESS", workerLogCanary + "@127.0.0.1:7233"},
		{"missing_port", "CONDUCTOR_TEMPORAL_ADDRESS", "127.0.0.1"},
		{"zero_port", "CONDUCTOR_TEMPORAL_ADDRESS", "127.0.0.1:0"},
		{"large_port", "CONDUCTOR_TEMPORAL_ADDRESS", "127.0.0.1:65536"},
		{"named_port", "CONDUCTOR_TEMPORAL_ADDRESS", "127.0.0.1:http"},
		{"address_query", "CONDUCTOR_TEMPORAL_ADDRESS", "127.0.0.1:7233?" + workerLogCanary},
	} {
		t.Run(row.name, func(t *testing.T) {
			env := validWorkerEnvironment(t)
			env[row.key] = row.value
			_, err := loadConfig(func(key string) string { return env[key] })
			if err == nil {
				t.Fatalf("accepted unsupported %s", row.name)
			}
			if strings.Contains(err.Error(), workerLogCanary) || strings.Contains(err.Error(), "synthetic-secret") || len(err.Error()) > 256 {
				t.Fatal("configuration error exposed input/credential data")
			}
		})
	}
}

func TestWorkerConfigurationSupportsLiteralLoopbackAndNoProviderOverrides(t *testing.T) {
	for _, address := range []string{"", "127.0.0.1:7233", "127.0.0.2:17001", "[::1]:7233", "[::ffff:127.0.0.1]:7233"} {
		t.Run(address, func(t *testing.T) {
			env := validWorkerEnvironment(t)
			env["CONDUCTOR_TEMPORAL_ADDRESS"] = address
			env["CONDUCTOR_GITHUB_API_ORIGIN"] = "https://example.invalid/" + workerLogCanary
			env["CONDUCTOR_GITLAB_API_ORIGIN"] = "https://example.invalid/" + workerLogCanary
			env["CONDUCTOR_CONTEXT_ALLOW_INSECURE_LOOPBACK"] = "true"
			read := map[string]bool{}
			got, err := loadConfig(func(key string) string { read[key] = true; return env[key] })
			if err != nil {
				t.Fatal(err)
			}
			wantAddress := address
			if wantAddress == "" {
				wantAddress = "127.0.0.1:7233"
			}
			if got.address != wantAddress || got.namespace != "synthetic-context" || got.credentialsFile != env["CONDUCTOR_CONTEXT_CREDENTIALS_FILE"] {
				t.Fatalf("worker configuration changed explicit binding: %+v", got)
			}
			wantKeys := map[string]bool{"DATABASE_URL": true, "CONDUCTOR_TEMPORAL_ADDRESS": true, "CONDUCTOR_TEMPORAL_NAMESPACE": true, "CONDUCTOR_CONTEXT_CREDENTIALS_FILE": true, "CONDUCTOR_TEMPORAL_MODE": true}
			if !reflect.DeepEqual(read, wantKeys) {
				t.Fatalf("worker loaded an undocumented configuration override: %v", read)
			}
		})
	}
}

type unformattableSDKField struct{}

func (unformattableSDKField) String() string { panic("SDK diagnostics must not be formatted") }

func TestSDKLoggerDropsMessagesStructuredFieldsAndErrorCauses(t *testing.T) {
	var output bytes.Buffer
	oldWriter, oldFlags, oldPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() { log.SetOutput(oldWriter); log.SetFlags(oldFlags); log.SetPrefix(oldPrefix) })
	logger := safeLogger{}
	fields := []interface{}{"token", workerLogCanary, "error", errors.New(workerLogCanary), "detail", unformattableSDKField{}}
	logger.Debug(workerLogCanary, fields...)
	logger.Info(workerLogCanary, fields...)
	if output.Len() != 0 {
		t.Fatal("SDK debug/info diagnostic content reached logs")
	}
	logger.Warn(workerLogCanary, fields...)
	logger.Error(workerLogCanary, fields...)
	want := "Temporal worker warning; inspect collection observations\nTemporal worker error; inspect collection observations\n"
	if output.String() != want {
		t.Fatalf("unsafe SDK diagnostic output: %q", output.String())
	}
}
