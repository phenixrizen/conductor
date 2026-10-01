package agents

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A home that belongs to another user is refused: what Conductor wrote there
// would belong to the user running it, 0600 in 0700 directories, and the
// home's owner could not use it. The refusal says how to run it as the owner.
func TestOwnedByRefusesAnotherUsersHome(t *testing.T) {
	dir := t.TempDir()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ownedBy(dir, fi, os.Geteuid()); err != nil {
		t.Fatalf("a home of one's own: %v", err)
	}
	if _, ok := fileOwner(fi); !ok {
		t.Skip("this platform does not report who owns a file")
	}
	err = ownedBy(dir, fi, os.Geteuid()+1)
	if err == nil || errors.Is(err, ErrByHand) || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "sudo -u ") {
		t.Fatalf("another user's home: %v", err)
	}
}

// Install checks the home before it writes anything; Status, which writes
// nothing, reads any home.
func TestInstallRefusesAHomeItDoesNotOwn(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	if fi, err := os.Stat(home); err != nil {
		t.Fatal(err)
	} else if _, ok := fileOwner(fi); !ok {
		t.Skip("this platform does not report who owns a file")
	}
	old := geteuid
	geteuid = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { geteuid = old })
	if err := CheckHome(home); err == nil {
		t.Fatal("CheckHome accepted a home of another user")
	}
	for _, a := range All() {
		if a.Install == nil || a.ID == "dsh" {
			continue
		}
		if touched, err := a.Install(home, t.TempDir()); err == nil || errors.Is(err, ErrByHand) || len(touched) != 0 {
			t.Errorf("%s: %q %v", a.ID, touched, err)
		}
		if ok, where := a.Status(home); ok || where == "" {
			t.Errorf("%s: status %v %q", a.ID, ok, where)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("wrote %v", entries)
	}
	geteuid = old
	if err := CheckHome(home); err != nil {
		t.Fatalf("a home of one's own: %v", err)
	}
}

// conductor hooks takes the hooks it installs, and the binary they run, from
// a hooks dir only when it is as conductor serve writes it: the user running
// conductor owns it and its group and others have no permission on it at all
// (0700). A checkout's directory, 0755, is refused with the rest. A hooks dir
// that does not exist holds nothing to take.
func TestCheckHooksDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil { // as WriteAssets makes it
		t.Fatal(err)
	}
	if err := CheckHooksDir(dir); err != nil {
		t.Fatalf("a hooks dir of one's own: %v", err)
	}
	if err := CheckHooksDir(filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("no hooks dir: %v", err)
	}
	if fi, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if _, ok := fileOwner(fi); !ok {
		t.Skip("this platform does not report who owns a file")
	}
	for _, mode := range []fs.FileMode{0o700, 0o500} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := CheckHooksDir(dir); err != nil {
			t.Errorf("mode %o: %v", mode, err)
		}
	}
	for _, mode := range []fs.FileMode{0o755, 0o750, 0o705, 0o710, 0o701, 0o740, 0o704, 0o720, 0o702, 0o775, 0o777} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := CheckHooksDir(dir); err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "chmod 700") {
			t.Errorf("mode %o: %v", mode, err)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := geteuid
	geteuid = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { geteuid = old })
	if err := CheckHooksDir(dir); err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("a hooks dir of another user: %v", err)
	}
}

// The process's own home passes when the process may write it, whoever owns
// it (a container whose HOME belongs to another uid). Another user's home
// does not, and neither does an own home the process cannot write.
func TestOwnedByAcceptsTheProcesssOwnWritableHome(t *testing.T) {
	home := t.TempDir()
	fi, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fileOwner(fi); !ok {
		t.Skip("this platform does not report who owns a file")
	}
	t.Setenv("HOME", home)
	someoneElse := os.Geteuid() + 1 // the home is ours; we write as another user
	if err := ownedBy(home, fi, someoneElse); err != nil {
		t.Fatalf("an own home it can write: %v", err)
	}
	other := t.TempDir()
	ofi, _ := os.Stat(other)
	if err := ownedBy(other, ofi, someoneElse); err == nil {
		t.Fatal("a home that is not HOME was accepted")
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(home, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(home, 0o700) })
		if err := ownedBy(home, fi, someoneElse); err == nil {
			t.Fatal("an own home it cannot write was accepted")
		}
	}
}

// Root never gets the own-home exception: under sudo, HOME may still name the
// invoking user's home, which root can always write, and what root wrote
// there would belong to root.
func TestOwnHomeExceptionNeverCoversRoot(t *testing.T) {
	home := t.TempDir()
	fi, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if owner, ok := fileOwner(fi); !ok || owner == 0 {
		t.Skip("needs a home that is not root's")
	}
	t.Setenv("HOME", home)
	if err := ownedBy(home, fi, 0); err == nil || !strings.Contains(err.Error(), "sudo -u") {
		t.Fatalf("root in a user's home: %v", err)
	}
}
