package client

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
)

func (c *Client) ExecutionProfiles(ctx context.Context) (domain.ExecutionProfilePage, error) {
	var page domain.ExecutionProfilePage
	err := c.doInto(ctx, http.MethodGet, "/api/v1/execution-profiles", nil, &page)
	return page, err
}
