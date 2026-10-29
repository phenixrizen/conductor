package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// relate adds a relation through relation.add and answers its receipt.
func relate(s *Store, opID, key, kind, to string) (Receipt, error) {
	return s.AddRelation(context.Background(), testOp(opID), key, kind, to)
}

func TestAddAndRemoveARelation(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	aID, a := newTicket(t, s, "CND", "A")
	bID, b := newTicket(t, s, "CND", "B")
	var got []Change
	s.OnChange(func(c Change) { got = append(got, c) })
	op := testOp("r1")
	op.ExpectedRev = 1
	rc, err := s.AddRelation(ctx, op, a, RelationBlocks, b)
	want(t, rc, err, StatusDone, "")
	id := rc.ID
	if len(id) != IDLen || rc.Rev != 2 || rc.Intent != "relation.add" {
		t.Fatalf("the receipt: %+v", rc)
	}
	// One add row, on the first ticket, its value the relation whole; it
	// counts for both tickets: both revs bumped, both announced.
	rows := changesOf(t, s, "test", "r1")
	if len(rows) != 1 || rows[0].Entity != EntityTicket || rows[0].EntityID != aID || rows[0].TicketID != aID || rows[0].ProjectID != pid ||
		rows[0].Field != "relations" || rows[0].Op != OpAdd {
		t.Fatalf("the rows: %+v", rows)
	}
	var r Relation
	if err := json.Unmarshal([]byte(rows[0].Value), &r); err != nil || r.ID != id || r.FromID != aID || r.ToID != bID || r.Kind != RelationBlocks || r.CreatedBy != "Tester" || r.CreatedAt.IsZero() {
		t.Fatalf("the add's value: %s %v", rows[0].Value, err)
	}
	if ticketOf(t, s, a).Rev != 2 || ticketOf(t, s, b).Rev != 2 {
		t.Fatal("the two tickets' revs were not both bumped")
	}
	if len(got) != 2 || got[0] != (Change{Seq: rows[0].Seq, Entity: EntityTicket, ID: aID, Ticket: aID, Project: "CND"}) ||
		got[1] != (Change{Seq: rows[0].Seq, Entity: EntityTicket, ID: bID, Ticket: bID, Project: "CND"}) {
		t.Fatalf("OnChange: %+v", got)
	}
	// Read from both ends.
	for _, c := range []struct{ key, other string }{{a, b}, {b, a}} {
		rs, err := s.Relations(ctx, c.key)
		if err != nil || len(rs) != 1 || rs[0].ID != id || rs[0].FromID != aID || rs[0].Other.Key != c.other || rs[0].Other.Title == "" || !rs[0].Other.DeletedAt.IsZero() {
			t.Fatalf("%s's relations: %+v %v", c.key, rs, err)
		}
	}
	// The member live already is invalid; and for relates the reverse too.
	rc, err = relate(s, "r2", a, RelationBlocks, b)
	want(t, rc, err, StatusFailed, RefusalInvalid)
	if !strings.Contains(rc.Error, "already") {
		t.Fatalf("the refusal's words: %q", rc.Error)
	}
	rc, err = relate(s, "r3", a, RelationRelates, b)
	want(t, rc, err, StatusDone, "")
	rc, err = relate(s, "r4", b, RelationRelates, a)
	want(t, rc, err, StatusFailed, RefusalInvalid)
	// Removed through the other end's key: the row named whole, its prior
	// the add's tag, both revs bumped, the ticket with the key the
	// receipt's subject.
	got = nil
	revA, revB := ticketOf(t, s, a).Rev, ticketOf(t, s, b).Rev
	rc, err = s.RemoveRelation(ctx, testOp("x1"), b, id)
	want(t, rc, err, StatusDone, "")
	if rc.Rev != revB+1 || ticketOf(t, s, a).Rev != revA+1 {
		t.Fatalf("the remove's receipt %+v; A's rev %d, was %d", rc, ticketOf(t, s, a).Rev, revA)
	}
	rows = changesOf(t, s, "test", "x1")
	var removed Relation
	if len(rows) != 1 || rows[0].EntityID != aID || rows[0].Op != OpRemove || rows[0].Field != "relations" ||
		rows[0].Prior != EncodeTags([]Tag{{"test", "r1", 0}}) || json.Unmarshal([]byte(rows[0].Value), &removed) != nil || removed.ID != id || removed.ToID != bID {
		t.Fatalf("the remove's rows: %+v", rows)
	}
	if len(got) != 2 || got[0].ID != bID || got[1].ID != aID {
		t.Fatalf("OnChange of the remove: %+v", got)
	}
	if rs, err := s.Relations(ctx, a); err != nil || len(rs) != 1 || rs[0].Kind != RelationRelates {
		t.Fatalf("A's relations after the remove: %+v %v", rs, err)
	}
	// Added again: a new row with a new id, the old one a tombstone.
	rc, err = relate(s, "r5", a, RelationBlocks, b)
	want(t, rc, err, StatusDone, "")
	if rc.ID == id {
		t.Fatal("the relation added again took the old id")
	}
	if n := count(t, s, "SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ? AND kind = 'blocks' AND deleted_at != ''", aID, bID); n != 1 {
		t.Fatalf("%d tombstones", n)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM relations WHERE id = ? AND deleted_at != ''", id); n != 1 {
		t.Fatal("the old relation is not a tombstone")
	}
	// The values and the references checked.
	_, c := newTicket(t, s, "CND", "C")
	for i, x := range []struct {
		key, kind, to, refusal string
	}{
		{a, "depends", c, RefusalInvalid},
		{a, "", c, RefusalInvalid},
		{a, RelationBlocks, "CND-99", RefusalNotFound},
		{a, RelationBlocks, "garbage", RefusalNotFound},
		{"CND-99", RelationBlocks, c, RefusalNotFound},
		{a, RelationBlocks, a, RefusalCycle},
		{a, RelationRelates, a, RefusalInvalid},
	} {
		rc, err := relate(s, opID("bad", i), x.key, x.kind, x.to)
		want(t, rc, err, StatusFailed, x.refusal)
	}
	for i, x := range []struct{ key, id string }{{a, "0000000000000000"}, {c, rc.ID}, {"CND-99", rc.ID}, {a, id}} {
		rc, err := s.RemoveRelation(ctx, testOp(opID("xbad", i)), x.key, x.id)
		want(t, rc, err, StatusFailed, RefusalNotFound)
	}
}

