package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/gitcli"
	"github.com/rocksolidlabs/conductor/internal/nvim"
	"github.com/rocksolidlabs/conductor/internal/proto"
)

// fileReply waits for the FILE frame answering reqID on sink.
func fileReply(t *testing.T, sink *chanSink, reqID string) proto.FileHeader {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		frames := append([][]byte(nil), sink.frames...)
		sink.mu.Unlock()
		for _, fr := range frames {
			f, err := proto.Decode(fr)
			if err != nil || f.Type != proto.TypeFile {
				continue
			}
			h, _, err := proto.DecodeFile(f.Payload)
			if err == nil && h.ReqID == reqID {
				return h
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no FILE reply for %s", reqID)
	return proto.FileHeader{}
}

// save sends body in parts of size n.
func save(s *Local, sub *Subscription, reqID, path string, body []byte, n int, base string, force bool) {
	for off := 0; off < len(body) || off == 0; off += n {
		end := min(off+n, len(body))
		s.FileWrite(sub, proto.FileWrite{ReqID: reqID, Path: path, Offset: int64(off), Total: int64(len(body)), BaseSha256: base, Force: force}, body[off:end])
		if end == len(body) {
			break
		}
	}
}

// A controller saves a file read before: the parts in order, the file
// written with its mode kept, the reply with the new hash, the save in
// Activity as the person's; a file changed on disk since the read is
// refused with what changed it, unless forced; the refusals have codes.
func TestFileWriteSavesAndRefuses(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	dir := s.info.Cwd
	file := filepath.Join(dir, "users.go")
	os.WriteFile(file, []byte("package api\n"), 0o640)
	read, body := ReadPath(dir, "users.go", false, nil)
	if read.Sha256 == "" || read.Mtime == "" || string(body) != "package api\n" {
		t.Fatalf("the read's hash and time: %+v", read)
	}
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	next := []byte("package api\n\n" + strings.Repeat("// a long line of a file saved in parts\n", 40))
	save(s, sub, "w1", "users.go", next, 512, read.Sha256, false)
	h := fileReply(t, sink, "w1")
	if h.Kind != "written" || h.Sha256 != sha256Hex(next) || h.Size != int64(len(next)) || h.Mtime == "" {
		t.Fatalf("written: %+v", h)
	}
	if b, _ := os.ReadFile(file); string(b) != string(next) {
		t.Fatal("the file on disk is not what was saved")
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v, want the file's own 0640", fi.Mode().Perm())
	}
	var saved bool
	for _, e := range s.Activity() {
		if e.Type == ActivityFile && e.Op == FileOpWrite && e.Path == "users.go" && e.Tool == "editor" && e.ByName == "nate" {
			saved = true
		}
	}
	if !saved {
		t.Fatal("the save is not in Activity")
	}
	// The agent edits the file; a save from the old read is refused with what changed it.
	os.WriteFile(file, []byte("package api // the agent's\n"), 0o640)
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpEdit, Path: "users.go", Tool: "Edit", ByName: "codex"})
	save(s, sub, "w2", "users.go", []byte("mine\n"), 512, h.Sha256, false)
	c := fileReply(t, sink, "w2")
	if c.Kind != "error" || c.Error.Code != "changed_on_disk" || c.By != "codex" || c.Tool != "Edit" || c.At == "" || c.Sha256 != sha256Hex([]byte("package api // the agent's\n")) {
		t.Fatalf("changed on disk: %+v %+v", c, c.Error)
	}
	if b, _ := os.ReadFile(file); string(b) != "package api // the agent's\n" {
		t.Fatal("a refused save wrote the file")
	}
	// Save anyway.
	save(s, sub, "w3", "users.go", []byte("mine\n"), 512, h.Sha256, true)
	if f := fileReply(t, sink, "w3"); f.Kind != "written" {
		t.Fatalf("forced: %+v", f)
	}
	// Refusals.
	view, _ := s.AttachWith(AttachOptions{Role: RoleView, Name: "guest"}, sink)
	s.FileWrite(view, proto.FileWrite{ReqID: "v1", Path: "users.go", Total: 1}, []byte("x"))
	if f := fileReply(t, sink, "v1"); f.Error == nil || f.Error.Code != "read_only" {
		t.Fatalf("a view role: %+v", f)
	}
	s.FileWrite(sub, proto.FileWrite{ReqID: "o1", Path: "users.go", Offset: 4, Total: 8}, []byte("abcd"))
	if f := fileReply(t, sink, "o1"); f.Error == nil || f.Error.Code != "out_of_order" {
		t.Fatalf("out of order: %+v", f)
	}
	s.FileWrite(sub, proto.FileWrite{ReqID: "t1", Path: "users.go", Total: proto.MaxFileBytes + 1}, nil)
	if f := fileReply(t, sink, "t1"); f.Error == nil || f.Error.Code != "too_large" {
		t.Fatalf("too large: %+v", f)
	}
	save(s, sub, "d1", "../escape.txt", []byte("x"), 512, "", true)
	if f := fileReply(t, sink, "d1"); f.Error == nil || f.Error.Code != "denied" {
		t.Fatalf("outside the cwd: %+v", f)
	}
	os.Remove(file)
	save(s, sub, "g1", "users.go", []byte("back\n"), 512, h.Sha256, false)
	if f := fileReply(t, sink, "g1"); f.Error == nil || f.Error.Code != "changed_on_disk" {
		t.Fatalf("deleted since: %+v", f)
	}
}

