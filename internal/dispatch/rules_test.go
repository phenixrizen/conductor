package dispatch

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestClassForEveryFieldInTheTable(t *testing.T) {
	cases := []struct {
		entity string
		fields []string
		class  FieldClass
	}{
		{EntityTicket, []string{"id", "project_id", "key", "created_at", "created_by"}, ClassImmutable},
		{EntityTicket, []string{"number"}, ClassAllocation},
		{EntityTicket, []string{"status"}, ClassGated},
		{EntityTicket, []string{"title", "description", "acceptance", "priority", "horizon", "owner", "parent_id", "milestone_id",
			"accepted_at", "accepted_by", "accepted_for", "accepted_criteria", "accepted_note", "closed_at", "deleted_at",
			"external_id", "external_url", "external_snapshot", "authority"}, ClassScalar},
		{EntityTicket, []string{"assignees", "ticket_assignees", "labels", "ticket_labels", "relations", "links", "attachments"}, ClassSet},
		{EntityTicket, []string{"comments"}, ClassLog},
		{EntityProject, []string{"id", "created_at"}, ClassImmutable},
		{EntityProject, []string{"key", "next_number", "number_from", "number_to"}, ClassAllocation},
		{EntityProject, []string{"name", "description", "cwd", "tracker", "tracker_url", "close_requires", "archived_at"}, ClassScalar},
		{EntityLabel, []string{"id", "project_id", "created_at"}, ClassImmutable},
		{EntityLabel, []string{"name", "color", "deleted_at"}, ClassScalar},
		{EntityMilestone, []string{"id", "project_id", "created_at"}, ClassImmutable},
		{EntityMilestone, []string{"name", "description", "target", "state", "deleted_at"}, ClassScalar},
		{EntityView, []string{"id", "project_id", "created_at"}, ClassImmutable},
		{EntityView, []string{"name", "kind", "query", "deleted_at"}, ClassScalar},
		{EntityComment, []string{"id", "ticket_id", "author", "by", "created_at"}, ClassImmutable},
		{EntityComment, []string{"body", "edited_at", "deleted_at"}, ClassScalar},
		{EntityPerson, []string{"name", "created_at"}, ClassImmutable},
		{"change", []string{"actor", "op_id", "n"}, ClassImmutable},
		{"change", []string{"value", "prior", "seq"}, ClassLog},
		{"changes", []string{"at"}, ClassLog},
		{EntityTicket, []string{"rev", "updated_at", "nothing"}, ""},
		{"nothing", []string{"id"}, ""},
	}
	for _, c := range cases {
		for _, f := range c.fields {
			if got := Class(c.entity, f); got != c.class {
				t.Errorf("Class(%s, %s) = %q, want %q", c.entity, f, got, c.class)
			}
		}
	}
}

// row is a change row of an actor at a time, for the merge tests.
func row(entity, field, op string, at time.Time, actor, opID string, n int) ChangeRow {
	return ChangeRow{Entity: entity, Field: field, Op: op, At: at, Actor: actor, OpID: opID, N: n}
}

