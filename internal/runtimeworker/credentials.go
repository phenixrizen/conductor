package runtimeworker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/phenixrizen/conductor/internal/domain"
)

type Credential struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspaceId"`
	RepositoryID string `json:"repositoryId"`
	BackendID    string `json:"backendId"`
	TokenFile    string `json:"tokenFile"`
}
type Credentials struct{ entries []Credential }

func ReadOperatorFile(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || len(path) > 4096 {
		return nil, domain.ErrInvalidInput
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, domain.ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, domain.ErrInvalidInput
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, domain.ErrUnavailable
	}
	return raw, nil
}
func LoadCredentials(path string) (*Credentials, error) {
	raw, err := ReadOperatorFile(path, 1<<20)
	if err != nil {
		return nil, err
	}
	var input struct {
		Credentials []Credential `json:"credentials"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF || len(input.Credentials) < 1 || len(input.Credentials) > 128 {
		return nil, domain.ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, c := range input.Credentials {
		if domain.ValidateAccessID(c.ID) != nil || domain.ValidateAccessID(c.WorkspaceID) != nil || domain.ValidateAccessID(c.RepositoryID) != nil || domain.ValidateAccessID(c.BackendID) != nil || !filepath.IsAbs(c.TokenFile) || len(c.TokenFile) > 4096 || seen[c.ID] {
			return nil, domain.ErrInvalidInput
		}
		seen[c.ID] = true
	}
	return &Credentials{entries: input.Credentials}, nil
}
func (c *Credentials) Resolve(ctx context.Context, target domain.RuntimeTarget, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, entry := range c.entries {
		if entry.ID != id {
			continue
		}
		if entry.WorkspaceID != target.WorkspaceID || entry.RepositoryID != target.RepositoryID || entry.BackendID != target.BackendID || target.Profile != domain.GroundcoverProfile {
			return "", domain.ErrForbidden
		}
		raw, err := ReadOperatorFile(entry.TokenFile, 8193)
		if err != nil {
			return "", err
		}
		token := strings.TrimSuffix(string(raw), "\n")
		if len(token) < 16 || len(token) > 8192 || strings.ContainsAny(token, "\r\n") {
			return "", domain.ErrInvalidInput
		}
		return token, nil
	}
	return "", domain.ErrUnavailable
}
