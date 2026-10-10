package gitcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/gitcli/gitclitest"
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

// Porcelain is the status without the line counts: the top and each change
// with its letter, from a directory inside the tree; outside one, an error.
func TestPorcelain(t *testing.T) {
	dir := repo(t)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644)
	os.Remove(filepath.Join(dir, "old.txt"))
	os.WriteFile(filepath.Join(dir, "internal", "api", "new.go"), []byte("package api\n"), 0o644)
	top, changes, truncated, err := Porcelain(context.Background(), filepath.Join(dir, "internal"))
	if err != nil || truncated {
		t.Fatalf("%v %v", err, truncated)
	}
	if top != dir && !strings.HasSuffix(top, filepath.Base(dir)) {
		t.Fatalf("top %q", top)
	}
	got := map[string]string{}
	for _, c := range changes {
		if c.Added != 0 || c.Removed != 0 {
			t.Fatalf("no line counts expected: %+v", c)
		}
		got[c.Path] = c.Status
	}
	if got["README.md"] != "M" || got["old.txt"] != "D" || got["internal/api/new.go"] != "?" || len(got) != 3 {
		t.Fatalf("%v", got)
	}
	if _, _, _, err := Porcelain(context.Background(), t.TempDir()); err == nil {
		t.Fatal("outside a repository must fail")
	}
}

// The git Conductor runs starts no program the repository's own
// configuration names (gitclitest arms one with a marking script at every
// such place, some through include and includeIf; hooks in .git/hooks and
// through core.hooksPath), and still reads it right: the status with its
// lines, the porcelain status, a file at a revision. Nothing fetches from
// the repository's remote either.
func TestGitStartsNoProgramTheRepositoryNames(t *testing.T) {
	for _, hooksPath := range []bool{false, true} {
		r := gitclitest.New(t, gitclitest.Options{HooksPath: hooksPath})
		ctx := context.Background()
		st, err := GetStatus(ctx, r.Dir, "")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]Change{}
		for _, c := range st.Changes {
			got[c.Path] = c
		}
		want := map[string]Change{
			"conv.txt":  {Path: "conv.txt", Status: "M", Added: 1},
			"clean.dat": {Path: "clean.dat", Status: "M", Added: 1},
			"proc.pdat": {Path: "proc.pdat", Status: "M", Added: 1},
			"inc.idat":  {Path: "inc.idat", Status: "M", Added: 1},
			"new.txt":   {Path: "new.txt", Status: "?", Added: 1},
		}
		for p, w := range want {
			if got[p] != w {
				t.Errorf("hooksPath %v: %s: got %+v want %+v", hooksPath, p, got[p], w)
			}
		}
		if len(st.Changes) != len(want) || st.Branch != "main" || st.Base == "" || st.Added != 5 || st.Removed != 0 {
			t.Fatalf("hooksPath %v: status %+v", hooksPath, st)
		}
		r.NoneFired(t, "the status")
		if _, changes, _, err := Porcelain(ctx, r.Dir); err != nil || len(changes) != len(want) {
			t.Fatalf("porcelain: %+v %v", changes, err)
		}
		r.NoneFired(t, "the porcelain status")
		for path, body := range gitclitest.Committed {
			if b, _, err := Show(ctx, r.Dir, "HEAD", path); err != nil || string(b) != body {
				t.Fatalf("show %s: %q %v", path, b, err)
			}
		}
		r.NoneFired(t, "show")
		// The repository allows the file protocol; the environment allows none.
		if out, err := Run(ctx, r.Dir, "ls-remote", "origin"); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("ls-remote: %q %v", out, err)
		}
		if _, err := Run(ctx, r.Dir, "fetch", "origin"); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("fetch: %v", err)
		}
		r.NoneFired(t, "a transport")
	}
}

// A read runs no filter at all, not even one the person's global
// configuration defines; worktree add, which checks files out, runs such a
// filter (as Git LFS's is set up) so that the files come out as a checkout
// makes them, unless the repository's own configuration redefines it.
func TestFiltersFromOutsideTheRepositoryRunOnlyForACheckout(t *testing.T) {
	r := gitclitest.New(t, gitclitest.Options{})
	ctx := context.Background()
	global := "[filter \"owner\"]\n\tclean = " + r.Script + " owner-clean\n\tsmudge = " + r.Script + " owner-smudge\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".gitconfig"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(r.Dir, "owner.own"), []byte("o\np\n"), 0o644)
	st, err := GetStatus(ctx, r.Dir, "")
	if err != nil {
		t.Fatal(err)
	}
	var own Change
	for _, c := range st.Changes {
		if c.Path == "owner.own" {
			own = c
		}
	}
	if own.Status != "M" || own.Added != 1 {
		t.Fatalf("owner.own: %+v", own)
	}
	r.NoneFired(t, "the status")
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := Run(ctx, r.Dir, "worktree", "add", "-q", "-b", "side", wt, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if fired := r.Fired(t); len(fired) != 1 || fired[0] != "owner-smudge" {
		t.Fatalf("worktree add ran %q, want the global filter's smudge alone", fired)
	}
	if b, err := os.ReadFile(filepath.Join(wt, "owner.own")); err != nil || string(b) != "o\n" {
		t.Fatalf("owner.own in the worktree: %q %v", b, err)
	}
	// The repository redefines the filter: it is the repository's now, and off.
	os.Remove(filepath.Join(r.Marks, "owner-smudge"))
	gitclitest.Git(t, r.Dir, "config", "filter.owner.smudge", r.Script+" repo-smudge")
	if _, err := Run(ctx, r.Dir, "worktree", "add", "-q", "-b", "other", filepath.Join(t.TempDir(), "wt2"), "HEAD"); err != nil {
		t.Fatal(err)
	}
	r.NoneFired(t, "worktree add with the filter redefined")
}

// filterNames reads the names a configuration gives a filter program,
// whatever their case or dots, each once; the repository's own when asked
// (the include and includeIf ones count as the repository's).
func TestFilterNames(t *testing.T) {
	r := gitclitest.New(t, gitclitest.Options{})
	global := "[filter \"owner\"]\n\tclean = x\n[filter \"Dotted.Name\"]\n\tsmudge = y\n\trequired = true\n[filter \"none\"]\n\trequired = true\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".gitconfig"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := filterNames(context.Background(), r.Dir, false)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(all)
	if want := []string{"Dotted.Name", "inc", "owner", "proc", "single"}; !slices.Equal(all, want) {
		t.Fatalf("all: %q, want %q", all, want)
	}
	repoOnly, err := filterNames(context.Background(), r.Dir, true)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(repoOnly)
	if want := []string{"inc", "proc", "single"}; !slices.Equal(repoOnly, want) {
		t.Fatalf("the repository's: %q, want %q", repoOnly, want)
	}
	if names, err := filterNames(context.Background(), t.TempDir(), true); err != nil || len(names) != 0 {
		t.Fatalf("outside a repository: %q %v", names, err)
	}
	// A name -c cannot carry is refused, not passed over.
	gitclitest.Git(t, r.Dir, "config", "filter.a=b.clean", "x")
	if _, err := Run(context.Background(), r.Dir, "status", "--porcelain"); err == nil || !strings.Contains(err.Error(), "cannot be turned off") {
		t.Fatalf("a filter named with =: %v", err)
	}
}