// An unfinished save takes its next part however late it comes (a relayed
// save's parts may be slowed on their way), and goes when the connection's
// next save starts: one at a time, and nothing kept past the connection.
func TestAnUnfinishedSaveWaitsForItsNextPart(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	dir := s.info.Cwd
	file := filepath.Join(dir, "a.txt")
	os.WriteFile(file, []byte("a\n"), 0o644)
	read, _ := ReadPath(dir, "a.txt", false, nil)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("0123456789\n")
	part := func(reqID string, off, end int) {
		s.FileWrite(sub, proto.FileWrite{ReqID: reqID, Path: "a.txt", Offset: int64(off), Total: int64(len(body)), BaseSha256: read.Sha256}, body[off:end])
	}
	part("w1", 0, 5)
	// No clock stands between the parts: what is pending is the save alone.
	sub.writeMu.Lock()
	if w := sub.write; w == nil || w.reqID != "w1" || len(w.buf) != 5 {
		t.Fatalf("pending: %+v", w)
	}
	sub.writeMu.Unlock()
	part("w1", 5, len(body))
	if h := fileReply(t, sink, "w1"); h.Kind != "written" {
		t.Fatalf("a save whose last part came late: %+v %+v", h, h.Error)
	}
	if b, _ := os.ReadFile(file); string(b) != string(body) {
		t.Fatalf("on disk %q", b)
	}
	// A save left unfinished goes when the next starts: its late part is refused.
	now, _ := ReadPath(dir, "a.txt", false, nil)
	read = now
	part("w2", 0, 5)
	part("w3", 0, len(body))
	if h := fileReply(t, sink, "w3"); h.Kind != "written" {
		t.Fatalf("the next save: %+v %+v", h, h.Error)
	}
	part("w2", 5, len(body))
	if h := fileReply(t, sink, "w2"); h.Error == nil || h.Error.Code != "out_of_order" {
		t.Fatalf("a replaced save's late part: %+v", h)
	}
}

// With FileEdit off a controller saves nothing.
func TestFileWriteRefusedWhenEditingIsOff(t *testing.T) {
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, FileEdit: "off"})
	os.WriteFile(filepath.Join(s.info.Cwd, "a.txt"), []byte("a\n"), 0o644)
	sink := newChanSink(false)
	sub, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	s.FileWrite(sub, proto.FileWrite{ReqID: "w1", Path: "a.txt", Total: 2, Force: true}, []byte("b\n"))
	if f := fileReply(t, sink, "w1"); f.Error == nil || f.Error.Code != "read_only" {
		t.Fatalf("editing off: %+v", f)
	}
	if b, _ := os.ReadFile(filepath.Join(s.info.Cwd, "a.txt")); string(b) != "a\n" {
		t.Fatal("written with editing off")
	}
}

