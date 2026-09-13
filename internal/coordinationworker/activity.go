// Package coordinationworker connects immutable human-authorized plans to real
// isolated producers. Temporal sees only opaque IDs and receipt digests.
package coordinationworker

import (
	"context"
	"errors"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/coordinationworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/store"
)

type WorkStore interface {
	CoordinationWork(context.Context, string, string) (store.CoordinationWork, error)
	CheckCoordinationWork(context.Context, string, string) error
	CoordinationInput(context.Context, string, string, string) (execution.Request, error)
	CoordinationAttempt(context.Context, string, string, string) (*store.CoordinationAttempt, error)
	BeginCoordinationAttempt(context.Context, string, string, string, string) (*store.CoordinationAttempt, bool, error)
	CompleteCoordinationTask(context.Context, string, string, string, string, execution.Result) (domain.TaskReceipt, error)
	StopCoordinationTask(context.Context, string, string, string, string, string, bool) (domain.TaskReceipt, error)
	FinalizeCoordination(context.Context, string, string) (string, error)
}

type Activity struct {
	store    WorkStore
	profiles map[string]ProfileConfig
	docker   string
}

func NewActivity(db WorkStore, profiles []ProfileConfig, docker string) (*Activity, error) {
	if db == nil || ValidateProfiles(profiles) != nil {
		return nil, contextworkflow.ErrInvalidConfiguration
	}
	a := &Activity{store: db, profiles: map[string]ProfileConfig{}, docker: docker}
	for _, p := range profiles {
		a.profiles[p.WorkspaceID+"\x00"+p.ID] = p
	}
	return a, nil
}
func (a *Activity) Load(ctx context.Context, ref coordinationworkflow.Reference) (coordinationworkflow.Plan, error) {
	w, err := a.store.CoordinationWork(ctx, ref.ID, ref.Binding)
	if err != nil {
		return coordinationworkflow.Plan{}, failure(err)
	}
	if err = a.store.CheckCoordinationWork(ctx, ref.ID, ref.Binding); err != nil {
		return coordinationworkflow.Plan{}, failure(err)
	}
	p := coordinationworkflow.Plan{MaxParallel: w.Run.Plan.MaxParallel, Tasks: []coordinationworkflow.Task{}}
	for _, t := range w.Run.Plan.Tasks {
		if _, err = a.runner(w.Run.WorkspaceID, t); err != nil {
			return p, failure(err)
		}
		entry := coordinationworkflow.Task{ID: w.TaskIDs[t.ID], TimeoutSeconds: t.TimeoutSeconds, Dependencies: []string{}}
		for _, key := range t.DependsOn {
			entry.Dependencies = append(entry.Dependencies, w.TaskIDs[key])
		}
		p.Tasks = append(p.Tasks, entry)
	}
	return p, nil
}
func (a *Activity) runner(workspace string, t domain.CoordinationTask) (execution.Runner, error) {
	p, ok := a.profiles[workspace+"\x00"+t.Profile]
	if !ok {
		return execution.Runner{}, domain.ErrUnavailable
	}
	digest, _ := domain.JSONDigest(p.Profile)
	if digest != t.ProfileDigest || p.Image != t.Image {
		return execution.Runner{}, domain.ErrConflict
	}
	return execution.Runner{DockerBinary: a.docker, Image: p.Image, Profile: p.Profile, CredentialFile: p.CredentialFile, AllowProviderNetwork: p.AllowProviderNetwork}, nil
}
func task(w store.CoordinationWork, id string) (domain.CoordinationTask, bool) {
	for _, t := range w.Run.Plan.Tasks {
		if w.TaskIDs[t.ID] == id {
			return t, true
		}
	}
	return domain.CoordinationTask{}, false
}
func result(r domain.TaskReceipt) coordinationworkflow.TaskResult {
	return coordinationworkflow.TaskResult{TaskID: r.TaskID, Digest: r.Digest, Outcome: r.Outcome}
}
func committed(w store.CoordinationWork, id string) (coordinationworkflow.TaskResult, bool) {
	for _, r := range w.Run.Receipts {
		if r.TaskID == id {
			return result(r), true
		}
	}
	return coordinationworkflow.TaskResult{}, false
}

