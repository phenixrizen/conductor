package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// mustDone fails the test unless the operation was done, and answers its
// receipt.
func mustDone(t *testing.T, what string, rc Receipt, err error) Receipt {
	t.Helper()
	if err != nil || rc.Status != StatusDone {
		t.Fatalf("%s: %+v %v", what, rc, err)
	}
	return rc
}

// listKeys reads a list's page and answers its tickets' keys.
func listKeys(t *testing.T, s *Store, q Query) []string {
	t.Helper()
	page, err := s.List(context.Background(), q)
	if err != nil {
		t.Fatalf("list %+v: %v", q, err)
	}
	keys := []string{}
	for _, ts := range page.Tickets {
		keys = append(keys, ts.Key)
	}
	if page.Total != len(keys) || page.Next != "" {
		t.Fatalf("list %+v: %d of %d on one page, next %q", q, len(keys), page.Total, page.Next)
	}
	return keys
}

// seededList is a seeded set of tickets the filters are read against:
// two projects, CND's labels and milestones by name.
type seededList struct {
	s          *Store
	labels     map[string]string
	milestones map[string]string
}

// seedList makes the seeded set: in CND (whose policy closes at once)
//
//	CND-1  Login page   backlog      p0 api     Alice person:Alice          now   m1
//	CND-2  Logout flow  ready        p2 ui      Bob   agent:claude          next
//	CND-3  Search box   in_progress  p3 api,ui  Alice person:Bob,agent:codex later m2
//	CND-4  Settings     in_review    p4                                              parent CND-1
//	CND-5  Billing      done         p1 db†     Carol                             m3† parent CND-1
//	CND-6  Reports      cancelled    p2 api     Bob                         now
//	CND-7  Export       backlog      p3 ui      Alice                             blocked by CND-3
//	CND-8  import       ready        p0                                           blocked by CND-5 (done)
//	CND-9  Archive      backlog      deleted                                      blocks CND-10
//	CND-10 Audit        ready        p2
//
// and OPS-1 Login page, backlog, Alice. † a tombstone a merge left
// referenced: the label db and the milestone m3 are deleted, and the
// rows naming them are written back as another replica's would be, so
// they read as absent and none.
func seedList(t *testing.T) seededList {
	t.Helper()
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	cnd := newOpenProject(t, s, "CND")
	newOpenProject(t, s, "OPS")
	sl := seededList{s: s, labels: map[string]string{}, milestones: map[string]string{}}
	for _, name := range []string{"api", "ui", "db"} {
		rc, err := s.CreateLabel(ctx, testOp("label-"+name), "CND", NewLabel{Name: name, Color: "blue"})
		sl.labels[name] = mustDone(t, "label "+name, rc, err).ID
	}
	for _, m := range []NewMilestone{{Name: "m1", Target: "2026-11-01"}, {Name: "m2"}, {Name: "m3", Target: "2026-12-01"}} {
		rc, err := s.CreateMilestone(ctx, testOp("milestone-"+m.Name), "CND", m)
		sl.milestones[m.Name] = mustDone(t, "milestone "+m.Name, rc, err).ID
	}
	l, m := sl.labels, sl.milestones
	for _, n := range []NewTicket{
		{Project: "CND", Title: "Login page", Status: TicketBacklog, Labels: []string{l["api"]}, Owner: "Alice", Assignees: []string{"person:Alice"}, Horizon: HorizonNow, Milestone: m["m1"]},
		{Project: "CND", Title: "Logout flow", Status: TicketReady, Priority: 2, Labels: []string{l["ui"]}, Owner: "Bob", Assignees: []string{"agent:claude"}, Horizon: HorizonNext},
		{Project: "CND", Title: "Search box", Status: TicketInProgress, Priority: 3, Labels: []string{l["ui"], l["api"]}, Owner: "Alice",
			Assignees: []string{"person:Bob", "agent:codex"}, Horizon: HorizonLater, Milestone: m["m2"]},
		{Project: "CND", Title: "Settings", Status: TicketInReview, Priority: 4, Parent: "CND-1"},
		{Project: "CND", Title: "Billing", Status: TicketDone, Priority: 1, Labels: []string{l["db"]}, Owner: "Carol", Milestone: m["m3"], Parent: "CND-1"},
		{Project: "CND", Title: "Reports", Status: TicketCancelled, Priority: 2, Labels: []string{l["api"]}, Owner: "Bob", Horizon: HorizonNow},
		{Project: "CND", Title: "Export", Status: TicketBacklog, Priority: 3, Labels: []string{l["ui"]}, Owner: "Alice"},
		{Project: "CND", Title: "import", Status: TicketReady},
		{Project: "CND", Title: "Archive", Status: TicketBacklog},
		{Project: "CND", Title: "Audit", Status: TicketReady, Priority: 2},
		{Project: "OPS", Title: "Login page", Status: TicketBacklog, Owner: "Alice"},
	} {
		makeTicket(t, s, n)
	}
	for i, r := range [][2]string{{"CND-3", "CND-7"}, {"CND-5", "CND-8"}, {"CND-9", "CND-10"}} {
		rc, err := s.AddRelation(ctx, testOp(opID("blocks-", i)), r[0], RelationBlocks, r[1])
		mustDone(t, r[0]+" blocks "+r[1], rc, err)
	}
	rc, err := s.DeleteTicket(ctx, testOp("delete-9"), "CND-9")
	mustDone(t, "delete CND-9", rc, err)
	rc, err = s.DeleteLabel(ctx, testOp("delete-db"), "CND", l["db"])
	mustDone(t, "delete db", rc, err)
	rc, err = s.DeleteMilestone(ctx, testOp("delete-m3"), "CND", m["m3"])
	mustDone(t, "delete m3", rc, err)
	// What a merge can leave: CND-5 naming the tombstones.
	id5 := ticketOf(t, s, "CND-5").ID
	if _, err := s.db.Exec("INSERT INTO ticket_labels (ticket_id, label_id) VALUES (?, ?)", id5, l["db"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE tickets SET milestone_id = ? WHERE id = ?", m["m3"], id5); err != nil {
		t.Fatal(err)
	}
	_ = cnd
	return sl
}

func TestEachFilterAloneAndThreeTogether(t *testing.T) {
	sl := seedList(t)
	l, m := sl.labels, sl.milestones
	cnd := func(f Filter) Query { f.Sort = SortKey; return Query{Project: "CND", Filter: f} }
	all := func(f Filter) Query { f.Sort = SortKey; return Query{Filter: f} }
	for _, c := range []struct {
		name string
		q    Query
		want string
	}{
		{"the project", cnd(Filter{}), "CND-1 CND-2 CND-3 CND-4 CND-5 CND-6 CND-7 CND-8 CND-10"},
		{"every project", all(Filter{}), "CND-1 CND-2 CND-3 CND-4 CND-5 CND-6 CND-7 CND-8 CND-10 OPS-1"},
		{"a status", cnd(Filter{Status: []string{TicketBacklog}}), "CND-1 CND-7"},
		{"a status of every project", all(Filter{Status: []string{TicketBacklog}}), "CND-1 CND-7 OPS-1"},
		{"statuses", cnd(Filter{Status: []string{TicketDone, TicketCancelled}}), "CND-5 CND-6"},
		{"priorities", cnd(Filter{Priority: []int{3, 4}}), "CND-3 CND-4 CND-7"},
		{"a label", cnd(Filter{Labels: []string{l["api"]}}), "CND-1 CND-3 CND-6"},
		{"labels", cnd(Filter{Labels: []string{l["api"], l["ui"]}}), "CND-1 CND-2 CND-3 CND-6 CND-7"},
		{"a tombstoned label", cnd(Filter{Labels: []string{l["db"]}}), ""},
		{"a label of no row", cnd(Filter{Labels: []string{newID()}}), ""},
		{"the owner", cnd(Filter{Owner: "Alice"}), "CND-1 CND-3 CND-7"},
		{"an agent assignee", cnd(Filter{Assignees: []string{"agent:claude"}}), "CND-2"},
		{"assignees", cnd(Filter{Assignees: []string{"person:Alice", "agent:codex"}}), "CND-1 CND-3"},
		{"a milestone", cnd(Filter{Milestone: m["m1"]}), "CND-1"},
		{"a milestone with no target", cnd(Filter{Milestone: m["m2"]}), "CND-3"},
		{"a tombstoned milestone", cnd(Filter{Milestone: m["m3"]}), ""},
		{"a horizon", cnd(Filter{Horizon: []string{HorizonNow}}), "CND-1 CND-6"},
		{"not planned", cnd(Filter{Horizon: []string{HorizonNone}}), "CND-4 CND-5 CND-7 CND-8 CND-10"},
		{"the parent", cnd(Filter{Parent: "CND-1"}), "CND-4 CND-5"},
		{"a parent of no ticket", cnd(Filter{Parent: "CND-99"}), ""},
		{"blocked", cnd(Filter{Blocked: true}), "CND-7"},
		{"a search", cnd(Filter{Q: "login"}), "CND-1"},
		{"a search of every project", all(Filter{Q: "login"}), "CND-1 OPS-1"},
		{"a search of white space alone", cnd(Filter{Q: " \t "}), "CND-1 CND-2 CND-3 CND-4 CND-5 CND-6 CND-7 CND-8 CND-10"},
		{"the tombstones asked for", cnd(Filter{Deleted: true}), "CND-1 CND-2 CND-3 CND-4 CND-5 CND-6 CND-7 CND-8 CND-9 CND-10"},
		{"a project of no row", Query{Project: "NONE"}, ""},
		{"three: statuses, the owner, a label", cnd(Filter{Status: []string{TicketBacklog, TicketInProgress}, Owner: "Alice", Labels: []string{l["ui"]}}), "CND-3 CND-7"},
		{"three: priorities, horizons, a label", cnd(Filter{Priority: []int{2, 3}, Horizon: []string{HorizonNow, HorizonLater}, Labels: []string{l["api"]}}), "CND-3 CND-6"},
		{"three: blocked, a status, a search", cnd(Filter{Blocked: true, Status: []string{TicketBacklog}, Q: "export"}), "CND-7"},
	} {
		if got := strings.Join(listKeys(t, sl.s, c.q), " "); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestASummaryIsTheTicketWithoutItsDescription(t *testing.T) {
	sl := seedList(t)
	page, err := sl.s.List(context.Background(), Query{Project: "CND", Filter: Filter{Sort: SortKey}})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]TicketSummary{}
	for _, ts := range page.Tickets {
		by[ts.Key] = ts
	}
	c3 := by["CND-3"]
	if c3.Title != "Search box" || c3.Status != TicketInProgress || c3.Priority != 3 || c3.Horizon != HorizonLater || c3.Owner != "Alice" ||
		c3.MilestoneID != sl.milestones["m2"] || c3.Blocked || c3.Number != 3 || c3.Rev == 0 || c3.CreatedAt.IsZero() || c3.UpdatedAt.IsZero() {
		t.Fatalf("CND-3: %+v", c3)
	}
	if strings.Join(c3.Assignees, " ") != "agent:codex person:Bob" || strings.Join(c3.Labels, " ") != sl.labels["api"]+" "+sl.labels["ui"] {
		t.Fatalf("CND-3's sets: %v %v", c3.Assignees, c3.Labels)
	}
	// A tombstoned label reads absent and a tombstoned milestone none; a
	// done blocker blocks nothing, a live open one blocks.
	if c5 := by["CND-5"]; len(c5.Labels) != 0 || c5.MilestoneID != "" || c5.ParentID != ticketOf(t, sl.s, "CND-1").ID || c5.ClosedAt.IsZero() {
		t.Fatalf("CND-5: %+v", c5)
	}
	if !by["CND-7"].Blocked || by["CND-8"].Blocked || by["CND-10"].Blocked {
		t.Fatalf("blocked: CND-7 %v, CND-8 %v, CND-10 %v", by["CND-7"].Blocked, by["CND-8"].Blocked, by["CND-10"].Blocked)
	}
	if len(by["CND-2"].Labels) != 1 || by["CND-4"].Assignees == nil || len(by["CND-4"].Assignees) != 0 {
		t.Fatalf("CND-2's labels %v, CND-4's assignees %#v", by["CND-2"].Labels, by["CND-4"].Assignees)
	}
}

// listRow is what a sort orders a ticket by, read back from the store.
type listRow struct {
	id, key, updated, created, status, title, project string
	number, priority                                  int64
	milestone, target, mname                          string // the live milestone's, "" for none
}

// foldASCII is s as SQLite's NOCASE compares it: ASCII letters folded.
func foldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// sortedRows is the rows in the order the sort gives, its keys compared as
// the plan says and then the id, the whole reversed for desc.
func sortedRows(rows []listRow, sort string, desc bool) []string {
	keyOf := func(r listRow) []any {
		switch sort {
		case SortUpdated:
			return []any{r.updated}
		case SortCreated:
			return []any{r.created}
		case SortPriority:
			return []any{r.priority}
		case SortStatus:
			return []any{int64(slices.Index(Statuses, r.status))}
		case SortTitle:
			return []any{foldASCII(r.title)}
		case SortKey:
			return []any{r.project, r.number}
		case SortMilestone:
			group := int64(2)
			if r.milestone != "" {
				group = 1
				if r.target != "" {
					group = 0
				}
			}
			return []any{group, r.target, foldASCII(r.mname), r.milestone}
		}
		panic(sort)
	}
	cmp := func(a, b listRow) int {
		ka, kb := keyOf(a), keyOf(b)
		for i := range ka {
			switch va := ka[i].(type) {
			case int64:
				if c := va - kb[i].(int64); c != 0 {
					if c < 0 {
						return -1
					}
					return 1
				}
			case string:
				if c := strings.Compare(va, kb[i].(string)); c != 0 {
					return c
				}
			}
		}
		return strings.Compare(a.id, b.id)
	}
	sorted := slices.Clone(rows)
	slices.SortFunc(sorted, cmp)
	if desc {
		slices.Reverse(sorted)
	}
	keys := make([]string, len(sorted))
	for i, r := range sorted {
		keys[i] = r.key
	}
	return keys
}

// listRows reads every live ticket's sort values from the store.
func listRows(t *testing.T, s *Store) []listRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT t.id, t.key, t.updated_at, t.created_at, t.status, t.title, p.key, t.number, t.priority,
			COALESCE(m.id, ''), COALESCE(m.target, ''), COALESCE(m.name, '')
		FROM tickets t JOIN projects p ON p.id = t.project_id LEFT JOIN milestones m ON m.id = t.milestone_id AND m.deleted_at = ''
		WHERE t.deleted_at = ''`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []listRow
	for rows.Next() {
		var r listRow
		if err := rows.Scan(&r.id, &r.key, &r.updated, &r.created, &r.status, &r.title, &r.project, &r.number, &r.priority,
			&r.milestone, &r.target, &r.mname); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestEverySortBothWays(t *testing.T) {
	sl := seedList(t)
	rows := listRows(t, sl.s)
	if len(rows) != 10 {
		t.Fatalf("%d live tickets", len(rows))
	}
	for _, sort := range Sorts {
		for _, desc := range []bool{false, true} {
			got := listKeys(t, sl.s, Query{Filter: Filter{Sort: sort, Desc: desc}})
			if want := sortedRows(rows, sort, desc); !slices.Equal(got, want) {
				t.Errorf("sort %s desc %v:\n got %v\nwant %v", sort, desc, got, want)
			}
		}
	}
	// What the plan's sorts mean, spelled out on the seeded set: the title
	// without regard to case, the key by number, a milestone with a target
	// first and none last, the status in a board's order.
	if got := listKeys(t, sl.s, Query{Project: "CND", Filter: Filter{Sort: SortTitle}}); strings.Join(got, " ") != "CND-10 CND-5 CND-7 CND-8 CND-1 CND-2 CND-6 CND-3 CND-4" {
		t.Errorf("by title: %v", got)
	}
	if got := listKeys(t, sl.s, Query{Project: "CND", Filter: Filter{Sort: SortKey, Desc: true}}); strings.Join(got, " ") != "CND-10 CND-8 CND-7 CND-6 CND-5 CND-4 CND-3 CND-2 CND-1" {
		t.Errorf("by key, descending: %v", got)
	}
	if got := listKeys(t, sl.s, Query{Project: "CND", Filter: Filter{Sort: SortMilestone}}); got[0] != "CND-1" || got[1] != "CND-3" {
		t.Errorf("by milestone: %v", got)
	}
	if got := listKeys(t, sl.s, Query{Project: "CND", Filter: Filter{Sort: SortStatus, Status: []string{TicketCancelled, TicketBacklog, TicketInReview}}}); strings.Join(got, " ") != "CND-1 CND-7 CND-4 CND-6" &&
		strings.Join(got, " ") != "CND-7 CND-1 CND-4 CND-6" {
		t.Errorf("by status: %v", got)
	}
}

// insertPaged writes n tickets of the project with the id as the cursor
// test needs them, past ticket.create's rules: few distinct values per
// sort key, so the id breaks most ties.
func insertPaged(t *testing.T, s *Store, projectID, projectKey string, n int, milestones []string) []listRow {
	t.Helper()
	titles := []string{"alpha", "Alpha", "beta", "Gamma", "gamma", "delta", "Delta", "epsilon", "zeta", "Eta", "theta", "iota", "Kappa"}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var rows []listRow
	for i := range n {
		r := listRow{
			id: newID(), number: int64(i + 1), project: projectKey,
			updated:  fmt.Sprintf("2026-10-%02dT12:00:00.000000000Z", 1+i%10),
			created:  fmt.Sprintf("2026-09-%02dT12:00:00.000000000Z", 1+i%7),
			priority: int64(i % 5), status: Statuses[i%len(Statuses)], title: titles[i%len(titles)],
		}
		r.key = TicketKey(projectKey, r.number)
		milestone := ""
		if j := i % (len(milestones) + 1); j < len(milestones) {
			milestone = milestones[j]
		}
		if _, err := tx.Exec(`INSERT INTO tickets (id, project_id, number, key, title, status, priority, milestone_id, created_at, updated_at, rev)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`, r.id, projectID, r.number, r.key, r.title, r.status, r.priority, milestone, r.created, r.updated); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// The live milestones' values, as the sort reads them.
	for i := range rows {
		var mid, target, name string
		err := s.db.QueryRow(`SELECT m.id, m.target, m.name FROM tickets t JOIN milestones m ON m.id = t.milestone_id AND m.deleted_at = '' WHERE t.id = ?`, rows[i].id).Scan(&mid, &target, &name)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		rows[i].milestone, rows[i].target, rows[i].mname = mid, target, name
	}
	return rows
}

func TestTheCursorPagesAThousandTicketsWithoutARepeatOrAGap(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "PAG")
	ms := []string{insertMilestone(t, s, pid, "Beta", false), insertMilestone(t, s, pid, "alpha", false), insertMilestone(t, s, pid, "beta", false),
		insertMilestone(t, s, pid, "gone", true)}
	if _, err := s.db.Exec("UPDATE milestones SET target = '2026-12-01' WHERE id = ?", ms[0]); err != nil {
		t.Fatal(err)
	}
	// A page of 190 ends inside a run of ties of every sort but the key,
	// whose keys are unique: the updated stamps come in runs of 100, the
	// created ones of 143, the statuses of 167, the priorities and the
	// milestones of 200, the titles (their case folded) of 77 or 154, and
	// no boundary of the pages (190, 380, 570, 760, 950) falls between two
	// runs, so the id breaks the tie at every one.
	const n, size = 1000, 190
	rows := insertPaged(t, s, pid, "PAG", n, ms)
	for _, sort := range Sorts {
		for _, desc := range []bool{false, true} {
			var got []string
			seen := map[string]bool{}
			next, pages := "", 0
			for {
				page, err := s.List(ctx, Query{Project: "PAG", Filter: Filter{Sort: sort, Desc: desc}, Limit: size, Cursor: next})
				if err != nil {
					t.Fatalf("sort %s desc %v, page %d: %v", sort, desc, pages, err)
				}
				if page.Total != n {
					t.Fatalf("sort %s desc %v, page %d: total %d", sort, desc, pages, page.Total)
				}
				for _, ts := range page.Tickets {
					if seen[ts.Key] {
						t.Fatalf("sort %s desc %v: %s again on page %d", sort, desc, ts.Key, pages)
					}
					seen[ts.Key] = true
					got = append(got, ts.Key)
				}
				pages++
				if page.Next == "" {
					break
				}
				if len(page.Tickets) != size {
					t.Fatalf("sort %s desc %v: a page of %d before the last", sort, desc, len(page.Tickets))
				}
				next = page.Next
			}
			if want := sortedRows(rows, sort, desc); !slices.Equal(got, want) {
				t.Fatalf("sort %s desc %v: %d pages, %d tickets, not in the sort's order", sort, desc, pages, len(got))
			}
			if pages != (n+size-1)/size {
				t.Fatalf("sort %s desc %v: %d pages", sort, desc, pages)
			}
		}
	}
	// A cursor is its list's: another sort's, another direction's, or one
	// not the store's is a bad query.
	first, err := s.List(ctx, Query{Project: "PAG", Filter: Filter{Sort: SortTitle}, Limit: 10})
	if err != nil || first.Next == "" {
		t.Fatalf("a first page: %v %q", err, first.Next)
	}
	for _, q := range []Query{
		{Project: "PAG", Filter: Filter{Sort: SortPriority}, Cursor: first.Next},
		{Project: "PAG", Filter: Filter{Sort: SortTitle, Desc: true}, Cursor: first.Next},
		{Project: "PAG", Filter: Filter{Sort: SortTitle}, Cursor: "not a cursor"},
		{Project: "PAG", Filter: Filter{Sort: SortTitle}, Cursor: encodeCursor(cursor{Sort: SortTitle, Keys: []any{int64(1)}, ID: newID()})},
		{Project: "PAG", Filter: Filter{Sort: SortTitle}, Cursor: encodeCursor(cursor{Sort: SortTitle, Keys: []any{"a"}, ID: "nope"})},
		{Project: "PAG", Filter: Filter{Sort: SortKey}, Cursor: encodeCursor(cursor{Sort: SortKey, Keys: []any{"PAG"}, ID: newID()})},
		{Project: "PAG", Cursor: strings.Repeat("A", maxCursor+1)},
	} {
		if _, err := s.List(ctx, q); !errors.Is(err, ErrBadQuery) {
			t.Errorf("cursor %.40q of sort %s desc %v: %v", q.Cursor, q.Sort, q.Desc, err)
		}
	}
}

// seedSearch makes the tickets the search is read against, words chosen
// so that no other ticket holds a word the cases match.
func seedSearch(t *testing.T) *Store {
	t.Helper()
	s := openStore(t, storeDir(t))
	newProject(t, s, "CND")
	for _, title := range []string{
		"Read or write the cache",  // CND-1
		"Cats and dogs",            // CND-2
		"Do not disturb",           // CND-3
		"a:b ratio",                // CND-4
		"(x) marks the spot",       // CND-5
		"Deploy migration",         // CND-6
		"Deploy timeout",           // CND-7
		"Crème brûlée",             // CND-8
		"Quote \"unquoted\" words", // CND-9
		"Plain eleven",             // CND-10
		"Plain eleven again",       // CND-11
		"Twelfth",                  // CND-12
		"Deploy rollback",          // CND-13
		"Comment holder",           // CND-14
	} {
		newTicket(t, s, "CND", title)
	}
	return s
}

func TestASearchIsWordsNeverSyntax(t *testing.T) {
	s := seedSearch(t)
	ctx := context.Background()
	for _, c := range []struct{ q, want string }{
		{"CND-12", "CND-12"},
		{"cnd-12", "CND-12"},
		{"OR", "CND-1"},
		{"AND", "CND-2"},
		{"NOT", "CND-3"},
		{"a:b", "CND-4"},
		{"(x)", "CND-5"},
		{"creme", "CND-8"},
		{`"unquoted"`, "CND-9"},
		{`unquoted"`, "CND-9"},
		{"NEAR(deploy timeout)", ""},
		{"deploy -timeout", "CND-7"},
		{"deploy NOT timeout", ""},
		// A term of no letter or digit is dropped; a search of nothing else
		// is an empty page, and none is an error.
		{`"`, ""},
		{"*", ""},
		{`"*"`, ""},
		{`- : ( ) ^ +`, ""},
		{`" deploy`, "CND-6 CND-7 CND-13"},
		{`deploy *`, "CND-6 CND-7 CND-13"},
		{`"*" timeout`, "CND-7"},
		// The last term is a prefix, the others whole words.
		{"migr", "CND-6"},
		{"deploy migr", "CND-6"},
		{"migr deploy", ""},
		{"plain elev", "CND-10 CND-11"},
		{"eleven plain", "CND-10 CND-11"},
		{"elev plain", ""},
	} {
		page, err := s.List(ctx, Query{Project: "CND", Filter: Filter{Q: c.q, Sort: SortKey}})
		if err != nil {
			t.Errorf("q %q: %v", c.q, err)
			continue
		}
		var keys []string
		for _, ts := range page.Tickets {
			keys = append(keys, ts.Key)
		}
		if got := strings.Join(keys, " "); got != c.want || page.Total != len(keys) {
			t.Errorf("q %q: %q (total %d), want %q", c.q, got, page.Total, c.want)
		}
	}
	// The match expression itself: every term quoted, its quotes doubled.
	for _, c := range []struct {
		text, expr string
		ok         bool
	}{
		{"CND-12", `"CND-12"*`, true},
		{`say "hi" OR`, `"say" """hi""" "OR"*`, true},
		{"a\x00b", `"a" "b"*`, true},
		{"  ", "", true},
		{`" * "*"`, "", false},
		// A term repeated is kept once; the last stays a prefix.
		{"deploy the deploy THE the", `"deploy" "the" "THE" "the"*`, true},
		{"deploy deploy deploy", `"deploy" "deploy"*`, true},
	} {
		if terms, ok := matchTerms(c.text); strings.Join(terms, " ") != c.expr || ok != c.ok {
			t.Errorf("matchTerms(%q) = %q %v, want %q %v", c.text, terms, ok, c.expr, c.ok)
		}
	}
}

