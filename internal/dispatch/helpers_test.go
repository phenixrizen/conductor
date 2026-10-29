package dispatch

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// openStore opens a store under dir and closes it with the test.
func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// storeDir is a fresh directory for a store.
func storeDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "dispatch")
}

// testOp is an operation of the actor test by Tester, with the given id.
func testOp(id string) Op { return Op{Actor: "test", ID: id, By: "Tester"} }

// newProject makes a project through project.create, its name its key,
// and returns its id.
func newProject(t *testing.T, s *Store, key string) string {
	t.Helper()
	rc, err := s.CreateProject(context.Background(), testOp("project-"+key), NewProject{Key: key, Name: key})
	if err != nil {
		t.Fatalf("project %s: %v", key, err)
	}
	if rc.Status != StatusDone {
		t.Fatalf("project %s: %+v", key, rc)
	}
	return rc.ID
}

// newOpenProject makes a project through project.create whose close
// policy requires nothing, so its tickets may be made done at once, its
// name its key, and returns its id.
func newOpenProject(t *testing.T, s *Store, key string) string {
	t.Helper()
	rc, err := s.CreateProject(context.Background(), testOp("project-"+key), NewProject{Key: key, Name: key, CloseRequires: &[]string{}})
	if err != nil {
		t.Fatalf("project %s: %v", key, err)
	}
	if rc.Status != StatusDone {
		t.Fatalf("project %s: %+v", key, rc)
	}
	return rc.ID
}

// newTicket makes a ticket of the project with the key through
// ticket.create, and returns its id and key.
func newTicket(t *testing.T, s *Store, project, title string) (id, key string) {
	t.Helper()
	return makeTicket(t, s, NewTicket{Project: project, Title: title})
}

// makeTicket makes a ticket through ticket.create, and returns its id and
// key.
func makeTicket(t *testing.T, s *Store, n NewTicket) (id, key string) {
	t.Helper()
	rc, err := s.CreateTicket(context.Background(), testOp("ticket-"+newID()), n)
	if err != nil {
		t.Fatalf("ticket %s: %v", n.Title, err)
	}
	if rc.Status != StatusDone {
		t.Fatalf("ticket %s: %+v", n.Title, rc)
	}
	return rc.ID, rc.Key
}

// ticketOf reads the ticket with the key, its sets with it: the
// assignees by actor, the live labels by id.
func ticketOf(t *testing.T, s *Store, key string) Ticket {
	t.Helper()
	tk, err := ticketByKey(context.Background(), s.db, key)
	if err != nil {
		t.Fatalf("ticket %s: %v", key, err)
	}
	tk.Assignees = texts(t, s, "SELECT actor FROM ticket_assignees WHERE ticket_id = ? ORDER BY actor", tk.ID)
	tk.Labels = texts(t, s, "SELECT label_id FROM ticket_labels JOIN labels ON labels.id = label_id WHERE ticket_id = ? AND labels.deleted_at = '' ORDER BY label_id", tk.ID)
	return tk
}

// projectOf reads the project with the key.
func projectOf(t *testing.T, s *Store, key string) Project {
	t.Helper()
	p, err := projectByKey(context.Background(), s.db, key)
	if err != nil {
		t.Fatalf("project %s: %v", key, err)
	}
	return p
}

