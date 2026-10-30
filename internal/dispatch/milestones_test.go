package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// newMilestone makes a milestone of the project with the key through
// milestone.create, and returns its id.
func newMilestone(t *testing.T, s *Store, project, name string) string {
	t.Helper()
	rc, err := s.CreateMilestone(context.Background(), testOp("milestone-"+newID()), project, NewMilestone{Name: name})
	if err != nil || rc.Status != StatusDone {
		t.Fatalf("milestone %s: %+v %v", name, rc, err)
	}
	return rc.ID
}

func TestCreateEditAndDeleteAMilestone(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	newProject(t, s, "OTH")
	rc, err := s.CreateMilestone(ctx, testOp("m1"), "CND", NewMilestone{Name: "1.0", Description: "## The release\n\n- a", Target: "2026-12-01"})
	want(t, rc, err, StatusDone, "")
	id := rc.ID
	if len(id) != IDLen || rc.Rev != 1 || rc.Intent != "milestone.create" {
		t.Fatalf("the receipt: %+v", rc)
	}
	rows := changesOf(t, s, "test", "m1")
	var created milestoneCreated
	if len(rows) != 4 || rows[0].Op != OpCreate || rows[0].Entity != EntityMilestone || rows[0].EntityID != id || rows[0].ProjectID != pid ||
		json.Unmarshal([]byte(rows[0].Value), &created) != nil || created.ID != id || created.ProjectID != pid || created.CreatedAt.IsZero() {
		t.Fatalf("the rows: %+v", rows)
	}
	// The state is open by default, which the schema holds already: no
	// row for it.
	if got := rowsOf(t, s, "m1")[1:]; !slices.Equal(got, []string{"set:name=1.0<", "set:description=## The release\n\n- a<", "set:target=2026-12-01<"}) {
		t.Fatalf("the set rows: %v", got)
	}
	m, err := s.Milestone(ctx, "CND", id)
	if err != nil || m.Name != "1.0" || m.State != MilestoneOpen || m.Target != "2026-12-01" || m.Rev != 1 || m.UpdatedAt.IsZero() || !m.DeletedAt.IsZero() {
		t.Fatalf("the milestone: %+v %v", m, err)
	}
	// Names may repeat: a milestone of a repeated name is a person's to
	// tidy.
	rc, err = s.CreateMilestone(ctx, testOp("m2"), "CND", NewMilestone{Name: "1.0", State: MilestoneClosed})
	want(t, rc, err, StatusDone, "")
	if got := rowsOf(t, s, "m2")[1:]; !slices.Equal(got, []string{"set:name=1.0<", "set:state=closed<open"}) {
		t.Fatalf("a closed milestone's rows: %v", got)
	}
	// Every value is checked.
	for i, c := range []struct {
		m       NewMilestone
		refusal string
	}{
		{NewMilestone{Name: ""}, RefusalInvalid},
		{NewMilestone{Name: "a\nb"}, RefusalInvalid},
		{NewMilestone{Name: strings.Repeat("é", MaxMilestoneName+1)}, RefusalBound},
		{NewMilestone{Name: "x", Description: strings.Repeat("d", MaxMilestoneDescription+1)}, RefusalBound},
		{NewMilestone{Name: "x", Description: "a\x1bb"}, RefusalInvalid},
		{NewMilestone{Name: "x", Target: "2026-2-01"}, RefusalInvalid},
		{NewMilestone{Name: "x", Target: "2026-02-30"}, RefusalInvalid},
		{NewMilestone{Name: "x", Target: "2026-13-01"}, RefusalInvalid},
		{NewMilestone{Name: "x", Target: "tomorrow"}, RefusalInvalid},
		{NewMilestone{Name: "x", Target: "2026-12-01T00:00:00Z"}, RefusalInvalid},
		{NewMilestone{Name: "x", State: "done"}, RefusalInvalid},
	} {
		rc, err := s.CreateMilestone(ctx, testOp(opID("bad", i)), "CND", c.m)
		want(t, rc, err, StatusFailed, c.refusal)
	}
	rc, err = s.CreateMilestone(ctx, testOp("none"), "NOPE", NewMilestone{Name: "x"})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	rc, err = s.CreateMilestone(ctx, testOp("bound"), "CND", NewMilestone{Name: strings.Repeat("é", MaxMilestoneName), Description: strings.Repeat("d", MaxMilestoneDescription),
		Target: "2028-02-29"})
	want(t, rc, err, StatusDone, "")
	// Edit: each field given, one row each with its prior; an edit of
	// nothing refused; the revision expected is the milestone's.
	stale := testOp("e0")
	stale.ExpectedRev = 2
	rc, err = s.EditMilestone(ctx, stale, "CND", id, MilestoneEdit{State: ptr(MilestoneClosed)})
	want(t, rc, err, StatusFailed, RefusalStaleRevision)
	edit := testOp("e1")
	edit.ExpectedRev = 1
	rc, err = s.EditMilestone(ctx, edit, "CND", id, MilestoneEdit{Name: ptr("1.0.0"), Target: ptr(""), State: ptr(MilestoneClosed)})
	want(t, rc, err, StatusDone, "")
	if rc.Rev != 2 {
		t.Fatalf("the edit's receipt: %+v", rc)
	}
	if got := rowsOf(t, s, "e1"); !slices.Equal(got, []string{"set:name=1.0.0<1.0", "set:target=<2026-12-01", "set:state=closed<open"}) {
		t.Fatalf("the edit's rows: %v", got)
	}
	rc, err = s.EditMilestone(ctx, testOp("e2"), "CND", id, MilestoneEdit{State: ptr(MilestoneClosed)})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = s.EditMilestone(ctx, testOp("e3"), "CND", id, MilestoneEdit{Target: ptr("2026-11-31")})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	// The read: the live ones by target, none last, then by name.
	rc, err = s.CreateMilestone(ctx, testOp("m3"), "CND", NewMilestone{Name: "0.9", Target: "2026-11-01"})
	want(t, rc, err, StatusDone, "")
	ms, err := s.Milestones(ctx, "CND")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, m := range ms {
		order = append(order, m.Target+"/"+m.Name)
	}
	if want := []string{"2026-11-01/0.9", "2028-02-29/" + strings.Repeat("é", MaxMilestoneName), "/1.0", "/1.0.0"}; !slices.Equal(order, want) {
		t.Fatalf("the milestones: %v, want %v", order, want)
	}
	if _, err := s.Milestones(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the milestones of no project: %v", err)
	}
	// Another project's, or none, is not_found through this one.
	oth := newMilestone(t, s, "OTH", "2.0")
	rc, err = s.EditMilestone(ctx, testOp("x1"), "CND", oth, MilestoneEdit{Name: ptr("mine")})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	rc, err = s.DeleteMilestone(ctx, testOp("x2"), "CND", oth)
	want(t, rc, err, StatusFailed, RefusalNotFound)
	if _, err := s.Milestone(ctx, "CND", oth); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another project's milestone read through this one: %v", err)
	}
	// An archived project refuses all three.
	if rc, err := s.ArchiveProject(ctx, testOp("archive"), "CND"); err != nil || rc.Status != StatusDone {
		t.Fatalf("archive: %+v %v", rc, err)
	}
	rc, err = s.CreateMilestone(ctx, testOp("a1"), "CND", NewMilestone{Name: "x"})
	want(t, rc, err, StatusFailed, RefusalArchived)
	rc, err = s.EditMilestone(ctx, testOp("a2"), "CND", id, MilestoneEdit{Name: ptr("x")})
	want(t, rc, err, StatusFailed, RefusalArchived)
	rc, err = s.DeleteMilestone(ctx, testOp("a3"), "CND", id)
	want(t, rc, err, StatusFailed, RefusalArchived)
}

