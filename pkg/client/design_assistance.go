package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/phenixrizen/conductor/internal/domain"
)

// Assistance writes preserve the caller's captured key and pins. An uncertain
// acknowledgment is recovered by an explicit identical call, never an auto retry.
func (c *Client) RequestDesignAssistance(ctx context.Context, key string, in domain.AssistanceInput) (domain.DesignAssistance, error) {
	var value domain.DesignAssistance
	err := c.doIntoHeaders(ctx, http.MethodPost, "/api/v1/design-assistance", in, &value, http.Header{"Idempotency-Key": {key}})
	return value, err
}
func (c *Client) ListDesignAssistance(ctx context.Context, in domain.AssistanceListOptions) (domain.AssistancePage, error) {
	query := url.Values{"limit": {strconv.Itoa(in.Limit)}}
	if in.ChangeID != "" {
		query.Set("changeId", in.ChangeID)
	}
	if in.Before != "" {
		query.Set("before", in.Before)
	}
	var page domain.AssistancePage
	err := c.doInto(ctx, http.MethodGet, "/api/v1/design-assistance?"+query.Encode(), nil, &page)
	return page, err
}
func (c *Client) GetDesignAssistance(ctx context.Context, id string) (domain.DesignAssistance, error) {
	var value domain.DesignAssistance
	err := c.doInto(ctx, http.MethodGet, assistancePath(id), nil, &value)
	return value, err
}
func (c *Client) ProposeDesignSections(ctx context.Context, id, key string, in domain.SuggestionInput) (domain.DesignAssistance, error) {
	var value domain.DesignAssistance
	err := c.doIntoHeaders(ctx, http.MethodPost, assistancePath(id)+"/suggestion", in, &value, http.Header{"Idempotency-Key": {key}})
	return value, err
}
func (c *Client) ApplyDesignSuggestion(ctx context.Context, id, key string, in domain.ApplySuggestionInput) (domain.DesignAssistance, error) {
	var value domain.DesignAssistance
	err := c.doIntoHeaders(ctx, http.MethodPost, assistancePath(id)+"/application", in, &value, http.Header{"Idempotency-Key": {key}})
	return value, err
}
func assistancePath(id string) string { return "/api/v1/design-assistance/" + url.PathEscape(id) }
