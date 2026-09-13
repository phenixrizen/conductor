package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/domain"
)

type graphCreateArgs struct {
	IdempotencyKey string               `json:"idempotencyKey"`
	Sources        []domain.GraphSource `json:"sources"`
}
type graphArtifactArgs struct {
	ID     string                    `json:"id"`
	Source domain.GraphArtifactQuery `json:"source"`
}

type graphQueryArgs struct {
	ID     string `json:"id"`
	Search string `json:"search,omitempty"`
	NodeID string `json:"nodeId,omitempty"`
	Depth  int    `json:"depth,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (b *Bridge) registerGraphs() {
	id := map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"}
	digest := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	source := schema(map[string]any{"repositoryId": stringSchema(128), "collectionId": id, "digest": digest, "fullSourceDigest": digest}, "repositoryId", "collectionId", "digest")
	addTool(b, "conductor_create_graph", "Derive a shared cross-repository graph from 1–16 exact inspected scoped receipts, including the selected repository. Every source requires current access; source repository IDs do not change this session's scope. Structural relations, source gaps and unknown freshness are evidence, never passing checks. Retry an uncertain request only explicitly with the same key and sources.", schema(map[string]any{"idempotencyKey": stringSchema(128), "sources": map[string]any{"type": "array", "minItems": 1, "maxItems": 16, "items": source}}, "idempotencyKey", "sources"), true, true, func(ctx context.Context, a graphCreateArgs) (any, error) {
		if domain.ValidateCollectionKey(a.IdempotencyKey) != nil {
			return nil, domain.ErrInvalidInput
		}
		input, err := domain.NormalizeGraphInput(domain.GraphInput{Sources: a.Sources})
		if err != nil {
			return nil, domain.ErrInvalidInput
		}
		return b.api.CreateRepositoryGraph(ctx, a.IdempotencyKey, input)
	})
	addTool(b, "conductor_list_graphs", "List a bounded page of shared graphs anchored to this repository. The service filters current access to every source repository before pagination.", schema(pageSchema()), false, true, func(ctx context.Context, a pageArgs) (any, error) {
		return b.api.ListRepositoryGraphs(ctx, a.Before, pageSize(a.Limit))
	})
	addTool(b, "conductor_get_graph", "Inspect a complete immutable cross-repository graph with exact receipt sources, structural provenance, gaps and truncation. Current access to every source repository is required.", schema(map[string]any{"id": id}, "id"), false, true, func(ctx context.Context, a idArgs) (any, error) { return b.api.GetRepositoryGraph(ctx, a.ID) })
	addTool(b, "conductor_query_graph", "Search graph symbols or traverse both directions from an exact node, up to five edges and 100 nodes. Choose either search or nodeId. Results retain graph digest, receipt sources, evidence gaps and truncation; unresolved relations remain unknown.", schema(map[string]any{"id": id, "search": map[string]any{"type": "string", "maxLength": 256}, "nodeId": digest, "depth": map[string]any{"type": "integer", "minimum": 0, "maximum": 5}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}, "id"), false, true, func(ctx context.Context, a graphQueryArgs) (any, error) {
		query := domain.GraphQuery{Search: a.Search, NodeID: a.NodeID, Depth: a.Depth, Limit: pageSize(a.Limit)}
		if domain.ValidateGraphQuery(query) != nil {
			return nil, domain.ErrInvalidInput
		}
		return b.api.QueryRepositoryGraph(ctx, a.ID, query)
	})
	addTool(b, "conductor_read_graph_source", "Read bounded retained text from any repository in the inspected graph without changing this session's fixed scope. Capture the exact graph digest, source tuple and literal path. Every included repository requires current read access. Missing/unavailable/truncated source and unknown freshness remain explicit; this never fetches newer source, executes code or exposes Git bundles.", schema(map[string]any{"id": id, "source": schema(map[string]any{"graphDigest": digest, "repositoryId": stringSchema(128), "collectionId": id, "receiptDigest": digest, "fullSourceDigest": digest, "path": stringSchema(1024)}, "graphDigest", "repositoryId", "collectionId", "receiptDigest", "path")}, "id", "source"), false, true, func(ctx context.Context, a graphArtifactArgs) (any, error) {
		if domain.ValidateGraphArtifactQuery(a.Source) != nil {
			return nil, domain.ErrInvalidInput
		}
		return b.api.GetRepositoryGraphArtifact(ctx, a.ID, a.Source)
	})
	b.server.AddResource(&mcp.Resource{Name: "repository-graphs", URI: b.baseURI + "/graphs", Description: "First bounded page of authorized cross-repository graph snapshots. Use conductor_list_graphs for continuation.", MIMEType: "application/json"}, b.readResource)
	b.server.AddResourceTemplate(&mcp.ResourceTemplate{Name: "repository-graph", URITemplate: b.baseURI + "/graphs/{id}", Description: "Immutable graph and receipt provenance. Every source repository remains subject to current authorization.", MIMEType: "application/json"}, b.readResource)
}
