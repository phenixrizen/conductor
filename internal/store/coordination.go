package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

func opaqueID() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}

func (p *Postgres) ExecutionCapabilities(ctx context.Context) (domain.ExecutionCapabilities, error) {
	var caps domain.ExecutionCapabilities
	if p.access == nil || p.access.request.RepositoryID == "" {
		return caps, domain.ErrForbidden
	}
	caps.RepositoryID = p.access.request.RepositoryID
	if err := p.authorizeRepository(ctx, p.tx, caps.RepositoryID, "read"); err != nil {
		return caps, err
	}
	var author bool
	if err := p.tx.QueryRow(ctx, `SELECT can_author FROM repository_grants WHERE repository_id=$1 AND principal_id=$2 FOR SHARE`, caps.RepositoryID, p.Principal().ID).Scan(&author); err != nil {
		return caps, err
	}
	err := p.tx.QueryRow(ctx, `SELECT can_execute,can_publish FROM execution_grants WHERE repository_id=$1 AND principal_id=$2 FOR SHARE`, caps.RepositoryID, p.Principal().ID).Scan(&caps.CanExecute, &caps.CanPublish)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return caps, err
	}
	if !author || p.Principal().Kind != "human" {
		caps.CanExecute = false
		caps.CanPublish = false
	}
	return caps, nil
}

func (p *Postgres) authorizeExecution(ctx context.Context, repo, action string) error {
	if err := p.authorizeRepository(ctx, p.tx, repo, "author"); err != nil {
		return err
	}
	if p.Principal().Kind != "human" {
		return domain.ErrForbidden
	}
	var execute, publish bool
	err := p.tx.QueryRow(ctx, `SELECT can_execute,can_publish FROM execution_grants WHERE repository_id=$1 AND principal_id=$2 FOR SHARE`, repo, p.Principal().ID).Scan(&execute, &publish)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrForbidden
	}
	if err != nil {
		return err
	}
	if action == "execute" && execute || action == "publish" && publish {
		return nil
	}
	return domain.ErrForbidden
}

func (p *Postgres) authorizeCoordinationPlan(ctx context.Context, plan domain.CoordinationPlan, action string) error {
	if p.access == nil || p.access.request.RepositoryID == "" {
		return domain.ErrForbidden
	}
	anchor := false
	for _, repo := range domain.CoordinationRepositoryIDs(plan) {
		anchor = anchor || repo == p.access.request.RepositoryID
		var err error
		if action == "execute" {
			err = p.authorizeExecution(ctx, repo, action)
		} else {
			err = p.authorizeRepository(ctx, p.tx, repo, action)
		}
		if err != nil {
			return err
		}
	}
	if !anchor {
		return domain.ErrNotFound
	}
	return nil
}

