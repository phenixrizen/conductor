package dispatch

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestAProjectKeyIsRefusedOutsideThePattern(t *testing.T) {
	for key, ok := range map[string]bool{
		"CND": true, "C1": true, "AB2345": true, "ZZZZZZ": true,
		"A": false, "ABCDEFG": false, "cnd": false, "1AB": false, "C-D": false, "": false, "CND ": false, "ÀBC": false, "C_D": false,
	} {
		if ValidKey(key) != ok {
			t.Errorf("ValidKey(%q) = %v", key, !ok)
		}
	}
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	rc, err := s.CreateProject(ctx, testOp("p1"), NewProject{Key: "cnd", Name: "Conductor"})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = s.CreateProject(ctx, testOp("p2"), NewProject{Key: "CND", Name: "Conductor"})
	want(t, rc, err, StatusDone, "")
	if rc.Key != "CND" || len(rc.ID) != IDLen {
		t.Fatalf("the create's receipt: %+v", rc)
	}
}

func TestTicketKeysParse(t *testing.T) {
	if k := TicketKey("CND", 12); k != "CND-12" {
		t.Fatalf("key %q", k)
	}
	for key, want := range map[string]struct {
		project string
		n       int64
		ok      bool
	}{
		"CND-12":                  {"CND", 12, true},
		"C1-1":                    {"C1", 1, true},
		"CND-999999999999999999":  {"CND", 999999999999999999, true},
		"CND-0":                   {},
		"CND-012":                 {},
		"CND":                     {},
		"CND-":                    {},
		"cnd-1":                   {},
		"CND-1x":                  {},
		"CND--1":                  {},
		"CND-1000000000000000000": {},
		"-1":                      {},
		"CND-1 ":                  {},
	} {
		project, n, ok := ParseKey(key)
		if project != want.project || n != want.n || ok != want.ok {
			t.Errorf("ParseKey(%q) = %q %d %v, want %q %d %v", key, project, n, ok, want.project, want.n, want.ok)
		}
	}
}

func TestNewIDShape(t *testing.T) {
	shape := regexp.MustCompile(`^[0-9a-f]{16}$`)
	seen := map[string]bool{}
	for range 100 {
		id := newID()
		if !shape.MatchString(id) {
			t.Fatalf("id %q", id)
		}
		if seen[id] {
			t.Fatalf("id %q twice", id)
		}
		seen[id] = true
	}
}

// Fifty creates at once get CND-1 to CND-50, allocated in the order they
// committed: no duplicate, no gap.
func TestNumbersAreAllocatedInOrderUnderConcurrentCreates(t *testing.T) {
	s := openStore(t, storeDir(t))
	pid := newProject(t, s, "CND")
	ctx := context.Background()
	const n = 50
	keys := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, err := s.CreateTicket(ctx, testOp(opID("t", i)), NewTicket{Project: "CND", Title: opID("Ticket ", i)})
			if err == nil && rc.Status != StatusDone {
				err = &Refusal{Code: rc.Refusal, Words: rc.Error}
			}
			keys[i], errs[i] = rc.Key, err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	numbers := make([]int64, 0, n)
	for _, key := range keys {
		project, num, ok := ParseKey(key)
		if !ok || project != "CND" {
			t.Fatalf("key %q", key)
		}
		numbers = append(numbers, num)
	}
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
	for i, num := range numbers {
		if num != int64(i+1) {
			t.Fatalf("numbers %v: want 1 to %d", numbers, n)
		}
	}
	// In the order the creates committed: the create rows' sequence
	// carries the numbers ascending.
	rows, err := s.db.Query("SELECT t.number FROM changes c JOIN tickets t ON t.id = c.entity_id WHERE c.entity = 'ticket' AND c.op = 'create' ORDER BY c.seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var last int64
	for rows.Next() {
		var num int64
		rows.Scan(&num)
		if num != last+1 {
			t.Fatalf("number %d after %d in the log's order", num, last)
		}
		last = num
	}
	var next int64
	if err := s.db.QueryRow("SELECT next_number FROM projects WHERE id = ?", pid).Scan(&next); err != nil || next != n+1 {
		t.Fatalf("next_number %d %v", next, err)
	}
	// A second project allocates from 1, apart.
	newProject(t, s, "OTH")
	if _, key := newTicket(t, s, "OTH", "Other"); key != "OTH-1" {
		t.Fatalf("the other project's first key %q", key)
	}
}

func TestAKeyIsKeptThroughADelete(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	id, key := newTicket(t, s, "CND", "First")
	if key != "CND-1" {
		t.Fatalf("key %q", key)
	}
	rc, err := s.DeleteTicket(ctx, testOp("del"), "CND-1")
	want(t, rc, err, StatusDone, "")
	var kept string
	var number int64
	var deleted string
	if err := s.db.QueryRow("SELECT key, number, deleted_at FROM tickets WHERE id = ?", id).Scan(&kept, &number, &deleted); err != nil {
		t.Fatal(err)
	}
	if kept != "CND-1" || number != 1 || deleted == "" {
		t.Fatalf("after the delete: %s %d %q", kept, number, deleted)
	}
	// The number is never reassigned: the next ticket is CND-2.
	if _, key := newTicket(t, s, "CND", "Second"); key != "CND-2" {
		t.Fatalf("the next key %q", key)
	}
	rc, err = s.UndeleteTicket(ctx, testOp("undel"), "CND-1")
	want(t, rc, err, StatusDone, "")
	if err := s.db.QueryRow("SELECT key, deleted_at FROM tickets WHERE id = ?", id).Scan(&kept, &deleted); err != nil || kept != "CND-1" || deleted != "" {
		t.Fatalf("after the undelete: %q %q %v", kept, deleted, err)
	}
	if _, key := newTicket(t, s, "CND", "Third"); key != "CND-3" {
		t.Fatalf("the key after the undelete %q", key)
	}
}

func TestTheReplicationRangeReadsBackAsZero(t *testing.T) {
	s := openStore(t, storeDir(t))
	pid := newProject(t, s, "CND")
	newTicket(t, s, "CND", "First")
	var from, to, next int64
	if err := s.db.QueryRow("SELECT number_from, number_to, next_number FROM projects WHERE id = ?", pid).Scan(&from, &to, &next); err != nil {
		t.Fatal(err)
	}
	if from != 0 || to != 0 || next != 2 {
		t.Fatalf("number_from %d number_to %d next_number %d", from, to, next)
	}
}

func TestAllocatingFromNoProjectIsAnError(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	_, err := s.Apply(ctx, testOp("t1"), "test.allocate", nil, func(x *tx) error {
		_, err := allocateNumber(x, "no-such-project")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "no such project") {
		t.Fatalf("allocating from no project: %v", err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM ops"); n != 0 {
		t.Fatalf("%d receipts after a database error", n)
	}
	// A ticket of a project with no row is refused before any number is
	// allocated.
	rc, err := s.CreateTicket(ctx, testOp("t2"), NewTicket{Project: "NOPE", Title: "Orphan"})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	if n := count(t, s, "SELECT COUNT(*) FROM tickets"); n != 0 {
		t.Fatalf("%d tickets", n)
	}
}