func TestADeletedTicketIsSearchedOnlyWhenAsked(t *testing.T) {
	s := seedSearch(t)
	ctx := context.Background()
	// A word of its own entry, one of a comment's, and the two together.
	rc, err := s.AddComment(ctx, testOp("c1"), "CND-13", "a sandpiper waits")
	mustDone(t, "comment", rc, err)
	entries := entriesOf(t, s, "CND-13")
	if len(entries) != 2 {
		t.Fatalf("CND-13's entries: %v", entries)
	}
	rc, err = s.DeleteTicket(ctx, testOp("d1"), "CND-13")
	mustDone(t, "delete", rc, err)
	for _, q := range []string{"rollback", "sandpiper", "rollback sandpiper"} {
		if got := listKeys(t, s, Query{Project: "CND", Filter: Filter{Q: q}}); len(got) != 0 {
			t.Fatalf("a deleted ticket found by %q: %v", q, got)
		}
		if got := listKeys(t, s, Query{Project: "CND", Filter: Filter{Q: q, Deleted: true}}); strings.Join(got, " ") != "CND-13" {
			t.Fatalf("the deleted ticket asked for by %q: %v", q, got)
		}
	}
	// Its entries are kept as they were: a delete writes none.
	if got := entriesOf(t, s, "CND-13"); !slices.Equal(got, entries) {
		t.Fatalf("the deleted ticket's entries: %v, were %v", got, entries)
	}
	rc, err = s.UndeleteTicket(ctx, testOp("u1"), "CND-13")
	mustDone(t, "undelete", rc, err)
	for _, q := range []string{"rollback", "sandpiper", "rollback sandpiper"} {
		if got := listKeys(t, s, Query{Project: "CND", Filter: Filter{Q: q}}); strings.Join(got, " ") != "CND-13" {
			t.Fatalf("the undeleted ticket by %q: %v", q, got)
		}
	}
	if got := entriesOf(t, s, "CND-13"); !slices.Equal(got, entries) {
		t.Fatalf("the undeleted ticket's entries: %v, were %v", got, entries)
	}
	searchEntries(t, s)
}