// A project holds at most MaxMilestones live milestones; a deleted one
// counts for nothing.
func TestAProjectHoldsAtMostItsBoundOfLiveMilestones(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	// All but the last written by hand in one transaction, and a
	// tombstone, which counts for nothing: the creates at the bound are
	// what is tested.
	rows, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range MaxMilestones {
		deleted, now := "", stamp(time.Now())
		if i == MaxMilestones-1 {
			deleted = now
		}
		if _, err := rows.Exec("INSERT INTO milestones (id, project_id, name, created_at, updated_at, deleted_at, rev) VALUES (?, ?, ?, ?, ?, ?, 1)",
			newID(), pid, fmt.Sprintf("m%d", i), now, now, deleted); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Commit(); err != nil {
		t.Fatal(err)
	}
	first := newMilestone(t, s, "CND", "the last")
	rc, err := s.CreateMilestone(ctx, testOp("past"), "CND", NewMilestone{Name: "one too many"})
	want(t, rc, err, StatusFailed, RefusalBound)
	rc, err = s.DeleteMilestone(ctx, testOp("d1"), "CND", first)
	want(t, rc, err, StatusDone, "")
	rc, err = s.CreateMilestone(ctx, testOp("again"), "CND", NewMilestone{Name: "one too many"})
	want(t, rc, err, StatusDone, "")
}

// A milestone deleted clears milestone_id on every ticket naming it, a
// deleted one included, one set row each with the milestone's id as its
// prior; the milestone is a tombstone, its row and history kept.
func TestADeletedMilestoneLeavesEveryTicket(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	id := newMilestone(t, s, "CND", "1.0")
	keep := newMilestone(t, s, "CND", "2.0")
	t1, k1 := makeTicket(t, s, NewTicket{Project: "CND", Title: "One", Milestone: id})
	_, k2 := makeTicket(t, s, NewTicket{Project: "CND", Title: "Two", Milestone: keep})
	t3, k3 := makeTicket(t, s, NewTicket{Project: "CND", Title: "Three, deleted", Milestone: id})
	rc, err := s.DeleteTicket(ctx, testOp("del3"), k3)
	want(t, rc, err, StatusDone, "")
	revs := map[string]int64{k1: ticketOf(t, s, k1).Rev, k2: ticketOf(t, s, k2).Rev, k3: ticketOf(t, s, k3).Rev}
	calls := listen(s)
	rc, err = s.DeleteMilestone(ctx, testOp("d1"), "CND", id)
	want(t, rc, err, StatusDone, "")
	if rc.Rev != 2 {
		t.Fatalf("the receipt: %+v", rc)
	}
	rows := changesOf(t, s, "test", "d1")
	if len(rows) != 3 || rows[0].Entity != EntityMilestone || rows[0].EntityID != id || rows[0].Field != "deleted_at" || rows[0].Prior != "" {
		t.Fatalf("the delete's rows: %+v", rows)
	}
	for i, tid := range []string{t1, t3} {
		r := rows[i+1]
		if r.Entity != EntityTicket || r.EntityID != tid || r.ProjectID != pid || r.Field != "milestone_id" || r.Op != OpSet || r.Value != "" || r.Prior != id {
			t.Fatalf("ticket %d's row: %+v", i+1, r)
		}
	}
	// One call, one change per entity: the milestone, then each ticket it
	// left.
	if got := calls.one(t); len(got) != 3 || got[0].Entity != EntityMilestone || got[1].ID != t1 || got[2].ID != t3 {
		t.Fatalf("OnChange: %+v", got)
	}
	if tk := ticketOf(t, s, k1); tk.MilestoneID != "" || tk.Rev != revs[k1]+1 {
		t.Fatalf("%s after the delete: %+v", k1, tk)
	}
	if tk := ticketOf(t, s, k3); tk.MilestoneID != "" || tk.Rev != revs[k3]+1 {
		t.Fatalf("%s after the delete: %+v", k3, tk)
	}
	if tk := ticketOf(t, s, k2); tk.MilestoneID != keep || tk.Rev != revs[k2] {
		t.Fatalf("%s after the delete: %+v", k2, tk)
	}
	m, err := s.Milestone(ctx, "CND", id)
	if err != nil || m.DeletedAt.IsZero() || m.Name != "1.0" {
		t.Fatalf("the tombstone: %+v %v", m, err)
	}
	if ms, err := s.Milestones(ctx, "CND"); err != nil || len(ms) != 1 || ms[0].ID != keep {
		t.Fatalf("the milestones: %+v %v", ms, err)
	}
	// Nothing more is written on it, and nothing may name it.
	rc, err = s.EditMilestone(ctx, testOp("w1"), "CND", id, MilestoneEdit{Name: ptr("x")})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	rc, err = s.DeleteMilestone(ctx, testOp("w2"), "CND", id)
	want(t, rc, err, StatusFailed, RefusalNotFound)
	rc, err = edit(s, "w3", k2, TicketEdit{Set: TicketSet{Milestone: ptr(id)}})
	want(t, rc, err, StatusFailed, RefusalNotFound)
}

// The write lock orders a milestone's delete against a ticket's set of
// it, whichever comes first: deleted then set, the set finds a tombstone
// (not_found); set then deleted, the delete clears it with one changes
// row for the ticket.
func TestAMilestonesDeleteAndASetAreOrderedByTheWriteLock(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	first := newMilestone(t, s, "CND", "first")
	tid, key := newTicket(t, s, "CND", "Planned")
	rc, err := s.DeleteMilestone(ctx, testOp("d1"), "CND", first)
	want(t, rc, err, StatusDone, "")
	rc, err = edit(s, "s1", key, TicketEdit{Set: TicketSet{Milestone: ptr(first)}})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	if got := ticketOf(t, s, key).MilestoneID; got != "" {
		t.Fatalf("the milestone after a refused set: %q", got)
	}
	second := newMilestone(t, s, "CND", "second")
	rc, err = edit(s, "s2", key, TicketEdit{Set: TicketSet{Milestone: ptr(second)}})
	want(t, rc, err, StatusDone, "")
	rc, err = s.DeleteMilestone(ctx, testOp("d2"), "CND", second)
	want(t, rc, err, StatusDone, "")
	if got := ticketOf(t, s, key).MilestoneID; got != "" {
		t.Fatalf("the milestone after the delete: %q", got)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM changes WHERE op_id = 'd2' AND entity = 'ticket' AND entity_id = ?", tid); n != 1 {
		t.Fatalf("%d changes rows for the ticket", n)
	}
}

// A milestone of another project is invalid on a ticket, a deleted one of
// its own not_found, made and deleted by the intents.
func TestAMilestoneOfAnotherProjectIsInvalidAndADeletedOneNotFound(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	newProject(t, s, "OTH")
	_, key := newTicket(t, s, "CND", "Planned")
	other := newMilestone(t, s, "OTH", "1.0")
	gone := newMilestone(t, s, "CND", "gone")
	rc, err := s.DeleteMilestone(ctx, testOp("d1"), "CND", gone)
	want(t, rc, err, StatusDone, "")
	for id, refusal := range map[string]string{other: RefusalInvalid, gone: RefusalNotFound} {
		rc, err := s.CreateTicket(ctx, testOp("c-"+id), NewTicket{Project: "CND", Title: "x", Milestone: id})
		want(t, rc, err, StatusFailed, refusal)
		rc, err = edit(s, "e-"+id, key, TicketEdit{Set: TicketSet{Milestone: ptr(id)}})
		want(t, rc, err, StatusFailed, refusal)
	}
}
