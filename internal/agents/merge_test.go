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
	h, err := openHome(dir, false)
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

// A list that already holds an entry that runs conductor for the marker, a
// bare one of the user's included, gets no second one; a list without one
// gets it even when another list has it.
func TestMergeJSONHooksLooksForTheMarkerPerList(t *testing.T) {
	h, dir := openTestHome(t)
	p := filepath.Join(dir, "hooks.json")
	os.WriteFile(p, []byte(`{"hooks":{"Stop":[{"command":"conductor notify --x-hook"}]}}`), 0o600)
	if changed, err := mergeJSONHooks(h, "hooks.json", []byte(testAsset), "notify --x-hook"); !changed || err != nil {
		t.Fatalf("merge: %v %v", changed, err)
	}
	b, _ := os.ReadFile(p)
	if strings.Count(string(b), "notify --x-hook") != 2 || !strings.Contains(string(b), `"conductor notify --x-hook"`) {
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
	if changed, err := createJSON(h, "hooks.json", []byte(testAsset), "notify --x-hook", "see the snippet"); !changed || err != nil {
		t.Fatalf("create: %v %v", changed, err)
	}
	if changed, err := createJSON(h, "hooks.json", []byte(testAsset), "notify --x-hook", "see the snippet"); changed || err != nil {
		t.Fatalf("same content: %v %v", changed, err)
	}
	os.WriteFile(p, []byte(`{"hooks":{}}`), 0o600)
	if changed, err := createJSON(h, "hooks.json", []byte(testAsset), "notify --x-hook", "see the snippet"); changed || !errors.Is(err, ErrByHand) || !strings.Contains(err.Error(), "see the snippet") {
		t.Fatalf("someone else's file: %v %v", changed, err)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"hooks":{}}` {
		t.Fatalf("overwritten: %s", b)
	}
}

// staleCommand tells Conductor's own commands for an older binary from
// everything else: the user's commands, bare `conductor` ones and ours.
func TestStaleCommand(t *testing.T) {
	const marker, ours = "notify --x-hook", "/opt/new/conductor notify --x-hook"
	for in, want := range map[string]bool{
		"/opt/old/conductor notify --x-hook":       true,
		"'/opt/my apps/conductor' notify --x-hook": true,
		`'/opt/it'\''s/conductor' notify --x-hook`: true,
		ours:                          false,
		"conductor notify --x-hook":   false, // bare: the user's
		"./conductor notify --x-hook": false,
		"/opt/old/conductor notify --x-hook --quiet=false": false,
		"/opt/old/conductor notify --y-hook":               false,
		"say done; /opt/old/conductor notify --x-hook":     false,
		`"/opt/old/conductor" notify --x-hook`:             false,
		"/opt/my apps/conductor notify --x-hook":           false,
		"/opt/old/bin/conductor notify --x-hook":           true,
		"/usr/bin/python3 notify --x-hook":                 false, // not conductor: not Conductor's to rewrite
		"/opt/old/conductor-dev notify --x-hook":           false,
		"'/opt/my apps/notconductor' notify --x-hook":      false,
	} {
		if got := staleCommand(in, marker, ours); got != want {
			t.Errorf("staleCommand(%q) = %v, want %v", in, got, want)
		}
	}
}

// A stale command is rewritten where it is; the rest of the document, key
// order and raw values included, is kept.
func TestMergeJSONHooksRewritesStaleCommandsInPlace(t *testing.T) {
	h, dir := openTestHome(t)
	p := filepath.Join(dir, "hooks.json")
	os.WriteFile(p, []byte(`{"hooks":{"Stop":[{"when":1.50,"command":"/opt/old/conductor notify --x-hook","z":"é"},{"command":"conductor notify --x-hook"}],"Start":[{"command":"/opt/old/conductor notify --x-hook"}]},"version":1}`), 0o600)
	if changed, err := mergeJSONHooks(h, "hooks.json", []byte(testAsset), "notify --x-hook"); !changed || err != nil {
		t.Fatalf("merge: %v %v", changed, err)
	}
	b, _ := os.ReadFile(p)
	want := `{
  "hooks": {
    "Stop": [
      {
        "when": 1.50,
        "command": "/opt/conductor notify --x-hook",
        "z": "é"
      },
      {
        "command": "conductor notify --x-hook"
      }
    ],
    "Start": [
      {
        "command": "/opt/conductor notify --x-hook"
      }
    ]
  },
  "version": 1
}
`
	if string(b) != want {
		t.Fatalf("got\n%s\nwant\n%s", b, want)
	}
}

// The dry run behind Status reports what a write would change and writes
// nothing.
func TestDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	touched, err := run(dir, true, step{"a/hooks.json", func(h *homeDir) (bool, error) {
		return mergeJSONHooks(h, "a/hooks.json", []byte(testAsset), "notify --x-hook")
	}})
	if err != nil || len(touched) != 1 {
		t.Fatalf("dry run: %q %v", touched, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the dry run wrote %v", entries)
	}
}