// blocks edges never form a cycle: A blocks B blocks C, then C blocks A
// is refused, and so is B blocks A; the walk takes a tombstone's
// relations as edges, so a cycle through a deleted ticket is refused too
// and its undelete makes none. relates is not a graph of blockers: it
// closes no cycle.
func TestABlocksCycleIsRefused(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	_, a := newTicket(t, s, "CND", "A")
	_, b := newTicket(t, s, "CND", "B")
	_, c := newTicket(t, s, "CND", "C")
	rc, err := relate(s, "ab", a, RelationBlocks, b)
	want(t, rc, err, StatusDone, "")
	rc, err = relate(s, "bc", b, RelationBlocks, c)
	want(t, rc, err, StatusDone, "")
	bc := rc.ID
	rc, err = relate(s, "ca", c, RelationBlocks, a)
	want(t, rc, err, StatusFailed, RefusalCycle)
	if !strings.Contains(rc.Error, "cycle") || !strings.Contains(rc.Error, a) || !strings.Contains(rc.Error, c) {
		t.Fatalf("the refusal's words: %q", rc.Error)
	}
	rc, err = relate(s, "ba", b, RelationBlocks, a)
	want(t, rc, err, StatusFailed, RefusalCycle)
	rc, err = relate(s, "ca-relates", c, RelationRelates, a)
	want(t, rc, err, StatusDone, "")
	// Through a tombstone: B deleted keeps its relations, which still
	// close the cycle.
	rc, err = s.DeleteTicket(ctx, testOp("db"), b)
	want(t, rc, err, StatusDone, "")
	rc, err = relate(s, "ca2", c, RelationBlocks, a)
	want(t, rc, err, StatusFailed, RefusalCycle)
	rc, err = s.UndeleteTicket(ctx, testOp("ub"), b)
	want(t, rc, err, StatusDone, "")
	// Once B no longer blocks C, C may block A.
	rc, err = s.RemoveRelation(ctx, testOp("xbc"), c, bc)
	want(t, rc, err, StatusDone, "")
	rc, err = relate(s, "ca3", c, RelationBlocks, a)
	want(t, rc, err, StatusDone, "")
	// A longer cycle, past what a pair would catch: D blocks E blocks F
	// blocks G, then G blocks D.
	var chain []string
	for i := range 4 {
		_, k := newTicket(t, s, "CND", fmt.Sprintf("Chain %d", i))
		chain = append(chain, k)
	}
	for i := range 3 {
		rc, err := relate(s, opID("chain", i), chain[i], RelationBlocks, chain[i+1])
		want(t, rc, err, StatusDone, "")
	}
	rc, err = relate(s, "close", chain[3], RelationBlocks, chain[0])
	want(t, rc, err, StatusFailed, RefusalCycle)
}