func TestMergeByClass(t *testing.T) {
	t1 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Second)
	check := func(name string, got, want Winner) {
		t.Helper()
		if got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	// Scalar: the later wins, and the tie-breakers in order.
	check("later scalar", Merge(row("ticket", "title", OpSet, t1, "a", "x", 0), row("ticket", "title", OpSet, t2, "b", "y", 0)), TakeRemote)
	check("earlier scalar", Merge(row("ticket", "title", OpSet, t2, "a", "x", 0), row("ticket", "title", OpSet, t1, "b", "y", 0)), KeepLocal)
	check("equal at, the greater actor", Merge(row("ticket", "title", OpSet, t1, "a", "x", 0), row("ticket", "title", OpSet, t1, "b", "x", 0)), TakeRemote)
	check("equal at, the lesser actor", Merge(row("ticket", "title", OpSet, t1, "b", "x", 0), row("ticket", "title", OpSet, t1, "a", "x", 0)), KeepLocal)
	check("equal at and actor, the greater op id", Merge(row("ticket", "title", OpSet, t1, "a", "1", 0), row("ticket", "title", OpSet, t1, "a", "2", 0)), TakeRemote)
	check("equal at and actor, the lesser op id", Merge(row("ticket", "title", OpSet, t1, "a", "2", 0), row("ticket", "title", OpSet, t1, "a", "1", 0)), KeepLocal)
	check("a project's scalar", Merge(row("project", "name", OpSet, t1, "a", "x", 0), row("project", "name", OpSet, t2, "b", "y", 0)), TakeRemote)
	// Gated: the scalar rule, the policy never re-applied.
	check("gated later", Merge(row("ticket", "status", OpSet, t1, "a", "x", 0), row("ticket", "status", OpSet, t2, "b", "y", 0)), TakeRemote)
	check("gated earlier", Merge(row("ticket", "status", OpSet, t2, "a", "x", 0), row("ticket", "status", OpSet, t1, "b", "y", 0)), KeepLocal)
	// Immutable and allocation: never written twice.
	check("immutable later", Merge(row("ticket", "key", OpSet, t1, "a", "x", 0), row("ticket", "key", OpSet, t2, "b", "y", 0)), KeepLocal)
	check("allocation later", Merge(row("ticket", "number", OpSet, t1, "a", "x", 0), row("ticket", "number", OpSet, t2, "b", "y", 0)), KeepLocal)
	// A second create of an id held is dropped, whenever it was made.
	check("a second create, later", Merge(row("ticket", "", OpCreate, t1, "a", "x", 0), row("ticket", "", OpCreate, t2, "b", "y", 0)), KeepLocal)
	check("a second create, earlier", Merge(row("ticket", "", OpCreate, t2, "a", "x", 0), row("ticket", "", OpCreate, t1, "b", "y", 0)), KeepLocal)
	// A change row of a held identity is dropped, a log row's included.
	check("a held identity", Merge(row("ticket", "title", OpSet, t1, "a", "x", 0), row("ticket", "title", OpSet, t2, "a", "x", 0)), KeepLocal)
	check("a held identity in a log", Merge(row("ticket", "comments", OpAdd, t1, "a", "x", 0), row("ticket", "comments", OpAdd, t1, "a", "x", 0)), KeepLocal)
	// A log is never merged: both rows stand.
	check("a log", Merge(row("ticket", "comments", OpAdd, t1, "a", "x", 0), row("ticket", "comments", OpAdd, t2, "b", "y", 0)), KeepBoth)
	check("the changes log", Merge(row("change", "value", OpSet, t1, "a", "x", 0), row("change", "value", OpSet, t1, "b", "y", 0)), KeepBoth)
	// Sets: observed remove.
	a1 := row("ticket", "labels", OpAdd, t1, "a", "x", 0)
	b1 := row("ticket", "labels", OpAdd, t1, "b", "y", 0)
	removeA1 := row("ticket", "labels", OpRemove, t2, "a", "z", 0)
	removeA1.Prior = EncodeTags([]Tag{TagOf(a1)})
	removeNone := row("ticket", "labels", OpRemove, t2, "b", "w", 0)
	removeNone.Prior = EncodeTags(nil)
	check("two adds", Merge(a1, b1), KeepBoth)
	check("an observed add is removed", Merge(a1, removeA1), TakeRemote)
	check("an add the remove did not see survives, the remove standing for what it names", Merge(b1, removeA1), KeepBoth)
	check("a remove that saw the add drops it", Merge(removeA1, a1), KeepLocal)
	check("a remove that did not see the add keeps both", Merge(removeA1, b1), KeepBoth)
	check("a remove naming nothing", Merge(a1, removeNone), KeepBoth)
	check("a remove naming nothing, from its side", Merge(removeNone, a1), KeepBoth)
	check("two removes", Merge(removeA1, removeNone), KeepBoth)
	// The plan's case: two adds of one member from two actors with equal
	// at, one removed, leave the member present: b1 survives, merged from
	// either side.
	if Merge(b1, removeA1) == TakeRemote || Merge(removeA1, b1) == KeepLocal {
		t.Error("the member removed though an add of it was not seen")
	}
	// A merge makes the same rows whichever side merges: every pair of a
	// set's changes is answered from the two sides as mirrors, keep both
	// both ways, or take remote one way and keep local the other.
	removeB1 := row("ticket", "labels", OpRemove, t2, "a", "v", 0)
	removeB1.Prior = EncodeTags([]Tag{TagOf(b1)})
	removeBoth := row("ticket", "labels", OpRemove, t2, "c", "u", 0)
	removeBoth.Prior = EncodeTags([]Tag{TagOf(a1), TagOf(b1)})
	set := []struct {
		name string
		c    ChangeRow
	}{{"a1", a1}, {"b1", b1}, {"remove a1", removeA1}, {"remove b1", removeB1}, {"remove both", removeBoth}, {"remove none", removeNone}}
	mirror := map[Winner]Winner{KeepBoth: KeepBoth, TakeRemote: KeepLocal, KeepLocal: TakeRemote}
	for i, x := range set {
		for j, y := range set {
			if i == j {
				continue
			}
			if xy, yx := Merge(x.c, y.c), Merge(y.c, x.c); yx != mirror[xy] {
				t.Errorf("Merge(%s, %s) = %v but Merge(%s, %s) = %v", x.name, y.name, xy, y.name, x.name, yx)
			}
		}
	}
	// An unknown field is kept as it is.
	check("unknown", Merge(row("ticket", "nothing", OpSet, t1, "a", "x", 0), row("ticket", "nothing", OpSet, t2, "b", "y", 0)), KeepLocal)
	for _, w := range []Winner{KeepLocal, TakeRemote, KeepBoth, Winner(7)} {
		if w.String() == "" {
			t.Errorf("no words for %d", int(w))
		}
	}
}

