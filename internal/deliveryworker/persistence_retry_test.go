package deliveryworker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

type lostPublicationAckDB struct {
	*activityDB
	lost bool
}

func (d *lostPublicationAckDB) CompleteDeliveryOperation(ctx context.Context, op, binding string, o domain.DeliveryObservation) (string, error) {
	digest, err := d.activityDB.CompleteDeliveryOperation(ctx, op, binding, o)
	if err != nil {
		return "", err
	}
	if !d.lost {
		d.lost = true
		return "", io.ErrUnexpectedEOF
	}
	return digest, nil
}
func TestPublicationWorkflowRecoversCommittedDatabaseAcknowledgment(t *testing.T) {
	bundle, patch := activityGit(t)
	ref := contextworkflow.Reference{ID: strings.Repeat("a", 32), Binding: strings.Repeat("b", 32)}
	db := &lostPublicationAckDB{activityDB: &activityDB{work: store.DeliveryWork{Binding: ref.Binding, Patch: patch, Delivery: domain.Delivery{ID: strings.Repeat("c", 32), ResultTree: patch.ResultTree}}}}
	sourceCalls, credentials := 0, 0
	a, err := NewActivity(db, func(context.Context, string, string) ([]byte, error) { sourceCalls++; return bundle, nil }, func(context.Context, domain.DeliveryTarget, string) (string, error) {
		credentials++
		return "synthetic", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &activityPublisher{t: t}
	a.provider = func(domain.DeliveryTarget, string) (Publisher, error) { return provider, nil }
	calls := 0
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(protect(func(ctx context.Context, r contextworkflow.Reference) (contextworkflow.Result, error) {
		calls++
		return a.Publish(ctx, r)
	}), activity.RegisterOptions{Name: ActivityName})
	env.ExecuteWorkflow(Publish, ref)
	if err = env.GetWorkflowError(); err != nil {
		t.Fatal("committed receipt was not recovered", err)
	}
	var result contextworkflow.Result
	if err = env.GetWorkflowResult(&result); err != nil || result.Digest != db.receipt || result.ReceiptID != ref.ID {
		t.Fatal("wrong retained receipt", err)
	}
	if calls != 2 || db.commits != 1 || provider.calls != 1 || credentials != 1 || sourceCalls != 1 {
		t.Fatalf("receipt recovery repeated publication: attempts=%d commits=%d calls=%d credentials=%d source=%d", calls, db.commits, provider.calls, credentials, sourceCalls)
	}
}

func TestPublicationPersistenceAuthorityFailuresStayPermanent(t *testing.T) {
	for _, err := range []error{domain.ErrForbidden, domain.ErrUnauthenticated, domain.ErrConflict, domain.ErrStaleApproval, domain.ErrNotFound, domain.ErrInvalidInput, domain.ErrUnavailable, domain.ErrCollectionStopped, context.Canceled} {
		classified := publicationStoreError(err)
		if !errors.Is(classified, err) {
			t.Fatal("persistence classifier changed authority failure", err)
		}
		safe := safeError(classified)
		if err == context.Canceled {
			if !errors.Is(safe, context.Canceled) {
				t.Fatal("lost cancellation")
			}
			continue
		}
		var application *temporal.ApplicationError
		if !errors.As(safe, &application) || !application.NonRetryable() {
			t.Fatal("authority failure became retryable", err, safe)
		}
	}
	private := errors.New("synthetic-private-database-detail")
	if strings.Contains(safeError(publicationStoreError(private)).Error(), private.Error()) {
		t.Fatal("persistence error entered workflow history")
	}
}
