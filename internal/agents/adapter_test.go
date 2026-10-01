package agents

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/catalog"
)

// useBin makes bin the conductor binary the adapters name when they render
// something themselves: launch flags, and assets missing from the hooks dir.
func useBin(t *testing.T, bin string) {
	t.Helper()
	old := executable
	executable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { executable = old })
}

func TestWriteAssetsAndInjectClaude(t *testing.T) {
	dir := t.TempDir()
	if err := writeAssets(t, dir, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "claude.json"))
	if err != nil || !strings.Contains(string(b), `"/opt/conductor notify --claude-hook"`) || strings.Contains(string(b), "{{BIN}}") {
		t.Fatalf("asset: %v %s", err, b)
	}
	argv, env := InjectFor("claude", dir, catalog.Signal{Kind: "hook"})
	if len(argv) != 2 || argv[0] != "--settings" || argv[1] != filepath.Join(dir, "claude.json") || len(env) != 0 {
		t.Fatalf("inject: %v %v", argv, env)
	}
	argv, _ = InjectFor("claude", dir, catalog.Signal{Kind: "hook", ToolEvents: true})
	if argv[1] != filepath.Join(dir, "claude-tools.json") {
		t.Fatalf("tool events: %v", argv)
	}
	if argv, env := InjectFor("claude", dir, catalog.Signal{Kind: "bell"}); argv != nil || env != nil {
		t.Fatal("bell signal must not inject")
	}
}

