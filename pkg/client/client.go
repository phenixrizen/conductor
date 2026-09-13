package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type APIError struct {
	StatusCode                   int
	Code, Message, CorrelationID string
	cause                        error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Conductor API %d %s: %s (correlation ID %s)", e.StatusCode, e.Code, e.Message, e.CorrelationID)
}

func (e *APIError) Unwrap() error { return e.cause }

type Client struct {
	BaseURL, Actor string
	HTTP           *http.Client
	bearerToken    string
	workspaceID    string
	repositoryID   string
}

func New(base, actor string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), Actor: actor, HTTP: &http.Client{Timeout: 30 * time.Second}}
}
func (c *Client) do(ctx context.Context, method, path string, body any) (domain.Package, error) {
	var p domain.Package
	err := c.doInto(ctx, method, path, body, &p)
	return p, err
}

func (c *Client) doInto(ctx context.Context, method, path string, body, output any) error {
	// BaseURL is retained as a public field for existing local clients. Recheck
	// it at the credential boundary so later mutation cannot downgrade TLS.
	if c.bearerToken != "" {
		if err := validateCredentialURL(c.BaseURL); err != nil {
			return err
		}
	}
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, &b)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
		if c.workspaceID != "" {
			req.Header.Set("X-Conductor-Workspace", c.workspaceID)
		}
		if c.repositoryID != "" {
			req.Header.Set("X-Conductor-Repository", c.repositoryID)
		}
	} else {
		req.Header.Set("X-Conductor-Actor", c.Actor)
	}
	configured := c.HTTP
	if configured == nil {
		configured = &http.Client{Timeout: 30 * time.Second}
	}
	// A redirect can replay a command or turn its POST into a refresh GET. Keep
	// one request per command, even with a caller-provided transport or cookie jar.
	// Copy the client so concurrent requests never mutate caller configuration.
	transportClient := *configured
	transportClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.bearerToken != "" {
		transportClient.Jar = nil
	}
	res, err := transportClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return &APIError{StatusCode: res.StatusCode, Code: "unexpected_redirect",
			Message:       "API redirects are not followed; configure the final Conductor URL",
			CorrelationID: res.Header.Get("X-Correlation-ID")}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	// Even a proxy's HTML denial or an interrupted error body must retain 401/403
	// so interactive clients discard inspection and re-establish server access.
	invalidError := func(message string, cause error) error {
		return &APIError{StatusCode: res.StatusCode, Code: "invalid_error_response",
			Message: message, CorrelationID: res.Header.Get("X-Correlation-ID"), cause: cause}
	}
	if err != nil {
		if res.StatusCode >= 300 {
			return invalidError("could not read API error response", err)
		}
		return fmt.Errorf("read Conductor response: %w", err)
	}
	if len(data) > 2<<20 {
		if res.StatusCode >= 300 {
			return invalidError("API error response exceeds 2 MiB", nil)
		}
		return fmt.Errorf("Conductor response exceeds 2 MiB")
	}
	if res.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code          string `json:"code"`
				Message       string `json:"message"`
				CorrelationID string `json:"correlationId"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return invalidError("API returned an invalid error response", nil)
		}
		return &APIError{StatusCode: res.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message, CorrelationID: envelope.Error.CorrelationID}
	}
	return json.Unmarshal(data, output)
}
func (c *Client) Create(ctx context.Context, v domain.Content) (domain.Package, error) {
	return c.do(ctx, "POST", "/api/v1/changes", map[string]any{"content": v})
}
func (c *Client) Get(ctx context.Context, id string) (domain.Package, error) {
	return c.do(ctx, "GET", changePath(id), nil)
}
func (c *Client) Submit(ctx context.Context, id string, r int64) (domain.Package, error) {
	return c.do(ctx, "POST", changePath(id)+"/review-requests", map[string]any{"revision": r})
}
func (c *Client) Revise(ctx context.Context, id string, expected int64, content domain.Content) (domain.Package, error) {
	return c.do(ctx, "POST", changePath(id)+"/revisions", map[string]any{"expectedRevision": expected, "content": content})
}
func (c *Client) Approve(ctx context.Context, id string, r int64, d string) (domain.Package, error) {
	return c.do(ctx, "POST", changePath(id)+"/approvals", map[string]any{"revision": r, "digest": d})
}
func changePath(id string) string { return "/api/v1/changes/" + url.PathEscape(id) }

func (c *Client) History(ctx context.Context, id string, before int64, limit int) (domain.HistoryPage, error) {
	var page domain.HistoryPage
	query := url.Values{"beforeRevision": {strconv.FormatInt(before, 10)}, "limit": {strconv.Itoa(limit)}}
	err := c.doInto(ctx, "GET", changePath(id)+"/history?"+query.Encode(), nil, &page)
	return page, err
}

func (c *Client) Revision(ctx context.Context, id string, revision int64) (domain.RevisionRecord, error) {
	var record domain.RevisionRecord
	err := c.doInto(ctx, "GET", changePath(id)+"/revisions/"+strconv.FormatInt(revision, 10), nil, &record)
	return record, err
}

func (c *Client) Events(ctx context.Context, id string, after int64, limit int) (domain.AuditPage, error) {
	var page domain.AuditPage
	query := url.Values{"afterSequence": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	err := c.doInto(ctx, "GET", changePath(id)+"/events?"+query.Encode(), nil, &page)
	return page, err
}

func (c *Client) ListChanges(ctx context.Context, repository, before string, limit int) (domain.ChangePage, error) {
	var page domain.ChangePage
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if repository != "" {
		query.Set("repository", repository)
	}
	if before != "" {
		query.Set("before", before)
	}
	err := c.doInto(ctx, "GET", "/api/v1/changes?"+query.Encode(), nil, &page)
	return page, err
}