// Admission compares stored immutable facts. It never fetches repository content
// or implicitly substitutes a newer package, graph or source receipt.
func (p *Postgres) validateCoordinationPins(ctx context.Context, plan domain.CoordinationPlan, approved bool) error {
	var graphDigest string
	var snapshot struct {
		Sources []struct {
			RepositoryID string `json:"repositoryId"`
			CollectionID string `json:"collectionId"`
			Digest       string `json:"digest"`
			Commit       string `json:"commit"`
		} `json:"sources"`
	}
	err := p.tx.QueryRow(ctx, `SELECT digest,snapshot FROM repository_graphs WHERE id=$1 AND workspace_id=$2`, plan.GraphID, p.access.request.WorkspaceID).Scan(&graphDigest, &snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if graphDigest != plan.GraphDigest || len(snapshot.Sources) != len(plan.Repositories) {
		return domain.ErrConflict
	}
	sources := make(map[string]domain.CoordinationRepository, len(plan.Repositories))
	for _, repo := range plan.Repositories {
		sources[repo.RepositoryID] = repo
	}
	for _, source := range snapshot.Sources {
		repo, ok := sources[source.RepositoryID]
		if !ok || repo.Commit != source.Commit || repo.CollectionID != source.CollectionID || repo.ReceiptDigest != source.Digest {
			return domain.ErrConflict
		}
		delete(sources, source.RepositoryID)
		work, err := readCollection(ctx, p.tx, repo.CollectionID, false)
		if err != nil {
			return err
		}
		c := work.Collection
		if c.WorkspaceID != p.access.request.WorkspaceID || c.RepositoryID != repo.RepositoryID {
			return domain.ErrNotFound
		}
		if c.Receipt == nil || c.Receipt.Digest != repo.ReceiptDigest || domain.ValidateCollectionReceipt(*c.Receipt, c) != nil {
			return domain.ErrConflict
		}
	}
	// Package edits use the same advisory lock. Sorting all pins prevents opposite
	// cross-repository plans from deadlocking while an approval is being inspected.
	pins := append([]domain.PackagePin(nil), plan.Packages...)
	sort.Slice(pins, func(i, j int) bool { return pins[i].ChangeID < pins[j].ChangeID })
	for _, pin := range pins {
		if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, pin.ChangeID); err != nil {
			return err
		}
		var workspace, repository string
		err = p.tx.QueryRow(ctx, `SELECT COALESCE(workspace_id,''),COALESCE(repository_id,'') FROM changes WHERE id=$1`, pin.ChangeID).Scan(&workspace, &repository)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if workspace != p.access.request.WorkspaceID || repository != pin.RepositoryID {
			return domain.ErrNotFound
		}
		r, err := getRevision(ctx, p.tx, pin.ChangeID)
		if err != nil {
			return err
		}
		if r.Number != pin.Revision || r.Digest != pin.Digest {
			return domain.ErrStaleApproval
		}
		if approved {
			var exists bool
			if err = p.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approvals WHERE change_id=$1 AND revision=$2 AND digest=$3)`, pin.ChangeID, pin.Revision, pin.Digest).Scan(&exists); err != nil {
				return err
			}
			if r.SubmittedAt == nil || !exists {
				return domain.ErrStaleApproval
			}
		}
	}
	return nil
}

func coordinationAudit(ctx context.Context, tx pgx.Tx, id, event, actor string, data any) error {
	_, err := tx.Exec(ctx, `INSERT INTO coordination_audit_events(run_id,event_type,actor,data) VALUES($1,$2,$3,$4)`, id, event, actor, data)
	return err
}

func (p *Postgres) CreateCoordination(ctx context.Context, key string, plan domain.CoordinationPlan) (domain.CoordinationRun, error) {
	var zero domain.CoordinationRun
	if domain.ValidateCollectionKey(key) != nil || domain.ValidateCoordinationPlan(plan) != nil {
		return zero, domain.ErrInvalidInput
	}
	normalized, err := domain.NormalizeCoordinationPlan(plan)
	if err != nil {
		return zero, err
	}
	plan = normalized
	if err := p.authorizeCoordinationPlan(ctx, plan, "author"); err != nil {
		return zero, err
	}
	if _, err := p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "coordination-create:"+p.access.request.RepositoryID+":"+p.Principal().ID); err != nil {
		return zero, err
	}
	digest, err := domain.JSONDigest(plan)
	if err != nil {
		return zero, err
	}
	var oldID, oldDigest string
	err = p.tx.QueryRow(ctx, `SELECT id,digest FROM coordination_runs WHERE repository_id=$1 AND proposer_id=$2 AND idempotency_key=$3`, p.access.request.RepositoryID, p.Principal().ID, key).Scan(&oldID, &oldDigest)
	if err == nil {
		if oldDigest != digest {
			return zero, domain.ErrIdempotencyConflict
		}
		return p.Coordination(ctx, oldID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	if err = p.validateCoordinationPins(ctx, plan, false); err != nil {
		return zero, err
	}
	id, err := opaqueID()
	if err != nil {
		return zero, err
	}
	binding, err := opaqueID()
	if err != nil {
		return zero, err
	}
	_, err = p.tx.Exec(ctx, `INSERT INTO coordination_runs(id,binding,workspace_id,repository_id,proposer_id,idempotency_key,plan,digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, binding, p.access.request.WorkspaceID, p.access.request.RepositoryID, p.Principal().ID, key, plan, digest)
	if err != nil {
		return zero, err
	}
	for _, repo := range plan.Repositories {
		if _, err = p.tx.Exec(ctx, `INSERT INTO coordination_repositories(run_id,repository_id) VALUES($1,$2)`, id, repo.RepositoryID); err != nil {
			return zero, err
		}
	}
	for _, task := range plan.Tasks {
		taskID, err := opaqueID()
		if err != nil {
			return zero, err
		}
		if _, err = p.tx.Exec(ctx, `INSERT INTO coordination_tasks(id,run_id,task_key) VALUES($1,$2,$3)`, taskID, id, task.ID); err != nil {
			return zero, err
		}
	}
	if err = coordinationAudit(ctx, p.tx, id, "run.proposed", p.Principal().ID, map[string]string{"digest": digest}); err != nil {
		return zero, err
	}
	return p.Coordination(ctx, id)
}

func (p *Postgres) Coordination(ctx context.Context, id string) (domain.CoordinationRun, error) {
	var run domain.CoordinationRun
	if p.access == nil || p.access.request.RepositoryID == "" {
		return run, domain.ErrForbidden
	}
	// Filter every included repository before decoding the plan or exposing its
	// existence. The later locking check closes a concurrent revocation race.
	err := p.tx.QueryRow(ctx, `SELECT id,workspace_id,repository_id,proposer_id,plan,digest,created_at FROM coordination_runs r WHERE id=$1 AND workspace_id=$2 AND EXISTS(SELECT 1 FROM coordination_repositories s WHERE s.run_id=r.id AND s.repository_id=$3) AND NOT EXISTS(SELECT 1 FROM coordination_repositories s LEFT JOIN repository_grants g ON g.repository_id=s.repository_id AND g.principal_id=$4 WHERE s.run_id=r.id AND g.can_read IS DISTINCT FROM true)`, id, p.access.request.WorkspaceID, p.access.request.RepositoryID, p.Principal().ID).Scan(&run.ID, &run.WorkspaceID, &run.RepositoryID, &run.ProposerID, &run.Plan, &run.Digest, &run.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return run, domain.ErrNotFound
	}
	if err != nil {
		return run, err
	}
	if err = p.authorizeCoordinationPlan(ctx, run.Plan, "read"); err != nil {
		return domain.CoordinationRun{}, err
	}
	run.CreatedAt = run.CreatedAt.UTC()
	var auth domain.RunAuthorization
	err = p.tx.QueryRow(ctx, `SELECT actor,digest,created_at FROM coordination_authorizations WHERE run_id=$1`, id).Scan(&auth.Actor, &auth.Digest, &auth.CreatedAt)
	if err == nil {
		auth.CreatedAt = auth.CreatedAt.UTC()
		run.Authorization = &auth
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return run, err
	}
	var cancelled time.Time
	err = p.tx.QueryRow(ctx, `SELECT created_at FROM coordination_cancellations WHERE run_id=$1`, id).Scan(&cancelled)
	if err == nil {
		cancelled = cancelled.UTC()
		run.CancelRequestedAt = &cancelled
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return run, err
	}
	var execution domain.CollectionExecution
	err = p.tx.QueryRow(ctx, `SELECT namespace,workflow_id,temporal_run_id,state,observed_at,(state NOT IN ('unavailable','unresolved') AND observed_at>clock_timestamp()-interval '30 seconds') FROM coordination_execution_observations WHERE run_id=$1`, id).Scan(&execution.Namespace, &execution.WorkflowID, &execution.RunID, &execution.State, &execution.ObservedAt, &execution.Current)
	if err == nil {
		execution.ObservedAt = execution.ObservedAt.UTC()
		run.Execution = &execution
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return run, err
	}
	run.Receipts = []domain.TaskReceipt{}
	rows, err := p.tx.Query(ctx, `SELECT r.task_id,t.task_key,r.digest,r.outcome,COALESCE(r.artifact_digest,''),r.created_at FROM coordination_task_receipts r JOIN coordination_tasks t ON t.id=r.task_id WHERE r.run_id=$1 ORDER BY t.task_key LIMIT 33`, id)
	if err != nil {
		return run, err
	}
	defer rows.Close()
	for rows.Next() {
		var receipt domain.TaskReceipt
		if err = rows.Scan(&receipt.TaskID, &receipt.TaskKey, &receipt.Digest, &receipt.Outcome, &receipt.ArtifactDigest, &receipt.CreatedAt); err != nil {
			return run, err
		}
		receipt.CreatedAt = receipt.CreatedAt.UTC()
		run.Receipts = append(run.Receipts, receipt)
	}
	return run, rows.Err()
}

func (p *Postgres) Coordinations(ctx context.Context, before string, limit int) (domain.CoordinationPage, error) {
	page := domain.CoordinationPage{Runs: []domain.CoordinationSummary{}}
	if p.access == nil || p.access.request.RepositoryID == "" {
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
	rows, err := p.tx.Query(ctx, `SELECT r.id FROM coordination_runs r WHERE workspace_id=$1 AND EXISTS(SELECT 1 FROM coordination_repositories s WHERE s.run_id=r.id AND s.repository_id=$2) AND NOT EXISTS(SELECT 1 FROM coordination_repositories s LEFT JOIN repository_grants g ON g.repository_id=s.repository_id AND g.principal_id=$3 WHERE s.run_id=r.id AND g.can_read IS DISTINCT FROM true) AND ($4::timestamptz IS NULL OR (r.created_at,r.id)<($4,$5::text)) ORDER BY r.created_at DESC,r.id DESC LIMIT $6`, p.access.request.WorkspaceID, p.access.request.RepositoryID, p.Principal().ID, date, lastID, limit+1)
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
	for _, id := range ids {
		run, err := p.Coordination(ctx, id)
		if err != nil {
			return domain.CoordinationPage{}, err
		}
		if len(page.Runs) == limit {
			last := page.Runs[limit-1]
			page.NextBefore, err = domain.EncodeChangeCursor(last.CreatedAt, last.ID)
			return page, err
		}
		page.Runs = append(page.Runs, domain.CoordinationSummary{ID: run.ID, WorkspaceID: run.WorkspaceID, RepositoryID: run.RepositoryID, ProposerID: run.ProposerID, Digest: run.Digest, CreatedAt: run.CreatedAt, Authorized: run.Authorization != nil, CancelRequestedAt: run.CancelRequestedAt, Execution: run.Execution, Tasks: len(run.Plan.Tasks), Receipts: len(run.Receipts)})
	}
	return page, nil
}

func (p *Postgres) AuthorizeCoordination(ctx context.Context, id, digest string) (domain.CoordinationRun, error) {
	var zero domain.CoordinationRun
	if _, err := p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "coordination:"+id); err != nil {
		return zero, err
	}
	run, err := p.Coordination(ctx, id)
	if err != nil {
		return zero, err
	}
	if err = p.authorizeCoordinationPlan(ctx, run.Plan, "execute"); err != nil {
		return zero, err
	}
	if run.Digest != digest {
		return zero, domain.ErrConflict
	}
	if run.CancelRequestedAt != nil {
		return zero, domain.ErrConflict
	}
	if run.Authorization != nil {
		return run, nil
	}
	// Lock every repository before examining claims; two runs cannot both see a
	// path as free. A cancellation request alone does not release an active writer.
	for _, repo := range domain.CoordinationRepositoryIDs(run.Plan) {
		if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "execution-admission:"+repo); err != nil {
			return zero, err
		}
	}
	if err = p.validateCoordinationPins(ctx, run.Plan, true); err != nil {
		return zero, err
	}
	for _, task := range run.Plan.Tasks {
		for _, scope := range task.Scopes {
			for _, path := range scope.WritablePaths {
				var conflict bool
				err = p.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_claims WHERE repository_id=$1 AND released_at IS NULL AND run_id<>$2 AND (path=$3 OR starts_with(path,$3||'/') OR starts_with($3,path||'/')))`, scope.RepositoryID, id, path).Scan(&conflict)
				if err != nil {
					return zero, err
				}
				if conflict {
					return zero, domain.ErrConflict
				}
				if _, err = p.tx.Exec(ctx, `INSERT INTO coordination_claims(run_id,repository_id,path) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, scope.RepositoryID, path); err != nil {
					return zero, err
				}
			}
		}
	}
	if _, err = p.tx.Exec(ctx, `INSERT INTO coordination_authorizations(run_id,digest,actor) VALUES($1,$2,$3)`, id, digest, p.Principal().ID); err != nil {
		return zero, err
	}
	if _, err = p.tx.Exec(ctx, `INSERT INTO coordination_outbox(run_id,operation) VALUES($1,'start')`, id); err != nil {
		return zero, err
	}
	if err = coordinationAudit(ctx, p.tx, id, "run.authorized", p.Principal().ID, map[string]string{"digest": digest}); err != nil {
		return zero, err
	}
	return p.Coordination(ctx, id)
}

func (p *Postgres) CancelCoordination(ctx context.Context, id, digest string) (domain.CoordinationRun, error) {
	var zero domain.CoordinationRun
	if _, err := p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "coordination:"+id); err != nil {
		return zero, err
	}
	run, err := p.Coordination(ctx, id)
	if err != nil {
		return zero, err
	}
	if err = p.authorizeCoordinationPlan(ctx, run.Plan, "execute"); err != nil {
		return zero, err
	}
	if run.Digest != digest {
		return zero, domain.ErrConflict
	}
	if run.CancelRequestedAt != nil {
		return run, nil
	}
	if _, err = p.tx.Exec(ctx, `INSERT INTO coordination_cancellations(run_id,actor) VALUES($1,$2)`, id, p.Principal().ID); err != nil {
		return zero, err
	}
	if run.Authorization != nil {
		if _, err = p.tx.Exec(ctx, `INSERT INTO coordination_outbox(run_id,operation) VALUES($1,'cancel')`, id); err != nil {
			return zero, err
		}
	}
	if err = coordinationAudit(ctx, p.tx, id, "run.cancellation_requested", p.Principal().ID, map[string]string{"digest": digest}); err != nil {
		return zero, err
	}
	return p.Coordination(ctx, id)
}
