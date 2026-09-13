package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

// CreateCollection sends one explicit request. Reuse its key and immutable input
// to reconcile an uncertain response; this client does not retry automatically.
func (c *Client) CreateCollection(ctx context.Context, key string, input domain.CollectionInput) (domain.Collection, error) {
	var value domain.Collection
	err := c.doIntoHeaders(ctx, http.MethodPost, "/api/v1/context-collections", input, &value, http.Header{"Idempotency-Key": {key}})
	return value, err
}

func (c *Client) GetCollection(ctx context.Context, id string) (domain.Collection, error) {
	var value domain.Collection
	err := c.doInto(ctx, http.MethodGet, collectionPath(id), nil, &value)
	return value, err
}

func (c *Client) ListCollections(ctx context.Context, before string, limit int) (domain.CollectionPage, error) {
	var page domain.CollectionPage
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		query.Set("before", before)
	}
	err := c.doInto(ctx, http.MethodGet, "/api/v1/context-collections?"+query.Encode(), nil, &page)
	return page, err
}

func (c *Client) CancelCollection(ctx context.Context, id string) (domain.Collection, error) {
	var value domain.Collection
	err := c.doInto(ctx, http.MethodPost, collectionPath(id)+"/cancellation", struct{}{}, &value)
	return value, err
}

func (c *Client) AttachCollection(ctx context.Context, changeID string, expected int64, collectionID, digest string) (domain.Package, error) {
	return c.do(ctx, http.MethodPost, changePath(changeID)+"/context-attachments", map[string]any{
		"expectedRevision": expected, "collectionId": collectionID, "digest": digest,
	})
}

func collectionPath(id string) string { return "/api/v1/context-collections/" + url.PathEscape(id) }
