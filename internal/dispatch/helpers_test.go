package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
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

// newProject makes the minimal project row this change's schema allows
// (the create intent comes with PR-2) through Apply, as project.create
// will: one create row, the project's rev 1. It returns the project's id.
func newProject(t *testing.T, s *Store, key string) string {
	t.Helper()
	rc, err := createProject(s, testOp("project-"+key), key)
	if err != nil {
		t.Fatalf("project %s: %v", key, err)
	}
	if rc.Status != StatusDone {
		t.Fatalf("project %s: %+v", key, rc)
	}
	return rc.ID
}

func createProject(s *Store, op Op, key string) (Receipt, error) {
	id := newID()
	return s.Apply(context.Background(), op, "project.create", map[string]string{"key": key}, func(x *tx) error {
		if !ValidKey(key) {
			return refuse(RefusalInvalid, "%s is not a project key", key)
		}
		if _, err := x.tx.ExecContext(x.ctx, "INSERT INTO projects (id, key, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", id, key, key, stamp(x.now), stamp(x.now)); err != nil {
			return err
		}
		x.created(id, key)
		value, _ := json.Marshal(map[string]string{"id": id, "key": key, "name": key})
		return x.record(ChangeRow{Entity: EntityProject, EntityID: id, ProjectID: id, Op: OpCreate, Value: string(value)})
	})
}

// createTicket makes the minimal ticket row this change's schema allows
// through Apply, as ticket.create will: its number allocated in the
// transaction, one create row.
func createTicket(s *Store, op Op, projectID, title string) (Receipt, error) {
	id := newID()
	return s.Apply(context.Background(), op, "ticket.create", map[string]string{"project": projectID, "title": title}, func(x *tx) error {
		n, err := allocateNumber(x, projectID)
		if err != nil {
			return err
		}
		var key string
		if err := x.tx.QueryRowContext(x.ctx, "SELECT key FROM projects WHERE id = ?", projectID).Scan(&key); err != nil {
			return err
		}
		tkey := TicketKey(key, n)
		if _, err := x.tx.ExecContext(x.ctx, "INSERT INTO tickets (id, project_id, number, key, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			id, projectID, n, tkey, title, stamp(x.now), stamp(x.now)); err != nil {
			return err
		}
		x.created(id, tkey)
		value, _ := json.Marshal(map[string]any{"id": id, "key": tkey, "number": n, "title": title})
		return x.record(ChangeRow{Entity: EntityTicket, EntityID: id, TicketID: id, ProjectID: projectID, Op: OpCreate, Value: string(value)})
	})
}

func newTicket(t *testing.T, s *Store, projectID, title string) (id, key string) {
	t.Helper()
	rc, err := createTicket(s, testOp("ticket-"+newID()), projectID, title)
	if err != nil {
		t.Fatalf("ticket %s: %v", title, err)
	}
	if rc.Status != StatusDone {
		t.Fatalf("ticket %s: %+v", title, rc)
	}
	return rc.ID, rc.Key
}

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
