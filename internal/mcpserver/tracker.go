package mcpserver

import (
	"context"

	"github.com/phenixrizen/conductor/internal/domain"
)

type trackerAPI interface {
	GetTracker(context.Context) (domain.TrackerSettings, error)
	CreateTrackerLink(context.Context, string, domain.TrackerLinkInput) (domain.TrackerLink, error)
	GetTrackerLink(context.Context, string) (domain.TrackerLink, error)
	ListTrackerLinks(context.Context, string, int) (domain.TrackerLinkPage, error)
	RequestTrackerSync(context.Context, string, string, domain.TrackerSyncInput) (domain.TrackerSync, error)
	GetTrackerSync(context.Context, string) (domain.TrackerSync, error)
}
type trackerLinkArgs struct {
	IdempotencyKey string                         `json:"idempotencyKey"`
	IssueID        string                         `json:"issueId"`
	Packages       []domain.TrackerPackageRef     `json:"packages"`
	Publications   []domain.TrackerPublicationRef `json:"publications,omitempty"`
}
type trackerSyncArgs struct {
	ID                       string `json:"id"`
	IdempotencyKey           string `json:"idempotencyKey"`
	LinkDigest               string `json:"linkDigest"`
	Mode                     string `json:"mode"`
	ExpectedProjectionDigest string `json:"expectedProjectionDigest,omitempty"`
}

func (b *Bridge) registerTracker() {
	api, ok := b.api.(trackerAPI)
	if !ok {
		return
	}
	id := map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"}
	digest := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	addTool(b, "conductor_get_tracker", "Inspect the selected workspace's configured Linear or Jira profile, explicit status mappings and current sync permissions. Tracker planning state never grants package approval or proves checks, merge or deployment.", schema(map[string]any{}), false, true, func(ctx context.Context, _ struct{}) (any, error) { return api.GetTracker(ctx) })
	addTool(b, "conductor_list_tracker_links", "List a bounded page of shared ticket relationships under current tracker and every involved repository grant.", schema(pageSchema()), false, true, func(ctx context.Context, a pageArgs) (any, error) {
		return api.ListTrackerLinks(ctx, a.Before, pageSize(a.Limit))
	})
	addTool(b, "conductor_get_tracker_link", "Inspect exact linked package/publication references, Conductor projection and the latest authoritative tracker observation. Unknown, stale and conflicting states are not successful synchronization.", schema(map[string]any{"id": id}, "id"), false, true, func(ctx context.Context, a idArgs) (any, error) { return api.GetTrackerLink(ctx, a.ID) })
	addTool(b, "conductor_get_tracker_sync", "Inspect a retained tracker request and its immutable result, including uncertain provider writes and unresolved workflow history.", schema(map[string]any{"id": id}, "id"), false, true, func(ctx context.Context, a idArgs) (any, error) { return api.GetTrackerSync(ctx, a.ID) })
	packageRef := schema(map[string]any{"repositoryId": stringSchema(128), "packageId": stringSchema(128), "revision": positiveSchema(), "digest": digest}, "repositoryId", "packageId", "revision", "digest")
	publicationRef := schema(map[string]any{"id": id, "digest": digest}, "id", "digest")
	addTool(b, "conductor_link_tracker_issue", "Link one existing stable Linear issue UUID or Jira issue numeric ID to 1–16 exact inspected packages in this workspace, optionally including exact publication receipts. This schedules an authoritative refresh; it does not create a ticket or write its planning fields. Retry uncertainty only explicitly using the same key and input.", schema(map[string]any{"idempotencyKey": stringSchema(128), "issueId": stringSchema(64), "packages": map[string]any{"type": "array", "minItems": 1, "maxItems": 16, "items": packageRef}, "publications": map[string]any{"type": "array", "maxItems": 16, "items": publicationRef}}, "idempotencyKey", "issueId", "packages"), true, true, func(ctx context.Context, a trackerLinkArgs) (any, error) {
		return api.CreateTrackerLink(ctx, a.IdempotencyKey, domain.TrackerLinkInput{IssueID: a.IssueID, Packages: a.Packages, Publications: a.Publications})
	})
	addTool(b, "conductor_sync_tracker_link", "Request an authoritative refresh or explicitly publish the inspected Conductor link card. Publication requires the exact observed projection digest (omit for an inspected absent card). Competing edits require separate human conflict resolution; this tool cannot change ticket assignment, priority, status or design approvals.", schema(map[string]any{"id": id, "idempotencyKey": stringSchema(128), "linkDigest": digest, "mode": map[string]any{"type": "string", "enum": []string{"refresh", "publish"}}, "expectedProjectionDigest": digest}, "id", "idempotencyKey", "linkDigest", "mode"), true, true, func(ctx context.Context, a trackerSyncArgs) (any, error) {
		return api.RequestTrackerSync(ctx, a.ID, a.IdempotencyKey, domain.TrackerSyncInput{LinkDigest: a.LinkDigest, Mode: a.Mode, ExpectedProjectionDigest: a.ExpectedProjectionDigest})
	})
}
