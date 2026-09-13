package contextworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
)

// Embedding unused SDK interfaces keeps the production boundary narrow without
// inventing mock behavior for unrelated client operations.
type fixtureClient struct {
	client.Client
	description *workflowservicepb.DescribeWorkflowExecutionResponse
	describeErr error
	service     *fixtureService
	start       func(client.StartWorkflowOptions, interface{}, []interface{}) (client.WorkflowRun, error)
	cancels     []string
}

func (c *fixtureClient) ExecuteWorkflow(_ context.Context, options client.StartWorkflowOptions, name interface{}, args ...interface{}) (client.WorkflowRun, error) {
	return c.start(options, name, args)
}
func (c *fixtureClient) DescribeWorkflowExecution(context.Context, string, string) (*workflowservicepb.DescribeWorkflowExecutionResponse, error) {
	return c.description, c.describeErr
}
func (c *fixtureClient) WorkflowService() workflowservicepb.WorkflowServiceClient { return c.service }
func (c *fixtureClient) CancelWorkflow(_ context.Context, _, run string) error {
	c.cancels = append(c.cancels, run)
	return nil
}

type fixtureService struct {
	workflowservicepb.WorkflowServiceClient
	history *workflowservicepb.GetWorkflowExecutionHistoryResponse
	closed  *workflowservicepb.GetWorkflowExecutionHistoryResponse
	calls   int
}

func (s *fixtureService) GetWorkflowExecutionHistory(_ context.Context, request *workflowservicepb.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservicepb.GetWorkflowExecutionHistoryResponse, error) {
	s.calls++
	if request.MaximumPageSize != 1 || request.WaitNewEvent || len(request.NextPageToken) > 0 {
		panic("unbounded history request")
	}
	if request.HistoryEventFilterType == enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT {
		return s.closed, nil
	}
	return s.history, nil
}

type fixtureRun struct{ client.WorkflowRun }

func (fixtureRun) GetRunID() string { return "run-1" }

func payload(t *testing.T, value any) *commonpb.Payloads {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &commonpb.Payloads{Payloads: []*commonpb.Payload{{Metadata: map[string][]byte{"encoding": []byte("json/plain")}, Data: data}}}
}

func runtimeFixture(t *testing.T) (*Runtime, *fixtureClient) {
	t.Helper()
	initial := &historypb.WorkflowExecutionStartedEventAttributes{
		WorkflowType: &commonpb.WorkflowType{Name: WorkflowName},
		TaskQueue:    &taskqueuepb.TaskQueue{Name: TaskQueue}, Input: payload(t, testReference),
	}
	s := &fixtureService{history: &workflowservicepb.GetWorkflowExecutionHistoryResponse{History: &historypb.History{Events: []*historypb.HistoryEvent{{
		EventId: 1, Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: initial},
	}}}}, closed: &workflowservicepb.GetWorkflowExecutionHistoryResponse{History: &historypb.History{Events: []*historypb.HistoryEvent{{
		Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{Result: payload(t, testResult)}},
	}}}}}
	c := &fixtureClient{service: s, description: &workflowservicepb.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
		Type: &commonpb.WorkflowType{Name: WorkflowName}, Execution: &commonpb.WorkflowExecution{WorkflowId: WorkflowName + "/" + testReference.ID, RunId: "run-1"},
		Status: enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
	}}}
	r, err := NewRuntime(c, "fixture", TaskQueue)
	if err != nil {
		t.Fatal(err)
	}
	return r, c
}

func TestStartExplicitPoliciesAndSafeErrors(t *testing.T) {
	r, c := runtimeFixture(t)
	c.start = func(options client.StartWorkflowOptions, name interface{}, args []interface{}) (client.WorkflowRun, error) {
		if options.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL || options.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE ||
			!options.WorkflowExecutionErrorWhenAlreadyStarted || options.RetryPolicy != nil || options.CronSchedule != "" || options.WorkflowExecutionTimeout != 12*time.Minute ||
			options.TaskQueue != TaskQueue || name != WorkflowName || len(args) != 1 || args[0] != testReference || len(options.Memo) != 0 || len(options.SearchAttributes) != 0 {
			t.Fatal("unsafe or unbounded workflow start")
		}
		return fixtureRun{}, nil
	}
	if observed, err := r.Start(context.Background(), WorkflowName+"/"+testReference.ID, testReference); err != nil || observed.State != "running" {
		t.Fatalf("start: %#v %v", observed, err)
	}
	c.start = func(client.StartWorkflowOptions, interface{}, []interface{}) (client.WorkflowRun, error) {
		return nil, errors.New("source-token-canary transport details")
	}
	if _, err := r.Start(context.Background(), WorkflowName+"/"+testReference.ID, testReference); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "canary") {
		t.Fatalf("transport error leaked or lost ambiguity: %v", err)
	}
}

func TestLookupAndCancelRequireActualBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fixtureClient)
		want   error
	}{
		{"missing", func(c *fixtureClient) { c.describeErr = serviceerror.NewNotFound("source-token-canary") }, ErrNotFound},
		{"namespace missing", func(c *fixtureClient) { c.describeErr = serviceerror.NewNamespaceNotFound("source-token-canary") }, ErrInvalidConfiguration},
		{"denied", func(c *fixtureClient) {
			c.describeErr = serviceerror.NewPermissionDenied("source-token-canary", "source-token-canary")
		}, ErrInvalidConfiguration},
		{"unavailable", func(c *fixtureClient) { c.describeErr = errors.New("source-token-canary") }, ErrUnavailable},
		{"wrong type", func(c *fixtureClient) { c.description.WorkflowExecutionInfo.Type.Name = "other" }, ErrBindingMismatch},
		{"wrong binding", func(c *fixtureClient) {
			c.service.history.History.Events[0].GetWorkflowExecutionStartedEventAttributes().Input = payload(t, Reference{ID: testReference.ID, Binding: strings.Repeat("f", 32)})
		}, ErrBindingMismatch},
		{"unbounded payload", func(c *fixtureClient) {
			c.service.history.History.Events[0].GetWorkflowExecutionStartedEventAttributes().Input.Payloads[0].Data = []byte(strings.Repeat("x", 1025))
		}, ErrBindingMismatch},
		{"continued execution", func(c *fixtureClient) {
			c.service.history.History.Events[0].GetWorkflowExecutionStartedEventAttributes().ContinuedExecutionRunId = "previous"
		}, ErrBindingMismatch},
		{"reset execution", func(c *fixtureClient) {
			c.service.history.History.Events[0].GetWorkflowExecutionStartedEventAttributes().OriginalExecutionRunId = "previous"
		}, ErrBindingMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, c := runtimeFixture(t)
			test.mutate(c)
			if _, err := r.Lookup(context.Background(), WorkflowName+"/"+testReference.ID, "", testReference); !errors.Is(err, test.want) {
				t.Fatalf("lookup: %v", err)
			}
			if err := r.Cancel(context.Background(), WorkflowName+"/"+testReference.ID, "", testReference); !errors.Is(err, test.want) || len(c.cancels) != 0 {
				t.Fatalf("cancel: %v calls=%v", err, c.cancels)
			}
		})
	}
	r, c := runtimeFixture(t)
	if err := r.Cancel(context.Background(), WorkflowName+"/"+testReference.ID, "", testReference); err != nil || len(c.cancels) != 1 || c.cancels[0] != "run-1" {
		t.Fatalf("cancel did not bind inspected run: %v %v", err, c.cancels)
	}
}

func TestCompletedResultRequiresMatchingReceipt(t *testing.T) {
	r, c := runtimeFixture(t)
	c.description.WorkflowExecutionInfo.Status = enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED
	got, err := r.Lookup(context.Background(), WorkflowName+"/"+testReference.ID, "", testReference)
	if err != nil || got.Result == nil || *got.Result != testResult || c.service.calls != 2 {
		t.Fatalf("completion: %#v %v history calls=%d", got, err, c.service.calls)
	}
	c.service.closed.History.Events[0].GetWorkflowExecutionCompletedEventAttributes().Result = payload(t, Result{ReceiptID: strings.Repeat("f", 32), Digest: testResult.Digest})
	if _, err := r.Lookup(context.Background(), WorkflowName+"/"+testReference.ID, "", testReference); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("accepted unrelated receipt: %v", err)
	}
}
