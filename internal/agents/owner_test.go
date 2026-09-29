package agents

import (
	"errors"
	"os"
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