func TestInstallClaudeIsIdempotentAndPreservesUserHooks(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	os.WriteFile(settings, []byte(`{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`), 0o600)
	a, _ := Get("claude")
	for i := 0; i < 2; i++ {
		if _, err := a.Install(home, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(settings)
	var doc map[string]any
	json.Unmarshal(b, &doc)
	if doc["model"] != "opus" {
		t.Fatal("user settings lost")
	}
	stop := doc["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 { // user's entry + one conductor entry, not two
		t.Fatalf("Stop hooks: %d", len(stop))
	}
	if ok, where := a.Status(home); !ok || where != settings {
		t.Fatalf("status %v %q", ok, where)
	}
}

func TestInjectUnknownAdapterIsNoop(t *testing.T) {
	if argv, env := InjectFor("", t.TempDir(), catalog.Signal{Kind: "hook"}); argv != nil || env != nil {
		t.Fatal("no adapter must inject nothing")
	}
}

// Every agent the plan supports has an adapter, listed in the order the Events
// page shows them.
func TestAllListsEveryAdapterInOrder(t *testing.T) {
	want := []string{"claude", "codex", "agy", "copilot", "cursor", "opencode", "pi", "omp", "aider", "goose", "amp", "dsh"}
	var got []string
	for _, a := range All() {
		got = append(got, a.ID)
		if a.Name == "" || len(a.Events) == 0 || a.Snippet == nil {
			t.Errorf("%s: name %q, events %v, snippet set %v", a.ID, a.Name, a.Events, a.Snippet != nil)
		}
		if (a.Install == nil) != (a.Status == nil) && a.ID != "dsh" {
			t.Errorf("%s: Install and Status must come together", a.ID)
		}
		if b, ok := Get(a.ID); !ok || b.ID != a.ID || b.Name != a.Name {
			t.Errorf("Get(%q) = %v, %v", a.ID, b.ID, ok)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("adapters %v, want %v", got, want)
	}
	for _, id := range []string{"", "gemini", "shell", "Claude"} {
		if _, ok := Get(id); ok {
			t.Errorf("Get(%q) found an adapter", id)
		}
	}
}

// What All and Get return is the caller's to change.
func TestAdaptersAreHandedOutAsCopies(t *testing.T) {
	a := All()[0]
	a.Events[0] = "changed"
	a.Assets["claude.json"] = "changed"
	b, _ := Get("claude")
	if b.Events[0] == "changed" || b.Assets["claude.json"] == "changed" {
		t.Fatal("changing a returned adapter changed the registry")
	}
}

// The launch route of each adapter: the flags go after the command, the
// environment into the session. Agents that have none are installed into their
// own config instead; OpenCode's route waits for OPENCODE_CONFIG_DIR to be
// verified additive.
func TestInjectForEachAdapter(t *testing.T) {
	useBin(t, "/opt/conductor")
	dir := t.TempDir()
	hook := catalog.Signal{Kind: "hook"}
	cases := []struct {
		id   string
		argv []string
		env  map[string]string
	}{
		{"claude", []string{"--settings", filepath.Join(dir, "claude.json")}, nil},
		{"codex", []string{"-c", `notify=["/opt/conductor","notify","--codex"]`, "-c", `tui.notification_method="bel"`}, nil},
		{"pi", []string{"--extension", filepath.Join(dir, "pi-conductor.ts")}, nil},
		{"aider", nil, map[string]string{
			"AIDER_NOTIFICATIONS":         "true",
			"AIDER_NOTIFICATIONS_COMMAND": `/opt/conductor notify --state needs_input --message "aider is waiting"`,
		}},
		{"agy", nil, nil}, {"copilot", nil, nil}, {"cursor", nil, nil}, {"opencode", nil, nil},
		{"omp", nil, nil}, {"goose", nil, nil}, {"amp", nil, nil}, {"dsh", nil, nil},
	}
	for _, c := range cases {
		argv, env := InjectFor(c.id, dir, hook)
		if !slices.Equal(argv, c.argv) || !maps.Equal(env, c.env) || (argv == nil) != (c.argv == nil) || (env == nil) != (c.env == nil) {
			t.Errorf("%s: argv %q env %q, want %q %q", c.id, argv, env, c.argv, c.env)
		}
	}
	if a, _ := Get("opencode"); a.Inject != nil {
		t.Fatal("opencode injects before OPENCODE_CONFIG_DIR is verified to add to the default config")
	}
}

// Only an agent whose signal is "hook" gets anything at launch, whatever its
// adapter can do.
func TestInjectOnlyForTheHookSignal(t *testing.T) {
	dir := t.TempDir()
	for _, a := range All() {
		for _, kind := range []string{"bell", "pattern", "none", ""} {
			if argv, env := InjectFor(a.ID, dir, catalog.Signal{Kind: kind, ToolEvents: true}); argv != nil || env != nil {
				t.Errorf("%s with signal %q injected %q %q", a.ID, kind, argv, env)
			}
		}
	}
}

// Flags that name files in the hooks dir are only right when the dir is
// absolute: a relative one would be read from the session's working directory.
func TestInjectNeedsAnAbsoluteHooksDir(t *testing.T) {
	for _, dir := range []string{"", "hooks", "./data/hooks"} {
		for _, id := range []string{"claude", "codex", "pi", "aider"} {
			if argv, env := InjectFor(id, dir, catalog.Signal{Kind: "hook"}); argv != nil || env != nil {
				t.Errorf("%s with hooks dir %q injected %q %q", id, dir, argv, env)
			}
		}
	}
}

// The files each adapter's Install writes, relative to home, and the one its
// Status reports. The agents that read skills also get the Conductor skill
// (skillFor).
var installed = map[string]struct {
	files  []string
	status string
}{
	"claude":   {[]string{".claude/settings.json", skillFor["claude"]}, ".claude/settings.json"},
	"codex":    {[]string{".codex/config.toml", ".codex/hooks.json", skillFor["codex"]}, ".codex/config.toml"},
	"agy":      {[]string{".gemini/config/hooks.json"}, ".gemini/config/hooks.json"},
	"copilot":  {[]string{".copilot/hooks/conductor.json"}, ".copilot/hooks/conductor.json"},
	"cursor":   {[]string{".cursor/hooks.json"}, ".cursor/hooks.json"},
	"opencode": {[]string{".config/opencode/plugins/conductor.ts"}, ".config/opencode/plugins/conductor.ts"},
	"pi":       {[]string{".pi/agent/extensions/conductor.ts", skillFor["pi"]}, ".pi/agent/extensions/conductor.ts"},
	"omp":      {[]string{".omp/agent/config.yml", ".omp/agent/extensions/conductor.ts"}, ".omp/agent/extensions/conductor.ts"},
	"goose":    {[]string{".agents/plugins/conductor/hooks/hooks.json", skillFor["goose"]}, ".agents/plugins/conductor/hooks/hooks.json"},
	"amp":      {[]string{".config/amp/plugins/conductor/index.ts"}, ".config/amp/plugins/conductor/index.ts"},
}

func under(home string, rels ...string) []string {
	var out []string
	for _, rel := range rels {
		out = append(out, filepath.Join(home, filepath.FromSlash(rel)))
	}
	slices.Sort(out)
	return out
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// On a home with none of the agent's files, Install creates them (files 0600,
// the directories it makes 0700), Status finds them, and a second Install
// changes nothing.
func TestInstallOnAnEmptyHome(t *testing.T) {
	useBin(t, "/opt/conductor")
	for _, a := range All() {
		want, ok := installed[a.ID]
		if !ok {
			if a.ID != "aider" && a.ID != "dsh" {
				t.Errorf("%s: no install expectation", a.ID)
			}
			continue
		}
		t.Run(a.ID, func(t *testing.T) {
			home, hooks := t.TempDir(), t.TempDir()
			if err := writeAssets(t, hooks, "/opt/conductor"); err != nil {
				t.Fatal(err)
			}
			where := filepath.Join(home, filepath.FromSlash(want.status))
			if ok, got := a.Status(home); ok || got != where {
				t.Fatalf("status before install: %v %q", ok, got)
			}
			touched, err := a.Install(home, hooks)
			if err != nil {
				t.Fatal(err)
			}
			if got, w := sorted(touched), under(home, want.files...); !slices.Equal(got, w) {
				t.Fatalf("touched %q, want %q", got, w)
			}
			for _, p := range touched {
				fi, err := os.Lstat(p)
				if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
					t.Fatalf("%s: %v %v", p, fi.Mode(), err)
				}
				for d := filepath.Dir(p); d != home; d = filepath.Dir(d) {
					if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
						t.Fatalf("directory %s: %v %v", d, fi.Mode(), err)
					}
				}
			}
			if ok, got := a.Status(home); !ok || got != where {
				t.Fatalf("status after install: %v %q", ok, got)
			}
			again, err := a.Install(home, hooks)
			if err != nil || len(again) != 0 {
				t.Fatalf("second install: %q %v", again, err)
			}
		})
	}
}

// An adapter that owns its file writes exactly what Conductor generated,
// replaces a stale copy of it and leaves every other file in the directory
// alone. (pi and Goose also write the skill, on a home that has none.)
func TestInstallReplacesOnlyItsOwnFile(t *testing.T) {
	useBin(t, "/opt/conductor")
	cases := []struct{ id, asset, file string }{
		{"copilot", "copilot.json", ".copilot/hooks/conductor.json"},
		{"opencode", "opencode/plugins/conductor.ts", ".config/opencode/plugins/conductor.ts"},
		{"pi", "pi-conductor.ts", ".pi/agent/extensions/conductor.ts"},
		{"goose", "goose/hooks/hooks.json", ".agents/plugins/conductor/hooks/hooks.json"},
		{"amp", "amp/conductor/index.ts", ".config/amp/plugins/conductor/index.ts"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			home, hooks := t.TempDir(), t.TempDir()
			if err := writeAssets(t, hooks, "/opt/conductor"); err != nil {
				t.Fatal(err)
			}
			own := filepath.Join(home, filepath.FromSlash(c.file))
			other := filepath.Join(filepath.Dir(own), "mine.txt")
			if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(other, []byte("the user's"), 0o644)
			os.WriteFile(own, []byte("an older conductor wrote this"), 0o644)
			a, _ := Get(c.id)
			touched, err := a.Install(home, hooks)
			wantTouched := []string{own}
			if rel, ok := skillFor[c.id]; ok {
				wantTouched = append(wantTouched, filepath.Join(home, filepath.FromSlash(rel)))
			}
			if err != nil || !slices.Equal(touched, wantTouched) {
				t.Fatalf("install: %q %v, want %q", touched, err, wantTouched)
			}
			got, _ := os.ReadFile(own)
			want, _ := os.ReadFile(filepath.Join(hooks, filepath.FromSlash(c.asset)))
			if string(got) != string(want) {
				t.Fatalf("%s holds\n%s\nwant the asset\n%s", own, got, want)
			}
			if b, _ := os.ReadFile(other); string(b) != "the user's" {
				t.Fatalf("a file next to it changed: %q", b)
			}
		})
	}
}

// Assets missing from the hooks dir are rendered for the running binary, so
// Install works before any server has written them.
func TestInstallRendersAssetsMissingFromTheHooksDir(t *testing.T) {
	useBin(t, "/usr/local/bin/conductor")
	home := t.TempDir()
	a, _ := Get("copilot")
	if _, err := a.Install(home, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".copilot", "hooks", "conductor.json"))
	if !strings.Contains(string(b), `"/usr/local/bin/conductor notify --copilot-hook"`) {
		t.Fatalf("rendered asset: %s", b)
	}
}

