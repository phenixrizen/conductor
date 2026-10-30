package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAddPersonAndTheDirectory(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	for i, name := range []string{"Bob", "Alice", "Zoë Q", strings.Repeat("n", MaxPersonName)} {
		rc, err := s.AddPerson(ctx, testOp(opID("a", i)), name)
		want(t, rc, err, StatusDone, "")
		if rc.ID != name {
			t.Fatalf("the receipt names %q", rc.ID)
		}
	}
	people, err := s.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range people {
		if p.CreatedAt.IsZero() {
			t.Fatalf("no created_at: %+v", p)
		}
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "Alice,Bob,Zoë Q,"+strings.Repeat("n", MaxPersonName) {
		t.Fatalf("the directory: %v", names)
	}
	// The create row carries the person whole.
	rows := changesOf(t, s, "test", "a1")
	if len(rows) != 1 || rows[0].Entity != EntityPerson || rows[0].EntityID != "Alice" || rows[0].Op != OpCreate || rows[0].N != 0 || rows[0].Field != "" {
		t.Fatalf("the create row: %+v", rows)
	}
	var p Person
	if err := json.Unmarshal([]byte(rows[0].Value), &p); err != nil || p.Name != "Alice" || p.CreatedAt.IsZero() {
		t.Fatalf("the create row's value: %q %v", rows[0].Value, err)
	}
	// A name held already is invalid; the directory is unchanged.
	rc, err := s.AddPerson(ctx, testOp("dup"), "Alice")
	want(t, rc, err, StatusFailed, RefusalInvalid)
	if !strings.Contains(rc.Error, "already") {
		t.Fatalf("the refusal's words: %q", rc.Error)
	}
	for name, refusal := range map[string]string{
		"":                                   RefusalInvalid,
		"a\tb":                               RefusalInvalid,
		"a\nb":                               RefusalInvalid,
		"\x7f":                               RefusalInvalid,
		"\xff\xfe":                           RefusalInvalid,
		strings.Repeat("n", MaxPersonName+1): RefusalBound,
	} {
		rc, err := s.AddPerson(ctx, testOp("bad-"+newID()), name)
		want(t, rc, err, StatusFailed, refusal)
	}
	if people, err := s.People(ctx); err != nil || len(people) != 4 {
		t.Fatalf("the directory after the refusals: %d %v", len(people), err)
	}
}

// A name a ticket gives that the directory lacks is added to it in the
// same operation, after the ticket's rows: its owner and each person:
// assignee, never an agent; a name there already writes nothing, and an
// edit that changes nothing of the ticket adds no one.
func TestANameATicketGivesIsAddedToTheDirectory(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	rc, err := s.AddPerson(ctx, testOp("p1"), "Bob")
	want(t, rc, err, StatusDone, "")
	calls := listen(s)
	rc, err = s.CreateTicket(ctx, testOp("c1"), NewTicket{Project: "CND", Title: "Named", Owner: "Alice", Assignees: []string{"person:Bob", "person:Carol", "agent:claude"}})
	want(t, rc, err, StatusDone, "")
	key, tid := rc.Key, rc.ID
	// The receipt's subject is the ticket, whose rev it carries.
	if rc.Rev != 1 {
		t.Fatalf("the receipt: %+v", rc)
	}
	names := func() string {
		t.Helper()
		people, err := s.People(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range people {
			out = append(out, p.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(); got != "Alice,Bob,Carol" {
		t.Fatalf("the directory: %s", got)
	}
	rows := changesOf(t, s, "test", "c1")
	var people []string
	for i, r := range rows {
		if r.Entity == EntityPerson {
			people = append(people, r.EntityID)
			if r.Op != OpCreate || (rows[i-1].Entity != EntityTicket && rows[i-1].Entity != EntityPerson) {
				t.Fatalf("a person's row: %+v", r)
			}
			continue
		}
		if len(people) > 0 {
			t.Fatalf("a ticket's row after a person's: %+v", r)
		}
	}
	if strings.Join(people, ",") != "Alice,Carol" {
		t.Fatalf("the people added: %v", people)
	}
	// OnChange, called once: the ticket, then each person added, its id
	// the name.
	if got := calls.one(t); len(got) != 3 || got[0].Entity != EntityTicket || got[0].ID != tid || got[1] != (Change{Seq: got[0].Seq, Entity: EntityPerson, ID: "Alice"}) ||
		got[2] != (Change{Seq: got[0].Seq, Entity: EntityPerson, ID: "Carol"}) {
		t.Fatalf("OnChange: %+v", got)
	}
	// An edit: a new owner and a new assignee added; a name held writes
	// nothing.
	rc, err = edit(s, "e1", key, TicketEdit{Set: TicketSet{Owner: ptr("Dan")}, Add: TicketMembers{Assignees: []string{"person:Erin", "agent:codex"}}})
	want(t, rc, err, StatusDone, "")
	if got := names(); got != "Alice,Bob,Carol,Dan,Erin" {
		t.Fatalf("the directory after an edit: %s", got)
	}
	rc, err = edit(s, "e2", key, TicketEdit{Set: TicketSet{Owner: ptr("Bob")}, Add: TicketMembers{Assignees: []string{"person:Alice"}}})
	want(t, rc, err, StatusDone, "")
	for _, r := range changesOf(t, s, "test", "e2") {
		if r.Entity == EntityPerson {
			t.Fatalf("a held name added again: %+v", r)
		}
	}
	// An owner a merge left that the directory lacks, set to itself: the
	// edit changes nothing, is refused, and adds no one; nor does an edit
	// of another field, which gives no name.
	if _, err := s.db.Exec("UPDATE tickets SET owner = 'Zed' WHERE id = ?", tid); err != nil {
		t.Fatal(err)
	}
	rc, err = edit(s, "e3", key, TicketEdit{Set: TicketSet{Owner: ptr("Zed")}})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = edit(s, "e4", key, TicketEdit{Set: TicketSet{Owner: ptr("Zed"), Title: ptr("Renamed")}})
	want(t, rc, err, StatusDone, "")
	if got := names(); strings.Contains(got, "Zed") {
		t.Fatalf("an edit that gave no name added one: %s", got)
	}
	// A name added by a ticket is a name in the directory: person.add of
	// it is refused as any held name is.
	rc, err = s.AddPerson(ctx, testOp("p2"), "Dan")
	want(t, rc, err, StatusFailed, RefusalInvalid)
}
