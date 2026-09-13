package domain

import "encoding/json"

// CoordinationArtifactQuery binds the exact retained receipt the user inspected.
// A failed task or a report with no patch remains useful shared evidence.
type CoordinationArtifactQuery struct {
	RunDigest      string `json:"runDigest"`
	TaskID         string `json:"taskId"`
	ArtifactDigest string `json:"artifactDigest"`
}
type CoordinationArtifact struct {
	RunID          string              `json:"runId"`
	RunDigest      string              `json:"runDigest"`
	TaskID         string              `json:"taskId"`
	ArtifactDigest string              `json:"artifactDigest"`
	Artifact       json.RawMessage     `json:"artifact"`
	Verification   *VerificationReview `json:"verification,omitempty"`
}

func ValidateCoordinationArtifactQuery(q CoordinationArtifactQuery) error {
	if !IsLowerHex(q.RunDigest, 64) || !IsLowerHex(q.TaskID, 32) || !IsLowerHex(q.ArtifactDigest, 64) {
		return ErrInvalidInput
	}
	return nil
}
