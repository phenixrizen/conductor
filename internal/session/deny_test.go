package session

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/phenixrizen/conductor/internal/nvim"
	"github.com/phenixrizen/conductor/internal/proto"
)

// DenyList keeps each directory, and each file with the name entry beside
// it and, for a symbolic link, the one beside its target; it leaves out
// empty paths and repeats, and keeps the order given.
func TestDenyList(t *testing.T) {
	// Its own path without links, so that a file's target is the file.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	etc := filepath.Join(root, "etc")
	elsewhere := filepath.Join(root, "real")
	for _, d := range []string{data, etc, elsewhere} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(etc, "conductor.json")
	os.WriteFile(cfg, []byte("{}"), 0o600)
	target := filepath.Join(elsewhere, "prod.json")
	os.WriteFile(target, []byte("{}"), 0o600)
	link := filepath.Join(etc, "agents.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	// A file that is no link is its own target: one name entry.
	same := filepath.Join(etc, "same.json")
	os.WriteFile(same, []byte("{}"), 0o600)
	for _, c := range []struct {
		name        string
		dirs, files []string
		want        []string
	}{
		{"nothing", nil, nil, nil},
		{"empty paths", []string{"", ""}, []string{""}, nil},
		{"a directory", []string{data}, nil, []string{data}},
		{"a directory twice", []string{data, data, ""}, nil, []string{data}},
		{"two directories", []string{data, etc}, nil, []string{data, etc}},
		{"a file", nil, []string{cfg}, []string{cfg, filepath.Join(etc, "*conductor.json*")}},
		{"a file that is not there yet", nil, []string{filepath.Join(etc, "missing.json")}, []string{filepath.Join(etc, "missing.json"), filepath.Join(etc, "*missing.json*")}},
		{"a link", nil, []string{link}, []string{link, filepath.Join(etc, "*agents.json*"), filepath.Join(elsewhere, "*prod.json*")}},
		{"a file that is no link", nil, []string{same}, []string{same, filepath.Join(etc, "*same.json*")}},
		{"the same file twice", nil, []string{cfg, cfg}, []string{cfg, filepath.Join(etc, "*conductor.json*")}},
		{"all of it", []string{data, ""}, []string{cfg, "", link}, []string{data, cfg, filepath.Join(etc, "*conductor.json*"), link, filepath.Join(etc, "*agents.json*"), filepath.Join(elsewhere, "*prod.json*")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := DenyList(c.dirs, c.files); !slices.Equal(got, c.want) {
				t.Errorf("DenyList(%q, %q)\n got %q\nwant %q", c.dirs, c.files, got, c.want)
			}
		})
	}
	if got, want := NameEntry("/etc/conductor/conductor.json"), filepath.FromSlash("/etc/conductor/*conductor.json*"); got != want {
		t.Errorf("NameEntry: %q, want %q", got, want)
	}
	// What the list names is refused, and what it does not name is served.
	deny := DenyList([]string{data}, []string{cfg, link})
	os.WriteFile(filepath.Join(data, "catalog.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(etc, "conductor.json.bak"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(elsewhere, ".prod.json.swp"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(etc, "plain.json"), []byte("{}"), 0o600)
	for _, p := range []string{"data", "data/catalog.json", "etc/conductor.json", "etc/conductor.json.bak", "etc/agents.json", "real/prod.json", "real/.prod.json.swp"} {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%s: resolved to %s, want it refused", p, r)
		}
	}
	for _, p := range []string{".", "etc", "etc/plain.json", "etc/same.json", "real"} {
		if _, err := ResolvePath(root, p, deny); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// FileDenyFunc is asked at each request, in place of FileDeny: a read, a
// find, a save and the editor follow the list it gives at that moment.
func TestFileDenyFuncIsAskedAtEachRequest(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	os.MkdirAll(state, 0o700)
	os.WriteFile(filepath.Join(state, "catalog.json"), []byte("secret\n"), 0o600)
	var mu sync.Mutex
	var list []string
	p := newFakeProc()
	s := NewLocal(Info{ID: "sess", Cwd: dir, Cols: 80, Rows: 24}, p, Options{ScrollbackBytes: 4096, FileDeny: []string{filepath.Join(dir, "unused")}, FileDenyFunc: func() []string {
		mu.Lock()
		defer mu.Unlock()
		return list
	}})
	t.Cleanup(func() { p.exit() })
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	read := func(req proto.FileGet) proto.FileHeader {
		t.Helper()
		n++
		req.T, req.ReqID = proto.CtlFileGet, "r"+strconv.Itoa(n)
		if err := s.FileGet(sub, req); err != nil {
			t.Fatal(err)
		}
		return fileReply(t, sink, req.ReqID)
	}
	if h := read(proto.FileGet{Path: "state/catalog.json"}); h.Kind != "file" {
		t.Fatalf("before the list names it: %+v", h)
	}
	mu.Lock()
	list = []string{state}
	mu.Unlock()
	if h := read(proto.FileGet{Path: "state/catalog.json"}); h.Error == nil || h.Error.Code != "denied" {
		t.Errorf("read: %+v, want it refused", h)
	}
	if h := read(proto.FileGet{Path: "catalog", Op: proto.FileOpFind}); h.Kind != "find" || len(h.Matches) != 0 {
		t.Errorf("find: %+v, want nothing found", h)
	}
	save(s, sub, "w1", "state/catalog.json", []byte("changed\n"), 512, "", true)
	if f := fileReply(t, sink, "w1"); f.Error == nil || f.Error.Code != "denied" {
		t.Errorf("save: %+v, want it refused", f)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "catalog.json")); string(b) != "secret\n" {
		t.Errorf("the file holds %q after a refused save", b)
	}
	if nvim.Available() {
		if err := s.NvimOpen(t.Context(), sub, proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "n1", Path: "state/catalog.json"}); err != ErrFileDenied {
			t.Errorf("the editor: %v, want ErrFileDenied", err)
		}
	}
}
