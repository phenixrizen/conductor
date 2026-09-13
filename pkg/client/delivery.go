package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

func (c *Client) CreateDelivery(ctx context.Context, key string, input domain.DeliveryInput) (domain.Delivery, error) {
	var out domain.Delivery
	err := c.doIntoHeaders(ctx, http.MethodPost, "/api/v1/repository-deliveries", input, &out, http.Header{"Idempotency-Key": {key}})
	return out, err
}
func (c *Client) GetDelivery(ctx context.Context, id string) (domain.Delivery, error) {
	var out domain.Delivery
	err := c.doInto(ctx, http.MethodGet, deliveryPath(id), nil, &out)
	return out, err
}
func (c *Client) ListDeliveries(ctx context.Context, before string, limit int) (domain.DeliveryPage, error) {
	var out domain.DeliveryPage
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		q.Set("before", before)
	}
	err := c.doInto(ctx, http.MethodGet, "/api/v1/repository-deliveries?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) AuthorizeDelivery(ctx context.Context, id, digest string) (domain.Delivery, error) {
	var out domain.Delivery
	err := c.doInto(ctx, http.MethodPost, deliveryPath(id)+"/authorizations", map[string]string{"digest": digest}, &out)
	return out, err
}
func (c *Client) ReconcileDelivery(ctx context.Context, id, digest, key string) (domain.Delivery, error) {
	var out domain.Delivery
	err := c.doIntoHeaders(ctx, http.MethodPost, deliveryPath(id)+"/reconciliations", map[string]string{"digest": digest}, &out, http.Header{"Idempotency-Key": {key}})
	return out, err
}
func deliveryPath(id string) string { return "/api/v1/repository-deliveries/" + url.PathEscape(id) }

// GetDeliveryArtifact is the sole larger response profile: the persisted artifact
// is bounded to 16 MiB plus its envelope. All ordinary reads retain 2 MiB bounds.
func (c *Client) GetDeliveryArtifact(ctx context.Context, id string) (domain.DeliveryArtifact, error) {
	var out domain.DeliveryArtifact
	err := c.doIntoHeadersLimit(ctx, http.MethodGet, deliveryPath(id)+"/artifact", nil, &out, nil, 17<<20)
	return out, err
}