func TestTagsRoundTrip(t *testing.T) {
	tags := []Tag{{"a", "x", 0}, {"b", "y", 3}}
	if got := Tags(EncodeTags(tags)); len(got) != 2 || got[0] != tags[0] || got[1] != tags[1] {
		t.Fatalf("round trip: %+v", got)
	}
	if got := Tags(""); got != nil {
		t.Fatalf("no prior: %+v", got)
	}
	if got := Tags("garbage"); got != nil {
		t.Fatalf("garbage: %+v", got)
	}
	if EncodeTags(nil) != "[]" {
		t.Fatalf("no tags: %q", EncodeTags(nil))
	}
	c := ChangeRow{Actor: "a", OpID: "x", N: 2}
	if TagOf(c) != (Tag{"a", "x", 2}) {
		t.Fatalf("TagOf: %+v", TagOf(c))
	}
}

// edge is an edge change at second sec by actor, for the reconcile tests.
func edge(sec int, actor, kind, op, from, to string) EdgeChange {
	return EdgeChange{At: time.Date(2026, 10, 28, 12, 0, sec, 0, time.UTC), Actor: actor, OpID: fmt.Sprintf("%s%d", actor, sec), Kind: kind, Op: op, From: from, To: to}
}

func parent(sec int, actor, child, parent string) EdgeChange {
	return edge(sec, actor, EdgeParent, OpSet, child, parent)
}

func blocks(sec int, actor, from, to string) EdgeChange {
	return edge(sec, actor, EdgeBlocks, OpAdd, from, to)
}

func unblocks(sec int, actor string, add EdgeChange) EdgeChange {
	e := edge(sec, actor, EdgeBlocks, OpRemove, add.From, add.To)
	e.Tags = []Tag{add.Tag()}
	return e
}

func link(sec int, actor, ticket, run string) EdgeChange {
	return edge(sec, actor, EdgeRun, OpAdd, ticket, run)
}

func unlink(sec int, actor string, add EdgeChange) EdgeChange {
	e := edge(sec, actor, EdgeRun, OpRemove, add.From, add.To)
	e.Tags = []Tag{add.Tag()}
	return e
}

func tombstone(sec int, actor, ticket string, deleted bool) EdgeChange {
	e := edge(sec, actor, EdgeTombstone, OpSet, ticket, "")
	e.Deleted = deleted
	return e
}

