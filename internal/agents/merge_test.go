package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAsset = `{"version":1,"hooks":{"Stop":[{"command":"/opt/conductor notify --x-hook"}],"Start":[{"command":"/opt/conductor notify --x-hook"}]}}`

func openTestHome(t *testing.T) (*homeDir, string) {
	t.Helper()
	dir := t.TempDir()
	h, err := openHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	return h, dir
}

// A missing file becomes the asset itself, indented with two spaces.
func TestMergeJSONHooksCreatesTheFile(t *testing.T) {
	h, dir := openTestHome(t)
	changed, err := mergeJSONHooks(h, "a/hooks.json", []byte(testAsset), "notify --x-hook")
	if err != nil || !changed {
		t.Fatalf("merge: %v %v", changed, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "a", "hooks.json"))
	want := "{\n  \"version\": 1,\n  \"hooks\": {\n    \"Stop\": [\n      {\n        \"command\": \"/opt/conductor notify --x-hook\"\n      }\n    ],\n    \"Start\": [\n      {\n        \"command\": \"/opt/conductor notify --x-hook\"\n      }\n    ]\n  }\n}\n"
	if string(b) != want {
		t.Fatalf("got\n%s\nwant\n%s", b, want)
	}
}

// Every key and value the merge does not own stays as it was, in its order;
// only the whitespace becomes two-space indentation.
func TestMergeJSONHooksKeepsWhatItDoesNotOwn(t *testing.T) {
	h, dir := openTestHome(t)
	p := filepath.Join(dir, "hooks.json")
	os.WriteFile(p, []byte(`{
    "zeta": {"b": 1, "a": [true, null, 1.50, "é"]},
    "hooks": {"Stop": [{"command": "say done"}], "Other": []},
    "alpha": "x"
}`), 0o600)
	changed, err := mergeJSONHooks(h, "hooks.json", []byte(testAsset), "notify --x-hook")
	if err != nil || !changed {
		t.Fatalf("merge: %v %v", changed, err)
	}
	b, _ := os.ReadFile(p)
	want := `{
  "zeta": {
    "b": 1,
    "a": [
      true,
      null,
      1.50,
      "é"
    ]
  },
  "hooks": {
    "Stop": [
      {
        "command": "say done"
      },
      {
        "command": "/opt/conductor notify --x-hook"
      }
    ],
    "Other": [],
    "Start": [
      {
        "command": "/opt/conductor notify --x-hook"
      }
    ]
  },
  "alpha": "x"
}
`
	if string(b) != want {
		t.Fatalf("got\n%s\nwant\n%s", b, want)
	}
	if changed, err := mergeJSONHooks(h, "hooks.json", []byte(testAsset), "notify --x-hook"); changed || err != nil {
		t.Fatalf("second merge: %v %v", changed, err)
	}
}

// A list that already holds a Conductor entry, under any binary path, gets no
// second one; a list without one gets it even when another list has it.
func TestMergeJSONHooksLooksForTheMarkerPerList(t *testing.T) {
	h, dir := openTestHome(t)
	p := filepath.Join(dir, "hooks.json")
	os.WriteFile(p, []byte(`{"hooks":{"Stop":[{"command":"/elsewhere/conductor notify --x-hook"}]}}`), 0o600)
	if changed, err := mergeJSONHooks(h, "hooks.json", []byte(testAsset), "notify --x-hook"); !changed || err != nil {
		t.Fatalf("merge: %v %v", changed, err)
	}
	b, _ := os.ReadFile(p)
	if strings.Count(string(b), "notify --x-hook") != 2 || !strings.Contains(string(b), "/elsewhere/conductor") {
		t.Fatalf("merged:\n%s", b)
	}
}