// claude: the hook lists merge into settings.json, every other key is kept in
// its place, and the result is indented with two spaces.
func TestInstallClaudeMergesEveryHookList(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	os.WriteFile(settings, []byte("{\n    \"permissions\": {\"allow\": [\"Bash(ls)\"]},\n    \"hooks\": {\"PreToolUse\": [{\"matcher\": \"Bash\", \"hooks\": [{\"type\": \"command\", \"command\": \"guard\"}]}]},\n    \"model\": \"opus\"\n}\n"), 0o644)
	a, _ := Get("claude")
	touched, err := a.Install(home, t.TempDir())
	if err != nil || !slices.Equal(touched, []string{settings, filepath.Join(home, ".claude", "skills", "conductor", "SKILL.md")}) {
		t.Fatalf("install: %q %v", touched, err)
	}
	b, _ := os.ReadFile(settings)
	if !strings.HasPrefix(string(b), "{\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(ls)\"\n    ]\n  },\n  \"hooks\": {\n    \"PreToolUse\": [") || !strings.HasSuffix(string(b), "  \"model\": \"opus\"\n}\n") {
		t.Fatalf("settings.json:\n%s", b)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct{ Type, Command string }
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"Notification", "Stop", "UserPromptSubmit", "PermissionRequest", "PermissionDenied"} {
		list := doc.Hooks[event]
		if len(list) != 1 || len(list[0].Hooks) != 1 || list[0].Hooks[0].Type != "command" || list[0].Hooks[0].Command != "/opt/conductor notify --claude-hook" {
			t.Errorf("%s: %+v", event, list)
		}
	}
	if pre := doc.Hooks["PreToolUse"]; len(pre) != 1 || pre[0].Hooks[0].Command != "guard" {
		t.Errorf("the user's PreToolUse hook changed: %+v", pre)
	}
	// Tool events stay a launch-time choice: settings.json gets the quiet set.
	for _, event := range []string{"PostToolUse", "PostToolUseFailure", "SubagentStop"} {
		if _, ok := doc.Hooks[event]; ok {
			t.Errorf("Install added %s", event)
		}
	}
	if fi, _ := os.Stat(settings); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v, want the file's own 0644", fi.Mode().Perm())
	}
}

