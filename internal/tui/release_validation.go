package tui

import (
	"encoding/json"
	"reflect"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/reviewinput"
)

// A response must describe the exact selected record and captured input before
// it can restore inspection or enable a consequential control.
func (m releaseModel) validateRelease(r releaseResult) error {
	if r.op == "access" {
		if (r.view == "runs" || r.view == "deliveries") && r.caps.RepositoryID != m.access.repositoryID {
			return errResponseScope
		}
		if r.view == "runs" {
			if len(r.profiles.Profiles) > 100 {
				return errResponseScope
			}
			seen := map[string]bool{}
			for _, p := range r.profiles.Profiles {
				d, e := domain.JSONDigest(p.WorkerProfile)
				if e != nil || d != p.ProfileDigest || seen[p.ID] || domain.ValidateExecutionProfileConfig(domain.ExecutionProfileConfig{WorkspaceID: m.access.workspaceID, ID: p.ID, Image: p.Image, Profile: p.WorkerProfile, Enabled: true}) != nil {
					return errResponseScope
				}
				seen[p.ID] = true
			}
		}
		if r.view == "tracker" && r.tracker.WorkspaceID != m.access.workspaceID {
			return errResponseScope
		}
		return nil
	}
	if r.op == "file" {
		d, ok := r.value.(reviewinput.Draft)
		if !ok || d.Kind != r.kind || !domain.IsLowerHex(d.Digest, 64) {
			return errResponseScope
		}
		return nil
	}
	idDigest := func(id, digest string) bool { return domain.IsLowerHex(id, 32) && domain.IsLowerHex(digest, 64) }
	isRecord := r.op == "get" || r.op == "create" || r.op == "authorize" || r.op == "cancel"
	wantID := func(id string) bool {
		return r.op == "create" && r.draft.Kind != "tracker-sync" && r.draft.Kind != "delivery-reconcile" || id == r.id
	}
	sameInput := func(value any) bool {
		b, e := json.Marshal(value)
		return e == nil && string(b) == string(r.draft.Input)
	}
	switch v := r.value.(type) {
	case domain.RuntimePage:
		if r.view != "runtime" || r.op != "list" || len(v.Evidence) > pageSize || !validCollectionCursor(v.NextBefore) {
			return errResponseScope
		}
		seen := map[string]bool{}
		for _, row := range v.Evidence {
			if !idDigest(row.ID, row.Digest) || seen[row.ID] || row.RepositoryID != m.access.repositoryID || !domain.IsLowerHex(row.DeliveryID, 32) || !domain.IsLowerHex(row.Commit, 40) || row.ReceiptDigest != "" && !domain.IsLowerHex(row.ReceiptDigest, 64) {
				return errResponseScope
			}
			seen[row.ID] = true
		}
	case domain.RuntimeEvidence:
		if r.view != "runtime" || !isRecord || !wantID(v.ID) || m.validateRuntime(v) != nil {
			return errResponseScope
		}
		if r.op == "create" && (!sameInput(v.Input) || v.RequesterID != m.access.principal.ID) {
			return errResponseScope
		}
	case domain.CoordinationArtifact:
		run, ok := m.record.(domain.CoordinationRun)
		q := r.taskArtifact
		if !ok || r.op != "task-artifact" || r.view != "runs" || v.RunID != run.ID || v.RunID != r.id || v.RunDigest != run.Digest || v.RunDigest != q.RunDigest || v.TaskID != q.TaskID || v.ArtifactDigest != q.ArtifactDigest || len(v.Artifact) > 16<<20 {
			return errResponseScope
		}
		found := false
		for _, receipt := range run.Receipts {
			if receipt.TaskID == v.TaskID && receipt.ArtifactDigest == v.ArtifactDigest {
				found = true
			}
		}
		var result execution.Result
		if !found || json.Unmarshal(v.Artifact, &result) != nil {
			return errResponseScope
		}
		d, e := domain.JSONDigest(result)
		if e != nil || d != v.ArtifactDigest {
			return errResponseScope
		}
	case domain.RepositoryGraphPage:
		if r.view != "graphs" || r.op != "list" || len(v.Graphs) > pageSize || !validCollectionCursor(v.NextBefore) {
			return errResponseScope
		}
		seen := map[string]bool{}
		for _, g := range v.Graphs {
			if !idDigest(g.ID, g.Digest) || seen[g.ID] {
				return errResponseScope
			}
			seen[g.ID] = true
		}
	case domain.RepositoryGraph:
		d, e := domain.JSONDigest(v.Snapshot)
		if r.view != "graphs" || !isRecord || !wantID(v.ID) || !idDigest(v.ID, v.Digest) || v.WorkspaceID != m.access.workspaceID || e != nil || d != v.Digest || len(v.Snapshot.Nodes) > domain.MaxGraphNodes || len(v.Snapshot.Edges) > domain.MaxGraphEdges || len(v.Snapshot.Gaps) > domain.MaxGraphGaps {
			return errResponseScope
		}
		input := domain.GraphInput{}
		anchor := false
		for _, s := range v.Snapshot.Sources {
			input.Sources = append(input.Sources, s.GraphSource)
			anchor = anchor || s.RepositoryID == m.access.repositoryID
		}
		if !anchor {
			return errResponseScope
		}
		if _, e = domain.NormalizeGraphInput(input); e != nil {
			return errResponseScope
		}
		if r.op == "create" && !sameInput(input) {
			return errResponseScope
		}
	case domain.GraphArtifactResult:
		q := r.graphArtifact
		g, ok := m.record.(domain.RepositoryGraph)
		if !ok || r.op != "graph-artifact" || v.GraphID != r.id || v.GraphID != g.ID || v.Digest != q.GraphDigest || v.Digest != g.Digest || v.Source.RepositoryID != q.RepositoryID || v.Source.CollectionID != q.CollectionID || v.Source.Digest != q.ReceiptDigest || v.Source.FullSourceDigest != q.FullSourceDigest || v.Artifact.Path != q.Path {
			return errResponseScope
		}
		sourceOK := false
		for _, s := range g.Snapshot.Sources {
			if s.GraphSource == v.Source.GraphSource && s.Commit == v.Source.Commit && s.CollectedAt.Equal(v.Source.CollectedAt) && s.Freshness == v.Source.Freshness {
				sourceOK = true
			}
		}
		if !sourceOK {
			return errResponseScope
		}
		expectedCoverage := "selected_paths"
		if q.FullSourceDigest != "" {
			expectedCoverage = "full_source"
		}
		if v.Coverage != expectedCoverage || len(v.Artifact.Message) > 1024 {
			return errResponseScope
		}
		a := v.Artifact
		if a.State == "collected" {
			if !domain.ValidGitOID(a.BlobOID) || a.Text == nil || len(*a.Text) > domain.MaxContextArtifactBytes || !domain.IsContextText([]byte(*a.Text)) || execution.Sum([]byte(*a.Text)) != a.Digest {
				return errResponseScope
			}
		} else if a.Text != nil || a.Digest != "" || a.Message == "" || (a.State != "missing" && a.State != "unavailable" && a.State != "truncated") {
			return errResponseScope
		}
	case domain.GraphQueryResult:
		if r.op != "query" || v.GraphID != r.id || v.Digest != r.digest || len(v.Nodes) > r.query.Limit || len(v.Edges) > domain.MaxGraphEdges || len(v.Gaps) > domain.MaxGraphGaps {
			return errResponseScope
		}
		anchor := false
		for _, s := range v.Sources {
			anchor = anchor || s.RepositoryID == m.access.repositoryID
		}
		if !anchor {
			return errResponseScope
		}
	case domain.CoordinationPage:
		if r.view != "runs" || r.op != "list" || len(v.Runs) > pageSize || !validCollectionCursor(v.NextBefore) {
			return errResponseScope
		}
		seen := map[string]bool{}
		for _, run := range v.Runs {
			if !idDigest(run.ID, run.Digest) || run.WorkspaceID != m.access.workspaceID || seen[run.ID] || run.Tasks < 1 || run.Tasks > 32 || run.Receipts > run.Tasks {
				return errResponseScope
			}
			seen[run.ID] = true
		}
	case domain.CoordinationRun:
		p, e := domain.NormalizeCoordinationPlan(v.Plan)
		d, _ := domain.JSONDigest(v.Plan)
		if r.view != "runs" || !isRecord || !wantID(v.ID) || !idDigest(v.ID, v.Digest) || v.WorkspaceID != m.access.workspaceID || e != nil || !reflect.DeepEqual(p, v.Plan) || d != v.Digest || len(v.Receipts) > len(p.Tasks) {
			return errResponseScope
		}
		anchor := false
		for _, repo := range p.Repositories {
			anchor = anchor || repo.RepositoryID == m.access.repositoryID
		}
		if !anchor {
			return errResponseScope
		}
		if r.op == "create" && (!sameInput(v.Plan) || v.ProposerID != m.access.principal.ID) {
			return errResponseScope
		}
		if r.op == "authorize" && (v.Digest != r.digest || v.Authorization == nil || v.Authorization.Digest != r.digest || v.Authorization.Actor != m.access.principal.ID) {
			return errResponseScope
		}
		if r.op == "cancel" && (v.Digest != r.digest || v.CancelRequestedAt == nil) {
			return errResponseScope
		}
		if v.Authorization != nil && v.Authorization.Digest != v.Digest {
			return errResponseScope
		}
		keys := map[string]bool{}
		for _, t := range p.Tasks {
			keys[t.ID] = true
		}
		seen := map[string]bool{}
		for _, receipt := range v.Receipts {
			if !idDigest(receipt.TaskID, receipt.Digest) || !keys[receipt.TaskKey] || seen[receipt.TaskKey] || receipt.ArtifactDigest != "" && !domain.IsLowerHex(receipt.ArtifactDigest, 64) {
				return errResponseScope
			}
			seen[receipt.TaskKey] = true
		}
	case domain.DeliveryPage:
		if r.view != "deliveries" || r.op != "list" || len(v.Deliveries) > pageSize || !validCollectionCursor(v.NextBefore) {
			return errResponseScope
		}
		seen := map[string]bool{}
		for _, d := range v.Deliveries {
			if !idDigest(d.ID, d.Digest) || d.WorkspaceID != m.access.workspaceID || d.RepositoryID != m.access.repositoryID || seen[d.ID] {
				return errResponseScope
			}
			seen[d.ID] = true
		}
	case domain.Delivery:
		d, e := domain.DeliveryProposalDigest(v)
		if r.view != "deliveries" || !isRecord || !wantID(v.ID) || !idDigest(v.ID, v.Digest) || v.WorkspaceID != m.access.workspaceID || v.RepositoryID != m.access.repositoryID || e != nil || d != v.Digest || domain.ValidateDeliveryInput(v.Input) != nil {
			return errResponseScope
		}
		target, repo := v.Target, m.access.repository
		if target.WorkspaceID != v.WorkspaceID || target.RepositoryID != v.RepositoryID || target.Provider != repo.Provider || target.Host != repo.Host || target.ProviderID != repo.ProviderID {
			return errResponseScope
		}
		if r.op == "create" && r.draft.Kind == "delivery" && (!sameInput(v.Input) || v.ProposerID != m.access.principal.ID) {
			return errResponseScope
		}
		if r.op == "authorize" && (v.Digest != r.digest || v.Authorization == nil || v.Authorization.Digest != r.digest || v.Authorization.Actor != m.access.principal.ID) {
			return errResponseScope
		}
		if r.op == "create" && r.draft.Kind == "delivery-reconcile" && v.Digest != r.digest {
			return errResponseScope
		}
	case domain.DeliveryArtifact:
		delivery, ok := m.record.(domain.Delivery)
		if !ok || r.op != "artifact" || v.DeliveryID != r.id || v.DeliveryID != delivery.ID || v.DeliveryDigest != r.digest || v.DeliveryDigest != delivery.Digest || v.ArtifactDigest != delivery.Input.ArtifactDigest || len(v.Artifact) > 16<<20 {
			return errResponseScope
		}
		var artifact execution.Result
		if json.Unmarshal(v.Artifact, &artifact) != nil {
			return errResponseScope
		}
		d, e := domain.JSONDigest(artifact)
		if e != nil || d != v.ArtifactDigest || !artifact.CleanupConfirmed {
			return errResponseScope
		}
		found := false
		for _, p := range artifact.Patches {
			if p.RepositoryID == delivery.RepositoryID && p.BaseCommit == delivery.BaseCommit && p.BaseTree == delivery.BaseTree && p.ResultTree == delivery.ResultTree && p.Digest == delivery.PatchDigest && execution.Sum(p.Patch) == p.Digest {
				found = true
			}
		}
		if !found {
			return errResponseScope
		}
	case domain.TrackerLinkPage:
		if r.view != "tracker" || r.op != "list" || len(v.Links) > pageSize || !validCollectionCursor(v.Next) {
			return errResponseScope
		}
		seen := map[string]bool{}
		for _, link := range v.Links {
			if seen[link.ID] || m.validateTrackerLink(link) != nil {
				return errResponseScope
			}
			seen[link.ID] = true
		}
	case domain.TrackerLink:
		if r.view != "tracker" || !isRecord || !wantID(v.ID) || m.validateTrackerLink(v) != nil {
			return errResponseScope
		}
		if r.op == "create" && (!sameInput(v.Input) || v.CreatorID != m.access.principal.ID) {
			return errResponseScope
		}
	case domain.TrackerSync:
		link, ok := m.record.(domain.TrackerLink)
		if !ok || v.LinkID != link.ID || !domain.IsLowerHex(v.ID, 32) || v.Input.LinkDigest != link.Digest {
			return errResponseScope
		}
		if r.op == "sync" && v.ID != r.id {
			return errResponseScope
		}
		if r.op == "create" && (!sameInput(v.Input) || v.ActorID != m.access.principal.ID) {
			return errResponseScope
		}
	default:
		return errResponseScope
	}
	return nil
}
func (m releaseModel) validateTrackerLink(l domain.TrackerLink) error {
	if !domain.IsLowerHex(l.ID, 32) || l.WorkspaceID != m.access.workspaceID {
		return errResponseScope
	}
	input, e := domain.NormalizeTrackerLink(l.Input)
	if e != nil || !reflect.DeepEqual(input, l.Input) {
		return errResponseScope
	}
	d := domain.TrackerDigest(struct {
		Input      domain.TrackerLinkInput
		Projection domain.TrackerProjection
	}{l.Input, l.Projection})
	if d != l.Digest {
		return errResponseScope
	}
	anchor := false
	for _, p := range l.Input.Packages {
		anchor = anchor || p.RepositoryID == m.access.repositoryID
	}
	if !anchor {
		return errResponseScope
	}
	return nil
}
