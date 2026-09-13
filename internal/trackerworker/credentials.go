package trackerworker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/tracker"
)

type CredentialFile struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspaceId"`
	Provider       string `json:"provider"`
	Host           string `json:"host"`
	OrganizationID string `json:"organizationId,omitempty"`
	ScopeID        string `json:"scopeId"`
	SecretFile     string `json:"secretFile"`
}
type CredentialFiles struct{ files map[string]CredentialFile }

func secretFile(path string, max int64) ([]byte, error) {
	if !filepath.IsAbs(path) || len(path) > 4096 {
		return nil, domain.ErrInvalidInput
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, domain.ErrUnavailable
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > max {
		return nil, domain.ErrUnavailable
	}
	data, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(data)) > max {
		return nil, domain.ErrUnavailable
	}
	return data, nil
}
func LoadCredentials(path string) (*CredentialFiles, error) {
	b, e := secretFile(path, 1<<20)
	if e != nil {
		return nil, e
	}
	var data struct {
		Credentials []CredentialFile `json:"credentials"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&data) != nil || d.Decode(new(any)) != io.EOF || len(data.Credentials) == 0 || len(data.Credentials) > 100 {
		return nil, domain.ErrInvalidInput
	}
	result := &CredentialFiles{map[string]CredentialFile{}}
	for _, c := range data.Credentials {
		if domain.ValidateAccessID(c.ID) != nil || domain.ValidateAccessID(c.WorkspaceID) != nil || !filepath.IsAbs(c.SecretFile) || !domain.TrackerRecordID(c.Provider, c.ScopeID) {
			return nil, domain.ErrInvalidInput
		}
		if _, ok := result.files[c.ID]; ok {
			return nil, domain.ErrInvalidInput
		}
		result.files[c.ID] = c
	}
	return result, nil
}
func (c *CredentialFiles) Resolve(ctx context.Context, config domain.TrackerConfig, id string) (tracker.Credential, error) {
	var key tracker.Credential
	if e := ctx.Err(); e != nil {
		return key, e
	}
	ref, ok := c.files[id]
	if !ok || ref.WorkspaceID != config.WorkspaceID || ref.Provider != config.Provider || ref.Host != config.Host || ref.OrganizationID != config.OrganizationID || ref.ScopeID != config.ScopeID || id != config.CredentialID && id != config.WebhookCredentialID {
		return key, domain.ErrForbidden
	}
	data, e := secretFile(ref.SecretFile, 16<<10)
	if e != nil {
		return key, e
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&key) != nil || d.Decode(new(any)) != io.EOF || key.Token == "" {
		return key, domain.ErrInvalidInput
	}
	return key, nil
}
