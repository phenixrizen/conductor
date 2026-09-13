package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

func trackerNonce() (string, error) {
	var b [16]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func readTrackerConfig(ctx context.Context, q queryExecutor, workspace string) (domain.TrackerConfig, int64, error) {
	var c domain.TrackerConfig
	var version int64
	e := q.QueryRow(ctx, `SELECT config,version FROM workspace_trackers WHERE workspace_id=$1 FOR SHARE`, workspace).Scan(&c, &version)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return c, version, e
}

// ApplyTrackerConfig is reachable only through a trusted database operator. A
// workspace cannot swap provider identity underneath retained issue relationships.
func (p *Postgres) ApplyTrackerConfig(ctx context.Context, operator string, c domain.TrackerConfig) error {
	if domain.ValidateActor(operator) != nil || domain.ValidateTrackerConfig(c) != nil {
		return domain.ErrInvalidInput
	}
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "tracker-config:"+c.WorkspaceID); e != nil {
		return e
	}
	var old domain.TrackerConfig
	var version int64
	e = tx.QueryRow(ctx, `SELECT config,version FROM workspace_trackers WHERE workspace_id=$1 FOR UPDATE`, c.WorkspaceID).Scan(&old, &version)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if e == nil && (old.Provider != c.Provider || old.Host != c.Host || old.ScopeID != c.ScopeID || old.OrganizationID != c.OrganizationID || old.ConductorOrigin != c.ConductorOrigin) {
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tracker_links WHERE workspace_id=$1)`, c.WorkspaceID).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return domain.ErrConflict
		}
	}
	version++
	stored := c
	stored.Grants = nil
	if _, e = tx.Exec(ctx, `INSERT INTO workspace_trackers(workspace_id,version,config) VALUES($1,$2,$3) ON CONFLICT(workspace_id) DO UPDATE SET version=excluded.version,config=excluded.config`, c.WorkspaceID, version, stored); e != nil {
		return e
	}
	for _, g := range c.Grants {
		if _, e = tx.Exec(ctx, `INSERT INTO tracker_grants(workspace_id,principal_id,can_read,can_sync,can_resolve) VALUES($1,$2,$3,$4,$5) ON CONFLICT(workspace_id,principal_id) DO UPDATE SET can_read=excluded.can_read,can_sync=excluded.can_sync,can_resolve=excluded.can_resolve`, c.WorkspaceID, g.PrincipalID, g.CanRead, g.CanSync, g.CanResolve); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO tracker_config_audit(workspace_id,version,operator,config) VALUES($1,$2,$3,$4)`, c.WorkspaceID, version, operator, c); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (p *Postgres) trackerPermission(ctx context.Context, action string) (domain.TrackerConfig, int64, domain.TrackerGrant, error) {
	var c domain.TrackerConfig
	var g domain.TrackerGrant
	if p.access == nil {
		return c, 0, g, domain.ErrForbidden
	}
	c, v, e := readTrackerConfig(ctx, p.queries(), p.access.request.WorkspaceID)
	if e != nil {
		return c, v, g, e
	}
	e = p.queries().QueryRow(ctx, `SELECT principal_id,can_read,can_sync,can_resolve FROM tracker_grants WHERE workspace_id=$1 AND principal_id=$2 FOR SHARE`, p.access.request.WorkspaceID, p.Principal().ID).Scan(&g.PrincipalID, &g.CanRead, &g.CanSync, &g.CanResolve)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && !g.CanRead {
		return c, v, g, domain.ErrNotFound
	}
	if e != nil {
		return c, v, g, e
	}
	if action != "read" && (!c.Enabled || !g.CanSync) {
		return c, v, g, domain.ErrForbidden
	}
	if action == "restore" && (!g.CanResolve || p.Principal().Kind != "human") {
		return c, v, g, domain.ErrForbidden
	}
	return c, v, g, nil
}
func (p *Postgres) TrackerSettings(ctx context.Context) (domain.TrackerSettings, error) {
	var s domain.TrackerSettings
	c, v, g, e := p.trackerPermission(ctx, "read")
	if e != nil {
		return s, e
	}
	if p.access.request.RepositoryID != "" {
		if e = p.authorizeRepository(ctx, p.queries(), p.access.request.RepositoryID, "read"); e != nil {
			return s, e
		}
	}
	s = domain.TrackerSettings{WorkspaceID: c.WorkspaceID, Provider: c.Provider, Host: c.Host, OrganizationID: c.OrganizationID, ScopeID: c.ScopeID, Profile: c.Profile, Version: v, Enabled: c.Enabled, Statuses: c.Statuses, CanSync: g.CanSync && c.Enabled, CanResolve: g.CanResolve && c.Enabled && p.Principal().Kind == "human"}
	return s, nil
}
func (p *Postgres) trackerRepos(ctx context.Context, link domain.TrackerLink, action string) error {
	if p.access == nil || link.WorkspaceID != p.access.request.WorkspaceID {
		return domain.ErrNotFound
	}
	repos := map[string]bool{}
	for _, ref := range link.Input.Packages {
		repos[ref.RepositoryID] = true
	}
	if p.access.request.RepositoryID != "" && !repos[p.access.request.RepositoryID] {
		return domain.ErrNotFound
	}
	ids := []string{}
	for id := range repos {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if e := p.authorizeRepository(ctx, p.queries(), id, action); e != nil {
			return e
		}
	}
	return nil
}
func readTrackerLink(ctx context.Context, q queryExecutor, id string) (domain.TrackerLink, error) {
	var l domain.TrackerLink
	e := q.QueryRow(ctx, `SELECT id,workspace_id,creator_id,config_version,input,digest,projection,created_at FROM tracker_links WHERE id=$1`, id).Scan(&l.ID, &l.WorkspaceID, &l.CreatorID, &l.ConfigVersion, &l.Input, &l.Digest, &l.Projection, &l.CreatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return l, domain.ErrNotFound
	}
	if e != nil {
		return l, e
	}
	var o domain.TrackerObservation
	e = q.QueryRow(ctx, `SELECT data FROM tracker_observations WHERE link_id=$1 ORDER BY created_at DESC,sync_id DESC LIMIT 1`, id).Scan(&o)
	if e == nil {
		o.Current = o.Code != "provider_snapshot_older" && time.Since(o.ObservedAt) < 5*time.Minute
		l.Observation = &o
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return l, e
	}
	if e = q.QueryRow(ctx, `SELECT COALESCE((SELECT id FROM tracker_syncs WHERE link_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1),'')`, id).Scan(&l.LatestSyncID); e != nil {
		return l, e
	}
	return l, nil
}
func (p *Postgres) TrackerLink(ctx context.Context, id string) (domain.TrackerLink, error) {
	var l domain.TrackerLink
	if _, _, _, e := p.trackerPermission(ctx, "read"); e != nil {
		return l, e
	}
	l, e := readTrackerLink(ctx, p.queries(), id)
	if e != nil {
		return l, e
	}
	if e = p.trackerRepos(ctx, l, "read"); e != nil {
		return domain.TrackerLink{}, e
	}
	l.Publications, e = p.trackerPublications(ctx, l)
	if e != nil {
		return domain.TrackerLink{}, e
	}
	return l, nil
}
func (p *Postgres) CreateTrackerLink(ctx context.Context, id, key string, in domain.TrackerLinkInput) (domain.TrackerLink, error) {
	var l domain.TrackerLink
	c, v, _, e := p.trackerPermission(ctx, "sync")
	if e != nil {
		return l, e
	}
	if p.access.request.RepositoryID == "" || !domain.TrackerRecordID(c.Provider, in.IssueID) {
		return l, domain.ErrInvalidInput
	}
	l = domain.TrackerLink{ID: id, WorkspaceID: c.WorkspaceID, CreatorID: p.Principal().ID, ConfigVersion: v, Input: in}
	if e = p.trackerRepos(ctx, l, "author"); e != nil {
		return l, e
	}
	if _, e = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "tracker-link:"+c.WorkspaceID+":"+p.Principal().ID+":"+key); e != nil {
		return l, e
	}
	var oldID, oldHash string
	hash := domain.TrackerDigest(in)
	e = p.tx.QueryRow(ctx, `SELECT id,input_digest FROM tracker_links WHERE workspace_id=$1 AND creator_id=$2 AND idempotency_key=$3`, c.WorkspaceID, p.Principal().ID, key).Scan(&oldID, &oldHash)
	if e == nil {
		if oldHash != hash {
			return l, domain.ErrIdempotencyConflict
		}
		return p.TrackerLink(ctx, oldID)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return l, e
	}
	var summary strings.Builder
	for _, ref := range in.Packages {
		var digest string
		e = p.tx.QueryRow(ctx, `SELECT r.digest FROM work_package_revisions r JOIN changes c ON c.id=r.change_id WHERE r.change_id=$1 AND r.revision=$2 AND c.workspace_id=$3 AND c.repository_id=$4`, ref.PackageID, ref.Revision, c.WorkspaceID, ref.RepositoryID).Scan(&digest)
		if errors.Is(e, pgx.ErrNoRows) {
			return l, domain.ErrNotFound
		}
		if e != nil {
			return l, e
		}
		if digest != ref.Digest {
			return l, domain.ErrConflict
		}
		fmt.Fprintf(&summary, "Package %s revision %d digest %s (repository %s).\n", ref.PackageID, ref.Revision, ref.Digest, ref.RepositoryID)
	}
	publications, e := p.trackerPublications(ctx, l)
	if e != nil {
		return l, e
	}
	for _, publication := range publications {
		fact := publication.Receipt.Observation
		fmt.Fprintf(&summary, "%s %s repository %s publication %s #%d %s; receipt %s.\n", publication.Target.Provider, publication.Target.Host, publication.Target.ProviderID, fact.ProviderID, fact.Number, fact.URL, publication.Digest)
	}

	summary.WriteString("These links do not establish ticket completion, effective approval, passing checks, merge or deployment.")
	if summary.Len() > 32<<10 {
		return l, domain.ErrInvalidInput
	}
	l.Projection = domain.TrackerProjection{URL: c.ConductorOrigin + "/?tracker-link=" + id, Title: fmt.Sprintf("Conductor: %d linked work packages", len(in.Packages)), Summary: summary.String()}
	l.Digest = domain.TrackerDigest(struct {
		Input      domain.TrackerLinkInput
		Projection domain.TrackerProjection
	}{in, l.Projection})
	e = p.tx.QueryRow(ctx, `INSERT INTO tracker_links(id,workspace_id,creator_id,config_version,input,input_digest,digest,projection,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`, id, c.WorkspaceID, l.CreatorID, v, in, hash, l.Digest, l.Projection, key).Scan(&l.CreatedAt)
	if e != nil {
		return l, e
	}
	repos := map[string]bool{}
	for _, ref := range in.Packages {
		repos[ref.RepositoryID] = true
	}
	for repo := range repos {
		if _, e = p.tx.Exec(ctx, `INSERT INTO tracker_link_repositories(link_id,workspace_id,repository_id) VALUES($1,$2,$3)`, id, c.WorkspaceID, repo); e != nil {
			return l, e
		}
	}
	if _, e = p.tx.Exec(ctx, `INSERT INTO tracker_audit(link_id,actor_id,event_type,data) VALUES($1,$2,'tracker.linked',$3)`, id, l.CreatorID, map[string]string{"digest": l.Digest}); e != nil {
		return l, e
	}
	syncID, e := trackerNonce()
	if e != nil {
		return l, e
	}
	_, e = p.RequestTrackerSync(ctx, syncID, id, "initial-refresh", domain.TrackerSyncInput{LinkDigest: l.Digest, Mode: "refresh"})
	if e != nil {
		return l, e
	}
	return p.TrackerLink(ctx, id)
}
func (p *Postgres) TrackerLinks(ctx context.Context, before string, limit int) (domain.TrackerLinkPage, error) {
	page := domain.TrackerLinkPage{Links: []domain.TrackerLink{}}
	if _, _, _, e := p.trackerPermission(ctx, "read"); e != nil {
		return page, e
	}
	var ids []string
	rows, e := p.tx.Query(ctx, `SELECT l.id FROM tracker_links l WHERE l.workspace_id=$1 AND ($3::text='' OR l.id<$3) AND ($4::text='' OR EXISTS(SELECT 1 FROM tracker_link_repositories r WHERE r.link_id=l.id AND r.repository_id=$4)) AND NOT EXISTS(SELECT 1 FROM tracker_link_repositories r WHERE r.link_id=l.id AND NOT EXISTS(SELECT 1 FROM repository_grants g WHERE g.repository_id=r.repository_id AND g.principal_id=$2 AND g.can_read)) ORDER BY l.id DESC LIMIT $5`, p.access.request.WorkspaceID, p.Principal().ID, before, p.access.request.RepositoryID, limit+1)
	if e != nil {
		return page, e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return page, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return page, e
	}
	for _, id := range ids {
		l, e := p.TrackerLink(ctx, id)
		if e != nil {
			return page, e
		}
		if len(page.Links) == limit {
			page.Truncated = true
			page.Next = page.Links[len(page.Links)-1].ID
			break
		}
		page.Links = append(page.Links, l)
	}
	return page, nil
}
func readTrackerSync(ctx context.Context, q queryExecutor, id string) (domain.TrackerSync, error) {
	var s domain.TrackerSync
	e := q.QueryRow(ctx, `SELECT s.id,s.link_id,s.actor_id,s.input,s.created_at,CASE WHEN o.unresolved_at IS NOT NULL THEN 'unresolved' WHEN o.delivered_at IS NOT NULL THEN 'dispatched' ELSE 'pending' END FROM tracker_syncs s JOIN tracker_sync_outbox o ON o.sync_id=s.id WHERE s.id=$1`, id).Scan(&s.ID, &s.LinkID, &s.ActorID, &s.Input, &s.CreatedAt, &s.Dispatch)
	if errors.Is(e, pgx.ErrNoRows) {
		return s, domain.ErrNotFound
	}
	if e != nil {
		return s, e
	}
	var o domain.TrackerObservation
	e = q.QueryRow(ctx, `SELECT data FROM tracker_observations WHERE sync_id=$1`, id).Scan(&o)
	if e == nil {
		o.Current = o.Code != "provider_snapshot_older" && time.Since(o.ObservedAt) < 5*time.Minute
		s.Observation = &o
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return s, e
	}
	return s, nil
}
func (p *Postgres) TrackerSync(ctx context.Context, id string) (domain.TrackerSync, error) {
	s, e := readTrackerSync(ctx, p.queries(), id)
	if e != nil {
		return s, e
	}
	if _, e = p.TrackerLink(ctx, s.LinkID); e != nil {
		return domain.TrackerSync{}, e
	}
	return s, nil
}
func (p *Postgres) RequestTrackerSync(ctx context.Context, id, linkID, key string, in domain.TrackerSyncInput) (domain.TrackerSync, error) {
	var zero domain.TrackerSync
	_, v, _, e := p.trackerPermission(ctx, in.Mode)
	if e != nil {
		return zero, e
	}
	l, e := p.TrackerLink(ctx, linkID)
	if e != nil {
		return zero, e
	}
	if e = p.trackerRepos(ctx, l, "author"); e != nil {
		return zero, e
	}
	if in.LinkDigest != l.Digest {
		return zero, domain.ErrConflict
	}
	if _, e = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "tracker-sync:"+linkID); e != nil {
		return zero, e
	}
	var oldID, oldHash string
	hash := domain.TrackerDigest(in)
	e = p.tx.QueryRow(ctx, `SELECT id,input_digest FROM tracker_syncs WHERE link_id=$1 AND actor_id=$2 AND idempotency_key=$3`, linkID, p.Principal().ID, key).Scan(&oldID, &oldHash)
	if e == nil {
		if oldHash != hash {
			return zero, domain.ErrIdempotencyConflict
		}
		return p.TrackerSync(ctx, oldID)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return zero, e
	}
	if in.Mode != "refresh" {
		if l.Observation == nil || !l.Observation.Current || l.Observation.Issue == nil || l.Observation.ProjectionDigest != in.ExpectedProjectionDigest {
			return zero, domain.ErrConflict
		}
	}
	// Serial mutation admission prevents competing writers from treating the same
	// inspected projection as a current compare-and-swap baseline.
	var busy bool
	e = p.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tracker_syncs s WHERE link_id=$1 AND NOT EXISTS(SELECT 1 FROM tracker_observations o WHERE o.sync_id=s.id))`, linkID).Scan(&busy)
	if e != nil {
		return zero, e
	}
	if busy {
		return zero, domain.ErrConflict
	}
	binding, e := trackerNonce()
	if e != nil {
		return zero, e
	}
	_, e = p.tx.Exec(ctx, `INSERT INTO tracker_syncs(id,link_id,actor_id,config_version,input,input_digest,binding,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, linkID, p.Principal().ID, v, in, hash, binding, key)
	if e != nil {
		return zero, e
	}
	if _, e = p.tx.Exec(ctx, `INSERT INTO tracker_sync_outbox(sync_id) VALUES($1)`, id); e != nil {
		return zero, e
	}
	if _, e = p.tx.Exec(ctx, `INSERT INTO tracker_audit(link_id,actor_id,event_type,data) VALUES($1,$2,'tracker.sync_requested',$3)`, linkID, p.Principal().ID, map[string]string{"syncId": id, "mode": in.Mode}); e != nil {
		return zero, e
	}
	return readTrackerSync(ctx, p.tx, id)
}
