// Package coordinationworkflow sequences bounded task DAGs in Temporal. History
// contains opaque identifiers and receipt digests; source, prompts, commands and
// model credentials are loaded only inside trusted activities.
package coordinationworkflow

import (
	"context"
	"errors"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	WorkflowName     = "conductor.coordinate.v1"
	TaskQueue        = "conductor-coordination-v1"
	LoadActivity     = "conductor.coordination-load.v1"
	ExecuteActivity  = "conductor.coordination-execute.v1"
	BlockActivity    = "conductor.coordination-block.v1"
	FinalizeActivity = "conductor.coordination-finalize.v1"
)

type Reference = contextworkflow.Reference
type Result = contextworkflow.Result
type Task struct {
	ID             string   `json:"id"`
	Dependencies   []string `json:"dependencies"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}
type Plan struct {
	Tasks       []Task `json:"tasks"`
	MaxParallel int    `json:"maxParallel"`
}
type TaskReference struct {
	Run    Reference `json:"run"`
	TaskID string    `json:"taskId"`
}
type TaskResult struct {
	TaskID  string `json:"taskId"`
	Digest  string `json:"digest"`
	Outcome string `json:"outcome"`
}
type Activities interface {
	Load(context.Context, Reference) (Plan, error)
	Execute(context.Context, TaskReference) (TaskResult, error)
	Block(context.Context, TaskReference) (TaskResult, error)
	Finalize(context.Context, Reference) (Result, error)
}

func validReference(r Reference) bool {
	return domain.IsLowerHex(r.ID, 32) && domain.IsLowerHex(r.Binding, 32)
}
func validTaskResult(ref TaskReference, r TaskResult) bool {
	if r.TaskID != ref.TaskID || !domain.IsLowerHex(r.Digest, 64) {
		return false
	}
	switch r.Outcome {
	case "succeeded", "failed", "blocked", "cancelled", "unresolved":
		return true
	}
	return false
}
func validatePlan(p Plan) error {
	if len(p.Tasks) < 1 || len(p.Tasks) > domain.MaxCoordinationTasks || p.MaxParallel < 1 || p.MaxParallel > 4 {
		return domain.ErrInvalidInput
	}
	tasks := map[string]Task{}
	for _, task := range p.Tasks {
		if !domain.IsLowerHex(task.ID, 32) || task.TimeoutSeconds < 1 || task.TimeoutSeconds > 1800 || len(task.Dependencies) > domain.MaxCoordinationTasks {
			return domain.ErrInvalidInput
		}
		if _, ok := tasks[task.ID]; ok {
			return domain.ErrInvalidInput
		}
		tasks[task.ID] = task
	}
	marks := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		task, ok := tasks[id]
		if !ok || marks[id] == 1 {
			return domain.ErrInvalidInput
		}
		if marks[id] == 2 {
			return nil
		}
		marks[id] = 1
		seen := map[string]bool{}
		for _, dep := range task.Dependencies {
			if seen[dep] {
				return domain.ErrInvalidInput
			}
			seen[dep] = true
			if err := visit(dep); err != nil {
				return err
			}
		}
		marks[id] = 2
		return nil
	}
	for _, task := range p.Tasks {
		if err := visit(task.ID); err != nil {
			return err
		}
	}
	return nil
}
func fail(err error) error {
	if errors.Is(err, context.Canceled) || temporal.IsCanceledError(err) {
		return temporal.NewCanceledError()
	}
	code := "execution_unresolved"
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		code = "invalid_execution"
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrUnauthenticated):
		code = "permission_revoked"
	case errors.Is(err, domain.ErrNotFound):
		code = "not_found"
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrStaleApproval):
		code = "execution_conflict"
	}
	return temporal.NewNonRetryableApplicationError(code, "coordination."+code, nil)
}
func options(timeout time.Duration, retries int32) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: timeout, ScheduleToCloseTimeout: timeout + 2*time.Minute, HeartbeatTimeout: 10 * time.Second, WaitForCancellation: true,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: 5 * time.Second, MaximumAttempts: retries},
	}
}

func Coordinate(ctx workflow.Context, ref Reference) (Result, error) {
	if !validReference(ref) {
		return Result{}, fail(domain.ErrInvalidInput)
	}
	metadata := workflow.WithActivityOptions(ctx, options(time.Minute, 3))
	var plan Plan
	if err := workflow.ExecuteActivity(metadata, LoadActivity, ref).Get(ctx, &plan); err != nil {
		return Result{}, err
	}
	if err := validatePlan(plan); err != nil {
		return Result{}, fail(err)
	}
	type pending struct {
		ref    TaskReference
		future workflow.Future
	}
	var active []pending
	started := map[string]bool{}
	finished := map[string]TaskResult{}
	for len(finished) < len(plan.Tasks) {
		// Iterate the retained task order, never a Go map, so replay schedules the
		// same ready tasks at the same concurrency boundary.
		for _, task := range plan.Tasks {
			if len(active) >= plan.MaxParallel {
				break
			}
			if started[task.ID] {
				continue
			}
			ready, blocked := true, false
			for _, dep := range task.Dependencies {
				result, ok := finished[dep]
				if !ok {
					ready = false
					break
				}
				blocked = blocked || result.Outcome != "succeeded"
			}
			if !ready {
				continue
			}
			taskRef := TaskReference{Run: ref, TaskID: task.ID}
			name := ExecuteActivity
			if blocked {
				name = BlockActivity
			}
			// One attempt may be reconciled by a later delivery. The activity's
			// immutable attempt fact forbids starting a second producer after an
			// ambiguous crash; it resolves a saved receipt or returns unresolved.
			taskCtx := workflow.WithActivityOptions(ctx, options(time.Duration(task.TimeoutSeconds)*time.Second+time.Minute, 2))
			active = append(active, pending{taskRef, workflow.ExecuteActivity(taskCtx, name, taskRef)})
			started[task.ID] = true
		}
		if len(active) == 0 {
			return Result{}, fail(domain.ErrInvalidInput)
		}
		selector := workflow.NewSelector(ctx)
		selected := -1
		var result TaskResult
		var selectedErr error
		for i, item := range active {
			selector.AddFuture(item.future, func(f workflow.Future) { selected = i; selectedErr = f.Get(ctx, &result) })
		}
		selector.Select(ctx)
		if selectedErr != nil {
			return Result{}, selectedErr
		}
		if selected < 0 || !validTaskResult(active[selected].ref, result) {
			return Result{}, fail(domain.ErrInvalidInput)
		}
		finished[result.TaskID] = result
		active = append(active[:selected], active[selected+1:]...)
	}
	var result Result
	if err := workflow.ExecuteActivity(metadata, FinalizeActivity, ref).Get(ctx, &result); err != nil {
		return Result{}, err
	}
	if result.ReceiptID != ref.ID || !domain.IsLowerHex(result.Digest, 64) {
		return Result{}, fail(domain.ErrInvalidInput)
	}
	return result, nil
}

func protect[A, B any](call func(context.Context, A) (B, error), valid func(A, B) bool) func(context.Context, A) (B, error) {
	return func(ctx context.Context, input A) (out B, err error) {
		defer func() {
			if recover() != nil {
				var zero B
				out = zero
				err = fail(errors.New("activity panic"))
			}
		}()
		inputOK := false
		switch value := any(input).(type) {
		case Reference:
			inputOK = validReference(value)
		case TaskReference:
			inputOK = validReference(value.Run) && domain.IsLowerHex(value.TaskID, 32)
		}
		if !inputOK {
			return out, fail(domain.ErrInvalidInput)
		}
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					activity.RecordHeartbeat(ctx)
				case <-stop:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
		defer func() { close(stop); <-done }()
		out, err = call(ctx, input)
		if err != nil {
			var zero B
			return zero, fail(err)
		}
		if !valid(input, out) {
			var zero B
			return zero, fail(domain.ErrInvalidInput)
		}
		return out, nil
	}
}
func RunWorker(ctx context.Context, c client.Client, activities Activities) error {
	if c == nil || activities == nil {
		return domain.ErrInvalidInput
	}
	activityContext, cancel := context.WithCancel(ctx)
	defer cancel()
	w := worker.New(c, TaskQueue, worker.Options{MaxConcurrentActivityExecutionSize: 4, MaxConcurrentWorkflowTaskExecutionSize: 4, MaxConcurrentActivityTaskPollers: 2, MaxConcurrentWorkflowTaskPollers: 2, BackgroundActivityContext: activityContext, WorkerStopTimeout: 20 * time.Second, DisableRegistrationAliasing: true, DefaultHeartbeatThrottleInterval: time.Second, MaxHeartbeatThrottleInterval: time.Second})
	w.RegisterWorkflowWithOptions(Coordinate, workflow.RegisterOptions{Name: WorkflowName})
	w.RegisterActivityWithOptions(protect(activities.Load, func(_ Reference, p Plan) bool { return validatePlan(p) == nil }), activity.RegisterOptions{Name: LoadActivity})
	w.RegisterActivityWithOptions(protect(activities.Execute, validTaskResult), activity.RegisterOptions{Name: ExecuteActivity})
	w.RegisterActivityWithOptions(protect(activities.Block, validTaskResult), activity.RegisterOptions{Name: BlockActivity})
	w.RegisterActivityWithOptions(protect(activities.Finalize, func(ref Reference, r Result) bool { return r.ReceiptID == ref.ID && domain.IsLowerHex(r.Digest, 64) }), activity.RegisterOptions{Name: FinalizeActivity})
	interrupt := make(chan interface{})
	done := make(chan struct{})
	go func() { defer close(done); <-activityContext.Done(); close(interrupt) }()
	err := w.Run(interrupt)
	cancel()
	<-done
	if err != nil {
		return errors.New("coordination worker unavailable")
	}
	return nil
}