// codex: notify goes into config.toml in a marked block above the first table,
// where it is a root key; a moved binary updates the block; hooks.json is
// written only when there is none.
func TestInstallCodexKeepsTheConfigAndNeverOverwritesHooks(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	config := filepath.Join(home, ".codex", "config.toml")
	hooksJSON := filepath.Join(home, ".codex", "hooks.json")
	os.MkdirAll(filepath.Dir(config), 0o700)
	original := "model = \"o3\"\n\n[profiles.fast]\nmodel = \"o4-mini\"\nnotify = [\"say\", \"fast\"]\n"
	os.WriteFile(config, []byte(original), 0o644)
	os.WriteFile(hooksJSON, []byte(`{"hooks":{}}`), 0o600)
	a, _ := Get("codex")
	touched, err := a.Install(home, t.TempDir())
	skill := filepath.Join(home, ".codex", "skills", "conductor", "SKILL.md")
	if !errors.Is(err, ErrByHand) || !strings.Contains(err.Error(), hooksJSON) || !strings.Contains(err.Error(), "features.hooks") || !slices.Equal(touched, []string{config, skill}) {
		t.Fatalf("install: %q %v", touched, err)
	}
	block := "# >>> conductor\nnotify = [\"/opt/conductor\", \"notify\", \"--codex\"]\n# <<< conductor\n"
	if b, _ := os.ReadFile(config); string(b) != block+"\n"+original {
		t.Fatalf("config.toml:\n%s", b)
	}
	if b, _ := os.ReadFile(hooksJSON); string(b) != `{"hooks":{}}` {
		t.Fatalf("the user's hooks.json changed: %s", b)
	}
	touched, err = a.Install(home, t.TempDir())
	if len(touched) != 0 || !errors.Is(err, ErrByHand) {
		t.Fatalf("second install: %q %v", touched, err)
	}
	useBin(t, "/usr/local/bin/conductor")
	touched, _ = a.Install(home, t.TempDir())
	b, _ := os.ReadFile(config)
	if !slices.Equal(touched, []string{config}) || strings.Count(string(b), "# >>> conductor") != 1 || strings.Contains(string(b), "/opt/conductor") || !strings.Contains(string(b), `notify = ["/usr/local/bin/conductor", "notify", "--codex"]`) {
		t.Fatalf("moved binary: %q\n%s", touched, b)
	}
}

