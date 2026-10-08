package gitcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := Run(context.Background(), dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q")
	run("config", "user.name", "t")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	os.MkdirAll(filepath.Join(dir, "internal", "api"), 0o755)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n\none\ntwo\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "internal", "api", "users.go"), []byte("package api\n\nfunc A() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "old.txt"), []byte("gone\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pic.bin"), []byte{0, 1, 2, 3}, 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

// The status lists what changed against HEAD with each file's lines: a
// modified file by git's count, an added file by its own lines, a deleted
// one, an untracked one, a binary one as none; the branch and the base's id.
func TestGetStatusListsChangesWithTheirLines(t *testing.T) {
	dir := repo(t)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n\none\nthree\nfour\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "internal", "api", "users_test.go"), []byte("package api\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644)
	os.Remove(filepath.Join(dir, "old.txt"))
	os.WriteFile(filepath.Join(dir, "pic.bin"), []byte{0, 9, 9, 9, 9}, 0o644)
	os.WriteFile(filepath.Join(dir, "new.bin"), []byte{0, 1}, 0o644)
	st, err := GetStatus(context.Background(), filepath.Join(dir, "internal"), "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Top != dir && !strings.HasSuffix(st.Top, filepath.Base(dir)) {
		t.Fatalf("top %q", st.Top)
	}
	if st.Branch == "" || st.Base == "" || len(st.Base) < 7 {
		t.Fatalf("branch %q base %q", st.Branch, st.Base)
	}
	got := map[string]Change{}
	for _, c := range st.Changes {
		got[c.Path] = c
	}
	want := map[string]Change{
		"README.md":                  {Path: "README.md", Status: "M", Added: 2, Removed: 1},
		"internal/api/users_test.go": {Path: "internal/api/users_test.go", Status: "?", Added: 5},
		"old.txt":                    {Path: "old.txt", Status: "D", Removed: 1},
		"pic.bin":                    {Path: "pic.bin", Status: "M", Binary: true},
		"new.bin":                    {Path: "new.bin", Status: "?", Binary: true},
	}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("%s: got %+v want %+v", p, got[p], w)
		}
	}
	if len(st.Changes) != len(want) || st.Added != 7 || st.Removed != 2 || st.Truncated {
		t.Fatalf("status %+v", st)
	}
}

// A clean tree lists nothing; outside a repository it is ErrNotRepo; a base
// that does not resolve leaves Base empty and counts nothing.
func TestGetStatusEdges(t *testing.T) {
	dir := repo(t)
	st, err := GetStatus(context.Background(), dir, "")
	if err != nil || len(st.Changes) != 0 || st.Added != 0 {
		t.Fatalf("clean: %+v %v", st, err)
	}
	if _, err := GetStatus(context.Background(), t.TempDir(), ""); err != ErrNotRepo {
		t.Fatalf("outside a repository: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644)
	st, err = GetStatus(context.Background(), dir, "no-such-base")
	if err != nil || st.Base != "" || len(st.Changes) != 1 || st.Changes[0].Added != 0 {
		t.Fatalf("bad base: %+v %v", st, err)
	}
	if _, err := GetStatus(context.Background(), dir, "--output=x"); err == nil {
		t.Fatal("a base starting with a dash was taken")
	}
}

// The list stops at MaxChanges and says so.
func TestGetStatusStopsAtTheBound(t *testing.T) {
	dir := repo(t)
	for i := 0; i < MaxChanges+3; i++ {
		os.WriteFile(filepath.Join(dir, "f"+strings.Repeat("0", 4-len(itoa(i)))+itoa(i)+".txt"), []byte("x\n"), 0o644)
	}
	st, err := GetStatus(context.Background(), dir, "")
	if err != nil || len(st.Changes) != MaxChanges || !st.Truncated {
		t.Fatalf("%d changes truncated=%v err=%v", len(st.Changes), st.Truncated, err)
	}
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+i/1000%10)) + string(rune('0'+i/100%10)) + string(rune('0'+i/10%10)) + string(rune('0'+i%10)))
}

// Show is a file at a revision; a path the revision lacks is an error; a
// dash or a colon in what is asked for is refused.
func TestShow(t *testing.T) {
	dir := repo(t)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644)
	b, truncated, err := Show(context.Background(), dir, "HEAD", "README.md")
	if err != nil || truncated || string(b) != "# repo\n\none\ntwo\n" {
		t.Fatalf("show: %q %v %v", b, truncated, err)
	}
	if _, _, err := Show(context.Background(), dir, "HEAD", "nope.txt"); err == nil {
		t.Fatal("a missing path showed")
	}
	if _, _, err := Show(context.Background(), dir, "--output=x", "README.md"); err == nil {
		t.Fatal("a dash revision was taken")
	}
	if _, _, err := Show(context.Background(), dir, "HEAD", "-x"); err == nil {
		t.Fatal("a dash path was taken")
	}
	if _, _, err := Show(context.Background(), t.TempDir(), "HEAD", "README.md"); err != ErrNotRepo {
		t.Fatalf("outside a repository: %v", err)
	}
}
