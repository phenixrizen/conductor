package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
	// A deny directory that does not exist hides nothing.
	if _, err := ResolvePath(root, "sub/file.go", []string{filepath.Join(root, "gone")}); err != nil {
		t.Errorf("missing deny directory: %v", err)
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
		secret: `{"adminToken":"secret"}`,
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

// An entry ending in "*" denies, in its directory, every name that starts with
// the rest of its last element, ignoring case: the copies an editor or a
// person leaves beside a config file hold its secrets too. The files beside it
// with other names still read, and an entry whose directory is gone denies
// nothing.
func TestResolvePathDeniesCopiesBesideADeniedFile(t *testing.T) {
	root := setupTree(t)
	for _, name := range []string{"conductor.json", "conductor.json.bak", "conductor.json~", "Conductor.JSON.orig", "conductor.example.json", "other.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(`{"adminToken":"secret"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "conductor.json.bak"), filepath.Join(root, "sub", "innocent.txt")); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "conductor.json")
	deny := []string{cfg, cfg + "*"}
	for _, p := range []string{"conductor.json", "conductor.json.bak", "conductor.json~", "Conductor.JSON.orig", "sub/innocent.txt", "conductor.json.swp"} {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%q: resolved to %s, want it denied", p, r)
		}
		if h, body := ReadPath(root, p, false, deny); h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || body != nil {
			t.Errorf("%q: %+v %q", p, h, body)
		}
	}
	for _, p := range []string{"conductor.example.json", "other.json", ".", "sub/file.go"} {
		if _, err := ResolvePath(root, p, deny); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	if _, err := ResolvePath(root, "other.json", []string{filepath.Join(root, "gone", "x.json*")}); err != nil {
		t.Fatalf("an entry in a directory that does not exist: %v", err)
	}
}
