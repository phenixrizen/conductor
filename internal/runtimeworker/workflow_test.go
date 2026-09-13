package runtimeworker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestRuntimeEvidenceWorkflowBoundsAndSafeFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		fail bool
	}{{"receipt", false}, {"panic", true}} {
		t.Run(test.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			ref := contextworkflow.Reference{ID: strings.Repeat("a", 32), Binding: strings.Repeat("b", 32)}
			env.RegisterActivityWithOptions(protect(func(context.Context, contextworkflow.Reference) (contextworkflow.Result, error) {
				if test.fail {
					panic("private-source-secret")
				}
				return contextworkflow.Result{ReceiptID: ref.ID, Digest: strings.Repeat("c", 64)}, nil
			}), activity.RegisterOptions{Name: ActivityName})
			env.ExecuteWorkflow(Collect, ref)
			err := env.GetWorkflowError()
			if test.fail {
				if err == nil || strings.Contains(err.Error(), "private-source-secret") {
					t.Fatalf("unsafe failure %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var result contextworkflow.Result
				if env.GetWorkflowResult(&result) != nil || result.ReceiptID != ref.ID {
					t.Fatal("receipt mismatch")
				}
			}
		})
	}
}

type dispatchDB struct {
	item                      *store.RuntimeOperation
	state, run, code, receipt string
	receiptFinished           bool
}

func (d *dispatchDB) ClaimRuntimeDispatch(context.Context, string, string) (*store.RuntimeOperation, error) {
	return d.item, nil
}
func (d *dispatchDB) FinishRuntimeDispatch(_ context.Context, _ store.RuntimeOperation, state, run, code string) error {
	d.state, d.run, d.code = state, run, code
	return nil
}
func (d *dispatchDB) RuntimeOperationReceipt(context.Context, string, string) (string, error) {
	return d.receipt, nil
}
func (d *dispatchDB) FinishRuntimeReceiptDispatch(context.Context, store.RuntimeOperation) error {
	d.receiptFinished = true
	return nil
}

type dispatchRuntime struct {
	lookup          contextworkflow.Observation
	err             error
	starts, lookups int
}

func (r *dispatchRuntime) Lookup(context.Context, string, string, contextworkflow.Reference) (contextworkflow.Observation, error) {
	r.lookups++
	return r.lookup, r.err
}
func (r *dispatchRuntime) Start(context.Context, string, contextworkflow.Reference) (contextworkflow.Observation, error) {
	r.starts++
	return contextworkflow.Observation{RunID: "fixture-run", State: "running"}, nil
}
func TestDispatcherNeverReplacesMissingKnownExecution(t *testing.T) {
	for _, mode := range []string{"new", "known", "expired", "binding", "receipt", "receipt-running", "receipt-completed", "wrong-receipt"} {
		t.Run(mode, func(t *testing.T) {
			item := &store.RuntimeOperation{ID: strings.Repeat("a", 32), Work: store.RuntimeWork{Binding: strings.Repeat("b", 32)}}
			db := &dispatchDB{item: item}
			runtime := &dispatchRuntime{err: contextworkflow.ErrNotFound}
			switch mode {
			case "known":
				item.RunID = "known"
			case "expired":
				item.UncertaintyExpired = true
			case "binding":
				item.Mismatch = true
			case "receipt":
				db.receipt = strings.Repeat("c", 64)
			case "receipt-running", "receipt-completed":
				db.receipt = strings.Repeat("c", 64)
				runtime.err = nil
				runtime.lookup = contextworkflow.Observation{RunID: "known", State: strings.TrimPrefix(mode, "receipt-"), Result: &contextworkflow.Result{ReceiptID: item.ID, Digest: db.receipt}}
			case "wrong-receipt":
				runtime.err = nil
				runtime.lookup = contextworkflow.Observation{RunID: "known", State: "completed", Result: &contextworkflow.Result{ReceiptID: item.ID, Digest: strings.Repeat("d", 64)}}
			}
			target := strings.Repeat("f", 64)
			d, err := NewDispatcher(db, runtime, "default", target, func(context.Context) (string, error) { return target, nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			if mode == "new" {
				if runtime.starts != 1 || db.state != "running" {
					t.Fatal("new intent not dispatched")
				}
			} else if runtime.starts != 0 {
				t.Fatal("replacement execution started")
			}
			if mode == "receipt" {
				if runtime.lookups != 1 || db.state != "unresolved" {
					t.Fatal("receipt fabricated execution observation")
				}
			} else if mode == "receipt-running" || mode == "receipt-completed" {
				if db.state != strings.TrimPrefix(mode, "receipt-") {
					t.Fatal("stored receipt substituted for observed execution state")
				}
			} else if mode != "new" && db.state != "unresolved" {
				t.Fatalf("uncertainty concealed: %s", db.state)
			}
		})
	}
	if !errors.Is(safeError(context.Canceled), context.Canceled) {
		t.Fatal("lost cancellation")
	}
	if strings.Contains(safeError(errors.New("source-secret")).Error(), "source-secret") {
		t.Fatal("raw failure leaked")
	}
	if safeError(domain.ErrUnavailable) == nil {
		t.Fatal("lost ambiguous outcome")
	}
}
