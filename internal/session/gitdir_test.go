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
// commondir or a HEAD is read only when it is a regular file of at most
// 4 KiB: one that is a FIFO, a link to /dev/zero or too long neither blocks
// nor fills memory, and counts for nothing.
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
	os.WriteFile(filepath.Join(mk("long"), ".git"), []byte("gitdir: "+strings.Repeat("x", 5000)+"\n"), 0o644)
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
		filepath.Join(root, "fifo", ".git"):   false,
		filepath.Join(root, "zero", ".git"):   false,
		filepath.Join(root, "long", ".git"):   false,
		filepath.Join(root, "wt", ".git"):     true,
		filepath.Join(root, "missing", "x"):   false,
		filepath.Join(root, "headzero", "nx"): false,
	} {
		if _, ok := readSmall(path); ok != want {
			t.Errorf("readSmall(%s) = %v, want %v", path, ok, want)
		}
	}
}
