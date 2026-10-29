package dispatch

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
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
	rc, err := createProject(s, testOp("p1"), "cnd")
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = createProject(s, testOp("p2"), "CND")
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
	const n = 50
	keys := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, err := createTicket(s, testOp(opID("t", i)), pid, opID("Ticket ", i))
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
	other := newProject(t, s, "OTH")
	if _, key := newTicket(t, s, other, "Other"); key != "OTH-1" {
		t.Fatalf("the other project's first key %q", key)
	}
}

func TestAKeyIsKeptThroughADelete(t *testing.T) {
	s := openStore(t, storeDir(t))
	pid := newProject(t, s, "CND")
	id, key := newTicket(t, s, pid, "First")
	if key != "CND-1" {
		t.Fatalf("key %q", key)
	}
	rc, err := setFields(s, testOp("del"), EntityTicket, id, field{"deleted_at", stamp(time.Now())})
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
	if _, key := newTicket(t, s, pid, "Second"); key != "CND-2" {
		t.Fatalf("the next key %q", key)
	}
	rc, err = setFields(s, testOp("undel"), EntityTicket, id, field{"deleted_at", ""})
	want(t, rc, err, StatusDone, "")
	if err := s.db.QueryRow("SELECT key FROM tickets WHERE id = ?", id).Scan(&kept); err != nil || kept != "CND-1" {
		t.Fatalf("after the undelete: %q %v", kept, err)
	}
}

func TestTheReplicationRangeReadsBackAsZero(t *testing.T) {
	s := openStore(t, storeDir(t))
	pid := newProject(t, s, "CND")
	newTicket(t, s, pid, "First")
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
	_, err := createTicket(s, testOp("t1"), "no-such-project", "Orphan")
	if err == nil || !strings.Contains(err.Error(), "no such project") {
		t.Fatalf("a ticket of no project: %v", err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM ops"); n != 0 {
		t.Fatalf("%d receipts after a database error", n)
	}
}