func TestTOMLBlock(t *testing.T) {
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
		// A byte order mark stays first.
		{"\ufeffmodel = 1\n", "\ufeff" + block + "\nmodel = 1\n"},
		{"\ufeff" + begin + "\nold\n" + end + "\n", "\ufeff" + block},
		// Marker lines inside a multi-line string are text, not the block.
		{"doc = \"\"\"\n" + begin + "\n" + end + "\n\"\"\"\n", block + "\ndoc = \"\"\"\n" + begin + "\n" + end + "\n\"\"\"\n"},
	}
	for _, c := range cases {
		doc, err := readTOML(c.in)
		if err != nil {
			t.Fatalf("readTOML(%q): %v", c.in, err)
		}
		got, err := doc.withBlock(begin, end, `notify = ["a"]`)
		if err != nil || got != c.want {
			t.Errorf("withBlock(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	doc, _ := readTOML(begin + "\nnotify = 1\n")
	if _, err := doc.withBlock(begin, end, "x"); err == nil {
		t.Fatal("a block without its end")
	}
}

func TestTOMLSetsRootKey(t *testing.T) {
	const begin, end = "# >>> conductor", "# <<< conductor"
	cases := map[string]bool{
		"notify = [\"a\"]\n":                        true,
		"  notify=[\"a\"]\n":                        true,
		"\"notify\" = [\"a\"]\n":                    true,
		"\ufeffnotify = [\"a\"]\n":                  true,
		"model = 1\n# notify = [\"a\"]\n":           false,
		"notifications = true\n":                    false,
		"[tui]\nnotify = [\"a\"]\n":                 false,
		begin + "\nnotify = [\"a\"]\n" + end + "\n": false,
		"model = 1\n[profiles.x]\nnotify = 1\n":     false,
		// A line that starts with [ inside a multi-line array or string is
		// not a table header: the root goes on after it.
		"paths = [\n  [\"a\", \"b\"],\n]\nnotify = [\"x\"]\n":      true,
		"doc = \"\"\"\n[not a table]\n\"\"\"\nnotify = [\"x\"]\n":  true,
		"doc = '''\n[not a table]\n'''\nnotify = [\"x\"]\n":        true,
		"doc = \"\"\"\nnotify = 1\n\"\"\"\n":                       false,
		"a = \"[x] # not a comment\" # a comment [y\nnotify = 1\n": true,
	}
	for in, want := range cases {
		doc, err := readTOML(in)
		if err != nil {
			t.Fatalf("readTOML(%q): %v", in, err)
		}
		if got := doc.setsRootKey("notify", begin, end); got != want {
			t.Errorf("setsRootKey(%q) = %v, want %v", in, got, want)
		}
	}
}

// A document that ends inside a string or an array cannot be read line by
// line with any confidence: the codex step leaves it to the user.
func TestReadTOMLRefusesWhatItCannotRead(t *testing.T) {
	for _, in := range []string{
		"doc = \"\"\"\nnever closed\n",
		"doc = '''\nnever closed\n",
		"paths = [\n  \"a\",\n",
		"name = \"never closed\n",
		"name = 'never closed\n",
	} {
		if _, err := readTOML(in); err == nil {
			t.Errorf("readTOML(%q) read it", in)
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
		// An explicit start of the one document, and comments in the list.
		{"---\nextensions:\n  # mine\n  - a.ts\n", "---\nextensions:\n  # mine\n  - a.ts\n  # conductor\n  - " + item + "\n"},
		{"extensions:\n  - \"a b.ts\" # quoted\n", "extensions:\n  - \"a b.ts\" # quoted\n  # conductor\n  - " + item + "\n"},
		// A byte order mark stays where it is and hides no key.
		{"\ufeffextensions:\n  - a.ts\n", "\ufeffextensions:\n  - a.ts\n  # conductor\n  - " + item + "\n"},
		{"\ufeff", "\ufeffextensions:\n  # conductor\n  - " + item + "\n"},
		// One in the middle of the file is the file's own: it stays.
		{"theme: dark\n# \ufeff\nextensions:\n  - a.ts\n", "theme: dark\n# \ufeff\nextensions:\n  - a.ts\n  # conductor\n  - " + item + "\n"},
	}
	for _, c := range cases {
		got, err := withYAMLListItem(c.in, "extensions", "# conductor", item, match)
		if err != nil || got != c.want {
			t.Errorf("withYAMLListItem(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{
		"extensions: []\n", "extensions: [a.ts]\n", "extensions: a.ts\n", "\"extensions\":\n  - a.ts\n",
		// A mapping, not a list, and a list of mappings.
		"extensions:\n  foo: bar\n",
		"extensions:\n  - path: a.ts\n    enabled: true\n",
		"extensions:\n  - [a.ts]\n",
		// More than one document: an entry could land in the wrong one.
		"extensions:\n  - a.ts\n---\nmodel: y\n",
		"model: y\n---\nother: 1\n",
		"model: y\n...\n",
		// A root written in flow style.
		"{extensions: [a.ts]}\n", "# mine\n{model: y}\n", "[a.ts]\n",
	} {
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
