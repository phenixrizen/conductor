package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key already identifies different command input")
	ErrCollectionStopped   = errors.New("collection no longer permits new work")
	ErrCapacity            = errors.New("collection capacity is exhausted")
)

const RemoteContextCollector = "conductor-remote/v1"

type CollectionInput struct {
	Commit     string   `json:"commit"`
	Paths      []string `json:"paths"`
	FullSource bool     `json:"fullSource,omitempty"`
}

type ContextIntegrationConfig struct {
	RepositoryID string `json:"repositoryId"`
	WorkspaceID  string `json:"workspaceId"`
	Profile      string `json:"profile"`
	Locator      string `json:"locator"`
	CredentialID string `json:"credentialId"`
	Enabled      bool   `json:"enabled"`
}

// Source records the operator-selected canonical source, never a token or secret
// file path. Its version is provenance, not authority to reuse a revoked grant.
type ContextSource struct {
	WorkspaceID        string `json:"workspaceId"`
	RepositoryID       string `json:"repositoryId"`
	Provider           string `json:"provider"`
	Host               string `json:"host"`
	ProviderID         string `json:"providerId"`
	Locator            string `json:"locator"`
	Profile            string `json:"profile"`
	IntegrationVersion int64  `json:"integrationVersion"`
}

type ContextIntegration struct {
	Source       ContextSource `json:"source"`
	CredentialID string        `json:"credentialId"`
	Enabled      bool          `json:"enabled"`
}

type CollectionReceipt struct {
	ID        string            `json:"id"`
	Digest    string            `json:"digest"`
	Snapshot  RepositoryContext `json:"snapshot"`
	CreatedAt time.Time         `json:"createdAt"`
}

// Execution is an observation of Temporal, not a database execution authority.
// Current is derived when read; old observations never become live progress.
type CollectionExecution struct {
	Namespace  string    `json:"namespace"`
	WorkflowID string    `json:"workflowId"`
	RunID      string    `json:"runId"`
	State      string    `json:"state"`
	ObservedAt time.Time `json:"observedAt"`
	Current    bool      `json:"current"`
}

type Collection struct {
	ID                string               `json:"id"`
	WorkspaceID       string               `json:"workspaceId"`
	RepositoryID      string               `json:"repositoryId"`
	RequesterID       string               `json:"requesterId"`
	Input             CollectionInput      `json:"input"`
	InputDigest       string               `json:"inputDigest"`
	Source            ContextSource        `json:"source"`
	CreatedAt         time.Time            `json:"createdAt"`
	CancelRequestedAt *time.Time           `json:"cancelRequestedAt,omitempty"`
	FullSource        *SourceBundleSummary `json:"fullSource,omitempty"`
	Receipt           *CollectionReceipt   `json:"receipt,omitempty"`
	Execution         *CollectionExecution `json:"execution,omitempty"`
}

type CollectionSummary struct {
	ID                string               `json:"id"`
	WorkspaceID       string               `json:"workspaceId"`
	RepositoryID      string               `json:"repositoryId"`
	RequesterID       string               `json:"requesterId"`
	Commit            string               `json:"commit"`
	CreatedAt         time.Time            `json:"createdAt"`
	CancelRequestedAt *time.Time           `json:"cancelRequestedAt,omitempty"`
	ReceiptDigest     string               `json:"receiptDigest,omitempty"`
	Execution         *CollectionExecution `json:"execution,omitempty"`
}
type CollectionPage struct {
	Collections []CollectionSummary `json:"collections"`
	NextBefore  string              `json:"nextBefore,omitempty"`
}

func IsLowerHex(value string, size int) bool {
	if len(value) != size || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func NormalizeCollectionInput(in CollectionInput) (CollectionInput, error) {
	if !IsLowerHex(in.Commit, 40) || len(in.Paths) < 1 || len(in.Paths) > MaxContextArtifacts {
		return CollectionInput{}, ErrInvalidInput
	}
	in.Paths = append([]string(nil), in.Paths...)
	sort.Strings(in.Paths)
	for i, path := range in.Paths {
		if ValidateContextPath(path) != nil || (i > 0 && in.Paths[i-1] == path) {
			return CollectionInput{}, ErrInvalidInput
		}
	}
	return in, nil
}

func ValidateCollectionKey(key string) error {
	if len(key) < 1 || len(key) > 128 {
		return ErrInvalidInput
	}
	for _, r := range key {
		if r < 33 || r > 126 || r == ',' {
			return ErrInvalidInput
		}
	}
	return nil
}

func JSONDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode digest input: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ValidateContextIntegrationConfig(c ContextIntegrationConfig) error {
	if ValidateAccessID(c.RepositoryID) != nil || ValidateAccessID(c.WorkspaceID) != nil || ValidateAccessID(c.CredentialID) != nil {
		return ErrInvalidInput
	}
	switch c.Profile {
	case "github-rest/2026-03-10":
		parts := strings.Split(c.Locator, "/")
		if len(parts) != 2 {
			return ErrInvalidInput
		}
		for _, part := range parts {
			if len(part) < 1 || len(part) > 100 || part == "." || part == ".." {
				return ErrInvalidInput
			}
			for _, r := range part {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
					return ErrInvalidInput
				}
			}
		}
	case "gitlab-rest/v4-19.3":
		if c.Locator != "" {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func ValidateContextSource(s ContextSource) error {
	if s.IntegrationVersion < 1 {
		return ErrInvalidInput
	}
	if err := ValidateContextIntegrationConfig(ContextIntegrationConfig{WorkspaceID: s.WorkspaceID, RepositoryID: s.RepositoryID, Profile: s.Profile, Locator: s.Locator, CredentialID: "validation"}); err != nil {
		return err
	}
	id, err := strconv.ParseUint(s.ProviderID, 10, 63)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != s.ProviderID {
		return ErrInvalidInput
	}
	if s.Provider == "github" && s.Host == "github.com" && s.Profile == "github-rest/2026-03-10" {
		return nil
	}
	if s.Provider == "gitlab" && s.Host == "gitlab.com" && s.Profile == "gitlab-rest/v4-19.3" {
		return nil
	}
	return ErrInvalidInput
}

// Enabling/disabling and secret rotation preserve binding identity. Repointing a
// locator, credential reference, or API profile requires a new explicit request.
func ContextBindingDigest(in ContextIntegration) (string, error) {
	in.Enabled = false
	in.Source.IntegrationVersion = 0
	return JSONDigest(in)
}

func ValidateCollectionReceipt(receipt CollectionReceipt, collection Collection) error {
	if receipt.Snapshot.SchemaVersion != 2 || receipt.Snapshot.Collector != RemoteContextCollector || receipt.ID != collection.ID || !IsLowerHex(receipt.Digest, 64) || receipt.Snapshot.CollectionID != collection.ID || receipt.Snapshot.Source == nil || *receipt.Snapshot.Source != collection.Source || receipt.Snapshot.Commit != collection.Input.Commit || len(receipt.Snapshot.Artifacts) != len(collection.Input.Paths) {
		return ErrInvalidInput
	}
	if err := ValidateRepositoryContext(receipt.Snapshot); err != nil {
		return err
	}
	for i, artifact := range receipt.Snapshot.Artifacts {
		if artifact.Path != collection.Input.Paths[i] {
			return ErrInvalidInput
		}
	}
	digest, err := JSONDigest(receipt.Snapshot)
	if err != nil || digest != receipt.Digest {
		return ErrInvalidInput
	}
	return nil
}
