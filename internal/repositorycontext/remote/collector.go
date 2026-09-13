// Package remote reads explicitly selected Git objects through bounded provider
// APIs. It never follows repository links or executes repository-controlled code.
package remote

import (
	"context"
	"crypto/sha1" // Git's SHA-1 object format, not an authentication primitive.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

// The GitHub HTTP version and inspected GitLab release profile are deliberate
// compatibility boundaries. See docs/architecture/context-integration-research.md
// for official source pins and the distinction between fixtures and live service
// compatibility. Enterprise and self-managed origins require a separate profile.
const (
	GitHubProfile         = "github-rest/2026-03-10"
	GitLabProfile         = "gitlab-rest/v4-19.3"
	maxResponseBytes      = 128 << 10
	maxTotalResponseBytes = 2 << 20
	maxRequests           = 128
	maxDirectoryEntries   = 1000
	requestTimeout        = 10 * time.Second
)

// Binding contains operator configuration, never user-selected remote URLs.
// GitHub's owner/name Locator locates the separately verified numeric ProviderID.
type Binding struct{ Provider, Host, ProviderID, Locator, Profile string }
type Config struct {
	Binding    Binding
	Token      string
	HTTPClient *http.Client
	// APIOrigin and AllowInsecureLoopback permit owned loopback fixtures only.
	// They do not enable arbitrary enterprise or self-managed provider origins.
	APIOrigin             string
	AllowInsecureLoopback bool
}

// Error deliberately contains no URL, response text, wrapped provider error, or
// credential. Activities may safely retain this classification in their history.
type Error struct {
	Code       string
	Retryable  bool
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	switch e.Code {
	case "invalid_config", "invalid_input", "access_revoked", "authorization_unavailable", "cancelled", "deadline", "transport", "rate_limited", "provider_unavailable", "provider_denied", "not_found", "redirect", "response_limit", "request_limit", "invalid_response", "identity_mismatch", "object_mismatch", "incomplete_tree":
		return "remote context: " + e.Code
	default:
		return "remote context: unavailable"
	}
}
func failure(code string) *Error { return &Error{Code: code} }

type Collector struct {
	binding               Binding
	token, origin, prefix string
	client                *http.Client
}

func New(config Config) (*Collector, error) {
	b := config.Binding
	if !decimalID(b.ProviderID) || !validToken(config.Token) {
		return nil, failure("invalid_config")
	}
	origin, prefix := "", ""
	switch b.Provider {
	case "github":
		if b.Host != "github.com" || b.Profile != GitHubProfile || !validLocator(b.Locator) {
			return nil, failure("invalid_config")
		}
		origin, prefix = "https://api.github.com", "/repos/"+b.Locator
	case "gitlab":
		if b.Host != "gitlab.com" || b.Profile != GitLabProfile || b.Locator != "" {
			return nil, failure("invalid_config")
		}
		origin, prefix = "https://gitlab.com", "/api/v4/projects/"+b.ProviderID
	default:
		return nil, failure("invalid_config")
	}
	if config.APIOrigin != "" && config.APIOrigin != origin {
		u, err := url.Parse(config.APIOrigin)
		if err != nil || !config.AllowInsecureLoopback || !loopbackOrigin(u) {
			return nil, failure("invalid_config")
		}
		origin = config.APIOrigin
	}
	client := http.Client{}
	if config.HTTPClient != nil {
		client = *config.HTTPClient
	}
	if client.Jar != nil {
		return nil, failure("invalid_config")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if client.Transport != nil {
		t, ok := client.Transport.(*http.Transport)
		if !ok {
			return nil, failure("invalid_config")
		}
		transport = t.Clone()
	}
	// Enforce bounds even with a caller-provided client. The caller may supply a
	// trusted CA pool, but cannot turn redirects or cookie state back on.
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 32 << 10
	transport.ResponseHeaderTimeout = requestTimeout
	transport.MaxConnsPerHost = 1
	transport.DisableCompression = true
	// A fresh connection prevents net/http from silently retrying an idempotent
	// request on a reused connection without another authorization callback.
	transport.DisableKeepAlives = true
	// HTTP/2 has its own transparent stream retry behavior. This narrow read
	// profile uses one fresh HTTP/1 connection per authorized provider operation.
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = nil
	if transport.TLSClientConfig != nil {
		transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		return nil, failure("invalid_config")
	}
	client.Transport, client.Timeout = transport, requestTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Collector{binding: b, token: config.Token, origin: origin, prefix: prefix, client: &client}, nil
}

func decimalID(value string) bool {
	if len(value) == 0 || len(value) > 20 || value[0] == '0' {
		return false
	}
	for _, b := range []byte(value) {
		if b < '0' || b > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}
func validToken(value string) bool {
	if len(value) == 0 || len(value) > 16<<10 {
		return false
	}
	for _, b := range []byte(value) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
func validLocator(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || len(p) > 100 {
			return false
		}
		for _, b := range []byte(p) {
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.') {
				return false
			}
		}
	}
	return true
}
func loopbackOrigin(u *url.URL) bool {
	if u == nil || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback() && u.Host != ""
}
func oid(value string) bool {
	return len(value) == 40 && value == strings.ToLower(value) && domain.ValidGitOID(value)
}

type collection struct {
	c                       *Collector
	authorize               func(context.Context) error
	commit                  string
	requests, responseBytes int
	finalIdentity           bool
	trees                   map[string]treeResult
}
type treeEntry struct {
	name, id, mode, kind string
	size                 *int64
}
type treeResult struct {
	entries map[string]treeEntry
	err     *Error
}

// Collect returns one result per sorted path. Transient provider failures abort
// the attempt, allowing the workflow to retry before any immutable receipt exists.
// Permanent path gaps retain no partial text. No retry or sleep occurs here.
// The authorizer returns domain.ErrForbidden, ErrUnauthenticated, ErrNotFound or
// ErrCollectionStopped for permanent denial. Unknown I/O failures become retryable
// without sending a provider request.
func (c *Collector) Collect(ctx context.Context, commit string, paths []string, authorize func(context.Context) error) ([]domain.ContextArtifact, error) {
	if !oid(commit) || len(paths) == 0 || len(paths) > domain.MaxContextArtifacts || authorize == nil {
		return nil, failure("invalid_input")
	}
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	for i, path := range paths {
		if domain.ValidateContextPath(path) != nil || i > 0 && path == paths[i-1] {
			return nil, failure("invalid_input")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	s := &collection{c: c, authorize: authorize, commit: commit, trees: make(map[string]treeResult)}
	if err := s.identity(ctx); err != nil {
		return nil, err
	}
	root, err := s.commitRoot(ctx)
	if err != nil {
		return nil, err
	}
	artifacts := make([]domain.ContextArtifact, 0, len(paths))
	remaining := domain.MaxContextTotalBytes
	for _, path := range paths {
		a, err := s.artifact(ctx, root, path, remaining)
		if err != nil {
			return nil, err
		}
		if a.Text != nil {
			remaining -= len(*a.Text)
		}
		artifacts = append(artifacts, a)
	}
	// A locator is not identity. Recheck it after all reads and reject the entire
	// observation on ordinary rename/rebinding races; REST is not an atomic proof.
	if c.binding.Provider == "github" {
		s.finalIdentity = true
		if err := s.identity(ctx); err != nil {
			return nil, err
		}
	}
	return artifacts, nil
}

func (s *collection) artifact(ctx context.Context, root, path string, remaining int) (domain.ContextArtifact, error) {
	a := domain.ContextArtifact{Path: path, State: "unavailable"}
	components := strings.Split(path, "/")
	directory := ""
	for i, name := range components {
		key := root
		if s.c.binding.Provider == "gitlab" {
			key = directory
		}
		tree := s.tree(ctx, key)
		if tree.err != nil {
			return gap(a, tree.err)
		}
		entry, ok := tree.entries[name]
		if !ok {
			a.State, a.Message = "missing", "The selected path is absent from a complete directory listing at the inspected commit."
			return a, nil
		}
		if i < len(components)-1 {
			if entry.mode != "040000" || entry.kind != "tree" {
				a.Message = "An intermediate path is not a regular directory; symlinks and submodules are unavailable."
				return a, nil
			}
			root = entry.id
			if directory != "" {
				directory += "/"
			}
			directory += name
			continue
		}
		if entry.kind != "blob" || entry.mode != "100644" && entry.mode != "100755" {
			a.Message = "Only regular text files are supported; directories, symlinks and submodules are unavailable."
			return a, nil
		}
		a.BlobOID = entry.id
		if entry.size != nil && (*entry.size > domain.MaxContextArtifactBytes || *entry.size > int64(remaining)) {
			return truncated(a), nil
		}
		blob, err := s.blob(ctx, entry.id)
		if err != nil {
			return gap(a, err)
		}
		if blob.Size == nil || blob.Content == nil || *blob.Size < 0 || blob.SHA != entry.id || blob.Encoding != "base64" || entry.size != nil && *entry.size != *blob.Size {
			return gap(a, failure("object_mismatch"))
		}
		if *blob.Size > domain.MaxContextArtifactBytes || *blob.Size > int64(remaining) {
			return truncated(a), nil
		}
		data, decodeErr := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(*blob.Content)), domain.MaxContextArtifactBytes+1))
		if decodeErr != nil || int64(len(data)) != *blob.Size || gitBlobID(data) != entry.id {
			return gap(a, failure("object_mismatch"))
		}
		if !domain.IsContextText(data) {
			a.Message = "The selected blob is binary or contains unsupported control bytes."
			return a, nil
		}
		text := string(data)
		digest := sha256.Sum256(data)
		a.State, a.Text, a.Digest = "collected", &text, hex.EncodeToString(digest[:])
		if strings.HasPrefix(text, "version https://git-lfs.github.com/spec/v1\n") || strings.HasPrefix(text, "version https://git-lfs.github.com/spec/v1\r\n") {
			a.Message = "Collected Git LFS pointer text only; the referenced LFS object was not fetched."
		}
		return a, nil
	}
	return gap(a, failure("invalid_response"))
}

func gitBlobID(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d%c", len(data), 0)
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
func truncated(a domain.ContextArtifact) domain.ContextArtifact {
	a.State, a.Message = "truncated", "The complete file exceeds the 64 KiB file limit or 256 KiB collection limit; no partial text was retained."
	return a
}
func gap(a domain.ContextArtifact, err *Error) (domain.ContextArtifact, error) {
	if err.Retryable || err.Code == "access_revoked" || err.Code == "cancelled" || err.Code == "deadline" || err.Code == "provider_denied" || err.Code == "identity_mismatch" {
		return domain.ContextArtifact{}, err
	}
	switch err.Code {
	case "not_found":
		a.Message = "The provider could not make this object available; a missing object is not proof that the selected path is absent."
	case "request_limit", "response_limit", "incomplete_tree":
		a.Message = "The bounded directory or response limit prevented complete inspection."
	case "object_mismatch":
		a.Message = "The provider object metadata or blob bytes did not match the inspected object identity."
	case "redirect":
		a.Message = "The provider requested a redirect, which this collection profile does not follow."
	default:
		a.Message = "The provider response could not establish a complete supported object."
	}
	return a, nil
}
