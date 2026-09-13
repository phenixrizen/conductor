package coordinationworker

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

// ProfileConfig is trusted operator configuration. Only model credentials are
// referenced here; publication and production credentials belong to other services.
type ProfileConfig struct {
	WorkspaceID          string            `json:"workspaceId"`
	ID                   string            `json:"id"`
	Image                string            `json:"image"`
	Profile              execution.Profile `json:"profile"`
	CredentialFile       string            `json:"credentialFile,omitempty"`
	AllowProviderNetwork bool              `json:"allowProviderNetwork"`
}

func ValidateProfiles(profiles []ProfileConfig) error {
	if len(profiles) < 1 || len(profiles) > 100 {
		return domain.ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, p := range profiles {
		key := p.WorkspaceID + "\x00" + p.ID
		if domain.ValidateAccessID(p.WorkspaceID) != nil || domain.ValidateAccessID(p.ID) != nil || seen[key] || !domain.ExecutionImagePinned(p.Image) || p.Profile.Validate() != nil {
			return domain.ErrInvalidInput
		}
		seen[key] = true
		if p.Profile.Adapter == "command/v1" {
			if p.CredentialFile != "" || p.AllowProviderNetwork {
				return domain.ErrInvalidInput
			}
		} else if !p.AllowProviderNetwork || !filepath.IsAbs(p.CredentialFile) {
			return domain.ErrInvalidInput
		}
	}
	return nil
}
func ReadProfiles(name string) ([]ProfileConfig, error) {
	if !filepath.IsAbs(name) {
		return nil, domain.ErrInvalidInput
	}
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, domain.ErrUnavailable
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > 256<<10 {
		return nil, domain.ErrInvalidInput
	}
	data, err := io.ReadAll(io.LimitReader(f, (256<<10)+1))
	if err != nil || len(data) > 256<<10 {
		return nil, domain.ErrInvalidInput
	}
	var envelope struct {
		Profiles []ProfileConfig `json:"profiles"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || decoder.Decode(new(any)) != io.EOF || ValidateProfiles(envelope.Profiles) != nil {
		return nil, domain.ErrInvalidInput
	}
	return envelope.Profiles, nil
}