// refusedNames is the refused edges as "from→to" in order.
func refusedNames(refused []EdgeChange) string {
	var out []string
	for _, e := range refused {
		out = append(out, e.From+"→"+e.To)
	}
	return strings.Join(out, " ")
}

// reversed is changes in the other order.
func reversed(changes []EdgeChange) []EdgeChange {
	out := make([]EdgeChange, len(changes))
	for i, e := range changes {
		out[len(changes)-1-i] = e
	}
	return out
}

// both runs Reconcile on the log in the order given and reversed, and
// fails unless both refuse what is wanted.
func both(t *testing.T, name string, log []EdgeChange, want string) {
	t.Helper()
	for _, l := range [][]EdgeChange{log, reversed(log)} {
		if got := refusedNames(Reconcile(l)); got != want {
			t.Errorf("%s: refused %q, want %q", name, got, want)
		}
	}
}

func TestReconcileParentSets(t *testing.T) {
	// Two concurrent sets that make a 2-cycle: the later refused, from
	// either replica's log in either order.
	both(t, "a 2-cycle", []EdgeChange{parent(1, "a", "A", "B"), parent(2, "b", "B", "A")}, "B→A")
	both(t, "a 2-cycle the other way", []EdgeChange{parent(2, "a", "A", "B"), parent(1, "b", "B", "A")}, "A→B")
	// Three that make a 3-cycle.
	both(t, "a 3-cycle", []EdgeChange{parent(1, "a", "A", "B"), parent(2, "b", "B", "C"), parent(3, "a", "C", "A")}, "C→A")
	// A chain of 9: the set that makes the ninth is refused; 8 stands.
	var chain []EdgeChange
	for i := 2; i <= 9; i++ {
		chain = append(chain, parent(i, "a", fmt.Sprintf("T%d", i), fmt.Sprintf("T%d", i-1)))
	}
	both(t, "a chain of 9", chain, "T9→T8")
	both(t, "a chain of 8", chain[:7], "")
	// A subtree of height 4 moved under a chain of 5 is refused, under a
	// chain of 4 allowed.
	var log []EdgeChange
	for i := 2; i <= 5; i++ {
		log = append(log, parent(i, "a", fmt.Sprintf("C%d", i), fmt.Sprintf("C%d", i-1)))
	}
	for i := 2; i <= 4; i++ {
		log = append(log, parent(10+i, "b", fmt.Sprintf("S%d", i), fmt.Sprintf("S%d", i-1)))
	}
	both(t, "a subtree of 4 under a chain of 5", append(append([]EdgeChange(nil), log...), parent(20, "a", "S1", "C5")), "S1→C5")
	both(t, "a subtree of 4 under a chain of 4", append(append([]EdgeChange(nil), log...), parent(20, "a", "S1", "C4")), "")
	// A set that makes a cycle through a longer chain, and a ticket set
	// under itself.
	both(t, "a cycle through the chain", append(append([]EdgeChange(nil), log...), parent(20, "a", "C1", "C5")), "C1→C5")
	both(t, "under itself", []EdgeChange{parent(1, "a", "A", "A")}, "A→A")
	// A clear is always admitted, and frees what it held.
	both(t, "a clear", []EdgeChange{parent(1, "a", "A", "B"), parent(2, "a", "A", ""), parent(3, "b", "B", "A")}, "")
	// A refused set stays refused: a later clear of the other side does
	// not admit it after the fact (serial).
	both(t, "serial", []EdgeChange{parent(1, "a", "A", "B"), parent(2, "b", "B", "A"), parent(3, "a", "A", "")}, "B→A")
	// A reparent refused on a ticket that has a parent leaves it with
	// none, as its repair clears parent_id: B→A replaced B→C under the
	// scalar rule, so C→B, a cycle only with B→C, stands.
	both(t, "a reparent refused on a ticket with a parent", []EdgeChange{parent(1, "a", "B", "C"), parent(2, "b", "A", "B"), parent(3, "a", "B", "A"), parent(4, "b", "C", "B")}, "B→A")
	// The parent cleared counts for nothing in a chain either: T5's
	// reparent under T7 is refused, which leaves T5 a root and T8 4 deep,
	// so a ticket set under T8 is 5 deep and stands.
	cut := append(append([]EdgeChange(nil), chain[:7]...), parent(20, "b", "T5", "T7"), parent(21, "a", "Y", "T8"))
	both(t, "a chain cut by a refused reparent", cut, "T5→T7")
	// The input is left as it was.
	in := []EdgeChange{parent(2, "b", "B", "A"), parent(1, "a", "A", "B")}
	Reconcile(in)
	if in[0].From != "B" {
		t.Fatal("Reconcile reordered its input")
	}
}

