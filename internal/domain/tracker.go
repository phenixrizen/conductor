package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

const LinearTrackerProfile = "linear-graphql/23f11eb41ef63ba219ec582911079c19d1abbf62"
const JiraTrackerProfile = "jira-cloud-rest/v3-2026-09-13"

var trackerUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var trackerNumber = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var trackerHost = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}\.atlassian\.net$`)

// Tracker-owned planning fields are display data. None changes package approval,
// executed verification, repository merge facts, or publication authority.
type TrackerStatusMapping struct {
	ID      string `json:"id"`
	Display string `json:"display"`
	Ignore  bool   `json:"ignore"`
}
type TrackerGrant struct {
	PrincipalID string `json:"principalId"`
	CanRead     bool   `json:"canRead"`
	CanSync     bool   `json:"canSync"`
	CanResolve  bool   `json:"canResolve"`
}
type TrackerConfig struct {
	WorkspaceID         string                 `json:"workspaceId"`
	Provider            string                 `json:"provider"`
	Host                string                 `json:"host"`
	OrganizationID      string                 `json:"organizationId,omitempty"`
	ScopeID             string                 `json:"scopeId"`
	Profile             string                 `json:"profile"`
	CredentialID        string                 `json:"credentialId"`
	WebhookCredentialID string                 `json:"webhookCredentialId"`
	ConductorOrigin     string                 `json:"conductorOrigin"`
	Enabled             bool                   `json:"enabled"`
	Statuses            []TrackerStatusMapping `json:"statuses"`
	Grants              []TrackerGrant         `json:"grants"`
}
type TrackerSettings struct {
	WorkspaceID    string                 `json:"workspaceId"`
	Provider       string                 `json:"provider"`
	Host           string                 `json:"host"`
	OrganizationID string                 `json:"organizationId,omitempty"`
	ScopeID        string                 `json:"scopeId"`
	Profile        string                 `json:"profile"`
	Version        int64                  `json:"version"`
	Enabled        bool                   `json:"enabled"`
	Statuses       []TrackerStatusMapping `json:"statuses"`
	CanSync        bool                   `json:"canSync"`
	CanResolve     bool                   `json:"canResolve"`
}
type TrackerPackageRef struct {
	RepositoryID string `json:"repositoryId"`
	PackageID    string `json:"packageId"`
	Revision     int64  `json:"revision"`
	Digest       string `json:"digest"`
}
type TrackerPublicationRef struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}
type TrackerLinkInput struct {
	IssueID      string                  `json:"issueId"`
	Packages     []TrackerPackageRef     `json:"packages"`
	Publications []TrackerPublicationRef `json:"publications,omitempty"`
}
type TrackerProjection struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}
type TrackerIssue struct {
	ID           string          `json:"id"`
	Key          string          `json:"key"`
	URL          string          `json:"url"`
	Title        string          `json:"title"`
	Description  json.RawMessage `json:"description"`
	Priority     string          `json:"priority"`
	AssigneeID   string          `json:"assigneeId"`
	StatusID     string          `json:"statusId"`
	StatusName   string          `json:"statusName"`
	MappedStatus string          `json:"mappedStatus"`
	Mapping      string          `json:"mapping"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}
type TrackerObservation struct {
	SyncID           string             `json:"syncId"`
	State            string             `json:"state"`
	Code             string             `json:"code,omitempty"`
	Issue            *TrackerIssue      `json:"issue,omitempty"`
	Projection       *TrackerProjection `json:"projection,omitempty"`
	ProjectionDigest string             `json:"projectionDigest"`
	ObservedAt       time.Time          `json:"observedAt"`
	Current          bool               `json:"current"`
}
type TrackerLink struct {
	Publications  []TrackerPublication `json:"publications"`
	LatestSyncID  string               `json:"latestSyncId,omitempty"`
	ID            string               `json:"id"`
	WorkspaceID   string               `json:"workspaceId"`
	CreatorID     string               `json:"creatorId"`
	ConfigVersion int64                `json:"configVersion"`
	Input         TrackerLinkInput     `json:"input"`
	Digest        string               `json:"digest"`
	Projection    TrackerProjection    `json:"projection"`
	CreatedAt     time.Time            `json:"createdAt"`
	Observation   *TrackerObservation  `json:"observation,omitempty"`
}
type TrackerLinkPage struct {
	Links     []TrackerLink `json:"links"`
	Next      string        `json:"next,omitempty"`
	Truncated bool          `json:"truncated"`
}
type TrackerSyncInput struct {
	LinkDigest               string `json:"linkDigest"`
	Mode                     string `json:"mode"`
	ExpectedProjectionDigest string `json:"expectedProjectionDigest,omitempty"`
}
type TrackerSync struct {
	ID          string              `json:"id"`
	LinkID      string              `json:"linkId"`
	ActorID     string              `json:"actorId"`
	Input       TrackerSyncInput    `json:"input"`
	CreatedAt   time.Time           `json:"createdAt"`
	Observation *TrackerObservation `json:"observation,omitempty"`
	Dispatch    string              `json:"dispatch"`
}

func TrackerDigest(v any) string {
	b, _ := json.Marshal(v)
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}
func TrackerRecordID(provider, id string) bool {
	if provider == "linear" {
		return trackerUUID.MatchString(id)
	}
	return provider == "jira" && trackerNumber.MatchString(id)
}
func TrackerText(s string, max int) bool {
	return len(s) <= max && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}
