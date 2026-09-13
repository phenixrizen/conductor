package domain

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	GitHubDeliveryProfile = "github-delivery/2026-03-10"
	GitLabDeliveryProfile = "gitlab-delivery/v4-19.3"
)

// DeliveryInput names immutable implementation evidence, never caller-supplied
// patches. A separate human authorization binds the resulting proposal digest.
type DeliveryInput struct {
	RunID          string `json:"runId"`
	TaskID         string `json:"taskId"`
	ArtifactDigest string `json:"artifactDigest"`
	BaseBranch     string `json:"baseBranch"`
	Title          string `json:"title"`
	Description    string `json:"description"`
}
type DeliveryAuthorization struct {
	Actor     string    `json:"actor"`
	Digest    string    `json:"digest"`
	CreatedAt time.Time `json:"createdAt"`
}
type DeliveryIntegrationConfig struct {
	WorkspaceID  string   `json:"workspaceId"`
	RepositoryID string   `json:"repositoryId"`
	Profile      string   `json:"profile"`
	Locator      string   `json:"locator"`
	CredentialID string   `json:"credentialId"`
	BaseBranches []string `json:"baseBranches"`
	Enabled      bool     `json:"enabled"`
}
type DeliveryTarget struct {
	WorkspaceID        string `json:"workspaceId"`
	RepositoryID       string `json:"repositoryId"`
	Provider           string `json:"provider"`
	Host               string `json:"host"`
	ProviderID         string `json:"providerId"`
	Locator            string `json:"locator"`
	Profile            string `json:"profile"`
	IntegrationVersion int64  `json:"integrationVersion"`
}

// ProviderCheck reports an observation at an exact provider commit. No check
// rows, neutral or skipped results, and unavailable APIs cannot imply passing.
type ProviderCheck struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Commit string `json:"commit"`
	State  string `json:"state"`
}
type ProviderDeployment struct {
	ID                    string    `json:"id"`
	RepositoryID          string    `json:"repositoryId"`
	ProviderProfile       string    `json:"providerProfile"`
	Commit                string    `json:"commit"`
	CommitRelation        string    `json:"commitRelation"`
	Environment           string    `json:"environment"`
	EnvironmentID         string    `json:"environmentId,omitempty"`
	ProductionEnvironment *bool     `json:"productionEnvironment,omitempty"`
	State                 string    `json:"state"`
	ProviderState         string    `json:"providerState"`
	StatusID              string    `json:"statusId,omitempty"`
	StatusTruncated       bool      `json:"statusTruncated"`
	ProviderUpdatedAt     time.Time `json:"providerUpdatedAt"`
	ObservedAt            time.Time `json:"observedAt"`
}
type DeliveryObservationTrigger struct {
	Kind          string `json:"kind"`
	Key           string `json:"key,omitempty"`
	EventType     string `json:"eventType,omitempty"`
	PayloadDigest string `json:"payloadDigest,omitempty"`
}
type DeliveryObservation struct {
	Deployments          []ProviderDeployment        `json:"deployments"`
	DeploymentsTruncated bool                        `json:"deploymentsTruncated"`
	Trigger              *DeliveryObservationTrigger `json:"trigger,omitempty"`
	Sequence             int64                       `json:"sequence"`
	ProviderID           string                      `json:"providerId"`
	Number               int64                       `json:"number"`
	URL                  string                      `json:"url"`
	Commit               string                      `json:"commit"`
	Tree                 string                      `json:"tree"`
	State                string                      `json:"state"`
	Draft                bool                        `json:"draft"`
	MergeCommit          string                      `json:"mergeCommit,omitempty"`
	Checks               []ProviderCheck             `json:"checks"`
	ChecksTruncated      bool                        `json:"checksTruncated"`
	ChecksState          string                      `json:"checksState"`
	Deployment           string                      `json:"deployment"`
	ProductionOutcome    string                      `json:"productionOutcome"`
	ObservedAt           time.Time                   `json:"observedAt"`
}
type DeliveryReceipt struct {
	Digest      string              `json:"digest"`
	Observation DeliveryObservation `json:"observation"`
	CreatedAt   time.Time           `json:"createdAt"`
}
type Delivery struct {
	Receipt       *DeliveryReceipt       `json:"receipt,omitempty"`
	ID            string                 `json:"id"`
	WorkspaceID   string                 `json:"workspaceId"`
	RepositoryID  string                 `json:"repositoryId"`
	ProposerID    string                 `json:"proposerId"`
	Input         DeliveryInput          `json:"input"`
	Digest        string                 `json:"digest"`
	Target        DeliveryTarget         `json:"target"`
	BaseCommit    string                 `json:"baseCommit"`
	BaseTree      string                 `json:"baseTree"`
	ResultTree    string                 `json:"resultTree"`
	PatchDigest   string                 `json:"patchDigest"`
	Branch        string                 `json:"branch"`
	CreatedAt     time.Time              `json:"createdAt"`
	Authorization *DeliveryAuthorization `json:"authorization,omitempty"`
	Observation   *DeliveryObservation   `json:"observation,omitempty"`
	Execution     *CollectionExecution   `json:"execution,omitempty"`
}
type DeliverySummary struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspaceId"`
	RepositoryID   string    `json:"repositoryId"`
	RunID          string    `json:"runId"`
	TaskID         string    `json:"taskId"`
	ArtifactDigest string    `json:"artifactDigest"`
	Digest         string    `json:"digest"`
	Title          string    `json:"title"`
	Branch         string    `json:"branch"`
	CreatedAt      time.Time `json:"createdAt"`
	Authorized     bool      `json:"authorized"`
	ReceiptDigest  string    `json:"receiptDigest,omitempty"`
	State          string    `json:"state"`
}
type DeliveryPage struct {
	Deliveries []DeliverySummary `json:"deliveries"`
	NextBefore string            `json:"nextBefore,omitempty"`
}

