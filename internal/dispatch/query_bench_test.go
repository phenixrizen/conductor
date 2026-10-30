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
// written in one transaction, as a restore would leave them, and the
// search entries of each ticket and each comment by indexTicket and
// indexComment, as an operation writes them. It answers the words, for
// the searches.
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
	var ids, comments []string
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
			comment := newID()
			exec("INSERT INTO comments (id, ticket_id, author, by, body, created_at, rev) VALUES (?, ?, 'admin', 'Alice', ?, ?, 1)",
				comment, id, benchText(words, z, 10+r.Intn(50)), stampAt(created+c+1))
			comments = append(comments, comment)
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
	for _, id := range comments {
		if err := indexComment(ctx, tx, id); err != nil {
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
// index, the statement), not waived, save a search of several very common
// words, whose cost the owner accepted (Owner decisions, 21). The benchmark
// reports, and fails nothing on a loaded machine's clock.
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
// page and at the list view's page of MaxListLimit. The searches of one
// letter and of two are a search box's first keystrokes, and those of the
// vocabulary's 3, 5 and 10 most common words a few words typed, reported
// without the full-text target: each term reads every entry it matches
// (termMatch), so every common word adds to a search's cost, which the
// owner accepted (Owner decisions, 21). The others show the list's other
// common pages, a search of one letter of every project among them: no page
// sends one, since a search box searches its project and the CLI and a
// saved view of every project send whole words. The last is the longest
// search a client may send, MaxQueryText characters of the vocabulary's
// most common words, of every project.
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
	// The vocabulary's words are in the order of the Zipf law, the most
	// common first: common(k) is a search of the k most common, and the
	// longest search as many of them as fit.
	common := func(k int) string { return strings.Join(words[:k], " ") }
	var longest strings.Builder
	for _, w := range words {
		if longest.Len()+1+len(w) > MaxQueryText {
			break
		}
		if longest.Len() > 0 {
			longest.WriteByte(' ')
		}
		longest.WriteString(w)
	}
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
		{"search-3-common-words", Query{Project: "BENA", Filter: Filter{Q: common(3), Sort: SortUpdated, Desc: true}}, 0},
		{"search-5-common-words", Query{Project: "BENA", Filter: Filter{Q: common(5), Sort: SortUpdated, Desc: true}}, 0},
		{"search-10-common-words", Query{Project: "BENA", Filter: Filter{Q: common(10), Sort: SortUpdated, Desc: true}}, 0},
		{"project", Query{Project: "BENA", Filter: Filter{Sort: SortUpdated, Desc: true}}, 0},
		{"every-project-by-title", Query{Filter: Filter{Sort: SortTitle}, Limit: MaxListLimit}, 0},
		{"search-every-project", Query{Filter: Filter{Q: search, Sort: SortUpdated, Desc: true}}, benchSearchTarget},
		{"search-one-letter-every-project-200", Query{Filter: Filter{Q: letter, Sort: SortUpdated, Desc: true}, Limit: MaxListLimit}, 0},
		{"assignee-by-milestone", Query{Project: "BENB", Filter: Filter{Assignees: []string{"agent:claude"}, Sort: SortMilestone}}, 0},
		{"search-longest-every-project", Query{Filter: Filter{Q: longest.String(), Sort: SortUpdated, Desc: true}}, 0},
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

// BenchmarkIndexedWrite times the operations whose rows make a search
// entry stale, on a ticket of n comments: a comment's add, edit and
// delete, and an edit of the ticket's title. Each holds the store's one
// connection while it runs, and rewrites the one entry it changes, the
// comment's (indexComment) or the ticket's own (indexTicket), so its time
// is its own text's and stays the same as the ticket's comments grow
// (docs/plans/dispatch.md, Owner decisions, 20; before it, every one of
// them rewrote one entry holding all the ticket's comments). "comments-3"
// is a ticket of the seed's largest kind; the others hold comments as
// long as a comment may be, up to the bound of MaxComments. Every
// iteration leaves the ticket as it found it, outside the timer: an added
// comment's row and entry are taken out again, so the ticket stays under
// its bound, and a deleted comment is restored with its entry. Run with
// go test -run '^$' -bench BenchmarkIndexedWrite -benchtime 3x ./internal/dispatch/.
func BenchmarkIndexedWrite(b *testing.B) {
	body := benchBodies()
	for _, c := range benchCommented {
		b.Run(fmt.Sprintf("comments-%d", c.n), func(b *testing.B) {
			s, key, last := commentedTicket(b, body, c.n, c.size)
			ctx := context.Background()
			bodies := [2]string{body(c.size), body(c.size)}
			done := func(what string, rc Receipt, err error) {
				if err != nil || rc.Status != StatusDone {
					b.Fatalf("%s: %+v %v", what, rc, err)
				}
			}
			b.Run("comment.add", func(b *testing.B) {
				i := 0
				for b.Loop() {
					rc, err := s.AddComment(ctx, testOp(fmt.Sprintf("a%d", i)), key, bodies[i%2])
					done("the add", rc, err)
					b.StopTimer()
					takeOutComment(b, s, rc.ID)
					b.StartTimer()
					i++
				}
			})
			b.Run("comment.edit", func(b *testing.B) {
				i := 0
				for b.Loop() {
					rc, err := s.EditComment(ctx, testOp(fmt.Sprintf("e%d", i)), key, last, bodies[i%2])
					done("the edit", rc, err)
					i++
				}
			})
			b.Run("comment.delete", func(b *testing.B) {
				i := 0
				for b.Loop() {
					rc, err := s.DeleteComment(ctx, testOp(fmt.Sprintf("d%d", i)), key, last)
					done("the delete", rc, err)
					b.StopTimer()
					if _, err := s.db.ExecContext(ctx, "UPDATE comments SET deleted_at = '' WHERE id = ?", last); err != nil {
						b.Fatal(err)
					}
					if err := indexComment(ctx, s.db, last); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
					i++
				}
			})
			b.Run("ticket.edit", func(b *testing.B) {
				titles := [2]string{"Many comments, edited", "Many comments"}
				i := 0
				for b.Loop() {
					rc, err := s.EditTicket(ctx, testOp(fmt.Sprintf("t%d", i)), key, TicketEdit{Set: TicketSet{Title: &titles[i%2]}})
					done("the title's edit", rc, err)
					i++
				}
			})
		})
	}
}

// benchCommented is the tickets BenchmarkIndexedWrite and
// BenchmarkTicketRead time, by their comments, n of size bytes each: a
// ticket of the seed's largest kind, then tickets of comments as long as
// a comment may be, up to the bound of MaxComments.
var benchCommented = []struct{ n, size int }{{3, 400}, {200, MaxCommentBody}, {1000, MaxCommentBody}, {MaxComments - 1, MaxCommentBody}}

// benchBodies answers texts of the vocabulary's words drawn by its Zipf
// law, each as many words as fit in size bytes, the same texts every run.
func benchBodies() func(size int) string {
	r := rand.New(rand.NewSource(1))
	words := benchWords(r, 4000)
	z := rand.NewZipf(r, 1.07, 2, uint64(len(words)-1))
	return func(size int) string {
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
}

// commentedTicket makes a store holding one ticket with a description of
// body(1500) and n comments of body(size), and answers the store, the
// ticket's key and its last comment's id. The comments are written in one
// transaction, as a restore would leave them, and each search entry as an
// operation writes it.
func commentedTicket(b *testing.B, body func(int) string, n, size int) (s *Store, key, last string) {
	b.Helper()
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	at := time.Now()
	for i := range n {
		last = newID()
		if _, err := tx.ExecContext(ctx, "INSERT INTO comments (id, ticket_id, author, by, body, created_at, rev) VALUES (?, ?, 'admin', 'Bench', ?, ?, 1)",
			last, rc.ID, body(size), stamp(at.Add(time.Duration(i)*time.Millisecond))); err != nil {
			b.Fatal(err)
		}
		if err := indexComment(ctx, tx, last); err != nil {
			b.Fatal(err)
		}
	}
	if err := indexTicket(ctx, tx, rc.ID); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return s, rc.Key, last
}

// BenchmarkTicketRead times the reads of a ticket of n comments: the
// ticket's own read (Ticket), which carries its last MaxTicketComments
// comments, a page of MaxCommentPage of them from the middle (Comments)
// and one of them (Comment). Each numbers the ticket's comments, which
// takes them all in their order, without their bodies, and reads whole
// only the comments it answers (commentsNumbered); numbered with their
// bodies, the window sorted the text of every comment to keep a few. Run
// with go test -run '^$' -bench BenchmarkTicketRead ./internal/dispatch/.
func BenchmarkTicketRead(b *testing.B) {
	body := benchBodies()
	for _, c := range benchCommented {
		b.Run(fmt.Sprintf("comments-%d", c.n), func(b *testing.B) {
			s, key, last := commentedTicket(b, body, c.n, c.size)
			ctx := context.Background()
			b.Run("Ticket", func(b *testing.B) {
				for b.Loop() {
					v, err := s.Ticket(ctx, key)
					if err != nil || len(v.Comments) != min(c.n, MaxTicketComments) {
						b.Fatalf("the read: %d comments, %v", len(v.Comments), err)
					}
				}
			})
			b.Run("Comments", func(b *testing.B) {
				for b.Loop() {
					cs, err := s.Comments(ctx, key, c.n/2, MaxCommentPage)
					if err != nil || len(cs) != min(c.n-c.n/2, MaxCommentPage) {
						b.Fatalf("the page: %d comments, %v", len(cs), err)
					}
				}
			})
			b.Run("Comment", func(b *testing.B) {
				for b.Loop() {
					if cm, err := s.Comment(ctx, key, last); err != nil || cm.N != c.n {
						b.Fatalf("the comment: %+v %v", cm, err)
					}
				}
			})
		})
	}
}

// takeOutComment takes the comment with the id out of the store, its row
// and its search entry, as though it had never been added: what
// BenchmarkIndexedWrite does between two adds, outside its timer, so a
// ticket stays under its bound however many adds it times.
func takeOutComment(b *testing.B, s *Store, id string) {
	b.Helper()
	var ticket, comment int64
	if err := s.db.QueryRow("SELECT t.rowid, c.rowid FROM comments c JOIN tickets t ON t.id = c.ticket_id WHERE c.id = ?", id).Scan(&ticket, &comment); err != nil {
		b.Fatal(err)
	}
	entry, err := entryRowid(ticket, comment)
	if err != nil {
		b.Fatal(err)
	}
	for _, q := range []struct {
		stmt string
		arg  any
	}{{"DELETE FROM tickets_fts WHERE rowid = ?", entry}, {"DELETE FROM comments WHERE id = ?", id}} {
		if _, err := s.db.Exec(q.stmt, q.arg); err != nil {
			b.Fatal(err)
		}
	}
}
