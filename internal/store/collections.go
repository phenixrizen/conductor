package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

// CollectionWork is available only to the trusted worker's database boundary.
// Binding is a random request nonce, never a client credential or source payload.
type CollectionWork struct {
	Collection    domain.Collection
	Binding       string
	BindingDigest string
	Integration   domain.ContextIntegration
}

func (p *Postgres) applyContextIntegration(ctx context.Context, tx pgx.Tx, c domain.ContextIntegrationConfig) error {
	var source domain.ContextSource
	err := tx.QueryRow(ctx, `SELECT id,workspace_id,provider,host,provider_id FROM managed_repositories WHERE id=$1 AND workspace_id=$2 FOR SHARE`, c.RepositoryID, c.WorkspaceID).Scan(&source.RepositoryID, &source.WorkspaceID, &source.Provider, &source.Host, &source.ProviderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	source.Profile, source.Locator, source.IntegrationVersion = c.Profile, c.Locator, 1
	if err = domain.ValidateContextSource(source); err != nil {
		return err
	}
	// Store the profile/reference, not secret material. Version changes are audit
	// provenance; collection retries compare their immutable binding separately.
	_, err = tx.Exec(ctx, `INSERT INTO context_integrations(repository_id,workspace_id,version,configuration,enabled) VALUES($1,$2,1,$3,$4) ON CONFLICT(repository_id) DO UPDATE SET version=context_integrations.version+1,configuration=excluded.configuration,enabled=excluded.enabled WHERE context_integrations.workspace_id=excluded.workspace_id`, c.RepositoryID, c.WorkspaceID, c, c.Enabled)
	return err
}

func readContextIntegration(ctx context.Context, q queryExecutor, repo string) (domain.ContextIntegration, error) {
	var in domain.ContextIntegration
	var c domain.ContextIntegrationConfig
	err := q.QueryRow(ctx, `SELECT r.id,r.workspace_id,r.provider,r.host,r.provider_id,i.version,i.configuration,i.enabled FROM managed_repositories r JOIN context_integrations i ON i.repository_id=r.id WHERE r.id=$1 FOR SHARE OF r,i`, repo).Scan(&in.Source.RepositoryID, &in.Source.WorkspaceID, &in.Source.Provider, &in.Source.Host, &in.Source.ProviderID, &in.Source.IntegrationVersion, &c, &in.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, domain.ErrUnavailable
	}
	if err != nil {
		return in, err
	}
	in.Source.Profile, in.Source.Locator, in.CredentialID = c.Profile, c.Locator, c.CredentialID
	if err = domain.ValidateContextSource(in.Source); err != nil {
		return in, err
	}
	return in, nil
}

func (p *Postgres) RequestCollection(ctx context.Context, id, binding, key string, input domain.CollectionInput) (domain.Collection, error) {
	if p.access == nil || p.access.request.RepositoryID == "" {
		return domain.Collection{}, domain.ErrForbidden
	}
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(binding, 32) || domain.ValidateCollectionKey(key) != nil {
		return domain.Collection{}, domain.ErrInvalidInput
	}
	in, err := domain.NormalizeCollectionInput(input)
	if err != nil {
		return domain.Collection{}, err
	}
	if err = p.authorizeRepository(ctx, p.tx, p.access.request.RepositoryID, "author"); err != nil {
		return domain.Collection{}, err
	}
	// A short per-repository admission lock protects both idempotency and capacity.
	// It never spans Temporal/provider I/O or serializes unrelated repositories.
	if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "context-admission:"+p.access.request.RepositoryID); err != nil {
		return domain.Collection{}, err
	}
	digest, err := domain.JSONDigest(in)
	if err != nil {
		return domain.Collection{}, err
	}
	var existingID, existingDigest string
	err = p.tx.QueryRow(ctx, `SELECT id,input_digest FROM context_collections WHERE repository_id=$1 AND requester_id=$2 AND idempotency_key=$3`, p.access.request.RepositoryID, p.Principal().ID, key).Scan(&existingID, &existingDigest)
	if err == nil {
		if existingDigest != digest {
			return domain.Collection{}, domain.ErrIdempotencyConflict
		}
		return p.Collection(ctx, existingID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Collection{}, err
	}
	integration, err := readContextIntegration(ctx, p.tx, p.access.request.RepositoryID)
	if err != nil {
		return domain.Collection{}, err
	}
	if !integration.Enabled {
		return domain.Collection{}, domain.ErrUnavailable
	}
	var count int
	err = p.tx.QueryRow(ctx, `SELECT count(*) FROM context_collections c LEFT JOIN context_receipts r ON r.collection_id=c.id LEFT JOIN context_execution_observations e ON e.collection_id=c.id WHERE c.repository_id=$1 AND r.collection_id IS NULL AND (e.state IS NULL OR e.state NOT IN ('completed','cancelled','failed','timed_out','terminated'))`, p.access.request.RepositoryID).Scan(&count)
	if err != nil {
		return domain.Collection{}, err
	}
	if count >= 20 {
		return domain.Collection{}, domain.ErrCapacity
	}
	bound, err := domain.ContextBindingDigest(integration)
	if err != nil {
		return domain.Collection{}, err
	}
	_, err = p.tx.Exec(ctx, `INSERT INTO context_collections(id,binding,workspace_id,repository_id,requester_id,idempotency_key,input,input_digest,integration,binding_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, binding, p.access.request.WorkspaceID, p.access.request.RepositoryID, p.Principal().ID, key, in, digest, integration, bound)
	if err != nil {
		return domain.Collection{}, err
	}
	if _, err = p.tx.Exec(ctx, `INSERT INTO context_outbox(collection_id,operation) VALUES($1,'start')`, id); err != nil {
		return domain.Collection{}, err
	}
	if err = contextAudit(ctx, p.tx, id, "collection.requested", p.Principal().ID, map[string]any{"inputDigest": digest}); err != nil {
		return domain.Collection{}, err
	}
	return p.Collection(ctx, id)
}

func readCollection(ctx context.Context, q queryExecutor, id string, lock bool) (CollectionWork, error) {
	var work CollectionWork
	c := &work.Collection
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	err := q.QueryRow(ctx, `SELECT id,binding,workspace_id,repository_id,requester_id,input,input_digest,integration,binding_digest,created_at,cancel_requested_at FROM context_collections WHERE id=$1`+suffix, id).Scan(&c.ID, &work.Binding, &c.WorkspaceID, &c.RepositoryID, &c.RequesterID, &c.Input, &c.InputDigest, &work.Integration, &work.BindingDigest, &c.CreatedAt, &c.CancelRequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return work, domain.ErrNotFound
	}
	if err != nil {
		return work, err
	}
	c.Source = work.Integration.Source
	c.CreatedAt = c.CreatedAt.UTC()
	normalizeOptionalTime(c.CancelRequestedAt)
	var receipt domain.CollectionReceipt
	err = q.QueryRow(ctx, `SELECT collection_id,digest,snapshot,created_at FROM context_receipts WHERE collection_id=$1`, id).Scan(&receipt.ID, &receipt.Digest, &receipt.Snapshot, &receipt.CreatedAt)
	if err == nil {
		receipt.CreatedAt = receipt.CreatedAt.UTC()
		c.Receipt = &receipt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return work, err
	}
	var execution domain.CollectionExecution
	err = q.QueryRow(ctx, `SELECT namespace,workflow_id,run_id,state,observed_at,(state NOT IN ('unavailable','unresolved') AND observed_at > clock_timestamp()-interval '30 seconds') FROM context_execution_observations WHERE collection_id=$1`, id).Scan(&execution.Namespace, &execution.WorkflowID, &execution.RunID, &execution.State, &execution.ObservedAt, &execution.Current)
	if err == nil {
		execution.ObservedAt = execution.ObservedAt.UTC()
		c.Execution = &execution
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return work, err
	}
	return work, nil
}

func normalizeOptionalTime(value *time.Time) {
	if value != nil {
		*value = value.UTC()
	}
}

func (p *Postgres) authorizeCollection(ctx context.Context, c domain.Collection, action string) error {
	if p.access == nil || c.WorkspaceID != p.access.request.WorkspaceID || (p.access.request.RepositoryID != "" && c.RepositoryID != p.access.request.RepositoryID) {
		return domain.ErrNotFound
	}
	return p.authorizeRepository(ctx, p.queries(), c.RepositoryID, action)
}

func (p *Postgres) Collection(ctx context.Context, id string) (domain.Collection, error) {
	work, err := readCollection(ctx, p.queries(), id, false)
	if err != nil {
		return domain.Collection{}, err
	}
	if err = p.authorizeCollection(ctx, work.Collection, "read"); err != nil {
		return domain.Collection{}, err
	}
	return work.Collection, nil
}

func (p *Postgres) Collections(ctx context.Context, before string, limit int) (domain.CollectionPage, error) {
	page := domain.CollectionPage{Collections: make([]domain.CollectionSummary, 0)}
	if p.access == nil {
		return page, domain.ErrForbidden
	}
	cursor, err := domain.ValidateChangeQuery("", before, limit)
	if err != nil {
		return page, err
	}
	var date *time.Time
	var id string
	if cursor != nil {
		date, id = &cursor.CreatedAt, cursor.ID
	}
	rows, err := p.queries().Query(ctx, `SELECT c.id,c.workspace_id,c.repository_id,c.requester_id,c.input->>'commit',c.created_at,c.cancel_requested_at,COALESCE(r.digest,''),CASE WHEN e.collection_id IS NULL THEN NULL ELSE jsonb_build_object('namespace',e.namespace,'workflowId',e.workflow_id,'runId',e.run_id,'state',e.state,'observedAt',e.observed_at,'current',(e.state NOT IN ('unavailable','unresolved') AND e.observed_at > clock_timestamp()-interval '30 seconds')) END FROM context_collections c LEFT JOIN context_receipts r ON r.collection_id=c.id LEFT JOIN context_execution_observations e ON e.collection_id=c.id WHERE c.workspace_id=$1 AND ($2::text='' OR c.repository_id=$2) AND EXISTS(SELECT 1 FROM repository_grants g WHERE g.repository_id=c.repository_id AND g.principal_id=$3 AND g.can_read) AND ($4::timestamptz IS NULL OR (c.created_at,c.id)<($4,$5::text)) ORDER BY c.created_at DESC,c.id DESC LIMIT $6`, p.access.request.WorkspaceID, p.access.request.RepositoryID, p.Principal().ID, date, id, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var c domain.CollectionSummary
		if err = rows.Scan(&c.ID, &c.WorkspaceID, &c.RepositoryID, &c.RequesterID, &c.Commit, &c.CreatedAt, &c.CancelRequestedAt, &c.ReceiptDigest, &c.Execution); err != nil {
			return page, err
		}
		if len(page.Collections) == limit {
			last := page.Collections[limit-1]
			page.NextBefore, err = domain.EncodeChangeCursor(last.CreatedAt, last.ID)
			if err != nil {
				return page, err
			}
			break
		}
		c.CreatedAt = c.CreatedAt.UTC()
		normalizeOptionalTime(c.CancelRequestedAt)
		if c.Execution != nil {
			c.Execution.ObservedAt = c.Execution.ObservedAt.UTC()
		}
		page.Collections = append(page.Collections, c)
	}
	return page, rows.Err()
}

func (p *Postgres) CancelCollection(ctx context.Context, id string) (domain.Collection, error) {
	work, err := readCollection(ctx, p.queries(), id, true)
	if err != nil {
		return domain.Collection{}, err
	}
	c := work.Collection
	if err = p.authorizeCollection(ctx, c, "author"); err != nil {
		return domain.Collection{}, err
	}
	if c.RequesterID != p.Principal().ID {
		return domain.Collection{}, domain.ErrForbidden
	}
	if c.Receipt != nil || c.CancelRequestedAt != nil {
		return c, nil
	}
	if _, err = p.tx.Exec(ctx, `UPDATE context_collections SET cancel_requested_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return c, err
	}
	if _, err = p.tx.Exec(ctx, `INSERT INTO context_outbox(collection_id,operation) VALUES($1,'cancel') ON CONFLICT DO NOTHING`, id); err != nil {
		return c, err
	}
	if err = contextAudit(ctx, p.tx, id, "collection.cancellation_requested", p.Principal().ID, nil); err != nil {
		return c, err
	}
	return p.Collection(ctx, id)
}

func contextAudit(ctx context.Context, q queryExecutor, id, event, actor string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	_, err := q.Exec(ctx, `INSERT INTO context_audit_events(collection_id,event_type,actor,data) VALUES($1,$2,$3,$4)`, id, event, actor, data)
	return err
}

// CollectionWork resolves a server-generated nonce. It deliberately permits a
// trusted retry to recover a receipt committed before later permission revocation.
func (p *Postgres) CollectionWork(ctx context.Context, id, binding string) (CollectionWork, error) {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(binding, 32) {
		return CollectionWork{}, domain.ErrInvalidInput
	}
	work, err := readCollection(ctx, p.queries(), id, false)
	if err != nil {
		return work, err
	}
	if work.Binding != binding {
		return CollectionWork{}, domain.ErrForbidden
	}
	return work, nil
}

func (p *Postgres) workerGate(ctx context.Context, work CollectionWork) (*Postgres, error) {
	var identity domain.AccessIdentity
	err := p.pool.QueryRow(ctx, `SELECT issuer,subject FROM access_principals WHERE id=$1`, work.Collection.RequesterID).Scan(&identity.Issuer, &identity.Subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	gate, err := p.BeginAccess(ctx, domain.AccessRequest{Identity: identity, WorkspaceID: work.Collection.WorkspaceID, RepositoryID: work.Collection.RepositoryID})
	if err != nil {
		return nil, err
	}
	return gate.(*Postgres), nil
}

func (p *Postgres) checkCollectionWork(ctx context.Context, work CollectionWork) error {
	if work.Collection.CancelRequestedAt != nil {
		return domain.ErrCollectionStopped
	}
	if err := p.authorizeCollection(ctx, work.Collection, "author"); err != nil {
		return err
	}
	integration, err := readContextIntegration(ctx, p.queries(), work.Collection.RepositoryID)
	if err != nil {
		return err
	}
	if !integration.Enabled {
		return domain.ErrCollectionStopped
	}
	digest, err := domain.ContextBindingDigest(integration)
	if err != nil {
		return err
	}
	if digest != work.BindingDigest {
		return domain.ErrCollectionStopped
	}
	return nil
}

func (p *Postgres) CheckCollectionWork(ctx context.Context, id, binding string) error {
	work, err := p.CollectionWork(ctx, id, binding)
	if err != nil {
		return err
	}
	gate, err := p.workerGate(ctx, work)
	if err != nil {
		return err
	}
	defer gate.Rollback(ctx)
	work, err = readCollection(ctx, gate.tx, id, true)
	if err != nil {
		return err
	}
	if err = gate.checkCollectionWork(ctx, work); err != nil {
		return err
	}
	return gate.Commit(ctx)
}

func (p *Postgres) CompleteCollection(ctx context.Context, id, binding string, artifacts []domain.ContextArtifact) (domain.CollectionReceipt, error) {
	return p.completeCollection(ctx, id, binding, artifacts, nil)
}

// CompleteCollectionWithCodeGraph commits source and derived index together.
func (p *Postgres) CompleteCollectionWithCodeGraph(ctx context.Context, id, binding string, artifacts []domain.ContextArtifact, index domain.CodeGraphIndex) (domain.CollectionReceipt, error) {
	if err := domain.ValidateCodeGraphIndex(index, artifacts); err != nil {
		return domain.CollectionReceipt{}, err
	}
	return p.completeCollection(ctx, id, binding, artifacts, &index)
}

func (p *Postgres) completeCollection(ctx context.Context, id, binding string, artifacts []domain.ContextArtifact, index *domain.CodeGraphIndex) (domain.CollectionReceipt, error) {
	work, err := p.CollectionWork(ctx, id, binding)
	if err != nil {
		return domain.CollectionReceipt{}, err
	}
	if work.Collection.Receipt != nil {
		return *work.Collection.Receipt, nil
	}
	gate, err := p.workerGate(ctx, work)
	if err != nil {
		// A concurrent attempt can commit while this one waits behind revocation.
		// Recover only an immutable receipt under the same trusted nonce; denial
		// still forbids fresh provider work and never opens public inspection.
		if latest, readErr := p.CollectionWork(ctx, id, binding); readErr == nil && latest.Collection.Receipt != nil {
			return *latest.Collection.Receipt, nil
		}
		return domain.CollectionReceipt{}, err
	}
	defer gate.Rollback(ctx)
	work, err = readCollection(ctx, gate.tx, id, true)
	if err != nil {
		return domain.CollectionReceipt{}, err
	}
	// Another attempt may have committed after the first lookup. Its immutable
	// receipt wins, regardless of later transient availability or timestamps.
	if work.Collection.Receipt != nil {
		return *work.Collection.Receipt, nil
	}
	if err = gate.checkCollectionWork(ctx, work); err != nil {
		return domain.CollectionReceipt{}, err
	}
	var now time.Time
	if err = gate.tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return domain.CollectionReceipt{}, err
	}
	now = now.UTC()
	source := work.Collection.Source
	snapshot := domain.RepositoryContext{SchemaVersion: 2, Repository: source.Provider + ":" + source.Host + ":" + source.ProviderID, Commit: work.Collection.Input.Commit, RequestedRef: work.Collection.Input.Commit, CollectedAt: now, Collector: domain.RemoteContextCollector, Artifacts: artifacts, CollectionID: id, Source: &source}
	digest, err := domain.JSONDigest(snapshot)
	if err != nil {
		return domain.CollectionReceipt{}, err
	}
	receipt := domain.CollectionReceipt{ID: id, Digest: digest, Snapshot: snapshot, CreatedAt: now}
	if err = domain.ValidateCollectionReceipt(receipt, work.Collection); err != nil {
		return receipt, err
	}
	if _, err = gate.tx.Exec(ctx, `INSERT INTO context_receipts(collection_id,digest,snapshot,created_at) VALUES($1,$2,$3,$4)`, id, digest, snapshot, now); err != nil {
		return receipt, err
	}
	if index != nil {
		indexDigest, e := domain.JSONDigest(index)
		if e != nil {
			return receipt, e
		}
		if _, e = gate.tx.Exec(ctx, `INSERT INTO context_codegraph_indexes(collection_id,receipt_digest,index_digest,index_data) VALUES($1,$2,$3,$4)`, id, digest, indexDigest, index); e != nil {
			return receipt, e
		}
	}
	if err = contextAudit(ctx, gate.tx, id, "collection.receipt_recorded", work.Collection.RequesterID, map[string]any{"digest": digest}); err != nil {
		return receipt, err
	}
	if err = gate.Commit(ctx); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func (p *Postgres) validateReceiptLink(ctx context.Context, q queryExecutor, content domain.Content, workspace, repository string) error {
	value, ok := content["repositoryContext"]
	if !ok {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// This persistence guard introduces authority only for remote version 2.
	// Legacy stored metadata remains readable under its original validation rules.
	var version struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err = json.Unmarshal(data, &version); err != nil || version.SchemaVersion != 2 {
		return nil
	}
	var snapshot domain.RepositoryContext
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return domain.ErrInvalidInput
	}
	if p.access == nil || snapshot.Source == nil || snapshot.Source.WorkspaceID != workspace || snapshot.Source.RepositoryID != repository {
		return domain.ErrForbidden
	}
	// Compare the complete JSON document. Comparing only recognized struct fields
	// would let unverified extensions masquerade as part of a trusted receipt.
	var persisted, candidate any
	if err = json.Unmarshal(data, &candidate); err != nil {
		return domain.ErrInvalidInput
	}
	err = q.QueryRow(ctx, `SELECT r.snapshot FROM context_receipts r JOIN context_collections c ON c.id=r.collection_id WHERE c.id=$1 AND c.workspace_id=$2 AND c.repository_id=$3`, snapshot.CollectionID, workspace, repository).Scan(&persisted)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(candidate, persisted) {
		return fmt.Errorf("%w: context does not match the stored collection receipt", domain.ErrInvalidInput)
	}
	return nil
}

func (p *Postgres) AttachCollection(ctx context.Context, changeID string, expected int64, id, digest string) (domain.Package, error) {
	if p.access == nil {
		return domain.Package{}, domain.ErrForbidden
	}
	if _, err := p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, changeID); err != nil {
		return domain.Package{}, err
	}
	pkg, err := p.Get(ctx, changeID)
	if err != nil {
		return pkg, err
	}
	if err = p.authorizeChange(ctx, p.tx, changeID, "author"); err != nil {
		return pkg, err
	}
	if pkg.Revision.Number != expected {
		return pkg, domain.ErrConflict
	}
	collection, err := p.Collection(ctx, id)
	if err != nil {
		return pkg, err
	}
	if collection.WorkspaceID != pkg.WorkspaceID || collection.RepositoryID != pkg.RepositoryID {
		return pkg, domain.ErrNotFound
	}
	if collection.Receipt == nil {
		return pkg, domain.ErrUnavailable
	}
	if collection.Receipt.Digest != digest {
		return pkg, domain.ErrConflict
	}
	content := make(domain.Content, len(pkg.Revision.Content)+1)
	for key, value := range pkg.Revision.Content {
		content[key] = value
	}
	content["repositoryContext"] = collection.Receipt.Snapshot
	if err = domain.ValidateContent(content); err != nil {
		return pkg, err
	}
	newDigest, err := domain.Digest(content)
	if err != nil {
		return pkg, err
	}
	// Revisions cannot repeat an earlier digest. Detect a historical reattachment
	// under the package lock so A→B→A is a conflict, not a database constraint error.
	var seen bool
	if err = p.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM work_package_revisions WHERE change_id=$1 AND digest=$2)`, changeID, newDigest).Scan(&seen); err != nil {
		return pkg, err
	}
	if seen {
		return pkg, domain.ErrConflict
	}
	updated, err := p.Revise(ctx, changeID, p.Principal().ID, expected, content, newDigest, time.Now().UTC())
	if err != nil {
		return pkg, err
	}
	if err = contextAudit(ctx, p.tx, id, "collection.attached", p.Principal().ID, map[string]any{"changeId": changeID, "revision": updated.Revision.Number, "digest": digest}); err != nil {
		return pkg, err
	}
	return updated, nil
}