// rowidsOf reads the one integer column of the rows a query gives.
func rowidsOf(t *testing.T, s *Store, query string, args ...any) []int64 {
	t.Helper()
	rows, err := s.db.Query(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// entriesOf reads the rowids of the search entries of the ticket with
// the key, in order: its own entry's first, then its comments'.
func entriesOf(t *testing.T, s *Store, key string) []int64 {
	t.Helper()
	var rowid int64
	if err := s.db.QueryRow("SELECT rowid FROM tickets WHERE key = ?", key).Scan(&rowid); err != nil {
		t.Fatal(err)
	}
	return rowidsOf(t, s, "SELECT rowid FROM tickets_fts WHERE rowid >= ? AND rowid < ? ORDER BY rowid", rowid<<entryShift, (rowid+1)<<entryShift)
}

// searchEntries checks the search index holds an entry of each ticket's
// own and one of each live comment, by their rowids (entryRowid), and
// nothing else: none of a deleted comment, none left over.
func searchEntries(t *testing.T, s *Store) {
	t.Helper()
	want := rowidsOf(t, s, fmt.Sprintf(`SELECT rowid << %[1]d FROM tickets
		UNION ALL SELECT (t.rowid << %[1]d) + c.rowid FROM comments c JOIN tickets t ON t.id = c.ticket_id WHERE c.deleted_at = ''
		ORDER BY 1`, entryShift))
	if got := rowidsOf(t, s, "SELECT rowid FROM tickets_fts ORDER BY rowid"); !slices.Equal(got, want) {
		t.Fatalf("the search index's entries:\n got %v\nwant %v", got, want)
	}
}

// A search's words are found across a ticket's entries, its own and its
// comments', each word in any of them; a ticket lacking one word anywhere
// is not found, nor are two tickets' words together.
func TestASearchFindsItsWordsAcrossATicketAndItsComments(t *testing.T) {
	s := seedSearch(t)
	ctx := context.Background()
	found := func(q string) string {
		t.Helper()
		return strings.Join(listKeys(t, s, Query{Project: "CND", Filter: Filter{Q: q, Sort: SortKey}}), " ")
	}
	// CND-14 "Comment holder", with two comments of a word each, and a
	// phrase cut between them.
	rc, err := s.AddComment(ctx, testOp("c1"), "CND-14", "a lapwing calls from the marsh")
	mustDone(t, "comment", rc, err)
	rc, err = s.AddComment(ctx, testOp("c2"), "CND-14", "marsh plover nests")
	c2 := mustDone(t, "comment", rc, err).ID
	searchEntries(t, s)
	for _, c := range []struct{ q, want string }{
		// The title's word and one of each comment's, in any order.
		{"holder lapwing plover", "CND-14"},
		{"plover holder lapwing", "CND-14"},
		{"lapwing plover", "CND-14"},
		// The last term a prefix, of a word in any entry.
		{"holder lapwing plov", "CND-14"},
		{"plover lapwing hold", "CND-14"},
		{"lapw", "CND-14"},
		// A word that is in none of its entries: not found.
		{"holder lapwing plover curlew", ""},
		{"lapwing curlew", ""},
		{"curlew holder", ""},
		// A word of another ticket's with one of its own: not found.
		{"cats lapwing", ""},
		{"holder cats", ""},
		// A term of several tokens is a phrase inside one entry: "marsh
		// plover" is the second comment's, "lapwing calls" the first's, and
		// none spans the end of one comment and the start of the next.
		{"marsh-plover", "CND-14"},
		{"lapwing-calls holder", "CND-14"},
		{"marsh-marsh", ""},
		{"holder-lapwing", ""},
	} {
		if got := found(c.q); got != c.want {
			t.Errorf("q %q: %q, want %q", c.q, got, c.want)
		}
	}
	// A deleted comment's words are in no entry: the search that needed
	// them finds nothing, and the words left still find the ticket.
	rc, err = s.DeleteComment(ctx, testOp("c3"), "CND-14", c2)
	mustDone(t, "delete", rc, err)
	for _, c := range []struct{ q, want string }{
		{"holder lapwing plover", ""},
		{"plover", ""},
		{"holder lapwing", "CND-14"},
	} {
		if got := found(c.q); got != c.want {
			t.Errorf("after the delete, q %q: %q, want %q", c.q, got, c.want)
		}
	}
	searchEntries(t, s)
}

// A comment's add, edit and delete rewrite that comment's entry and no
// other: the ticket's own entry and its other comments' are left as they
// are, which the test sees by writing them by hand first with words of
// their own; an edit of the ticket's text rewrites its own entry alone.
func TestACommentsWriteRewritesItsOwnEntryAlone(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	_, key := makeTicket(t, s, NewTicket{Project: "CND", Title: "Heron", Description: "kestrel"})
	rc, err := s.AddComment(ctx, testOp("a"), key, "lapwing")
	a := mustDone(t, "comment a", rc, err).ID
	rc, err = s.AddComment(ctx, testOp("b"), key, "plover")
	b := mustDone(t, "comment b", rc, err).ID
	found := func(q string) bool {
		t.Helper()
		return len(listKeys(t, s, Query{Project: "CND", Filter: Filter{Q: q}})) == 1
	}
	// The ticket's own entry and b's, written by hand with a word the rows
	// do not hold: a rewrite of either from its row would lose the word.
	var ticketRowid, bRowid int64
	if err := s.db.QueryRow("SELECT rowid FROM tickets WHERE key = ?", key).Scan(&ticketRowid); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT rowid FROM comments WHERE id = ?", b).Scan(&bRowid); err != nil {
		t.Fatal(err)
	}
	own, _ := entryRowid(ticketRowid, 0)
	bEntry, _ := entryRowid(ticketRowid, bRowid)
	for _, w := range []struct {
		entry        int64
		column, word string
	}{{own, "title", "sentinelown"}, {bEntry, "body", "sentinelb"}} {
		if _, err := s.db.Exec("DELETE FROM tickets_fts WHERE rowid = ?", w.entry); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("INSERT INTO tickets_fts (rowid, "+w.column+") VALUES (?, ?)", w.entry, w.word); err != nil {
			t.Fatal(err)
		}
	}
	// untouched says the two entries are still the ones written by hand.
	untouched := func(after string) {
		t.Helper()
		for word, want := range map[string]bool{"sentinelown": true, "sentinelb": true, "heron": false, "kestrel": false, "plover": false} {
			if got := found(word); got != want {
				t.Errorf("after %s, %s found %v, want %v", after, word, got, want)
			}
		}
	}
	untouched("the entries written by hand")
	// An add: the new comment's entry, and no other.
	rc, err = s.AddComment(ctx, testOp("c"), key, "curlew")
	c := mustDone(t, "comment c", rc, err).ID
	if !found("curlew") || !found("lapwing") {
		t.Errorf("after the add: curlew %v, lapwing %v", found("curlew"), found("lapwing"))
	}
	untouched("the add")
	// An edit: the comment's new words in, its old ones out, no other entry
	// written.
	rc, err = s.EditComment(ctx, testOp("e"), key, a, "dunlin")
	mustDone(t, "edit a", rc, err)
	if found("lapwing") || !found("dunlin") || !found("curlew") {
		t.Errorf("after the edit: lapwing %v, dunlin %v, curlew %v", found("lapwing"), found("dunlin"), found("curlew"))
	}
	untouched("the edit")
	// A refused edit writes nothing.
	rc, err = s.EditComment(ctx, Op{Actor: "test", ID: "e2", ExpectedRev: 1}, key, a, "redshank")
	if err != nil || rc.Refusal != RefusalStaleRevision {
		t.Fatalf("a stale edit: %+v %v", rc, err)
	}
	if found("redshank") || !found("dunlin") {
		t.Errorf("after a refused edit: redshank %v, dunlin %v", found("redshank"), found("dunlin"))
	}
	untouched("a refused edit")
	// A delete: the comment's entry gone, no other.
	rc, err = s.DeleteComment(ctx, testOp("d"), key, c)
	mustDone(t, "delete c", rc, err)
	if found("curlew") || !found("dunlin") {
		t.Errorf("after the delete: curlew %v, dunlin %v", found("curlew"), found("dunlin"))
	}
	untouched("the delete")
	if got, want := len(entriesOf(t, s, key)), 3; got != want {
		t.Errorf("%d entries after the delete, want the ticket's own, a's and b's", got)
	}
	// An edit of the ticket's description rewrites its own entry from its
	// row, title and all, and leaves its comments' as they are.
	rc, err = edit(s, "t", key, TicketEdit{Set: TicketSet{Description: ptr("merlin")}})
	mustDone(t, "the ticket's edit", rc, err)
	for word, want := range map[string]bool{"sentinelown": false, "heron": true, "merlin": true, "kestrel": false, "sentinelb": true, "plover": false, "dunlin": true} {
		if got := found(word); got != want {
			t.Errorf("after the ticket's edit, %s found %v, want %v", word, got, want)
		}
	}
}

// An entry's rowid is its ticket's rowid shifted, plus its comment's:
// every rowid a store can give fits, and one past what the rowids can
// hold is an error, never an entry over another's.
func TestASearchEntrysRowidHoldsItsTicketAndComment(t *testing.T) {
	for _, c := range []struct {
		ticket, comment, want int64
		ok                    bool
	}{
		{1, 0, 1 << 32, true},
		{2, 5, 2<<32 + 5, true},
		{1<<31 - 1, 1<<32 - 1, 1<<63 - 1, true},
		{1 << 31, 0, 0, false},
		{1, 1 << 32, 0, false},
		{0, 1, 0, false},
		{1, -1, 0, false},
	} {
		got, err := entryRowid(c.ticket, c.comment)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("entryRowid(%d, %d) = %d %v, want %d (ok %v)", c.ticket, c.comment, got, err, c.want, c.ok)
		}
		if err == nil && (got>>entryShift != c.ticket || got&(1<<entryShift-1) != c.comment) {
			t.Errorf("entryRowid(%d, %d) = %d does not give them back", c.ticket, c.comment, got)
		}
	}
	// A comment whose rowid the index cannot hold fails its operation as a
	// failure of the database does: nothing of it is kept, no receipt.
	s := seedSearch(t)
	ctx := context.Background()
	tk := ticketOf(t, s, "CND-14")
	for _, rowid := range []int64{0, 1 << 32} {
		id := newID()
		if _, err := s.db.Exec("INSERT INTO comments (rowid, id, ticket_id, author, by, body, created_at, rev) VALUES (?, ?, ?, 'test', 'Tester', 'teal', ?, 1)",
			rowid, id, tk.ID, stamp(time.Now())); err != nil {
			t.Fatal(err)
		}
		opID := fmt.Sprintf("big%d", rowid)
		if rc, err := s.EditComment(ctx, testOp(opID), "CND-14", id, "wigeon"); err == nil {
			t.Fatalf("an edit of the comment of rowid %d: %+v", rowid, rc)
		}
		var body string
		if err := s.db.QueryRow("SELECT body FROM comments WHERE id = ?", id).Scan(&body); err != nil || body != "teal" {
			t.Fatalf("the comment of rowid %d after the failed edit: %q %v", rowid, body, err)
		}
		if _, found, err := s.Receipt(ctx, "test", opID); found || err != nil {
			t.Fatalf("a receipt of the failed edit: %v %v", found, err)
		}
	}
}

