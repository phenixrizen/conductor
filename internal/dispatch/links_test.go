package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// addLink adds a link through link.add and answers its receipt.
func addLink(s *Store, opID, key string, l NewLink) (Receipt, error) {
	return s.AddLink(context.Background(), testOp(opID), key, l)
}

// runs is the engine's runs as a test gives the store sight of them.
func runs(ids ...string) func(string) bool {
	return func(id string) bool { return slices.Contains(ids, id) }
}

// A delivery link without a reference takes its URL as one: two with
// different URLs are two links, the same URL twice is invalid, and one
// removed and added again is a new row with a new id.
func TestADeliveryLinkTakesItsURLAsItsReference(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	tid, key := newTicket(t, s, "CND", "Delivered")
	op := testOp("l1")
	op.ExpectedRev = 1
	rc, err := s.AddLink(ctx, op, key, NewLink{Kind: LinkDelivery, URL: "https://github.com/rocksolidlabs/conductor/pull/1", Note: "the merge"})
	want(t, rc, err, StatusDone, "")
	first := rc.ID
	if len(first) != IDLen || rc.Rev != 2 || rc.Intent != "link.add" {
		t.Fatalf("the receipt: %+v", rc)
	}
	rows := changesOf(t, s, "test", "l1")
	var added Link
	if len(rows) != 1 || rows[0].Entity != EntityTicket || rows[0].EntityID != tid || rows[0].ProjectID != pid || rows[0].Field != "links" || rows[0].Op != OpAdd ||
		json.Unmarshal([]byte(rows[0].Value), &added) != nil || added.ID != first || added.Ref != "https://github.com/rocksolidlabs/conductor/pull/1" ||
		added.URL != added.Ref || added.Note != "the merge" || added.CreatedBy != "Tester" || added.CreatedAt.IsZero() || added.Kind != LinkDelivery {
		t.Fatalf("the add's row: %+v", rows)
	}
	rc, err = addLink(s, "l2", key, NewLink{Kind: LinkDelivery, URL: "https://github.com/rocksolidlabs/conductor/pull/2"})
	want(t, rc, err, StatusDone, "")
	rc, err = addLink(s, "l3", key, NewLink{Kind: LinkDelivery, URL: "https://github.com/rocksolidlabs/conductor/pull/1"})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	// The same URL under another kind is another link.
	rc, err = addLink(s, "l4", key, NewLink{Kind: LinkExternal, URL: "https://github.com/rocksolidlabs/conductor/pull/1"})
	want(t, rc, err, StatusDone, "")
	// A reference given is the identity: the same reference with another
	// URL is the same link.
	rc, err = addLink(s, "l5", key, NewLink{Kind: LinkDelivery, Ref: "#42", URL: "https://github.com/rocksolidlabs/conductor/pull/42"})
	want(t, rc, err, StatusDone, "")
	rc, err = addLink(s, "l6", key, NewLink{Kind: LinkDelivery, Ref: "#42", URL: "https://example.com/other"})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	links, err := s.Links(ctx, key)
	if err != nil || len(links) != 4 || links[0].ID != first || links[3].Ref != "#42" {
		t.Fatalf("the links: %+v %v", links, err)
	}
	// Removed: the row named whole, its prior the add's tag; added again,
	// a new row with a new id, the old one a tombstone.
	rc, err = s.RemoveLink(ctx, testOp("x1"), key, first)
	want(t, rc, err, StatusDone, "")
	rows = changesOf(t, s, "test", "x1")
	var removed Link
	if len(rows) != 1 || rows[0].Op != OpRemove || rows[0].Field != "links" || rows[0].Prior != EncodeTags([]Tag{{"test", "l1", 0}}) ||
		json.Unmarshal([]byte(rows[0].Value), &removed) != nil || removed.ID != first || removed.URL != added.URL || removed.Note != "the merge" {
		t.Fatalf("the remove's rows: %+v", rows)
	}
	rc, err = addLink(s, "l7", key, NewLink{Kind: LinkDelivery, URL: "https://github.com/rocksolidlabs/conductor/pull/1"})
	want(t, rc, err, StatusDone, "")
	if rc.ID == first {
		t.Fatal("the link added again took the old id")
	}
	if n := count(t, s, "SELECT COUNT(*) FROM links WHERE id = ? AND deleted_at != ''", first); n != 1 {
		t.Fatal("the removed link is not a tombstone")
	}
	// The values checked.
	for i, l := range []NewLink{
		{Kind: "pr", URL: "https://example.com/x"},
		{Kind: "", URL: "https://example.com/x"},
		{Kind: LinkDelivery},
		{Kind: LinkDelivery, Ref: "#7"},
		{Kind: LinkExternal},
		{Kind: LinkRun},
		{Kind: LinkRun, URL: "https://example.com/run"},
		{Kind: LinkDelivery, URL: "ftp://example.com/x"},
		{Kind: LinkDelivery, URL: "example.com/x"},
		{Kind: LinkDelivery, URL: "https://example.com/x", Ref: "a\nb"},
		{Kind: LinkDelivery, URL: "https://example.com/x", Note: "a\rb"},
	} {
		rc, err := addLink(s, opID("bad", i), key, l)
		want(t, rc, err, StatusFailed, RefusalInvalid)
	}
	for i, l := range []NewLink{
		{Kind: LinkDelivery, URL: "https://h/" + strings.Repeat("u", MaxLinkURL)},
		{Kind: LinkDelivery, URL: "https://example.com/y", Ref: strings.Repeat("r", MaxLinkRef+1)},
		{Kind: LinkDelivery, URL: "https://example.com/y", Note: strings.Repeat("é", MaxLinkNote+1)},
	} {
		rc, err := addLink(s, opID("big", i), key, l)
		want(t, rc, err, StatusFailed, RefusalBound)
	}
	for i, x := range []struct{ key, id string }{{key, "0000000000000000"}, {key, first}, {"CND-99", rc.ID}} {
		rc, err := s.RemoveLink(ctx, testOp(opID("xbad", i)), x.key, x.id)
		want(t, rc, err, StatusFailed, RefusalNotFound)
	}
	_, other := newTicket(t, s, "CND", "Other")
	rc, err = s.RemoveLink(ctx, testOp("xother"), other, rc.ID)
	want(t, rc, err, StatusFailed, RefusalNotFound)
}

