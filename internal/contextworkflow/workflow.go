// Package contextworkflow keeps collection sequencing in Temporal while source,
// credentials, manifests, and durable collection facts remain outside its history.
package contextworkflow

import (
	"context"
	"errors"
	"regexp"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	WorkflowName = "conductor.collect.v1"
	ActivityName = "conductor.collect-and-persist.v1"
	TaskQueue    = "conductor-collection-v1"
)

// Reference contains only server-generated opaque identifiers. A matching
// workflow ID alone does not prove that an execution has the expected binding.
type Reference struct {
	ID      string `json:"id"`
	Binding string `json:"binding"`
}

// Result is a reference to an already committed immutable receipt, never source.
type Result struct {
	ReceiptID string `json:"receiptId"`
	Digest    string `json:"digest"`
}

// ActivityFunc first resolves an existing receipt, then performs and commits any
// fresh work under its own authorization and cancellation rules. It must honor
// ctx and return context.Canceled when cancelled; successful persisted receipts
// remain facts even if a later workflow cancellation races with reporting them.
type ActivityFunc func(context.Context, Reference) (Result, error)

// Failure deliberately has no underlying cause: raw provider, SQL, or filesystem
// errors must not become Temporal failure messages or stack-trace diagnostics.
type Failure struct {
	Code       string
	Retryable  bool
	RetryAfter time.Duration
}

func (f Failure) Error() string { return "collection activity failed" }

var opaqueID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var configurationName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func validReference(ref Reference) bool {
	return opaqueID.MatchString(ref.ID) && opaqueID.MatchString(ref.Binding)
}

func validResult(ref Reference, result Result) bool {
	return result.ReceiptID == ref.ID && digest.MatchString(result.Digest)
}

func collectionWorkflow(ctx workflow.Context, ref Reference) (Result, error) {
	if !validReference(ref) {
		return Result{}, safeFailure(Failure{Code: "invalid_request"})
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    2 * time.Minute,
		ScheduleToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:       5 * time.Second,
		WaitForCancellation:    true,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: time.Second, BackoffCoefficient: 2,
			MaximumInterval: 30 * time.Second, MaximumAttempts: 3,
		},
	})
	var result Result
	if err := workflow.ExecuteActivity(ctx, ActivityName, ref).Get(ctx, &result); err != nil {
		return Result{}, err
	}
	if !validResult(ref, result) {
		return Result{}, safeFailure(Failure{Code: "invalid_result"})
	}
	return result, nil
}

func safeFailure(err error) error {
	if errors.Is(err, context.Canceled) || temporal.IsCanceledError(err) {
		return temporal.NewCanceledError()
	}
	failure := Failure{Code: "internal"}
	var classified Failure
	var pointer *Failure
	if errors.As(err, &classified) {
		failure = classified
	} else if errors.As(err, &pointer) && pointer != nil {
		failure = *pointer
	}
	if failure.Code == "cancelled" {
		return temporal.NewCanceledError()
	}
	retryable := false
	switch failure.Code {
	case "unavailable", "database_unavailable", "rate_limited":
		retryable = failure.Retryable
	case "invalid_request", "invalid_configuration", "binding_mismatch", "permission_revoked", "integration_disabled", "not_found", "invalid_result", "internal":
	default:
		failure.Code = "internal"
	}
	// A longer provider delay cannot fit this workflow's reviewed retry policy.
	// Fail explicitly instead of retrying earlier than the provider requested.
	if failure.RetryAfter < 0 || failure.RetryAfter > 30*time.Second {
		retryable = false
		failure.RetryAfter = 0
	}
	if !retryable {
		failure.RetryAfter = 0
	}
	return temporal.NewApplicationErrorWithOptions(failure.Code, "collection."+failure.Code, temporal.ApplicationErrorOptions{
		NonRetryable: !retryable, NextRetryDelay: failure.RetryAfter,
	})
}

func protectedActivity(collect ActivityFunc) ActivityFunc {
	return func(ctx context.Context, ref Reference) (result Result, err error) {
		// Recovery belongs at this boundary because SDK panic conversion otherwise
		// records the panic value and stack in durable history and worker logs.
		defer func() {
			if recover() != nil {
				result, err = Result{}, safeFailure(Failure{Code: "internal"})
			}
		}()
		if !validReference(ref) {
			return Result{}, safeFailure(Failure{Code: "invalid_request"})
		}
		stop := make(chan struct{})
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					activity.RecordHeartbeat(ctx) // No details, even opaque source metadata.
				case <-stop:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
		defer func() { close(stop); <-stopped }()
		result, err = collect(ctx, ref)
		if err != nil {
			return Result{}, safeFailure(err)
		}
		if !validResult(ref, result) {
			return Result{}, safeFailure(Failure{Code: "invalid_result"})
		}
		return result, nil
	}
}

// RunWorker runs bounded workflow and activity concurrency until cancellation.
// The caller owns the SDK client and must keep it open until this returns.
func RunWorker(ctx context.Context, c client.Client, taskQueue string, collect ActivityFunc) error {
	if c == nil || collect == nil || !configurationName.MatchString(taskQueue) {
		return ErrInvalidConfiguration
	}
	activityContext, cancel := context.WithCancel(ctx)
	defer cancel()
	w := worker.New(c, taskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize: 2, MaxConcurrentWorkflowTaskExecutionSize: 2,
		MaxConcurrentActivityTaskPollers: 2, MaxConcurrentWorkflowTaskPollers: 2,
		BackgroundActivityContext: activityContext,
		WorkerStopTimeout:         10 * time.Second, DisableRegistrationAliasing: true,
		DefaultHeartbeatThrottleInterval: time.Second, MaxHeartbeatThrottleInterval: time.Second,
	})
	w.RegisterWorkflowWithOptions(collectionWorkflow, workflow.RegisterOptions{Name: WorkflowName})
	w.RegisterActivityWithOptions(protectedActivity(collect), activity.RegisterOptions{Name: ActivityName})
	interrupt := make(chan interface{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-activityContext.Done()
		cancel()
		close(interrupt)
	}()
	err := w.Run(interrupt)
	cancel()
	// Join the watcher on startup/fatal failure as well as normal shutdown.
	<-stopped
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