// An empty file or a null hooks key is a fresh start.
func TestMergeJSONHooksTreatsEmptyAsMissing(t *testing.T) {
	for _, in := range []string{"", "  \n", `{"hooks":null}`} {
		h, dir := openTestHome(t)
		p := filepath.Join(dir, "hooks.json")
		os.WriteFile(p, []byte(in), 0o600)
		if changed, err := mergeJSONHooks(h, "hooks.json", []byte(testAsset), "notify --x-hook"); !changed || err != nil {
			t.Fatalf("%q: %v %v", in, changed, err)
		}
		b, _ := os.ReadFile(p)
		if !strings.Contains(string(b), `"Start": [`) {
			t.Fatalf("%q became\n%s", in, b)
		}
	}
}

// A file the merge cannot understand is left alone, and the user is told to
// merge by hand.
func TestMergeJSONHooksRefusesWhatItCannotMerge(t *testing.T) {
	for _, in := range []string{`[]`, `{"hooks":[]}`, `{"hooks":{"Stop":{}}}`, `{"hooks":`, `{"a":1} {"b":2}`, `// comment
{}`} {
		h, dir := openTestHome(t)
		p := filepath.Join(dir, "hooks.json")
		os.WriteFile(p, []byte(in), 0o600)
		changed, err := mergeJSONHooks(h, "hooks.json", []byte(testAsset), "notify --x-hook")
		if changed || !errors.Is(err, ErrByHand) || !strings.Contains(err.Error(), p) {
			t.Fatalf("%q: %v %v", in, changed, err)
		}
		if b, _ := os.ReadFile(p); string(b) != in {
			t.Fatalf("%q was rewritten to %q", in, b)
		}
	}
}