func ValidDeliveryBranch(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " ~^:?*[\\") || strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}
func ValidateDeliveryInput(input DeliveryInput) error {
	if !IsLowerHex(input.RunID, 32) || !IsLowerHex(input.TaskID, 32) || !IsLowerHex(input.ArtifactDigest, 64) || !ValidDeliveryBranch(input.BaseBranch) || !validAccessText(input.Title, 240) || len(input.Description) > 16<<10 || !utf8.ValidString(input.Description) || strings.ContainsRune(input.Description, 0) {
		return ErrInvalidInput
	}
	return nil
}
func ValidateDeliveryIntegration(config DeliveryIntegrationConfig) error {
	if ValidateAccessID(config.WorkspaceID) != nil || ValidateAccessID(config.RepositoryID) != nil || ValidateAccessID(config.CredentialID) != nil || len(config.BaseBranches) < 1 || len(config.BaseBranches) > 8 {
		return ErrInvalidInput
	}
	branches := map[string]bool{}
	for _, branch := range config.BaseBranches {
		if !ValidDeliveryBranch(branch) || branches[branch] {
			return ErrInvalidInput
		}
		branches[branch] = true
	}
	switch config.Profile {
	case GitHubDeliveryProfile:
		parts := strings.Split(config.Locator, "/")
		if len(parts) != 2 {
			return ErrInvalidInput
		}
		for _, part := range parts {
			if part == "" || part == "." || part == ".." || len(part) > 100 || strings.ContainsFunc(part, func(r rune) bool {
				return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_')
			}) {
				return ErrInvalidInput
			}
		}
	case GitLabDeliveryProfile:
		if config.Locator != "" {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

// ValidateDeliveryObservation protects the durable evidence boundary. Provider
// state may establish a merge, but never an unobserved deployment or outcome.
func ValidateDeliveryObservation(d Delivery, o DeliveryObservation) error {
	if o.Number < 1 || o.ProviderID == "" || len(o.ProviderID) > 128 || !IsLowerHex(o.Commit, 40) || o.Tree != d.ResultTree || o.ObservedAt.IsZero() || len(o.Checks) > 200 || len(o.Deployments) > 100 || o.ProductionOutcome != "not_observed" {
		return ErrInvalidInput
	}
	switch o.State {
	case "draft":
		if !o.Draft {
			return ErrInvalidInput
		}
	case "open", "closed":
	case "merged":
		if !IsLowerHex(o.MergeCommit, 40) {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	if o.Deployment == "not_observed" && len(o.Deployments) > 0 || o.Deployment == "observed" && (len(o.Deployments) == 0 || o.DeploymentsTruncated) || o.Deployment == "truncated" && !o.DeploymentsTruncated {
		return ErrInvalidInput
	}
	switch o.Deployment {
	case "not_observed", "observed", "truncated", "unavailable":
	default:
		return ErrInvalidInput
	}
	for _, deployment := range o.Deployments {
		if deployment.ID == "" || len(deployment.ID) > 128 || deployment.RepositoryID != d.RepositoryID || deployment.ProviderProfile != d.Target.Profile || deployment.Environment == "" || len(deployment.Environment) > 256 || len(deployment.ProviderState) > 64 || deployment.ProviderUpdatedAt.IsZero() || deployment.ObservedAt.IsZero() {
			return ErrInvalidInput
		}
		if !(deployment.CommitRelation == "published_head" && deployment.Commit == o.Commit || deployment.CommitRelation == "provider_merge" && o.State == "merged" && deployment.Commit == o.MergeCommit) {
			return ErrInvalidInput
		}
		switch deployment.State {
		case "success", "failed", "pending", "inactive", "unknown":
		default:
			return ErrInvalidInput
		}
		if deployment.StatusTruncated && deployment.State != "unknown" {
			return ErrInvalidInput
		}
	}
	switch o.ChecksState {
	case "unknown", "passed", "failed", "pending", "unavailable":
	default:
		return ErrInvalidInput
	}
	if o.ChecksState == "passed" && (len(o.Checks) == 0 || o.ChecksTruncated) {
		return ErrInvalidInput
	}
	for _, check := range o.Checks {
		if check.Commit != o.Commit || len(check.ID) > 256 || len(check.Name) > 512 {
			return ErrInvalidInput
		}
		switch check.State {
		case "passed", "failed", "pending", "unexecuted", "unknown":
		default:
			return ErrInvalidInput
		}
		if o.ChecksState == "passed" && check.State != "passed" {
			return ErrInvalidInput
		}
	}
	return nil
}

// DeliveryArtifact carries exact persisted implementation evidence for human inspection.
type DeliveryArtifact struct {
	DeliveryID     string          `json:"deliveryId"`
	DeliveryDigest string          `json:"deliveryDigest"`
	ArtifactDigest string          `json:"artifactDigest"`
	Artifact       json.RawMessage `json:"artifact"`
}

// DeliveryProposalDigest excludes later authorization, receipts, observations and
// database timestamps. Its explicit shape keeps future presentation fields from
// silently changing the meaning of a person's exact publication authorization.
func DeliveryProposalDigest(d Delivery) (string, error) {
	return JSONDigest(struct {
		ID           string         `json:"id"`
		WorkspaceID  string         `json:"workspaceId"`
		RepositoryID string         `json:"repositoryId"`
		ProposerID   string         `json:"proposerId"`
		Input        DeliveryInput  `json:"input"`
		Target       DeliveryTarget `json:"target"`
		BaseCommit   string         `json:"baseCommit"`
		BaseTree     string         `json:"baseTree"`
		ResultTree   string         `json:"resultTree"`
		PatchDigest  string         `json:"patchDigest"`
		Branch       string         `json:"branch"`
	}{d.ID, d.WorkspaceID, d.RepositoryID, d.ProposerID, d.Input, d.Target, d.BaseCommit, d.BaseTree, d.ResultTree, d.PatchDigest, d.Branch})
}