func ValidateTrackerConfig(c TrackerConfig) error {
	if ValidateAccessID(c.WorkspaceID) != nil || ValidateAccessID(c.CredentialID) != nil || ValidateAccessID(c.WebhookCredentialID) != nil || !TrackerRecordID(c.Provider, c.ScopeID) || len(c.Statuses) > 100 || len(c.Statuses) == 0 || len(c.Grants) > 256 {
		return ErrInvalidInput
	}
	if c.Provider == "linear" {
		if c.Host != "linear.app" || c.Profile != LinearTrackerProfile || !trackerUUID.MatchString(c.OrganizationID) {
			return ErrInvalidInput
		}
	} else if c.Provider != "jira" || !trackerHost.MatchString(c.Host) || c.Profile != JiraTrackerProfile || c.OrganizationID != "" {
		return ErrInvalidInput
	}
	u, e := url.Parse(c.ConductorOrigin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || len(c.ConductorOrigin) > 512 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, s := range c.Statuses {
		if !TrackerRecordID(c.Provider, s.ID) || seen[s.ID] || !TrackerText(s.Display, 128) || s.Display == "" {
			return ErrInvalidInput
		}
		seen[s.ID] = true
	}
	seen = map[string]bool{}
	for _, g := range c.Grants {
		if ValidateAccessID(g.PrincipalID) != nil || seen[g.PrincipalID] || (!g.CanRead && (g.CanSync || g.CanResolve)) || g.CanResolve && !g.CanSync {
			return ErrInvalidInput
		}
		seen[g.PrincipalID] = true
	}
	return nil
}
func NormalizeTrackerLink(in TrackerLinkInput) (TrackerLinkInput, error) {
	in.Packages = append([]TrackerPackageRef(nil), in.Packages...)
	in.Publications = append([]TrackerPublicationRef(nil), in.Publications...)
	if len(in.Packages) < 1 || len(in.Packages) > 16 || len(in.Publications) > 16 || len(in.IssueID) > 64 {
		return in, ErrInvalidInput
	}
	sort.Slice(in.Packages, func(i, j int) bool { return in.Packages[i].PackageID < in.Packages[j].PackageID })
	sort.Slice(in.Publications, func(i, j int) bool { return in.Publications[i].ID < in.Publications[j].ID })
	for i, p := range in.Packages {
		if ValidateAccessID(p.RepositoryID) != nil || ValidateAccessID(p.PackageID) != nil || p.Revision < 1 || !IsLowerHex(p.Digest, 64) || i > 0 && in.Packages[i-1].PackageID == p.PackageID {
			return in, ErrInvalidInput
		}
	}
	for i, p := range in.Publications {
		if !IsLowerHex(p.ID, 32) || !IsLowerHex(p.Digest, 64) || i > 0 && in.Publications[i-1].ID == p.ID {
			return in, ErrInvalidInput
		}
	}
	return in, nil
}
func ValidateTrackerSync(in TrackerSyncInput) error {
	if !IsLowerHex(in.LinkDigest, 64) || in.Mode != "refresh" && in.Mode != "publish" && in.Mode != "restore" || in.ExpectedProjectionDigest != "" && !IsLowerHex(in.ExpectedProjectionDigest, 64) || in.Mode == "refresh" && in.ExpectedProjectionDigest != "" {
		return ErrInvalidInput
	}
	return nil
}
func MapTrackerStatus(config TrackerConfig, issue *TrackerIssue) {
	issue.Mapping = "unmapped"
	issue.MappedStatus = ""
	for _, s := range config.Statuses {
		if s.ID == issue.StatusID {
			issue.MappedStatus = s.Display
			issue.Mapping = "mapped"
			if s.Ignore {
				issue.Mapping = "intentionally_unsynchronized"
			}
			return
		}
	}
}

// Publication receipt identity is immutable; current provider observations remain
// independent evidence and cannot automatically complete the linked ticket.
type TrackerPublication struct {
	ID          string               `json:"id"`
	Digest      string               `json:"digest"`
	Target      DeliveryTarget       `json:"target"`
	Receipt     DeliveryReceipt      `json:"receipt"`
	Observation *DeliveryObservation `json:"observation,omitempty"`
	Current     bool                 `json:"current"`
}

// Command field names are exact: case aliases and duplicate keys must not create
// alternate interpretations of an inspected immutable input.
func decodeTrackerCommand(data []byte, target any, names ...string) error {
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return ErrInvalidInput
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || seen[key] {
			return ErrInvalidInput
		}
		seen[key] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return ErrInvalidInput
		}
	}
	if _, err = d.Token(); err != nil || d.Decode(new(any)) != io.EOF {
		return ErrInvalidInput
	}
	if json.Unmarshal(data, target) != nil {
		return ErrInvalidInput
	}
	return nil
}
func (in *TrackerLinkInput) UnmarshalJSON(data []byte) error {
	type plain TrackerLinkInput
	var value plain
	if err := decodeTrackerCommand(data, &value, "issueId", "packages", "publications"); err != nil {
		return err
	}
	*in = TrackerLinkInput(value)
	return nil
}
func (in *TrackerSyncInput) UnmarshalJSON(data []byte) error {
	type plain TrackerSyncInput
	var value plain
	if err := decodeTrackerCommand(data, &value, "linkDigest", "mode", "expectedProjectionDigest"); err != nil {
		return err
	}
	*in = TrackerSyncInput(value)
	return nil
}
func (in *TrackerPackageRef) UnmarshalJSON(data []byte) error {
	type plain TrackerPackageRef
	var value plain
	if err := decodeTrackerCommand(data, &value, "repositoryId", "packageId", "revision", "digest"); err != nil {
		return err
	}
	*in = TrackerPackageRef(value)
	return nil
}
func (in *TrackerPublicationRef) UnmarshalJSON(data []byte) error {
	type plain TrackerPublicationRef
	var value plain
	if err := decodeTrackerCommand(data, &value, "id", "digest"); err != nil {
		return err
	}
	*in = TrackerPublicationRef(value)
	return nil
}