// A run link names a run the engine knows (not_found otherwise, and with
// no sight of the engine at all) and no other live ticket's: a second
// ticket is refused run_linked, the receipt naming the first.
func TestARunBelongsToOneTicketAtMost(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	tid, first := newTicket(t, s, "CND", "First")
	_, second := newTicket(t, s, "CND", "Second")
	rc, err := addLink(s, "blind", first, NewLink{Kind: LinkRun, Ref: "run1"})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	s.SetRunExists(runs("run1", "run2"))
	rc, err = addLink(s, "none", first, NewLink{Kind: LinkRun, Ref: "run9"})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	rc, err = addLink(s, "l1", first, NewLink{Kind: LinkRun, Ref: "run1"})
	want(t, rc, err, StatusDone, "")
	rc, err = addLink(s, "l2", second, NewLink{Kind: LinkRun, Ref: "run1"})
	want(t, rc, err, StatusFailed, RefusalRunLinked)
	if rc.Key != first || !strings.Contains(rc.Error, first) || !strings.Contains(rc.Error, "run1") {
		t.Fatalf("the refusal names the ticket that holds the run: %+v", rc)
	}
	// Kept as a failed receipt, answered again the same.
	again, err := addLink(s, "l2", second, NewLink{Kind: LinkRun, Ref: "run1"})
	if err != nil || !again.Repeat || again.Key != first || again.Refusal != RefusalRunLinked {
		t.Fatalf("the repeat: %+v %v", again, err)
	}
	// The first holds it once: a second link of it is invalid.
	rc, err = addLink(s, "l3", first, NewLink{Kind: LinkRun, Ref: "run1", URL: "https://example.com/runs/run1"})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	// Another run is free for the second.
	rc, err = addLink(s, "l4", second, NewLink{Kind: LinkRun, Ref: "run2"})
	want(t, rc, err, StatusDone, "")
	got, found, err := s.RunTicket(ctx, "run1")
	if err != nil || !found || got.ID != tid || !got.DeletedAt.IsZero() {
		t.Fatalf("run1's ticket: %+v %v %v", got, found, err)
	}
	if _, found, err := s.RunTicket(ctx, "run9"); err != nil || found {
		t.Fatalf("a run linked to nothing: %v %v", found, err)
	}
}

