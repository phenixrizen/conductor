package databaseops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationInputAndPrivateConnectionFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "001_one.sql"), []byte("SELECT 1;"), 0600)
	out, err := ReadMigrations(dir)
	if err != nil || len(out) != 1 || len(out[0].Digest) != 64 {
		t.Fatal(out, err)
	}
	os.WriteFile(filepath.Join(dir, "003_gap.sql"), []byte("SELECT 3;"), 0600)
	if _, err := ReadMigrations(dir); err == nil {
		t.Fatal("gap accepted")
	}
	for _, u := range []string{"postgres://user:secret@example.invalid/db?sslmode=disable", "postgres://user:secret@127.0.0.1/db?options=malicious", "postgres://user:secret@127.0.0.1/db?sslmode=disable&sslmode=verify-full", "postgres://user:bad%0Apassword@127.0.0.1/db"} {
		if _, cleanup, err := serviceFile(u); err == nil {
			cleanup()
			t.Fatal("unsafe database config accepted")
		}
	}
	path, cleanup, err := serviceFile("postgres://user:synthetic-secret@127.0.0.1:5432/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, _ := os.Stat(path)
	b, _ := os.ReadFile(path)
	if info.Mode().Perm() != 0600 || !strings.Contains(string(b), "password=synthetic-secret\n") {
		t.Fatal("private service file incorrect")
	}
}
