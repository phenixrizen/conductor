package domain

import (
	"errors"
	"time"
)

const (
	DefaultHistoryPageSize = 20
	MaxHistoryPageSize     = 100
	MaxHistoricalApprovals = 100
)

var ErrUnavailable = errors.New("requested service is unavailable")

// RevisionSummary reports retained approval facts, not effective approval of the
// current package. Historical inspection never grants approval to newer content.
type RevisionSummary struct {
	Number        int64      `json:"number"`
	Digest        string     `json:"digest"`
	Author        string     `json:"author"`
	CreatedAt     time.Time  `json:"createdAt"`
	SubmittedAt   *time.Time `json:"submittedAt,omitempty"`
	ApprovalCount int64      `json:"approvalCount"`
}

type HistoryPage struct {
	Revisions          []RevisionSummary `json:"revisions"`
	NextBeforeRevision int64             `json:"nextBeforeRevision,omitempty"`
}

type RevisionRecord struct {
	Revision           Revision   `json:"revision"`
	Approvals          []Approval `json:"approvals"`
	ApprovalsTruncated bool       `json:"approvalsTruncated"`
}

type AuditEvent struct {
	Sequence  int64          `json:"sequence"`
	ChangeID  string         `json:"changeId"`
	EventType string         `json:"eventType"`
	Actor     string         `json:"actor"`
	Revision  int64          `json:"revision"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"createdAt"`
}

type AuditPage struct {
	Events            []AuditEvent `json:"events"`
	NextAfterSequence int64        `json:"nextAfterSequence,omitempty"`
}

func ValidateHistoryQuery(id string, cursor int64, limit int) error {
	if id == "" || cursor < 0 || limit < 1 || limit > MaxHistoryPageSize {
		return ErrInvalidInput
	}
	return nil
}
