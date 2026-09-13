package client

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
	"net/url"
)

// GetCoordinationArtifact is an explicit larger response profile for complete
// retained output, including failed checks and reports with no publication.
func (c *Client) GetCoordinationArtifact(ctx context.Context, id string, q domain.CoordinationArtifactQuery) (domain.CoordinationArtifact, error) {
	var out domain.CoordinationArtifact
	if !domain.IsLowerHex(id, 32) || domain.ValidateCoordinationArtifactQuery(q) != nil {
		return out, domain.ErrInvalidInput
	}
	values := url.Values{"runDigest": {q.RunDigest}, "taskId": {q.TaskID}, "artifactDigest": {q.ArtifactDigest}}
	err := c.doIntoHeadersLimit(ctx, http.MethodGet, coordinationPath(id)+"/artifact?"+values.Encode(), nil, &out, nil, 17<<20)
	return out, err
}
