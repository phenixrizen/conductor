package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/runtimeevidence"
)

func (p *Postgres) ApplyRuntimeIntegrations(ctx context.Context, operator string, configs []domain.RuntimeIntegrationConfig) error {
	if domain.ValidateAccessID(operator) != nil || len(configs) < 1 || len(configs) > 128 {
		return domain.ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, c := range configs {
		key := c.RepositoryID + ":" + c.Environment
		if domain.ValidateRuntimeIntegration(c) != nil || seen[key] {
			return domain.ErrInvalidInput
		}
		seen[key] = true
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, c := range configs {
		var repo string
		err = tx.QueryRow(ctx, `SELECT id FROM managed_repositories WHERE id=$1 AND workspace_id=$2 FOR SHARE`, c.RepositoryID, c.WorkspaceID).Scan(&repo)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO runtime_integrations(repository_id,workspace_id,environment,configuration) VALUES($1,$2,$3,$4) ON CONFLICT(repository_id,environment) DO UPDATE SET configuration=EXCLUDED.configuration,version=runtime_integrations.version+1`, c.RepositoryID, c.WorkspaceID, c.Environment, c); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO runtime_audit_events(repository_id,actor,event_type,data) VALUES($1,$2,'runtime.integration_configured',$3)`, c.RepositoryID, operator, map[string]any{"profile": c.Profile, "environment": c.Environment, "enabled": c.Enabled}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (p *Postgres) LoadRuntimeWork(ctx context.Context, id, binding string) (RuntimeWork, error) {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(binding, 32) {
		return RuntimeWork{}, domain.ErrInvalidInput
	}
	w, err := readRuntime(ctx, p.queries(), id)
	if err != nil {
		return w, err
	}
	if w.Binding != binding {
		return RuntimeWork{}, domain.ErrForbidden
	}
	return w, nil
}
func (p *Postgres) runtimeGate(ctx context.Context, w RuntimeWork) (*Postgres, error) {
	var identity domain.AccessIdentity
	if err := p.pool.QueryRow(ctx, `SELECT issuer,subject FROM access_principals WHERE id=$1`, w.Evidence.RequesterID).Scan(&identity.Issuer, &identity.Subject); err != nil {
		return nil, err
	}
	access, err := p.BeginAccess(ctx, domain.AccessRequest{Identity: identity, WorkspaceID: w.Evidence.WorkspaceID, RepositoryID: w.Evidence.RepositoryID})
	if err != nil {
		return nil, err
	}
	return access.(*Postgres), nil
}
func (p *Postgres) CheckRuntimeWork(ctx context.Context, id, binding string) (RuntimeWork, error) {
	w, err := p.LoadRuntimeWork(ctx, id, binding)
	if err != nil {
		return w, err
	}
	gate, err := p.runtimeGate(ctx, w)
	if err != nil {
		return RuntimeWork{}, err
	}
	defer gate.Rollback(ctx)
	if err = gate.checkRuntimeWork(ctx, w); err != nil {
		return RuntimeWork{}, err
	}
	return w, gate.Commit(ctx)
}
func (p *Postgres) RuntimeOperationReceipt(ctx context.Context, id, binding string) (string, error) {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(binding, 32) {
		return "", domain.ErrInvalidInput
	}
	var digest string
	err := p.queries().QueryRow(ctx, `SELECT r.digest FROM runtime_receipts r JOIN runtime_evidence e ON e.id=r.evidence_id WHERE e.id=$1 AND e.binding=$2`, id, binding).Scan(&digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return digest, err
}
func (p *Postgres) CompleteRuntimeEvidence(ctx context.Context, id, binding string, receipt domain.RuntimeReceipt) (string, error) {
	if digest, err := p.RuntimeOperationReceipt(ctx, id, binding); err != nil || digest != "" {
		return digest, err
	}
	w, err := p.LoadRuntimeWork(ctx, id, binding)
	if err != nil {
		return "", err
	}
	gate, err := p.runtimeGate(ctx, w)
	if err != nil {
		if digest, e := p.RuntimeOperationReceipt(ctx, id, binding); e == nil && digest != "" {
			return digest, nil
		}
		return "", err
	}
	defer gate.Rollback(ctx)
	if _, err = gate.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "runtime-receipt:"+id); err != nil {
		return "", err
	}
	if digest, err := gate.RuntimeOperationReceipt(ctx, id, binding); err != nil || digest != "" {
		return digest, err
	}
	if err = gate.checkRuntimeWork(ctx, w); err != nil {
		return "", err
	}
	if err = runtimeevidence.ValidateReceipt(w.Evidence, receipt, time.Now().UTC()); err != nil {
		return "", domain.ErrInvalidInput
	}
	if _, err = gate.tx.Exec(ctx, `INSERT INTO runtime_receipts(evidence_id,digest,receipt) VALUES($1,$2,$3)`, id, receipt.Digest, receipt); err != nil {
		return "", err
	}
	if err = runtimeAudit(ctx, gate.tx, w.Evidence, "runtime.collected", w.Evidence.RequesterID, map[string]string{"digest": receipt.Digest, "productionOutcome": receipt.ProductionOutcome}); err != nil {
		return "", err
	}
	return receipt.Digest, gate.Commit(ctx)
}
func (p *Postgres) CheckRuntimeSchema(ctx context.Context) error {
	var ready bool
	err := p.pool.QueryRow(ctx, `SELECT to_regclass('runtime_evidence') IS NOT NULL AND to_regclass('runtime_receipts') IS NOT NULL AND to_regclass('runtime_outbox') IS NOT NULL`).Scan(&ready)
	if err != nil || !ready {
		return domain.ErrUnavailable
	}
	return nil
}