func TestReconcileBlocks(t *testing.T) {
	// Two blocks that make a cycle: the later refused, either order.
	both(t, "a 2-cycle", []EdgeChange{blocks(1, "a", "A", "B"), blocks(2, "b", "B", "A")}, "B→A")
	both(t, "a 3-cycle", []EdgeChange{blocks(1, "a", "A", "B"), blocks(2, "b", "B", "C"), blocks(3, "a", "C", "A")}, "C→A")
	both(t, "a self edge", []EdgeChange{blocks(1, "a", "A", "A")}, "A→A")
	// The four edges of the field rules: B→A, B→C, A→B, C→A in log order
	// leave B→A, B→C and C→A and refuse A→B, delivered whole, as {1, 3}
	// then {2, 4}, and as {2, 3, 4} then {1}.
	e := []EdgeChange{blocks(1, "a", "B", "A"), blocks(2, "b", "B", "C"), blocks(3, "a", "A", "B"), blocks(4, "b", "C", "A")}
	deliveries := map[string][][]EdgeChange{
		"whole":              {e},
		"{1, 3} then {2, 4}": {{e[0], e[2]}, {e[1], e[3]}},
		"{2, 3, 4} then {1}": {{e[1], e[2], e[3]}, {e[0]}},
	}
	for name, batches := range deliveries {
		var log []EdgeChange
		for _, b := range batches {
			log = append(log, b...)
			// After every batch the answer is a function of the log so far.
			if got := refusedNames(Reconcile(log)); len(log) == 4 && got != "A→B" {
				t.Errorf("%s: refused %q after the whole log", name, got)
			}
		}
	}
	// A repair made against each batch's state would differ: the edges a
	// batch admitted taken as the state the next batch meets (as if they
	// came first), {2, 3, 4} refuses C→A, and then {1} refuses B→A.
	stateRepair := func(batches [][]EdgeChange) string {
		var state []EdgeChange
		var refused []string
		for _, batch := range batches {
			log := append(append([]EdgeChange(nil), state...), batch...)
			out := Reconcile(log)
			refused = append(refused, refusedNames(out))
			for _, e := range batch {
				if !hasTag(tagsOf(out), e.Tag()) {
					e.At = time.Time{} // admitted: state, before anything that follows
					state = append(state, e)
				}
			}
		}
		return strings.TrimSpace(strings.Join(refused, " "))
	}
	if got := stateRepair(deliveries["{2, 3, 4} then {1}"]); got != "C→A B→A" {
		t.Errorf("a repair against each batch's state refused %q", got)
	}
	if got := stateRepair(deliveries["whole"]); got != "A→B" {
		t.Errorf("the whole log against no state refused %q", got)
	}
	// An edge refused after one batch and admitted once an earlier remove
	// arrives: {A→B at 1, B→A at 3} refuses B→A; the remove of A→B at 2
	// arriving admits it, restored with a second repair row.
	add := blocks(1, "a", "A", "B")
	batch1 := []EdgeChange{add, blocks(3, "b", "B", "A")}
	if got := refusedNames(Reconcile(batch1)); got != "B→A" {
		t.Fatalf("before the remove: %q", got)
	}
	log := append(append([]EdgeChange(nil), batch1...), unblocks(2, "a", add))
	both(t, "after the remove", log, "")
	refusedRow := Repair(batch1[1], false, time.Now(), 0)
	restoredRow := Repair(batch1[1], true, time.Now(), 1)
	if refusedRow.Actor != ActorMerge || restoredRow.Actor != ActorMerge || refusedRow.OpID != restoredRow.OpID || refusedRow.N == restoredRow.N {
		t.Fatalf("the repair rows: %+v %+v", refusedRow, restoredRow)
	}
	// The reverse: an edge admitted after one batch and refused once an
	// earlier add arrives.
	if got := refusedNames(Reconcile([]EdgeChange{blocks(3, "b", "B", "A")})); got != "" {
		t.Fatalf("alone: %q", got)
	}
	both(t, "an earlier add arrives", []EdgeChange{blocks(3, "b", "B", "A"), blocks(1, "a", "A", "B")}, "B→A")
	// Two adds of one member are two tags, both needed gone: a remove
	// naming one leaves the edge, and the reverse edge is still refused.
	add2 := blocks(2, "b", "A", "B")
	both(t, "two tags, one removed", []EdgeChange{add, add2, unblocks(3, "a", add), blocks(4, "b", "B", "A")}, "B→A")
	both(t, "two tags, both removed", []EdgeChange{add, add2, unblocks(3, "a", add), unblocks(4, "b", add2), blocks(5, "b", "B", "A")}, "")
	// A remove of every tag names them all.
	all := edge(3, "a", EdgeBlocks, OpRemove, "A", "B")
	all.Tags = []Tag{add.Tag(), add2.Tag()}
	both(t, "a remove of every tag", []EdgeChange{add, add2, all, blocks(4, "b", "B", "A")}, "")
	// A remove naming no tag removes nothing, as Merge and the set rule
	// read it: the edge stands, so the reverse edge is refused.
	none := edge(3, "a", EdgeBlocks, OpRemove, "A", "B")
	both(t, "a remove naming no tag", []EdgeChange{add, none, blocks(4, "b", "B", "A")}, "B→A")
	addRow := ChangeRow{Entity: EntityTicket, Field: "relations", Op: OpAdd, At: add.At, Actor: add.Actor, OpID: add.OpID, N: add.N}
	noneRow := ChangeRow{Entity: EntityTicket, Field: "relations", Op: OpRemove, At: none.At, Actor: none.Actor, OpID: none.OpID, N: none.N, Prior: EncodeTags(none.Tags)}
	if Merge(addRow, noneRow) == TakeRemote || Merge(noneRow, addRow) == KeepLocal {
		t.Error("Merge and Reconcile disagree on a remove naming no tag: Merge removes the add")
	}
	// A remove that sorts before the add it names (its writer's clock
	// behind the adder's) removes it all the same, as the set rule does
	// whatever the clocks say: the add never stands, and the reverse edge
	// is admitted.
	late := blocks(20, "b", "A", "B")
	early := unblocks(10, "a", late)
	both(t, "a remove before its add", []EdgeChange{early, late, blocks(30, "a", "B", "A")}, "")
	// Only the member it names: the same tag on another member's add is
	// not removed.
	elsewhere := unblocks(10, "a", late)
	elsewhere.From, elsewhere.To = "C", "D"
	both(t, "a remove of another member", []EdgeChange{elsewhere, late, blocks(30, "a", "B", "A")}, "B→A")
	// A refused add stays refused when the edge that refused it goes
	// later (serial), and a remove of a refused add changes nothing.
	both(t, "serial", []EdgeChange{add, blocks(2, "b", "B", "A"), unblocks(3, "a", add)}, "B→A")
}