func TestSetJSONKeyOwnsOneKey(t *testing.T) {
	h, dir := openTestHome(t)
	asset := []byte(`{"conductor":{"enabled":true}}`)
	p := filepath.Join(dir, "hooks.json")
	os.WriteFile(p, []byte(`{"mine":{"enabled":false},"conductor":{"enabled":false}}`), 0o600)
	if changed, err := setJSONKey(h, "hooks.json", asset, "conductor"); !changed || err != nil {
		t.Fatalf("set: %v %v", changed, err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "{\n  \"mine\": {\n    \"enabled\": false\n  },\n  \"conductor\": {\n    \"enabled\": true\n  }\n}\n" {
		t.Fatalf("got\n%s", b)
	}
	if changed, err := setJSONKey(h, "hooks.json", asset, "conductor"); changed || err != nil {
		t.Fatalf("second set: %v %v", changed, err)
	}
}

func TestCreateJSONNeverOverwrites(t *testing.T) {
	h, dir := openTestHome(t)
	p := filepath.Join(dir, "hooks.json")
	if changed, err := createJSON(h, "hooks.json", []byte(testAsset)); !changed || err != nil {
		t.Fatalf("create: %v %v", changed, err)
	}
	if changed, err := createJSON(h, "hooks.json", []byte(testAsset)); changed || err != nil {
		t.Fatalf("same content: %v %v", changed, err)
	}
	os.WriteFile(p, []byte(`{"hooks":{}}`), 0o600)
	if changed, err := createJSON(h, "hooks.json", []byte(testAsset)); changed || !errors.Is(err, ErrByHand) {
		t.Fatalf("someone else's file: %v %v", changed, err)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"hooks":{}}` {
		t.Fatalf("overwritten: %s", b)
	}
}

func TestWithMarkedBlock(t *testing.T) {
	const begin, end = "# >>> conductor", "# <<< conductor"
	block := begin + "\nnotify = [\"a\"]\n" + end + "\n"
	cases := []struct{ in, want string }{
		{"", block},
		{"\n", block},
		{"model = \"o3\"\n[t]\nx = 1\n", block + "\nmodel = \"o3\"\n[t]\nx = 1\n"},
		// A block already there is replaced where it is.
		{"a = 1\n" + begin + "\nnotify = [\"old\"]\n" + end + "\nb = 2\n", "a = 1\n" + block + "b = 2\n"},
		{block + "\nrest = 1\n", block + "\nrest = 1\n"},
		{"  " + begin + "\nold\n  " + end, block},
	}
	for _, c := range cases {
		got, err := withMarkedBlock(c.in, begin, end, `notify = ["a"]`)
		if err != nil || got != c.want {
			t.Errorf("withMarkedBlock(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := withMarkedBlock(begin+"\nnotify = 1\n", begin, end, "x"); !errors.Is(err, ErrByHand) {
		t.Fatalf("a block without its end: %v", err)
	}
}

func TestTOMLSetsRootKey(t *testing.T) {
	const begin, end = "# >>> conductor", "# <<< conductor"
	cases := map[string]bool{
		"notify = [\"a\"]\n":                        true,
		"  notify=[\"a\"]\n":                        true,
		"\"notify\" = [\"a\"]\n":                    true,
		"model = 1\n# notify = [\"a\"]\n":           false,
		"notifications = true\n":                    false,
		"[tui]\nnotify = [\"a\"]\n":                 false,
		begin + "\nnotify = [\"a\"]\n" + end + "\n": false,
		"model = 1\n[profiles.x]\nnotify = 1\n":     false,
	}
	for in, want := range cases {
		if got := tomlSetsRootKey(in, "notify", begin, end); got != want {
			t.Errorf("tomlSetsRootKey(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestWithYAMLListItem(t *testing.T) {
	const item, match = `"/h/.omp/agent/extensions/conductor.ts"`, "/h/.omp/agent/extensions/conductor.ts"
	cases := []struct{ in, want string }{
		{"", "extensions:\n  # conductor\n  - " + item + "\n"},
		{"theme: dark", "theme: dark\nextensions:\n  # conductor\n  - " + item + "\n"},
		{"extensions:\n  - a.ts\n  - b.ts\n", "extensions:\n  - a.ts\n  - b.ts\n  # conductor\n  - " + item + "\n"},
		// Items at the key's own indentation are a list too.
		{"extensions:\n- a.ts\nmodel: y\n", "extensions:\n- a.ts\n# conductor\n- " + item + "\nmodel: y\n"},
		// An empty list, and one with a comment on the key.
		{"extensions:\nmodel: y\n", "extensions:\n  # conductor\n  - " + item + "\nmodel: y\n"},
		{"extensions: # mine\n  - a.ts\n\n# next\nmodel: y\n", "extensions: # mine\n  - a.ts\n  # conductor\n  - " + item + "\n\n# next\nmodel: y\n"},
		// Already listed: nothing changes.
		{"extensions:\n  - " + match + "\n", "extensions:\n  - " + match + "\n"},
	}
	for _, c := range cases {
		got, err := withYAMLListItem(c.in, "extensions", "# conductor", item, match)
		if err != nil || got != c.want {
			t.Errorf("withYAMLListItem(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"extensions: []\n", "extensions: [a.ts]\n", "extensions: a.ts\n", "\"extensions\":\n  - a.ts\n"} {
		if _, err := withYAMLListItem(in, "extensions", "# conductor", item, match); !errors.Is(err, ErrByHand) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

// A file Install replaces keeps its mode; a new one is 0600 in directories
// made 0700; the same content is not written again.
func TestHomeWrite(t *testing.T) {
	h, dir := openTestHome(t)
	p := filepath.Join(dir, "a", "b", "f.json")
	if changed, err := h.write("a/b/f.json", []byte("one")); !changed || err != nil {
		t.Fatalf("new: %v %v", changed, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("new file mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Dir(p)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("new directory mode %v", fi.Mode())
	}
	os.Chmod(p, 0o644)
	if changed, err := h.write("a/b/f.json", []byte("one")); changed || err != nil {
		t.Fatalf("same content: %v %v", changed, err)
	}
	if changed, err := h.write("a/b/f.json", []byte("two")); !changed || err != nil {
		t.Fatalf("new content: %v %v", changed, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Fatalf("replaced file mode %v, want its own 0644", fi.Mode())
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("content %q", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("left behind %v", entries)
	}
	if _, err := h.write("../escape", []byte("x")); err == nil {
		t.Fatal("a path out of home was written")
	}
	os.Mkdir(filepath.Join(dir, "dir.json"), 0o700)
	if _, err := h.write("dir.json", []byte("x")); err == nil {
		t.Fatal("a directory was replaced")
	}
}
