package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

func setupTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "sub", ".git", "objects"), 0o755)
	os.WriteFile(filepath.Join(root, "sub", "file.go"), []byte("package sub\n"), 0o600)
	os.WriteFile(filepath.Join(root, "sub", ".git", "objects", "x"), []byte("obj"), 0o600)
	os.WriteFile(filepath.Join(root, "bin.dat"), []byte("ab\x00cd"), 0o600)
	os.WriteFile(filepath.Join(root, "big.txt"), bytes.Repeat([]byte("z"), MaxFileRead+10), 0o600)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600)
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "escape"))
	os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "inside"))
	return root
}

func TestResolvePath(t *testing.T) {
	root := setupTree(t)
	realRoot, _ := filepath.EvalSymlinks(root)
	ok := []string{"sub/file.go", "./sub/file.go", filepath.Join(root, "sub", "file.go"), "inside/file.go", ".", "", "missing.txt"}
	for _, p := range ok {
		if _, err := ResolvePath(root, p, nil); err != nil {
			t.Errorf("%q: unexpected error %v", p, err)
		}
	}
	bad := []string{"../x", "/etc/passwd", "escape", "sub/.git/objects/x", "a\x00b", "sub/../../x"}
	for _, p := range bad {
		if r, err := ResolvePath(root, p, nil); err == nil {
			t.Errorf("%q: expected error, resolved to %s", p, r)
		}
	}
	if r, _ := ResolvePath(root, "inside/file.go", nil); r != filepath.Join(realRoot, "sub", "file.go") {
		t.Errorf("symlink inside root resolved to %s", r)
	}
}

func TestReadPathKinds(t *testing.T) {
	root := setupTree(t)
	h, body := ReadPath(root, "sub/file.go", false, nil)
	if h.Kind != "file" || string(body) != "package sub\n" || !h.Exists {
		t.Fatalf("file: %+v %q", h, body)
	}
	h, body = ReadPath(root, "bin.dat", false, nil)
	if h.Kind != "file" || !h.Binary || body != nil {
		t.Fatalf("binary: %+v %q", h, body)
	}
	h, body = ReadPath(root, "big.txt", false, nil)
	if !h.Truncated || len(body) != MaxFileRead {
		t.Fatalf("truncated: %+v %d", h, len(body))
	}
	h, _ = ReadPath(root, "sub", false, nil)
	if h.Kind != "dir" || len(h.Entries) != 2 || !h.Entries[0].Dir || h.Entries[1].Name != "file.go" {
		t.Fatalf("dir: %+v", h)
	}
	h, _ = ReadPath(root, "nope", false, nil)
	if h.Kind != "error" || h.Error.Code != "not_found" || h.Exists {
		t.Fatalf("missing: %+v", h)
	}
	h, _ = ReadPath(root, "/etc/passwd", false, nil)
	if h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("denied: %+v", h)
	}
	h, body = ReadPath(root, "sub/file.go", true, nil)
	if h.Kind != "file" || body != nil || !h.Exists {
		t.Fatalf("stat: %+v %q", h, body)
	}
}

