package dispatch

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// benchWords is a vocabulary of made-up words, the same every run: two
// to four syllables each, drawn by a Zipf law so a few are everywhere and
// most are rare, as words are in a tracker's text.
func benchWords(r *rand.Rand, n int) []string {
	syllables := []string{"ka", "lo", "mi", "ne", "ro", "ta", "vu", "se", "di", "po", "ga", "ru", "fe", "zo", "bi", "chu", "an", "el", "or", "ix"}
	seen := map[string]bool{}
	var words []string
	for len(words) < n {
		var b strings.Builder
		for range 2 + r.Intn(3) {
			b.WriteString(syllables[r.Intn(len(syllables))])
		}
		if w := b.String(); !seen[w] {
			seen[w] = true
			words = append(words, w)
		}
	}
	return words
}

// benchText is n words of the vocabulary, drawn by z.
func benchText(words []string, z *rand.Zipf, n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = words[z.Uint64()]
	}
	return strings.Join(parts, " ")
}

// seedBench fills a store with the benchmark's tickets: n in the three
// projects BENA, BENB and BENC (a half, a third and a sixth), each with a
// title of 3 to 9 words and a description of 20 to 200, a third with
// acceptance criteria, up to three comments, labels of ten per project,
// assignees, an owner, a milestone of five per project, a status, a
// priority and a horizon spread over their vocabularies, a fifth with a
// parent, one in twenty blocked and one in fifty deleted. The rows are
// written in one transaction, as a restore would leave them, and each
// ticket's search entry by indexTicket, as an operation writes it. It
// answers the words, for the searches.
func seedBench(tb testing.TB, s *Store, n int) []string {
	tb.Helper()
	ctx := context.Background()
	r := rand.New(rand.NewSource(1))
	words := benchWords(r, 4000)
	z := rand.NewZipf(r, 1.07, 2, uint64(len(words)-1))
	people := []string{"Alice", "Bob", "Carol", "Dan", "Erin", "Frank"}
	actors := []string{"person:Alice", "person:Bob", "person:Carol", "person:Dan", "agent:claude", "agent:codex", "agent:gemini", "person:Erin"}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) {
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			tb.Fatalf("%s: %v", q, err)
		}
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	stampAt := func(minutes int) string { return stamp(base.Add(time.Duration(minutes) * time.Minute)) }
	type project struct {
		key, id    string
		share      int
		labels     []string
		milestones []string
		tickets    []string
	}
	projects := []*project{{key: "BENA", share: 3}, {key: "BENB", share: 2}, {key: "BENC", share: 1}}
	for _, p := range projects {
		p.id = newID()
		exec("INSERT INTO projects (id, key, name, close_requires, created_at, updated_at, rev) VALUES (?, ?, ?, '[]', ?, ?, 1)", p.id, p.key, p.key, stampAt(0), stampAt(0))
		for i := range 10 {
			id := newID()
			exec("INSERT INTO labels (id, project_id, name, color, created_at, rev) VALUES (?, ?, ?, ?, ?, 1)", id, p.id, fmt.Sprintf("label%d", i), LabelColors[i], stampAt(0))
			p.labels = append(p.labels, id)
		}
		for i := range 5 {
			id := newID()
			target := ""
			if i < 3 {
				target = fmt.Sprintf("2026-%02d-01", 3+i)
			}
			exec("INSERT INTO milestones (id, project_id, name, target, created_at, updated_at, rev) VALUES (?, ?, ?, ?, ?, ?, 1)", id, p.id, fmt.Sprintf("Milestone %d", i), target, stampAt(0), stampAt(0))
			p.milestones = append(p.milestones, id)
		}
	}
	var ids []string
	for i := range n {
		p := projects[0]
		switch k := i % 6; {
		case k >= 3 && k < 5:
			p = projects[1]
		case k == 5:
			p = projects[2]
		}
		id, number := newID(), int64(len(p.tickets)+1)
		created := 10 * i
		parent, milestone, acceptance, deleted, closedAt := "", "", "", "", ""
		if len(p.tickets) > 10 && r.Intn(5) == 0 {
			parent = p.tickets[r.Intn(len(p.tickets))]
		}
		if r.Intn(3) > 0 {
			milestone = p.milestones[r.Intn(len(p.milestones))]
		}
		if r.Intn(3) == 0 {
			acceptance = benchText(words, z, 10+r.Intn(40))
		}
		status := Statuses[r.Intn(len(Statuses))]
		if closed(status) {
			closedAt = stampAt(created + 5)
		}
		if r.Intn(50) == 0 {
			deleted = stampAt(created + 7)
		}
		owner := ""
		if r.Intn(4) > 0 {
			owner = people[r.Intn(len(people))]
		}
		exec(`INSERT INTO tickets (id, project_id, number, key, title, description, acceptance, status, priority, horizon, owner, parent_id, milestone_id,
				created_at, created_by, updated_at, closed_at, deleted_at, rev) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'admin', ?, ?, ?, 1)`,
			id, p.id, number, TicketKey(p.key, number), benchText(words, z, 3+r.Intn(7)), benchText(words, z, 20+r.Intn(181)), acceptance,
			status, r.Intn(MaxPriority+1), Horizons[r.Intn(len(Horizons))], owner, parent, milestone, stampAt(created), stampAt(created+r.Intn(100000)), closedAt, deleted)
		for _, a := range r.Perm(len(actors))[:r.Intn(3)] {
			exec("INSERT INTO ticket_assignees (ticket_id, actor, added_at) VALUES (?, ?, ?)", id, actors[a], stampAt(created))
		}
		for _, l := range r.Perm(len(p.labels))[:r.Intn(4)] {
			exec("INSERT INTO ticket_labels (ticket_id, label_id) VALUES (?, ?)", id, p.labels[l])
		}
		for c := range r.Intn(4) {
			exec("INSERT INTO comments (id, ticket_id, author, by, body, created_at, rev) VALUES (?, ?, 'admin', 'Alice', ?, ?, 1)",
				newID(), id, benchText(words, z, 10+r.Intn(50)), stampAt(created+c+1))
		}
		if len(p.tickets) > 0 && r.Intn(20) == 0 {
			exec("INSERT INTO relations (id, from_id, to_id, kind, created_at) VALUES (?, ?, ?, ?, ?)", newID(), p.tickets[r.Intn(len(p.tickets))], id, RelationBlocks, stampAt(created))
		}
		p.tickets = append(p.tickets, id)
		ids = append(ids, id)
	}
	for _, id := range ids {
		if err := indexTicket(ctx, tx, id); err != nil {
			tb.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return words
}

// The targets BenchmarkList measures against (docs/plans/dispatch.md,
// "Queries"), on the Linux box: a filtered, sorted page and a full-text
// page of 10,000 tickets in three projects. A target missed is tuned (an
// index, the statement), not waived; the benchmark reports, and fails
// nothing on a loaded machine's clock.
const (
	benchFilteredTarget = 20 * time.Millisecond
	benchSearchTarget   = 50 * time.Millisecond
)

// mostBegun is the prefix of n letters that the most words of the
// vocabulary begin with: a search box's first keystrokes at their
// slowest, since a search's last term is a prefix and FTS5 reads the list
// of every term it begins, or its prefix index's list of them all.
func mostBegun(words []string, n int) string {
	counts := map[string]int{}
	best := ""
	for _, w := range words {
		p := w[:n]
		counts[p]++
		if counts[p] > counts[best] {
			best = p
		}
	}
	return best
}

// BenchmarkList reads pages of 10,000 tickets in three projects: run with
// go test -run '^$' -bench BenchmarkList -benchmem ./internal/dispatch/.
// "filtered" and "search" are the plan's two targets, at a list's default
// page and at the list view's page of MaxListLimit; the searches of one
// letter and of two are a search box's first keystrokes, held to the
// full-text target as the search of every project is. The others show
// the list's other common pages, a search of one letter of every project
// among them: no page sends one, since a search box searches its project
// and the CLI and a saved view of every project send whole words.
func BenchmarkList(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	words := seedBench(b, s, 10000)
	ctx := context.Background()
	labels, err := s.Labels(ctx, "BENA")
	if err != nil {
		b.Fatal(err)
	}
	// Two words of the search, one common and one less so, the last typed
	// halfway: what a person looking for a ticket types.
	search := words[3] + " " + words[40][:len(words[40])-1]
	letter, letters := mostBegun(words, 1), mostBegun(words, 2)
	filtered := Filter{Status: []string{TicketReady, TicketInProgress, TicketInReview}, Labels: []string{labels[0].ID, labels[1].ID},
		Sort: SortPriority, Desc: true}
	for _, c := range []struct {
		name   string
		q      Query
		target time.Duration
	}{
		{"filtered", Query{Project: "BENA", Filter: filtered}, benchFilteredTarget},
		{"filtered-200", Query{Project: "BENA", Filter: filtered, Limit: MaxListLimit}, benchFilteredTarget},
		{"search", Query{Project: "BENA", Filter: Filter{Q: search, Sort: SortUpdated, Desc: true}}, benchSearchTarget},
		{"search-200", Query{Project: "BENA", Filter: Filter{Q: search, Sort: SortUpdated, Desc: true}, Limit: MaxListLimit}, benchSearchTarget},
		{"search-one-letter-200", Query{Project: "BENA", Filter: Filter{Q: letter, Sort: SortUpdated, Desc: true}, Limit: MaxListLimit}, benchSearchTarget},
		{"search-two-letters-200", Query{Project: "BENA", Filter: Filter{Q: letters, Sort: SortUpdated, Desc: true}, Limit: MaxListLimit}, benchSearchTarget},
		{"project", Query{Project: "BENA", Filter: Filter{Sort: SortUpdated, Desc: true}}, 0},
		{"every-project-by-title", Query{Filter: Filter{Sort: SortTitle}, Limit: MaxListLimit}, 0},
		{"search-every-project", Query{Filter: Filter{Q: search, Sort: SortUpdated, Desc: true}}, benchSearchTarget},
		{"search-one-letter-every-project-200", Query{Filter: Filter{Q: letter, Sort: SortUpdated, Desc: true}, Limit: MaxListLimit}, 0},
		{"assignee-by-milestone", Query{Project: "BENB", Filter: Filter{Assignees: []string{"agent:claude"}, Sort: SortMilestone}}, 0},
	} {
		b.Run(c.name, func(b *testing.B) {
			page, err := s.List(ctx, c.q)
			if err != nil {
				b.Fatal(err)
			}
			if len(page.Tickets) == 0 || page.Total == 0 {
				b.Fatalf("%s: an empty page measures nothing", c.name)
			}
			start := time.Now()
			for b.Loop() {
				if _, err := s.List(ctx, c.q); err != nil {
					b.Fatal(err)
				}
			}
			per := time.Since(start) / time.Duration(b.N)
			b.ReportMetric(float64(page.Total), "tickets/list")
			if c.target > 0 {
				b.ReportMetric(float64(c.target.Milliseconds()), "target-ms")
				if per > c.target {
					b.Logf("%s: %v a page, past the target of %v", c.name, per, c.target)
				}
			}
		})
	}
}

// BenchmarkCommentEdit edits a comment of a ticket of n comments, as
// comment.edit does: the operation rewrites the ticket's search entry
// whole (indexTicket), the body of every live comment in it, while it
// holds the store's one connection, so its time grows with the ticket's
// comment text. A comment's add and delete rewrite the entry the same
// way, and so does an edit of the ticket's title, description or
// acceptance criteria. "comments-3" is a ticket of the seed's largest
// kind; the others hold comments as long as a comment may be, up to the
// bound of MaxComments. Run with
// go test -run '^$' -bench BenchmarkCommentEdit -benchtime 3x ./internal/dispatch/.
func BenchmarkCommentEdit(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	words := benchWords(r, 4000)
	z := rand.NewZipf(r, 1.07, 2, uint64(len(words)-1))
	// body is the vocabulary's words, as many as fit in size bytes.
	body := func(size int) string {
		var sb strings.Builder
		for {
			w := words[z.Uint64()]
			if sb.Len()+len(w)+1 > size {
				return sb.String()
			}
			sb.WriteString(w)
			sb.WriteByte(' ')
		}
	}
	for _, c := range []struct{ n, size int }{{3, 400}, {200, MaxCommentBody}, {1000, MaxCommentBody}, {MaxComments - 1, MaxCommentBody}} {
		b.Run(fmt.Sprintf("comments-%d", c.n), func(b *testing.B) {
			s, err := Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { s.Close() })
			ctx := context.Background()
			if rc, err := s.CreateProject(ctx, testOp("p"), NewProject{Key: "BEN", Name: "BEN"}); err != nil || rc.Status != StatusDone {
				b.Fatalf("the project: %+v %v", rc, err)
			}
			rc, err := s.CreateTicket(ctx, testOp("t"), NewTicket{Project: "BEN", Title: "Many comments", Description: body(1500)})
			if err != nil || rc.Status != StatusDone {
				b.Fatalf("the ticket: %+v %v", rc, err)
			}
			id, key := rc.ID, rc.Key
			// The comments in one transaction, as a restore would leave them,
			// and the entry as an operation writes it.
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			var last string
			at := time.Now()
			for i := range c.n {
				last = newID()
				if _, err := tx.ExecContext(ctx, "INSERT INTO comments (id, ticket_id, author, by, body, created_at, rev) VALUES (?, ?, 'admin', 'Bench', ?, ?, 1)",
					last, id, body(c.size), stamp(at.Add(time.Duration(i)*time.Millisecond))); err != nil {
					b.Fatal(err)
				}
			}
			if err := indexTicket(ctx, tx, id); err != nil {
				b.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			bodies := [2]string{body(c.size), body(c.size)}
			i := 0
			for b.Loop() {
				rc, err := s.EditComment(ctx, testOp(fmt.Sprintf("e%d", i)), key, last, bodies[i%2])
				if err != nil || rc.Status != StatusDone {
					b.Fatalf("the edit: %+v %v", rc, err)
				}
				i++
			}
		})
	}
}
