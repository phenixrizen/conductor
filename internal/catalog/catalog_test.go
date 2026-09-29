package catalog

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestDefaultContainsBuiltIns(t *testing.T) {
	c := Default()
	for _, id := range []string{"claude", "codex", "agy", "shell"} {
		if _, ok := c.Get(id); !ok {
			t.Fatalf("missing default agent %s", id)
		}
	}
	if got := c.List()[0].ID; got != "claude" {
		t.Fatalf("expected claude first, got %s", got)
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
	if len(c.List()) != 5 {
		t.Fatalf("expected 5 agents, got %d", len(c.List()))
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

func TestOverlayUpsertsAndHides(t *testing.T) {
	c := Default()
	if err := c.ApplyOverlay(Overlay{
		Agents: []Agent{{ID: "claude", Name: "Claude (opus)", Command: []string{"claude", "--model", "opus"}, AllowArgs: true}, {ID: "aider", Name: "Aider", Command: []string{"aider"}}},
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
	if _, ok := c.Get("aider"); !ok {
		t.Fatal("new agent missing")
	}
	ids := []string{}
	for _, a := range c.List() {
		ids = append(ids, a.ID)
	}
	if ids[0] != "claude" || ids[len(ids)-1] != "aider" {
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
	if got := idsOf(c); got != "claude,codex,agy,shell" {
		t.Fatalf("replacing an agent moved it: %s", got)
	}
	if err := c.Upsert(Agent{ID: "zed", Name: "Zed", Command: []string{"zed"}}); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(c); got != "claude,codex,agy,shell,zed" {
		t.Fatalf("new agent not appended: %s", got)
	}
	if !c.Hide("agy") {
		t.Fatal("hiding an existing agent reported false")
	}
	if got := idsOf(c); got != "claude,codex,shell,zed" {
		t.Fatalf("hide left the wrong order: %s", got)
	}
	if c.Hide("agy") || c.Hide("never-there") {
		t.Fatal("hiding an unknown agent reported true")
	}
	// A hidden agent can come back; it is a new entry, so it goes last.
	if err := c.Upsert(Agent{ID: "agy", Name: "Antigravity", Command: []string{"agy"}}); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(c); got != "claude,codex,shell,zed,agy" {
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
	if got := idsOf(c); got != "claude,codex,agy,shell" {
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
	if got := idsOf(c); got != "claude,codex,agy,shell" {
		t.Fatalf("no-op overlay changed the catalog: %s", got)
	}
	// Agents apply first, so hiding wins over an agent with the same id.
	if err := c.ApplyOverlay(Overlay{
		Agents: []Agent{{ID: "aider", Name: "Aider", Command: []string{"aider"}}},
		Hidden: []string{"aider", "shell"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(c); got != "claude,codex,agy" {
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

	if got := idsOf(orig); got != "claude,codex,agy,shell,tool" {
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

func TestDefaultAdaptersAndSignals(t *testing.T) {
	want := map[string]struct{ adapter, kind string }{
		"claude": {"claude", "hook"},
		"codex":  {"codex", "hook"},
		"agy":    {"agy", "bell"},
		"shell":  {"", "none"},
	}
	c := Default()
	for id, w := range want {
		a, ok := c.Get(id)
		if !ok {
			t.Fatalf("missing default agent %s", id)
		}
		if a.Adapter != w.adapter {
			t.Errorf("%s: adapter %q, want %q", id, a.Adapter, w.adapter)
		}
		if a.Signal == nil || a.Signal.Kind != w.kind {
			t.Errorf("%s: signal %+v, want kind %q", id, a.Signal, w.kind)
		}
		if err := validate(a); err != nil {
			t.Errorf("%s: built-in fails validation: %v", id, err)
		}
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
