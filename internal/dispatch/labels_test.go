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

// newLabel makes a label of the project with the key through
// label.create, and returns its id.
func newLabel(t *testing.T, s *Store, project, name string) string {
	t.Helper()
	rc, err := s.CreateLabel(context.Background(), testOp("label-"+newID()), project, NewLabel{Name: name, Color: "blue"})
	if err != nil || rc.Status != StatusDone {
		t.Fatalf("label %s: %+v %v", name, rc, err)
	}
	return rc.ID
}

func TestCreateEditAndDeleteALabel(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	newProject(t, s, "OTH")
	rc, err := s.CreateLabel(ctx, testOp("l1"), "CND", NewLabel{Name: "api", Color: "teal"})
	want(t, rc, err, StatusDone, "")
	id := rc.ID
	if len(id) != IDLen || rc.Rev != 1 || rc.Intent != "label.create" {
		t.Fatalf("the receipt: %+v", rc)
	}
	rows := changesOf(t, s, "test", "l1")
	if len(rows) != 3 || rows[0].Op != OpCreate || rows[0].Entity != EntityLabel || rows[0].EntityID != id || rows[0].ProjectID != pid || rows[0].TicketID != "" {
		t.Fatalf("the rows: %+v", rows)
	}
	var created labelCreated
	if err := json.Unmarshal([]byte(rows[0].Value), &created); err != nil || created.ID != id || created.ProjectID != pid || created.CreatedAt.IsZero() {
		t.Fatalf("the create row's value: %s %v", rows[0].Value, err)
	}
	if got := rowsOf(t, s, "l1")[1:]; !slices.Equal(got, []string{"set:name=api<", "set:color=teal<"}) {
		t.Fatalf("the set rows: %v", got)
	}
	lb, err := s.Label(ctx, "CND", id)
	if err != nil || lb.Name != "api" || lb.Color != "teal" || lb.ProjectID != pid || lb.Rev != 1 || lb.CreatedAt.IsZero() || !lb.DeletedAt.IsZero() {
		t.Fatalf("the label: %+v %v", lb, err)
	}
	// The name is the project's live labels' alone, whatever its case;
	// another project may hold it.
	for i, name := range []string{"api", "API", "Api"} {
		rc, err := s.CreateLabel(ctx, testOp(opID("dup", i)), "CND", NewLabel{Name: name, Color: "red"})
		want(t, rc, err, StatusFailed, RefusalInvalid)
	}
	rc, err = s.CreateLabel(ctx, testOp("oth"), "OTH", NewLabel{Name: "api", Color: "red"})
	want(t, rc, err, StatusDone, "")
	// Every value is checked: the name's rule, a colour of the twelve.
	for i, c := range []struct {
		l       NewLabel
		refusal string
	}{
		{NewLabel{Name: "", Color: "red"}, RefusalInvalid},
		{NewLabel{Name: "a\tb", Color: "red"}, RefusalInvalid},
		{NewLabel{Name: strings.Repeat("é", MaxLabelName+1), Color: "red"}, RefusalBound},
		{NewLabel{Name: "ui", Color: ""}, RefusalInvalid},
		{NewLabel{Name: "ui", Color: "#ff0000"}, RefusalInvalid},
		{NewLabel{Name: "ui", Color: "purple"}, RefusalInvalid},
		{NewLabel{Name: "ui", Color: "Red"}, RefusalInvalid},
	} {
		rc, err := s.CreateLabel(ctx, testOp(opID("bad", i)), "CND", c.l)
		want(t, rc, err, StatusFailed, c.refusal)
	}
	rc, err = s.CreateLabel(ctx, testOp("none"), "NOPE", NewLabel{Name: "ui", Color: "red"})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	for i, color := range LabelColors {
		rc, err := s.CreateLabel(ctx, testOp(opID("color", i)), "CND", NewLabel{Name: "c-" + color, Color: color})
		want(t, rc, err, StatusDone, "")
	}
	rc, err = s.CreateLabel(ctx, testOp("long"), "CND", NewLabel{Name: strings.Repeat("é", MaxLabelName), Color: "gray"})
	want(t, rc, err, StatusDone, "")
	// Edit: a rename and a recolour, one row each with its prior; a rename
	// to another live label's name refused; to its own name in another
	// case allowed; an edit of nothing refused.
	stale := testOp("e0")
	stale.ExpectedRev = 2
	rc, err = s.EditLabel(ctx, stale, "CND", id, LabelEdit{Name: ptr("backend")})
	want(t, rc, err, StatusFailed, RefusalStaleRevision)
	edit := testOp("e1")
	edit.ExpectedRev = 1
	rc, err = s.EditLabel(ctx, edit, "CND", id, LabelEdit{Name: ptr("backend"), Color: ptr("indigo")})
	want(t, rc, err, StatusDone, "")
	if rc.Rev != 2 {
		t.Fatalf("the edit's receipt: %+v", rc)
	}
	if got := rowsOf(t, s, "e1"); !slices.Equal(got, []string{"set:name=backend<api", "set:color=indigo<teal"}) {
		t.Fatalf("the edit's rows: %v", got)
	}
	rc, err = s.EditLabel(ctx, testOp("e2"), "CND", id, LabelEdit{Name: ptr("c-red")})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = s.EditLabel(ctx, testOp("e3"), "CND", id, LabelEdit{Name: ptr("Backend")})
	want(t, rc, err, StatusDone, "")
	rc, err = s.EditLabel(ctx, testOp("e4"), "CND", id, LabelEdit{Color: ptr("indigo")})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = s.EditLabel(ctx, testOp("e5"), "CND", id, LabelEdit{Color: ptr("magenta")})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	// The old name is free once renamed.
	rc, err = s.CreateLabel(ctx, testOp("l2"), "CND", NewLabel{Name: "api", Color: "red"})
	want(t, rc, err, StatusDone, "")
	// A label of another project through this one, or none, is not_found.
	oth, err := s.Labels(ctx, "OTH")
	if err != nil || len(oth) != 1 {
		t.Fatalf("OTH's labels: %+v %v", oth, err)
	}
	rc, err = s.EditLabel(ctx, testOp("x1"), "CND", oth[0].ID, LabelEdit{Name: ptr("mine")})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	rc, err = s.DeleteLabel(ctx, testOp("x2"), "CND", oth[0].ID)
	want(t, rc, err, StatusFailed, RefusalNotFound)
	rc, err = s.DeleteLabel(ctx, testOp("x3"), "CND", "0000000000000000")
	want(t, rc, err, StatusFailed, RefusalNotFound)
	if _, err := s.Label(ctx, "CND", oth[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another project's label read through this one: %v", err)
	}
	// The read: the live labels by name.
	labels, err := s.Labels(ctx, "CND")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range labels {
		names = append(names, l.Name)
	}
	if !slices.IsSorted(names) || len(names) != 2+len(LabelColors)+1 || !slices.Contains(names, "Backend") || !slices.Contains(names, "api") {
		t.Fatalf("the labels: %v", names)
	}
	if _, err := s.Labels(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the labels of no project: %v", err)
	}
	// An archived project refuses all three.
	if rc, err := s.ArchiveProject(ctx, testOp("archive"), "CND"); err != nil || rc.Status != StatusDone {
		t.Fatalf("archive: %+v %v", rc, err)
	}
	rc, err = s.CreateLabel(ctx, testOp("a1"), "CND", NewLabel{Name: "new", Color: "red"})
	want(t, rc, err, StatusFailed, RefusalArchived)
	rc, err = s.EditLabel(ctx, testOp("a2"), "CND", id, LabelEdit{Name: ptr("new")})
	want(t, rc, err, StatusFailed, RefusalArchived)
	rc, err = s.DeleteLabel(ctx, testOp("a3"), "CND", id)
	want(t, rc, err, StatusFailed, RefusalArchived)
}

// A project holds at most MaxLabels live labels; a deleted one counts for
// nothing.
func TestAProjectHoldsAtMostItsBoundOfLiveLabels(t *testing.T) {
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
	for i := range MaxLabels {
		deleted := ""
		if i == MaxLabels-1 {
			deleted = stamp(time.Now())
		}
		if _, err := rows.Exec("INSERT INTO labels (id, project_id, name, color, created_at, deleted_at, rev) VALUES (?, ?, ?, 'gray', ?, ?, 1)",
			newID(), pid, fmt.Sprintf("l%d", i), stamp(time.Now()), deleted); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Commit(); err != nil {
		t.Fatal(err)
	}
	first := newLabel(t, s, "CND", "the last")
	rc, err := s.CreateLabel(ctx, testOp("past"), "CND", NewLabel{Name: "one too many", Color: "red"})
	want(t, rc, err, StatusFailed, RefusalBound)
	rc, err = s.DeleteLabel(ctx, testOp("d1"), "CND", first)
	want(t, rc, err, StatusDone, "")
	rc, err = s.CreateLabel(ctx, testOp("again"), "CND", NewLabel{Name: "one too many", Color: "red"})
	want(t, rc, err, StatusDone, "")
}

// A label deleted leaves every ticket that has it, a deleted one
// included, with one changes row each naming the tags of its adds, and
// its name is free for a new label; the label itself is a tombstone, its
// row and history kept, and nothing more is written on it.
func TestADeletedLabelLeavesEveryTicketAndFreesItsName(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	id := newLabel(t, s, "CND", "api")
	keep := newLabel(t, s, "CND", "ui")
	t1, k1 := makeTicket(t, s, NewTicket{Project: "CND", Title: "One", Labels: []string{id, keep}})
	t2, k2 := makeTicket(t, s, NewTicket{Project: "CND", Title: "Two", Labels: []string{id}})
	t3, k3 := newTicket(t, s, "CND", "Three")
	rc, err := edit(s, "add3", k3, TicketEdit{Add: TicketMembers{Labels: []string{id}}})
	want(t, rc, err, StatusDone, "")
	_, k4 := newTicket(t, s, "CND", "Four, without it")
	t5, k5 := makeTicket(t, s, NewTicket{Project: "CND", Title: "Five, deleted", Labels: []string{id}})
	rc, err = s.DeleteTicket(ctx, testOp("del5"), k5)
	want(t, rc, err, StatusDone, "")
	tagOf := func(ticketID string) Tag {
		t.Helper()
		var tag Tag
		if err := s.db.QueryRow("SELECT actor, op_id, n FROM changes WHERE entity_id = ? AND field = 'labels' AND op = 'add' AND value = ?", ticketID, id).Scan(&tag.Actor, &tag.OpID, &tag.N); err != nil {
			t.Fatal(err)
		}
		return tag
	}
	tags := map[string]Tag{t1: tagOf(t1), t2: tagOf(t2), t3: tagOf(t3), t5: tagOf(t5)}
	revs := map[string]int64{}
	for _, k := range []string{k1, k2, k3, k4, k5} {
		revs[k] = ticketOf(t, s, k).Rev
	}
	calls := listen(s)
	rc, err = s.DeleteLabel(ctx, testOp("d1"), "CND", id)
	want(t, rc, err, StatusDone, "")
	if rc.Rev != 2 {
		t.Fatalf("the receipt: %+v", rc)
	}
	rows := changesOf(t, s, "test", "d1")
	if len(rows) != 5 || rows[0].Entity != EntityLabel || rows[0].EntityID != id || rows[0].Field != "deleted_at" || rows[0].Op != OpSet || rows[0].Prior != "" {
		t.Fatalf("the delete's rows: %+v", rows)
	}
	for i, tid := range []string{t1, t2, t3, t5} {
		r := rows[i+1]
		if r.Entity != EntityTicket || r.EntityID != tid || r.TicketID != tid || r.ProjectID != pid || r.Field != "labels" || r.Op != OpRemove || r.Value != id ||
			r.Prior != EncodeTags([]Tag{tags[tid]}) {
			t.Fatalf("the remove row of ticket %d: %+v", i+1, r)
		}
	}
	// One call, one change per entity: the label, then each ticket it
	// left.
	got := calls.one(t)
	if len(got) != 5 || got[0].Entity != EntityLabel || got[0].ID != id || got[0].Project != "CND" {
		t.Fatalf("OnChange: %+v", got)
	}
	for i, tid := range []string{t1, t2, t3, t5} {
		if got[i+1] != (Change{Seq: got[0].Seq, Entity: EntityTicket, ID: tid, Ticket: tid, Project: "CND"}) {
			t.Fatalf("OnChange %d: %+v", i+1, got[i+1])
		}
	}
	// Each ticket it left has its rev bumped once; the one without it not.
	for _, k := range []string{k1, k2, k3, k5} {
		if rev := ticketOf(t, s, k).Rev; rev != revs[k]+1 {
			t.Fatalf("%s's rev %d, before %d", k, rev, revs[k])
		}
	}
	if rev := ticketOf(t, s, k4).Rev; rev != revs[k4] {
		t.Fatalf("%s's rev moved", k4)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM ticket_labels WHERE label_id = ?", id); n != 0 {
		t.Fatalf("%d memberships left", n)
	}
	if got := ticketOf(t, s, k1).Labels; !slices.Equal(got, []string{keep}) {
		t.Fatalf("%s's labels: %v", k1, got)
	}
	// The tombstone: read with its row, out of the list, and not_found to
	// every write and reference.
	lb, err := s.Label(ctx, "CND", id)
	if err != nil || lb.DeletedAt.IsZero() || lb.Name != "api" || lb.Rev != 2 {
		t.Fatalf("the tombstone: %+v %v", lb, err)
	}
	if labels, err := s.Labels(ctx, "CND"); err != nil || len(labels) != 1 || labels[0].ID != keep {
		t.Fatalf("the labels: %+v %v", labels, err)
	}
	for i, f := range []func() (Receipt, error){
		func() (Receipt, error) { return s.EditLabel(ctx, testOp("w1"), "CND", id, LabelEdit{Name: ptr("x")}) },
		func() (Receipt, error) { return s.DeleteLabel(ctx, testOp("w2"), "CND", id) },
		func() (Receipt, error) {
			return edit(s, "w3", k4, TicketEdit{Add: TicketMembers{Labels: []string{id}}})
		},
		func() (Receipt, error) {
			return s.CreateTicket(ctx, testOp("w4"), NewTicket{Project: "CND", Title: "x", Labels: []string{id}})
		},
	} {
		rc, err := f()
		if err != nil || rc.Refusal != RefusalNotFound {
			t.Fatalf("write %d on the tombstone: %+v %v", i, rc, err)
		}
	}
	// Its name is free: a new label, a new id.
	rc, err = s.CreateLabel(ctx, testOp("l2"), "CND", NewLabel{Name: "api", Color: "red"})
	want(t, rc, err, StatusDone, "")
	if rc.ID == id {
		t.Fatal("the new label took the tombstone's id")
	}
}

// The write lock orders a label's delete against a ticket's add of it,
// whichever comes first: deleted then added, the add finds a tombstone
// (not_found); added then deleted, the delete removes the membership with
// one changes row for the ticket.
func TestALabelsDeleteAndAnAddAreOrderedByTheWriteLock(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	// Deleted, then added.
	first := newLabel(t, s, "CND", "first")
	tid, key := newTicket(t, s, "CND", "Labelled")
	rc, err := s.DeleteLabel(ctx, testOp("d1"), "CND", first)
	want(t, rc, err, StatusDone, "")
	rc, err = edit(s, "a1", key, TicketEdit{Add: TicketMembers{Labels: []string{first}}})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	if n := count(t, s, "SELECT COUNT(*) FROM ticket_labels WHERE ticket_id = ?", tid); n != 0 {
		t.Fatalf("%d memberships", n)
	}
	// Added, then deleted.
	second := newLabel(t, s, "CND", "second")
	rc, err = edit(s, "a2", key, TicketEdit{Add: TicketMembers{Labels: []string{second}}})
	want(t, rc, err, StatusDone, "")
	rc, err = s.DeleteLabel(ctx, testOp("d2"), "CND", second)
	want(t, rc, err, StatusDone, "")
	if n := count(t, s, "SELECT COUNT(*) FROM ticket_labels WHERE ticket_id = ?", tid); n != 0 {
		t.Fatalf("%d memberships after the delete", n)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM changes WHERE op_id = 'd2' AND entity = 'ticket' AND entity_id = ?", tid); n != 1 {
		t.Fatalf("%d changes rows for the ticket", n)
	}
}

// A label of another project is invalid on a ticket, a deleted one of its
// own not_found, made and deleted by the intents.
func TestALabelOfAnotherProjectIsInvalidAndADeletedOneNotFound(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	newProject(t, s, "OTH")
	_, key := newTicket(t, s, "CND", "Labelled")
	other := newLabel(t, s, "OTH", "api")
	gone := newLabel(t, s, "CND", "gone")
	rc, err := s.DeleteLabel(ctx, testOp("d1"), "CND", gone)
	want(t, rc, err, StatusDone, "")
	for id, refusal := range map[string]string{other: RefusalInvalid, gone: RefusalNotFound} {
		rc, err := s.CreateTicket(ctx, testOp("c-"+id), NewTicket{Project: "CND", Title: "x", Labels: []string{id}})
		want(t, rc, err, StatusFailed, refusal)
		rc, err = edit(s, "e-"+id, key, TicketEdit{Add: TicketMembers{Labels: []string{id}}})
		want(t, rc, err, StatusFailed, refusal)
	}
}
