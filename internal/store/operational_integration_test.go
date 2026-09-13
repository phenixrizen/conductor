package store

import (
	"github.com/phenixrizen/conductor/internal/domain"
	"testing"
)

func TestOperationalReadinessCountsWithoutSource(t *testing.T) {
	ctx, p, s, input, _ := runtimeFixture(t)
	actor := accessContext(ctx, "worker", "workspace-one", "repo-one")
	if _, err := s.CreateRuntimeEvidence(actor, "operational", input); err != nil {
		t.Fatal(err)
	}
	snapshot, err := p.OperationalSnapshot(ctx)
	if err != nil || !snapshot.Ready || len(snapshot.Queues) != 5 {
		t.Fatal(snapshot, err)
	}
	found := false
	for _, q := range snapshot.Queues {
		if q.Kind == "runtime" {
			found = q.Pending == 1 && q.Unresolved == 0 && q.OldestSeconds >= 0
		}
	}
	if !found {
		t.Fatal("pending collection absent from diagnostics")
	}
	// Diagnostics never become a permission-bearing context for the public read.
	if _, err := p.RuntimeEvidence(ctx, "unscoped"); err != domain.ErrForbidden {
		t.Fatal(err)
	}
	p.Close()
	if snapshot, err = p.OperationalSnapshot(ctx); err == nil || snapshot.Ready {
		t.Fatal("closed database reported ready")
	}
}
