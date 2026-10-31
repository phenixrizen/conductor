//go:build unix

package ledger

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A new ledger's database, its -wal and its -shm are all 0600 under a
// umask that makes files 0644: SQLite makes the -wal and -shm with the
// database file's mode as it is at the first transaction, so the file is
// made private before the transaction that proves it writes (#444).
func TestANewLedgersFilesArePrivate(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
	dir := filepath.Join(t.TempDir(), "runs")
	l := openLedger(t, dir)
	newRun(t, l, "team-00000001")
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
// read-only and is 0600 afterwards: made private whatever the open finds.
func TestANewerLedgerIsMadePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runs")
	l := openLedger(t, dir)
	newRun(t, l, "team-00000001")
	if _, err := l.db.Exec("UPDATE schema_version SET version = ?", Version+1); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, File)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	again := openLedger(t, dir)
	if ro, why := again.ReadOnly(); !ro {
		t.Fatalf("read-only %v: %s", ro, why)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file's mode after the open: %v %v", fi.Mode(), err)
	}
}
