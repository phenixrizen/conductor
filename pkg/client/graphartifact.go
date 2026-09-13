package client

import (
	"context"

	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
	"net/url"
)

// GetRepositoryGraphArtifact reads a bounded retained artifact using the exact
// inspected graph/source tuple. The client's workspace and anchor remain fixed.
func (c *Client) GetRepositoryGraphArtifact(ctx context.Context, id string, q domain.GraphArtifactQuery) (domain.GraphArtifactResult, error) {
	var result domain.GraphArtifactResult
	values := url.Values{"graphDigest": {q.GraphDigest}, "repositoryId": {q.RepositoryID}, "collectionId": {q.CollectionID}, "receiptDigest": {q.ReceiptDigest}, "path": {q.Path}}
	if q.FullSourceDigest != "" {
		values.Set("fullSourceDigest", q.FullSourceDigest)
	}
	err := c.doInto(ctx, http.MethodGet, graphPath(id)+"/artifact?"+values.Encode(), nil, &result)
	return result, err
}