// texts reads the one text column of the rows a query gives.
func texts(t *testing.T, s *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := s.db.Query(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
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

// insertLabel writes a label's row of the project as the test needs it,
// past label.create's rules (a tombstone at once, say): live, or a
// tombstone when deleted. It returns the label's id.
func insertLabel(t *testing.T, s *Store, projectID, name string, deleted bool) string {
	t.Helper()
	id, now := newID(), stamp(time.Now())
	if _, err := s.db.Exec("INSERT INTO labels (id, project_id, name, color, created_at, deleted_at, rev) VALUES (?, ?, ?, 'gray', ?, ?, 1)",
		id, projectID, name, now, deletedStamp(deleted, now)); err != nil {
		t.Fatal(err)
	}
	return id
}

// insertMilestone writes a milestone's row of the project as the test
// needs it, past milestone.create's rules (a tombstone at once, say). It
// returns the milestone's id.
func insertMilestone(t *testing.T, s *Store, projectID, name string, deleted bool) string {
	t.Helper()
	id, now := newID(), stamp(time.Now())
	if _, err := s.db.Exec("INSERT INTO milestones (id, project_id, name, created_at, updated_at, deleted_at, rev) VALUES (?, ?, ?, ?, ?, ?, 1)",
		id, projectID, name, now, now, deletedStamp(deleted, now)); err != nil {
		t.Fatal(err)
	}
	return id
}

func deletedStamp(deleted bool, now string) string {
	if deleted {
		return now
	}
	return ""
}

// ptr is a pointer to v, for the fields an edit sets.
func ptr[T any](v T) *T { return &v }

// field is a scalar field set by setFields, in order.
type field struct{ name, value string }

// setFields sets scalar text fields of a project's or a ticket's row in
// one operation, as the edit intents will: the row's rev read and
// compared with the one expected, one set row per field with its prior.
func setFields(s *Store, op Op, entity, id string, fields ...field) (Receipt, error) {
	table := revTables[entity].table
	return s.Apply(context.Background(), op, entity+".edit", fields, func(x *tx) error {
		var rev int64
		if err := x.tx.QueryRowContext(x.ctx, "SELECT rev FROM "+table+" WHERE id = ?", id).Scan(&rev); err != nil {
			if isNoRows(err) {
				return refuse(RefusalNotFound, "no %s %s", entity, id)
			}
			return err
		}
		if err := x.expect(rev); err != nil {
			return err
		}
		for _, f := range fields {
			var prior string
			if err := x.tx.QueryRowContext(x.ctx, "SELECT "+f.name+" FROM "+table+" WHERE id = ?", id).Scan(&prior); err != nil {
				return err
			}
			if _, err := x.tx.ExecContext(x.ctx, "UPDATE "+table+" SET "+f.name+" = ? WHERE id = ?", f.value, id); err != nil {
				return err
			}
			row := ChangeRow{Entity: entity, EntityID: id, Field: f.name, Op: OpSet, Value: f.value, Prior: prior}
			if entity == EntityTicket {
				row.TicketID = id
				if err := x.tx.QueryRowContext(x.ctx, "SELECT project_id FROM tickets WHERE id = ?", id).Scan(&row.ProjectID); err != nil {
					return err
				}
			} else {
				row.ProjectID = id
			}
			if err := x.record(row); err != nil {
				return err
			}
		}
		return nil
	})
}

// changesOf reads the change rows of one operation, by n.
func changesOf(t *testing.T, s *Store, actor, opID string) []ChangeRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT seq, entity, entity_id, ticket_id, project_id, field, op, value, prior, at, actor, by, op_id, n
		FROM changes WHERE actor = ? AND op_id = ? ORDER BY n`, actor, opID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []ChangeRow
	for rows.Next() {
		var c ChangeRow
		var at string
		if err := rows.Scan(&c.Seq, &c.Entity, &c.EntityID, &c.TicketID, &c.ProjectID, &c.Field, &c.Op, &c.Value, &c.Prior, &at, &c.Actor, &c.By, &c.OpID, &c.N); err != nil {
			t.Fatal(err)
		}
		c.At = unstamp(at)
		out = append(out, c)
	}
	return out
}

// tableNames is every table of the store's schema but the FTS index's
// own and SQLite's, by name: what the schema's tables are listed against.
func tableNames(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name != 'sqlite_sequence' AND name NOT LIKE 'tickets\\_fts\\_%' ESCAPE '\\' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	return names
}

// count counts the rows a query gives.
func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// want fails the test unless the receipt has the status and the refusal
// given.
func want(t *testing.T, rc Receipt, err error, status, refusal string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", rc.OpID, err)
	}
	if rc.Status != status || rc.Refusal != refusal {
		t.Fatalf("receipt %+v: want %s %q", rc, status, refusal)
	}
}

func opID(prefix string, i int) string { return fmt.Sprintf("%s%d", prefix, i) }
