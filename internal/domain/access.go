package domain

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrUnauthenticated = errors.New("authenticated principal is required")
	ErrForbidden       = errors.New("permission denied")
)

type AccessIdentity struct {
	Issuer  string
	Subject string
}
type AccessRequest struct {
	Identity     AccessIdentity
	WorkspaceID  string
	RepositoryID string
}
type accessContextKey struct{}

func WithAccess(ctx context.Context, access AccessRequest) context.Context {
	return context.WithValue(ctx, accessContextKey{}, access)
}
func AccessFromContext(ctx context.Context) (AccessRequest, bool) {
	a, ok := ctx.Value(accessContextKey{}).(AccessRequest)
	return a, ok
}

type Principal struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Session struct {
	Principal  Principal   `json:"principal"`
	Workspaces []Workspace `json:"workspaces"`
	Truncated  bool        `json:"truncated"`
}
type ManagedRepository struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Provider    string `json:"provider"`
	Host        string `json:"host"`
	ProviderID  string `json:"providerId"`
	Name        string `json:"name"`
	CanRead     bool   `json:"canRead"`
	CanAuthor   bool   `json:"canAuthor"`
	CanApprove  bool   `json:"canApprove"`
}
type RepositoryPage struct {
	Repositories []ManagedRepository `json:"repositories"`
	Truncated    bool                `json:"truncated"`
}
type PrincipalConfig struct {
	ID      string `json:"id"`
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Active  bool   `json:"active"`
}
type WorkspaceConfig struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type MembershipConfig struct {
	WorkspaceID string `json:"workspaceId"`
	PrincipalID string `json:"principalId"`
	Active      bool   `json:"active"`
}
type RepositoryConfig struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Provider    string `json:"provider"`
	Host        string `json:"host"`
	ProviderID  string `json:"providerId"`
	Name        string `json:"name"`
}
type GrantConfig struct {
	RepositoryID string `json:"repositoryId"`
	PrincipalID  string `json:"principalId"`
	CanRead      bool   `json:"canRead"`
	CanAuthor    bool   `json:"canAuthor"`
	CanApprove   bool   `json:"canApprove"`
}
type AccessConfig struct {
	Principals          []PrincipalConfig          `json:"principals"`
	Workspaces          []WorkspaceConfig          `json:"workspaces"`
	Memberships         []MembershipConfig         `json:"memberships"`
	Repositories        []RepositoryConfig         `json:"repositories"`
	Grants              []GrantConfig              `json:"grants"`
	ContextIntegrations []ContextIntegrationConfig `json:"contextIntegrations,omitempty"`
}

const MaxAccessPageSize = 100

func ValidateAccessID(s string) error {
	if s == "" || len(s) > 128 || !utf8.ValidString(s) || strings.TrimSpace(s) != s || strings.ContainsFunc(s, unicode.IsControl) {
		return ErrInvalidInput
	}
	return nil
}
func validAccessText(s string, max int) bool {
	return s != "" && len(s) <= max && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

// NormalizeRepositoryHost preserves installation identity without accepting a URL,
// credentials, or path. Names and provider IDs never establish host identity.
func NormalizeRepositoryHost(host string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.ContainsAny(host, "/@?#\\") || !validAccessText(host, 253) {
		return "", ErrInvalidInput
	}
	name := host
	if strings.Contains(host, ":") {
		var err error
		var port string
		name, port, err = net.SplitHostPort(host)
		portNumber, portErr := strconv.Atoi(port)
		if portErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", ErrInvalidInput
		}
		if err != nil {
			return "", ErrInvalidInput
		}
		if portNumber == 443 {
			host = name
		} else {
			host = name + ":" + strconv.Itoa(portNumber)
		}
	}
	if name == "" || strings.HasSuffix(name, ".") || strings.ContainsAny(name, " []") {
		return "", ErrInvalidInput
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '.' {
			return "", ErrInvalidInput
		}
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", ErrInvalidInput
		}
	}
	return host, nil
}
func ValidateAccessConfig(c AccessConfig) error {
	if len(c.Principals)+len(c.Workspaces)+len(c.Memberships)+len(c.Repositories)+len(c.Grants)+len(c.ContextIntegrations) > 500 {
		return fmt.Errorf("%w: at most 500 access records", ErrInvalidInput)
	}
	for _, p := range c.Principals {
		if ValidateAccessID(p.ID) != nil || !validAccessText(p.Issuer, 2048) || !validAccessText(p.Subject, 512) || (p.Kind != "human" && p.Kind != "agent") {
			return ErrInvalidInput
		}
	}
	for _, w := range c.Workspaces {
		if ValidateAccessID(w.ID) != nil || !validAccessText(w.Name, 256) {
			return ErrInvalidInput
		}
	}
	for _, m := range c.Memberships {
		if ValidateAccessID(m.WorkspaceID) != nil || ValidateAccessID(m.PrincipalID) != nil {
			return ErrInvalidInput
		}
	}
	for _, r := range c.Repositories {
		if ValidateAccessID(r.ID) != nil || ValidateAccessID(r.WorkspaceID) != nil || (r.Provider != "github" && r.Provider != "gitlab") || !validAccessText(r.ProviderID, 256) || !validAccessText(r.Name, 512) {
			return ErrInvalidInput
		}
		if _, err := NormalizeRepositoryHost(r.Host); err != nil {
			return err
		}
	}
	for _, g := range c.Grants {
		if ValidateAccessID(g.RepositoryID) != nil || ValidateAccessID(g.PrincipalID) != nil || (!g.CanRead && (g.CanAuthor || g.CanApprove)) {
			return ErrInvalidInput
		}
	}
	for _, integration := range c.ContextIntegrations {
		if err := ValidateContextIntegrationConfig(integration); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRepositoryAction is shared domain policy. Stored permission bits cannot
// grant an agent human architectural authority, even if provisioned incorrectly.
func ValidateRepositoryAction(principal Principal, read, author, approve bool, action string) error {
	if !read {
		return ErrNotFound
	}
	switch action {
	case "read":
		return nil
	case "author":
		if author {
			return nil
		}
	case "approve":
		if approve && principal.Kind == "human" {
			return nil
		}
	default:
		return ErrInvalidInput
	}
	return ErrForbidden
}
