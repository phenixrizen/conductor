package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

type RuntimeWork struct {
	Evidence              domain.RuntimeEvidence
	Binding, CredentialID string
}

func readRuntime(ctx context.Context, q queryExecutor, id string) (RuntimeWork, error) {
	var w RuntimeWork
	r := &w.Evidence
	err := q.QueryRow(ctx, `SELECT id,binding,workspace_id,repository_id,requester_id,input,digest,target,deployment,criteria,credential_id,created_at FROM runtime_evidence WHERE id=$1`, id).Scan(&r.ID, &w.Binding, &r.WorkspaceID, &r.RepositoryID, &r.RequesterID, &r.Input, &r.Digest, &r.Target, &r.Deployment, &r.Criteria, &w.CredentialID, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, domain.ErrNotFound
	}
	if err != nil {
		return w, err
	}
	var receipt domain.RuntimeReceipt
	err = q.QueryRow(ctx, `SELECT receipt FROM runtime_receipts WHERE evidence_id=$1`, id).Scan(&receipt)
	if err == nil {
		r.Receipt = &receipt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	var execution domain.CollectionExecution
	err = q.QueryRow(ctx, `SELECT e.namespace,e.workflow_id,e.temporal_run_id,e.state,e.observed_at FROM runtime_execution_observations e JOIN runtime_outbox o ON o.id=e.outbox_id WHERE o.evidence_id=$1`, id).Scan(&execution.Namespace, &execution.WorkflowID, &execution.RunID, &execution.State, &execution.ObservedAt)
	if err == nil {
		execution.Current = time.Since(execution.ObservedAt) < ContextObservationFreshness
		r.Execution = &execution
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	r.Freshness = "not_collected"
	if r.Receipt != nil {
		r.Freshness = "current"
		if time.Since(r.Input.End) > time.Duration(r.Target.MaxAgeSeconds)*time.Second {
			r.Freshness = "stale"
		}
	}
	return w, nil
}
func readRuntimeTarget(ctx context.Context, q queryExecutor, workspace, repo, environment string) (domain.RuntimeTarget, string, error) {
	var c domain.RuntimeIntegrationConfig
	var version int64
	err := q.QueryRow(ctx, `SELECT configuration,version FROM runtime_integrations WHERE workspace_id=$1 AND repository_id=$2 AND environment=$3 FOR SHARE`, workspace, repo, environment).Scan(&c, &version)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !c.Enabled {
		return domain.RuntimeTarget{}, "", domain.ErrUnavailable
	}
	if err != nil {
		return domain.RuntimeTarget{}, "", err
	}
	if domain.ValidateRuntimeIntegration(c) != nil || c.WorkspaceID != workspace || c.RepositoryID != repo || c.Environment != environment {
		return domain.RuntimeTarget{}, "", domain.ErrUnavailable
	}
	return domain.RuntimeTarget{WorkspaceID: c.WorkspaceID, RepositoryID: c.RepositoryID, Environment: c.Environment, Service: c.Service, BackendID: c.BackendID, Profile: c.Profile, SourceCommit: domain.GroundcoverSourceCommit, IntegrationVersion: version, MetricFields: c.MetricFields, RecordFields: c.RecordFields, Metrics: c.Metrics, StepSeconds: c.StepSeconds, MaxAgeSeconds: c.MaxAgeSeconds}, c.CredentialID, nil
}
func (p *Postgres) runtimeDeployment(ctx context.Context, input domain.RuntimeInput, action string) (DeliveryWork, domain.ProviderDeployment, error) {
	w, err := readDelivery(ctx, p.tx, input.DeliveryID)
	if err != nil {
		return w, domain.ProviderDeployment{}, err
	}
	if err = p.authorizeDelivery(ctx, w, action); err != nil {
		return w, domain.ProviderDeployment{}, err
	}
	if w.Delivery.Digest != input.DeliveryDigest || w.Delivery.Receipt == nil {
		return w, domain.ProviderDeployment{}, domain.ErrConflict
	}
	var observation domain.DeliveryObservation
	err = p.tx.QueryRow(ctx, `SELECT observation FROM delivery_observations WHERE delivery_id=$1 AND sequence=$2`, input.DeliveryID, input.ObservationSequence).Scan(&observation)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, domain.ProviderDeployment{}, domain.ErrNotFound
	}
	if err != nil {
		return w, domain.ProviderDeployment{}, err
	}
	if input.Commit != observation.Commit && input.Commit != observation.MergeCommit {
		return w, domain.ProviderDeployment{}, domain.ErrConflict
	}
	for _, d := range observation.Deployments {
		if d.ID == input.DeploymentID && d.RepositoryID == w.Delivery.RepositoryID && d.Commit == input.Commit && d.Environment == input.Environment {
			return w, d, nil
		}
	}
	return w, domain.ProviderDeployment{}, domain.ErrConflict
}
func (p *Postgres) runtimeCriteria(ctx context.Context, w DeliveryWork, input domain.RuntimeInput, target domain.RuntimeTarget) ([]domain.RuntimeCriterionLink, error) {
	links := []domain.RuntimeCriterionLink{}
	requirements := append([]domain.RuntimeRequirement(nil), input.Requirements...)
	sort.Slice(requirements, func(i, j int) bool {
		return requirements[i].ChangeID < requirements[j].ChangeID || requirements[i].ChangeID == requirements[j].ChangeID && requirements[i].CriterionID < requirements[j].CriterionID
	})
	for _, r := range requirements {
		found := false
		for _, pin := range w.Plan.Packages {
			if pin.ChangeID == r.ChangeID && pin.Revision == r.Revision && pin.Digest == r.Digest {
				found = true
				break
			}
		}
		if !found {
			return nil, domain.ErrConflict
		}
		// Same lock as edits and approvals; criteria never advance inside collection.
		if _, err := p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, r.ChangeID); err != nil {
			return nil, err
		}
		revision, err := getRevision(ctx, p.tx, r.ChangeID)
		if err != nil {
			return nil, err
		}
		if revision.Number != r.Revision || revision.Digest != r.Digest || revision.SubmittedAt == nil {
			return nil, domain.ErrStaleApproval
		}
		var approved bool
		if err = p.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approvals WHERE change_id=$1 AND revision=$2 AND digest=$3)`, r.ChangeID, r.Revision, r.Digest).Scan(&approved); err != nil {
			return nil, err
		}
		if !approved {
			return nil, domain.ErrStaleApproval
		}
		criteria, err := domain.ParseRuntimeCriteria(revision.Content)
		if err != nil {
			return nil, err
		}
		matched := false
		for _, criterion := range criteria.Criteria {
			if criterion.ID != r.CriterionID {
				continue
			}
			if !slices.Contains(target.Metrics, criterion.Metric) {
				return nil, domain.ErrUnavailable
			}
			digest, err := domain.JSONDigest(criterion)
			if err != nil {
				return nil, err
			}
			links = append(links, domain.RuntimeCriterionLink{Requirement: r, Criterion: criterion, CriterionDigest: digest})
			matched = true
			break
		}
		if !matched {
			return nil, domain.ErrNotFound
		}
	}
	return links, nil
}
func (p *Postgres) CreateRuntimeEvidence(ctx context.Context, key string, input domain.RuntimeInput) (domain.RuntimeEvidence, error) {
	if p.access == nil || domain.ValidateCollectionKey(key) != nil || domain.ValidateRuntimeInput(input, time.Now().UTC()) != nil {
		return domain.RuntimeEvidence{}, domain.ErrInvalidInput
	}
	w, deployment, err := p.runtimeDeployment(ctx, input, "author")
	if err != nil {
		return domain.RuntimeEvidence{}, err
	}
	// Capture one author/idempotency scope before any command facts are created.
	lock := "runtime:" + w.Delivery.RepositoryID + ":" + p.Principal().ID + ":" + key
	if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock); err != nil {
		return domain.RuntimeEvidence{}, err
	}
	var previous string
	err = p.tx.QueryRow(ctx, `SELECT id FROM runtime_evidence WHERE repository_id=$1 AND requester_id=$2 AND idempotency_key=$3`, w.Delivery.RepositoryID, p.Principal().ID, key).Scan(&previous)
	if err == nil {
		old, err := p.RuntimeEvidence(ctx, previous)
		if err != nil {
			return old, err
		}
		a, _ := domain.JSONDigest(old.Input)
		b, _ := domain.JSONDigest(input)
		if a != b {
			return domain.RuntimeEvidence{}, domain.ErrIdempotencyConflict
		}
		return old, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.RuntimeEvidence{}, err
	}
	target, credential, err := readRuntimeTarget(ctx, p.tx, w.Delivery.WorkspaceID, w.Delivery.RepositoryID, input.Environment)
	if err != nil {
		return domain.RuntimeEvidence{}, err
	}
	links, err := p.runtimeCriteria(ctx, w, input, target)
	if err != nil {
		return domain.RuntimeEvidence{}, err
	}
	id, err := opaqueID()
	if err != nil {
		return domain.RuntimeEvidence{}, err
	}
	binding, err := opaqueID()
	if err != nil {
		return domain.RuntimeEvidence{}, err
	}
	r := domain.RuntimeEvidence{ID: id, WorkspaceID: w.Delivery.WorkspaceID, RepositoryID: w.Delivery.RepositoryID, RequesterID: p.Principal().ID, Input: input, Target: target, Deployment: deployment, Criteria: links, Freshness: "not_collected"}
	r.Digest, err = domain.RuntimeRequestDigest(r)
	if err != nil {
		return r, err
	}
	err = p.tx.QueryRow(ctx, `INSERT INTO runtime_evidence(id,binding,workspace_id,repository_id,requester_id,delivery_id,idempotency_key,input,digest,target,deployment,criteria,credential_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING created_at`, r.ID, binding, r.WorkspaceID, r.RepositoryID, r.RequesterID, input.DeliveryID, key, input, r.Digest, target, deployment, links, credential).Scan(&r.CreatedAt)
	if err != nil {
		return r, err
	}
	if _, err = p.tx.Exec(ctx, `INSERT INTO runtime_outbox(evidence_id) VALUES($1)`, id); err != nil {
		return r, err
	}
	err = runtimeAudit(ctx, p.tx, r, "runtime.requested", r.RequesterID, map[string]string{"digest": r.Digest})
	return r, err
}
func runtimeAudit(ctx context.Context, q queryExecutor, r domain.RuntimeEvidence, event, actor string, data any) error {
	_, err := q.Exec(ctx, `INSERT INTO runtime_audit_events(evidence_id,repository_id,actor,event_type,data) VALUES($1,$2,$3,$4,$5)`, r.ID, r.RepositoryID, actor, event, data)
	return err
}
func (p *Postgres) RuntimeEvidence(ctx context.Context, id string) (domain.RuntimeEvidence, error) {
	if p.access == nil {
		return domain.RuntimeEvidence{}, domain.ErrForbidden
	}
	w, err := readRuntime(ctx, p.tx, id)
	if err != nil {
		return w.Evidence, err
	}
	if p.access.request.WorkspaceID != w.Evidence.WorkspaceID || p.access.request.RepositoryID != w.Evidence.RepositoryID {
		return domain.RuntimeEvidence{}, domain.ErrNotFound
	}
	d, err := readDelivery(ctx, p.tx, w.Evidence.Input.DeliveryID)
	if err != nil {
		return domain.RuntimeEvidence{}, err
	}
	if err = p.authorizeDelivery(ctx, d, "read"); err != nil {
		return domain.RuntimeEvidence{}, err
	}
	return w.Evidence, nil
}
func (p *Postgres) RuntimeEvidencePage(ctx context.Context, before string, limit int) (domain.RuntimePage, error) {
	page := domain.RuntimePage{Evidence: []domain.RuntimeSummary{}}
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
	rows, err := p.tx.Query(ctx, `SELECT e.id FROM runtime_evidence e JOIN repository_deliveries d ON d.id=e.delivery_id WHERE e.workspace_id=$1 AND e.repository_id=$2 AND ($3::timestamptz IS NULL OR (e.created_at,e.id)<($3,$4::text)) AND NOT EXISTS(SELECT 1 FROM coordination_repositories cr WHERE cr.run_id=d.run_id AND NOT EXISTS(SELECT 1 FROM repository_grants g WHERE g.repository_id=cr.repository_id AND g.principal_id=$5 AND g.can_read)) ORDER BY e.created_at DESC,e.id DESC LIMIT $6`, p.access.request.WorkspaceID, p.access.request.RepositoryID, date, lastID, p.Principal().ID, limit+1)
	if err != nil {
		return page, err
	}
	ids := []string{}
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
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	for _, id := range ids {
		r, err := p.RuntimeEvidence(ctx, id)
		if err != nil {
			return page, err
		}
		summary := domain.RuntimeSummary{ID: id, DeliveryID: r.Input.DeliveryID, RepositoryID: r.RepositoryID, Environment: r.Input.Environment, Commit: r.Input.Commit, Digest: r.Digest, CreatedAt: r.CreatedAt}
		if r.Receipt != nil {
			summary.ReceiptDigest = r.Receipt.Digest
		}
		page.Evidence = append(page.Evidence, summary)
	}
	if more {
		last := page.Evidence[len(page.Evidence)-1]
		page.NextBefore, err = domain.EncodeChangeCursor(last.CreatedAt, last.ID)
	}
	return page, err
}
func (p *Postgres) checkRuntimeWork(ctx context.Context, w RuntimeWork) error {
	r := w.Evidence
	if p.Principal().ID != r.RequesterID {
		return domain.ErrForbidden
	}
	d, deployment, err := p.runtimeDeployment(ctx, r.Input, "author")
	if err != nil {
		return err
	}
	target, credential, err := readRuntimeTarget(ctx, p.tx, r.WorkspaceID, r.RepositoryID, r.Input.Environment)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(target, r.Target) || credential != w.CredentialID || !reflect.DeepEqual(deployment, r.Deployment) {
		return domain.ErrConflict
	}
	criteria, err := p.runtimeCriteria(ctx, d, r.Input, target)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(criteria)
	b, _ := json.Marshal(r.Criteria)
	if string(a) != string(b) {
		return domain.ErrConflict
	}
	return nil
}
