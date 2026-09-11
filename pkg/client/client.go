package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
	"strings"
)

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
		var e any
		_ = json.NewDecoder(res.Body).Decode(&e)
		return domain.Package{}, fmt.Errorf("Conductor API %s: %v", res.Status, e)
	}
	var p domain.Package
	return p, json.NewDecoder(res.Body).Decode(&p)
}
func (c *Client) Create(ctx context.Context, v domain.Content) (domain.Package, error) {
	return c.do(ctx, "POST", "/api/v1/changes", map[string]any{"content": v})
}
func (c *Client) Get(ctx context.Context, id string) (domain.Package, error) {
	return c.do(ctx, "GET", "/api/v1/changes/"+id, nil)
}
func (c *Client) Submit(ctx context.Context, id string, r int64) (domain.Package, error) {
	return c.do(ctx, "POST", "/api/v1/changes/"+id+"/review-requests", map[string]any{"revision": r})
}
func (c *Client) Approve(ctx context.Context, id string, r int64, d string) (domain.Package, error) {
	return c.do(ctx, "POST", "/api/v1/changes/"+id+"/approvals", map[string]any{"revision": r, "digest": d})
}
