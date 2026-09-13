package collectionworker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
)

func credentialFixture(t *testing.T) (string, string, CredentialFile, domain.ContextIntegration) {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "provider.token")
	catalogPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(tokenPath, []byte(workerTokenCanary+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, work, _ := activityFixture()
	entry := CredentialFile{ID: work.Integration.CredentialID, WorkspaceID: work.Collection.Source.WorkspaceID, RepositoryID: work.Collection.Source.RepositoryID, Provider: "github", Host: "github.com", ProviderID: "42", TokenFile: tokenPath}
	writeCredentialCatalog(t, catalogPath, []CredentialFile{entry})
	return catalogPath, tokenPath, entry, work.Integration
}
func writeCredentialCatalog(t *testing.T, path string, entries []CredentialFile) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"credentials": entries})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func assertCredentialFailure(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, contextworkflow.ErrInvalidConfiguration) {
		t.Fatalf("credential failure=%v", err)
	}
	if strings.Contains(err.Error(), workerTokenCanary) || err.Error() != contextworkflow.ErrInvalidConfiguration.Error() {
		t.Fatal("credential failure exposed raw filesystem/provider details")
	}
}

func TestCredentialRotationPreservesCanonicalBinding(t *testing.T) {
	path, tokenFile, _, integration := credentialFixture(t)
	c, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Resolve(context.Background(), integration)
	if err != nil || got != workerTokenCanary {
		t.Fatalf("resolve=%q %v", got, err)
	}
	if err = os.WriteFile(tokenFile, []byte("synthetic-rotated-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rotated, err := c.Resolve(context.Background(), integration)
	if err != nil || rotated != "synthetic-rotated-token" {
		t.Fatalf("token was cached across attempts: %q %v", rotated, err)
	}
	if err = os.Remove(tokenFile); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Resolve(context.Background(), integration); err == nil {
		t.Fatal("removed operator credential kept working")
	}
}

func TestCredentialResolverRejectsCanonicalMismatch(t *testing.T) {
	for _, field := range []string{"id", "workspace", "repository", "provider", "host", "provider_id"} {
		t.Run(field, func(t *testing.T) {
			path, tokenFile, _, integration := credentialFixture(t)
			c, err := LoadCredentials(path)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "id":
				integration.CredentialID = "other"
			case "workspace":
				integration.Source.WorkspaceID = "other-workspace"
			case "repository":
				integration.Source.RepositoryID = "other-repository"
			case "provider":
				integration.Source.Provider = "gitlab"
			case "host":
				integration.Source.Host = "gitlab.com"
			case "provider_id":
				integration.Source.ProviderID = "43"
			}
			// A FIFO would block if mismatched configuration were allowed to reach
			// credential loading. Mismatch must be rejected before opening the file.
			if err = os.Remove(tokenFile); err != nil {
				t.Fatal(err)
			}
			if err = syscall.Mkfifo(tokenFile, 0600); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			got, err := c.Resolve(context.Background(), integration)
			assertCredentialFailure(t, err)
			if got != "" || time.Since(started) > time.Second {
				t.Fatal("mismatched canonical binding read or retained credential")
			}
		})
	}
}

func TestCredentialCatalogBoundsAndStrictFields(t *testing.T) {
	for _, kind := range []string{"empty", "null", "unknown_field", "inline_token", "trailing_json", "duplicate_id", "too_many", "oversized", "relative_token", "unsupported_host", "invalid_provider_id", "invalid_provider", "relative_catalog", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			path, _, entry, _ := credentialFixture(t)
			switch kind {
			case "empty":
				writeCredentialCatalog(t, path, nil)
			case "null":
				if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown_field", "inline_token":
				data, _ := json.Marshal(entry)
				var row map[string]any
				_ = json.Unmarshal(data, &row)
				key := "unexpected"
				if kind == "inline_token" {
					key = "token"
				}
				row[key] = workerTokenCanary
				b, _ := json.Marshal(map[string]any{"credentials": []any{row}})
				if err := os.WriteFile(path, b, 0600); err != nil {
					t.Fatal(err)
				}
			case "trailing_json":
				data, _ := os.ReadFile(path)
				if err := os.WriteFile(path, append(data, []byte(` {"token":"`+workerTokenCanary+`"}`)...), 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate_id":
				writeCredentialCatalog(t, path, []CredentialFile{entry, entry})
			case "too_many":
				rows := make([]CredentialFile, 101)
				for i := range rows {
					rows[i] = entry
					rows[i].ID = strings.Repeat("a", i+1)
				}
				writeCredentialCatalog(t, path, rows)
			case "oversized":
				if err := os.WriteFile(path, []byte(strings.Repeat(" ", (1<<20)+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "relative_token":
				entry.TokenFile = "provider.token"
				writeCredentialCatalog(t, path, []CredentialFile{entry})
			case "unsupported_host":
				entry.Host = "github.example.test"
				writeCredentialCatalog(t, path, []CredentialFile{entry})
			case "invalid_provider_id":
				entry.ProviderID = "042"
				writeCredentialCatalog(t, path, []CredentialFile{entry})
			case "invalid_provider":
				entry.Provider = "other"
				writeCredentialCatalog(t, path, []CredentialFile{entry})
			case "relative_catalog":
				path = "relative.json"
			case "directory":
				path = t.TempDir()
			case "fifo":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			started := time.Now()
			_, err := LoadCredentials(path)
			assertCredentialFailure(t, err)
			if time.Since(started) > time.Second {
				t.Fatal("catalog file type was not checked promptly")
			}
		})
	}
}

func TestCredentialTokenFileBoundsAndControls(t *testing.T) {
	for _, kind := range []string{"empty", "newline_only", "double_newline", "crlf", "space", "nul", "unicode", "too_large", "directory", "fifo", "missing", "max_size"} {
		t.Run(kind, func(t *testing.T) {
			path, tokenPath, _, integration := credentialFixture(t)
			c, err := LoadCredentials(path)
			if err != nil {
				t.Fatal(err)
			}
			var data []byte
			switch kind {
			case "empty":
				data = nil
			case "newline_only":
				data = []byte("\n")
			case "double_newline":
				data = []byte(workerTokenCanary + "\n\n")
			case "crlf":
				data = []byte(workerTokenCanary + "\r\n")
			case "space":
				data = []byte(" " + workerTokenCanary)
			case "nul":
				data = append([]byte(workerTokenCanary), 0)
			case "unicode":
				data = []byte("token-é")
			case "too_large":
				data = []byte(strings.Repeat("x", (16<<10)+1))
			case "max_size":
				data = []byte(strings.Repeat("x", 16<<10))
			}
			if kind == "directory" || kind == "fifo" || kind == "missing" {
				if err = os.Remove(tokenPath); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					err = os.Mkdir(tokenPath, 0700)
				}
				if kind == "fifo" {
					err = syscall.Mkfifo(tokenPath, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			} else if err = os.WriteFile(tokenPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			got, err := c.Resolve(context.Background(), integration)
			if kind == "max_size" {
				if err != nil || len(got) != 16<<10 {
					t.Fatalf("bounded maximum token rejected: size=%d err=%v", len(got), err)
				}
			} else {
				assertCredentialFailure(t, err)
				if got != "" {
					t.Fatal("invalid file yielded partial token")
				}
			}
			if time.Since(started) > time.Second {
				t.Fatal("token file type was not checked promptly")
			}
		})
	}
}

func TestCredentialResolverCancellationDoesNotOpenFiles(t *testing.T) {
	path, tokenPath, _, integration := credentialFixture(t)
	c, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := c.Resolve(ctx, integration)
	if got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("credential cancellation=%v", err)
	}
}

func TestCredentialCatalogSupportsGitLabNumericProject(t *testing.T) {
	path, _, entry, integration := credentialFixture(t)
	entry.Provider, entry.Host = "gitlab", "gitlab.com"
	writeCredentialCatalog(t, path, []CredentialFile{entry})
	integration.Source.Provider, integration.Source.Host, integration.Source.Profile, integration.Source.Locator = "gitlab", "gitlab.com", "gitlab-rest/v4-19.3", ""
	c, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Resolve(context.Background(), integration)
	if err != nil || got != workerTokenCanary {
		t.Fatalf("GitLab credential=%q %v", got, err)
	}
}