func (a *Activity) Execute(ctx context.Context, ref coordinationworkflow.TaskReference) (coordinationworkflow.TaskResult, error) {
	w, err := a.store.CoordinationWork(ctx, ref.Run.ID, ref.Run.Binding)
	if err != nil {
		return coordinationworkflow.TaskResult{}, failure(err)
	}
	if r, ok := committed(w, ref.TaskID); ok {
		return r, nil
	}
	t, ok := task(w, ref.TaskID)
	if !ok {
		return coordinationworkflow.TaskResult{}, failure(domain.ErrNotFound)
	}
	attempt, err := a.store.CoordinationAttempt(ctx, ref.Run.ID, ref.Run.Binding, ref.TaskID)
	if err != nil {
		return coordinationworkflow.TaskResult{}, failure(err)
	}
	if attempt != nil {
		return a.recoverAttempt(ctx, ref, *attempt)
	}
	runner, err := a.runner(w.Run.WorkspaceID, t)
	if err != nil {
		return coordinationworkflow.TaskResult{}, failure(err)
	}
	request, err := a.store.CoordinationInput(ctx, ref.Run.ID, ref.Run.Binding, ref.TaskID)
	if err != nil {
		return coordinationworkflow.TaskResult{}, failure(err)
	}
	attempt, created, err := a.store.BeginCoordinationAttempt(ctx, ref.Run.ID, ref.Run.Binding, ref.TaskID, execution.InputDigest(request))
	if err != nil {
		return coordinationworkflow.TaskResult{}, failure(err)
	}
	if !created {
		if attempt == nil {
			return coordinationworkflow.TaskResult{}, failure(domain.ErrConflict)
		}
		return a.recoverAttempt(ctx, ref, *attempt)
	}
	// This is the final authorization boundary before the runner reads its private
	// model credential and starts task-owned disposable resources.
	if err = a.store.CheckCoordinationWork(ctx, ref.Run.ID, ref.Run.Binding); err != nil {
		return a.stop(ref, "cancelled", attempt.InputDigest, true)
	}
	executionCtx, cancelExecution := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				checkCtx, cancel := context.WithTimeout(executionCtx, 5*time.Second)
				err := a.store.CheckCoordinationWork(checkCtx, ref.Run.ID, ref.Run.Binding)
				cancel()
				if err != nil {
					cancelExecution()
					return
				}
			}
		}
	}()
	produced, runErr := runner.Run(executionCtx, request)
	wasCancelled := executionCtx.Err() != nil
	cancelExecution()
	<-watchDone
	if runErr != nil {
		clean := produced.CleanupConfirmed
		if !clean {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			clean, _ = runner.CleanupAttempt(cleanupCtx, attempt.InputDigest)
			cancel()
		}
		outcome := "unresolved"
		if wasCancelled && clean {
			outcome = "cancelled"
		}
		return a.stop(ref, outcome, attempt.InputDigest, clean)
	}
	outcome, err := execution.ValidateResult(request, runner.Profile, runner.Image, produced)
	if err != nil {
		return a.stop(ref, "unresolved", attempt.InputDigest, produced.CleanupConfirmed)
	}
	receipt, err := a.store.CompleteCoordinationTask(ctx, ref.Run.ID, ref.Run.Binding, ref.TaskID, outcome, produced)
	if err != nil {
		// A committed receipt wins an ambiguous acknowledgement. Otherwise revoked
		// authority discards source-bearing output and records only the stopped fact.
		if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrUnauthenticated) || errors.Is(err, domain.ErrCollectionStopped) || errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrStaleApproval) {
			return a.stop(ref, "cancelled", attempt.InputDigest, produced.CleanupConfirmed)
		}
		return coordinationworkflow.TaskResult{}, failure(err)
	}
	return result(receipt), nil
}
func (a *Activity) recoverAttempt(ctx context.Context, ref coordinationworkflow.TaskReference, attempt store.CoordinationAttempt) (coordinationworkflow.TaskResult, error) {
	// A duplicate delivery may overlap the original activity. Wait beyond its
	// admitted deadline before exact cleanup; never launch a replacement producer.
	if !attempt.RecoveryReady {
		return coordinationworkflow.TaskResult{}, contextworkflow.Failure{Code: "unavailable", Retryable: true}
	}
	clean, err := (execution.Runner{DockerBinary: a.docker}).CleanupAttempt(ctx, attempt.InputDigest)
	if err != nil {
		return coordinationworkflow.TaskResult{}, failure(err)
	}
	return a.stop(ref, "unresolved", attempt.InputDigest, clean)
}
func (a *Activity) stop(ref coordinationworkflow.TaskReference, outcome, inputDigest string, cleanup bool) (coordinationworkflow.TaskResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	receipt, err := a.store.StopCoordinationTask(ctx, ref.Run.ID, ref.Run.Binding, ref.TaskID, outcome, inputDigest, cleanup)
	if err != nil {
		return coordinationworkflow.TaskResult{}, failure(err)
	}
	return result(receipt), nil
}
func (a *Activity) Block(ctx context.Context, ref coordinationworkflow.TaskReference) (coordinationworkflow.TaskResult, error) {
	receipt, err := a.store.StopCoordinationTask(ctx, ref.Run.ID, ref.Run.Binding, ref.TaskID, "blocked", "", true)
	if err != nil {
		return coordinationworkflow.TaskResult{}, failure(err)
	}
	return result(receipt), nil
}
func (a *Activity) Finalize(ctx context.Context, ref coordinationworkflow.Reference) (coordinationworkflow.Result, error) {
	digest, err := a.store.FinalizeCoordination(ctx, ref.ID, ref.Binding)
	if err != nil {
		return coordinationworkflow.Result{}, failure(err)
	}
	return coordinationworkflow.Result{ReceiptID: ref.ID, Digest: digest}, nil
}
func failure(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrUnauthenticated):
		return contextworkflow.Failure{Code: "permission_revoked"}
	case errors.Is(err, domain.ErrCollectionStopped):
		return contextworkflow.Failure{Code: "cancelled"}
	case errors.Is(err, domain.ErrNotFound):
		return contextworkflow.Failure{Code: "not_found"}
	case errors.Is(err, domain.ErrInvalidInput), errors.Is(err, execution.ErrInvalid):
		return contextworkflow.Failure{Code: "invalid_request"}
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrStaleApproval):
		return contextworkflow.Failure{Code: "binding_mismatch"}
	case errors.Is(err, domain.ErrUnavailable):
		return contextworkflow.Failure{Code: "invalid_configuration"}
	default:
		return contextworkflow.Failure{Code: "unavailable", Retryable: true}
	}
}