// A save and the editor go through the deny list as a read does: a data
// directory with everything in it, a config file and the copies beside it
// are refused to a controller, a file the directory does not hold yet is not
// made, and the files are left as they were; the rest of the folder saves.
func TestFileWriteAndEditorRefuseTheDenyList(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, ".conductor")
	os.MkdirAll(data, 0o700)
	cfg := filepath.Join(dir, "conductor.json")
	files := map[string]string{".conductor/catalog.json": "secret\n", "conductor.json": "secret\n", "conductor.json.bak": "secret\n", "notes.txt": "ordinary\n"}
	for rel, body := range files {
		os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o600)
	}
	p := newFakeProc()
	s := NewLocal(Info{ID: "sess", Cwd: dir, Cols: 80, Rows: 24}, p, Options{ScrollbackBytes: 4096, FileDeny: DenyList([]string{data}, []string{cfg})})
	t.Cleanup(func() { p.exit() })
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate", Owner: true}, sink)
	if err != nil {
		t.Fatal(err)
	}
	refused := []string{".conductor/catalog.json", ".conductor/new.json", filepath.Join(data, "catalog.json"), "conductor.json", "conductor.json.bak", ".conductor.json.swp"}
	for i, rel := range refused {
		id := "d" + strconv.Itoa(i)
		save(s, sub, id, rel, []byte("changed\n"), 512, "", true)
		if f := fileReply(t, sink, id); f.Error == nil || f.Error.Code != "denied" {
			t.Errorf("save %s: %+v, want it refused", rel, f)
		}
	}
	for rel, body := range files {
		if b, _ := os.ReadFile(filepath.Join(dir, rel)); string(b) != body {
			t.Errorf("%s holds %q after the refused saves", rel, b)
		}
	}
	for _, rel := range []string{".conductor/new.json", ".conductor.json.swp"} {
		if _, err := os.Lstat(filepath.Join(dir, rel)); err == nil {
			t.Errorf("a refused save made %s", rel)
		}
	}
	save(s, sub, "ok", "notes.txt", []byte("mine\n"), 512, "", true)
	if f := fileReply(t, sink, "ok"); f.Kind != "written" {
		t.Fatalf("notes.txt: %+v", f)
	}
	if !nvim.Available() {
		t.Log("nvim is not on PATH: the editor's refusals are not checked")
		return
	}
	for i, rel := range refused {
		if err := s.NvimOpen(t.Context(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "n" + strconv.Itoa(i), Path: rel}); err != ErrFileDenied {
			t.Errorf("the editor on %s: %v, want ErrFileDenied", rel, err)
		}
	}
}

