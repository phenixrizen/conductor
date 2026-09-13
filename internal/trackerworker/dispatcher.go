package trackerworker

import (
	"context"
	"errors"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/trackerworkflow"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

type DispatchStore interface {
	ClaimTrackerDispatch(context.Context, string, string) (*store.TrackerDispatch, error)
	FinishTrackerDispatch(context.Context, store.TrackerDispatch, string, string) error
	CompleteTrackerSync(context.Context, string, string, domain.TrackerObservation) (domain.TrackerObservation, error)
}
type Dispatcher struct {
	db                         DispatchStore
	engine                     client.Client
	address, namespace, target string
}

func NewDispatcher(ctx context.Context, db DispatchStore, engine client.Client, address, namespace string) (*Dispatcher, error) {
	target, e := contextworkflow.RuntimeTarget(ctx, engine, namespace, address, trackerworkflow.TaskQueue)
	if e != nil {
		return nil, e
	}
	return &Dispatcher{db, engine, address, namespace, target}, nil
}
func (d *Dispatcher) Step(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	target, e := contextworkflow.RuntimeTarget(ctx, d.engine, d.namespace, d.address, trackerworkflow.TaskQueue)
	if e != nil {
		return false, errors.New("tracker runtime unavailable")
	}
	if target != d.target {
		return false, errors.New("tracker runtime binding changed")
	}
	item, e := d.db.ClaimTrackerDispatch(ctx, d.target, d.namespace)
	if e != nil || item == nil {
		return false, e
	}
	finish := func(outcome, run string) (bool, error) {
		return true, d.db.FinishTrackerDispatch(ctx, *item, outcome, run)
	}
	unresolved := func(code string) (bool, error) {
		if saved, e := d.db.CompleteTrackerSync(ctx, item.ID, item.Binding, domain.TrackerObservation{State: "unknown", Code: code}); e != nil {
			return true, e
		} else if saved.State != "unknown" {
			return finish("delivered", item.RunID)
		}
		return finish("unresolved", item.RunID)
	}
	if item.HasReceipt {
		return finish("delivered", item.RunID)
	}
	if item.Target != d.target || item.Namespace != d.namespace {
		return unresolved("runtime_binding_changed")
	}
	workflowID := trackerworkflow.WorkflowName + "/" + item.ID
	ref := trackerworkflow.Reference{ID: item.ID, Binding: item.Binding}
	description, e := d.engine.DescribeWorkflowExecution(ctx, workflowID, item.RunID)
	var missing *serviceerror.NotFound
	if errors.As(e, &missing) {
		// Missing retained history after any attempted handoff is explicit uncertainty,
		// not permission to create a replacement workflow with the same authority.
		if item.PreviouslyAttempted || item.RunID != "" {
			return unresolved("workflow_history_unavailable")
		}
		run, startErr := d.engine.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: workflowID, TaskQueue: trackerworkflow.TaskQueue, WorkflowExecutionTimeout: 15 * time.Minute, WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL, WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE, WorkflowExecutionErrorWhenAlreadyStarted: true}, trackerworkflow.WorkflowName, ref)
		if startErr != nil {
			return finish("retry", "")
		}
		return finish("delivered", run.GetRunID())
	}
	if e != nil {
		return finish("retry", item.RunID)
	}
	info := description.GetWorkflowExecutionInfo()
	runID := info.GetExecution().GetRunId()
	if info.GetType().GetName() != trackerworkflow.WorkflowName || info.GetExecution().GetWorkflowId() != workflowID || info.GetTaskQueue() != trackerworkflow.TaskQueue || runID == "" || item.RunID != "" && item.RunID != runID {
		return unresolved("workflow_identity_mismatch")
	}
	history := d.engine.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	if !history.HasNext() {
		return unresolved("workflow_history_unavailable")
	}
	event, e := history.Next()
	if e != nil {
		return finish("retry", runID)
	}
	started := event.GetWorkflowExecutionStartedEventAttributes()
	var actual trackerworkflow.Reference
	if started == nil || started.GetWorkflowType().GetName() != trackerworkflow.WorkflowName || started.GetTaskQueue().GetName() != trackerworkflow.TaskQueue || converter.GetDefaultDataConverter().FromPayloads(started.GetInput(), &actual) != nil || actual != ref {
		return unresolved("workflow_identity_mismatch")
	}
	// A terminal execution without its database activity receipt is never reported
	// as synchronized. The user can inspect this unknown result before a new intent.
	if info.GetStatus() != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return unresolved("workflow_ended_without_receipt")
	}
	return finish("delivered", runID)
}
func (d *Dispatcher) Run(ctx context.Context, report func(string)) error {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		_, e := d.Step(ctx)
		if e != nil && report != nil {
			report("tracker dispatch unavailable; reconciliation pending")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}
