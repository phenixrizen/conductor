package client

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) GetTracker(ctx context.Context) (domain.TrackerSettings, error) {
	var v domain.TrackerSettings
	e := c.doInto(ctx, "GET", "/api/v1/tracker", nil, &v)
	return v, e
}
func (c *Client) CreateTrackerLink(ctx context.Context, key string, in domain.TrackerLinkInput) (domain.TrackerLink, error) {
	var v domain.TrackerLink
	e := c.doIntoHeaders(ctx, "POST", "/api/v1/tracker-links", in, &v, http.Header{"Idempotency-Key": {key}})
	return v, e
}
func (c *Client) GetTrackerLink(ctx context.Context, id string) (domain.TrackerLink, error) {
	var v domain.TrackerLink
	e := c.doInto(ctx, "GET", "/api/v1/tracker-links/"+url.PathEscape(id), nil, &v)
	return v, e
}
func (c *Client) ListTrackerLinks(ctx context.Context, before string, limit int) (domain.TrackerLinkPage, error) {
	var v domain.TrackerLinkPage
	q := url.Values{"before": {before}, "limit": {strconv.Itoa(limit)}}
	e := c.doInto(ctx, "GET", "/api/v1/tracker-links?"+q.Encode(), nil, &v)
	return v, e
}
func (c *Client) RequestTrackerSync(ctx context.Context, id, key string, in domain.TrackerSyncInput) (domain.TrackerSync, error) {
	var v domain.TrackerSync
	e := c.doIntoHeaders(ctx, "POST", "/api/v1/tracker-links/"+url.PathEscape(id)+"/syncs", in, &v, http.Header{"Idempotency-Key": {key}})
	return v, e
}
func (c *Client) GetTrackerSync(ctx context.Context, id string) (domain.TrackerSync, error) {
	var v domain.TrackerSync
	e := c.doInto(ctx, "GET", "/api/v1/tracker-syncs/"+url.PathEscape(id), nil, &v)
	return v, e
}