// A notify of the user's own at the root of config.toml would become a
// duplicate key: Install leaves the file alone and says so.
func TestInstallCodexRefusesASecondNotify(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	config := filepath.Join(home, ".codex", "config.toml")
	os.MkdirAll(filepath.Dir(config), 0o700)
	original := "# mine\nnotify = [\"say\", \"done\"]\n"
	os.WriteFile(config, []byte(original), 0o600)
	a, _ := Get("codex")
	touched, err := a.Install(home, t.TempDir())
	if !errors.Is(err, ErrByHand) || !strings.Contains(err.Error(), "notify") || !strings.Contains(err.Error(), config) {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(config); string(b) != original {
		t.Fatalf("config.toml changed:\n%s", b)
	}
	// The hooks file and the skill are independent of it and still written.
	if !slices.Equal(touched, []string{filepath.Join(home, ".codex", "hooks.json"), filepath.Join(home, ".codex", "skills", "conductor", "SKILL.md")}) {
		t.Fatalf("touched %q", touched)
	}
	if ok, _ := a.Status(home); ok {
		t.Fatal("status reports an install that did not happen")
	}
}

// agy: Conductor owns the "conductor" key of hooks.json and nothing else.
func TestInstallAgyMergesItsOwnKey(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	path := filepath.Join(home, ".gemini", "config", "hooks.json")
	os.MkdirAll(filepath.Dir(path), 0o700)
	mine := `{"mine":{"enabled":true,"Stop":[{"matcher":"*","hooks":[{"type":"command","command":"say done"}]}]},"conductor":{"enabled":false}}`
	os.WriteFile(path, []byte(mine), 0o600)
	a, _ := Get("agy")
	touched, err := a.Install(home, t.TempDir())
	if err != nil || !slices.Equal(touched, []string{path}) {
		t.Fatalf("install: %q %v", touched, err)
	}
	b, _ := os.ReadFile(path)
	var doc map[string]map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["mine"]["enabled"] != true || len(doc["mine"]["Stop"].([]any)) != 1 {
		t.Fatalf("the user's key changed: %v", doc["mine"])
	}
	c := doc["conductor"]
	if c["enabled"] != true || !strings.Contains(string(b), `"command": "/opt/conductor notify --agy-hook"`) || c["Stop"] == nil || c["PostToolUse"] == nil {
		t.Fatalf("conductor key: %s", b)
	}
	if strings.Index(string(b), `"mine"`) > strings.Index(string(b), `"conductor"`) {
		t.Fatalf("keys reordered:\n%s", b)
	}
	if again, err := a.Install(home, t.TempDir()); err != nil || len(again) != 0 {
		t.Fatalf("second install: %q %v", again, err)
	}
}

// cursor: entries are added to hooks.json's lists next to the user's.
func TestInstallCursorMergesIntoHooksJSON(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	path := filepath.Join(home, ".cursor", "hooks.json")
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte(`{"version":1,"hooks":{"stop":[{"command":"say done"}],"beforeShellExecution":[{"command":"./guard.sh"}]}}`), 0o600)
	a, _ := Get("cursor")
	if touched, err := a.Install(home, t.TempDir()); err != nil || !slices.Equal(touched, []string{path}) {
		t.Fatalf("install: %q %v", touched, err)
	}
	b, _ := os.ReadFile(path)
	var doc struct {
		Version int                                   `json:"version"`
		Hooks   map[string][]struct{ Command string } `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	stop := doc.Hooks["stop"]
	if doc.Version != 1 || len(stop) != 2 || stop[0].Command != "say done" || stop[1].Command != "/opt/conductor notify --cursor-hook" {
		t.Fatalf("hooks.json:\n%s", b)
	}
	for _, event := range []string{"postToolUse", "afterFileEdit"} {
		if l := doc.Hooks[event]; len(l) != 1 || l[0].Command != "/opt/conductor notify --cursor-hook" {
			t.Fatalf("%s: %+v", event, l)
		}
	}
	if l := doc.Hooks["beforeShellExecution"]; len(l) != 1 || l[0].Command != "./guard.sh" {
		t.Fatalf("the user's hook changed: %+v", l)
	}
	if again, err := a.Install(home, t.TempDir()); err != nil || len(again) != 0 {
		t.Fatalf("second install: %q %v", again, err)
	}
}

// omp: the extension is copied and its path added to the extensions list of
// config.yml, under a marker, among the user's entries.
func TestInstallOmpAddsItsExtensionToTheList(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	config := filepath.Join(home, ".omp", "agent", "config.yml")
	ext := filepath.Join(home, ".omp", "agent", "extensions", "conductor.ts")
	os.MkdirAll(filepath.Dir(config), 0o700)
	os.WriteFile(config, []byte("theme: dark\nextensions:\n  - /x/a.ts\n\nmodel: y\n"), 0o600)
	a, _ := Get("omp")
	touched, err := a.Install(home, t.TempDir())
	if err != nil || !slices.Equal(sorted(touched), sorted([]string{config, ext})) {
		t.Fatalf("install: %q %v", touched, err)
	}
	want := "theme: dark\nextensions:\n  - /x/a.ts\n  # conductor\n  - \"" + ext + "\"\n\nmodel: y\n"
	if b, _ := os.ReadFile(config); string(b) != want {
		t.Fatalf("config.yml:\n%s\nwant\n%s", b, want)
	}
	if again, err := a.Install(home, t.TempDir()); err != nil || len(again) != 0 {
		t.Fatalf("second install: %q %v", again, err)
	}

	// A list written inline is not edited line by line.
	other := t.TempDir()
	flow := filepath.Join(other, ".omp", "agent", "config.yml")
	os.MkdirAll(filepath.Dir(flow), 0o700)
	os.WriteFile(flow, []byte("extensions: [/x/a.ts]\n"), 0o600)
	if _, err := a.Install(other, t.TempDir()); !errors.Is(err, ErrByHand) {
		t.Fatalf("flow list: %v", err)
	}
	if b, _ := os.ReadFile(flow); string(b) != "extensions: [/x/a.ts]\n" {
		t.Fatalf("flow list changed: %s", b)
	}
	if ok, _ := a.Status(other); ok {
		t.Fatal("status reports an extension config.yml does not list")
	}
}

// DeepSeek Harness is a developer preview: Install writes nothing and points
// at the snippet.
func TestInstallDshIsByHand(t *testing.T) {
	a, _ := Get("dsh")
	home := t.TempDir()
	touched, err := a.Install(home, t.TempDir())
	if len(touched) != 0 || !errors.Is(err, ErrByHand) || !strings.Contains(err.Error(), "snippet") || !strings.Contains(err.Error(), "developer preview") {
		t.Fatalf("install: %q %v", touched, err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("home has %v", entries)
	}
	if !a.Experimental || slices.Contains(a.Events, "experimental") {
		t.Fatalf("experimental %v, events %v", a.Experimental, a.Events)
	}
	for _, other := range All() {
		if other.Experimental != (other.ID == "dsh") {
			t.Errorf("%s: experimental %v", other.ID, other.Experimental)
		}
	}
	if s := a.Snippet(t.TempDir()); !strings.Contains(s, "permission-requested") || !strings.Contains(s, "question-asked") {
		t.Fatalf("snippet:\n%s", s)
	}
}

// aider is wired at launch only.
func TestAiderHasOnlyALaunchRoute(t *testing.T) {
	a, _ := Get("aider")
	if a.Install != nil || a.Status != nil || len(a.Assets) != 0 || a.Inject == nil {
		t.Fatalf("aider: install %v status %v assets %v inject %v", a.Install != nil, a.Status != nil, a.Assets, a.Inject != nil)
	}
	useBin(t, "/opt/conductor")
	if s := a.Snippet(t.TempDir()); !strings.Contains(s, "AIDER_NOTIFICATIONS=true") || !strings.Contains(s, "/opt/conductor notify --state needs_input") {
		t.Fatalf("snippet:\n%s", s)
	}
}

// Every snippet is what Install would put in place, with the binary path the
// hooks dir holds.
func TestSnippetsNameTheBinary(t *testing.T) {
	useBin(t, "/opt/conductor")
	hooks := t.TempDir()
	if err := writeAssets(t, hooks, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	for _, a := range All() {
		if s := a.Snippet(hooks); !strings.Contains(s, "/opt/conductor") || strings.Contains(s, "{{BIN}}") {
			t.Errorf("%s snippet:\n%s", a.ID, s)
		}
	}
}

// Install never writes through a link: not when the file is one, and not
// when a directory on the way leads out of home. A link that stays in home is
// followed. A step refused that way leaves the others to go ahead.
func TestInstallDoesNotFollowLinksOutOfHome(t *testing.T) {
	useBin(t, "/opt/conductor")
	outside := t.TempDir()
	target := filepath.Join(outside, "settings.json")
	os.WriteFile(target, []byte(`{"model":"opus"}`), 0o600)

	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	if err := os.Symlink(target, filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	claude, _ := Get("claude")
	skill := filepath.Join(home, ".claude", "skills", "conductor", "SKILL.md")
	if touched, err := claude.Install(home, t.TempDir()); !errors.Is(err, ErrByHand) || !strings.Contains(err.Error(), "symbolic link") || !slices.Equal(touched, []string{skill}) {
		t.Fatalf("file link: %q %v", touched, err)
	}
	if b, _ := os.ReadFile(target); string(b) != `{"model":"opus"}` {
		t.Fatalf("wrote through the link: %s", b)
	}

	home = t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, ".copilot")); err != nil {
		t.Fatal(err)
	}
	copilot, _ := Get("copilot")
	if touched, err := copilot.Install(home, t.TempDir()); err == nil || len(touched) != 0 {
		t.Fatalf("directory link out of home: %q %v", touched, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "hooks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrote outside home: %v", err)
	}

	// Dotfiles kept elsewhere in home, linked with an absolute path.
	home = t.TempDir()
	dotfiles := filepath.Join(home, "dotfiles", "copilot")
	os.MkdirAll(dotfiles, 0o700)
	if err := os.Symlink(dotfiles, filepath.Join(home, ".copilot")); err != nil {
		t.Fatal(err)
	}
	touched, err := copilot.Install(home, t.TempDir())
	if err != nil || !slices.Equal(touched, []string{filepath.Join(home, ".copilot", "hooks", "conductor.json")}) {
		t.Fatalf("link within home: %q %v", touched, err)
	}
	if _, err := os.Stat(filepath.Join(dotfiles, "hooks", "conductor.json")); err != nil {
		t.Fatal(err)
	}
}

// allFiles returns the content of every file under dir, keyed by path.
func allFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func joined(files map[string]string) string {
	var b strings.Builder
	for _, p := range slices.Sorted(maps.Keys(files)) {
		b.WriteString(files[p])
	}
	return b.String()
}

// After an upgrade moves the binary, Status reads the install as out of date,
// and Install rewrites Conductor's entries to the new path where they are:
// each once, next to the user's entries, which stay as they were.
func TestInstallAfterTheBinaryMoved(t *testing.T) {
	const oldBin, newBin = "/opt/conductor-1.0/conductor", "/opt/conductor-1.1/conductor"
	// Something of the user's in the files Conductor edits.
	seed := map[string][2]string{
		"claude": {".claude/settings.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`},
		"cursor": {".cursor/hooks.json", `{"version":1,"hooks":{"stop":[{"command":"say done"}]}}`},
		"agy":    {".gemini/config/hooks.json", `{"mine":{"enabled":true,"say":"done"}}`},
		"omp":    {".omp/agent/config.yml", "extensions:\n  - /x/say-done.ts\n"},
		"codex":  {".codex/config.toml", "model = \"say done\"\n"},
	}
	for _, a := range All() {
		if _, ok := installed[a.ID]; !ok {
			continue
		}
		t.Run(a.ID, func(t *testing.T) {
			home := t.TempDir()
			if s, ok := seed[a.ID]; ok {
				p := filepath.Join(home, filepath.FromSlash(s[0]))
				os.MkdirAll(filepath.Dir(p), 0o700)
				os.WriteFile(p, []byte(s[1]), 0o600)
			}
			useBin(t, oldBin)
			if _, err := a.Install(home, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			if ok, _ := a.Status(home); !ok {
				t.Fatal("status after the first install")
			}
			count := strings.Count(joined(allFiles(t, home)), oldBin)
			if count == 0 {
				t.Fatal("the first install names no binary")
			}

			useBin(t, newBin)
			if ok, where := a.Status(home); ok || where != filepath.Join(home, filepath.FromSlash(installed[a.ID].status)) {
				t.Fatalf("status with the old binary installed: %v %q", ok, where)
			}
			touched, err := a.Install(home, t.TempDir())
			if err != nil || len(touched) == 0 {
				t.Fatalf("install after the move: %q %v", touched, err)
			}
			after := joined(allFiles(t, home))
			if strings.Contains(after, oldBin) || strings.Count(after, newBin) != count {
				t.Fatalf("after the move, %d mentions of the new binary, want %d, and the old one %v:\n%s", strings.Count(after, newBin), count, strings.Contains(after, oldBin), after)
			}
			if _, ok := seed[a.ID]; ok && !strings.Contains(after, "say") {
				t.Fatalf("the user's entry is gone:\n%s", after)
			}
			if ok, _ := a.Status(home); !ok {
				t.Fatal("status after the repair")
			}
			if again, err := a.Install(home, t.TempDir()); err != nil || len(again) != 0 {
				t.Fatalf("third install: %q %v", again, err)
			}
		})
	}
}

