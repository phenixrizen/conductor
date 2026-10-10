package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/gitcli"
	"github.com/phenixrizen/conductor/internal/nvim"
	"github.com/phenixrizen/conductor/internal/proto"
)

// A bare repository in the working directory is read only whatever git's
// own settings would discover: with safe.bareRepository=explicit in the
// person's configuration (git then refuses to find it), a save to its
// config or a new hook is refused and its files read as ReadOnly. A folder
// that only partly looks like one (a HEAD, a refs folder) is edited as any.
func TestFileWriteRefusesABareRepositoryWhateverGitDiscovers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[safe]\n\tbareRepository = explicit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := gitDirTree(t)
	if err := os.MkdirAll(filepath.Join(root, "notbare", "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "notbare", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	s, _ := newLocal(t, root)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(root, "store", "config"))
	for i, p := range []string{"store/config", "store/hooks/post-checkout", "linked/.git/config"} {
		id := "b" + string(rune('0'+i))
		save(s, sub, id, p, []byte("[core]\n\tfsmonitor = /bin/true\n"), 512, "", true)
		if f := fileReply(t, sink, id); f.Kind != "error" || f.Error == nil || f.Error.Code != "read_only" {
			t.Errorf("%s: %+v %+v", p, f, f.Error)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "store", "config")); !bytes.Equal(b, config) {
		t.Fatal("the bare repository's config changed")
	}
	if _, err := os.Lstat(filepath.Join(root, "store", "hooks", "post-checkout")); err == nil {
		t.Fatal("a hook was made in the bare repository")
	}
	if h, _ := ReadPath(root, "store/config", false, nil); h.Kind != "file" || !h.ReadOnly {
		t.Fatalf("read store/config: %+v", h)
	}
	save(s, sub, "nb", "notbare/HEAD", []byte("just a file\n"), 512, "", true)
	if f := fileReply(t, sink, "nb"); f.Kind != "written" {
		t.Fatalf("notbare/HEAD: %+v %+v", f, f.Error)
	}
}

// A bare repository whose HEAD is a link to a branch not born yet (git
// does not follow it) is still a git directory, and a .git file a .git
// link elsewhere points to is read only wherever it lies: saves are
// refused, reads say ReadOnly, Neovim does not open them.
func TestFileWriteRefusesALinkedHeadAndAPointerFileElsewhere(t *testing.T) {
	root := gitDirTree(t)
	bare := filepath.Join(root, "unborn")
	if _, err := gitcli.Run(context.Background(), root, "init", "-q", "--bare", bare); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bare, "HEAD")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("refs/heads/main", filepath.Join(bare, "HEAD")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gitfile2"), []byte("gitdir: "+filepath.Join(root, ".git", "worktrees", "wt")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../gitfile2", filepath.Join(root, "elsewhere", ".git")); err != nil {
		t.Fatal(err)
	}
	s, _ := newLocal(t, root)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range []string{"unborn/config", "unborn/hooks/post-checkout", "gitfile2"} {
		id := fmt.Sprintf("p%d", i)
		save(s, sub, id, p, []byte("gitdir: /elsewhere\n"), 512, "", true)
		if f := fileReply(t, sink, id); f.Kind != "error" || f.Error == nil || f.Error.Code != "read_only" {
			t.Errorf("save %s: %+v %+v", p, f, f.Error)
		}
	}
	for _, p := range []string{"unborn/config", "gitfile2"} {
		if h, _ := ReadPath(root, p, false, nil); h.Kind != "file" || !h.ReadOnly {
			t.Errorf("read %s: %+v", p, h)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "gitfile2")); !strings.Contains(string(b), "worktrees") {
		t.Fatalf("gitfile2 changed: %q", b)
	}
	if nvim.Available() {
		for i, p := range []string{"unborn/config", "gitfile2"} {
			if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: fmt.Sprintf("n%d", i), Path: p}); !errors.Is(err, errGitDir) {
				t.Errorf("Neovim on %s: %v", p, err)
			}
		}
	}
}