func TestTheSearchEntryIsRewrittenWithItsRows(t *testing.T) {
	s := seedSearch(t)
	ctx := context.Background()
	searchEntries(t, s)
	found := func(q string) string {
		t.Helper()
		return strings.Join(listKeys(t, s, Query{Project: "CND", Filter: Filter{Q: q, Sort: SortKey}}), " ")
	}
	// A comment's words, found once it is added, gone after its delete; an
	// edit's new words found and the old ones gone.
	if got := found("zephyr"); got != "" {
		t.Fatalf("zephyr before any comment: %q", got)
	}
	rc, err := s.AddComment(ctx, testOp("c1"), "CND-14", "a zephyr passes")
	c1 := mustDone(t, "comment", rc, err).ID
	rc, err = s.AddComment(ctx, testOp("c2"), "CND-14", "and a gale")
	c2 := mustDone(t, "comment", rc, err).ID
	if got := found("zephyr"); got != "CND-14" {
		t.Fatalf("zephyr after its comment: %q", got)
	}
	if got := found("zephyr gale"); got != "CND-14" {
		t.Fatalf("two comments' words: %q", got)
	}
	rc, err = s.EditComment(ctx, testOp("c3"), "CND-14", c1, "a breeze passes")
	mustDone(t, "edit", rc, err)
	if got := found("zephyr"); got != "" {
		t.Fatalf("zephyr after its edit: %q", got)
	}
	if got := found("breeze"); got != "CND-14" {
		t.Fatalf("breeze after the edit: %q", got)
	}
	rc, err = s.DeleteComment(ctx, testOp("c4"), "CND-14", c1)
	mustDone(t, "delete", rc, err)
	if got := found("breeze"); got != "" {
		t.Fatalf("breeze after its delete: %q", got)
	}
	if got := found("gale"); got != "CND-14" {
		t.Fatalf("the other comment after a delete: %q", got)
	}
	_ = c2
	// The title, the description and the acceptance criteria.
	rc, err = edit(s, "e1", "CND-6", TicketEdit{Set: TicketSet{Title: ptr("Deploy upgrade"), Description: ptr("kestrel"), Acceptance: ptr("osprey")}})
	mustDone(t, "edit", rc, err)
	for q, want := range map[string]string{"migration": "", "upgrade": "CND-6", "kestrel": "CND-6", "osprey": "CND-6"} {
		if got := found(q); got != want {
			t.Errorf("%s after the edit: %q, want %q", q, got, want)
		}
	}
	// A refused edit leaves the entry as it was.
	rc, err = s.EditTicket(ctx, Op{Actor: "test", ID: "e2", ExpectedRev: 1}, "CND-6", TicketEdit{Set: TicketSet{Title: ptr("Deploy pelican")}})
	if err != nil || rc.Refusal != RefusalStaleRevision {
		t.Fatalf("a stale edit: %+v %v", rc, err)
	}
	if got := found("pelican"); got != "" {
		t.Fatalf("a refused edit's word: %q", got)
	}
	// A ticket made with words in every field is found by each.
	_, key := makeTicket(t, s, NewTicket{Project: "CND", Title: "Heron", Description: "egret", Acceptance: "bittern"})
	for _, q := range []string{"heron", "egret", "bittern", key} {
		if got := found(q); got != key {
			t.Errorf("%s: %q", q, got)
		}
	}
	searchEntries(t, s)
}

