package contextworkflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var (
	ErrNotFound             = errors.New("collection execution not found")
	ErrUnavailable          = errors.New("collection execution unavailable")
	ErrBindingMismatch      = errors.New("collection execution binding mismatch")
	ErrInvalidConfiguration = errors.New("invalid collection runtime configuration")
)

// Observation reports a checked execution, not a prediction from a run handle.
// Completion identifies a receipt; the caller still resolves it in PostgreSQL.
type Observation struct {
	RunID  string
	State  string
	Result *Result
}

type Runtime struct {
	client       client.Client
	namespace    string
	taskQueue    string
	address      string
	target       string
	workflowName string
	timeout      time.Duration
}

// NewRuntime requires the same namespace used to configure the SDK client.
// Configure that client's MaxPayloadSize to at most 1 MiB, use a safe logger,
// and do not attach source, credentials, or arbitrary context propagators.
func NewRuntime(c client.Client, namespace, taskQueue string) (*Runtime, error) {
	if c == nil || !configurationName.MatchString(namespace) || !configurationName.MatchString(taskQueue) {
		return nil, ErrInvalidConfiguration
	}
	return &Runtime{client: c, namespace: namespace, taskQueue: taskQueue, workflowName: WorkflowName, timeout: 12 * time.Minute}, nil
}

func (r *Runtime) validWorkflowID(id string, ref Reference) bool {
	return validReference(ref) && id == r.workflowName+"/"+ref.ID
}

// ForWorkflow shares the checked type/input/cluster binding and duplicate policy
// across Conductor workflows. Only explicitly implemented profiles are admitted;
// callers cannot use this boundary to dispatch arbitrary repository workflow code.
func (r *Runtime) ForWorkflow(name string) (*Runtime, error) {
	copy := *r
	switch name {
	case WorkflowName:
		copy.timeout = 12 * time.Minute
	case "conductor.coordinate.v1":
		copy.timeout = 20 * time.Hour
	case "conductor.runtime.v1":
		copy.timeout = 10 * time.Minute
	case "conductor.publish.v1":
		copy.timeout = 30 * time.Minute
	default:
		return nil, ErrInvalidConfiguration
	}
	copy.workflowName = name
	return &copy, nil
}

// Start rejects both running and retained closed duplicates. A duplicate is
// attached only after reading and checking its real type and initial binding.
func (r *Runtime) Start(ctx context.Context, workflowID string, ref Reference) (Observation, error) {
	if !r.validWorkflowID(workflowID, ref) {
		return Observation{}, ErrBindingMismatch
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := r.checkTarget(ctx); err != nil {
		return Observation{}, err
	}
	run, err := r.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: r.taskQueue,
		WorkflowExecutionTimeout: r.timeout, WorkflowRunTimeout: r.timeout,
		WorkflowTaskTimeout:                      10 * time.Second,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}, r.workflowName, ref)
	if err == nil {
		if run.GetRunID() == "" || len(run.GetRunID()) > 128 {
			return Observation{}, ErrUnavailable
		}
		return Observation{RunID: run.GetRunID(), State: "running"}, nil
	}
	var duplicate *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &duplicate) {
		return r.Lookup(ctx, workflowID, duplicate.RunId, ref)
	}
	return Observation{}, classifyRPC(err)
}

func classifyRPC(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var namespace *serviceerror.NamespaceNotFound
	var invalid *serviceerror.InvalidArgument
	var denied *serviceerror.PermissionDenied
	if errors.As(err, &namespace) || errors.As(err, &invalid) || errors.As(err, &denied) || status.Code(err) == codes.Unauthenticated {
		return ErrInvalidConfiguration
	}
	var missing *serviceerror.NotFound
	if errors.As(err, &missing) {
		return ErrNotFound
	}
	return ErrUnavailable
}

