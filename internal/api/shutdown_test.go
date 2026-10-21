package api

import (
	"context"
	"fmt"
	"testing"
)

// Every run's record is written before the server exits, however its
// shutdown budget stands (#128): the ends the stops hand the records are
// waited for, within a grace of the records' own, before the records close.
// Twenty runs stopped under a budget already spent all have their records,
// each stopped.
func TestShutdownWritesEveryRunsRecordWhenItsBudgetIsSpent(t *testing.T) {
	e := newTestEnv(t, nil)
	e.stopEverything(t)
	const n = 20
	for i := range n {
		e.launchCrew(t, fmt.Sprintf("Crew %d", i), catMember("core", "immediately"))
	}
	// A budget already spent: every stop is cut short at once.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.srv.Shutdown(ctx)
	got, err := e.srv.records.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("%d of %d records written", len(got), n)
	}
	for _, r := range got {
		if r.StoppedAt == nil {
			t.Fatalf("record %s is not stopped: %s", r.ID, r.State)
		}
	}
}
