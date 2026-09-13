package domain

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Public profile configuration contains no credential references or paths. The
// trusted worker keeps its separate secret catalog and must match these exact
// reviewed fields and image before executing a task.
type WorkerProfile struct {
	Adapter      string   `json:"adapter"`
	Model        string   `json:"model,omitempty"`
	Command      []string `json:"command,omitempty"`
	MaxBudgetUSD string   `json:"maxBudgetUsd,omitempty"`
}
type ExecutionProfileConfig struct {
	WorkspaceID string        `json:"workspaceId"`
	ID          string        `json:"id"`
	Image       string        `json:"image"`
	Profile     WorkerProfile `json:"profile"`
	Enabled     bool          `json:"enabled"`
}
type ExecutionProfile struct {
	ID            string `json:"id"`
	ProfileDigest string `json:"profileDigest"`
	Image         string `json:"image"`
	WorkerProfile
}
type ExecutionProfilePage struct {
	Profiles  []ExecutionProfile `json:"profiles"`
	Truncated bool               `json:"truncated"`
}

func ValidateExecutionProfileConfig(c ExecutionProfileConfig) error {
	if ValidateAccessID(c.WorkspaceID) != nil || !planKey(c.ID) || len(c.Image) > 512 || !utf8.ValidString(c.Image) {
		return ErrInvalidInput
	}
	imageParts := strings.Split(c.Image, "@sha256:")
	imageOK := len(imageParts) == 2 && imageParts[0] != "" && IsLowerHex(imageParts[1], 64) && !strings.ContainsAny(imageParts[0], " \r\n\t")
	imageOK = imageOK || strings.HasPrefix(c.Image, "sha256:") && IsLowerHex(strings.TrimPrefix(c.Image, "sha256:"), 64)
	if !imageOK || len(c.Profile.Adapter) < 1 || len(c.Profile.Adapter) > 64 || len(c.Profile.Model) > 128 || len(c.Profile.Command) > 64 || len(c.Profile.MaxBudgetUSD) > 8 {
		return ErrInvalidInput
	}
	for _, value := range []string{c.Profile.Adapter, c.Profile.Model, c.Profile.MaxBudgetUSD} {
		if len(value) > 16384 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return ErrInvalidInput
		}
	}
	for _, arg := range c.Profile.Command {
		if len(arg) > 16384 || !utf8.ValidString(arg) || strings.ContainsRune(arg, 0) {
			return ErrInvalidInput
		}
	}
	encoded, err := json.Marshal(c)
	if err != nil || len(encoded) > 32<<10 {
		return ErrInvalidInput
	}
	return nil
}
