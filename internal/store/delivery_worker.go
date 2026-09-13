package store

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/delivery"
	"github.com/phenixrizen/conductor/internal/domain"
)

// ApplyDeliveryIntegrations is a trusted database-operator operation. Configuration
// changes invalidate existing proposal bindings, including disable/re-enable.
func (p *Postgres) ApplyDeliveryIntegrations(ctx context.Context, operator string, configs []domain.DeliveryIntegrationConfig) error {
	if domain.ValidateAccessID(operator) != nil || len(configs) < 1 || len(configs) > 128 {
		return domain.ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, c := range configs {
		if domain.ValidateDeliveryIntegration(c) != nil || seen[c.RepositoryID] {
			return domain.ErrInvalidInput
		}
		seen[c.RepositoryID] = true
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, c := range configs {
		var provider, host string
		err = tx.QueryRow(ctx, `SELECT provider,host FROM managed_repositories WHERE id=$1 AND workspace_id=$2 FOR SHARE`, c.RepositoryID, c.WorkspaceID).Scan(&provider, &host)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !(provider == "github" && host == "github.com" && c.Profile == domain.GitHubDeliveryProfile || provider == "gitlab" && host == "gitlab.com" && c.Profile == domain.GitLabDeliveryProfile) {
			return domain.ErrInvalidInput
		}
		if _, err = tx.Exec(ctx, `INSERT INTO delivery_integrations(repository_id,workspace_id,profile,locator,credential_id,base_branches,enabled) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(repository_id) DO UPDATE SET profile=EXCLUDED.profile,locator=EXCLUDED.locator,credential_id=EXCLUDED.credential_id,base_branches=EXCLUDED.base_branches,enabled=EXCLUDED.enabled,version=delivery_integrations.version+1`, c.RepositoryID, c.WorkspaceID, c.Profile, c.Locator, c.CredentialID, c.BaseBranches, c.Enabled); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO delivery_audit_events(repository_id,event_type,actor,data) VALUES($1,'integration.configured',$2,$3)`, c.RepositoryID, operator, map[string]any{"profile": c.Profile, "enabled": c.Enabled}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (p *Postgres) DeliveryWork(ctx context.Context, id, binding string) (DeliveryWork, error) {
	w, err := readDelivery(ctx, p.queries(), id)
	if err != nil {
		return w, err
	}
	if !domain.IsLowerHex(binding, 32) || w.Binding != binding {
		return DeliveryWork{}, domain.ErrForbidden
	}
	return w, nil
}
func (p *Postgres) deliveryGate(ctx context.Context, w DeliveryWork) (*Postgres, error) {
	if w.Delivery.Authorization == nil {
		return nil, domain.ErrForbidden
	}
	var identity domain.AccessIdentity
	if err := p.pool.QueryRow(ctx, `SELECT issuer,subject FROM access_principals WHERE id=$1`, w.Delivery.Authorization.Actor).Scan(&identity.Issuer, &identity.Subject); err != nil {
		return nil, err
	}
	access, err := p.BeginAccess(ctx, domain.AccessRequest{Identity: identity, WorkspaceID: w.Delivery.WorkspaceID, RepositoryID: w.Delivery.RepositoryID})
	if err != nil {
		return nil, err
	}
	return access.(*Postgres), nil
}
func (p *Postgres) checkDeliveryWork(ctx context.Context, w *DeliveryWork) error {
	if err := p.authorizeDelivery(ctx, *w, "publish"); err != nil {
		return err
	}
	if w.Delivery.Authorization == nil || w.Delivery.Authorization.Actor != p.Principal().ID || w.Delivery.Authorization.Digest != w.Delivery.Digest {
		return domain.ErrForbidden
	}
	if err := p.checkExecutionAuthority(ctx, *w); err != nil {
		return err
	}
	target, credential, branches, err := readDeliveryTarget(ctx, p.tx, w.Delivery.WorkspaceID, w.Delivery.RepositoryID)
	if err != nil {
		return err
	}
	if target != w.Delivery.Target || credential != w.CredentialID || !slices.Contains(branches, w.Delivery.Input.BaseBranch) {
		return domain.ErrConflict
	}
	if err = p.loadDeliveryPatch(ctx, w); err != nil {
		return err
	}
	d := w.Delivery
	patch := w.Patch
	if patch.BaseCommit != d.BaseCommit || patch.BaseTree != d.BaseTree || patch.ResultTree != d.ResultTree || patch.Digest != d.PatchDigest {
		return domain.ErrConflict
	}
	return nil
}
func (p *Postgres) CheckDeliveryWork(ctx context.Context, id, binding string) (DeliveryWork, error) {
	w, err := p.DeliveryWork(ctx, id, binding)
	if err != nil {
		return w, err
	}
	gate, err := p.deliveryGate(ctx, w)
	if err != nil {
		return DeliveryWork{}, err
	}
	defer gate.Rollback(ctx)
	if err = gate.checkDeliveryWork(ctx, &w); err != nil {
		return DeliveryWork{}, err
	}
	return w, gate.Commit(ctx)
}

// Persisted operation receipts win a retry even after authorization is revoked.
// This internal nonce lookup grants no public read access and does no provider I/O.
func (p *Postgres) DeliveryOperationReceipt(ctx context.Context, operation, binding string) (string, error) {
	var digest string
	err := p.queries().QueryRow(ctx, `SELECT r.digest FROM delivery_operation_receipts r JOIN delivery_outbox o ON o.id=r.outbox_id JOIN repository_deliveries d ON d.id=o.delivery_id WHERE o.binding=$1 AND d.binding=$2`, operation, binding).Scan(&digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return digest, err
}
func (p *Postgres) CompleteDeliveryOperation(ctx context.Context, operation, binding string, observation domain.DeliveryObservation) (string, error) {
	if digest, err := p.DeliveryOperationReceipt(ctx, operation, binding); err != nil || digest != "" {
		return digest, err
	}
	item, err := p.LoadDeliveryOperation(ctx, operation, binding)
	if err != nil {
		return "", err
	}
	w := item.Work
	gate, err := p.deliveryGate(ctx, w)
	if err != nil {
		if digest, e := p.DeliveryOperationReceipt(ctx, operation, binding); e == nil && digest != "" {
			return digest, nil
		}
		return "", err
	}
	defer gate.Rollback(ctx)
	if _, err = gate.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "delivery:"+w.Delivery.ID); err != nil {
		return "", err
	}
	if digest, err := gate.DeliveryOperationReceipt(ctx, operation, binding); err != nil || digest != "" {
		return digest, err
	}
	if err = gate.checkDeliveryWork(ctx, &w); err != nil {
		return "", err
	}
	if observation.Deployments == nil {
		observation.Deployments = []domain.ProviderDeployment{}
	}
	observation.Trigger = &domain.DeliveryObservationTrigger{Kind: "authorization"}
	if item.Operation == "reconcile" {
		observation.Trigger = &domain.DeliveryObservationTrigger{Kind: "manual", Key: item.DeduplicationKey}
		if strings.HasPrefix(item.DeduplicationKey, "webhook:") {
			key := strings.TrimPrefix(item.DeduplicationKey, "webhook:")
			trigger := domain.DeliveryObservationTrigger{Kind: "webhook", Key: key}
			if err = gate.tx.QueryRow(ctx, `SELECT event_type,payload_digest FROM delivery_event_inbox WHERE repository_id=$1 AND delivery_key=$2`, w.Delivery.RepositoryID, key).Scan(&trigger.EventType, &trigger.PayloadDigest); err != nil {
				return "", err
			}
			observation.Trigger = &trigger
		}
	}
	if err = domain.ValidateDeliveryObservation(w.Delivery, observation); err != nil {
		return "", err
	}
	digest, err := domain.JSONDigest(observation)
	if err != nil {
		return "", err
	}
	if item.Operation == "publish" {
		if _, err = gate.tx.Exec(ctx, `INSERT INTO delivery_receipts(delivery_id,digest,observation) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, w.Delivery.ID, digest, observation); err != nil {
			return "", err
		}
	}
	if _, err = gate.tx.Exec(ctx, `INSERT INTO delivery_observations(delivery_id,observation) VALUES($1,$2)`, w.Delivery.ID, observation); err != nil {
		return "", err
	}
	if _, err = gate.tx.Exec(ctx, `INSERT INTO delivery_operation_receipts(outbox_id,digest) VALUES($1,$2)`, item.Number, digest); err != nil {
		return "", err
	}
	if err = deliveryAudit(ctx, gate.tx, w.Delivery, "delivery.observed", w.Delivery.Authorization.Actor, map[string]string{"digest": digest, "state": observation.State}); err != nil {
		return "", err
	}
	return digest, gate.Commit(ctx)
}
func (p *Postgres) ReconcileDelivery(ctx context.Context, id, digest, key string) (domain.Delivery, error) {
	w, err := readDelivery(ctx, p.tx, id)
	if err != nil {
		return domain.Delivery{}, err
	}
	if err = p.authorizeDelivery(ctx, w, "author"); err != nil {
		return domain.Delivery{}, err
	}
	if w.Delivery.Digest != digest || w.Delivery.Observation == nil {
		return domain.Delivery{}, domain.ErrConflict
	}
	binding, err := opaqueID()
	if err != nil {
		return domain.Delivery{}, err
	}
	tag, err := p.tx.Exec(ctx, `INSERT INTO delivery_outbox(delivery_id,operation,deduplication_key,binding) VALUES($1,'reconcile',$2,$3) ON CONFLICT DO NOTHING`, id, key, binding)
	if err != nil {
		return domain.Delivery{}, err
	}
	if tag.RowsAffected() > 0 {
		err = deliveryAudit(ctx, p.tx, w.Delivery, "delivery.reconciliation_requested", p.Principal().ID, map[string]string{"digest": digest})
	}
	return w.Delivery, err
}

func (p *Postgres) CheckDeliverySchema(ctx context.Context) error {
	var ready bool
	err := p.pool.QueryRow(ctx, `SELECT to_regclass('repository_deliveries') IS NOT NULL AND to_regclass('delivery_outbox') IS NOT NULL AND to_regclass('delivery_operation_receipts') IS NOT NULL AND to_regclass('context_source_bundles') IS NOT NULL`).Scan(&ready)
	if err != nil || !ready {
		return domain.ErrUnavailable
	}
	return nil
}

// RecordDeliveryWebhook stores verified event identity and outbox intents in one
// commit. Its payload is not retained and never becomes authoritative evidence.
func (p *Postgres) RecordDeliveryWebhook(ctx context.Context, target domain.DeliveryTarget, event delivery.WebhookEvent) error {
	if domain.ValidateCollectionKey(event.Key) != nil || !domain.IsLowerHex(event.PayloadDigest, 64) || !domain.IsLowerHex(event.Commit, 40) || len(event.Type) > 64 {
		return domain.ErrInvalidInput
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	actual, _, _, err := readDeliveryTarget(ctx, tx, target.WorkspaceID, target.RepositoryID)
	if err != nil {
		return err
	}
	if actual.Provider != target.Provider || actual.Host != target.Host || actual.ProviderID != target.ProviderID {
		return domain.ErrForbidden
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "delivery-event:"+target.RepositoryID+":"+event.Key); err != nil {
		return err
	}
	var digest, kind string
	err = tx.QueryRow(ctx, `SELECT payload_digest,event_type FROM delivery_event_inbox WHERE repository_id=$1 AND delivery_key=$2`, target.RepositoryID, event.Key).Scan(&digest, &kind)
	if err == nil {
		if digest != event.PayloadDigest || kind != event.Type {
			return domain.ErrIdempotencyConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT d.id FROM repository_deliveries d JOIN delivery_receipts r ON r.delivery_id=d.id WHERE d.repository_id=$1 AND (r.observation->>'commit'=$2 OR EXISTS(SELECT 1 FROM delivery_observations x WHERE x.delivery_id=d.id AND x.observation->>'mergeCommit'=$2) OR $3) ORDER BY d.id LIMIT 101`, target.RepositoryID, event.Commit, event.Type == "deployment_status" || event.Type == "Deployment Hook" || event.Type == "push" || event.Type == "Push Hook")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(ids) > 100 {
		return domain.ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO delivery_event_inbox(repository_id,delivery_key,payload_digest,event_type) VALUES($1,$2,$3,$4)`, target.RepositoryID, event.Key, event.PayloadDigest, event.Type); err != nil {
		return err
	}
	for _, id := range ids {
		binding, err := opaqueID()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO delivery_outbox(delivery_id,operation,deduplication_key,binding) VALUES($1,'reconcile',$2,$3) ON CONFLICT DO NOTHING`, id, "webhook:"+event.Key, binding); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// LoadDeliverySource holds the original publisher, execution authority, package
// pins and every source grant through the selected immutable source read.
func (p *Postgres) LoadDeliverySource(ctx context.Context, id, binding string) ([]byte, error) {
	w, err := p.DeliveryWork(ctx, id, binding)
	if err != nil {
		return nil, err
	}
	gate, err := p.deliveryGate(ctx, w)
	if err != nil {
		return nil, err
	}
	defer gate.Rollback(ctx)
	if err = gate.checkDeliveryWork(ctx, &w); err != nil {
		return nil, err
	}
	source := w.Source
	bundle, err := gate.LoadSourceBundle(ctx, w.Delivery.WorkspaceID, source.RepositoryID, source.CollectionID, source.ReceiptDigest, source.Commit)
	if err != nil {
		return nil, err
	}
	if bundle.Tree != w.Delivery.BaseTree {
		return nil, domain.ErrConflict
	}
	if err = gate.Commit(ctx); err != nil {
		return nil, err
	}
	return bundle.Bundle, nil
}
