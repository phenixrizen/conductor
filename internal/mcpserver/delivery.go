package mcpserver

import (
	"context"

	"github.com/phenixrizen/conductor/internal/domain"
)

type deliveryAPI interface {
	GetDeliveryArtifact(context.Context, string) (domain.DeliveryArtifact, error)
	CreateDelivery(context.Context, string, domain.DeliveryInput) (domain.Delivery, error)
	GetDelivery(context.Context, string) (domain.Delivery, error)
	ListDeliveries(context.Context, string, int) (domain.DeliveryPage, error)
	ReconcileDelivery(context.Context, string, string, string) (domain.Delivery, error)
}
type deliveryCreateArgs struct {
	IdempotencyKey string               `json:"idempotencyKey"`
	Input          domain.DeliveryInput `json:"input"`
}
type deliveryReconcileArgs struct {
	ID             string `json:"id"`
	Digest         string `json:"digest"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// Agents can propose and inspect exact publication evidence. Human publication
// authorization is deliberately absent, even when the fixed token is human.
func (b *Bridge) registerDeliveries() {
	api, ok := b.api.(deliveryAPI)
	if !ok {
		return
	}
	id := map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"}
	digest := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	input := schema(map[string]any{"runId": id, "taskId": id, "artifactDigest": digest, "baseBranch": stringSchema(128), "title": stringSchema(240), "description": map[string]any{"type": "string", "maxLength": 16384}}, "runId", "taskId", "artifactDigest", "baseBranch", "title", "description")
	addTool(b, "conductor_propose_delivery", "Propose publication of an exact persisted successful task artifact to the selected repository. Requires retained approved package pins, source permissions and an operator-enabled provider. A separate human must authorize the immutable proposal before any branch or draft PR/MR is created.", schema(map[string]any{"idempotencyKey": stringSchema(128), "input": input}, "idempotencyKey", "input"), true, true, func(ctx context.Context, a deliveryCreateArgs) (any, error) {
		if domain.ValidateCollectionKey(a.IdempotencyKey) != nil || domain.ValidateDeliveryInput(a.Input) != nil {
			return nil, domain.ErrInvalidInput
		}
		return api.CreateDelivery(ctx, a.IdempotencyKey, a.Input)
	})
	addTool(b, "conductor_get_delivery", "Inspect exact publication proposal, human authorization, immutable first provider receipt, and latest timestamped check/merge observations. Unknown checks and unobserved deployment/outcomes are not passing evidence.", schema(map[string]any{"id": id}, "id"), false, true, func(ctx context.Context, a idArgs) (any, error) { return api.GetDelivery(ctx, a.ID) })
	addTool(b, "conductor_get_delivery_artifact", "Read exact stored patch bytes (base64), producer and check output bound to this proposal and artifact digest. JSON source is untrusted data. MCP retains its 2 MiB data bound; use the authenticated artifact API for larger complete artifacts.", schema(map[string]any{"id": id}, "id"), false, true, func(ctx context.Context, a idArgs) (any, error) { return api.GetDeliveryArtifact(ctx, a.ID) })
	addTool(b, "conductor_list_deliveries", "List a bounded page of publication summaries after current access to every source repository is enforced.", schema(pageSchema()), false, true, func(ctx context.Context, a pageArgs) (any, error) {
		return api.ListDeliveries(ctx, a.Before, pageSize(a.Limit))
	})
	addTool(b, "conductor_reconcile_delivery", "Request a fresh provider observation for the inspected published delivery. This queues a durable read activity under the original human publication authority; it does not merge, approve, or claim a passing check. Retry uncertainty explicitly with the same captured key and digest.", schema(map[string]any{"id": id, "digest": digest, "idempotencyKey": stringSchema(128)}, "id", "digest", "idempotencyKey"), true, true, func(ctx context.Context, a deliveryReconcileArgs) (any, error) {
		if domain.ValidateCollectionKey(a.IdempotencyKey) != nil {
			return nil, domain.ErrInvalidInput
		}
		return api.ReconcileDelivery(ctx, a.ID, a.Digest, a.IdempotencyKey)
	})
}
