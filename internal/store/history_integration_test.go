package store

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func TestPostgresHistoryPagingAndHistoricalApproval(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	s := service.New(p)
	created := integrationCreate(t, ctx, s)
	submitted, err := s.Submit(ctx, created.ID, "author", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, reviewer := range []string{"reviewer-a", "reviewer-b"} {
		if _, err := s.Approve(ctx, created.ID, reviewer, 1, created.Revision.Digest); err != nil {
			t.Fatal(err)
		}
	}
	// Another change introduces a global audit-sequence gap that must neither
	// leak into this change's history nor break its continuation cursor.
	other := integrationCreate(t, ctx, s)
	for revision := int64(1); revision < 3; revision++ {
		if _, err := s.Revise(ctx, created.ID, "author", revision, domain.Content{"intent": fmt.Sprintf("Synthetic revision %d", revision+1)}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.History(ctx, created.ID, 0, 2)
	if err != nil || len(first.Revisions) != 2 || first.Revisions[0].Number != 3 || first.Revisions[1].Number != 2 || first.NextBeforeRevision != 2 {
		t.Fatalf("first history page: %+v err=%v", first, err)
	}
	// A revision appended between requests must not shift the second page.
	latest, err := s.Revise(ctx, created.ID, "author", 3, domain.Content{"intent": "Synthetic revision 4"})
	if err != nil {
		t.Fatal(err)
	}
	last, err := s.History(ctx, created.ID, first.NextBeforeRevision, 2)
	if err != nil || len(last.Revisions) != 1 || last.Revisions[0].Number != 1 || last.NextBeforeRevision != 0 {
		t.Fatalf("last history page: %+v err=%v", last, err)
	}
	summary := last.Revisions[0]
	if summary.ApprovalCount != 2 || summary.Digest != created.Revision.Digest || summary.Author != "author" || !summary.CreatedAt.Equal(created.Revision.CreatedAt) || summary.SubmittedAt == nil {
		t.Fatalf("historical summary lost retained facts: %+v", summary)
	}
	empty, err := s.History(ctx, created.ID, 1, 2)
	if err != nil || empty.Revisions == nil || len(empty.Revisions) != 0 || empty.NextBeforeRevision != 0 {
		t.Fatalf("exhausted history: %+v err=%v", empty, err)
	}
	record, err := s.Revision(ctx, created.ID, 1)
	if err != nil || !reflect.DeepEqual(record.Revision, submitted.Revision) || len(record.Approvals) != 2 || record.ApprovalsTruncated {
		t.Fatalf("historical revision: %+v err=%v", record, err)
	}
	for i, approval := range record.Approvals {
		if approval.ChangeID != created.ID || approval.Revision != 1 || approval.Digest != created.Revision.Digest || approval.Reviewer != []string{"reviewer-a", "reviewer-b"}[i] {
			t.Fatalf("historical approval lost binding: %+v", approval)
		}
	}
	current, err := s.Get(ctx, created.ID)
	if err != nil || current.Approved || current.Approval != nil || !reflect.DeepEqual(current, latest) {
		t.Fatalf("historical inspection affected current approval: %+v err=%v", current, err)
	}
	if _, err := s.Approve(ctx, created.ID, "reviewer-c", 1, created.Revision.Digest); !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatalf("historical approval was allowed: %v", err)
	}
	currentRecord, err := s.Revision(ctx, created.ID, 4)
	if err != nil || currentRecord.Approvals == nil || len(currentRecord.Approvals) != 0 || currentRecord.ApprovalsTruncated {
		t.Fatalf("unapproved revision record: %+v err=%v", currentRecord, err)
	}
	otherHistory, err := s.History(ctx, other.ID, 0, 20)
	if err != nil || len(otherHistory.Revisions) != 1 || otherHistory.Revisions[0].ApprovalCount != 0 {
		t.Fatalf("unrelated history: %+v err=%v", otherHistory, err)
	}
	var events []domain.AuditEvent
	var cursor int64
	for {
		page, err := s.Events(ctx, created.ID, cursor, 2)
		if err != nil || len(page.Events) > 2 {
			t.Fatalf("audit page: %+v err=%v", page, err)
		}
		for _, event := range page.Events {
			if event.ChangeID != created.ID || event.Sequence <= cursor {
				t.Fatalf("audit cursor/isolation violation: %+v after=%d", event, cursor)
			}
			cursor = event.Sequence
			events = append(events, event)
		}
		if page.NextAfterSequence == 0 {
			break
		}
		if page.NextAfterSequence != cursor {
			t.Fatalf("audit continuation is not last returned sequence: %+v", page)
		}
	}
	wantKinds := []string{"package.created", "review.requested", "review.approved", "review.approved", "package.revised", "package.revised", "package.revised"}
	var kinds []string
	for _, event := range events {
		kinds = append(kinds, event.EventType)
		if event.EventType == "review.approved" && (event.Data["digest"] != created.Revision.Digest || event.Revision != 1) {
			t.Fatalf("approval audit lost exact revision/digest: %+v", event)
		}
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("audit history: got %v, want %v", kinds, wantKinds)
	}
	emptyEvents, err := s.Events(ctx, created.ID, cursor, 2)
	if err != nil || emptyEvents.Events == nil || len(emptyEvents.Events) != 0 || emptyEvents.NextAfterSequence != 0 {
		t.Fatalf("exhausted audit page: %+v err=%v", emptyEvents, err)
	}
}

func TestPostgresHistoryApprovalBound(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	s := service.New(p)
	created := integrationCreate(t, ctx, s)
	if _, err := s.Submit(ctx, created.ID, "author", 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < domain.MaxHistoricalApprovals; i++ {
		if _, err := s.Approve(ctx, created.ID, fmt.Sprintf("reviewer-%03d", i), 1, created.Revision.Digest); err != nil {
			t.Fatal(err)
		}
	}
	complete, err := s.Revision(ctx, created.ID, 1)
	if err != nil || len(complete.Approvals) != 100 || complete.ApprovalsTruncated {
		t.Fatalf("exact approval bound: count=%d truncated=%v err=%v", len(complete.Approvals), complete.ApprovalsTruncated, err)
	}
	if _, err := s.Approve(ctx, created.ID, "reviewer-100", 1, created.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	truncated, err := s.Revision(ctx, created.ID, 1)
	if err != nil || len(truncated.Approvals) != 100 || !truncated.ApprovalsTruncated || !reflect.DeepEqual(truncated.Approvals, complete.Approvals) {
		t.Fatalf("truncated approvals: count=%d truncated=%v err=%v", len(truncated.Approvals), truncated.ApprovalsTruncated, err)
	}
	history, err := s.History(ctx, created.ID, 0, 20)
	if err != nil || len(history.Revisions) != 1 || history.Revisions[0].ApprovalCount != 101 {
		t.Fatalf("total approvals hidden by detail bound: %+v err=%v", history, err)
	}
	page, err := s.Events(ctx, created.ID, 0, 100)
	if err != nil || len(page.Events) != 100 || page.NextAfterSequence != page.Events[99].Sequence {
		t.Fatalf("maximum audit page: count=%d next=%d err=%v", len(page.Events), page.NextAfterSequence, err)
	}
}

func TestPostgresHistoryMissingAndInvalidInputs(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	s := service.New(p)
	created := integrationCreate(t, ctx, s)
	for _, tc := range []struct {
		name string
		call func() error
		want error
	}{
		{"missing history", func() error { _, err := s.History(ctx, "missing", 0, 20); return err }, domain.ErrNotFound},
		{"missing events", func() error { _, err := s.Events(ctx, "missing", 0, 20); return err }, domain.ErrNotFound},
		{"missing package revision", func() error { _, err := s.Revision(ctx, "missing", 1); return err }, domain.ErrNotFound},
		{"missing revision", func() error { _, err := s.Revision(ctx, created.ID, 2); return err }, domain.ErrNotFound},
		{"invalid revision", func() error { _, err := s.Revision(ctx, created.ID, 0); return err }, domain.ErrInvalidInput},
		{"empty ID", func() error { _, err := s.History(ctx, "", 0, 20); return err }, domain.ErrInvalidInput},
		{"negative history cursor", func() error { _, err := s.History(ctx, created.ID, -1, 20); return err }, domain.ErrInvalidInput},
		{"negative audit cursor", func() error { _, err := s.Events(ctx, created.ID, -1, 20); return err }, domain.ErrInvalidInput},
		{"zero limit", func() error { _, err := s.History(ctx, created.ID, 0, 0); return err }, domain.ErrInvalidInput},
		{"excess history limit", func() error { _, err := s.History(ctx, created.ID, 0, 101); return err }, domain.ErrInvalidInput},
		{"excess audit limit", func() error { _, err := s.Events(ctx, created.ID, 0, 101); return err }, domain.ErrInvalidInput},
	} {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	if got := integrationCounts(t, ctx, p); got != [4]int{1, 1, 0, 1} {
		t.Fatalf("history reads mutated facts: %v", got)
	}
}
