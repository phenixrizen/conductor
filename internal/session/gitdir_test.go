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
	// git ends the path at the first NUL: what follows does not count.
	if err := os.WriteFile(filepath.Join(root, "gitfile3"), []byte("gitdir: "+filepath.Join(root, ".git", "worktrees", "wt")+"\x00\nignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for dir, to := range map[string]string{"elsewhere": "../gitfile2", "elsewhere3": "../gitfile3"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(to, filepath.Join(root, dir, ".git")); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := newLocal(t, root)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range []string{"unborn/config", "unborn/hooks/post-checkout", "gitfile2", "gitfile3"} {
		id := fmt.Sprintf("p%d", i)
		save(s, sub, id, p, []byte("gitdir: /elsewhere\n"), 512, "", true)
		if f := fileReply(t, sink, id); f.Kind != "error" || f.Error == nil || f.Error.Code != "read_only" {
			t.Errorf("save %s: %+v %+v", p, f, f.Error)
		}
	}
	for _, p := range []string{"unborn/config", "gitfile2", "gitfile3"} {
		if h, _ := ReadPath(root, p, false, nil); h.Kind != "file" || !h.ReadOnly {
			t.Errorf("read %s: %+v", p, h)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "gitfile2")); !strings.Contains(string(b), "worktrees") {
		t.Fatalf("gitfile2 changed: %q", b)
	}
	if nvim.Available() {
		for i, p := range []string{"unborn/config", "gitfile2", "gitfile3"} {
			if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: fmt.Sprintf("n%d", i), Path: p}); !errors.Is(err, errGitDir) {
				t.Errorf("Neovim on %s: %v", p, err)
			}
		}
	}
}