// Entries that run a bare `conductor` are the user's own (the README showed
// them): Install leaves them as they are and adds what is missing, and a
// partial install reads as not installed until it has.
func TestInstallLeavesBareConductorEntriesAlone(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	bare := `{"hooks":[{"type":"command","command":"conductor notify --claude-hook"}]}`
	os.WriteFile(settings, []byte(`{"hooks":{"Notification":[`+bare+`],"Stop":[`+bare+`]}}`), 0o600)
	a, _ := Get("claude")
	if ok, _ := a.Status(home); ok {
		t.Fatal("a partial install reads as installed")
	}
	touched, err := a.Install(home, t.TempDir())
	if err != nil || !slices.Equal(touched, []string{settings, filepath.Join(home, ".claude", "skills", "conductor", "SKILL.md")}) {
		t.Fatalf("install: %q %v", touched, err)
	}
	b, _ := os.ReadFile(settings)
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for event, want := range map[string]string{
		"Notification":      "conductor notify --claude-hook",
		"Stop":              "conductor notify --claude-hook",
		"UserPromptSubmit":  "/opt/conductor notify --claude-hook",
		"PermissionRequest": "/opt/conductor notify --claude-hook",
		"PermissionDenied":  "/opt/conductor notify --claude-hook",
	} {
		if l := doc.Hooks[event]; len(l) != 1 || l[0].Hooks[0].Command != want {
			t.Errorf("%s: %+v, want one %q", event, l, want)
		}
	}
	if ok, _ := a.Status(home); !ok {
		t.Fatal("status after install")
	}
	if again, err := a.Install(home, t.TempDir()); err != nil || len(again) != 0 {
		t.Fatalf("second install: %q %v", again, err)
	}
}