func decodePayload(payloads *commonpb.Payloads, target any) error {
	if payloads == nil || len(payloads.Payloads) != 1 || proto.Size(payloads) > 1024 {
		return ErrBindingMismatch
	}
	p := payloads.Payloads[0]
	if p == nil || len(p.Metadata) != 1 || string(p.Metadata["encoding"]) != "json/plain" {
		return ErrBindingMismatch
	}
	decoder := json.NewDecoder(bytes.NewReader(p.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrBindingMismatch
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrBindingMismatch
	}
	return nil
}

// Lookup reads only the bounded first event and, when completed, a close event.
// It never loads source history, waits for completion, or uses visibility search.
func (r *Runtime) Lookup(ctx context.Context, workflowID, runID string, ref Reference) (Observation, error) {
	if !r.validWorkflowID(workflowID, ref) || len(runID) > 128 {
		return Observation{}, ErrBindingMismatch
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := r.checkTarget(ctx); err != nil {
		return Observation{}, err
	}
	description, err := r.client.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		return Observation{}, classifyRPC(err)
	}
	if description == nil || proto.Size(description) > 64*1024 {
		return Observation{}, ErrBindingMismatch
	}
	info := description.GetWorkflowExecutionInfo()
	if info.GetType().GetName() != r.workflowName || info.GetExecution().GetWorkflowId() != workflowID ||
		info.GetExecution().GetRunId() == "" || len(info.GetExecution().GetRunId()) > 128 ||
		(runID != "" && info.GetExecution().GetRunId() != runID) {
		return Observation{}, ErrBindingMismatch
	}
	runID = info.GetExecution().GetRunId()
	execution := &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: runID}
	history, err := r.client.WorkflowService().GetWorkflowExecutionHistory(ctx, &workflowservicepb.GetWorkflowExecutionHistoryRequest{
		Namespace: r.namespace, Execution: execution, MaximumPageSize: 1,
		HistoryEventFilterType: enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
	})
	if err != nil {
		return Observation{}, classifyRPC(err)
	}
	if history == nil || proto.Size(history) > 64*1024 || len(history.GetHistory().GetEvents()) < 1 || len(history.GetHistory().GetEvents()) > 32 {
		return Observation{}, ErrBindingMismatch
	}
	first := history.GetHistory().GetEvents()[0]
	started := first.GetWorkflowExecutionStartedEventAttributes()
	var actual Reference
	if first.GetEventId() != 1 || started.GetWorkflowType().GetName() != r.workflowName || decodePayload(started.GetInput(), &actual) != nil || actual != ref {
		return Observation{}, ErrBindingMismatch
	}
	if started.GetTaskQueue().GetName() != r.taskQueue || started.GetContinuedExecutionRunId() != "" ||
		started.GetRetryPolicy() != nil || started.GetAttempt() > 1 ||
		(started.GetOriginalExecutionRunId() != "" && started.GetOriginalExecutionRunId() != runID) ||
		(started.GetFirstExecutionRunId() != "" && started.GetFirstExecutionRunId() != runID) {
		return Observation{}, ErrBindingMismatch
	}
	observation := Observation{RunID: runID}
	switch info.GetStatus() {
	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
		observation.State = "running"
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		observation.State = "completed"
	case enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		observation.State = "cancelled"
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:
		observation.State = "failed"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		observation.State = "timed_out"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		observation.State = "terminated"
	default: // This workflow never retries, resets, or continues as new.
		return Observation{}, ErrBindingMismatch
	}
	if observation.State != "completed" {
		return observation, nil
	}
	closed, err := r.client.WorkflowService().GetWorkflowExecutionHistory(ctx, &workflowservicepb.GetWorkflowExecutionHistoryRequest{
		Namespace: r.namespace, Execution: execution, MaximumPageSize: 1,
		HistoryEventFilterType: enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT,
	})
	if err != nil {
		return Observation{}, classifyRPC(err)
	}
	if closed == nil || proto.Size(closed) > 4096 || len(closed.GetHistory().GetEvents()) != 1 || len(closed.GetNextPageToken()) != 0 {
		return Observation{}, ErrBindingMismatch
	}
	completed := closed.GetHistory().GetEvents()[0].GetWorkflowExecutionCompletedEventAttributes()
	var result Result
	if decodePayload(completed.GetResult(), &result) != nil || !validResult(ref, result) {
		return Observation{}, ErrBindingMismatch
	}
	observation.Result = &result
	return observation, nil
}

// Cancel targets the checked run so a racing replacement cannot be cancelled.
// A successful RPC acknowledges intent, not activity cleanup or rollback.
func (r *Runtime) Cancel(ctx context.Context, workflowID, runID string, ref Reference) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	observation, err := r.Lookup(ctx, workflowID, runID, ref)
	if err != nil {
		return err
	}
	if observation.State != "running" {
		return nil
	}
	if err := r.client.CancelWorkflow(ctx, workflowID, observation.RunID); err != nil {
		return classifyRPC(err)
	}
	return nil
}