func tagsOf(changes []EdgeChange) []Tag {
	var out []Tag
	for _, e := range changes {
		out = append(out, e.Tag())
	}
	return out
}

func TestReconcileRunLinks(t *testing.T) {
	// Two run links of one run on two tickets: the later refused.
	both(t, "two tickets", []EdgeChange{link(1, "a", "T1", "R"), link(2, "b", "T2", "R")}, "T2→R")
	both(t, "two tickets the other way", []EdgeChange{link(2, "a", "T1", "R"), link(1, "b", "T2", "R")}, "T1→R")
	// Two runs, no conflict; one ticket linking its run twice, two tags.
	both(t, "two runs", []EdgeChange{link(1, "a", "T1", "R1"), link(2, "b", "T2", "R2")}, "")
	both(t, "one ticket twice", []EdgeChange{link(1, "a", "T1", "R"), link(2, "b", "T1", "R")}, "")
	// A link removed frees the run; a refused link stays refused when
	// the holder's link goes later (serial).
	first := link(1, "a", "T1", "R")
	both(t, "removed then taken", []EdgeChange{first, unlink(2, "a", first), link(3, "b", "T2", "R")}, "")
	both(t, "taken then removed", []EdgeChange{first, link(2, "b", "T2", "R"), unlink(3, "a", first)}, "T2→R")
	// A tombstone holds no run: a deleted ticket's link lets another
	// ticket take the run, and the undelete while the other holds it
	// leaves the undeleted ticket's link refused.
	both(t, "deleted then taken", []EdgeChange{first, tombstone(2, "a", "T1", true), link(3, "b", "T2", "R")}, "")
	both(t, "undeleted while held", []EdgeChange{first, tombstone(2, "a", "T1", true), link(3, "b", "T2", "R"), tombstone(4, "a", "T1", false)}, "T1→R")
	// Undeleted while nobody holds it: the link holds the run again.
	both(t, "undeleted, free", []EdgeChange{first, tombstone(2, "a", "T1", true), tombstone(3, "a", "T1", false), link(4, "b", "T2", "R")}, "T2→R")
	second := link(3, "b", "T2", "R")
	both(t, "undeleted after the other let go", []EdgeChange{first, tombstone(2, "a", "T1", true), second, unlink(4, "b", second), tombstone(5, "a", "T1", false), link(6, "b", "T3", "R")}, "T3→R")
	// A link added to a tombstone (its writer had not seen the delete)
	// holds no run while the ticket is deleted, so the live ticket holding
	// the run then does not refuse it: it stands, inactive.
	taken := link(2, "b", "T2", "R")
	inactive := link(3, "a", "T1", "R")
	both(t, "a link added to a tombstone", []EdgeChange{tombstone(1, "b", "T1", true), taken, inactive}, "")
	// It comes back with the undelete, and holds the run when no other
	// live ticket does at that moment.
	both(t, "a link added to a tombstone, the run free at the undelete", []EdgeChange{tombstone(1, "b", "T1", true), taken, inactive, unlink(4, "b", taken), tombstone(5, "b", "T1", false), link(6, "b", "T3", "R")}, "T3→R")
	// Held by another live ticket at the undelete, it is refused there.
	both(t, "a link added to a tombstone, the run held at the undelete", []EdgeChange{tombstone(1, "b", "T1", true), taken, inactive, tombstone(5, "b", "T1", false)}, "T1→R")
	// A remove naming no tag removes nothing: the run stays held.
	none := edge(2, "a", EdgeRun, OpRemove, "T1", "R")
	both(t, "a remove naming no tag", []EdgeChange{first, none, link(3, "b", "T2", "R")}, "T2→R")
	// A remove that sorts before the link it names removes it all the
	// same: the link never holds the run, and another ticket takes it.
	late := link(20, "a", "T1", "R")
	early := unlink(10, "b", late)
	both(t, "a remove before its link", []EdgeChange{early, late, link(30, "b", "T2", "R")}, "")
	// Every refused edge of the undelete case is the link, the edge.
	out := Reconcile([]EdgeChange{first, tombstone(2, "a", "T1", true), second, tombstone(4, "a", "T1", false)})
	if len(out) != 1 || out[0].Kind != EdgeRun || out[0].Tag() != first.Tag() {
		t.Fatalf("the undelete's refusal: %+v", out)
	}
}

