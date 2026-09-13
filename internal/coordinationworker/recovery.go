package coordinationworker

import (
	"context"

	"github.com/phenixrizen/conductor/internal/coordinationworkflow"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/store"
)

type RecoveryStore interface {
	CoordinationRecoveryTargets(context.Context) ([]store.CoordinationRecoveryTarget, error)
	ConfirmCoordinationCleanup(context.Context, string, string, string, string) error
}

// Recover reconciles resources after an observed terminal or unknown Temporal
// run. It cannot restart a producer. A lost attempt stays unresolved even after
// cleanup, while tasks that never began acquire factual cancellation receipts.
func (a *Activity) Recover(ctx context.Context) error {
	db, ok := a.store.(RecoveryStore)
	if !ok {
		return nil
	}
	targets, err := db.CoordinationRecoveryTargets(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		w, err := a.store.CoordinationWork(ctx, target.ID, target.Binding)
		if err != nil {
			return err
		}
		for _, t := range w.Run.Plan.Tasks {
			ref := coordinationworkflow.TaskReference{Run: coordinationworkflow.Reference{ID: target.ID, Binding: target.Binding}, TaskID: w.TaskIDs[t.ID]}
			attempt, err := a.store.CoordinationAttempt(ctx, target.ID, target.Binding, ref.TaskID)
			if err != nil {
				return err
			}
			_, hasReceipt := committed(w, ref.TaskID)
			if attempt == nil {
				if !hasReceipt {
					if _, err = a.stop(ref, "cancelled", "", true); err != nil {
						return err
					}
				}
				continue
			}
			if !attempt.RecoveryReady {
				continue
			}
			clean, err := (execution.Runner{DockerBinary: a.docker}).CleanupAttempt(ctx, attempt.InputDigest)
			if err != nil {
				return err
			}
			if !clean {
				return execution.ErrUnavailable
			}
			if !hasReceipt {
				if _, err = a.stop(ref, "unresolved", attempt.InputDigest, true); err != nil {
					return err
				}
			}
			if err = db.ConfirmCoordinationCleanup(ctx, target.ID, target.Binding, ref.TaskID, attempt.InputDigest); err != nil {
				return err
			}
		}
		// A cancelled workflow may not call Finalize. This aggregate only records
		// existing receipts and cannot change Temporal's observed terminal outcome.
		latest, err := a.store.CoordinationWork(ctx, target.ID, target.Binding)
		if err != nil {
			return err
		}
		if len(latest.Run.Receipts) == len(latest.Run.Plan.Tasks) {
			if _, err = a.store.FinalizeCoordination(ctx, target.ID, target.Binding); err != nil {
				return err
			}
		}
	}
	return nil
}
