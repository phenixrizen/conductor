package dispatch

import (
	"context"
	"encoding/json"
	"time"
)

// Person is a name in the directory the owner and assignee pickers
// offer (decision D4: people are local labels until identity exists).
// The name is the person: it is what a ticket's owner and an assignee's
// actor carry, nothing carries a person's id, and two replicas adding one
// name add one row.
type Person struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// AddPerson adds a name to the directory (person.add): 1 to
// MaxPersonName characters with no control character (invalid; bound
// past the length); a name in the directory already is invalid.
func (s *Store) AddPerson(ctx context.Context, op Op, name string) (Receipt, error) {
	args := struct {
		Name string `json:"name"`
	}{name}
	return s.Apply(ctx, op, "person.add", args, func(t *tx) error {
		if err := checkName("name", name, MaxPersonName); err != nil {
			return err
		}
		var held int
		if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM people WHERE name = ?", name).Scan(&held); err != nil {
			return err
		}
		if held > 0 {
			return refuse(RefusalInvalid, "%s is in the directory already", name)
		}
		if _, err := t.tx.ExecContext(t.ctx, "INSERT INTO people (name, created_at) VALUES (?, ?)", name, stamp(t.now)); err != nil {
			return err
		}
		value, err := json.Marshal(Person{Name: name, CreatedAt: t.now})
		if err != nil {
			return err
		}
		t.created(name, "")
		return t.record(ChangeRow{Entity: EntityPerson, EntityID: name, Op: OpCreate, Value: string(value)})
	})
}

// People reads the directory, by name.
func (s *Store) People(ctx context.Context) ([]Person, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT name, created_at FROM people ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		var created string
		if err := rows.Scan(&p.Name, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = unstamp(created)
		out = append(out, p)
	}
	return out, rows.Err()
}
