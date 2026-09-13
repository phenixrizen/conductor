package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

// CreateRepositoryGraph derives shared structural evidence from exact inspected
// receipts. A lost response requires an explicit retry of the same input/key.
func (c *Client) CreateRepositoryGraph(ctx context.Context, key string, input domain.GraphInput) (domain.RepositoryGraph, error) {
	var g domain.RepositoryGraph
	err := c.doIntoHeaders(ctx, http.MethodPost, "/api/v1/repository-graphs", input, &g, http.Header{"Idempotency-Key": {key}})
	return g, err
}
func (c *Client) GetRepositoryGraph(ctx context.Context, id string) (domain.RepositoryGraph, error) {
	var g domain.RepositoryGraph
	err := c.doInto(ctx, http.MethodGet, graphPath(id), nil, &g)
	return g, err
}
func (c *Client) ListRepositoryGraphs(ctx context.Context, before string, limit int) (domain.RepositoryGraphPage, error) {
	var page domain.RepositoryGraphPage
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		q.Set("before", before)
	}
	err := c.doInto(ctx, http.MethodGet, "/api/v1/repository-graphs?"+q.Encode(), nil, &page)
	return page, err
}
func (c *Client) QueryRepositoryGraph(ctx context.Context, id string, q domain.GraphQuery) (domain.GraphQueryResult, error) {
	var result domain.GraphQueryResult
	query := url.Values{"depth": {strconv.Itoa(q.Depth)}, "limit": {strconv.Itoa(q.Limit)}}
	if q.Search != "" {
		query.Set("search", q.Search)
	}
	if q.NodeID != "" {
		query.Set("nodeId", q.NodeID)
	}
	err := c.doInto(ctx, http.MethodGet, graphPath(id)+"/query?"+query.Encode(), nil, &result)
	return result, err
}
func graphPath(id string) string { return "/api/v1/repository-graphs/" + url.PathEscape(id) }
