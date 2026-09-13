package trackerworker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
)

func TestTrackerCredentialFilesBindIdentityAndRejectUnsafeFiles(t *testing.T) {
	directory := t.TempDir()
	secret := filepath.Join(directory, "secret.json")
	index := filepath.Join(directory, "references.json")
	if err := os.WriteFile(secret, []byte(`{"token":"synthetic-test-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := domain.TrackerConfig{WorkspaceID: "workspace", Provider: "jira", Host: "synthetic.atlassian.net", ScopeID: "123", CredentialID: "api", WebhookCredentialID: "hook"}
	data, _ := json.Marshal(map[string]any{"credentials": []CredentialFile{{ID: "api", WorkspaceID: cfg.WorkspaceID, Provider: cfg.Provider, Host: cfg.Host, ScopeID: cfg.ScopeID, SecretFile: secret}}})
	if err := os.WriteFile(index, data, 0600); err != nil {
		t.Fatal(err)
	}
	refs, err := LoadCredentials(index)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := refs.Resolve(context.Background(), cfg, "api"); err != nil || value.Token != "synthetic-test-token" {
		t.Fatal("selected credential unavailable", err)
	}
	for _, mutate := range []func(*domain.TrackerConfig){func(c *domain.TrackerConfig) { c.WorkspaceID = "foreign" }, func(c *domain.TrackerConfig) { c.Host = "foreign.atlassian.net" }, func(c *domain.TrackerConfig) { c.ScopeID = "124" }, func(c *domain.TrackerConfig) { c.CredentialID = "different" }} {
		changed := cfg
		mutate(&changed)
		if _, err := refs.Resolve(context.Background(), changed, "api"); err == nil {
			t.Fatal("foreign identity obtained credential")
		}
	}
	for _, mode := range []os.FileMode{0644, 0620} {
		if err := os.Chmod(secret, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := refs.Resolve(context.Background(), cfg, "api"); err == nil {
			t.Fatal("read shared credential file")
		}
	}
	if err := os.Chmod(secret, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "symlink")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, err := secretFile(link, 16384); err == nil {
		t.Fatal("followed credential symlink")
	}
	fifo := filepath.Join(directory, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := secretFile(fifo, 16384); err == nil {
		t.Fatal("read credential FIFO")
	}
	if err := os.WriteFile(secret, make([]byte, 16385), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := refs.Resolve(context.Background(), cfg, "api"); err == nil {
		t.Fatal("read oversized credential")
	}
}
