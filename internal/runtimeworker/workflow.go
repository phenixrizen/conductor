package runtimeworker

import (
	"context"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const WorkflowName = "conductor.runtime.v1"
const ActivityName = "conductor.runtime-collect-and-persist.v1"
const TaskQueue = "conductor-runtime-v1"

func retryableFailure() error {
	return temporal.NewApplicationError("runtime evidence outcome unknown; reconciliation required", "runtime evidence.unavailable")
}
func permanentFailure() error {
	return temporal.NewNonRetryableApplicationError("runtime evidence stopped; inspect exact authorization and evidence", "runtime evidence.stopped", nil)
}
func Collect(ctx workflow.Context, ref contextworkflow.Reference) (contextworkflow.Result, error) {
	if !domain.IsLowerHex(ref.ID, 32) || !domain.IsLowerHex(ref.Binding, 32) {
		return contextworkflow.Result{}, permanentFailure()
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 3 * time.Minute, ScheduleToCloseTimeout: 8 * time.Minute, HeartbeatTimeout: 5 * time.Second, WaitForCancellation: true, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 10 * time.Second, MaximumAttempts: 3}})
	var result contextworkflow.Result
	if err := workflow.ExecuteActivity(ctx, ActivityName, ref).Get(ctx, &result); err != nil {
		return result, err
	}
	if result.ReceiptID != ref.ID || !domain.IsLowerHex(result.Digest, 64) {
		return contextworkflow.Result{}, permanentFailure()
	}
	return result, nil
}
func protect(call contextworkflow.ActivityFunc) contextworkflow.ActivityFunc {
	return func(ctx context.Context, ref contextworkflow.Reference) (out contextworkflow.Result, err error) {
		defer func() {
			if recover() != nil {
				out = contextworkflow.Result{}
				err = permanentFailure()
			}
		}()
		if !domain.IsLowerHex(ref.ID, 32) || !domain.IsLowerHex(ref.Binding, 32) {
			return out, permanentFailure()
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
		out, err = call(ctx, ref)
		if err != nil {
			return contextworkflow.Result{}, safeError(err)
		}
		if out.ReceiptID != ref.ID || !domain.IsLowerHex(out.Digest, 64) {
			return contextworkflow.Result{}, permanentFailure()
		}
		return out, nil
	}
}
func RunWorker(ctx context.Context, c client.Client, call contextworkflow.ActivityFunc) error {
	if c == nil || call == nil {
		return domain.ErrInvalidInput
	}
	active, cancel := context.WithCancel(ctx)
	defer cancel()
	w := worker.New(c, TaskQueue, worker.Options{MaxConcurrentActivityExecutionSize: 2, MaxConcurrentWorkflowTaskExecutionSize: 2, MaxConcurrentActivityTaskPollers: 2, MaxConcurrentWorkflowTaskPollers: 2, BackgroundActivityContext: active, WorkerStopTimeout: 20 * time.Second, DisableRegistrationAliasing: true, DefaultHeartbeatThrottleInterval: time.Second, MaxHeartbeatThrottleInterval: time.Second})
	w.RegisterWorkflowWithOptions(Collect, workflow.RegisterOptions{Name: WorkflowName})
	w.RegisterActivityWithOptions(protect(call), activity.RegisterOptions{Name: ActivityName})
	interrupt := make(chan interface{})
	done := make(chan struct{})
	go func() { defer close(done); <-active.Done(); close(interrupt) }()
	err := w.Run(interrupt)
	cancel()
	<-done
	if err != nil {
		return contextworkflow.ErrUnavailable
	}
	return nil
}
