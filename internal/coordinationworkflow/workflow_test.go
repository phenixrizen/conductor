package coordinationworkflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

var fixtureRef = Reference{ID: strings.Repeat("a", 32), Binding: strings.Repeat("b", 32)}

func taskID(value string) string { return strings.Repeat(value, 32) }
func result(ref TaskReference, outcome string) TaskResult {
	return TaskResult{TaskID: ref.TaskID, Digest: strings.Repeat("c", 64), Outcome: outcome}
}
func TestDependencySequencingParallelBoundAndBlockedDescendants(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed prerequisite"}[failure], func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			plan := Plan{MaxParallel: 2, Tasks: []Task{{ID: taskID("1"), TimeoutSeconds: 5}, {ID: taskID("2"), TimeoutSeconds: 5}, {ID: taskID("3"), Dependencies: []string{taskID("1"), taskID("2")}, TimeoutSeconds: 5}}}
			var mu sync.Mutex
			active, peak := 0, 0
			done := map[string]bool{}
			blocked := 0
			env.RegisterActivityWithOptions(protect(func(context.Context, Reference) (Plan, error) { return plan, nil }, func(_ Reference, p Plan) bool { return validatePlan(p) == nil }), activity.RegisterOptions{Name: LoadActivity})
			env.RegisterActivityWithOptions(protect(func(_ context.Context, ref TaskReference) (TaskResult, error) {
				mu.Lock()
				if ref.TaskID == taskID("3") && (!done[taskID("1")] || !done[taskID("2")]) {
					t.Error("descendant started before prerequisites")
				}
				active++
				if active > peak {
					peak = active
				}
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				active--
				done[ref.TaskID] = true
				mu.Unlock()
				outcome := "succeeded"
				if failure && ref.TaskID == taskID("2") {
					outcome = "failed"
				}
				return result(ref, outcome), nil
			}, validTaskResult), activity.RegisterOptions{Name: ExecuteActivity})
			env.RegisterActivityWithOptions(protect(func(_ context.Context, ref TaskReference) (TaskResult, error) {
				mu.Lock()
				defer mu.Unlock()
				blocked++
				if ref.TaskID != taskID("3") {
					t.Error("unexpected blocked task")
				}
				return result(ref, "blocked"), nil
			}, validTaskResult), activity.RegisterOptions{Name: BlockActivity})
			env.RegisterActivityWithOptions(protect(func(_ context.Context, ref Reference) (Result, error) {
				return Result{ReceiptID: ref.ID, Digest: strings.Repeat("d", 64)}, nil
			}, func(ref Reference, r Result) bool { return r.ReceiptID == ref.ID }), activity.RegisterOptions{Name: FinalizeActivity})
			env.ExecuteWorkflow(Coordinate, fixtureRef)
			if err := env.GetWorkflowError(); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if peak != 2 || active != 0 {
				t.Fatalf("concurrency peak=%d active=%d", peak, active)
			}
			if failure && blocked != 1 || !failure && blocked != 0 {
				t.Fatalf("blocked=%d", blocked)
			}
		})
	}
}
func TestActivityBoundaryRejectsPrivateResultsAndErrors(t *testing.T) {
	for _, call := range []func(context.Context, TaskReference) (TaskResult, error){
		func(context.Context, TaskReference) (TaskResult, error) {
			return TaskResult{}, errors.New("source-token-canary")
		},
		func(context.Context, TaskReference) (TaskResult, error) { panic("source-token-canary") },
		func(context.Context, TaskReference) (TaskResult, error) {
			return TaskResult{TaskID: "source-token-canary"}, nil
		},
	} {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestActivityEnvironment()
		safe := protect(call, validTaskResult)
		env.RegisterActivity(safe)
		_, err := env.ExecuteActivity(safe, TaskReference{Run: fixtureRef, TaskID: taskID("1")})
		if err == nil || strings.Contains(err.Error(), "canary") {
			t.Fatalf("unsafe or absent failure: %v", err)
		}
	}
}
func TestInvalidDAGAndIdentityNeverStartProducer(t *testing.T) {
	for _, plan := range []Plan{{MaxParallel: 1, Tasks: []Task{{ID: taskID("1"), TimeoutSeconds: 1, Dependencies: []string{taskID("2")}}}}, {MaxParallel: 1, Tasks: []Task{{ID: taskID("1"), TimeoutSeconds: 1, Dependencies: []string{taskID("1")}}}}, {MaxParallel: 5, Tasks: []Task{{ID: taskID("1"), TimeoutSeconds: 1}}}} {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterActivityWithOptions(protect(func(context.Context, Reference) (Plan, error) { return plan, nil }, func(_ Reference, p Plan) bool { return validatePlan(p) == nil }), activity.RegisterOptions{Name: LoadActivity})
		env.ExecuteWorkflow(Coordinate, fixtureRef)
		if env.GetWorkflowError() == nil {
			t.Fatal("invalid plan executed")
		}
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(Coordinate, Reference{ID: "source-token-canary"})
	if err := env.GetWorkflowError(); err == nil || strings.Contains(err.Error(), "canary") {
		t.Fatalf("unsafe invalid identity: %v", err)
	}
}