// What the .git check and the session's branch read of a .git file, a
// commondir or a HEAD is read only when it is a regular file, and bounded
// (a .git file up to 1 MiB, as git takes one): one that is a FIFO, a link
// to /dev/zero or too long neither blocks nor fills memory, and counts for
// nothing.
func TestGitDirReadsAreBounded(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mk := func(parts ...string) string {
		t.Helper()
		p := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fifo := func(path string) {
		t.Helper()
		if err := syscall.Mkfifo(path, 0o644); err != nil {
			t.Skipf("no FIFO here: %v", err)
		}
	}
	zero := func(path string) {
		t.Helper()
		if err := os.Symlink("/dev/zero", path); err != nil {
			t.Fatal(err)
		}
	}
	fifo(filepath.Join(mk("fifo"), ".git"))
	zero(filepath.Join(mk("zero"), ".git"))
	os.WriteFile(filepath.Join(mk("long"), ".git"), []byte("gitdir: "+strings.Repeat("x", maxGitFile)+"\n"), 0o644)
	gd := mk("gd")
	zero(filepath.Join(gd, "commondir"))
	fifo(filepath.Join(gd, "HEAD"))
	os.WriteFile(filepath.Join(mk("wt"), ".git"), []byte("gitdir: "+gd+"\n"), 0o644)
	for _, d := range []string{"headfifo", "headzero"} {
		mk(d, "objects")
		mk(d, "refs")
	}
	fifo(filepath.Join(root, "headfifo", "HEAD"))
	zero(filepath.Join(root, "headzero", "HEAD"))
	dirs := []string{"fifo", "zero", "long", "wt", "headfifo", "headzero"}
	done := make(chan []string)
	go func() {
		var marked []string
		for _, d := range dirs {
			if inGitDir(filepath.Join(root, d, "file.txt")) {
				marked = append(marked, d)
			}
			if b := GitBranch(filepath.Join(root, d)); b != "" {
				marked = append(marked, d+" branch "+b)
			}
		}
		done <- marked
	}()
	select {
	case marked := <-done:
		if len(marked) != 0 {
			t.Fatalf("marked as a git directory: %q", marked)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a FIFO or /dev/zero blocked")
	}
	for path, want := range map[string]bool{
		filepath.Join(root, "fifo", ".git"): false,
		filepath.Join(root, "zero", ".git"): false,
		filepath.Join(root, "long", ".git"): false,
		filepath.Join(root, "wt", ".git"):   true,
		filepath.Join(root, "missing", "x"): false,
	} {
		if _, ok := readGitFile(path); ok != want {
			t.Errorf("readGitFile(%s) = %v, want %v", path, ok, want)
		}
	}
}

// A .git file is taken as git takes one, and nothing else is: "gitdir: "
// then a path on one line, trailing line breaks dropped, up to 1 MiB, so a
// padded pointer counts; a file that only starts like one (YAML with a
// gitdir key and more) does not, nor does one naming an absolute directory
// that is no git directory. A HEAD counts by its first bytes, a link by its
// refs/ target, an object id in either case.
func TestGitFilesAndHeadsAsGitReadsThem(t *testing.T) {
	root := gitDirTree(t)
	wtGitDir := filepath.Join(root, ".git", "worktrees", "wt")
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, c := range map[string]struct {
		body string
		want bool
	}{
		"pointer":      {"gitdir: " + wtGitDir + "\n", true},
		"padded":       {"gitdir: " + wtGitDir + strings.Repeat("\n", 8192), true},
		"crlf":         {"gitdir: " + wtGitDir + "\r\n", true},
		"relative":     {"gitdir: ../somewhere/.git/worktrees/x\n", true},
		"yaml":         {"gitdir: null\nother: value\n", false},
		"nospace":      {"gitdir:" + wtGitDir + "\n", false},
		"notgitdir":    {"gitdir: " + filepath.Join(root, "docs") + "\n", false},
		"empty":        {"gitdir: \n", false},
		"prose":        {"the gitdir: line names the git directory\n", false},
		"overlong.yml": {"gitdir: " + wtGitDir + strings.Repeat("\n", maxGitFile), false},
		"readme.md":    {"# notes\n", false},
	} {
		if got := isGitFile(write(name, c.body)); got != c.want {
			t.Errorf("isGitFile(%s) = %v, want %v", name, got, c.want)
		}
	}
	// A save of an ordinary YAML file that starts with a gitdir key goes through.
	s, _ := newLocal(t, root)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := ReadPath(root, "yaml", false, nil)
	if h.ReadOnly {
		t.Fatalf("yaml read as ReadOnly: %+v", h)
	}
	save(s, sub, "y", "yaml", []byte("gitdir: null\nother: changed\n"), 512, h.Sha256, false)
	if f := fileReply(t, sink, "y"); f.Kind != "written" {
		t.Fatalf("yaml save: %+v %+v", f, f.Error)
	}
	// HEADs.
	sha := strings.Repeat("ab", 20)
	for name, c := range map[string]struct {
		body string
		want bool
	}{
		"ref":       {"ref: refs/heads/main\n", true},
		"refspaced": {"ref:   refs/heads/main\n", true},
		"padded":    {"ref: refs/heads/main" + strings.Repeat(" ", 8192), true},
		"lower":     {sha + "\n", true},
		"upper":     {strings.ToUpper(sha) + "\n", true},
		"notrefs":   {"ref: heads/main\n", false},
		"short":     {sha[:30] + "\n", false},
		"words":     {"hello\n", false},
	} {
		d := filepath.Join(root, "heads", name)
		for _, sub := range []string{"objects", "refs"} {
			if err := os.MkdirAll(filepath.Join(d, sub), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		os.WriteFile(filepath.Join(d, "HEAD"), []byte(c.body), 0o644)
		if got := isGitDirectory(d); got != c.want {
			t.Errorf("isGitDirectory(heads/%s) = %v, want %v", name, got, c.want)
		}
	}
	link := filepath.Join(root, "heads", "link")
	for _, sub := range []string{"objects", "refs"} {
		os.MkdirAll(filepath.Join(link, sub), 0o755)
	}
	os.Symlink("refs/heads/unborn", filepath.Join(link, "HEAD"))
	if !isGitDirectory(link) {
		t.Error("a HEAD linked to an unborn branch")
	}
	// A bare repository with an upper-case detached HEAD is read only.
	if err := os.WriteFile(filepath.Join(root, "store", "HEAD"), []byte(strings.ToUpper(sha)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	save(s, sub, "u", "store/config", []byte("x\n"), 512, "", true)
	if f := fileReply(t, sink, "u"); f.Kind != "error" || f.Error == nil || f.Error.Code != "read_only" {
		t.Fatalf("store/config with an upper-case HEAD: %+v %+v", f, f.Error)
	}
}