// A link whose reference and URL are 2 KiB each of backslashes and whose
// note is 1,000 of U+2028, added and removed, is recorded whole in its
// two changes rows, the add's value under 16 KiB.
func TestALinkAtItsBoundIsRecordedWhole(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	_, key := newTicket(t, s, "CND", "Linked")
	l := NewLink{
		Kind: LinkDelivery,
		Ref:  strings.Repeat(`\`, MaxLinkRef),
		URL:  "https://h/" + strings.Repeat(`\`, MaxLinkURL-len("https://h/")),
		Note: strings.Repeat(" ", MaxLinkNote),
	}
	op := Op{Actor: "admin", ID: "add", By: strings.Repeat(" ", MaxByName)}
	rc, err := s.AddLink(ctx, op, key, l)
	want(t, rc, err, StatusDone, "")
	id := rc.ID
	rc, err = s.RemoveLink(ctx, Op{Actor: "admin", ID: "remove", By: op.By}, key, id)
	want(t, rc, err, StatusDone, "")
	for _, opID := range []string{"add", "remove"} {
		rows := changesOf(t, s, "admin", opID)
		if len(rows) != 1 {
			t.Fatalf("%s: %d rows", opID, len(rows))
		}
		var got Link
		if err := json.Unmarshal([]byte(rows[0].Value), &got); err != nil {
			t.Fatalf("%s's value: %v", opID, err)
		}
		if got.ID != id || got.Kind != l.Kind || got.Ref != l.Ref || got.URL != l.URL || got.Note != l.Note || got.CreatedBy != op.By {
			t.Fatalf("%s's value is not the link whole", opID)
		}
		if opID == "add" && len(rows[0].Value) >= 16<<10 {
			t.Fatalf("the add's value is %d bytes", len(rows[0].Value))
		}
	}
	if links, err := s.Links(ctx, key); err != nil || len(links) != 0 {
		t.Fatalf("after the remove: %+v %v", links, err)
	}
}

// A ticket holds at most MaxLinks live links; a remove frees a place.
func TestLinksPastTheirBoundAreRefused(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	_, key := newTicket(t, s, "CND", "Linked")
	var last string
	for i := range MaxLinks {
		rc, err := addLink(s, opID("l", i), key, NewLink{Kind: LinkExternal, URL: fmt.Sprintf("https://example.com/%d", i)})
		want(t, rc, err, StatusDone, "")
		last = rc.ID
	}
	rc, err := addLink(s, "past", key, NewLink{Kind: LinkExternal, URL: "https://example.com/past"})
	want(t, rc, err, StatusFailed, RefusalBound)
	rc, err = s.RemoveLink(ctx, testOp("x1"), key, last)
	want(t, rc, err, StatusDone, "")
	rc, err = addLink(s, "fits", key, NewLink{Kind: LinkExternal, URL: "https://example.com/past"})
	want(t, rc, err, StatusDone, "")
}

// A blocker and a ticket with a run link deleted: the blocked ticket
// reads unblocked; the run's lookup names the deleted ticket as deleted;
// the run links to another ticket and the lookup names that one, live,
// from then on: after a second deleted ticket's link names the run too,
// and after the first deleted ticket's undelete, which removes its link
// with one changes row; with the deleted tickets' links alone it names
// the later-linked tombstone, by its add's place in the log, whichever
// was deleted later or numbered higher; and no other ticket's rev moves
// on a delete or an undelete.
func TestARunsTicketThroughDeletesAndUndeletes(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	s.SetRunExists(runs("run1", "run2"))
	_, blocker := newTicket(t, s, "CND", "Blocker")
	_, blocked := newTicket(t, s, "CND", "Blocked")
	r1ID, r1 := newTicket(t, s, "CND", "Ran first")
	r2ID, r2 := newTicket(t, s, "CND", "Ran second")
	liveID, live := newTicket(t, s, "CND", "Runs it now")
	rc, err := relate(s, "rel", blocker, RelationBlocks, blocked)
	want(t, rc, err, StatusDone, "")
	lookup := func(wantID string, deleted bool) {
		t.Helper()
		tk, found, err := s.RunTicket(ctx, "run1")
		if err != nil || !found || tk.ID != wantID || tk.DeletedAt.IsZero() != !deleted {
			t.Fatalf("the run's ticket: %s (deleted %v) %v %v, want %s (deleted %v)", tk.Key, !tk.DeletedAt.IsZero(), found, err, wantID, deleted)
		}
	}
	isBlocked := func(want bool) {
		t.Helper()
		if got, err := s.Blocked(ctx, blocked); err != nil || got != want {
			t.Fatalf("blocked %v %v, want %v", got, err, want)
		}
	}
	isBlocked(true)
	rc, err = addLink(s, "l1", r1, NewLink{Kind: LinkRun, Ref: "run1"})
	want(t, rc, err, StatusDone, "")
	lookup(r1ID, false)
	revs := func() string {
		var out []string
		for _, k := range []string{blocked, live} {
			out = append(out, fmt.Sprint(ticketOf(t, s, k).Rev))
		}
		return strings.Join(out, ",")
	}
	before := revs()
	// Both deleted: the blocked ticket reads unblocked, the run names the
	// deleted ticket as deleted, and no other ticket's rev moved.
	for i, k := range []string{blocker, r1} {
		rc, err := s.DeleteTicket(ctx, testOp(opID("d", i)), k)
		want(t, rc, err, StatusDone, "")
	}
	isBlocked(false)
	lookup(r1ID, true)
	if after := revs(); after != before {
		t.Fatalf("revs moved by the deletes: %s, before %s", after, before)
	}
	// A tombstone holds no run: a second ticket links it, then is deleted
	// too; with the two deleted tickets' links alone the lookup names the
	// later-linked tombstone.
	rc, err = addLink(s, "l2", r2, NewLink{Kind: LinkRun, Ref: "run1"})
	want(t, rc, err, StatusDone, "")
	lookup(r2ID, false)
	rc, err = s.DeleteTicket(ctx, testOp("d2"), r2)
	want(t, rc, err, StatusDone, "")
	lookup(r2ID, true)
	// A live ticket takes the run: the lookup names it, live, with the
	// two deleted tickets' links still there.
	rc, err = addLink(s, "l3", live, NewLink{Kind: LinkRun, Ref: "run1"})
	want(t, rc, err, StatusDone, "")
	lookup(liveID, false)
	if n := count(t, s, "SELECT COUNT(*) FROM links WHERE kind = 'run' AND ref = 'run1' AND deleted_at = ''"); n != 3 {
		t.Fatalf("%d live links name the run", n)
	}
	// The first deleted ticket's undelete removes its link, the run being
	// the live ticket's: one remove row beside the undelete's own, its
	// prior the add's tag; the lookup still names the live ticket; no
	// other ticket's rev moved.
	before = revs()
	rc, err = s.UndeleteTicket(ctx, testOp("u1"), r1)
	want(t, rc, err, StatusDone, "")
	rows := rowsOf(t, s, "u1")
	if len(rows) != 2 || !strings.HasPrefix(rows[0], "set:deleted_at=<") || !strings.HasPrefix(rows[1], "remove:links=") ||
		!strings.HasSuffix(rows[1], "<"+EncodeTags([]Tag{{"test", "l1", 0}})) {
		t.Fatalf("the undelete's rows: %v", rows)
	}
	if links, err := s.Links(ctx, r1); err != nil || len(links) != 0 {
		t.Fatalf("%s's links after its undelete: %+v %v", r1, links, err)
	}
	lookup(liveID, false)
	if after := revs(); after != before {
		t.Fatalf("revs moved by the undelete: %s, before %s", after, before)
	}
	// The blocker's undelete brings its relation back whole.
	rc, err = s.UndeleteTicket(ctx, testOp("u0"), blocker)
	want(t, rc, err, StatusDone, "")
	if rows := rowsOf(t, s, "u0"); len(rows) != 1 {
		t.Fatalf("the blocker's undelete wrote %v", rows)
	}
	isBlocked(true)
	// The live ticket's link removed: the lookup falls back to the one
	// tombstone whose link still names the run.
	links, err := s.Links(ctx, live)
	if err != nil || len(links) != 1 {
		t.Fatalf("the live ticket's links: %+v %v", links, err)
	}
	rc, err = s.RemoveLink(ctx, testOp("x3"), live, links[0].ID)
	want(t, rc, err, StatusDone, "")
	lookup(r2ID, true)
	// r1, undeleted, may take the run again now that no live ticket holds
	// it; its link then wins over the tombstone's.
	rc, err = addLink(s, "l4", r1, NewLink{Kind: LinkRun, Ref: "run1"})
	want(t, rc, err, StatusDone, "")
	lookup(r1ID, false)
	// And r2's undelete, the run r1's now, removes r2's link.
	rc, err = s.UndeleteTicket(ctx, testOp("u2"), r2)
	want(t, rc, err, StatusDone, "")
	if rows := rowsOf(t, s, "u2"); len(rows) != 2 || !strings.HasSuffix(rows[1], "<"+EncodeTags([]Tag{{"test", "l2", 0}})) {
		t.Fatalf("r2's undelete's rows: %v", rows)
	}
	lookup(r1ID, false)
	// A live ticket wins over a tombstone whose link is newer: P links
	// run2 and is deleted, Q links it and is deleted, P's undelete keeps
	// its link (no live ticket holds the run), and the lookup answers P.
	// Q is made first, so P is the higher-numbered.
	qID, q := newTicket(t, s, "CND", "Q")
	pID, p := newTicket(t, s, "CND", "P")
	for i, step := range []func() (Receipt, error){
		func() (Receipt, error) { return addLink(s, "p-link", p, NewLink{Kind: LinkRun, Ref: "run2"}) },
		func() (Receipt, error) { return s.DeleteTicket(ctx, testOp("p-del"), p) },
		func() (Receipt, error) { return addLink(s, "q-link", q, NewLink{Kind: LinkRun, Ref: "run2"}) },
		func() (Receipt, error) { return s.DeleteTicket(ctx, testOp("q-del"), q) },
		func() (Receipt, error) { return s.UndeleteTicket(ctx, testOp("p-undel"), p) },
	} {
		rc, err := step()
		if err != nil || rc.Status != StatusDone {
			t.Fatalf("step %d: %+v %v", i, rc, err)
		}
	}
	if rows := rowsOf(t, s, "p-undel"); len(rows) != 1 {
		t.Fatalf("P's undelete removed its link: %v", rows)
	}
	if tk, found, err := s.RunTicket(ctx, "run2"); err != nil || !found || tk.ID != pID || !tk.DeletedAt.IsZero() {
		t.Fatalf("run2's ticket: %s %v %v, want %s live", tk.Key, found, err, p)
	}
	// P deleted again: with the two tombstones' links alone the lookup
	// answers Q, the later-linked, though P is both the later-deleted and
	// the higher-numbered.
	rc, err = s.DeleteTicket(ctx, testOp("p-del2"), p)
	want(t, rc, err, StatusDone, "")
	if np, nq := ticketOf(t, s, p).Number, ticketOf(t, s, q).Number; np <= nq {
		t.Fatalf("P is numbered %d and Q %d", np, nq)
	}
	if tk, found, err := s.RunTicket(ctx, "run2"); err != nil || !found || tk.ID != qID || tk.DeletedAt.IsZero() {
		t.Fatalf("run2's ticket: %s %v %v, want %s deleted", tk.Key, found, err, q)
	}
}
