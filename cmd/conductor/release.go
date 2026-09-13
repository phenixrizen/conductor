package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/reviewinput"
	"github.com/phenixrizen/conductor/pkg/client"
)

type releaseOptions struct {
	file, digest, key, before, search, node string
	limit, depth                            int
	artifact                                domain.GraphArtifactQuery
}

func releaseCommand(op string) bool {
	switch op {
	case "graph-preview", "graph-create", "graphs", "graph", "graph-query", "graph-artifact", "run-preview", "run-propose", "runs", "run", "run-authorize", "run-cancel", "execution-profiles", "execution-capabilities", "delivery-preview", "delivery-propose", "deliveries", "delivery", "delivery-artifact", "delivery-authorize", "delivery-reconcile", "tracker", "tracker-links", "tracker-link", "tracker-link-preview", "tracker-link-create", "tracker-sync-preview", "tracker-sync", "tracker-sync-show":
		return true
	}
	return false
}
func releasePreview(op string) bool { return releaseCommand(op) && strings.HasSuffix(op, "-preview") }
func runReleaseCommand(ctx context.Context, c *client.Client, op string, args []string, o releaseOptions) (any, error) {
	kind := ""
	switch op {
	case "graph-preview", "graph-create":
		kind = "graph"
	case "run-preview", "run-propose":
		kind = "run"
	case "delivery-preview", "delivery-propose":
		kind = "delivery"
	case "tracker-link-preview", "tracker-link-create":
		kind = "tracker-link"
	case "tracker-sync-preview", "tracker-sync":
		kind = "tracker-sync"
	}
	if kind != "" {
		expectedArgs := 0
		if op == "tracker-sync" {
			expectedArgs = 1
		}
		if len(args) != expectedArgs || expectedArgs == 1 && !domain.IsLowerHex(args[0], 32) {
			return nil, errors.New("request file commands take no ID except tracker-sync, which requires one inspected link ID")
		}
		draft, err := reviewinput.Read(ctx, o.file, kind)
		if err != nil {
			return nil, err
		}
		if releasePreview(op) {
			return draft, nil
		}
		// The digest comes from a prior preview. No source or inspection is refreshed
		// inside this command, and the file's captured key is preserved on every retry.
		if o.digest != draft.Digest {
			return nil, errors.New("--digest must equal the complete request digest displayed by the matching preview command")
		}
		switch kind {
		case "graph":
			var in domain.GraphInput
			_ = json.Unmarshal(draft.Input, &in)
			return c.CreateRepositoryGraph(ctx, draft.IdempotencyKey, in)
		case "run":
			var in domain.CoordinationPlan
			_ = json.Unmarshal(draft.Input, &in)
			return c.CreateCoordination(ctx, draft.IdempotencyKey, in)
		case "delivery":
			var in domain.DeliveryInput
			_ = json.Unmarshal(draft.Input, &in)
			return c.CreateDelivery(ctx, draft.IdempotencyKey, in)
		case "tracker-link":
			var in domain.TrackerLinkInput
			_ = json.Unmarshal(draft.Input, &in)
			return c.CreateTrackerLink(ctx, draft.IdempotencyKey, in)
		case "tracker-sync":
			var in domain.TrackerSyncInput
			_ = json.Unmarshal(draft.Input, &in)
			return c.RequestTrackerSync(ctx, args[0], draft.IdempotencyKey, in)
		}
	}
	noID := op == "graphs" || op == "runs" || op == "deliveries" || op == "execution-profiles" || op == "execution-capabilities" || op == "tracker" || op == "tracker-links"
	if noID && len(args) != 0 || !noID && (len(args) != 1 || !domain.IsLowerHex(args[0], 32)) {
		return nil, errors.New("provide exactly one 32-character record ID after flags, or no ID for discovery")
	}
	if o.limit < 1 || o.limit > 100 {
		return nil, errors.New("--limit must be 1-100")
	}
	id := ""
	if len(args) == 1 {
		id = args[0]
	}
	if op == "run-authorize" || op == "run-cancel" || op == "delivery-authorize" || op == "delivery-reconcile" {
		if !domain.IsLowerHex(o.digest, 64) {
			return nil, errors.New("--digest must be the exact inspected record digest")
		}
	}
	switch op {
	case "graphs":
		return c.ListRepositoryGraphs(ctx, o.before, o.limit)
	case "graph":
		return c.GetRepositoryGraph(ctx, id)
	case "graph-artifact":
		if domain.ValidateGraphArtifactQuery(o.artifact) != nil {
			return nil, errors.New("graph-artifact requires the inspected --digest, --source-repository, --collection-id, --receipt-digest, optional --full-source-digest, and exactly one --path")
		}
		return c.GetRepositoryGraphArtifact(ctx, id, o.artifact)
	case "graph-query":
		q := domain.GraphQuery{Search: o.search, NodeID: o.node, Depth: o.depth, Limit: o.limit}
		if domain.ValidateGraphQuery(q) != nil {
			return nil, domain.ErrInvalidInput
		}
		return c.QueryRepositoryGraph(ctx, id, q)
	case "runs":
		return c.ListCoordinations(ctx, o.before, o.limit)
	case "run":
		return c.GetCoordination(ctx, id)
	case "run-authorize":
		return c.AuthorizeCoordination(ctx, id, o.digest)
	case "run-cancel":
		return c.CancelCoordination(ctx, id, o.digest)
	case "execution-profiles":
		return c.ExecutionProfiles(ctx)
	case "execution-capabilities":
		return c.ExecutionCapabilities(ctx)
	case "deliveries":
		return c.ListDeliveries(ctx, o.before, o.limit)
	case "delivery":
		return c.GetDelivery(ctx, id)
	case "delivery-artifact":
		return c.GetDeliveryArtifact(ctx, id)
	case "delivery-authorize":
		return c.AuthorizeDelivery(ctx, id, o.digest)
	case "delivery-reconcile":
		if domain.ValidateCollectionKey(o.key) != nil {
			return nil, errors.New("--idempotency-key is required")
		}
		return c.ReconcileDelivery(ctx, id, o.digest, o.key)
	case "tracker":
		return c.GetTracker(ctx)
	case "tracker-links":
		return c.ListTrackerLinks(ctx, o.before, o.limit)
	case "tracker-link":
		return c.GetTrackerLink(ctx, id)
	case "tracker-sync-show":
		return c.GetTrackerSync(ctx, id)
	}
	return nil, errors.New("unknown shared release command")
}
