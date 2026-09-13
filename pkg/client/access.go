package client

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode"

	"github.com/phenixrizen/conductor/internal/domain"
)

const MaxTokenBytes = 16 << 10

// NewAuthenticated uses a caller-obtained API access token. It does not acquire,
// refresh, or inspect tokens, and local actor labels never accompany credentials.
// Empty scope allows session discovery; the service requires scope per command.
func NewAuthenticated(base, token, workspace, repositoryID string) (*Client, error) {
	if err := validateCredentialURL(base); err != nil {
		return nil, err
	}
	if !validBearerToken(token) {
		return nil, errors.New("access token must be a nonempty bearer token of at most 16 KiB without whitespace")
	}
	for _, scope := range []string{workspace, repositoryID} {
		if scope != "" && (domain.ValidateAccessID(scope) != nil || strings.IndexFunc(scope, unicode.IsControl) >= 0) {
			return nil, errors.New("workspace and repository IDs must be bounded identifiers without control characters")
		}
	}
	c := New(base, "")
	c.bearerToken, c.workspaceID, c.repositoryID = token, workspace, repositoryID
	return c, nil
}

func (c *Client) Session(ctx context.Context) (domain.Session, error) {
	var session domain.Session
	err := c.doInto(ctx, "GET", "/api/v1/session", nil, &session)
	return session, err
}

func (c *Client) Repositories(ctx context.Context) (domain.RepositoryPage, error) {
	var page domain.RepositoryPage
	err := c.doInto(ctx, "GET", "/api/v1/repositories", nil, &page)
	return page, err
}

func validateCredentialURL(base string) error {
	u, err := url.Parse(base)
	if err != nil || len(base) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(base, "#") {
		return errors.New("authenticated API URL must have a host and no credentials, query, or fragment")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("authenticated API requests require HTTPS; HTTP is allowed only for local loopback development")
}

func validBearerToken(token string) bool {
	if token == "" || len(token) > MaxTokenBytes {
		return false
	}
	padding := false
	for _, c := range token {
		if c == '=' {
			padding = true
			continue
		}
		if padding || !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.ContainsRune("-._~+/", c)) {
			return false
		}
	}
	return token[0] != '='
}