// A hooks.json of the user's that holds Conductor's hooks, merged by hand,
// gets the new binary path in place and needs nothing more by hand.
func TestInstallCodexRepairsItsHooksAmongTheUsers(t *testing.T) {
	useBin(t, "/opt/old/conductor")
	asset, err := assetFor(codexAssets, "", "codex-hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	mine := strings.Replace(string(asset), `"hooks": {`, `"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "say hi"}]}],`, 1)
	home := t.TempDir()
	hooksJSON := filepath.Join(home, ".codex", "hooks.json")
	os.MkdirAll(filepath.Dir(hooksJSON), 0o700)
	os.WriteFile(hooksJSON, []byte(mine), 0o600)
	useBin(t, "/opt/new/conductor")
	a, _ := Get("codex")
	touched, err := a.Install(home, t.TempDir())
	if err != nil || !slices.Contains(touched, hooksJSON) {
		t.Fatalf("install: %q %v", touched, err)
	}
	b, _ := os.ReadFile(hooksJSON)
	if !strings.Contains(string(b), "say hi") || strings.Contains(string(b), "/opt/old/") || strings.Count(string(b), "/opt/new/conductor notify --codex-hook") != 4 {
		t.Fatalf("hooks.json:\n%s", b)
	}
	if ok, _ := a.Status(home); !ok {
		t.Fatal("status after the repair")
	}
}

// The snippet covers both of Codex's files, and says when the hooks run.
func TestCodexSnippetCoversBothFiles(t *testing.T) {
	useBin(t, "/opt/conductor")
	a, _ := Get("codex")
	s := a.Snippet(t.TempDir())
	for _, want := range []string{"# >>> conductor", `notify = ["/opt/conductor", "notify", "--codex"]`, "hooks.json", "features.hooks", `"PermissionRequest"`, "/opt/conductor notify --codex-hook"} {
		if !strings.Contains(s, want) {
			t.Errorf("snippet lacks %q:\n%s", want, s)
		}
	}
}

// omp lists the extension by a path with a quote and a backslash in it once:
// the check for it compares what was written.
func TestInstallOmpListsAnOddPathOnce(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := filepath.Join(t.TempDir(), `my "odd\ home`)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	a, _ := Get("omp")
	if _, err := a.Install(home, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if again, err := a.Install(home, t.TempDir()); err != nil || len(again) != 0 {
		t.Fatalf("second install: %q %v", again, err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".omp", "agent", "config.yml"))
	items := regexp.MustCompile(`(?m)^  - (".*")$`).FindAllSubmatch(b, -1)
	var got string
	if len(items) != 1 || json.Unmarshal(items[0][1], &got) != nil || got != filepath.Join(home, ".omp", "agent", "extensions", "conductor.ts") {
		t.Fatalf("config.yml:\n%s", b)
	}
	if ok, _ := a.Status(home); !ok {
		t.Fatal("status after install")
	}
}

