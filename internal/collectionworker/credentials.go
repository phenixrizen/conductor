package collectionworker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
)

type CredentialFile struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspaceId"`
	RepositoryID string `json:"repositoryId"`
	Provider     string `json:"provider"`
	Host         string `json:"host"`
	ProviderID   string `json:"providerId"`
	TokenFile    string `json:"tokenFile"`
}

type Credentials struct{ files map[string]CredentialFile }

// LoadCredentials loads references only. Token files are reread for each new
// activity attempt so an operator can rotate secret material under one binding.
// This file and all referenced paths must be controlled by the worker operator.
func LoadCredentials(path string) (*Credentials, error) {
	data, err := boundedFile(path, 1<<20)
	if err != nil {
		return nil, err
	}
	var config struct {
		Credentials []CredentialFile `json:"credentials"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || len(config.Credentials) == 0 || len(config.Credentials) > 100 {
		return nil, contextworkflow.ErrInvalidConfiguration
	}
	files := make(map[string]CredentialFile, len(config.Credentials))
	for _, entry := range config.Credentials {
		if domain.ValidateAccessID(entry.ID) != nil || domain.ValidateAccessID(entry.WorkspaceID) != nil || domain.ValidateAccessID(entry.RepositoryID) != nil || !filepath.IsAbs(entry.TokenFile) || len(entry.TokenFile) > 4096 {
			return nil, contextworkflow.ErrInvalidConfiguration
		}
		// Use the same canonical provider boundary as operator provisioning.
		profile, locator := "github-rest/2026-03-10", "synthetic/check"
		if entry.Provider == "gitlab" {
			profile, locator = "gitlab-rest/v4-19.3", ""
		}
		source := domain.ContextSource{WorkspaceID: entry.WorkspaceID, RepositoryID: entry.RepositoryID, Provider: entry.Provider, Host: entry.Host, ProviderID: entry.ProviderID, Profile: profile, Locator: locator, IntegrationVersion: 1}
		if domain.ValidateContextSource(source) != nil {
			return nil, contextworkflow.ErrInvalidConfiguration
		}
		if _, duplicate := files[entry.ID]; duplicate {
			return nil, contextworkflow.ErrInvalidConfiguration
		}
		files[entry.ID] = entry
	}
	return &Credentials{files: files}, nil
}

func (c *Credentials) Resolve(ctx context.Context, integration domain.ContextIntegration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	entry, ok := c.files[integration.CredentialID]
	s := integration.Source
	if !ok || entry.WorkspaceID != s.WorkspaceID || entry.RepositoryID != s.RepositoryID || entry.Provider != s.Provider || entry.Host != s.Host || entry.ProviderID != s.ProviderID {
		return "", contextworkflow.ErrInvalidConfiguration
	}
	data, err := boundedFile(entry.TokenFile, 16<<10)
	if err != nil {
		return "", err
	}
	token := strings.TrimSuffix(string(data), "\n")
	if token == "" {
		return "", contextworkflow.ErrInvalidConfiguration
	}
	for _, ch := range token {
		if ch < 33 || ch > 126 {
			return "", contextworkflow.ErrInvalidConfiguration
		}
	}
	return token, nil
}

func boundedFile(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, contextworkflow.ErrInvalidConfiguration
	}
	// Nonblocking open prevents a substituted FIFO from hanging startup/activity.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, contextworkflow.ErrInvalidConfiguration
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, contextworkflow.ErrInvalidConfiguration
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, contextworkflow.ErrInvalidConfiguration
	}
	return data, nil
}