// setupDataDir adds a data directory, state/, to a setupTree root, plus a
// symlink to it and a sibling whose name starts with "state".
func setupDataDir(t *testing.T, root string) string {
	t.Helper()
	data := filepath.Join(root, "state")
	for _, dir := range []string{filepath.Join(data, "hooks"), filepath.Join(root, "stateful")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(data, "catalog.json"):          `{"agents":[]}`,
		filepath.Join(root, "stateful", "notes.txt"): "notes",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(data, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestResolvePathDenyList(t *testing.T) {
	root := setupTree(t)
	data := setupDataDir(t, root)
	deny := []string{data}
	denied := []string{"state", "state/", "state/catalog.json", "state/hooks", "state/missing.json", "alias/catalog.json", filepath.Join(data, "catalog.json"), "sub/../state/catalog.json"}
	for _, p := range denied {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%q: resolved to %s, want it denied", p, r)
		}
	}
	// The rest of the root is untouched, a sibling whose name starts with the
	// same letters included.
	for _, p := range []string{".", "sub/file.go", "stateful/notes.txt", "missing.txt"} {
		if _, err := ResolvePath(root, p, deny); err != nil {
			t.Errorf("%q: unexpected error %v", p, err)
		}
	}
	// A deny entry is the directory itself, not its spelling: a symlink to it
	// denies the same files.
	if r, err := ResolvePath(root, "state/catalog.json", []string{filepath.Join(root, "alias")}); err == nil {
		t.Errorf("deny entry given as a symlink: resolved to %s", r)
	}
	// A session whose root is inside the data directory reads nothing at all.
	for _, r := range []string{data, filepath.Join(data, "hooks")} {
		if got, err := ResolvePath(r, ".", deny); err == nil {
			t.Errorf("root %s inside the deny directory: resolved to %s", r, got)
		}
	}
	// A deny directory that does not exist hides nothing beside its path.
	if _, err := ResolvePath(root, "sub/file.go", []string{filepath.Join(root, "gone")}); err != nil {
		t.Errorf("missing deny directory: %v", err)
	}
}

// An entry that is not there is refused by its path, below the deepest
// directory on it that is, compared as a file: through a link on the way
// to it, in another case, through a link on the way that leads nowhere yet
// and at that link's target; a name entry whose directory is not there
// too. A name that only starts the same, or a path beside it, reads; and
// once the entry is made, a read of it is refused as before.
func TestResolvePathDeniesAMissingEntryByItsPath(t *testing.T) {
	root := setupTree(t)
	other := t.TempDir()
	if err := os.Symlink(root, filepath.Join(other, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "later"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		entry string
		paths []string
	}{
		{filepath.Join(root, "gone"), []string{"gone", "gone/catalog.json", "gone/a/b", "GONE/catalog.json", filepath.Join(root, "gone", "x")}},
		{filepath.Join(root, "sub", "gone", "conductor.json"), []string{"sub/gone/conductor.json", "inside/gone/conductor.json", "sub/Gone/Conductor.JSON"}},
		{filepath.Join(other, "link", "gone"), []string{"gone/catalog.json"}},
		{filepath.Join(root, "dangling", "data"), []string{"dangling/data/catalog.json", "later/data/catalog.json"}},
		{filepath.Join(root, "gone", "*conductor.json*"), []string{"gone/conductor.json", "gone/conductor.json.bak", "gone/.Conductor.json.swp", "gone/conductor.json.d/x"}},
	} {
		for _, p := range c.paths {
			if r, err := ResolvePath(root, p, []string{c.entry}); err == nil {
				t.Errorf("entry %s: %q resolved to %s", c.entry, p, r)
			}
		}
	}
	for _, c := range []struct{ entry, path string }{
		{filepath.Join(root, "gone"), "gone-not/catalog.json"},
		{filepath.Join(root, "gone"), "sub/gone"},
		{filepath.Join(root, "sub", "gone", "conductor.json"), "sub/gone/other.json"},
		{filepath.Join(root, "gone", "*conductor.json*"), "gone/agents.json"},
		{filepath.Join(root, "gone", "*conductor.json*"), "conductor.json"},
	} {
		if _, err := ResolvePath(root, c.path, []string{c.entry}); err != nil {
			t.Errorf("entry %s: %q: %v", c.entry, c.path, err)
		}
	}
	// Made since: refused as a file now, and listed nowhere.
	deny := []string{filepath.Join(root, "made")}
	if err := os.MkdirAll(filepath.Join(root, "made"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "made", "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h, body := ReadPath(root, "made/f", false, deny); h.Kind != "error" || h.Error.Code != "denied" || body != nil {
		t.Fatalf("an entry made since: %+v %q", h, body)
	}
}

func TestReadPathReportsDeniedForTheDenyList(t *testing.T) {
	root := setupTree(t)
	data := setupDataDir(t, root)
	for _, stat := range []bool{false, true} {
		for _, p := range []string{"state/catalog.json", "state", "state/missing.json"} {
			h, body := ReadPath(root, p, stat, []string{data})
			if h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || h.Exists || body != nil {
				t.Errorf("%q (stat %t): %+v %q", p, stat, h, body)
			}
		}
	}
	if h, body := ReadPath(root, "stateful/notes.txt", false, []string{data}); h.Kind != "file" || string(body) != "notes" {
		t.Fatalf("ordinary file: %+v %q", h, body)
	}
}

// A deny entry may be a single file, as the server's config file is: that file
// is refused under every name that resolves to it, while the files beside it
// and the directory holding it still read.
func TestResolvePathDenyListFileEntry(t *testing.T) {
	root := setupTree(t)
	secret := filepath.Join(root, "conductor.json")
	for path, body := range map[string]string{
		secret: `{"workbenchToken":"secret"}`,
		filepath.Join(root, "conductor.example.json"): `{}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(secret, filepath.Join(root, "sub", "config-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(secret, filepath.Join(root, "hard.json")); err != nil {
		t.Fatal(err)
	}
	deny := []string{secret}
	for _, p := range []string{"conductor.json", "./conductor.json", secret, "sub/../conductor.json", "sub/config-link", "hard.json"} {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%q: resolved to %s, want it denied", p, r)
		}
		for _, stat := range []bool{false, true} {
			if h, body := ReadPath(root, p, stat, deny); h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || h.Exists || body != nil {
				t.Errorf("%q (stat %t): %+v %q", p, stat, h, body)
			}
		}
	}
	// A deny entry given as a symlink names the file it points to.
	if r, err := ResolvePath(root, "conductor.json", []string{filepath.Join(root, "sub", "config-link")}); err == nil {
		t.Errorf("deny entry given as a symlink: resolved to %s", r)
	}
	if h, body := ReadPath(root, "conductor.example.json", false, deny); h.Kind != "file" || string(body) != "{}" {
		t.Errorf("the sibling file: %+v %q", h, body)
	}
	if h, _ := ReadPath(root, ".", false, deny); h.Kind != "dir" {
		t.Errorf("the directory holding the denied file: %+v", h)
	}
	if h, body := ReadPath(root, "sub/file.go", false, deny); h.Kind != "file" || string(body) != "package sub\n" {
		t.Errorf("a file elsewhere in the root: %+v %q", h, body)
	}
}

// On a case-insensitive file system (macOS and Windows by default) another
// spelling of the data directory names the same directory. The rule compares
// directories, not names, so it holds there too. Skipped where names are case
// sensitive, as on Linux CI.
func TestResolvePathDenyListIgnoresSpelling(t *testing.T) {
	root := setupTree(t)
	data := setupDataDir(t, root)
	a, errA := os.Stat(filepath.Join(data, "catalog.json"))
	b, errB := os.Stat(filepath.Join(root, "STATE", "Catalog.JSON"))
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Skip("the temp directory is on a case-sensitive file system")
	}
	for _, p := range []string{"STATE/catalog.json", "State/Catalog.JSON", "STATE"} {
		if r, err := ResolvePath(root, p, []string{data}); err == nil {
			t.Errorf("%q: resolved to %s, want it denied", p, r)
		}
	}
}

// An entry of the form dir/*name* denies, in dir, every name that contains
// name, folded as a case-insensitive file system folds it: the copies an editor
// or a person leaves beside a config file hold its secrets too, whether they
// start with its name (conductor.json.bak, conductor.json~), end with it, or
// wrap it (.conductor.json.swp, #conductor.json#, .#conductor.json). The files
// beside it with other names still read, and an entry whose directory is gone
// denies nothing outside that directory's path.
func TestResolvePathDeniesCopiesBesideADeniedFile(t *testing.T) {
	root := setupTree(t)
	copies := []string{
		"conductor.json.bak", "conductor.json~", "Conductor.JSON.orig", "conductor.jſon.bak",
		".conductor.json.swp", "#conductor.json#", ".#conductor.json", "old.conductor.json",
	}
	for _, name := range append([]string{"conductor.json", "conductor.example.json", "other.json"}, copies...) {
		if name == ".#conductor.json" {
			continue // an Emacs lock file is a dangling symbolic link, made below
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(`{"workbenchToken":"secret"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("user@host.1234", filepath.Join(root, ".#conductor.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "conductor.json.bak"), filepath.Join(root, "sub", "innocent.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "conductor.json.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "conductor.json.d", "inner"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "conductor.json")
	deny := []string{cfg, filepath.Join(root, "*conductor.json*")}
	denied := append([]string{"conductor.json", "sub/innocent.txt", "conductor.json.swp", "conductor.json.d", "conductor.json.d/inner"}, copies...)
	for _, p := range denied {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%q: resolved to %s, want it denied", p, r)
		}
		for _, statOnly := range []bool{false, true} {
			if h, body := ReadPath(root, p, statOnly, deny); h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || body != nil {
				t.Errorf("%q (stat %t): %+v %q", p, statOnly, h, body)
			}
		}
	}
	for _, p := range []string{"conductor.example.json", "other.json", ".", "sub/file.go", "sub"} {
		if _, err := ResolvePath(root, p, deny); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	if _, err := ResolvePath(root, "other.json", []string{filepath.Join(root, "gone", "*x.json*")}); err != nil {
		t.Fatalf("an entry in a directory that does not exist: %v", err)
	}
}

// The directory of a name entry is compared as a file, like every other entry:
// naming it through a symbolic link denies the same names in the real directory
// and the other way around, and the same name in another directory reads.
func TestResolvePathNameEntryDirectoryIsComparedAsAFile(t *testing.T) {
	root := setupTree(t)
	for _, d := range []string{"real", "elsewhere"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"conductor.json.bak", "notes.txt"} {
			if err := os.WriteFile(filepath.Join(root, d, name), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	deny := []string{filepath.Join(root, "link", "*conductor.json*")}
	for _, p := range []string{"real/conductor.json.bak", "link/conductor.json.bak"} {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%q: resolved to %s, want it denied", p, r)
		}
	}
	for _, p := range []string{"elsewhere/conductor.json.bak", "real/notes.txt", "link/notes.txt"} {
		if _, err := ResolvePath(root, p, deny); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
}

// Names are folded the way a case-insensitive file system folds them (simple
// Unicode folding), which is wider than lower-casing both sides: the long s
// opens conductor.json on APFS, and the Kelvin sign is a K.
func TestContainsFold(t *testing.T) {
	for _, c := range []struct {
		name, sub string
		want      bool
	}{
		{"conductor.json.bak", "conductor.json", true},
		{"Conductor.JSON~", "conductor.json", true},
		{"conductor.jſon.bak", "conductor.json", true},
		{"conductor.json", "conductor.jſon", true},
		{"BAK.\u212Aey", "bak.key", true},
		{"#conductor.json#", "conductor.json", true},
		{"xconductor.jso", "conductor.json", false},
		{"conductor.jsn.bak", "conductor.json", false},
		{"", "a", false},
		{"a", "a", true},
		{"ǆa", "ǅA", true},
	} {
		if got := containsFold(c.name, c.sub); got != c.want {
			t.Errorf("containsFold(%q, %q) = %t, want %t", c.name, c.sub, got, c.want)
		}
	}
}

// A file larger than a viewer's queue takes is read in part: its first bytes,
// marked truncated, in a frame the queue takes whole. Nothing drops the
// viewer as a slow consumer for asking.
func TestAFileLargerThanTheQueueIsReadInPart(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("0123456789abcdef"), (MaxFileRead+64)/16)
	// over and exact are cut; under fits the read but not, with its header,
	// the queue, and is cut too; small is read whole.
	sizes := map[string]int{"over.txt": MaxFileRead + 64, "exact.txt": MaxFileRead, "under.txt": MaxFileRead - 64, "small.txt": 512 << 10}
	for name, n := range sizes {
		if err := os.WriteFile(filepath.Join(dir, name), content[:n], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := newLocal(t, dir)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "reader"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	for name, n := range sizes {
		before := sink.count()
		if err := s.FileGet(sub, proto.FileGet{ReqID: name[:4], Path: name}); err != nil {
			t.Fatal(err)
		}
		var h proto.FileHeader
		var body []byte
		ok := waitFor(3*time.Second, func() bool {
			for i := before; i < sink.count(); i++ {
				f, err := proto.Decode(sink.frame(i))
				if err != nil || f.Type != proto.TypeFile {
					continue
				}
				if len(sink.frame(i)) > HighWaterMark {
					t.Fatalf("%s: a frame of %d bytes, over the queue's %d", name, len(sink.frame(i)), HighWaterMark)
				}
				h, body, err = proto.DecodeFile(f.Payload)
				return err == nil && h.ReqID == name[:4]
			}
			return false
		})
		if !ok {
			t.Fatalf("%s: no answer; the reader ended with %v", name, sub.Reason())
		}
		if sub.Reason() != nil {
			t.Fatalf("%s: the reader was dropped: %v", name, sub.Reason())
		}
		if !bytes.Equal(body, content[:len(body)]) || len(body) == 0 {
			t.Fatalf("%s: the body is not the file's first %d bytes", name, len(body))
		}
		whole := len(body) == n
		if h.Truncated == whole || whole != (name == "small.txt") {
			t.Fatalf("%s: %d of %d bytes, truncated %v", name, len(body), n, h.Truncated)
		}
		if h.Truncated && (h.Sha256 != "" || h.Mtime != "") {
			t.Fatalf("%s: a part carries the whole file's hash %q or time %q", name, h.Sha256, h.Mtime)
		}
		if !h.Truncated && h.Sha256 == "" {
			t.Fatalf("%s: a whole file without its hash", name)
		}
	}
}
