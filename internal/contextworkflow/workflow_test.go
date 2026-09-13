package contextworkflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

var testReference = Reference{ID: strings.Repeat("1", 32), Binding: strings.Repeat("2", 32)}
var testResult = Result{ReceiptID: testReference.ID, Digest: strings.Repeat("a", 64)}

func TestActivityFailureBoundary(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		code       string
		retryable  bool
		retryAfter time.Duration
	}{
		{"raw", errors.New("source-token-canary"), "internal", false, 0},
		{"wrapped raw", fmt.Errorf("db: %w", errors.New("source-token-canary")), "internal", false, 0},
		{"unknown", Failure{Code: "source-token-canary", Retryable: true}, "internal", false, 0},
		{"transient", Failure{Code: "unavailable", Retryable: true}, "unavailable", true, 0},
		{"database", &Failure{Code: "database_unavailable", Retryable: true}, "database_unavailable", true, 0},
		{"rate limited", Failure{Code: "rate_limited", Retryable: true, RetryAfter: 8 * time.Second}, "rate_limited", true, 8 * time.Second},
		{"long delay", Failure{Code: "rate_limited", Retryable: true, RetryAfter: time.Minute}, "rate_limited", false, 0},
		{"revocation", Failure{Code: "permission_revoked", Retryable: true}, "permission_revoked", false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := safeFailure(test.err)
			var app *temporal.ApplicationError
			if !errors.As(err, &app) || app.Type() != "collection."+test.code || app.NonRetryable() == test.retryable || app.NextRetryDelay() != test.retryAfter {
				t.Fatalf("unexpected classification: %#v", err)
			}
			if app.Unwrap() != nil || app.HasDetails() || strings.Contains(err.Error(), "canary") {
				t.Fatal("failure exposed private diagnostics")
			}
		})
	}
	if !temporal.IsCanceledError(safeFailure(fmt.Errorf("hidden: %w", context.Canceled))) {
		t.Fatal("cancellation lost its Temporal type")
	}
	if !temporal.IsCanceledError(safeFailure(Failure{Code: "cancelled"})) {
		t.Fatal("durable cancellation intent lost its Temporal type")
	}
}

func TestWorkflowRetriesAndBounds(t *testing.T) {
	for _, test := range []struct {
		name     string
		failure  error
		succeed  bool
		attempts int32
	}{
		{"transient recovery", Failure{Code: "database_unavailable", Retryable: true}, true, 2},
		{"transient exhausted", Failure{Code: "unavailable", Retryable: true}, false, 3},
		{"revoked", Failure{Code: "permission_revoked"}, false, 1},
		{"raw error", errors.New("source-token-canary"), false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			var attempts atomic.Int32
			env.RegisterActivityWithOptions(protectedActivity(func(context.Context, Reference) (Result, error) {
				attempt := attempts.Add(1)
				if test.succeed && attempt > 1 {
					return testResult, nil
				}
				return Result{}, test.failure
			}), activity.RegisterOptions{Name: ActivityName})
			env.ExecuteWorkflow(collectionWorkflow, testReference)
			if !env.IsWorkflowCompleted() || attempts.Load() != test.attempts {
				t.Fatalf("completed=%v attempts=%d", env.IsWorkflowCompleted(), attempts.Load())
			}
			if test.succeed {
				var got Result
				if err := env.GetWorkflowResult(&got); err != nil || got != testResult {
					t.Fatalf("result=%#v err=%v", got, err)
				}
			} else if err := env.GetWorkflowError(); err == nil || strings.Contains(err.Error(), "canary") {
				t.Fatalf("unsafe or missing failure: %v", err)
			}
		})
	}
}

func TestActivityRejectsUnsafeOutputAndPanic(t *testing.T) {
	for _, collect := range []ActivityFunc{
		func(context.Context, Reference) (Result, error) { panic("source-token-canary") },
		func(context.Context, Reference) (Result, error) { return Result{ReceiptID: "source-token-canary"}, nil },
	} {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestActivityEnvironment()
		env.RegisterActivity(protectedActivity(collect))
		_, err := env.ExecuteActivity(protectedActivity(collect), testReference)
		if err == nil || strings.Contains(err.Error(), "canary") {
			t.Fatalf("unsafe or missing failure: %v", err)
		}
	}
}

func TestInvalidReferenceNeverCallsActivity(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(collectionWorkflow, Reference{ID: "source-token-canary"})
	if err := env.GetWorkflowError(); err == nil || strings.Contains(err.Error(), "canary") {
		t.Fatalf("unsafe or missing failure: %v", err)
	}
}
