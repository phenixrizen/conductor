package store

import (
	"encoding/json"
	"os"
	"path/filepath"
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
