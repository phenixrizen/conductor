package trackerworkflow

import (
	"context"
	"errors"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const WorkflowName = "conductor.tracker-sync.v1"
const ActivityName = "conductor.tracker-reconcile.v1"
const TaskQueue = "conductor-tracker-v1"

type Reference struct {
	ID      string `json:"id"`
	Binding string `json:"binding"`
}
type Result struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}
type ActivityFunc func(context.Context, Reference) (Result, error)

func Workflow(ctx workflow.Context, ref Reference) (Result, error) {
	if !domain.IsLowerHex(ref.ID, 32) || !domain.IsLowerHex(ref.Binding, 32) {
		return Result{}, temporal.NewNonRetryableApplicationError("invalid tracker reference", "invalid_reference", nil)
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 2 * time.Minute, ScheduleToCloseTimeout: 10 * time.Minute, HeartbeatTimeout: 10 * time.Second, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 30 * time.Second, MaximumAttempts: 5}})
	var result Result
	e := workflow.ExecuteActivity(ctx, ActivityName, ref).Get(ctx, &result)
	if e != nil {
		return Result{}, e
	}
	if result.ID != ref.ID || !domain.IsLowerHex(result.Digest, 64) {
		return Result{}, temporal.NewNonRetryableApplicationError("tracker receipt unavailable", "invalid_receipt", nil)
	}
	return result, nil
}
func RunWorker(ctx context.Context, c client.Client, sync ActivityFunc) error {
	if c == nil || sync == nil {
		return errors.New("tracker worker configuration unavailable")
	}
	w := worker.New(c, TaskQueue, worker.Options{MaxConcurrentActivityExecutionSize: 2, MaxConcurrentWorkflowTaskExecutionSize: 2, WorkerStopTimeout: 10 * time.Second, BackgroundActivityContext: ctx})
	w.RegisterWorkflowWithOptions(Workflow, workflow.RegisterOptions{Name: WorkflowName})
	w.RegisterActivityWithOptions(func(ctx context.Context, ref Reference) (Result, error) {
		done := make(chan struct{})
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					activity.RecordHeartbeat(ctx)
				}
			}
		}()
		defer func() { close(done); <-stopped }()
		result, e := sync(ctx, ref)
		if e != nil {
			return Result{}, temporal.NewApplicationError("tracker activity unavailable", "tracker_unavailable")
		}
		return result, nil
	}, activity.RegisterOptions{Name: ActivityName})
	interrupt := make(chan interface{})
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			close(interrupt)
		case <-finished:
		}
	}()
	defer close(finished)
	if e := w.Run(interrupt); e != nil {
		return errors.New("tracker worker unavailable")
	}
	return nil
}
