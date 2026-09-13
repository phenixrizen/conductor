package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/delivery"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

type DeliveryWork struct {
	Delivery              domain.Delivery
	Binding, CredentialID string
	Plan                  domain.CoordinationPlan
	Patch                 execution.Patch
	Source                domain.CoordinationRepository
}

func deliveryAudit(ctx context.Context, q queryExecutor, d domain.Delivery, event, actor string, data any) error {
	_, err := q.Exec(ctx, `INSERT INTO delivery_audit_events(delivery_id,repository_id,event_type,actor,data) VALUES($1,$2,$3,$4,$5)`, d.ID, d.RepositoryID, event, actor, data)
	return err
}
func readDelivery(ctx context.Context, q queryExecutor, id string) (DeliveryWork, error) {
	var w DeliveryWork
	d := &w.Delivery
	err := q.QueryRow(ctx, `SELECT id,binding,workspace_id,repository_id,proposer_id,input,digest,target,credential_id,base_commit,base_tree,result_tree,patch_digest,branch,created_at FROM repository_deliveries WHERE id=$1`, id).Scan(&d.ID, &w.Binding, &d.WorkspaceID, &d.RepositoryID, &d.ProposerID, &d.Input, &d.Digest, &d.Target, &w.CredentialID, &d.BaseCommit, &d.BaseTree, &d.ResultTree, &d.PatchDigest, &d.Branch, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, domain.ErrNotFound
	}
	if err != nil {
		return w, err
	}
	var a domain.DeliveryAuthorization
	err = q.QueryRow(ctx, `SELECT actor,digest,created_at FROM delivery_authorizations WHERE delivery_id=$1`, id).Scan(&a.Actor, &a.Digest, &a.CreatedAt)
	if err == nil {
		d.Authorization = &a
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	var receipt domain.DeliveryReceipt
	err = q.QueryRow(ctx, `SELECT digest,observation,created_at FROM delivery_receipts WHERE delivery_id=$1`, id).Scan(&receipt.Digest, &receipt.Observation, &receipt.CreatedAt)
	if err == nil {
		d.Receipt = &receipt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	var execution domain.CollectionExecution
	err = q.QueryRow(ctx, `SELECT e.namespace,e.workflow_id,e.temporal_run_id,e.state,e.observed_at FROM delivery_execution_observations e JOIN delivery_outbox o ON o.id=e.outbox_id WHERE o.delivery_id=$1 ORDER BY o.id DESC LIMIT 1`, id).Scan(&execution.Namespace, &execution.WorkflowID, &execution.RunID, &execution.State, &execution.ObservedAt)
	if err == nil {
		execution.Current = time.Since(execution.ObservedAt) < ContextObservationFreshness
		d.Execution = &execution
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	var o domain.DeliveryObservation
	var seq int64
	err = q.QueryRow(ctx, `SELECT sequence,observation FROM delivery_observations WHERE delivery_id=$1 ORDER BY sequence DESC LIMIT 1`, id).Scan(&seq, &o)
	if err == nil {
		o.Sequence = seq
		d.Observation = &o
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	err = q.QueryRow(ctx, `SELECT plan FROM coordination_runs WHERE id=$1 AND workspace_id=$2`, d.Input.RunID, d.WorkspaceID).Scan(&w.Plan)
	return w, err
}
func (p *Postgres) authorizeDelivery(ctx context.Context, w DeliveryWork, action string) error {
	if p.access == nil || p.access.request.WorkspaceID != w.Delivery.WorkspaceID || p.access.request.RepositoryID != w.Delivery.RepositoryID {
		return domain.ErrNotFound
	}
	if err := p.authorizeCoordinationPlan(ctx, w.Plan, "read"); err != nil {
		return err
	}
	if action == "publish" {
		return p.authorizeExecution(ctx, w.Delivery.RepositoryID, "publish")
	}
	return p.authorizeRepository(ctx, p.tx, w.Delivery.RepositoryID, action)
}
func readDeliveryTarget(ctx context.Context, q queryExecutor, workspace, repo string) (domain.DeliveryTarget, string, []string, error) {
	var t domain.DeliveryTarget
	var credential string
	var branches []string
	var enabled bool
	err := q.QueryRow(ctx, `SELECT r.workspace_id,r.id,r.provider,r.host,r.provider_id,i.locator,i.profile,i.version,i.credential_id,i.base_branches,i.enabled FROM delivery_integrations i JOIN managed_repositories r ON r.id=i.repository_id AND r.workspace_id=i.workspace_id WHERE r.id=$1 AND r.workspace_id=$2 FOR SHARE OF r,i`, repo, workspace).Scan(&t.WorkspaceID, &t.RepositoryID, &t.Provider, &t.Host, &t.ProviderID, &t.Locator, &t.Profile, &t.IntegrationVersion, &credential, &branches, &enabled)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled {
		return t, credential, branches, domain.ErrUnavailable
	}
	return t, credential, branches, err
}

// checkExecutionAuthority revalidates the human who authorized the run in the
// same transaction as publication admission. A later principal/grant revocation,
// package revision or cancellation therefore prevents a new provider operation.
func (p *Postgres) checkExecutionAuthority(ctx context.Context, w DeliveryWork) error {
	if _, err := p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "coordination:"+w.Delivery.Input.RunID); err != nil {
		return err
	}
	var actor string
	var cancelled bool
	var runDigest, authDigest string
	err := p.tx.QueryRow(ctx, `SELECT a.actor,r.digest,a.digest,EXISTS(SELECT 1 FROM coordination_cancellations c WHERE c.run_id=r.id) FROM coordination_runs r JOIN coordination_authorizations a ON a.run_id=r.id WHERE r.id=$1 AND r.workspace_id=$2 FOR SHARE OF r,a`, w.Delivery.Input.RunID, w.Delivery.WorkspaceID).Scan(&actor, &runDigest, &authDigest, &cancelled)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (cancelled || runDigest != authDigest) {
		return domain.ErrForbidden
	}
	if err != nil {
		return err
	}
	var identity domain.AccessIdentity
	if err = p.tx.QueryRow(ctx, `SELECT issuer,subject FROM access_principals WHERE id=$1`, actor).Scan(&identity.Issuer, &identity.Subject); err != nil {
		return err
	}
	principal, err := principalInTransaction(ctx, p.tx, identity)
	if err != nil {
		return err
	}
	var active bool
	if err = p.tx.QueryRow(ctx, `SELECT active FROM workspace_memberships WHERE workspace_id=$1 AND principal_id=$2 FOR SHARE`, w.Delivery.WorkspaceID, actor).Scan(&active); errors.Is(err, pgx.ErrNoRows) || err == nil && !active {
		return domain.ErrForbidden
	}
	if err != nil {
		return err
	}
	gate := &Postgres{pool: p.pool, tx: p.tx, access: &transactionAccess{request: domain.AccessRequest{Identity: identity, WorkspaceID: w.Delivery.WorkspaceID, RepositoryID: w.Delivery.RepositoryID}, principal: principal}}
	if err = gate.authorizeCoordinationPlan(ctx, w.Plan, "execute"); err != nil {
		return err
	}
	return p.validateCoordinationPins(ctx, w.Plan, true)
}
func (p *Postgres) loadDeliveryPatch(ctx context.Context, w *DeliveryWork) error {
	var raw json.RawMessage
	var digest, outcome, key string
	err := p.queries().QueryRow(ctx, `SELECT r.artifact,r.artifact_digest,r.outcome,t.task_key FROM coordination_task_receipts r JOIN coordination_tasks t ON t.id=r.task_id AND t.run_id=r.run_id WHERE r.task_id=$1 AND r.run_id=$2`, w.Delivery.Input.TaskID, w.Delivery.Input.RunID).Scan(&raw, &digest, &outcome, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if digest != w.Delivery.Input.ArtifactDigest || outcome != "succeeded" {
		return domain.ErrConflict
	}
	var task *domain.CoordinationTask
	for i := range w.Plan.Tasks {
		if w.Plan.Tasks[i].ID == key {
			task = &w.Plan.Tasks[i]
			break
		}
	}
	if task == nil {
		return domain.ErrConflict
	}
	w.Patch, err = delivery.SelectedPatch(raw, digest, w.Delivery.RepositoryID, *task)
	if err != nil {
		return domain.ErrConflict
	}
	for _, source := range w.Plan.Repositories {
		if source.RepositoryID == w.Delivery.RepositoryID {
			w.Source = source
			break
		}
	}
	if w.Source.RepositoryID == "" || w.Source.Commit != w.Patch.BaseCommit {
		return domain.ErrConflict
	}
	return nil
}
func (p *Postgres) CreateDelivery(ctx context.Context, key string, input domain.DeliveryInput) (domain.Delivery, error) {
	var zero domain.Delivery
	if domain.ValidateCollectionKey(key) != nil || domain.ValidateDeliveryInput(input) != nil || p.access == nil {
		return zero, domain.ErrInvalidInput
	}
	w := DeliveryWork{Delivery: domain.Delivery{WorkspaceID: p.access.request.WorkspaceID, RepositoryID: p.access.request.RepositoryID, ProposerID: p.Principal().ID, Input: input}}
	err := p.tx.QueryRow(ctx, `SELECT plan FROM coordination_runs WHERE id=$1 AND workspace_id=$2`, input.RunID, w.Delivery.WorkspaceID).Scan(&w.Plan)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, domain.ErrNotFound
	}
	if err != nil {
		return zero, err
	}
	if err = p.authorizeDelivery(ctx, w, "author"); err != nil {
		return zero, err
	}
	if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "delivery-create:"+w.Delivery.RepositoryID+":"+w.Delivery.ProposerID); err != nil {
		return zero, err
	}
	var previous string
	err = p.tx.QueryRow(ctx, `SELECT id FROM repository_deliveries WHERE repository_id=$1 AND proposer_id=$2 AND idempotency_key=$3`, w.Delivery.RepositoryID, w.Delivery.ProposerID, key).Scan(&previous)
	if err == nil {
		old, err := p.Delivery(ctx, previous)
		if err == nil && !reflect.DeepEqual(old.Input, input) {
			err = domain.ErrIdempotencyConflict
		}
		return old, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	if err = p.checkExecutionAuthority(ctx, w); err != nil {
		return zero, err
	}
	if err = p.loadDeliveryPatch(ctx, &w); err != nil {
		return zero, err
	}
	var branches []string
	w.Delivery.Target, w.CredentialID, branches, err = readDeliveryTarget(ctx, p.tx, w.Delivery.WorkspaceID, w.Delivery.RepositoryID)
	if err != nil {
		return zero, err
	}
	if !slices.Contains(branches, input.BaseBranch) {
		return zero, domain.ErrForbidden
	}
	w.Delivery.BaseCommit, w.Delivery.BaseTree, w.Delivery.ResultTree, w.Delivery.PatchDigest = w.Patch.BaseCommit, w.Patch.BaseTree, w.Patch.ResultTree, w.Patch.Digest
	w.Delivery.ID, err = opaqueID()
	if err != nil {
		return zero, err
	}
	w.Binding, err = opaqueID()
	if err != nil {
		return zero, err
	}
	w.Delivery.Branch = "conductor/publication/" + w.Delivery.ID
	// The proposal digest binds source, target, contents and generated branch. It
	// deliberately excludes mutable observations and human authorization records.
	w.Delivery.Digest, err = domain.JSONDigest(w.Delivery)
	if err != nil {
		return zero, err
	}
	d := w.Delivery
	err = p.tx.QueryRow(ctx, `INSERT INTO repository_deliveries(id,binding,workspace_id,repository_id,proposer_id,run_id,task_id,idempotency_key,input,digest,target,credential_id,base_commit,base_tree,result_tree,patch_digest,branch) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING created_at`, d.ID, w.Binding, d.WorkspaceID, d.RepositoryID, d.ProposerID, input.RunID, input.TaskID, key, input, d.Digest, d.Target, w.CredentialID, d.BaseCommit, d.BaseTree, d.ResultTree, d.PatchDigest, d.Branch).Scan(&d.CreatedAt)
	if err != nil {
		return zero, err
	}
	if err = deliveryAudit(ctx, p.tx, d, "delivery.proposed", d.ProposerID, map[string]string{"digest": d.Digest}); err != nil {
		return zero, err
	}
	return d, nil
}
func (p *Postgres) Delivery(ctx context.Context, id string) (domain.Delivery, error) {
	w, err := readDelivery(ctx, p.queries(), id)
	if err != nil {
		return domain.Delivery{}, err
	}
	if err = p.authorizeDelivery(ctx, w, "read"); err != nil {
		return domain.Delivery{}, err
	}
	return w.Delivery, nil
}
func (p *Postgres) Deliveries(ctx context.Context, before string, limit int) (domain.DeliveryPage, error) {
	page := domain.DeliveryPage{Deliveries: []domain.DeliverySummary{}}
	if p.access == nil {
		return page, domain.ErrForbidden
	}
	if err := p.authorizeRepository(ctx, p.tx, p.access.request.RepositoryID, "read"); err != nil {
		return page, err
	}
	cursor, err := domain.ValidateChangeQuery("", before, limit)
	if err != nil {
		return page, err
	}
	var date *time.Time
	var lastID string
	if cursor != nil {
		date = &cursor.CreatedAt
		lastID = cursor.ID
	}
	// All source grants filter discovery before pagination. An inaccessible run
	// cannot leak proposal metadata or consume a visible page slot.
	rows, err := p.tx.Query(ctx, `SELECT d.id FROM repository_deliveries d WHERE d.workspace_id=$1 AND d.repository_id=$2 AND ($3::timestamptz IS NULL OR (d.created_at,d.id)<($3,$4::text)) AND NOT EXISTS(SELECT 1 FROM coordination_repositories cr WHERE cr.run_id=d.run_id AND NOT EXISTS(SELECT 1 FROM repository_grants g WHERE g.repository_id=cr.repository_id AND g.principal_id=$5 AND g.can_read)) ORDER BY d.created_at DESC,d.id DESC LIMIT $6`, p.access.request.WorkspaceID, p.access.request.RepositoryID, date, lastID, p.Principal().ID, limit+1)
	if err != nil {
		return page, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return page, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(ids) > limit {
		ids = ids[:limit]
		last, err := p.Delivery(ctx, ids[len(ids)-1])
		if err != nil {
			return page, err
		}
		page.NextBefore, err = domain.EncodeChangeCursor(last.CreatedAt, last.ID)
		if err != nil {
			return page, err
		}
	}
	for _, id := range ids {
		d, err := p.Delivery(ctx, id)
		if err != nil {
			return page, err
		}
		summary := domain.DeliverySummary{ID: d.ID, WorkspaceID: d.WorkspaceID, RepositoryID: d.RepositoryID, RunID: d.Input.RunID, TaskID: d.Input.TaskID, ArtifactDigest: d.Input.ArtifactDigest, Digest: d.Digest, Title: d.Input.Title, Branch: d.Branch, CreatedAt: d.CreatedAt, Authorized: d.Authorization != nil, State: "not_observed"}
		if d.Receipt != nil {
			summary.ReceiptDigest = d.Receipt.Digest
		}
		if d.Observation != nil {
			summary.State = d.Observation.State
		}
		page.Deliveries = append(page.Deliveries, summary)
	}
	return page, nil
}
func (p *Postgres) AuthorizeDelivery(ctx context.Context, id, digest string) (domain.Delivery, error) {
	w, err := readDelivery(ctx, p.tx, id)
	if err != nil {
		return domain.Delivery{}, err
	}
	if err = p.authorizeDelivery(ctx, w, "publish"); err != nil {
		return domain.Delivery{}, err
	}
	if w.Delivery.Digest != digest {
		return domain.Delivery{}, domain.ErrConflict
	}
	if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "delivery:"+id); err != nil {
		return domain.Delivery{}, err
	}
	if err = p.checkExecutionAuthority(ctx, w); err != nil {
		return domain.Delivery{}, err
	}
	target, credential, branches, err := readDeliveryTarget(ctx, p.tx, w.Delivery.WorkspaceID, w.Delivery.RepositoryID)
	if err != nil {
		return domain.Delivery{}, err
	}
	if target != w.Delivery.Target || credential != w.CredentialID || !slices.Contains(branches, w.Delivery.Input.BaseBranch) {
		return domain.Delivery{}, domain.ErrConflict
	}
	if err = p.loadDeliveryPatch(ctx, &w); err != nil {
		return domain.Delivery{}, err
	}
	tag, err := p.tx.Exec(ctx, `INSERT INTO delivery_authorizations(delivery_id,digest,actor) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, digest, p.Principal().ID)
	if err != nil {
		return domain.Delivery{}, err
	}
	if tag.RowsAffected() > 0 {
		binding, err := opaqueID()
		if err != nil {
			return domain.Delivery{}, err
		}
		if _, err = p.tx.Exec(ctx, `INSERT INTO delivery_outbox(delivery_id,operation,deduplication_key,binding) VALUES($1,'publish','publish',$2)`, id, binding); err != nil {
			return domain.Delivery{}, err
		}
		if err = deliveryAudit(ctx, p.tx, w.Delivery, "delivery.authorized", p.Principal().ID, map[string]string{"digest": digest}); err != nil {
			return domain.Delivery{}, err
		}
	}
	return p.Delivery(ctx, id)
}

func (p *Postgres) DeliveryArtifact(ctx context.Context, id string) (domain.DeliveryArtifact, error) {
	var out domain.DeliveryArtifact
	w, err := readDelivery(ctx, p.queries(), id)
	if err != nil {
		return out, err
	}
	if err = p.authorizeDelivery(ctx, w, "read"); err != nil {
		return out, err
	}
	out.DeliveryID, out.DeliveryDigest, out.ArtifactDigest = w.Delivery.ID, w.Delivery.Digest, w.Delivery.Input.ArtifactDigest
	var storedDigest string
	err = p.tx.QueryRow(ctx, `SELECT artifact,artifact_digest FROM coordination_task_receipts WHERE run_id=$1 AND task_id=$2`, w.Delivery.Input.RunID, w.Delivery.Input.TaskID).Scan(&out.Artifact, &storedDigest)
	if err != nil {
		return domain.DeliveryArtifact{}, err
	}
	if storedDigest != out.ArtifactDigest || len(out.Artifact) > 16<<20 {
		return domain.DeliveryArtifact{}, domain.ErrUnavailable
	}
	// JSONB may change whitespace or field order; compare the typed immutable
	// artifact digest while returning the entire exact semantic document.
	var result execution.Result
	if json.Unmarshal(out.Artifact, &result) != nil {
		return domain.DeliveryArtifact{}, domain.ErrUnavailable
	}
	digest, err := domain.JSONDigest(result)
	if err != nil || digest != storedDigest {
		return domain.DeliveryArtifact{}, domain.ErrUnavailable
	}
	return out, nil
}
