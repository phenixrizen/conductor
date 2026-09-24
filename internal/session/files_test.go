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
		if _, err := ResolvePath(root, p); err != nil {
			t.Errorf("%q: unexpected error %v", p, err)
		}
	}
	bad := []string{"../x", "/etc/passwd", "escape", "sub/.git/objects/x", "a\x00b", "sub/../../x"}
	for _, p := range bad {
		if r, err := ResolvePath(root, p); err == nil {
			t.Errorf("%q: expected error, resolved to %s", p, r)
		}
	}
	if r, _ := ResolvePath(root, "inside/file.go"); r != filepath.Join(realRoot, "sub", "file.go") {
		t.Errorf("symlink inside root resolved to %s", r)
	}
}

func TestReadPathKinds(t *testing.T) {
	root := setupTree(t)
	h, body := ReadPath(root, "sub/file.go", false)
	if h.Kind != "file" || string(body) != "package sub\n" || !h.Exists {
		t.Fatalf("file: %+v %q", h, body)
	}
	h, body = ReadPath(root, "bin.dat", false)
	if h.Kind != "file" || !h.Binary || body != nil {
		t.Fatalf("binary: %+v %q", h, body)
	}
	h, body = ReadPath(root, "big.txt", false)
	if !h.Truncated || len(body) != MaxFileRead {
		t.Fatalf("truncated: %+v %d", h, len(body))
	}
	h, _ = ReadPath(root, "sub", false)
	if h.Kind != "dir" || len(h.Entries) != 2 || !h.Entries[0].Dir || h.Entries[1].Name != "file.go" {
		t.Fatalf("dir: %+v", h)
	}
	h, _ = ReadPath(root, "nope", false)
	if h.Kind != "error" || h.Error.Code != "not_found" || h.Exists {
		t.Fatalf("missing: %+v", h)
	}
	h, _ = ReadPath(root, "/etc/passwd", false)
	if h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("denied: %+v", h)
	}
	h, body = ReadPath(root, "sub/file.go", true)
	if h.Kind != "file" || body != nil || !h.Exists {
		t.Fatalf("stat: %+v %q", h, body)
	}
}
