package catalog

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// builtIns is the IDs of the built-in agents in order, comma separated.
const builtIns = "claude,codex,agy,copilot,cursor,opencode,pi,omp,aider,goose,amp,dsh,shell"

func TestDefaultContainsBuiltIns(t *testing.T) {
	if got := idsOf(Default()); got != builtIns {
		t.Fatalf("built-ins %s, want %s", got, builtIns)
	}
}

func TestLoadMergesByID(t *testing.T) {
	c, err := Load(File{Agents: []Agent{
		{ID: "claude", Name: "Claude (pinned)", Command: []string{"/opt/claude", "--model", "opus"}},
		{ID: "my-tool", Name: "My tool", Command: []string{"mytool"}, Env: map[string]string{"TOKEN": "x"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := c.Get("claude")
	if a.Name != "Claude (pinned)" || a.Command[0] != "/opt/claude" {
		t.Fatalf("override not applied: %+v", a)
	}
	if len(c.List()) != 14 {
		t.Fatalf("expected 14 agents, got %d", len(c.List()))
	}
	if r, _ := c.Get("my-tool"); r.Redacted().Env["TOKEN"] != "***" {
		t.Fatal("env not redacted")
	}
}

func TestLoadDisableDefaults(t *testing.T) {
	c, err := Load(File{DisableDefaults: true, Agents: []Agent{{ID: "x", Name: "X", Command: []string{"x"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.List()) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(c.List()))
	}
	if _, err := Load(File{DisableDefaults: true}); err == nil {
		t.Fatal("expected error for empty catalog")
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	cases := []Agent{
		{ID: "Bad ID", Name: "x", Command: []string{"x"}},
		{ID: "ok", Name: "", Command: []string{"x"}},
		{ID: "ok", Name: "x", Command: nil},
		{ID: "ok", Name: "x", Command: []string{"x\x00y"}},
		{ID: "ok", Name: "x", Command: []string{"x"}, Env: map[string]string{"A=B": "c"}},
		{ID: "ok", Name: "x", Command: []string{"x"}, Signal: &Signal{Kind: "nope"}},
		{ID: "ok", Name: "x", Command: []string{"x"}, EnvPassthrough: []string{"not a name"}},
		{ID: "ok", Name: strings.Repeat("a", 61), Command: []string{"x"}},
	}
	for i, a := range cases {
		if _, err := Load(File{Agents: []Agent{a}}); err == nil {
			t.Fatalf("case %d: expected error", i)
		} else if !strings.Contains(err.Error(), "agents[0]") {
			t.Fatalf("case %d: unexpected error %v", i, err)
		}
	}
}

func TestSignalValidation(t *testing.T) {
	base := Agent{ID: "x", Name: "X", Command: []string{"x"}}
	ok := base
	ok.Signal = &Signal{Kind: "pattern", Pattern: `^> $`}
	if err := validate(ok); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Signal{{Kind: "nope"}, {Kind: "pattern"}, {Kind: "pattern", Pattern: "("}, {Kind: "pattern", Pattern: strings.Repeat("a", 201)}, {Kind: "bell", Pattern: "x"}} {
		a := base
		a.Signal = &s
		if err := validate(a); err == nil {
			t.Fatalf("signal %+v accepted", s)
		}
	}
	if got := base.EffectiveSignal(); got.Kind != "bell" {
		t.Fatalf("default signal %+v", got)
	}
}

// CompilePattern is the one rule for a screen pattern, for the catalog and
// for `conductor host --signal-pattern` alike.
func TestCompilePattern(t *testing.T) {
	re, err := CompilePattern(`^> $`)
	if err != nil || !re.MatchString("> ") || re.MatchString(">") {
		t.Fatalf("CompilePattern(^> $) = %v, %v", re, err)
	}
	if _, err := CompilePattern(strings.Repeat("a", 200)); err != nil {
		t.Fatalf("a pattern of 200 bytes was rejected: %v", err)
	}
	for _, p := range []string{"", "(", `(?P<n>`, `a**`, strings.Repeat("a", 201)} {
		if re, err := CompilePattern(p); err == nil || re != nil {
			t.Fatalf("CompilePattern(%.20q) = %v, %v; want an error", p, re, err)
		}
	}
}

// A pattern that matches an empty line would match a screen that shows
// nothing, and mark every quiet session as waiting.
func TestCompilePatternRejectsAPatternThatMatchesAnEmptyLine(t *testing.T) {
	for _, p := range []string{`^$`, `.*`, `x*`, `(?:)`, `\s*$`, `^\s*$`, `a?`, `$`, `(?i)`, `^|x`} {
		re, err := CompilePattern(p)
		if err == nil || re != nil || !strings.Contains(err.Error(), "empty line") {
			t.Fatalf("CompilePattern(%q) = %v, %v; want an error about an empty line", p, re, err)
		}
		a := Agent{ID: "x", Name: "X", Command: []string{"x"}, Signal: &Signal{Kind: "pattern", Pattern: p}}
		if err := validate(a); err == nil {
			t.Fatalf("an agent whose signal pattern is %q was accepted", p)
		}
	}
	// A pattern that needs some text stays valid, whatever else it says.
	for _, p := range []string{`^> $`, `\? $`, `\(Y\)es/\(N\)o\s*$`, `x+`, `\S`, `\b|x`} {
		if _, err := CompilePattern(p); err != nil {
			t.Fatalf("CompilePattern(%q): %v", p, err)
		}
	}
}

func TestOverlayUpsertsAndHides(t *testing.T) {
	c := Default()
	if err := c.ApplyOverlay(Overlay{
		Agents: []Agent{{ID: "claude", Name: "Claude (opus)", Command: []string{"claude", "--model", "opus"}, AllowArgs: true}, {ID: "zed", Name: "Zed", Command: []string{"zed"}}},
		Hidden: []string{"shell"},
	}); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.Get("claude"); a.Name != "Claude (opus)" || a.Command[2] != "opus" {
		t.Fatalf("upsert did not replace: %+v", a)
	}
	if _, ok := c.Get("shell"); ok {
		t.Fatal("hidden agent still visible")
	}
	if _, ok := c.Get("zed"); !ok {
		t.Fatal("new agent missing")
	}
	ids := []string{}
	for _, a := range c.List() {
		ids = append(ids, a.ID)
	}
	if ids[0] != "claude" || ids[len(ids)-1] != "zed" {
		t.Fatalf("order: %v", ids)
	}
	if err := c.Upsert(Agent{ID: "Bad ID", Name: "x", Command: []string{"x"}}); err == nil {
		t.Fatal("invalid id accepted")
	}
}

// idsOf lists a catalog's agent IDs in order, comma separated.
func idsOf(c Catalog) string {
	ids := []string{}
	for _, a := range c.List() {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, ",")
}

func TestSignalKinds(t *testing.T) {
	base := Agent{ID: "x", Name: "X", Command: []string{"x"}}
	valid := []Signal{
		{Kind: "hook"},
		{Kind: "bell"},
		{Kind: "none"},
		{Kind: "hook", ToolEvents: true},
		{Kind: "none", ToolEvents: true},
		{Kind: "pattern", Pattern: `^> $`, ToolEvents: true},
		{Kind: "pattern", Pattern: strings.Repeat("a", 200)},
	}
	for _, s := range valid {
		a := base
		a.Signal = &s
		if err := validate(a); err != nil {
			t.Fatalf("signal %+v rejected: %v", s, err)
		}
	}
	for _, s := range []Signal{{}, {Kind: "Hook"}, {Kind: "hook", Pattern: "x"}, {Kind: "none", Pattern: "x"}} {
		a := base
		a.Signal = &s
		if err := validate(a); err == nil {
			t.Fatalf("signal %+v accepted", s)
		}
	}
}

func TestEffectiveSignal(t *testing.T) {
	var a Agent
	if got := a.EffectiveSignal(); got != (Signal{Kind: "bell"}) {
		t.Fatalf("default signal %+v", got)
	}
	a.Signal = &Signal{Kind: "hook", ToolEvents: true}
	if got := a.EffectiveSignal(); got != *a.Signal {
		t.Fatalf("explicit signal %+v", got)
	}
}

func TestEnvPassthroughValidation(t *testing.T) {
	base := Agent{ID: "x", Name: "X", Command: []string{"x"}}
	ok := base
	// Proxy variables are lower case by convention, so both cases must pass.
	ok.EnvPassthrough = []string{"HTTP_PROXY", "_PRIVATE", "OPENAI_API_KEY", "A1", "http_proxy", "https_proxy", "no_proxy", "Mixed_Case9"}
	if err := validate(ok); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "1BAD", "1bad", "BAD-NAME", "bad-name", "A=B", "A B", "A\n", "A\x00", "$HOME"} {
		a := base
		a.EnvPassthrough = []string{"OK", name}
		if err := validate(a); err == nil {
			t.Fatalf("env name %q accepted", name)
		}
	}
	full := base
	for i := 1; i <= 32; i++ {
		full.EnvPassthrough = append(full.EnvPassthrough, strings.Repeat("V", i))
	}
	if err := validate(full); err != nil {
		t.Fatalf("32 entries rejected: %v", err)
	}
	full.EnvPassthrough = append(full.EnvPassthrough, "ONE_MORE")
	if err := validate(full); err == nil {
		t.Fatal("33 entries accepted")
	}
}

func TestSizeLimits(t *testing.T) {
	// Name and description are measured in characters, so multibyte text counts
	// once per rune. Command elements are measured in bytes, so the same text
	// counts in full.
	const (
		twoByte  = "\u00e9"     // one character, 2 bytes
		fourByte = "\U0001F600" // one character, 4 bytes
	)
	env := func(n int) map[string]string {
		m := make(map[string]string, n)
		for i := 1; i <= n; i++ {
			m[strings.Repeat("K", i)] = "v"
		}
		return m
	}
	cases := []struct {
		name   string
		change func(*Agent)
		field  string // the field the error must name; empty when the agent is valid
	}{
		{"name 60 characters", func(a *Agent) { a.Name = strings.Repeat("a", 60) }, ""},
		{"name 61 characters", func(a *Agent) { a.Name = strings.Repeat("a", 61) }, "name"},
		{"name 60 two-byte characters", func(a *Agent) { a.Name = strings.Repeat(twoByte, 60) }, ""},
		{"name 61 two-byte characters", func(a *Agent) { a.Name = strings.Repeat(twoByte, 61) }, "name"},
		{"name 60 four-byte characters", func(a *Agent) { a.Name = strings.Repeat(fourByte, 60) }, ""},
		{"name 61 four-byte characters", func(a *Agent) { a.Name = strings.Repeat(fourByte, 61) }, "name"},
		{"description 200 characters", func(a *Agent) { a.Description = strings.Repeat("a", 200) }, ""},
		{"description 201 characters", func(a *Agent) { a.Description = strings.Repeat("a", 201) }, "description"},
		{"description 200 two-byte characters", func(a *Agent) { a.Description = strings.Repeat(twoByte, 200) }, ""},
		{"description 201 two-byte characters", func(a *Agent) { a.Description = strings.Repeat(twoByte, 201) }, "description"},
		{"command 32 elements", func(a *Agent) { a.Command = slices.Repeat([]string{"x"}, 32) }, ""},
		{"command 33 elements", func(a *Agent) { a.Command = slices.Repeat([]string{"x"}, 33) }, "command"},
		{"command element 4096 bytes", func(a *Agent) { a.Command = []string{strings.Repeat("a", 4096)} }, ""},
		{"command element 4097 bytes", func(a *Agent) { a.Command = []string{strings.Repeat("a", 4097)} }, "command"},
		{"later command element 4097 bytes", func(a *Agent) { a.Command = []string{"x", strings.Repeat("a", 4097)} }, "command"},
		{"command element 2048 two-byte characters", func(a *Agent) { a.Command = []string{strings.Repeat(twoByte, 2048)} }, ""},
		{"command element 2049 two-byte characters", func(a *Agent) { a.Command = []string{strings.Repeat(twoByte, 2049)} }, "command"},
		{"env 32 keys", func(a *Agent) { a.Env = env(32) }, ""},
		{"env 33 keys", func(a *Agent) { a.Env = env(33) }, "env"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := Agent{ID: "x", Name: "X", Command: []string{"x"}}
			c.change(&a)
			err := validate(a)
			switch {
			case c.field == "" && err != nil:
				t.Fatalf("valid agent rejected: %v", err)
			case c.field != "" && err == nil:
				t.Fatal("agent over the limit accepted")
			case c.field != "" && !strings.Contains(err.Error(), c.field):
				t.Fatalf("error does not name %q: %v", c.field, err)
			}
		})
	}
}

func TestUpsertKeepsPositionAndHideRemoves(t *testing.T) {
	c := Default()
	if err := c.Upsert(Agent{ID: "codex", Name: "Codex (pinned)", Command: []string{"codex", "--yolo"}}); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(c); got != builtIns {
		t.Fatalf("replacing an agent moved it: %s", got)
	}
	if err := c.Upsert(Agent{ID: "zed", Name: "Zed", Command: []string{"zed"}}); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(c); got != builtIns+",zed" {
		t.Fatalf("new agent not appended: %s", got)
	}
	if !c.Hide("agy") {
		t.Fatal("hiding an existing agent reported false")
	}
	withoutAgy := strings.Replace(builtIns, "agy,", "", 1)
	if got := idsOf(c); got != withoutAgy+",zed" {
		t.Fatalf("hide left the wrong order: %s", got)
	}
	if c.Hide("agy") || c.Hide("never-there") {
		t.Fatal("hiding an unknown agent reported true")
	}
	// A hidden agent can come back; it is a new entry, so it goes last.
	if err := c.Upsert(Agent{ID: "agy", Name: "Antigravity", Command: []string{"agy"}}); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(c); got != withoutAgy+",zed,agy" {
		t.Fatalf("re-added agent misplaced: %s", got)
	}
	// A rejected upsert changes nothing.
	if err := c.Upsert(Agent{ID: "claude", Name: "", Command: []string{"claude"}}); err == nil {
		t.Fatal("empty name accepted")
	}
	if a, _ := c.Get("claude"); a.Name != "Claude Code" {
		t.Fatalf("rejected upsert modified the entry: %+v", a)
	}
}

func TestApplyOverlayIsAllOrNothing(t *testing.T) {
	c := Default()
	err := c.ApplyOverlay(Overlay{
		Agents: []Agent{
			{ID: "fine", Name: "Fine", Command: []string{"fine"}},
			{ID: "Bad ID", Name: "Bad", Command: []string{"bad"}},
		},
		Hidden: []string{"shell"},
	})
	if err == nil || !strings.Contains(err.Error(), "agents[1]") {
		t.Fatalf("expected an error naming agents[1], got %v", err)
	}
	if got := idsOf(c); got != builtIns {
		t.Fatalf("failed overlay changed the catalog: %s", got)
	}
	if _, ok := c.Get("fine"); ok {
		t.Fatal("failed overlay left its first agent behind")
	}
}

func TestApplyOverlayHidesLast(t *testing.T) {
	c := Default()
	// Unknown ids are not an error, and an empty overlay is a no-op.
	if err := c.ApplyOverlay(Overlay{Hidden: []string{"nope"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyOverlay(Overlay{}); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(c); got != builtIns {
		t.Fatalf("no-op overlay changed the catalog: %s", got)
	}
	// Agents apply first, so hiding wins over an agent with the same id.
	if err := c.ApplyOverlay(Overlay{
		Agents: []Agent{{ID: "zed", Name: "Zed", Command: []string{"zed"}}},
		Hidden: []string{"zed", "shell"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(c); got != strings.TrimSuffix(builtIns, ",shell") {
		t.Fatalf("hidden did not win: %s", got)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	orig, err := Load(File{Agents: []Agent{{
		ID: "tool", Name: "Tool", Command: []string{"tool", "--flag"},
		Env:            map[string]string{"TOKEN": "x"},
		EnvPassthrough: []string{"TOOL_HOME"},
		Signal:         &Signal{Kind: "pattern", Pattern: `^> $`},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	clone := orig.Clone()
	if !reflect.DeepEqual(orig.List(), clone.List()) {
		t.Fatalf("clone differs from the original:\n%+v\n%+v", clone.List(), orig.List())
	}

	// Structural edits to the clone do not reach the original.
	clone.Hide("claude")
	if err := clone.Upsert(Agent{ID: "extra", Name: "Extra", Command: []string{"extra"}}); err != nil {
		t.Fatal(err)
	}
	// Neither do edits made in place to an agent's slices, map or signal.
	a, _ := clone.Get("tool")
	a.Command[0] = "changed"
	a.Env["TOKEN"] = "changed"
	a.EnvPassthrough[0] = "CHANGED"
	a.Signal.Pattern = "changed"

	if got := idsOf(orig); got != builtIns+",tool" {
		t.Fatalf("original order changed: %s", got)
	}
	b, _ := orig.Get("tool")
	if b.Command[0] != "tool" || b.Env["TOKEN"] != "x" || b.EnvPassthrough[0] != "TOOL_HOME" || b.Signal.Pattern != `^> $` {
		t.Fatalf("original agent changed through the clone: %+v signal %+v", b, b.Signal)
	}
}

func TestZeroCatalogIsUsable(t *testing.T) {
	var c Catalog
	if c.Hide("x") || len(c.List()) != 0 {
		t.Fatal("zero catalog is not empty")
	}
	clone := c.Clone()
	if err := clone.Upsert(Agent{ID: "x", Name: "X", Command: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(Agent{ID: "y", Name: "Y", Command: []string{"y"}}); err != nil {
		t.Fatal(err)
	}
	if idsOf(c) != "y" || idsOf(clone) != "x" {
		t.Fatalf("zero catalog and its clone share state: %q %q", idsOf(c), idsOf(clone))
	}
}

func TestRedactedKeepsRoutingFields(t *testing.T) {
	a := Agent{
		ID: "x", Name: "X", Command: []string{"x"},
		Env:            map[string]string{"TOKEN": "secret"},
		EnvPassthrough: []string{"HOME_TOKEN"},
		Adapter:        "claude",
		Signal:         &Signal{Kind: "pattern", Pattern: `^> $`, ToolEvents: true},
	}
	r := a.Redacted()
	if r.Env["TOKEN"] != "***" {
		t.Fatalf("env not redacted: %v", r.Env)
	}
	if a.Env["TOKEN"] != "secret" {
		t.Fatal("redaction changed the original")
	}
	if r.Adapter != "claude" || !slices.Equal(r.EnvPassthrough, a.EnvPassthrough) || r.Signal == nil || *r.Signal != *a.Signal {
		t.Fatalf("redaction dropped non-secret fields: %+v", r)
	}
}

func TestAgentJSONShape(t *testing.T) {
	overlay := Overlay{
		Agents: []Agent{
			{
				ID: "x", Name: "X", Command: []string{"x"},
				EnvPassthrough: []string{"FOO"},
				Adapter:        "claude",
				Signal:         &Signal{Kind: "pattern", Pattern: "ready$", ToolEvents: true},
			},
			{ID: "y", Name: "Y", Command: []string{"y"}, Signal: &Signal{Kind: "hook"}},
		},
		Hidden: []string{"shell"},
	}
	b, err := json.Marshal(overlay)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"agents":[` +
		`{"id":"x","name":"X","command":["x"],"allowArgs":false,"envPassthrough":["FOO"],"adapter":"claude","signal":{"kind":"pattern","pattern":"ready$","toolEvents":true}},` +
		`{"id":"y","name":"Y","command":["y"],"allowArgs":false,"signal":{"kind":"hook"}}` +
		`],"hidden":["shell"]}`
	if string(b) != want {
		t.Fatalf("json shape changed:\n got %s\nwant %s", b, want)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var back Overlay
	if err := dec.Decode(&back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, overlay) {
		t.Fatalf("round trip differs:\n got %+v\nwant %+v", back, overlay)
	}
}

func TestReadFileRejectsTrailingData(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"agents":[]} x`, "after the JSON value"},
		{`{"agents":[]}{"agents":[]}`, "more than one JSON value"},
		{"", "empty document"},
	} {
		path := filepath.Join(t.TempDir(), "agents.json")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
			t.Errorf("%q: %v", tc.body, err)
		}
	}
}

// Every field an agent carries into a launch or a page is bounded, whether it
// comes from the config or from the Agents page.
func TestValidateBoundsCwdIconAdapterAndEnv(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Agent)
		field  string // the field the error must name; empty when the agent is valid
	}{
		{"cwd of 4096 bytes", func(a *Agent) { a.Cwd = "/" + strings.Repeat("d", 4095) }, ""},
		{"cwd of 4097 bytes", func(a *Agent) { a.Cwd = "/" + strings.Repeat("d", 4096) }, "cwd"},
		{"cwd with NUL", func(a *Agent) { a.Cwd = "/srv\x00x" }, "cwd"},
		{"a lucide icon", func(a *Agent) { a.Icon = "i-lucide-pi-square" }, ""},
		{"icon of 64 bytes", func(a *Agent) { a.Icon = "i" + strings.Repeat("-", 63) }, ""},
		{"icon of 65 bytes", func(a *Agent) { a.Icon = "i" + strings.Repeat("-", 64) }, "icon"},
		{"icon with a space", func(a *Agent) { a.Icon = "i-lucide-x y" }, "icon"},
		{"icon in upper case", func(a *Agent) { a.Icon = "I-Lucide-X" }, "icon"},
		{"adapter", func(a *Agent) { a.Adapter = "claude" }, ""},
		{"adapter of 33 characters", func(a *Agent) { a.Adapter = strings.Repeat("a", 33) }, "adapter"},
		{"adapter with a slash", func(a *Agent) { a.Adapter = "../x" }, "adapter"},
		{"env key of 128 bytes", func(a *Agent) { a.Env = map[string]string{strings.Repeat("K", 128): "v"} }, ""},
		{"env key of 129 bytes", func(a *Agent) { a.Env = map[string]string{strings.Repeat("K", 129): "v"} }, "env"},
		{"env value of 16384 bytes", func(a *Agent) { a.Env = map[string]string{"K": strings.Repeat("v", 16384)} }, ""},
		{"env value of 16385 bytes", func(a *Agent) { a.Env = map[string]string{"K": strings.Repeat("v", 16385)} }, "env"},
		{"envPassthrough name of 128 bytes", func(a *Agent) { a.EnvPassthrough = []string{strings.Repeat("P", 128)} }, ""},
		{"envPassthrough name of 129 bytes", func(a *Agent) { a.EnvPassthrough = []string{strings.Repeat("P", 129)} }, "envPassthrough"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := Agent{ID: "x", Name: "X", Command: []string{"x"}}
			c.change(&a)
			err := validate(a)
			switch {
			case c.field == "" && err != nil:
				t.Fatalf("valid agent rejected: %v", err)
			case c.field != "" && err == nil:
				t.Fatal("agent over the limit accepted")
			case c.field != "" && !strings.Contains(err.Error(), c.field):
				t.Fatalf("error does not name %q: %v", c.field, err)
			}
		})
	}
}

// A saved agent that replaces another keeps what it leaves out: the adapter,
// the signal, and every env value it holds as the mask, which the Agents page
// stores for a key it did not change. A masked key the replaced agent does not
// have is dropped, and so is every masked value of an agent that replaces
// nothing.
func TestOverlayInheritsWhatAnOverrideLeavesOut(t *testing.T) {
	base, err := Load(File{DisableDefaults: true, Agents: []Agent{
		{ID: "keyed", Name: "keyed", Command: []string{"k"}, Adapter: "claude", Signal: &Signal{Kind: SignalPattern, Pattern: `^> $`},
			Env: map[string]string{"API_KEY": "v1", "REGION": "eu"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ov := Overlay{Agents: []Agent{
		{ID: "keyed", Name: "Keyed", Command: []string{"k2"}, Env: map[string]string{"API_KEY": RedactedValue, "REGION": "us", "GONE": RedactedValue}},
		{ID: "fresh", Name: "fresh", Command: []string{"f"}, Env: map[string]string{"X": RedactedValue, "Y": "y"}},
	}}
	got := base.Clone()
	if err := got.ApplyOverlay(ov); err != nil {
		t.Fatal(err)
	}
	k, _ := got.Get("keyed")
	if k.Command[0] != "k2" || k.Adapter != "claude" || k.Signal == nil || k.Signal.Kind != SignalPattern || k.Signal.Pattern != `^> $` ||
		!reflect.DeepEqual(k.Env, map[string]string{"API_KEY": "v1", "REGION": "us"}) {
		t.Fatalf("keyed %+v (signal %+v)", k, k.Signal)
	}
	if f, _ := got.Get("fresh"); !reflect.DeepEqual(f.Env, map[string]string{"Y": "y"}) || f.Adapter != "" || f.Signal != nil {
		t.Fatalf("fresh %+v", f)
	}
	// What an override names it keeps; an env it does not list is not inherited.
	own := base.Clone()
	if err := own.ApplyOverlay(Overlay{Agents: []Agent{{ID: "keyed", Name: "k", Command: []string{"k"}, Adapter: "codex", Signal: &Signal{Kind: SignalNone}}}}); err != nil {
		t.Fatal(err)
	}
	if k, _ := own.Get("keyed"); k.Adapter != "codex" || k.Signal.Kind != SignalNone || len(k.Env) != 0 {
		t.Fatalf("own %+v", k)
	}
	// Neither the overlay nor the base changes.
	if ov.Agents[0].Env["API_KEY"] != RedactedValue || ov.Agents[0].Adapter != "" {
		t.Fatal("ApplyOverlay changed the overlay")
	}
	if b, _ := base.Get("keyed"); b.Env["REGION"] != "eu" {
		t.Fatal("ApplyOverlay changed the base")
	}
}

// An override's env value equal to the replaced agent's is that agent's value,
// as the mask is, and a value of its own stays. A rotation in the config
// reaches an override saved with the old value once a save has stored the mask
// for it (internal/api, keepMaskedEnv).
func TestOverlayValueEqualToTheBaseIsTheBase(t *testing.T) {
	apply := func(secret, saved string) string {
		t.Helper()
		c, err := Load(File{DisableDefaults: true, Agents: []Agent{{ID: "keyed", Name: "keyed", Command: []string{"k"}, Env: map[string]string{"API_KEY": secret}}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.ApplyOverlay(Overlay{Agents: []Agent{{ID: "keyed", Name: "keyed", Command: []string{"k"}, Env: map[string]string{"API_KEY": saved}}}}); err != nil {
			t.Fatal(err)
		}
		a, _ := c.Get("keyed")
		return a.Env["API_KEY"]
	}
	for _, tc := range []struct{ secret, saved, want string }{
		{"v1", "v1", "v1"},
		{"v2", RedactedValue, "v2"}, // what the next save stores for "v1" while the config held it
		{"v1", "other", "other"},
		{"v2", "other", "other"},
	} {
		if got := apply(tc.secret, tc.saved); got != tc.want {
			t.Errorf("config %s, saved %s: runs with %s, want %s", tc.secret, tc.saved, got, tc.want)
		}
	}
}

func TestSourceOfEachAgent(t *testing.T) {
	c, err := Load(File{Agents: []Agent{
		{ID: "claude", Name: "Claude (pinned)", Command: []string{"claude"}},
		{ID: "my-tool", Name: "My tool", Command: []string{"mytool"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]Source{"codex": SourceBuiltIn, "claude": SourceConfig, "my-tool": SourceConfig, "nope": ""} {
		if got := c.Source(id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
	err = c.ApplyOverlay(Overlay{
		Agents: []Agent{{ID: "codex", Name: "Codex", Command: []string{"codex"}}, {ID: "added", Name: "Added", Command: []string{"a"}}},
		Hidden: []string{"my-tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]Source{"codex": SourceSaved, "added": SourceSaved, "my-tool": "", "shell": SourceBuiltIn} {
		if got := c.Source(id); got != want {
			t.Errorf("after the overlay, %s: %q, want %q", id, got, want)
		}
	}
}

func TestUpsertStoresACopy(t *testing.T) {
	var c Catalog
	a := Agent{ID: "x", Name: "X", Command: []string{"x"}, Env: map[string]string{"K": "v"}, Signal: &Signal{Kind: SignalBell}}
	if err := c.Upsert(a); err != nil {
		t.Fatal(err)
	}
	a.Command[0], a.Env["K"], a.Signal.Kind = "changed", "changed", SignalNone
	if got, _ := c.Get("x"); got.Command[0] != "x" || got.Env["K"] != "v" || got.Signal.Kind != SignalBell {
		t.Fatalf("the catalog shares the caller's agent: %+v", got)
	}
	// Clone takes a value: a catalog a call returns clones without a variable.
	if ids := idsOf(Default().Clone()); ids != builtIns {
		t.Fatalf("clone lists %s", ids)
	}
}

// The workbench fetches no icon at runtime: it bundles the agent icons that
// web/app/utils/agentIcons.ts lists (AGENT_ICONS, which web/nuxt.config.ts
// takes), and the built-in agents' icons reach it from the server. The list
// holds exactly the built-ins' icons and two more: i-lucide-bot, which an
// agent without an icon, or with one the list lacks, shows
// (AGENT_ICON_FALLBACK), and i-lucide-wrench, the tool icon of the example
// config (conductor.example.json). A built-in icon missing from the list would
// show the fallback; a name nothing uses only weighs on the bundle.
func TestBuiltInIconsAreInTheWorkbenchBundle(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "app", "utils", "agentIcons.ts"))
	if err != nil {
		t.Fatalf("the workbench's icon list: %v", err)
	}
	list := regexp.MustCompile(`(?s)export const AGENT_ICONS\b[^=]*=\s*\[(.*?)\]`).FindSubmatch(b)
	if list == nil {
		t.Fatal("web/app/utils/agentIcons.ts has no AGENT_ICONS = [...] list")
	}
	lucide := regexp.MustCompile(`^i-lucide-[a-z0-9-]+$`)
	bundled := map[string]bool{}
	for _, quoted := range regexp.MustCompile(`'([^']*)'`).FindAllSubmatch(list[1], -1) {
		name := string(quoted[1])
		if !lucide.MatchString(name) {
			t.Errorf("AGENT_ICONS holds %q, not an i-lucide-<name> icon", name)
			continue
		}
		bundled[name] = true
	}
	want := map[string]bool{"i-lucide-bot": true, "i-lucide-wrench": true}
	for _, a := range Default().List() {
		if a.Icon != "" {
			want[a.Icon] = true
		}
	}
	var missing, extra []string
	for _, name := range slices.Sorted(maps.Keys(want)) {
		if !bundled[name] {
			missing = append(missing, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(bundled)) {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("AGENT_ICONS in web/app/utils/agentIcons.ts lacks %q (a built-in agent's icon, or a generic one), and holds %q, which no built-in agent uses and which is neither generic icon", missing, extra)
	}
}

// site is an optional https URL with a host and no user info, at most 200 bytes.
func TestSiteValidation(t *testing.T) {
	base := Agent{ID: "x", Name: "X", Command: []string{"x"}}
	for _, ok := range []string{"", "https://example.com", "https://example.com/docs/cli?x=1", "https://example.com:8443/x", "https://example.com:65535"} {
		a := base
		a.Site = ok
		if err := validate(a); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://example.com", "example.com", "https://", "https://user:pw@example.com", "https://example.com/" + strings.Repeat("a", 200), "javascript:alert(1)", "https://exa mple.com",
		// What a browser's URL parser refuses too: no host name, a port out of range, a no-break space in the host.
		"https://:443", "https://example.com:99999", "https://example.com:0", "https://exa\u00a0mple.com"} {
		a := base
		a.Site = bad
		if err := validate(a); err == nil || !strings.Contains(err.Error(), "site") {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

// Every built-in but Shell names its website, held to the site rule. The
// three a check on 2026-10-02 found moved are pinned where they live now:
// developers.openai.com/codex/cli redirects to learn.chatgpt.com, Goose's
// docs left block.github.io, and github.com/deepseek-ai/dsh is a 404.
func TestDefaultsHaveSites(t *testing.T) {
	moved := map[string]string{
		"codex": "https://learn.chatgpt.com/docs/codex/cli",
		"goose": "https://goose-docs.ai",
		"dsh":   "https://github.com/deepseek-ai/deepseek-harness",
	}
	for _, a := range defaults() {
		if a.ID == "shell" {
			if a.Site != "" {
				t.Fatalf("shell has a site: %q", a.Site)
			}
			continue
		}
		if a.Site == "" {
			t.Errorf("%s: no site", a.ID)
		}
		if want, ok := moved[a.ID]; ok && a.Site != want {
			t.Errorf("%s: site %q, want %q", a.ID, a.Site, want)
		}
		if err := validate(a); err != nil {
			t.Errorf("%s: %v", a.ID, err)
		}
	}
}

// A saved override that leaves the site out keeps the built-in's, as it keeps
// its adapter and signal; one with a site of its own keeps that.
func TestOverlayInheritsTheSite(t *testing.T) {
	c := Default()
	if err := c.ApplyOverlay(Overlay{Agents: []Agent{
		{ID: "claude", Name: "Claude, mine", Command: []string{"claude"}},
		{ID: "codex", Name: "Codex", Command: []string{"codex"}, Site: "https://example.com/codex"},
	}}); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.Get("claude"); a.Site != "https://claude.com/claude-code" {
		t.Fatalf("claude: %q", a.Site)
	}
	if a, _ := c.Get("codex"); a.Site != "https://example.com/codex" {
		t.Fatalf("codex: %q", a.Site)
	}
}

// A yolo recipe is bounded like the command and the environment it adds to,
// and never names Conductor's own variables, which BuildEnv would drop.
func TestYoloValidation(t *testing.T) {
	ok := Agent{ID: "x", Name: "X", Command: []string{"x"}, Yolo: &Yolo{Args: []string{"--yes"}, Env: map[string]string{"X_MODE": "auto"}}}
	if err := validate(ok); err != nil {
		t.Fatal(err)
	}
	for _, y := range []Yolo{
		{Args: []string{""}},
		{Args: []string{"a\x00b"}},
		{Args: []string{strings.Repeat("a", 4097)}},
		{Args: slices.Repeat([]string{"-y"}, 17)},
		{Env: map[string]string{"CONDUCTOR_NOTIFY_TOKEN": "x"}},
		{Env: map[string]string{"BAD NAME": "x"}},
		{Env: map[string]string{"X": strings.Repeat("v", 4097)}},
		{Env: map[string]string{"X": "a\x00"}},
	} {
		a := ok
		a.Yolo = &y
		if err := validate(a); err == nil || !strings.Contains(err.Error(), "yolo") {
			t.Errorf("%+v: %v", y, err)
		}
	}
	empty := ok
	empty.Yolo = &Yolo{}
	if err := validate(empty); err != nil || !empty.Yolo.Empty() || !(*Yolo)(nil).Empty() {
		t.Fatalf("an empty recipe: %v", err)
	}
	bad := ok
	bad.TrustPrompt = ".*"
	if err := validate(bad); err == nil || !strings.Contains(err.Error(), "trustPrompt") {
		t.Fatalf("a trust prompt matching nothing: %v", err)
	}
}

// A saved override that leaves the recipe out takes the replaced agent's, an
// empty one has none, and one of its own replaces it whole; the trust prompt
// is inherited too. Redacted leaves the recipe as it is: its values are
// flags, not secrets.
func TestOverlayInheritsTheYoloRecipe(t *testing.T) {
	c := Default()
	if err := c.ApplyOverlay(Overlay{Agents: []Agent{
		{ID: "claude", Name: "Claude, mine", Command: []string{"claude"}},
		{ID: "codex", Name: "Codex", Command: []string{"codex"}, Yolo: &Yolo{}},
		{ID: "copilot", Name: "Copilot", Command: []string{"copilot"}, Yolo: &Yolo{Args: []string{"--allow-all-tools"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	claude, _ := c.Get("claude")
	if claude.Yolo == nil || !slices.Equal(claude.Yolo.Args, []string{"--dangerously-skip-permissions"}) || claude.TrustPrompt == "" {
		t.Fatalf("claude: %+v %q", claude.Yolo, claude.TrustPrompt)
	}
	if codex, _ := c.Get("codex"); !codex.Yolo.Empty() {
		t.Fatalf("codex: %+v", codex.Yolo)
	}
	copilot, _ := c.Get("copilot")
	if !slices.Equal(copilot.Yolo.Args, []string{"--allow-all-tools"}) || copilot.Yolo.Env != nil {
		t.Fatalf("copilot: %+v", copilot.Yolo)
	}
	if r := copilot.Redacted(); r.Yolo == nil || r.Yolo.Env != nil || !slices.Equal(r.Yolo.Args, copilot.Yolo.Args) {
		t.Fatalf("redacted: %+v", r.Yolo)
	}
	base, _ := Default().Get("copilot")
	if r := base.Redacted(); r.Yolo.Env["COPILOT_ALLOW_ALL"] != "true" {
		t.Fatalf("a recipe's env is not a secret: %+v", r.Yolo)
	}
	cp := c.Clone()
	got, _ := cp.Get("claude")
	got.Yolo.Args[0] = "changed"
	if again, _ := c.Get("claude"); again.Yolo.Args[0] != "--dangerously-skip-permissions" {
		t.Fatal("a clone shares the recipe")
	}
}

// The built-ins' recipes, as the yolo research found them (docs/features.md,
// the adapter matrix): pi and the shell have none.
func TestDefaultYoloRecipes(t *testing.T) {
	want := map[string]Yolo{
		"claude":   {Args: []string{"--dangerously-skip-permissions"}},
		"codex":    {Args: []string{"--dangerously-bypass-approvals-and-sandbox"}},
		"agy":      {Args: []string{"--dangerously-skip-permissions"}},
		"copilot":  {Args: []string{"--yolo"}, Env: map[string]string{"COPILOT_ALLOW_ALL": "true"}},
		"cursor":   {Args: []string{"--yolo", "--trust"}},
		"opencode": {Args: []string{"--auto"}},
		"omp":      {Args: []string{"--yolo"}},
		"aider":    {Args: []string{"--yes-always"}},
		"goose":    {Env: map[string]string{"GOOSE_MODE": "auto"}},
		"amp":      {Args: []string{"--dangerously-allow-all"}},
		"dsh":      {Env: map[string]string{"DSH_PERMISSION_MODE": "danger-full-access"}},
	}
	for _, a := range defaults() {
		w, ok := want[a.ID]
		if !ok {
			if !a.Yolo.Empty() {
				t.Errorf("%s has a recipe: %+v", a.ID, a.Yolo)
			}
			continue
		}
		if a.Yolo == nil || !slices.Equal(a.Yolo.Args, w.Args) || !maps.Equal(a.Yolo.Env, w.Env) {
			t.Errorf("%s: %+v, want %+v", a.ID, a.Yolo, w)
		}
	}
	// The words the CLIs drew in the prompt investigation's captures (Claude
	// Code 2.1.287, Codex 0.159.0), as the trust watcher reads a screen: each
	// escape sequence a space.
	for id, tc := range map[string]struct{ screen, words string }{
		"claude": {"root/c1 Quick safety check: Is this a project you created or one you trust? (Like your own code", "Is this a project you created or one you trust?"},
		"codex":  {"root/x1 Trust this folder? Codex can read, edit, and run files here", "Trust this folder?"},
	} {
		a, _ := Default().Get(id)
		re, err := CompilePattern(a.TrustPrompt)
		if words := tc.words; err != nil || re.FindString(tc.screen) != words {
			t.Errorf("%s's trust prompt %q does not find %q: %v", id, a.TrustPrompt, tc.words, err)
		}
	}
}

// A session recipe is bounded like a command, places {id} as a whole argument
// once, says how the id is known, and anchors an id pattern that cannot match
// an empty id or one that reads as a flag. The empty recipe disables an
// inherited one.
func TestSessionRecipeValidation(t *testing.T) {
	good := SessionRecipe{StartArgs: []string{"--session-id", IDArg}, ResumeArgs: []string{"--resume", IDArg}, IDPattern: `^[0-9a-f-]{36}$`}
	a := Agent{ID: "x", Name: "X", Command: []string{"x"}, Session: &good}
	if err := validate(a); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]SessionRecipe{
		"no resume args":       {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: []string{"--resume"}},
		"{id} twice":           {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: []string{IDArg, IDArg}},
		"{id} inside an arg":   {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: []string{"--resume=" + IDArg}},
		"no way to the id":     {IDPattern: good.IDPattern, ResumeArgs: good.ResumeArgs},
		"unanchored pattern":   {IDFrom: "hook", IDPattern: `[0-9a-f]+`, ResumeArgs: good.ResumeArgs},
		"pattern takes a flag": {IDFrom: "hook", IDPattern: `^.+$`, ResumeArgs: good.ResumeArgs},
		"pattern takes empty":  {IDFrom: "hook", IDPattern: `^[a-z]*$`, ResumeArgs: good.ResumeArgs},
		"bad policy":           {IDFrom: "hook", IDPolicy: "newest", IDPattern: good.IDPattern, ResumeArgs: good.ResumeArgs},
		"bad idFrom":           {IDFrom: "screen", IDPattern: good.IDPattern, ResumeArgs: good.ResumeArgs},
		"bad newId":            {StartArgs: good.StartArgs, NewID: "ulid", IDPattern: good.IDPattern, ResumeArgs: good.ResumeArgs},
		"NUL":                  {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: []string{"--resume\x00", IDArg}},
		"too many args":        {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: append(slices.Repeat([]string{"-x"}, 16), IDArg)},
	} {
		b := a
		b.Session = &r
		if err := validate(b); err == nil || !strings.Contains(err.Error(), "session") {
			t.Errorf("%s: %v", name, err)
		}
	}
	b := a
	b.Session = &SessionRecipe{}
	if err := validate(b); err != nil || !b.Session.Empty() || !(*SessionRecipe)(nil).Empty() {
		t.Fatalf("the empty recipe: %v", err)
	}
	if got := Expand([]string{"resume", IDArg, "-c", "x={id}y"}, "abc"); !slices.Equal(got, []string{"resume", "abc", "-c", "x={id}y"}) {
		t.Fatalf("Expand: %q", got)
	}
}

// The built-ins' session recipes, as the resume research found them (the
// adapter matrix says which are verified live): the agents whose ids reach
// Conductor only through a plugin, and the shell, have none yet.
func TestDefaultSessionRecipes(t *testing.T) {
	with := map[string][]string{
		"claude":  {"--resume", IDArg},
		"codex":   {"resume", IDArg, "-c", `tui.resume_cwd="session"`},
		"agy":     {"--conversation", IDArg},
		"copilot": {"--session-id", IDArg},
		"cursor":  {"--resume", IDArg},
		"pi":      {"--session-id", IDArg},
		"goose":   {"session", "--resume", "--name", IDArg},
	}
	for _, a := range defaults() {
		want, ok := with[a.ID]
		if !ok {
			if !a.Session.Empty() {
				t.Errorf("%s has a recipe: %+v", a.ID, a.Session)
			}
			continue
		}
		if a.Session.Empty() || !slices.Equal(a.Session.ResumeArgs, want) {
			t.Errorf("%s: %+v", a.ID, a.Session)
		}
	}
	claude, _ := Default().Get("claude")
	re := regexp.MustCompile(claude.Session.IDPattern)
	if !re.MatchString("3f80c8bd-0000-4000-8000-000000000001") || re.MatchString("--dangerously-skip-permissions") {
		t.Fatal("claude's id pattern")
	}
	codex, _ := Default().Get("codex")
	if codex.Session.IDPolicy != "lowest" || codex.Session.IDFrom != "hook" || len(codex.Session.StartArgs) != 0 {
		t.Fatalf("codex: %+v", codex.Session)
	}
	if c := Default(); c.ApplyOverlay(Overlay{Agents: []Agent{{ID: "claude", Name: "C", Command: []string{"claude"}}}}) != nil {
		t.Fatal("overlay")
	} else if a, _ := c.Get("claude"); a.Session.Empty() {
		t.Fatal("a saved override that leaves the recipe out lost the built-in's")
	}
}