func TestRepairRowsCarryTheMergeActor(t *testing.T) {
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		e     EdgeChange
		field string
	}{
		{parent(1, "a", "A", "B"), "parent_id"},
		{blocks(2, "b", "A", "B"), "relations"},
		{link(3, "a", "T1", "R"), "links"},
	} {
		for n, admitted := range []bool{false, true} {
			r := Repair(c.e, admitted, at, n)
			var tag Tag
			if err := json.Unmarshal([]byte(r.OpID), &tag); err != nil || tag != c.e.Tag() {
				t.Fatalf("the repair row's op id %q is not the edge's tag: %+v %v", r.OpID, tag, err)
			}
			if r.Actor != ActorMerge || r.Op != OpReconcile || r.Entity != EntityTicket || r.EntityID != c.e.From || r.TicketID != c.e.From ||
				r.Field != c.field || r.N != n || !r.At.Equal(at) || r.Prior != "" {
				t.Fatalf("the repair row of %+v: %+v", c.e, r)
			}
			var value struct {
				Kind, From, To string
				Tag            Tag
				Admitted       bool
			}
			if err := json.Unmarshal([]byte(r.Value), &value); err != nil || value.Kind != c.e.Kind || value.From != c.e.From || value.To != c.e.To || value.Tag != c.e.Tag() || value.Admitted != admitted {
				t.Fatalf("the repair row's value: %s %v", r.Value, err)
			}
			if Class(r.Entity, r.Field) == "" {
				t.Fatalf("the repair row's field %s has no class", r.Field)
			}
		}
	}
}