func TestABadQueryIsRefused(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	for _, q := range []Query{
		{Project: "cnd"},
		{Filter: Filter{Status: []string{"open"}}},
		{Filter: Filter{Priority: []int{5}}},
		{Filter: Filter{Priority: []int{-1}}},
		{Filter: Filter{Labels: []string{"api"}}},
		{Filter: Filter{Owner: "Al\x01ice"}},
		{Filter: Filter{Owner: strings.Repeat("a", MaxPersonName+1)}},
		{Filter: Filter{Assignees: []string{"Alice"}}},
		{Filter: Filter{Milestone: "m1"}},
		{Filter: Filter{Horizon: []string{"soon"}}},
		{Filter: Filter{Parent: "CND"}},
		{Filter: Filter{Q: strings.Repeat("é", MaxQueryText+1)}},
		{Filter: Filter{Q: "\xff"}},
		{Filter: Filter{Sort: "rank"}},
		{Filter: Filter{Status: slices.Repeat([]string{TicketReady}, MaxFilterValues+1)}},
		{Limit: MaxListLimit + 1},
		{Limit: -1},
	} {
		if _, err := s.List(ctx, q); !errors.Is(err, ErrBadQuery) {
			t.Errorf("%+v: %v", q, err)
		}
	}
	// At their bounds they run.
	for _, q := range []Query{
		{Filter: Filter{Q: strings.Repeat("é", MaxQueryText)}},
		{Filter: Filter{Status: slices.Repeat([]string{TicketReady}, MaxFilterValues)}},
		{Limit: MaxListLimit},
		{Limit: 1},
	} {
		if _, err := s.List(ctx, q); err != nil {
			t.Errorf("%.60v: %v", q, err)
		}
	}
}