// gitDirTree makes a repository to save into: one commit, a linked
// worktree wt (its .git a file), the links meta to .git and cfg to
// .git/config, a bare repository store and a folder linked whose .git is a
// link to it, a folder pointed whose .git is a link to the file gitfile
// beside it (naming wt's git directory), and an empty folder sub. It
// returns the root, its links resolved.
func gitDirTree(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(gitRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := gitcli.AddWorktree(context.Background(), root, filepath.Join(root, "wt"), "side", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitcli.Run(context.Background(), root, "init", "-q", "--bare", filepath.Join(root, "store")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"linked", "sub", "pointed"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pointed", "gitfile"), []byte("gitdir: "+filepath.Join(root, ".git", "worktrees", "wt")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for link, to := range map[string]string{"linked/.git": "../store", "meta": ".git", "cfg": ".git/config", "pointed/.git": "gitfile"} {
		if err := os.Symlink(to, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Nothing in a repository's .git is saved through the Files tab, however
// the path reaches it: the .git directory, a linked worktree's .git file
// and its git directory, a new .git file, a link to .git or into it,
// another spelling of .git, a bare repository and a .git link to one. The
// files stay as they were and none is made. A read of such a file says
// ReadOnly; a working tree's file reads and saves as before.
func TestFileWriteRefusesARepositorysGitDir(t *testing.T) {
	root := gitDirTree(t)
	s, _ := newLocal(t, root)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string][]byte{}
	for _, p := range []string{".git/config", ".git/HEAD", "wt/.git", ".git/worktrees/wt/HEAD", "store/config", "pointed/gitfile"} {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		kept[p] = b
	}
	body := []byte("[core]\n\tfsmonitor = /bin/true\n")
	refused := []string{
		".git/config", ".git/HEAD", ".git/hooks/post-checkout", "meta/config", "meta/hooks/pre-commit", "cfg",
		"wt/.git", ".git/worktrees/wt/HEAD", "sub/.git", ".GIT/config", ".git./config", ".Git",
		"linked/.git/config", "store/config", "store/hooks/post-checkout", filepath.Join(root, ".git", "config"), "pointed/gitfile", "pointed/.git",
	}
	// What must not be made: a path a look finds before any save is not
	// what a save made (on a case-insensitive filesystem, APFS by default,
	// .Git names .git already).
	notMade := []string{".git/hooks/post-checkout", "store/hooks/post-checkout", "sub/.git", ".Git"}
	there := map[string]bool{}
	for _, p := range notMade {
		if _, err := os.Lstat(filepath.Join(root, p)); err == nil {
			there[p] = true
		}
	}
	for i, p := range refused {
		id := fmt.Sprintf("g%d", i)
		save(s, sub, id, p, body, 512, "", true)
		if f := fileReply(t, sink, id); f.Kind != "error" || f.Error == nil || f.Error.Code != "read_only" {
			t.Errorf("%s: %+v %+v", p, f, f.Error)
		}
	}
	for p, b := range kept {
		if now, err := os.ReadFile(filepath.Join(root, p)); err != nil || !bytes.Equal(now, b) {
			t.Errorf("%s changed: %q %v", p, now, err)
		}
	}
	for _, p := range notMade {
		if _, err := os.Lstat(filepath.Join(root, p)); err == nil && !there[p] {
			t.Errorf("%s was made", p)
		}
	}
	for _, p := range []string{".git/config", "cfg", "meta/HEAD", "wt/.git", "store/config", "linked/.git/HEAD", "pointed/gitfile", "pointed/.git"} {
		if h, _ := ReadPath(root, p, false, nil); h.Kind != "file" || !h.ReadOnly {
			t.Errorf("read %s: %+v", p, h)
		}
	}
	for _, p := range []string{"README.md", "wt/README.md", "docs/old.md"} {
		h, _ := ReadPath(root, p, false, nil)
		if h.Kind != "file" || h.ReadOnly {
			t.Errorf("read %s: %+v", p, h)
		}
		save(s, sub, "w-"+p, p, []byte("saved\n"), 512, h.Sha256, false)
		if f := fileReply(t, sink, "w-"+p); f.Kind != "written" {
			t.Errorf("save %s: %+v %+v", p, f, f.Error)
		}
	}
}

// isDotGit takes every spelling a file system may take for .git, and only
// those.
func TestIsDotGit(t *testing.T) {
	for _, n := range []string{".git", ".GIT", ".Git", ".git.", ".git ", ".git. .", "GIT~1", "git~1", ".g\u200cit", "\ufeff.git"} {
		if !isDotGit(n) {
			t.Errorf("%q is .git", n)
		}
	}
	for _, n := range []string{"git", ".gitignore", ".github", "x.git", ".git2", "", ".", ".gi", "GIT~2"} {
		if isDotGit(n) {
			t.Errorf("%q is not .git", n)
		}
	}
}

// Neovim opens no file in a repository's .git (it would write it), however
// the path reaches it; nothing starts. (A working tree's file opens as the
// bridge's own tests show.)
func TestNvimRefusedInARepositorysGitDir(t *testing.T) {
	if !nvim.Available() {
		t.Skip("nvim is not on PATH")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := gitDirTree(t)
	s, _ := newLocal(t, root)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate", Owner: true}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range []string{".git/config", "meta/HEAD", "cfg", "wt/.git", "linked/.git/config", "store/config", "pointed/gitfile"} {
		err := s.NvimOpen(context.Background(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: fmt.Sprintf("n%d", i), Path: p})
		if !errors.Is(err, errGitDir) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if n := s.nvimCount.Load(); n != 0 {
		t.Fatalf("%d editors open", n)
	}
}
