package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/phenixrizen/conductor/internal/domain"
)

type APIError struct {
	StatusCode                   int
	Code, Message, CorrelationID string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Conductor API %d %s: %s (correlation ID %s)", e.StatusCode, e.Code, e.Message, e.CorrelationID)
}

type Client struct {
	BaseURL, Actor string
	HTTP           *http.Client
}

func New(base, actor string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), Actor: actor, HTTP: http.DefaultClient}
}
func (c *Client) do(ctx context.Context, method, path string, body any) (domain.Package, error) {
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			return domain.Package{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, &b)
	if err != nil {
		return domain.Package{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Conductor-Actor", c.Actor)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return domain.Package{}, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code          string `json:"code"`
				Message       string `json:"message"`
				CorrelationID string `json:"correlationId"`
			} `json:"error"`
		}
		if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
			return domain.Package{}, fmt.Errorf("Conductor API %s returned an invalid error: %w", res.Status, err)
		}
		return domain.Package{}, &APIError{StatusCode: res.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message, CorrelationID: envelope.Error.CorrelationID}
	}
	var p domain.Package
	return p, json.NewDecoder(res.Body).Decode(&p)
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
