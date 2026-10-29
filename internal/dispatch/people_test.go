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
