package client

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) ExecutionCapabilities(ctx context.Context) (domain.ExecutionCapabilities, error) {
	var caps domain.ExecutionCapabilities
	err := c.doInto(ctx, http.MethodGet, "/api/v1/execution-capabilities", nil, &caps)
	return caps, err
}

func coordinationPath(id string) string { return "/api/v1/coordination-runs/" + url.PathEscape(id) }
func (c *Client) CreateCoordination(ctx context.Context, key string, plan domain.CoordinationPlan) (domain.CoordinationRun, error) {
	var run domain.CoordinationRun
	err := c.doIntoHeaders(ctx, http.MethodPost, "/api/v1/coordination-runs", plan, &run, http.Header{"Idempotency-Key": {key}})
	return run, err
}
func (c *Client) GetCoordination(ctx context.Context, id string) (domain.CoordinationRun, error) {
	var run domain.CoordinationRun
	err := c.doInto(ctx, http.MethodGet, coordinationPath(id), nil, &run)
	return run, err
}
func (c *Client) ListCoordinations(ctx context.Context, before string, limit int) (domain.CoordinationPage, error) {
	var page domain.CoordinationPage
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		q.Set("before", before)
	}
	err := c.doInto(ctx, http.MethodGet, "/api/v1/coordination-runs?"+q.Encode(), nil, &page)
	return page, err
}

// Commands use the displayed immutable digest directly, with no refresh or retry.
func (c *Client) AuthorizeCoordination(ctx context.Context, id, digest string) (domain.CoordinationRun, error) {
	var run domain.CoordinationRun
	err := c.doInto(ctx, http.MethodPost, coordinationPath(id)+"/authorization", map[string]string{"digest": digest}, &run)
	return run, err
}
func (c *Client) CancelCoordination(ctx context.Context, id, digest string) (domain.CoordinationRun, error) {
	var run domain.CoordinationRun
	err := c.doInto(ctx, http.MethodPost, coordinationPath(id)+"/cancellation", map[string]string{"digest": digest}, &run)
	return run, err
}