// A relation is between two tickets of one project: another project's
// ticket is invalid, and nothing is written.
func TestACrossProjectRelationIsRefused(t *testing.T) {
	s := openStore(t, storeDir(t))
	newProject(t, s, "CND")
	newProject(t, s, "OTH")
	_, a := newTicket(t, s, "CND", "A")
	_, o := newTicket(t, s, "OTH", "O")
	for i, kind := range RelationKinds {
		rc, err := relate(s, opID("x", i), a, kind, o)
		want(t, rc, err, StatusFailed, RefusalInvalid)
		rc, err = relate(s, opID("y", i), o, kind, a)
		want(t, rc, err, StatusFailed, RefusalInvalid)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM relations"); n != 0 {
		t.Fatalf("%d relations written", n)
	}
	if ticketOf(t, s, a).Rev != 1 || ticketOf(t, s, o).Rev != 1 {
		t.Fatal("a refused relation moved a rev")
	}
}

// A ticket is blocked while a live ticket blocks it whose status is
// neither done nor cancelled: a blocker closed, deleted or removed
// blocks nothing; reopened or undeleted it blocks again; relates never
// blocks.
func TestBlockedIsComputedFromLiveBlockers(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newOpenProject(t, s, "CND")
	_, x := newTicket(t, s, "CND", "X")
	_, a := newTicket(t, s, "CND", "Blocker A")
	_, b := newTicket(t, s, "CND", "Blocker B")
	_, r := newTicket(t, s, "CND", "Only related")
	blocked := func(want bool) {
		t.Helper()
		got, err := s.Blocked(ctx, x)
		if err != nil || got != want {
			t.Fatalf("blocked %v (%v), want %v", got, err, want)
		}
	}
	setStatus := func(opID, key, status string) {
		t.Helper()
		rc, err := edit(s, opID, key, TicketEdit{Set: TicketSet{Status: ptr(status)}})
		want(t, rc, err, StatusDone, "")
	}
	blocked(false)
	rc, err := relate(s, "rx", r, RelationRelates, x)
	want(t, rc, err, StatusDone, "")
	blocked(false)
	rc, err = relate(s, "ax", a, RelationBlocks, x)
	want(t, rc, err, StatusDone, "")
	ax := rc.ID
	blocked(true)
	// A blocker's own blocked state does not pass on: X blocks nothing.
	if got, err := s.Blocked(ctx, a); err != nil || got {
		t.Fatalf("the blocker reads blocked: %v %v", got, err)
	}
	setStatus("a-done", a, TicketDone)
	blocked(false)
	setStatus("a-ready", a, TicketReady)
	blocked(true)
	setStatus("a-cancelled", a, TicketCancelled)
	blocked(false)
	setStatus("a-back", a, TicketInProgress)
	blocked(true)
	// Deleted, it blocks nothing; undeleted, it blocks again.
	rc, err = s.DeleteTicket(ctx, testOp("da"), a)
	want(t, rc, err, StatusDone, "")
	blocked(false)
	rc, err = s.UndeleteTicket(ctx, testOp("ua"), a)
	want(t, rc, err, StatusDone, "")
	blocked(true)
	// Two blockers: both must be out of the way.
	rc, err = relate(s, "bx", b, RelationBlocks, x)
	want(t, rc, err, StatusDone, "")
	rc, err = s.RemoveRelation(ctx, testOp("xa"), x, ax)
	want(t, rc, err, StatusDone, "")
	blocked(true)
	setStatus("b-done", b, TicketDone)
	blocked(false)
	// A blocked ticket's status changes freely: a person decides.
	setStatus("b-open", b, TicketReady)
	setStatus("x-done", x, TicketDone)
	blocked(true)
	if _, err := s.Blocked(ctx, "CND-99"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no ticket: %v", err)
	}
}

