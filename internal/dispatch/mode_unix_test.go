//go:build unix

package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A new store's database, its -wal and its -shm are all 0600 under a
// umask that makes files 0644: SQLite makes the -wal and -shm with the
// database file's mode as it is at the first transaction, so the file is
// made private before the schema's transaction (#444).
func TestANewStoresFilesArePrivate(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
	dir := storeDir(t)
	s := openStore(t, dir)
	rc, err := s.AddPerson(context.Background(), testOp("a1"), "Alice")
	want(t, rc, err, StatusDone, "")
	for _, name := range []string{File, File + "-wal", File + "-shm"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %o", name, fi.Mode().Perm())
		}
	}
}

// An existing database at 0644 that a newer Conductor wrote opens
// read-only and is 0600 afterwards, as the ledger's is: made private
// before the schema's transaction refuses it.
func TestANewerDatabaseIsMadePrivate(t *testing.T) {
	dir := storeDir(t)
	s := openStore(t, dir)
	rc, err := s.AddPerson(context.Background(), testOp("a1"), "Alice")
	want(t, rc, err, StatusDone, "")
	if _, err := s.db.Exec("UPDATE schema_version SET version = ?", Version+1); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, File)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	again := openStore(t, dir)
	if ro, why := again.ReadOnly(); !ro || !strings.Contains(why, "newer") {
		t.Fatalf("read-only %v: %s", ro, why)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file's mode after the open: %v %v", fi.Mode(), err)
	}
}