func TestCheckAdapter(t *testing.T) {
	for _, id := range []string{"", "claude", "dsh"} {
		if err := CheckAdapter(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	if err := CheckAdapter("gemini"); err == nil || !strings.Contains(err.Error(), `"gemini"`) || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("gemini: %v", err)
	}
}

// A skill file of the user's own is left to them with its own instruction;
// the settings snippet would be beside the point. Any other step left to the
// user wants the snippet.
func TestSnippetNeededOnlyForStepsOtherThanTheSkill(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	skill := filepath.Join(home, filepath.FromSlash(claudeSkill))
	if err := os.MkdirAll(filepath.Dir(skill), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# my own skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := Get("claude")
	_, err := a.Install(home, t.TempDir())
	if !errors.Is(err, ErrByHand) || SnippetNeeded(err) {
		t.Fatalf("the skill alone: %v (snippet %v)", err, SnippetNeeded(err))
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere.json"), settings); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Install(home, t.TempDir()); !SnippetNeeded(err) {
		t.Fatalf("the settings left by hand: %v", err)
	}
	if SnippetNeeded(nil) || SnippetNeeded(errors.New("disk full")) {
		t.Fatal("no step left to the user wants no snippet")
	}
	d, _ := Get("dsh")
	if _, err := d.Install(home, t.TempDir()); !SnippetNeeded(err) {
		t.Fatalf("dsh: %v", err)
	}
}