// A ticket holds at most MaxRelations live relations, at either end,
// whichever end adds the next; a remove is never refused. A relation whose
// other end is deleted is inactive and counts for nothing, so the deleted
// ticket's place is free; its undelete brings the relation back and may
// leave the ticket past its bound, refusing its next add and never a
// remove.
func TestRelationsPastTheirBoundAreRefused(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	hubID, hub := newTicket(t, s, "CND", "Hub")
	// All but the last at the hub written by hand in one transaction, half
	// from the hub and half to it: the adds at the bound are what is
	// tested.
	now := stamp(time.Now())
	rows, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range MaxRelations - 1 {
		spoke := newID()
		if _, err := rows.Exec("INSERT INTO tickets (id, project_id, number, key, title, created_at, updated_at, rev) VALUES (?, ?, ?, ?, 'Spoke', ?, ?, 1)",
			spoke, pid, 1000+i, fmt.Sprintf("CND-%d", 1000+i), now, now); err != nil {
			t.Fatal(err)
		}
		from, to := hubID, spoke
		if i%2 == 1 {
			from, to = spoke, hubID
		}
		if _, err := rows.Exec("INSERT INTO relations (id, from_id, to_id, kind, created_at) VALUES (?, ?, ?, 'relates', ?)", newID(), from, to, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Commit(); err != nil {
		t.Fatal(err)
	}
	_, spoke := newTicket(t, s, "CND", "The last spoke")
	rc, err := relate(s, "r-last", spoke, RelationRelates, hub)
	want(t, rc, err, StatusDone, "")
	last := rc.ID
	_, extra := newTicket(t, s, "CND", "Extra")
	rc, err = relate(s, "past1", hub, RelationBlocks, extra)
	want(t, rc, err, StatusFailed, RefusalBound)
	if !strings.Contains(rc.Error, hub) || !strings.Contains(rc.Error, fmt.Sprint(MaxRelations)) {
		t.Fatalf("the refusal's words: %q", rc.Error)
	}
	// From the other end, the hub's bound refuses it too.
	rc, err = relate(s, "past2", extra, RelationBlocks, hub)
	want(t, rc, err, StatusFailed, RefusalBound)
	rc, err = s.RemoveRelation(ctx, testOp("x1"), hub, last)
	want(t, rc, err, StatusDone, "")
	rc, err = relate(s, "fits", extra, RelationBlocks, hub)
	want(t, rc, err, StatusDone, "")
	if rs, err := s.Relations(ctx, hub); err != nil || len(rs) != MaxRelations {
		t.Fatalf("the hub's relations: %d %v", len(rs), err)
	}
	// A spoke deleted: its relation stays, inactive, and the hub takes one
	// more in its place, then refuses the next.
	rc, err = s.DeleteTicket(ctx, testOp("del"), "CND-1000")
	want(t, rc, err, StatusDone, "")
	_, more := newTicket(t, s, "CND", "More")
	rc, err = relate(s, "while-deleted", hub, RelationRelates, more)
	want(t, rc, err, StatusDone, "")
	taken := rc.ID
	rs, err := s.Relations(ctx, hub)
	if err != nil || len(rs) != MaxRelations+1 {
		t.Fatalf("the hub's relations with a spoke deleted: %d %v", len(rs), err)
	}
	var inactive []string
	for _, r := range rs {
		if !r.Other.DeletedAt.IsZero() {
			inactive = append(inactive, r.Other.Key)
		}
	}
	if len(inactive) != 1 || inactive[0] != "CND-1000" {
		t.Fatalf("the inactive relations: %v", inactive)
	}
	_, beyond := newTicket(t, s, "CND", "Beyond")
	rc, err = relate(s, "past3", hub, RelationRelates, beyond)
	want(t, rc, err, StatusFailed, RefusalBound)
	if !strings.Contains(rc.Error, fmt.Sprintf("this would make %d", MaxRelations+1)) {
		t.Fatalf("the refusal's words: %q", rc.Error)
	}
	// The spoke's undelete brings its relation back whole: the hub stands
	// past its bound, which refuses its next add and allows a remove.
	rc, err = s.UndeleteTicket(ctx, testOp("undel"), "CND-1000")
	want(t, rc, err, StatusDone, "")
	if rs, err := s.Relations(ctx, hub); err != nil || len(rs) != MaxRelations+1 {
		t.Fatalf("the hub's relations after the undelete: %d %v", len(rs), err)
	}
	rc, err = relate(s, "past4", hub, RelationRelates, beyond)
	want(t, rc, err, StatusFailed, RefusalBound)
	if !strings.Contains(rc.Error, fmt.Sprintf("this would make %d", MaxRelations+2)) {
		t.Fatalf("the refusal's words past the bound: %q", rc.Error)
	}
	rc, err = s.RemoveRelation(ctx, testOp("x2"), hub, taken)
	want(t, rc, err, StatusDone, "")
}
