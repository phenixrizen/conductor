package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type doc struct {
	N int `json:"n"`
}

func TestOpenCreatesDirAndRejectsUnwritable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "conductor.d")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir %v %v", fi, err)
	}
	if s.Dir() != dir {
		t.Fatalf("Dir %q", s.Dir())
	}
	if _, err := Open(filepath.Join(os.DevNull, "x")); err == nil {
		t.Fatal("expected error for unwritable parent")
	}
}

func TestLoadMissingSaveRoundTrip(t *testing.T) {
	s, _ := Open(t.TempDir())
	var d doc
	if ok, err := s.Load("catalog.json", &d); ok || err != nil {
		t.Fatalf("missing: %v %v", ok, err)
	}
	if err := s.Save("catalog.json", doc{N: 7}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Load("catalog.json", &d); !ok || err != nil || d.N != 7 {
		t.Fatalf("round trip: %v %v %+v", ok, err, d)
	}
	fi, _ := os.Stat(filepath.Join(s.Dir(), "catalog.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v", fi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(s.Dir(), "*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestSaveRejectsBadNames(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, name := range []string{"../x.json", "a/b.json", "x.txt", "", "X.json"} {
		if err := s.Save(name, doc{}); err == nil {
			t.Fatalf("%q accepted", name)
		}
	}
}

func TestConcurrentSavesLeaveValidJSON(t *testing.T) {
	s, _ := Open(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = s.Save("c.json", doc{N: i}) }(i)
	}
	wg.Wait()
	b, err := os.ReadFile(filepath.Join(s.Dir(), "c.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d doc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("torn file: %v %q", err, b)
	}
}

func TestLoadReportsBadNamesAndCorruptFiles(t *testing.T) {
	s, _ := Open(t.TempDir())
	var d doc
	if _, err := s.Load("../x.json", &d); err == nil {
		t.Fatal("bad name accepted by Load")
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "bad.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Load("bad.json", &d); ok || err == nil {
		t.Fatalf("corrupt file: ok=%v err=%v", ok, err)
	}
}

func TestFailedSaveKeepsPreviousDocument(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.Save("c.json", doc{N: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("c.json", make(chan int)); err == nil {
		t.Fatal("expected an encode error")
	}
	var d doc
	if ok, err := s.Load("c.json", &d); !ok || err != nil || d.N != 1 {
		t.Fatalf("previous document lost: %v %v %+v", ok, err, d)
	}
	if left, _ := filepath.Glob(filepath.Join(s.Dir(), "*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// writeRaw puts a hand-written file into the store directory, as a user editing
// the data directory would.
func writeRaw(t *testing.T, s *Store, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.Dir(), name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A typo in a hand-edited document must fail the load, not be dropped silently,
// at the top level and inside list elements (the catalog overlay is a list).
func TestLoadRejectsUnknownFields(t *testing.T) {
	type item struct {
		N int `json:"n"`
	}
	type list struct {
		Items []item `json:"items"`
	}
	s, _ := Open(t.TempDir())
	for _, tc := range []struct{ name, body, field string }{
		{"top level", `{"items":[],"extra":1}`, "extra"},
		{"inside a list element", `{"items":[{"n":1},{"n":2,"nn":3}]}`, "nn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeRaw(t, s, "c.json", tc.body)
			var l list
			ok, err := s.Load("c.json", &l)
			if ok || err == nil {
				t.Fatalf("accepted: ok=%v err=%v %+v", ok, err, l)
			}
			if msg := err.Error(); !strings.Contains(msg, "c.json") || !strings.Contains(msg, tc.field) {
				t.Fatalf("error should name the file and the field %q: %v", tc.field, err)
			}
		})
	}
}

// Decoder.Decode stops after the first value, so Load has to insist on the end
// of input itself; json.Unmarshal used to do that for free.
func TestLoadRejectsTrailingData(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, tc := range []struct{ name, body string }{
		{"text", `{"n":1} oops`},
		{"stray brace", `{"n":1}}`},
		{"second value", `{"n":1}{"n":2}`},
		{"second value on a new line", "{\"n\":1}\n{\"n\":2}\n"},
		{"truncated second value", `{"n":1}{"n":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeRaw(t, s, "c.json", tc.body)
			var d doc
			ok, err := s.Load("c.json", &d)
			if ok || err == nil {
				t.Fatalf("accepted: ok=%v err=%v %+v", ok, err, d)
			}
			if !strings.Contains(err.Error(), "c.json") {
				t.Fatalf("error should name the file: %v", err)
			}
		})
	}

	// Whitespace after the value is fine: Save ends its files with a newline.
	writeRaw(t, s, "c.json", "{\"n\":5}\n\n \t\r\n")
	var d doc
	if ok, err := s.Load("c.json", &d); !ok || err != nil || d.N != 5 {
		t.Fatalf("trailing whitespace: %v %v %+v", ok, err, d)
	}
}

// An empty or cut-off file is damage, never an empty document: callers must not
// be able to mistake it for "nothing stored yet" and overwrite it on the next Save.
func TestLoadRejectsEmptyAndTruncatedFiles(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, tc := range []struct{ name, body, want string }{
		{"empty file", "", "empty document"},
		{"only whitespace", " \n\t\n", "empty document"},
		{"truncated value", `{"n":`, "unexpected EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeRaw(t, s, "c.json", tc.body)
			var d doc
			ok, err := s.Load("c.json", &d)
			if ok || err == nil {
				t.Fatalf("accepted: ok=%v err=%v %+v", ok, err, d)
			}
			if msg := err.Error(); !strings.Contains(msg, "c.json") || !strings.Contains(msg, tc.want) {
				t.Fatalf("error should name the file and say %q: %v", tc.want, err)
			}
		})
	}
}