// Two edges whose actor and op id differ only in where a '.' falls (an
// op id may hold one, and so may a crew member's name in an attempt's
// actor) get two repair identities: a join of the parts with '.' would
// give both agent:a.b.c.0, and the second repair row would trip the log's
// UNIQUE(actor, op_id, n).
func TestRepairIdentitiesNeverCollide(t *testing.T) {
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	a := blocks(1, "agent:a.b", "A", "B")
	a.OpID = "c"
	b := blocks(1, "agent:a", "C", "D")
	b.OpID = "b.c"
	ra, rb := Repair(a, false, at, 0), Repair(b, false, at, 0)
	if TagOf(ra) == TagOf(rb) {
		t.Fatalf("two edges' repairs share the identity %+v", TagOf(ra))
	}
	// In a store's log, both rows are kept.
	s := openStore(t, storeDir(t))
	for _, r := range []ChangeRow{ra, rb} {
		if _, err := s.db.Exec(`INSERT INTO changes (entity, entity_id, ticket_id, field, op, value, at, actor, op_id, n) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Entity, r.EntityID, r.TicketID, r.Field, r.Op, r.Value, stamp(r.At), r.Actor, r.OpID, r.N); err != nil {
			t.Fatalf("the repair row of %s: %v", r.EntityID, err)
		}
	}
}

// Two replicas' logs merged come out in the one order, the same from
// either side: at, then actor, then op_id, then n.
func TestTheOneOrderOfTheLog(t *testing.T) {
	t1 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Nanosecond)
	a := []ChangeRow{row("ticket", "title", OpSet, t2, "a", "x", 0), row("ticket", "labels", OpAdd, t1, "a", "y", 1), row("ticket", "labels", OpAdd, t1, "a", "y", 0), row("ticket", "title", OpSet, t1, "a", "z", 0)}
	b := []ChangeRow{row("ticket", "title", OpSet, t1, "b", "x", 0), row("ticket", "owner", OpSet, t2, "b", "w", 0), row("ticket", "priority", OpSet, t1, "a", "y", 2)}
	key := func(rows []ChangeRow) string {
		var out []string
		for _, r := range rows {
			out = append(out, fmt.Sprintf("%s/%s/%d@%d", r.Actor, r.OpID, r.N, r.At.Sub(t1)))
		}
		return strings.Join(out, " ")
	}
	ab := append(append([]ChangeRow(nil), a...), b...)
	ba := append(append([]ChangeRow(nil), b...), a...)
	Order(ab)
	Order(ba)
	want := "a/y/0@0 a/y/1@0 a/y/2@0 a/z/0@0 b/x/0@0 a/x/0@1 b/w/0@1"
	if key(ab) != want || key(ba) != want {
		t.Fatalf("the one order:\n ab %s\n ba %s\nwant %s", key(ab), key(ba), want)
	}
	if !Less(ab[0], ab[1]) || Less(ab[1], ab[0]) || Less(ab[0], ab[0]) {
		t.Fatal("Less is not a strict order")
	}
}
