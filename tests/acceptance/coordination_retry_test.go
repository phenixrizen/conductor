package acceptance_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/coordinationworker"
	"github.com/phenixrizen/conductor/internal/coordinationworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
)

// The actual activity and database commit run before the acknowledgment is lost.
// Repeated delivery must recover those immutable receipts without another Docker
// producer. Only transport failure injection is synthetic; no result is invented.
type coordinationRetryActivities struct {
	*coordinationworker.Activity
	firstTask                        string
	mu                               sync.Mutex
	loads, executions, finalizations int
	taskReceipt                      coordinationworkflow.TaskResult
	runReceipt                       coordinationworkflow.Result
}

func (a *coordinationRetryActivities) Load(ctx context.Context, ref coordinationworkflow.Reference) (coordinationworkflow.Plan, error) {
	a.mu.Lock()
	a.loads++
	first := a.loads == 1
	a.mu.Unlock()
	if first {
		return coordinationworkflow.Plan{}, retryAcknowledgment()
	}
	return a.Activity.Load(ctx, ref)
}

func retryAcknowledgment() error {
	return fmt.Errorf("private-retry-cause: %w", contextworkflow.Failure{Code: "database_unavailable", Retryable: true})
}

func (a *coordinationRetryActivities) Execute(ctx context.Context, ref coordinationworkflow.TaskReference) (coordinationworkflow.TaskResult, error) {
	r, err := a.Activity.Execute(ctx, ref)
	if err != nil || ref.TaskID != a.firstTask {
		return r, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.executions++
	if a.executions == 1 {
		a.taskReceipt = r
		return coordinationworkflow.TaskResult{}, retryAcknowledgment()
	}
	if r != a.taskReceipt {
		return coordinationworkflow.TaskResult{}, domain.ErrConflict
	}
	return r, nil
}

func (a *coordinationRetryActivities) Finalize(ctx context.Context, ref coordinationworkflow.Reference) (coordinationworkflow.Result, error) {
	r, err := a.Activity.Finalize(ctx, ref)
	if err != nil {
		return r, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finalizations++
	if a.finalizations == 1 {
		a.runReceipt = r
		return coordinationworkflow.Result{}, retryAcknowledgment()
	}
	if r != a.runReceipt {
		return coordinationworkflow.Result{}, domain.ErrConflict
	}
	return r, nil
}

func (a *coordinationRetryActivities) assertRecovered(t *testing.T) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loads != 2 || a.executions != 2 || a.finalizations != 2 || a.taskReceipt.Outcome != "succeeded" || a.runReceipt.Digest == "" {
		t.Fatalf("retry stages did not recover the retained receipts: load=%d execute=%d finalize=%d", a.loads, a.executions, a.finalizations)
	}
}
