package tui

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/reviewinput"
	"github.com/phenixrizen/conductor/pkg/client"
)

type releaseRequest struct {
	serial, generation                            int
	op, view, id, digest, path, kind, key, cursor string
	draft                                         reviewinput.Draft
	query                                         domain.GraphQuery
	graphArtifact                                 domain.GraphArtifactQuery
	taskArtifact                                  domain.CoordinationArtifactQuery
}
type releaseResult struct {
	releaseRequest
	value        any
	err          error
	session      domain.Session
	repositories domain.RepositoryPage
	caps         domain.ExecutionCapabilities
	profiles     domain.ExecutionProfilePage
	tracker      domain.TrackerSettings
}
type releaseExecutor func(releaseRequest) (tea.Cmd, context.CancelFunc)

func releaseRunner(ctx context.Context, c *client.Client) releaseExecutor {
	return func(req releaseRequest) (tea.Cmd, context.CancelFunc) {
		operation, cancel := context.WithTimeout(ctx, 30*time.Second)
		return func() tea.Msg {
			defer cancel()
			r := releaseResult{releaseRequest: req}
			switch req.op {
			case "access":
				r.session, r.err = c.Session(operation)
				if r.err == nil {
					r.repositories, r.err = c.Repositories(operation)
				}
				if r.err == nil && (req.view == "runs" || req.view == "deliveries") {
					r.caps, r.err = c.ExecutionCapabilities(operation)
				}
				if r.err == nil && req.view == "runs" {
					r.profiles, r.err = c.ExecutionProfiles(operation)
				}
				if r.err == nil && req.view == "tracker" {
					r.tracker, r.err = c.GetTracker(operation)
				}
			case "file":
				r.value, r.err = reviewinput.Read(operation, req.path, req.kind)
			case "list":
				switch req.view {
				case "runtime":
					r.value, r.err = c.ListRuntimeEvidence(operation, req.cursor, pageSize)
				case "graphs":
					r.value, r.err = c.ListRepositoryGraphs(operation, req.cursor, pageSize)
				case "runs":
					r.value, r.err = c.ListCoordinations(operation, req.cursor, pageSize)
				case "deliveries":
					r.value, r.err = c.ListDeliveries(operation, req.cursor, pageSize)
				case "tracker":
					r.value, r.err = c.ListTrackerLinks(operation, req.cursor, pageSize)
				}
			case "get":
				switch req.view {
				case "runtime":
					r.value, r.err = c.GetRuntimeEvidence(operation, req.id)
				case "graphs":
					r.value, r.err = c.GetRepositoryGraph(operation, req.id)
				case "runs":
					r.value, r.err = c.GetCoordination(operation, req.id)
				case "deliveries":
					r.value, r.err = c.GetDelivery(operation, req.id)
				case "tracker":
					r.value, r.err = c.GetTrackerLink(operation, req.id)
				}
			case "create":
				switch req.draft.Kind {
				case "runtime":
					var in domain.RuntimeInput
					_ = json.Unmarshal(req.draft.Input, &in)
					r.value, r.err = c.CreateRuntimeEvidence(operation, req.draft.IdempotencyKey, in)
				case "graph":
					var in domain.GraphInput
					_ = json.Unmarshal(req.draft.Input, &in)
					r.value, r.err = c.CreateRepositoryGraph(operation, req.draft.IdempotencyKey, in)
				case "run":
					var in domain.CoordinationPlan
					_ = json.Unmarshal(req.draft.Input, &in)
					r.value, r.err = c.CreateCoordination(operation, req.draft.IdempotencyKey, in)
				case "delivery":
					var in domain.DeliveryInput
					_ = json.Unmarshal(req.draft.Input, &in)
					r.value, r.err = c.CreateDelivery(operation, req.draft.IdempotencyKey, in)
				case "tracker-link":
					var in domain.TrackerLinkInput
					_ = json.Unmarshal(req.draft.Input, &in)
					r.value, r.err = c.CreateTrackerLink(operation, req.draft.IdempotencyKey, in)
				case "tracker-sync":
					var in domain.TrackerSyncInput
					_ = json.Unmarshal(req.draft.Input, &in)
					r.value, r.err = c.RequestTrackerSync(operation, req.id, req.draft.IdempotencyKey, in)
				case "delivery-reconcile":
					r.value, r.err = c.ReconcileDelivery(operation, req.id, req.digest, req.draft.IdempotencyKey)
				}
			case "authorize":
				if req.view == "runs" {
					r.value, r.err = c.AuthorizeCoordination(operation, req.id, req.digest)
				} else {
					r.value, r.err = c.AuthorizeDelivery(operation, req.id, req.digest)
				}
			case "cancel":
				r.value, r.err = c.CancelCoordination(operation, req.id, req.digest)
			case "task-artifact":
				r.value, r.err = c.GetCoordinationArtifact(operation, req.id, req.taskArtifact)
			case "graph-artifact":
				r.value, r.err = c.GetRepositoryGraphArtifact(operation, req.id, req.graphArtifact)
			case "artifact":
				r.value, r.err = c.GetDeliveryArtifact(operation, req.id)
			case "query":
				r.value, r.err = c.QueryRepositoryGraph(operation, req.id, req.query)
			case "sync":
				r.value, r.err = c.GetTrackerSync(operation, req.id)
			default:
				r.err = errors.New("unsupported workbench operation")
			}
			return r
		}, cancel
	}
}
