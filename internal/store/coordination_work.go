package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

// CoordinationWork is available only to the trusted worker holding the run's
// opaque binding. Public reads continue through the scoped Coordination method.
type CoordinationWork struct {
	Run           domain.CoordinationRun
	Binding       string
	TaskIDs       map[string]string
	ReceiptDigest string
}
type CoordinationAttempt struct {
	TaskID, RunID, InputDigest, ProfileDigest, Image string
	Deadline                                         time.Time
	RecoveryReady                                    bool
}

func readCoordinationWork(ctx context.Context, q queryExecutor, id string) (CoordinationWork, error) {
	var w CoordinationWork
	err := q.QueryRow(ctx, `SELECT id,binding,workspace_id,repository_id,proposer_id,plan,digest,created_at FROM coordination_runs WHERE id=$1`, id).Scan(&w.Run.ID, &w.Binding, &w.Run.WorkspaceID, &w.Run.RepositoryID, &w.Run.ProposerID, &w.Run.Plan, &w.Run.Digest, &w.Run.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, domain.ErrNotFound
	}
	if err != nil {
		return w, err
	}
	var auth domain.RunAuthorization
	err = q.QueryRow(ctx, `SELECT actor,digest,created_at FROM coordination_authorizations WHERE run_id=$1`, id).Scan(&auth.Actor, &auth.Digest, &auth.CreatedAt)
	if err == nil {
		w.Run.Authorization = &auth
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	var cancelled time.Time
	err = q.QueryRow(ctx, `SELECT created_at FROM coordination_cancellations WHERE run_id=$1`, id).Scan(&cancelled)
	if err == nil {
		w.Run.CancelRequestedAt = &cancelled
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	var observation domain.CollectionExecution
	err = q.QueryRow(ctx, `SELECT namespace,workflow_id,temporal_run_id,state,observed_at FROM coordination_execution_observations WHERE run_id=$1`, id).Scan(&observation.Namespace, &observation.WorkflowID, &observation.RunID, &observation.State, &observation.ObservedAt)
	if err == nil {
		w.Run.Execution = &observation
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	rows, err := q.Query(ctx, `SELECT t.id,t.task_key,r.digest,r.outcome,r.artifact_digest,r.created_at FROM coordination_tasks t LEFT JOIN coordination_task_receipts r ON r.task_id=t.id WHERE t.run_id=$1 ORDER BY t.task_key LIMIT 33`, id)
	if err != nil {
		return w, err
	}
	defer rows.Close()
	w.TaskIDs = map[string]string{}
	w.Run.Receipts = []domain.TaskReceipt{}
	for rows.Next() {
		var taskID, key string
		var digest, outcome, artifact *string
		var created *time.Time
		if err = rows.Scan(&taskID, &key, &digest, &outcome, &artifact, &created); err != nil {
			return w, err
		}
		w.TaskIDs[key] = taskID
		if digest != nil {
			r := domain.TaskReceipt{TaskID: taskID, TaskKey: key, Digest: *digest, Outcome: *outcome, CreatedAt: *created}
			if artifact != nil {
				r.ArtifactDigest = *artifact
			}
			w.Run.Receipts = append(w.Run.Receipts, r)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return w, err
	}
	err = q.QueryRow(ctx, `SELECT digest FROM coordination_run_receipts WHERE run_id=$1`, id).Scan(&w.ReceiptDigest)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	return w, nil
}
func (p *Postgres) CoordinationWork(ctx context.Context, id, binding string) (CoordinationWork, error) {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(binding, 32) {
		return CoordinationWork{}, domain.ErrInvalidInput
	}
	w, err := readCoordinationWork(ctx, p.queries(), id)
	if err != nil {
		return w, err
	}
	if w.Binding != binding {
		return CoordinationWork{}, domain.ErrForbidden
	}
	return w, nil
}
func (p *Postgres) coordinationGate(ctx context.Context, id, binding string) (*Postgres, CoordinationWork, error) {
	w, err := p.CoordinationWork(ctx, id, binding)
	if err != nil {
		return nil, w, err
	}
	if w.Run.Authorization == nil || w.Run.Authorization.Digest != w.Run.Digest {
		return nil, w, domain.ErrForbidden
	}
	var identity domain.AccessIdentity
	if err = p.pool.QueryRow(ctx, `SELECT issuer,subject FROM access_principals WHERE id=$1`, w.Run.Authorization.Actor).Scan(&identity.Issuer, &identity.Subject); err != nil {
		return nil, w, err
	}
	tx, err := p.BeginAccess(ctx, domain.AccessRequest{Identity: identity, WorkspaceID: w.Run.WorkspaceID, RepositoryID: w.Run.RepositoryID})
	if err != nil {
		return nil, w, err
	}
	gate := tx.(*Postgres)
	ok := false
	defer func() {
		if !ok {
			_ = gate.Rollback(ctx)
		}
	}()
	if _, err = gate.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "coordination:"+id); err != nil {
		return nil, w, err
	}
	w, err = readCoordinationWork(ctx, gate.tx, id)
	if err != nil {
		return nil, w, err
	}
	if w.Run.CancelRequestedAt != nil {
		return nil, w, domain.ErrCollectionStopped
	}
	if e := w.Run.Execution; e != nil && e.State != "running" && e.State != "unavailable" {
		return nil, w, domain.ErrCollectionStopped
	}
	if err = gate.authorizeCoordinationPlan(ctx, w.Run.Plan, "execute"); err != nil {
		return nil, w, err
	}
	if err = gate.validateCoordinationPins(ctx, w.Run.Plan, true); err != nil {
		return nil, w, err
	}
	ok = true
	return gate, w, nil
}
func (p *Postgres) CheckCoordinationWork(ctx context.Context, id, binding string) error {
	gate, _, err := p.coordinationGate(ctx, id, binding)
	if err != nil {
		return err
	}
	defer gate.Rollback(ctx)
	return gate.Commit(ctx)
}
func taskFor(w CoordinationWork, id string) (domain.CoordinationTask, bool) {
	for _, t := range w.Run.Plan.Tasks {
		if w.TaskIDs[t.ID] == id {
			return t, true
		}
	}
	return domain.CoordinationTask{}, false
}
func receiptFor(w CoordinationWork, id string) (domain.TaskReceipt, bool) {
	for _, r := range w.Run.Receipts {
		if r.TaskID == id {
			return r, true
		}
	}
	return domain.TaskReceipt{}, false
}
func dependenciesPassed(w CoordinationWork, t domain.CoordinationTask) bool {
	for _, key := range t.DependsOn {
		r, ok := receiptFor(w, w.TaskIDs[key])
		if !ok || r.Outcome != "succeeded" {
			return false
		}
	}
	return true
}

func (p *Postgres) CoordinationAttempt(ctx context.Context, id, binding, taskID string) (*CoordinationAttempt, error) {
	w, err := p.CoordinationWork(ctx, id, binding)
	if err != nil {
		return nil, err
	}
	if _, ok := taskFor(w, taskID); !ok {
		return nil, domain.ErrNotFound
	}
	return readCoordinationAttempt(ctx, p.queries(), taskID)
}
func readCoordinationAttempt(ctx context.Context, q queryExecutor, taskID string) (*CoordinationAttempt, error) {
	var a CoordinationAttempt
	err := q.QueryRow(ctx, `SELECT task_id,run_id,input_digest,profile_digest,image,deadline,deadline<=clock_timestamp() FROM coordination_task_attempts WHERE task_id=$1`, taskID).Scan(&a.TaskID, &a.RunID, &a.InputDigest, &a.ProfileDigest, &a.Image, &a.Deadline, &a.RecoveryReady)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// BeginCoordinationAttempt commits before any model credential read or Docker
// creation. The boolean is true only for the transaction that created the fact.
func (p *Postgres) BeginCoordinationAttempt(ctx context.Context, id, binding, taskID, inputDigest string) (*CoordinationAttempt, bool, error) {
	if !domain.IsLowerHex(inputDigest, 64) {
		return nil, false, domain.ErrInvalidInput
	}
	gate, w, err := p.coordinationGate(ctx, id, binding)
	if err != nil {
		return nil, false, err
	}
	defer gate.Rollback(ctx)
	task, ok := taskFor(w, taskID)
	if !ok {
		return nil, false, domain.ErrNotFound
	}
	if r, ok := receiptFor(w, taskID); ok && r.Digest != "" {
		return nil, false, domain.ErrConflict
	}
	if !dependenciesPassed(w, task) {
		return nil, false, domain.ErrConflict
	}
	existing, err := readCoordinationAttempt(ctx, gate.tx, taskID)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, gate.Commit(ctx)
	}
	_, err = gate.tx.Exec(ctx, `INSERT INTO coordination_task_attempts(task_id,run_id,input_digest,profile_digest,image,deadline) VALUES($1,$2,$3,$4,$5,clock_timestamp()+($6::integer*interval '1 second'))`, taskID, id, inputDigest, task.ProfileDigest, task.Image, task.TimeoutSeconds+30)
	if err != nil {
		return nil, false, err
	}
	a, err := readCoordinationAttempt(ctx, gate.tx, taskID)
	if err != nil {
		return nil, false, err
	}
	if err = coordinationAudit(ctx, gate.tx, id, "task.attempt_admitted", w.Run.Authorization.Actor, map[string]any{"taskId": taskID, "inputDigest": inputDigest}); err != nil {
		return nil, false, err
	}
	return a, true, gate.Commit(ctx)
}

// CoordinationArtifact is trusted receipt recovery, bound to the exact run and
// task. Source-bearing artifacts are never returned by an unscoped public API.
func (p *Postgres) CoordinationArtifact(ctx context.Context, id, binding, taskID string) (execution.Result, string, error) {
	var result execution.Result
	w, err := p.CoordinationWork(ctx, id, binding)
	if err != nil {
		return result, "", err
	}
	r, ok := receiptFor(w, taskID)
	if !ok || r.ArtifactDigest == "" {
		return result, "", domain.ErrNotFound
	}
	var data []byte
	if err = p.queries().QueryRow(ctx, `SELECT artifact FROM coordination_task_receipts WHERE run_id=$1 AND task_id=$2`, id, taskID).Scan(&data); err != nil {
		return result, "", err
	}
	if len(data) > 16<<20 || json.Unmarshal(data, &result) != nil {
		return execution.Result{}, "", domain.ErrUnavailable
	}
	digest, err := domain.JSONDigest(result)
	if err != nil || digest != r.ArtifactDigest {
		return execution.Result{}, "", domain.ErrUnavailable
	}
	return result, digest, nil
}
func insertCoordinationReceipt(ctx context.Context, tx pgx.Tx, w CoordinationWork, taskID, outcome string, result *execution.Result) (domain.TaskReceipt, error) {
	t, ok := taskFor(w, taskID)
	if !ok {
		return domain.TaskReceipt{}, domain.ErrNotFound
	}
	if r, ok := receiptFor(w, taskID); ok {
		return r, nil
	}
	var artifact any = map[string]any{}
	var artifactDigest *string
	if result != nil {
		artifact = result
		d, err := domain.JSONDigest(result)
		if err != nil {
			return domain.TaskReceipt{}, err
		}
		artifactDigest = &d
	}
	r := domain.TaskReceipt{TaskID: taskID, TaskKey: t.ID, Outcome: outcome}
	if artifactDigest != nil {
		r.ArtifactDigest = *artifactDigest
	}
	d, err := domain.JSONDigest(struct{ RunID, PlanDigest, TaskID, Outcome, ArtifactDigest string }{w.Run.ID, w.Run.Digest, taskID, outcome, r.ArtifactDigest})
	if err != nil {
		return r, err
	}
	r.Digest = d
	err = tx.QueryRow(ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, taskID, w.Run.ID, r.Digest, outcome, artifactDigest, artifact).Scan(&r.CreatedAt)
	if err != nil {
		return r, err
	}
	actor := "trusted-worker"
	if w.Run.Authorization != nil {
		actor = w.Run.Authorization.Actor
	}
	err = coordinationAudit(ctx, tx, w.Run.ID, "task.receipt_recorded", actor, map[string]any{"taskId": taskID, "digest": r.Digest, "outcome": outcome})
	return r, err
}
func recordCleanup(ctx context.Context, tx pgx.Tx, taskID, inputDigest string) error {
	_, err := tx.Exec(ctx, `INSERT INTO coordination_task_cleanup(task_id,input_digest) SELECT task_id,input_digest FROM coordination_task_attempts WHERE task_id=$1 AND input_digest=$2 ON CONFLICT DO NOTHING`, taskID, inputDigest)
	return err
}
func (p *Postgres) CompleteCoordinationTask(ctx context.Context, id, binding, taskID, outcome string, result execution.Result) (domain.TaskReceipt, error) {
	w, err := p.CoordinationWork(ctx, id, binding)
	if err != nil {
		return domain.TaskReceipt{}, err
	}
	if r, ok := receiptFor(w, taskID); ok {
		return r, nil
	}
	if outcome != "succeeded" && outcome != "failed" && outcome != "blocked" || !result.CleanupConfirmed {
		return domain.TaskReceipt{}, domain.ErrInvalidInput
	}
	gate, w, err := p.coordinationGate(ctx, id, binding)
	if err != nil {
		return domain.TaskReceipt{}, err
	}
	defer gate.Rollback(ctx)
	if r, ok := receiptFor(w, taskID); ok {
		return r, gate.Commit(ctx)
	}
	a, err := readCoordinationAttempt(ctx, gate.tx, taskID)
	if err != nil {
		return domain.TaskReceipt{}, err
	}
	if a == nil || a.InputDigest != result.InputDigest || a.ProfileDigest != result.ProfileDigest || a.Image != result.Image {
		return domain.TaskReceipt{}, domain.ErrConflict
	}
	declaredTask, ok := taskFor(w, taskID)
	if !ok || execution.ValidateVerificationBindings(declaredTask, result) != nil {
		return domain.TaskReceipt{}, domain.ErrConflict
	}
	if err = recordCleanup(ctx, gate.tx, taskID, a.InputDigest); err != nil {
		return domain.TaskReceipt{}, err
	}
	r, err := insertCoordinationReceipt(ctx, gate.tx, w, taskID, outcome, &result)
	if err != nil {
		return r, err
	}
	return r, gate.Commit(ctx)
}

// StopCoordinationTask records only a bounded factual outcome, never source or a
// usable patch after revocation. An uncertain attempt remains unresolved even
// when its resources have subsequently been removed.
func (p *Postgres) StopCoordinationTask(ctx context.Context, id, binding, taskID, outcome, inputDigest string, cleanup bool) (domain.TaskReceipt, error) {
	if outcome != "cancelled" && outcome != "blocked" && outcome != "unresolved" {
		return domain.TaskReceipt{}, domain.ErrInvalidInput
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return domain.TaskReceipt{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "coordination:"+id); err != nil {
		return domain.TaskReceipt{}, err
	}
	w, err := readCoordinationWork(ctx, tx, id)
	if err != nil {
		return domain.TaskReceipt{}, err
	}
	if w.Binding != binding {
		return domain.TaskReceipt{}, domain.ErrForbidden
	}
	if r, ok := receiptFor(w, taskID); ok {
		return r, tx.Commit(ctx)
	}
	task, ok := taskFor(w, taskID)
	if !ok {
		return domain.TaskReceipt{}, domain.ErrNotFound
	}
	a, err := readCoordinationAttempt(ctx, tx, taskID)
	if err != nil {
		return domain.TaskReceipt{}, err
	}
	if a != nil {
		if a.InputDigest != inputDigest {
			return domain.TaskReceipt{}, domain.ErrConflict
		}
		if cleanup {
			if err = recordCleanup(ctx, tx, taskID, inputDigest); err != nil {
				return domain.TaskReceipt{}, err
			}
		}
		if !cleanup {
			outcome = "unresolved"
		}
	}
	if outcome == "blocked" && (a != nil || len(task.DependsOn) == 0 || dependenciesPassed(w, task)) {
		return domain.TaskReceipt{}, domain.ErrConflict
	}
	if a == nil && outcome == "unresolved" {
		return domain.TaskReceipt{}, domain.ErrConflict
	}
	r, err := insertCoordinationReceipt(ctx, tx, w, taskID, outcome, nil)
	if err != nil {
		return r, err
	}
	if err = releaseCoordinationClaims(ctx, tx, id); err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}
func (p *Postgres) FinalizeCoordination(ctx context.Context, id, binding string) (string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "coordination:"+id); err != nil {
		return "", err
	}
	w, err := readCoordinationWork(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if w.Binding != binding {
		return "", domain.ErrForbidden
	}
	if w.ReceiptDigest != "" {
		return w.ReceiptDigest, tx.Commit(ctx)
	}
	if len(w.Run.Receipts) != len(w.Run.Plan.Tasks) {
		return "", domain.ErrConflict
	}
	receipts := append([]domain.TaskReceipt(nil), w.Run.Receipts...)
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].TaskID < receipts[j].TaskID })
	pins := []string{w.Run.Digest}
	for _, r := range receipts {
		pins = append(pins, r.Digest)
	}
	digest, err := domain.JSONDigest(pins)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO coordination_run_receipts(run_id,digest) VALUES($1,$2)`, id, digest); err != nil {
		return "", err
	}
	return digest, tx.Commit(ctx)
}

// CoordinationInput resolves complete immutable source under the authorizing
// human's current all-repository grants. Neither HTTP callers nor history can
// select a host directory, substitute a baseline, or inject a dependency patch.
func (p *Postgres) CoordinationInput(ctx context.Context, id, binding, taskID string) (execution.Request, error) {
	var request execution.Request
	gate, w, err := p.coordinationGate(ctx, id, binding)
	if err != nil {
		return request, err
	}
	defer gate.Rollback(ctx)
	task, ok := taskFor(w, taskID)
	if !ok {
		return request, domain.ErrNotFound
	}
	if !dependenciesPassed(w, task) {
		return request, domain.ErrConflict
	}
	tasks := map[string]domain.CoordinationTask{}
	for _, t := range w.Run.Plan.Tasks {
		tasks[t.ID] = t
	}
	ancestors := map[string]bool{}
	var visit func(string)
	visit = func(key string) {
		for _, dependency := range tasks[key].DependsOn {
			if !ancestors[dependency] {
				ancestors[dependency] = true
				visit(dependency)
			}
		}
	}
	visit(task.ID)
	keys := []string{}
	for key := range ancestors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Resolve the per-repository frontier from the immutable DAG before reading
	// source-bearing artifacts. Long chains need only their maximal cumulative
	// heads, not every historical patch and log repeatedly loaded into memory.
	// Memoize ancestry: a bounded DAG can still contain exponentially many paths.
	ancestry := map[[2]string]bool{}
	var descends func(string, string) bool
	descends = func(descendant, ancestor string) bool {
		pair := [2]string{descendant, ancestor}
		if known, ok := ancestry[pair]; ok {
			return known
		}
		for _, dep := range tasks[descendant].DependsOn {
			if dep == ancestor || descends(dep, ancestor) {
				ancestry[pair] = true
				return true
			}
		}
		ancestry[pair] = false
		return false
	}
	needed := map[string]bool{}
	for _, scope := range task.Scopes {
		candidates := []string{}
		for _, key := range keys {
			for _, source := range tasks[key].Scopes {
				if source.RepositoryID == scope.RepositoryID {
					candidates = append(candidates, key)
					break
				}
			}
		}
		for _, key := range candidates {
			shadowed := false
			for _, other := range candidates {
				if other != key && descends(other, key) {
					shadowed = true
					break
				}
			}
			if !shadowed {
				needed[key] = true
			}
		}
	}
	artifactBytes := 0
	artifacts := map[string]execution.Result{}
	request = execution.Request{RunID: id, TaskID: taskID, PlanDigest: w.Run.Digest, GraphDigest: w.Run.Plan.GraphDigest, Prompt: task.Prompt, TimeoutSeconds: task.TimeoutSeconds}
	for _, key := range keys {
		receipt, ok := receiptFor(w, w.TaskIDs[key])
		if !ok || receipt.Outcome != "succeeded" {
			return request, domain.ErrConflict
		}
		request.DependencyDigests = append(request.DependencyDigests, receipt.Digest)
		if !needed[key] {
			continue
		}
		artifact, digest, err := gate.CoordinationArtifact(ctx, id, binding, w.TaskIDs[key])
		if err != nil {
			return request, err
		}
		if digest != receipt.ArtifactDigest {
			return request, domain.ErrConflict
		}
		encoded, err := json.Marshal(artifact)
		if err != nil {
			return request, domain.ErrInvalidInput
		}
		artifactBytes += len(encoded)
		if artifactBytes > execution.MaxInputBytes/2 {
			return request, domain.ErrInvalidInput
		}
		artifacts[key] = artifact
	}
	sourceBytes := 0
	for _, scope := range task.Scopes {
		var pin domain.CoordinationRepository
		for _, r := range w.Run.Plan.Repositories {
			if r.RepositoryID == scope.RepositoryID {
				pin = r
			}
		}
		// All repositories were authorized and locked by coordinationGate. Narrow
		// a copy of that same transaction to this plan-pinned repository so the
		// source getter retains its ordinary selected-repository isolation rule.
		sourceGate := *gate
		sourceAccess := *gate.access
		sourceAccess.request.RepositoryID = pin.RepositoryID
		sourceGate.access = &sourceAccess
		source, err := sourceGate.LoadSourceBundle(ctx, w.Run.WorkspaceID, pin.RepositoryID, pin.CollectionID, pin.ReceiptDigest, pin.Commit)
		if err != nil {
			return request, err
		}
		if source.Digest != pin.FullSourceDigest || source.Commit != pin.Commit {
			return request, domain.ErrConflict
		}
		sourceBytes += len(source.Bundle)
		if sourceBytes > execution.MaxSourceBytes {
			return request, domain.ErrInvalidInput
		}
		repo := execution.Repository{ID: pin.RepositoryID, Commit: pin.Commit, Bundle: source.Bundle, WritablePaths: scope.WritablePaths}
		candidates := map[string]execution.Patch{}
		for key, artifact := range artifacts {
			for _, patch := range artifact.Patches {
				if patch.RepositoryID == repo.ID {
					candidates[key] = patch
				}
			}
		}
		// Include maximal applicable ancestors. Each artifact already contains its
		// predecessors, and the sandbox merges divergent heads against the common
		// original tree instead of blindly applying cumulative patches twice.
		for _, key := range keys {
			patch, ok := candidates[key]
			if !ok {
				continue
			}
			shadowed := false
			for other := range candidates {
				if other != key && descends(other, key) {
					shadowed = true
					break
				}
			}
			if !shadowed {
				repo.Dependencies = append(repo.Dependencies, patch)
			}
		}
		request.Repositories = append(request.Repositories, repo)
	}
	for _, pin := range w.Run.Plan.Packages {
		if pin.RepositoryID == task.Scopes[0].RepositoryID {
			request.ChangeID = pin.ChangeID
			request.Revision = pin.Revision
			request.Digest = pin.Digest
		}
	}
	for _, check := range task.Checks {
		request.Checks = append(request.Checks, execution.Check{ID: check.ID, RepositoryID: check.RepositoryID, Argv: check.Argv, TimeoutSeconds: check.TimeoutSeconds, Requirements: check.Requirements})
	}
	if execution.ValidateRequest(request) != nil {
		return execution.Request{}, domain.ErrInvalidInput
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > execution.MaxInputBytes {
		return execution.Request{}, domain.ErrInvalidInput
	}
	return request, gate.Commit(ctx)
}

type CoordinationRecoveryTarget struct{ ID, Binding string }

func (p *Postgres) CoordinationRecoveryTargets(ctx context.Context) ([]CoordinationRecoveryTarget, error) {
	rows, err := p.pool.Query(ctx, `SELECT r.id,r.binding FROM coordination_runs r JOIN coordination_execution_observations e ON e.run_id=r.id WHERE e.state IN ('completed','cancelled','failed','timed_out','terminated','unresolved') AND (EXISTS(SELECT 1 FROM coordination_tasks t LEFT JOIN coordination_task_receipts x ON x.task_id=t.id WHERE t.run_id=r.id AND x.task_id IS NULL) OR EXISTS(SELECT 1 FROM coordination_task_attempts a LEFT JOIN coordination_task_cleanup c ON c.task_id=a.task_id WHERE a.run_id=r.id AND c.task_id IS NULL)) ORDER BY e.observed_at LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CoordinationRecoveryTarget{}
	for rows.Next() {
		var r CoordinationRecoveryTarget
		if err = rows.Scan(&r.ID, &r.Binding); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
func (p *Postgres) ConfirmCoordinationCleanup(ctx context.Context, id, binding, taskID, inputDigest string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "coordination:"+id); err != nil {
		return err
	}
	w, err := readCoordinationWork(ctx, tx, id)
	if err != nil {
		return err
	}
	if w.Binding != binding {
		return domain.ErrForbidden
	}
	a, err := readCoordinationAttempt(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if a == nil || a.RunID != id || a.InputDigest != inputDigest {
		return domain.ErrConflict
	}
	if err = recordCleanup(ctx, tx, taskID, inputDigest); err != nil {
		return err
	}
	if err = releaseCoordinationClaims(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
