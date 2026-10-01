package agents

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
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
	if _, ok := FileOwner(fi); !ok {
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
	} else if _, ok := FileOwner(fi); !ok {
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
	} else if _, ok := FileOwner(fi); !ok {
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
	if _, ok := FileOwner(fi); !ok {
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
		// Creating a file takes search permission as well as write.
		if err := os.Chmod(home, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ownedBy(home, fi, someoneElse); err == nil {
			t.Fatal("an own home it cannot search was accepted")
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
	if owner, ok := FileOwner(fi); !ok || owner == 0 {
		t.Skip("needs a home that is not root's")
	}
	t.Setenv("HOME", home)
	if err := ownedBy(home, fi, 0); err == nil || !strings.Contains(err.Error(), "sudo -u") {
		t.Fatalf("root in a user's home: %v", err)
	}
}

// CheckOwner passes a file of one's own and refuses another user's, naming
// the file and saying nothing was written; a missing path is its own error;
// where the system cannot say who owns a file, it passes.
func TestCheckOwnerRefusesAnotherUsersFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckOwner(path); err != nil {
		t.Fatalf("own file: %v", err)
	}
	if err := CheckOwner(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := FileOwner(fi); !ok {
		t.Skip("this platform does not report who owns a file")
	}
	was := geteuid
	geteuid = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { geteuid = was })
	if err := CheckOwner(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "belongs to "+ownerName(os.Geteuid())) || !strings.Contains(err.Error(), "nothing written") {
		t.Fatalf("another user's file: %v", err)
	}
	// The own-home exception passes the process's home itself, never a file in it.
	t.Setenv("HOME", filepath.Dir(path))
	if err := CheckOwner(path); err == nil {
		t.Fatal("a file in the process's own home passed as if it were the home")
	}
}

// ownerName is how the owner checks name the user uid.
func ownerName(uid int) string {
	who := strconv.Itoa(uid)
	if u, err := user.LookupId(who); err == nil && u.Username != "" {
		who = u.Username
	}
	return who
}

// rootFile returns a regular file root owns, and skips the test where there
// is none, or where the tests run as root.
func rootFile(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("needs a user other than root")
	}
	for _, p := range []string{"/etc/passwd", "/etc/hosts"} {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			if owner, ok := FileOwner(fi); ok && owner == 0 {
				return p
			}
		}
	}
	t.Skip("no regular file owned by root")
	return ""
}

// CheckOwner follows a link, so that under sudo a link of the user's to a
// file of root's passes it; CheckLinkOwner judges the link itself, by its own
// owner, and names it. A file that is not a link it judges as CheckOwner does.
func TestCheckLinkOwnerJudgesTheLinkItself(t *testing.T) {
	dir := t.TempDir()
	own := filepath.Join(dir, "zshrc")
	if err := os.WriteFile(own, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".zshrc")
	if err := os.Symlink(own, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckLinkOwner(link); err != nil {
		t.Fatalf("own link: %v", err)
	}
	if err := CheckLinkOwner(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	target := rootFile(t)
	toRoot := filepath.Join(dir, ".bashrc")
	if err := os.Symlink(target, toRoot); err != nil {
		t.Fatal(err)
	}
	was := geteuid
	t.Cleanup(func() { geteuid = was })
	geteuid = func() int { return 0 }
	if err := CheckOwner(toRoot); err != nil {
		t.Fatalf("CheckOwner follows the link to root's file, which root owns: %v", err)
	}
	err := CheckLinkOwner(toRoot)
	if err == nil || !strings.Contains(err.Error(), toRoot+", a link,") || !strings.Contains(err.Error(), "belongs to "+ownerName(os.Geteuid())) || !strings.Contains(err.Error(), "nothing written") {
		t.Fatalf("a link of another user's to root's file: %v", err)
	}
	geteuid = func() int { return os.Geteuid() + 1 }
	if err := CheckLinkOwner(own); err == nil || strings.Contains(err.Error(), "a link") {
		t.Fatalf("another user's file, not a link: %v", err)
	}
}

// A link of one's own to another user's file is refused by CheckOwner for
// what it is: the link leads to a file of theirs. The refusal names the link
// and the owner of the file, and does not tell the user to run conductor as
// that owner: the rc file is the user's, the file it leads to is not.
func TestCheckOwnerNamesALinkToAnotherUsersFile(t *testing.T) {
	target := rootFile(t)
	link := filepath.Join(t.TempDir(), ".zshrc")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckLinkOwner(link); err != nil {
		t.Fatalf("the link is the user's own: %v", err)
	}
	err := CheckOwner(link)
	if err == nil || !strings.Contains(err.Error(), link+" leads to a file of "+ownerName(0)+"'s") || !strings.Contains(err.Error(), "nothing written") {
		t.Fatalf("a link of one's own to root's file: %v", err)
	}
	if strings.Contains(err.Error(), "belongs to") || strings.Contains(err.Error(), "sudo") {
		t.Fatalf("the refusal says the link is root's, or to run as root: %v", err)
	}
}

// SetEUIDForTest replaces the user the checks take and restores it.
func TestSetEUIDForTest(t *testing.T) {
	restore := SetEUIDForTest(4242)
	if geteuid() != 4242 {
		restore()
		t.Fatalf("geteuid %d", geteuid())
	}
	restore()
	if geteuid() != os.Geteuid() {
		t.Fatalf("restored geteuid %d, want %d", geteuid(), os.Geteuid())
	}
}
