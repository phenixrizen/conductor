package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	RepositoryContextCollector = "conductor-git/v1"
	MaxContextArtifacts        = 32
	MaxContextArtifactBytes    = 64 << 10
	MaxContextTotalBytes       = 256 << 10
)

// RepositoryContext is a snapshot of explicitly selected files at one Git commit.
// Its provenance describes collection, not architectural acceptance or test results.
type RepositoryContext struct {
	SchemaVersion int               `json:"schemaVersion"`
	Repository    string            `json:"repository"`
	Commit        string            `json:"commit"`
	RequestedRef  string            `json:"requestedRef"`
	CollectedAt   time.Time         `json:"collectedAt"`
	Collector     string            `json:"collector"`
	Artifacts     []ContextArtifact `json:"artifacts"`
}

type ContextArtifact struct {
	Path    string  `json:"path"`
	State   string  `json:"state"`
	BlobOID string  `json:"blobOID,omitempty"`
	Digest  string  `json:"digest,omitempty"`
	Text    *string `json:"text,omitempty"`
	Message string  `json:"message,omitempty"`
}

// ValidateRepositoryContext checks the reserved content field without rewriting
// it, so extension fields survive a package round trip.
func ValidateRepositoryContext(value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: encode repository context: %v", ErrInvalidInput, err)
	}
	var snapshot RepositoryContext
	if err := json.Unmarshal(b, &snapshot); err != nil {
		return fmt.Errorf("%w: invalid repository context", ErrInvalidInput)
	}
	invalid := func(message string) error {
		return fmt.Errorf("%w: repository context %s", ErrInvalidInput, message)
	}
	if snapshot.SchemaVersion != 1 || snapshot.Collector != RepositoryContextCollector {
		return invalid("requires schemaVersion 1 and collector conductor-git/v1")
	}
	if !validContextLabel(snapshot.Repository, 1024) || !validContextLabel(snapshot.RequestedRef, 1024) || strings.HasPrefix(snapshot.RequestedRef, "-") {
		return invalid("requires a repository identity and requested ref")
	}
	if !ValidGitOID(snapshot.Commit) {
		return invalid("requires a full commit object ID")
	}
	_, offset := snapshot.CollectedAt.Zone()
	if snapshot.CollectedAt.IsZero() || offset != 0 {
		return invalid("requires a UTC collection timestamp")
	}
	if len(snapshot.Artifacts) == 0 || len(snapshot.Artifacts) > MaxContextArtifacts {
		return invalid("requires 1 to 32 explicit artifact paths")
	}
	seen := make(map[string]bool)
	total := 0
	for _, artifact := range snapshot.Artifacts {
		if err := ValidateContextPath(artifact.Path); err != nil {
			return err
		}
		if seen[artifact.Path] {
			return invalid("contains duplicate artifact paths")
		}
		seen[artifact.Path] = true
		if artifact.BlobOID != "" && !ValidGitOID(artifact.BlobOID) {
			return invalid("contains an invalid blob object ID")
		}
		if len(artifact.Message) > 1024 {
			return invalid("artifact message exceeds 1024 bytes")
		}
		switch artifact.State {
		case "collected":
			if artifact.BlobOID == "" || artifact.Text == nil || len(*artifact.Text) > MaxContextArtifactBytes || !IsContextText([]byte(*artifact.Text)) {
				return invalid("collected artifacts require a blob ID and complete UTF-8 text of at most 64 KiB")
			}
			digest := sha256.Sum256([]byte(*artifact.Text))
			if artifact.Digest != hex.EncodeToString(digest[:]) {
				return invalid("artifact digest does not match its text")
			}
			total += len(*artifact.Text)
		case "missing", "unavailable", "truncated":
			if artifact.Text != nil || artifact.Digest != "" || strings.TrimSpace(artifact.Message) == "" {
				return invalid("uncollected artifacts require a reason and must omit text and digest")
			}
			if artifact.State == "missing" && artifact.BlobOID != "" {
				return invalid("missing artifacts must omit the blob ID")
			}
			if artifact.State == "truncated" && artifact.BlobOID == "" {
				return invalid("truncated artifacts require a blob ID")
			}
		default:
			return invalid("contains an unknown artifact state")
		}
	}
	if total > MaxContextTotalBytes {
		return invalid("collected text exceeds 256 KiB")
	}
	return nil
}

func ValidateContextPath(value string) error {
	if !validContextLabel(value, 1024) || value == "." || strings.HasPrefix(value, "-") || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("%w: context paths must be clean relative file paths", ErrInvalidInput)
	}
	return nil
}

func ValidGitOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// IsContextText rejects invalid UTF-8 and binary control bytes. Newlines, carriage
// returns and tabs are retained as original bytes for digest verification.
func IsContextText(value []byte) bool {
	if !utf8.Valid(value) {
		return false
	}
	for _, b := range value {
		if (b < 32 && b != '\n' && b != '\r' && b != '\t') || b == 127 {
			return false
		}
	}
	return true
}

func validContextLabel(value string, max int) bool {
	if strings.TrimSpace(value) == "" || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, b := range []byte(value) {
		if b < 32 || b == 127 {
			return false
		}
	}
	return true
}