func TestChildrenArePagedAsAList(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	_, parent := newTicket(t, s, "CND", "Parent")
	for i := range 25 {
		makeTicket(t, s, NewTicket{Project: "CND", Title: fmt.Sprintf("Child %d", i+1), Parent: parent})
	}
	rc, err := s.DeleteTicket(ctx, testOp("d1"), "CND-3")
	mustDone(t, "delete a child", rc, err)
	var keys []string
	next := ""
	for {
		page, err := s.Children(ctx, parent, 10, next)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 24 {
			t.Fatalf("total %d", page.Total)
		}
		for _, ts := range page.Tickets {
			keys = append(keys, ts.Key)
		}
		if next = page.Next; next == "" {
			break
		}
	}
	var want []string
	for n := 2; n <= 26; n++ {
		if n != 3 {
			want = append(want, TicketKey("CND", int64(n)))
		}
	}
	if !slices.Equal(keys, want) {
		t.Fatalf("children %v", keys)
	}
	// A deleted parent's children keep it, and are found by it.
	rc, err = s.DeleteTicket(ctx, testOp("d2"), parent)
	mustDone(t, "delete the parent", rc, err)
	if page, err := s.Children(ctx, parent, 0, ""); err != nil || page.Total != 24 {
		t.Fatalf("a deleted parent's children: %d %v", page.Total, err)
	}
}

