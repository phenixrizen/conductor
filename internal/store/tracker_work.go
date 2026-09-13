package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

type TrackerWork struct {
	Sync           domain.TrackerSync
	Link           domain.TrackerLink
	Config         domain.TrackerConfig
	ConfigVersion  int64
	Binding        string
	WriteAttempted bool
}

func (p *Postgres) TrackerWork(ctx context.Context, id, binding string) (TrackerWork, error) {
	var w TrackerWork
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(binding, 32) {
		return w, domain.ErrInvalidInput
	}
	e := p.queries().QueryRow(ctx, `SELECT binding,config_version FROM tracker_syncs WHERE id=$1`, id).Scan(&w.Binding, &w.ConfigVersion)
	if errors.Is(e, pgx.ErrNoRows) {
		return w, domain.ErrNotFound
	}
	if e != nil {
		return w, e
	}
	if w.Binding != binding {
		return w, domain.ErrForbidden
	}
	w.Sync, e = readTrackerSync(ctx, p.queries(), id)
	if e != nil {
		return w, e
	}
	w.Link, e = readTrackerLink(ctx, p.queries(), w.Sync.LinkID)
	if e != nil {
		return w, e
	}
	if w.Sync.Observation != nil {
		return w, nil
	}
	w.Config, _, e = readTrackerConfig(ctx, p.queries(), w.Link.WorkspaceID)
	if e != nil {
		return w, e
	}
	e = p.queries().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tracker_write_attempts WHERE sync_id=$1)`, id).Scan(&w.WriteAttempted)
	return w, e
}
func (p *Postgres) trackerWorkerGate(ctx context.Context, w TrackerWork) (*Postgres, error) {
	var identity domain.AccessIdentity
	e := p.queries().QueryRow(ctx, `SELECT issuer,subject FROM access_principals WHERE id=$1`, w.Sync.ActorID).Scan(&identity.Issuer, &identity.Subject)
	if e != nil {
		return nil, domain.ErrForbidden
	}
	tx, e := p.BeginAccess(ctx, domain.AccessRequest{Identity: identity, WorkspaceID: w.Link.WorkspaceID})
	if e != nil {
		return nil, e
	}
	gate := tx.(*Postgres)
	_, version, _, e := gate.trackerPermission(ctx, w.Sync.Input.Mode)
	if e == nil && version != w.ConfigVersion {
		e = domain.ErrConflict
	}
	if e == nil {
		e = gate.trackerRepos(ctx, w.Link, "author")
	}
	if e != nil {
		_ = gate.Rollback(ctx)
		return nil, e
	}
	return gate, nil
}
func (p *Postgres) CheckTrackerWork(ctx context.Context, id, binding string) error {
	w, e := p.TrackerWork(ctx, id, binding)
	if e != nil {
		return e
	}
	g, e := p.trackerWorkerGate(ctx, w)
	if e != nil {
		return e
	}
	defer g.Rollback(ctx)
	return g.Commit(ctx)
}
func (p *Postgres) BeginTrackerWrite(ctx context.Context, id, binding string) (bool, error) {
	w, e := p.TrackerWork(ctx, id, binding)
	if e != nil {
		return false, e
	}
	if w.Sync.Input.Mode == "refresh" || w.Sync.Observation != nil {
		return false, domain.ErrForbidden
	}
	g, e := p.trackerWorkerGate(ctx, w)
	if e != nil {
		return false, e
	}
	defer g.Rollback(ctx)
	tag, e := g.tx.Exec(ctx, `INSERT INTO tracker_write_attempts(sync_id) VALUES($1) ON CONFLICT DO NOTHING`, id)
	if e != nil {
		return false, e
	}
	if e = g.Commit(ctx); e != nil {
		return false, e
	}
	return tag.RowsAffected() == 1, nil
}
func (p *Postgres) CompleteTrackerSync(ctx context.Context, id, binding string, o domain.TrackerObservation) (domain.TrackerObservation, error) {
	w, e := p.TrackerWork(ctx, id, binding)
	if e != nil {
		return o, e
	}
	if w.Sync.Observation != nil {
		return *w.Sync.Observation, nil
	}
	if o.State != "refreshed" && o.State != "synchronized" && o.State != "conflict" && o.State != "unknown" && o.State != "unavailable" {
		return o, domain.ErrInvalidInput
	}
	if o.ProjectionDigest != "" && !domain.IsLowerHex(o.ProjectionDigest, 64) || !domain.TrackerText(o.Code, 64) {
		return o, domain.ErrInvalidInput
	}
	if o.Issue != nil {
		if o.Issue.ID != w.Link.Input.IssueID || o.Issue.UpdatedAt.IsZero() {
			return o, domain.ErrInvalidInput
		}
		domain.MapTrackerStatus(w.Config, o.Issue)
	}
	if o.Projection == nil && o.ProjectionDigest != "" || o.Projection != nil && domain.TrackerDigest(o.Projection) != o.ProjectionDigest {
		return o, domain.ErrInvalidInput
	}
	encoded, err := json.Marshal(o)
	if err != nil || len(encoded) > 128<<10 {
		return o, domain.ErrInvalidInput
	}
	var tx pgx.Tx
	if o.Issue != nil {
		gate, err := p.trackerWorkerGate(ctx, w)
		if err != nil {
			return domain.TrackerObservation{}, err
		}
		tx = gate.tx
	} else {
		tx, e = p.pool.Begin(ctx)
		if e != nil {
			return o, e
		}
	}
	defer tx.Rollback(ctx)
	// A single immutable observation is the activity receipt, including explicit
	// unknown/failure states. It never authorizes a provider write on its own.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "tracker-result:"+id); e != nil {
		return o, e
	}
	var old domain.TrackerObservation
	e = tx.QueryRow(ctx, `SELECT data FROM tracker_observations WHERE sync_id=$1`, id).Scan(&old)
	if e == nil {
		return old, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return o, e
	}
	// A delayed read may finish after newer provider data. Preserve its receipt,
	// but never display it as a current authoritative snapshot.
	if o.Issue != nil {
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "tracker-observation:"+w.Sync.LinkID); e != nil {
			return o, e
		}
		var newer bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tracker_observations WHERE link_id=$1 AND (data->'issue'->>'updatedAt')::timestamptz > $2)`, w.Sync.LinkID, o.Issue.UpdatedAt).Scan(&newer)
		if e != nil {
			return o, e
		}
		if newer {
			o.State = "conflict"
			o.Code = "provider_snapshot_older"
		}
	}
	o.SyncID = id
	o.ObservedAt = time.Now().UTC()
	o.Current = false
	// Hash the returned JSONB representation: provider-owned JSON object key order
	// may normalize during persistence and must not change a retried receipt.
	if e = tx.QueryRow(ctx, `INSERT INTO tracker_observations(sync_id,link_id,data,created_at) VALUES($1,$2,$3,$4) RETURNING data`, id, w.Sync.LinkID, o, o.ObservedAt).Scan(&o); e != nil {
		return o, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO tracker_audit(link_id,actor_id,event_type,data) VALUES($1,$2,'tracker.sync_observed',$3)`, w.Sync.LinkID, w.Sync.ActorID, map[string]string{"syncId": id, "state": o.State, "code": o.Code}); e != nil {
		return o, e
	}
	return o, tx.Commit(ctx)
}

// TrackerWebhookConfig is private operator-runtime configuration, never an HTTP
// response. The receiver resolves the credential reference outside the database.
func (p *Postgres) TrackerWebhookConfig(ctx context.Context, workspace string) (domain.TrackerConfig, int64, error) {
	if domain.ValidateAccessID(workspace) != nil {
		return domain.TrackerConfig{}, 0, domain.ErrInvalidInput
	}
	return readTrackerConfig(ctx, p.queries(), workspace)
}
func (p *Postgres) AcceptTrackerEvent(ctx context.Context, workspace string, version int64, issueID, bodyDigest string) error {
	if !domain.IsLowerHex(bodyDigest, 64) {
		return domain.ErrInvalidInput
	}
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	c, current, e := readTrackerConfig(ctx, tx, workspace)
	if e != nil {
		return e
	}
	if current != version || !c.Enabled || !domain.TrackerRecordID(c.Provider, issueID) {
		return domain.ErrForbidden
	}
	tag, e := tx.Exec(ctx, `INSERT INTO tracker_inbox(workspace_id,body_digest,issue_id,config_version) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, workspace, bodyDigest, issueID, version)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	rows, e := tx.Query(ctx, `SELECT id,creator_id,digest FROM tracker_links WHERE workspace_id=$1 AND input->>'issueId'=$2 ORDER BY id LIMIT 101`, workspace, issueID)
	if e != nil {
		return e
	}
	type linked struct{ id, actor, digest string }
	links := []linked{}
	for rows.Next() {
		var l linked
		if e = rows.Scan(&l.id, &l.actor, &l.digest); e != nil {
			rows.Close()
			return e
		}
		links = append(links, l)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(links) > 100 {
		return domain.ErrUnavailable
	}
	for _, l := range links {
		// A webhook only requests an authoritative read under the original linking
		// principal. Provider payload fields cannot mutate planning or approval facts.
		id, e := trackerNonce()
		if e != nil {
			return e
		}
		binding, e := trackerNonce()
		if e != nil {
			return e
		}
		in := domain.TrackerSyncInput{LinkDigest: l.digest, Mode: "refresh"}
		// Bound pending work without losing a signed event while another read runs.
		// A rejected delivery rolls its inbox insert back so the provider can retry.
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "tracker-sync:"+l.id); e != nil {
			return e
		}
		var pending int
		e = tx.QueryRow(ctx, `SELECT count(*) FROM tracker_syncs s WHERE link_id=$1 AND NOT EXISTS(SELECT 1 FROM tracker_observations o WHERE o.sync_id=s.id)`, l.id).Scan(&pending)
		if e != nil {
			return e
		}
		if pending >= 100 {
			return domain.ErrUnavailable
		}
		if _, e = tx.Exec(ctx, `INSERT INTO tracker_syncs(id,link_id,actor_id,config_version,input,input_digest,binding,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, l.id, l.actor, version, in, domain.TrackerDigest(in), binding, "webhook-"+bodyDigest); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO tracker_sync_outbox(sync_id) VALUES($1)`, id); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO tracker_audit(link_id,actor_id,event_type,data) VALUES($1,$2,'tracker.sync_requested',$3)`, l.id, l.actor, map[string]string{"syncId": id, "mode": "refresh", "inboxDigest": bodyDigest}); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
