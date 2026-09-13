package mcpserver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/domain"
)

type coordinationCreateArgs struct {
	IdempotencyKey string                  `json:"idempotencyKey"`
	Plan           domain.CoordinationPlan `json:"plan"`
}

// Agents may propose and inspect shared plans. Human execution authorization is
// deliberately absent, even when an MCP host happens to use a human credential.
func (b *Bridge) registerCoordination() {
	id := map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"}
	addTool(b, "conductor_execution_profiles", "Read the operator-configured workspace profiles and immutable profile/image digests. Missing or truncated profiles do not establish runnable capacity. No credential references are returned.", schema(map[string]any{}), false, true, func(ctx context.Context, _ emptyArgs) (any, error) { return b.api.ExecutionProfiles(ctx) })
	addTool(b, "conductor_execution_capabilities", "Read current selected-repository execution capabilities. Human execution authority is separate from package approval; this bridge cannot authorize a run.", schema(map[string]any{}), false, true, func(ctx context.Context, _ emptyArgs) (any, error) { return b.api.ExecutionCapabilities(ctx) })
	addTool(b, "conductor_list_runs", "Discover one bounded page of shared coordinated plans and retained observations. Every included repository must remain readable. Missing receipts or stale execution observations establish no passing evidence.", schema(pageSchema()), false, true, func(ctx context.Context, a pageArgs) (any, error) {
		return b.api.ListCoordinations(ctx, a.Before, pageSize(a.Limit))
	})
	addTool(b, "conductor_get_run", "Inspect the exact immutable plan, dependencies, source/package/profile pins, separate human authorization, cancellation intent and retained task receipts. A completed workflow is not deployment or production outcome verification.", schema(map[string]any{"id": id}, "id"), false, true, func(ctx context.Context, a idArgs) (any, error) { return b.api.GetCoordination(ctx, a.ID) })
	addTool(b, "conductor_propose_run", "Propose a bounded shared task DAG using exact inspected graph, package and whole-source digests. Keep the same key and complete plan for an explicit uncertain-result retry. This stores a proposal only; a human must separately inspect and authorize execution. Prompts and perspective labels cannot grant authority.", schema(map[string]any{"idempotencyKey": stringSchema(128), "plan": coordinationPlanSchema()}, "idempotencyKey", "plan"), true, true, func(ctx context.Context, a coordinationCreateArgs) (any, error) {
		if domain.ValidateCollectionKey(a.IdempotencyKey) != nil {
			return nil, domain.ErrInvalidInput
		}
		plan, err := domain.NormalizeCoordinationPlan(a.Plan)
		if err != nil {
			return nil, domain.ErrInvalidInput
		}
		return b.api.CreateCoordination(ctx, a.IdempotencyKey, plan)
	})
	b.server.AddResource(&mcp.Resource{Name: "coordination-runs", URI: b.baseURI + "/runs", Description: "First bounded page of shared plans. Use conductor_list_runs for continuation.", MIMEType: "application/json"}, b.readResource)
	b.server.AddResourceTemplate(&mcp.ResourceTemplate{Name: "coordination-run", URITemplate: b.baseURI + "/runs/{id}", Description: "Exact shared task plan, authorization and observed receipts, with current all-repository access.", MIMEType: "application/json"}, b.readResource)
}

func coordinationPlanSchema() map[string]any {
	digest := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	commit := map[string]any{"type": "string", "pattern": "^[0-9a-f]{40}$"}
	id := map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"}
	key := map[string]any{"type": "string", "pattern": "^[A-Za-z0-9._-]{1,64}$"}
	bounded := func(low, high int) map[string]any {
		return map[string]any{"type": "integer", "minimum": low, "maximum": high}
	}
	array := func(item map[string]any, low, high int) map[string]any {
		return map[string]any{"type": "array", "minItems": low, "maxItems": high, "items": item}
	}
	nullableArray := func(item map[string]any, high int) map[string]any {
		v := array(item, 0, high)
		v["type"] = []string{"array", "null"}
		return v
	}
	pin := schema(map[string]any{"changeId": stringSchema(128), "repositoryId": stringSchema(128), "revision": positiveSchema(), "digest": digest}, "changeId", "repositoryId", "revision", "digest")
	repo := schema(map[string]any{"repositoryId": stringSchema(128), "commit": commit, "collectionId": id, "receiptDigest": digest, "fullSourceDigest": digest}, "repositoryId", "commit", "collectionId", "receiptDigest")
	scope := schema(map[string]any{"repositoryId": stringSchema(128), "writablePaths": nullableArray(stringSchema(1024), 128)}, "repositoryId")
	check := schema(map[string]any{"id": key, "repositoryId": stringSchema(128), "argv": array(map[string]any{"type": "string", "maxLength": 4096}, 1, 64), "timeoutSeconds": bounded(1, 1800)}, "id", "repositoryId", "argv", "timeoutSeconds")
	task := schema(map[string]any{"id": key, "perspective": map[string]any{"type": "string", "enum": []string{"architect", "developer", "qc", "product", "operations"}}, "profile": key, "profileDigest": digest, "image": stringSchema(512), "prompt": stringSchema(32768), "dependsOn": nullableArray(key, 32), "scopes": array(scope, 1, 16), "checks": nullableArray(check, 16), "timeoutSeconds": bounded(1, 1800)}, "id", "perspective", "profile", "prompt", "scopes", "timeoutSeconds")
	return schema(map[string]any{"schemaVersion": map[string]any{"type": "integer", "const": 1}, "graphId": id, "graphDigest": digest, "packages": array(pin, 1, 16), "repositories": array(repo, 1, 16), "tasks": array(task, 1, 32), "maxParallel": bounded(1, 4)}, "schemaVersion", "graphId", "graphDigest", "packages", "repositories", "tasks", "maxParallel")
}