func TestBlockersAreTheLiveOpenTicketsThatBlock(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newOpenProject(t, s, "CND")
	for _, title := range []string{"Blocked", "Open blocker", "Done blocker", "Deleted blocker", "Second open blocker", "Related"} {
		newTicket(t, s, "CND", title)
	}
	for i, r := range []struct{ from, kind string }{{"CND-5", RelationBlocks}, {"CND-2", RelationBlocks}, {"CND-3", RelationBlocks},
		{"CND-4", RelationBlocks}, {"CND-6", RelationRelates}} {
		rc, err := s.AddRelation(ctx, testOp(opID("r", i)), r.from, r.kind, "CND-1")
		mustDone(t, r.from+" "+r.kind, rc, err)
	}
	rc, err := edit(s, "e1", "CND-3", TicketEdit{Set: TicketSet{Status: ptr(TicketDone)}})
	mustDone(t, "done", rc, err)
	rc, err = s.DeleteTicket(ctx, testOp("d1"), "CND-4")
	mustDone(t, "delete", rc, err)
	blockers, err := s.Blockers(ctx, "CND-1")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, b := range blockers {
		if b.Title == "" || b.Status == "" || b.ID == "" {
			t.Fatalf("a blocker: %+v", b)
		}
		keys = append(keys, b.Key)
	}
	if strings.Join(keys, " ") != "CND-2 CND-5" {
		t.Fatalf("blockers %v", keys)
	}
	if blocked, err := s.Blocked(ctx, "CND-1"); err != nil || !blocked {
		t.Fatalf("blocked %v %v", blocked, err)
	}
	if b, err := s.Blockers(ctx, "CND-2"); err != nil || len(b) != 0 {
		t.Fatalf("an unblocked ticket's blockers: %v %v", b, err)
	}
	if _, err := s.Blockers(ctx, "CND-99"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no ticket: %v", err)
	}
}