// What the .git check and the session's branch read of a .git file, a
// commondir or a HEAD is read only when it is a regular file, and bounded
// (a .git file up to 1 MiB, as git takes one): one that is a FIFO, a link
// to /dev/zero or too long neither blocks nor fills memory; it counts for
// nothing, but a commondir that cannot be read leaves its repository read
// only.
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
		// wt's git directory has a commondir that cannot be read (a link to
		// /dev/zero): what it stands for is not known, so wt is read only.
		if len(marked) != 1 || marked[0] != "wt" {
			t.Fatalf("marked as a git directory: %q, want wt alone", marked)
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

// A .git file is parsed as git parses one ("gitdir: ", trailing line
// breaks dropped, the path up to the first NUL, up to 1 MiB) and counts
// when its path, absolute or from its own directory, is a git directory: a
// padded, CRLF or NUL-ended pointer counts; a YAML file with a gitdir key,
// on one line or more, does not, nor does a pointer to nowhere. A HEAD
// counts by its first bytes, a link by its refs/ target, an object id in
// either case.
func TestGitFilesAndHeadsAsGitReadsThem(t *testing.T) {
	if target, ok := readGitFile(writeTemp(t, "gitdir: \x00x\n")); !ok || target != "" {
		t.Fatalf("an empty path after a NUL: %q %v", target, ok)
	}
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
		"relative":     {"gitdir: .git/worktrees/wt\n", true},
		"nowhere":      {"gitdir: ../somewhere/.git/worktrees/x\n", false},
		"yaml1":        {"gitdir: null\n", false},
		"emptypath":    {"gitdir: \x00x\n", false},
		"nul":          {"gitdir: " + wtGitDir + "\x00\nignored\n", true},
		"relnul":       {"gitdir: .git/worktrees/wt\x00 trailing\n", true},
		"relmultiline": {"gitdir: notes\nmore: lines\n", false},
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

// Metadata a repository's .git links out to the working tree (its config,
// its hooks folder, its refs folder) is read only there too, as is the
// common directory a directory-form .git's commondir names (with no HEAD of
// its own), and a pointer whose absolute path takes a link and then .., as
// the kernel and git walk it. A working tree's file next to them, and a
// one-line YAML file with a gitdir key, still save.
func TestFileWriteRefusesMetadataLinkedOutAndPointersAsGitWalksThem(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root, err := filepath.EvalSymlinks(gitRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// .git/config and .git/hooks are links into the working tree.
	must(os.Rename(filepath.Join(root, ".git", "config"), filepath.Join(root, "settings")))
	must(os.Symlink("../settings", filepath.Join(root, ".git", "config")))
	must(os.MkdirAll(filepath.Join(root, "tools", "hooks"), 0o755))
	must(os.WriteFile(filepath.Join(root, "tools", "hooks", "pre-commit"), []byte("#!/bin/sh\n"), 0o755))
	must(os.RemoveAll(filepath.Join(root, ".git", "hooks")))
	must(os.Symlink("../tools/hooks", filepath.Join(root, ".git", "hooks")))
	must(os.Symlink(".git", filepath.Join(root, "meta")))
	// A pointer to actual/repo through jump/.. (jump links to actual/child).
	must(os.MkdirAll(filepath.Join(root, "actual", "child"), 0o755))
	if _, err := gitcli.Run(context.Background(), root, "init", "-q", "--bare", filepath.Join(root, "actual", "repo")); err != nil {
		t.Fatal(err)
	}
	must(os.Symlink("actual/child", filepath.Join(root, "jump")))
	must(os.WriteFile(filepath.Join(root, "ptr4"), []byte("gitdir: "+filepath.Join(root, "jump")+"/../repo\n"), 0o644))
	must(os.Mkdir(filepath.Join(root, "sib"), 0o755))
	must(os.Symlink("../ptr4", filepath.Join(root, "sib", ".git")))
	// .git/refs links out to ref-store.
	must(os.Rename(filepath.Join(root, ".git", "refs"), filepath.Join(root, "ref-store")))
	must(os.Symlink("../ref-store", filepath.Join(root, ".git", "refs")))
	// A folder whose .git is a directory with a commondir naming shared,
	// which holds the configuration, refs and objects but no HEAD.
	must(os.MkdirAll(filepath.Join(root, "split", ".git"), 0o755))
	must(os.WriteFile(filepath.Join(root, "split", ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	must(os.WriteFile(filepath.Join(root, "split", ".git", "commondir"), []byte("../shared\n"), 0o644))
	for _, d := range []string{"refs", "objects"} {
		must(os.MkdirAll(filepath.Join(root, "split", "shared", d), 0o755))
	}
	must(os.WriteFile(filepath.Join(root, "split", "shared", "config"), []byte("[core]\n"), 0o644))
	// .git/config.worktree links to a file not made yet.
	must(os.Symlink("../worktree-settings", filepath.Join(root, ".git", "config.worktree")))
	// A commondir whose path ends in a space, which git keeps.
	must(os.MkdirAll(filepath.Join(root, "spaced", ".git"), 0o755))
	must(os.WriteFile(filepath.Join(root, "spaced", ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	must(os.WriteFile(filepath.Join(root, "spaced", ".git", "commondir"), []byte("../shared \n"), 0o644))
	for _, d := range []string{"refs", "objects"} {
		must(os.MkdirAll(filepath.Join(root, "spaced", "shared ", d), 0o755))
	}
	must(os.WriteFile(filepath.Join(root, "spaced", "shared ", "config"), []byte("[core]\n"), 0o644))
	// .git/shallow links to alias, which links to a file not made yet.
	must(os.Symlink("../alias", filepath.Join(root, ".git", "shallow")))
	must(os.Symlink("settings3", filepath.Join(root, "alias")))
	// .git/reftable links out to reftable-store.
	must(os.MkdirAll(filepath.Join(root, "reftable-store"), 0o755))
	must(os.WriteFile(filepath.Join(root, "reftable-store", "tables.list"), []byte("x\n"), 0o644))
	must(os.Symlink("../reftable-store", filepath.Join(root, ".git", "reftable")))
	// A commondir padded past what is read: its repository is read only.
	must(os.MkdirAll(filepath.Join(root, "bigcommon", ".git"), 0o755))
	must(os.WriteFile(filepath.Join(root, "bigcommon", ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	must(os.WriteFile(filepath.Join(root, "bigcommon", ".git", "commondir"), []byte("../shared"+strings.Repeat("\n", maxGitFile)), 0o644))
	for _, d := range []string{"refs", "objects"} {
		must(os.MkdirAll(filepath.Join(root, "bigcommon", "shared", d), 0o755))
	}
	must(os.WriteFile(filepath.Join(root, "bigcommon", "shared", "config"), []byte("[core]\n"), 0o644))
	// A hard link to .git's HEAD in the working tree is the same file.
	must(os.Link(filepath.Join(root, ".git", "HEAD"), filepath.Join(root, "head-copy")))
	// .git/info/sparse-checkout links to Settings5, not made yet: a save of
	// settings5 is refused too, as a case-folding file system takes it.
	must(os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755))
	must(os.Symlink("../../Settings5", filepath.Join(root, ".git", "info", "sparse-checkout")))
	// project's .git links to git-pointer, not made yet: a save there would
	// make the folder's .git file.
	must(os.Mkdir(filepath.Join(root, "project"), 0o755))
	must(os.Symlink("git-pointer", filepath.Join(root, "project", ".git")))
	// A one-line YAML file with a gitdir key: an ordinary file.
	must(os.WriteFile(filepath.Join(root, "settings.yml"), []byte("gitdir: null\n"), 0o644))

	s, _ := newLocal(t, root)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.closeNvims(sub) })
	settings, _ := os.ReadFile(filepath.Join(root, "settings"))
	head, _ := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	branchRef := filepath.Join("ref-store", strings.TrimPrefix(strings.TrimSpace(string(head)), "ref: refs/"))
	if _, err := os.Stat(filepath.Join(root, branchRef)); err != nil {
		t.Fatalf("the branch's loose ref: %v", err)
	}
	refused := []string{"settings", "meta/config", "tools/hooks/pre-commit", "tools/hooks/post-checkout", "ptr4", branchRef, "split/shared/config", "worktree-settings", "spaced/shared /config", "settings3", "reftable-store/tables.list", "bigcommon/shared/config", "bigcommon/shared/new", "head-copy", "settings5", "project/git-pointer"}
	for i, p := range refused {
		id := fmt.Sprintf("m%d", i)
		save(s, sub, id, p, []byte("[core]\n\tfsmonitor = /bin/true\n"), 512, "", true)
		if f := fileReply(t, sink, id); f.Kind != "error" || f.Error == nil || f.Error.Code != "read_only" {
			t.Errorf("save %s: %+v %+v", p, f, f.Error)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "settings")); !bytes.Equal(b, settings) {
		t.Fatal("the linked config changed")
	}
	if _, err := os.Lstat(filepath.Join(root, "worktree-settings")); err == nil {
		t.Fatal("the file a dangling config.worktree link names was made")
	}
	if _, err := os.Lstat(filepath.Join(root, "settings3")); err == nil {
		t.Fatal("the file at the end of a dangling chain of links was made")
	}
	if _, err := os.Lstat(filepath.Join(root, "project", "git-pointer")); err == nil {
		t.Fatal("the file a dangling .git link names was made")
	}
	if nvim.Available() {
		if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "n-pointer", Path: "project/git-pointer"}); !errors.Is(err, errGitDir) {
			t.Errorf("Neovim on project/git-pointer: %v", err)
		}
	}
	for _, p := range []string{"settings", "tools/hooks/pre-commit", "ptr4", branchRef, "split/shared/config", "spaced/shared /config", "head-copy"} {
		h, _ := ReadPath(root, p, false, nil)
		if h.Kind != "file" || !h.ReadOnly {
			t.Errorf("read %s: %+v", p, h)
			continue
		}
		if nvim.Available() {
			if err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "n-" + p, Path: p}); !errors.Is(err, errGitDir) {
				t.Errorf("Neovim on %s: %v", p, err)
			}
		}
	}
	// Ordinary files still save, a one-line YAML file with a gitdir key too.
	for _, p := range []string{"README.md", "settings.yml"} {
		h, _ := ReadPath(root, p, false, nil)
		if h.ReadOnly {
			t.Errorf("%s reads as ReadOnly", p)
		}
		save(s, sub, "ok-"+p, p, []byte("gitdir: changed\n"), 512, h.Sha256, false)
		if f := fileReply(t, sink, "ok-"+p); f.Kind != "written" {
			t.Errorf("%s: %+v %+v", p, f, f.Error)
		}
	}
}

// A check looks at each git directory once, however many .git on the way
// name it, and at no more than maxScan entries in all: past that the path
// is read only rather than looked at further.
func TestGitDirScanIsBounded(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "shared.git")
	for _, d := range []string{"objects", "refs", "extra"} {
		if err := os.MkdirAll(filepath.Join(shared, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(shared, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	for i := range 100 {
		os.WriteFile(filepath.Join(shared, fmt.Sprintf("entry%03d", i)), nil, 0o644)
	}
	// 200 folders deep, each with a .git naming the same git directory.
	dir := filepath.Join(root, "w")
	for range 200 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+shared+"\n"), 0o644)
		dir = filepath.Join(dir, "d")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "notes.txt")
	os.WriteFile(file, []byte("x\n"), 0o644)
	ids := gitIDs{budget: maxScan, bytes: maxScanBytes}
	for p := file; p != root; p = filepath.Dir(p) {
		gitDirsIn(p, &ids)
	}
	if len(ids.scanned) != 1 || ids.incomplete {
		t.Fatalf("scanned %d git directories, incomplete %v; want the shared one once", len(ids.scanned), ids.incomplete)
	}
	start := time.Now()
	if inGitDir(file) {
		t.Fatal("a working-tree file read as .git")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the check took %v", d)
	}
	// With a budget below what the shared git directory holds, the check
	// stops and the file is read only.
	saved := maxScan
	maxScan = 50
	t.Cleanup(func() { maxScan = saved })
	if !inGitDir(file) {
		t.Fatal("a check past its budget let the file be edited")
	}
	plain := filepath.Join(root, "plain.txt")
	os.WriteFile(plain, nil, 0o644)
	if inGitDir(plain) {
		t.Fatal("a file with no repository on its way read as .git")
	}
	maxScan = saved
	// The shared git directory's commondir, padded, is read once however
	// many .git name the directory; .git files padded past the byte budget
	// in all leave the file read only.
	os.WriteFile(filepath.Join(shared, "commondir"), []byte("."+strings.Repeat("\n", 900_000)), 0o644)
	ids = gitIDs{budget: maxScan, bytes: maxScanBytes}
	for p := file; p != root; p = filepath.Dir(p) {
		gitDirsIn(p, &ids)
	}
	if ids.incomplete || len(ids.scanned) != 1 {
		t.Fatalf("with a padded commondir: incomplete %v, scanned %d", ids.incomplete, len(ids.scanned))
	}
	padded := "gitdir: " + shared + strings.Repeat("\n", 64<<10)
	for d := filepath.Dir(file); d != root; d = filepath.Dir(d) {
		os.WriteFile(filepath.Join(d, ".git"), []byte(padded), 0o644)
	}
	if !inGitDir(file) {
		t.Fatal("200 padded .git files past the byte budget let the file be edited")
	}
}

// writeTemp writes body to a new file in a temporary directory.
func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A FIFO where a git directory's hooks folder should be is not waited on:
// the check of an ordinary file beside it answers at once.
func TestGitDirHooksFIFODoesNotBlock(t *testing.T) {
	root, err := filepath.EvalSymlinks(gitRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".git", "hooks")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, ".git", "hooks"), 0o644); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	done := make(chan bool)
	go func() { done <- gitMetadata("README.md", filepath.Join(root, "README.md")) }()
	select {
	case got := <-done:
		if got {
			t.Fatal("README.md taken for .git")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the check blocked on a FIFO at .git/hooks")
	}
}
