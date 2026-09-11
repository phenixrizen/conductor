package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrConflict      = errors.New("revision conflict")
	ErrStaleApproval = errors.New("approval does not match current revision")
	ErrSelfApproval  = errors.New("authors cannot approve their own revision")
	ErrNotSubmitted  = errors.New("revision is not submitted for review")
)

type Content map[string]any

func Digest(content Content) (string, error) {
	b, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("canonicalize content: %w", err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), nil
}

type Revision struct {
	ChangeID      string     `json:"changeId"`
	Number        int64      `json:"number"`
	SchemaVersion int        `json:"schemaVersion"`
	Digest        string     `json:"digest"`
	Content       Content    `json:"content"`
	Author        string     `json:"author"`
	CreatedAt     time.Time  `json:"createdAt"`
	SubmittedAt   *time.Time `json:"submittedAt,omitempty"`
}

type Approval struct {
	ChangeID  string    `json:"changeId"`
	Revision  int64     `json:"revision"`
	Digest    string    `json:"digest"`
	Reviewer  string    `json:"reviewer"`
	CreatedAt time.Time `json:"createdAt"`
}

type Package struct {
	ID       string    `json:"id"`
	Revision Revision  `json:"revision"`
	Approval *Approval `json:"approval,omitempty"`
	Approved bool      `json:"approved"`
}

func ValidateApproval(r Revision, revision int64, digest, reviewer string) error {
	if r.Number != revision || r.Digest != digest {
		return ErrStaleApproval
	}
	if r.SubmittedAt == nil {
		return ErrNotSubmitted
	}
	if r.Author == reviewer {
		return ErrSelfApproval
	}
	return nil
}
