package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

func (c *Client) CreateRuntimeEvidence(ctx context.Context, key string, input domain.RuntimeInput) (domain.RuntimeEvidence, error) {
	var out domain.RuntimeEvidence
	err := c.doIntoHeaders(ctx, http.MethodPost, "/api/v1/runtime-evidence", input, &out, http.Header{"Idempotency-Key": {key}})
	return out, err
}
func (c *Client) GetRuntimeEvidence(ctx context.Context, id string) (domain.RuntimeEvidence, error) {
	var out domain.RuntimeEvidence
	err := c.doInto(ctx, http.MethodGet, runtimeEvidencePath(id), nil, &out)
	return out, err
}
func (c *Client) ListRuntimeEvidence(ctx context.Context, before string, limit int) (domain.RuntimePage, error) {
	var out domain.RuntimePage
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		q.Set("before", before)
	}
	err := c.doInto(ctx, http.MethodGet, "/api/v1/runtime-evidence?"+q.Encode(), nil, &out)
	return out, err
}

func runtimeEvidencePath(id string) string { return "/api/v1/runtime-evidence/" + url.PathEscape(id) }
