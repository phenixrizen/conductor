package crew

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/store"
)

// validCrew is a crew that passes Validate: two members, the second starting
// once the first is done. Every call returns a new one.
func validCrew(id, name string) Crew {
	return Crew{
		ID: id, Name: name, Goal: "ship /v1/users", Cwd: "/srv/api",
		Where: "server", Isolation: "worktree", OpenAfterLaunch: true, ViewLinkTTLSeconds: 8 * 3600,
		Members: []Member{
			{Name: "lead", AgentID: "claude", Prompt: "Own the plan for $GOAL.", Start: Start{When: "immediately"}},
			{Name: "tests", AgentID: "shell", Prompt: "Write the tests.", Args: []string{"--model", "x"}, Start: Start{When: "after", Member: "lead"}},
		},
		CreatedAt: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 9, 29, 11, 0, 0, 0, time.UTC),
	}
}

func newStore(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

// clock makes s stamp every change with at.
func clock(s *Store, at time.Time) { s.now = func() time.Time { return at } }

// encodedSize is the length of c as JSON.
func encodedSize(t *testing.T, c Crew) int {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}

// crewOfEncodedSize returns a valid crew whose JSON is exactly size bytes:
// twelve members with prompts and args of "<", which JSON writes as six bytes
// (\u003c), and a goal of "g" making up the rest.
func crewOfEncodedSize(t *testing.T, id string, size int) Crew {
	t.Helper()
	c := validCrew(id, "Edge")
	c.Goal = ""
	c.Members = nil
	for i := range 12 {
		c.Members = append(c.Members, Member{Name: fmt.Sprintf("m%02d", i), AgentID: "shell", Prompt: strings.Repeat("<", 4000), Args: []string{""}, Start: Start{When: "manual"}})
	}
	lt := (size - encodedSize(t, c) - 1000) / 6 // leave about 1000 bytes to the goal
	for i := range c.Members {
		n := lt / 12
		if i == 0 {
			n += lt % 12
		}
		c.Members[i].Args[0] = strings.Repeat("<", n)
	}
	c.Goal = strings.Repeat("g", size-encodedSize(t, c))
	if got := encodedSize(t, c); got != size || c.Validate() != nil {
		t.Fatalf("crew of %d bytes: %v", got, c.Validate())
	}
	return c
}

func TestValidateRejectsBadMembers(t *testing.T) {
	if err := validCrew("api", "API").Validate(); err != nil {
		t.Fatalf("valid crew rejected: %v", err)
	}
	manual := func(name string) Member { return Member{Name: name, AgentID: "shell", Start: Start{When: "manual"}} }
	cases := []struct {
		name   string
		change func(*Crew)
		want   string // part of the message
	}{
		{"duplicate member names", func(c *Crew) { c.Members[1] = manual("lead") }, `"lead"`},
		{"member name Lead!", func(c *Crew) { c.Members[0].Name = "Lead!"; c.Members[1].Start.Member = "Lead!" }, "must match"},
		{"member name starting with a dot", func(c *Crew) { c.Members[1].Name = ".tests" }, "must match"},
		{"member name of 41 characters", func(c *Crew) { c.Members[1].Name = strings.Repeat("t", 41) }, "must match"},
		{"after pointing to itself", func(c *Crew) { c.Members[1].Start.Member = "tests" }, "itself"},
		{"after pointing to a missing member", func(c *Crew) { c.Members[1].Start.Member = "ghost" }, `"ghost"`},
		{"after without a member", func(c *Crew) { c.Members[1].Start.Member = "" }, "start.member"},
		{"member set without after", func(c *Crew) { c.Members[0].Start.Member = "tests" }, "start.member"},
		{"unknown start", func(c *Crew) { c.Members[0].Start.When = "later" }, `"later"`},
		{"no start", func(c *Crew) { c.Members[0].Start.When = "" }, "start.when"},
		{"13 members", func(c *Crew) {
			for i := len(c.Members); i < 13; i++ {
				c.Members = append(c.Members, manual(fmt.Sprintf("m%d", i)))
			}
		}, "at most 12"},
		{"prompt of 5000 runes", func(c *Crew) { c.Members[0].Prompt = strings.Repeat("p", 5000) }, "at most 4000"},
		{"no agent", func(c *Crew) { c.Members[0].AgentID = "" }, "agentId"},
		{"33 args", func(c *Crew) { c.Members[1].Args = slices.Repeat([]string{"x"}, 33) }, "at most 32"},
		{"arg over 4096 bytes", func(c *Crew) { c.Members[1].Args = []string{strings.Repeat("a", 4097)} }, "4096 bytes"},
		{"arg with NUL", func(c *Crew) { c.Members[1].Args = []string{"a\x00b"} }, "NUL"},
		{"blank crew name", func(c *Crew) { c.Name = "  " }, "name must not be empty"},
		{"crew name of 61 runes", func(c *Crew) { c.Name = strings.Repeat("é", 61) }, "at most 60"},
		{"goal of 2001 runes", func(c *Crew) { c.Goal = strings.Repeat("g", 2001) }, "at most 2000"},
		{"cwd over 4096 bytes", func(c *Crew) { c.Cwd = "/" + strings.Repeat("d", 4096) }, "at most 4096"},
		{"cwd with NUL", func(c *Crew) { c.Cwd = "/srv\x00/api" }, "NUL"},
		{"where elsewhere", func(c *Crew) { c.Where = "cloud" }, `"cloud"`},
		{"no where", func(c *Crew) { c.Where = "" }, "where"},
		{"isolation elsewhere", func(c *Crew) { c.Isolation = "container" }, `"container"`},
		{"no isolation", func(c *Crew) { c.Isolation = "" }, "isolation"},
		{"negative view link TTL", func(c *Crew) { c.ViewLinkTTLSeconds = -1 }, "viewLinkTtlSeconds"},
		{"view link TTL over a year", func(c *Crew) { c.ViewLinkTTLSeconds = 365*24*3600 + 1 }, "viewLinkTtlSeconds"},
		// Names the member pattern lets through but git refuses as a branch name.
		{"member name with two dots", func(c *Crew) { c.Members[1].Name = "a..b" }, "git"},
		{"member name ending in .lock", func(c *Crew) { c.Members[1].Name = "tests.lock" }, "git"},
		{"member name ending in a dot", func(c *Crew) { c.Members[1].Name = "tests." }, "git"},
		{"two members starting after each other", func(c *Crew) { c.Members[0].Start = Start{When: "after", Member: "tests"} }, "cycle"},
		{"three members starting after each other", func(c *Crew) {
			c.Members[0].Start = Start{When: "after", Member: "review"}
			c.Members = append(c.Members, Member{Name: "review", AgentID: "shell", Start: Start{When: "after", Member: "tests"}})
		}, "cycle"},
		{"a member starting after a cycle", func(c *Crew) {
			c.Members[0].Start = Start{When: "after", Member: "tests"}
			c.Members = append([]Member{{Name: "late", AgentID: "shell", Start: Start{When: "after", Member: "lead"}}}, c.Members...)
		}, "cycle"},
		{"args over 8 KiB in all", func(c *Crew) {
			c.Members[1].Args = []string{strings.Repeat("a", 4096), strings.Repeat("a", 4096), "b"}
		}, "8192 bytes"},
		{"agentId that is no agent id", func(c *Crew) { c.Members[0].AgentID = "Bad Id" }, `"Bad Id"`},
		{"agentId over 32 characters", func(c *Crew) { c.Members[0].AgentID = strings.Repeat("a", 33) }, "agentId"},
		{"name with a tab", func(c *Crew) { c.Name = "API\tsweep" }, "control"},
		{"name with an escape sequence", func(c *Crew) { c.Name = "API \x1b[31msweep" }, "control"},
		{"crew over 512 KiB as JSON", func(c *Crew) {
			// Within every other limit: 8 KiB of "<" per member is 48 KiB of JSON.
			c.Members = nil
			for i := range 12 {
				c.Members = append(c.Members, Member{Name: fmt.Sprintf("m%02d", i), AgentID: "shell", Args: []string{strings.Repeat("<", 4096), strings.Repeat("<", 4096)}, Start: Start{When: "manual"}})
			}
		}, "as JSON"},
	}
	for _, tc := range cases {
		c := validCrew("api", "API")
		tc.change(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) || !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want an ErrInvalid about %s", tc.name, err, tc.want)
		}
	}

	// Agents are checked against a catalog, apart from the structure.
	cat, err := catalog.Load(catalog.File{DisableDefaults: true, Agents: []catalog.Agent{
		{ID: "claude", Name: "Claude", Command: []string{"claude"}},
		{ID: "shell", Name: "Shell", Command: []string{"/bin/sh"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := validCrew("api", "API").CheckAgents(cat); err != nil {
		t.Fatalf("known agents rejected: %v", err)
	}
	c := validCrew("api", "API")
	c.Members[1].AgentID = "nope"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate needs no catalog, yet: %v", err)
	}
	if err := c.CheckAgents(cat); err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), `"tests"`) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown agent: %v", err)
	}
}

// A value a validation message quotes is cut to 80 characters, so that an
// error does not send back whatever a client sent, however long.
func TestValidationMessagesQuoteAtMost80Characters(t *testing.T) {
	huge := strings.Repeat("x", 900<<10)
	cut := `"` + strings.Repeat("x", 80) + `…"`
	for name, change := range map[string]func(*Crew){
		"where":        func(c *Crew) { c.Where = huge },
		"isolation":    func(c *Crew) { c.Isolation = huge },
		"member name":  func(c *Crew) { c.Members[0].Name = huge },
		"agentId":      func(c *Crew) { c.Members[0].AgentID = huge },
		"start.when":   func(c *Crew) { c.Members[0].Start.When = huge },
		"start.member": func(c *Crew) { c.Members[1].Start.Member = huge },
	} {
		c := validCrew("api", "API")
		change(&c)
		err := c.Validate()
		if err == nil || !errors.Is(err, ErrInvalid) || len(err.Error()) > 300 || !strings.Contains(err.Error(), cut) {
			t.Errorf("%s: %.400v", name, err)
		}
	}
	// CheckAgents, asked about an agent Validate refuses, quotes it alike.
	cat, err := catalog.Load(catalog.File{DisableDefaults: true, Agents: []catalog.Agent{{ID: "shell", Name: "Shell", Command: []string{"/bin/sh"}}}})
	if err != nil {
		t.Fatal(err)
	}
	c := validCrew("api", "API")
	c.Members[0].AgentID = huge
	if err := c.CheckAgents(cat); err == nil || len(err.Error()) > 300 || !strings.Contains(err.Error(), cut) {
		t.Errorf("CheckAgents: %.400v", err)
	}
	// Characters, not bytes; a short value is quoted whole.
	c = validCrew("api", "API")
	c.Where = strings.Repeat("é", 81)
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), `"`+strings.Repeat("é", 80)+`…"`) {
		t.Errorf("81 characters: %v", err)
	}
	c.Where = strings.Repeat("é", 80)
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), `"`+strings.Repeat("é", 80)+`"`) {
		t.Errorf("80 characters: %v", err)
	}
	// So does the store, about an id.
	st, _ := newStore(t)
	if err := st.Put(validCrew(strings.Repeat("Z", 1000), "API")); err == nil || len(err.Error()) > 300 || !strings.Contains(err.Error(), `"`+strings.Repeat("Z", 80)+`…"`) {
		t.Errorf("Put: %.400v", err)
	}
}

func TestValidateAcceptsCrewsAtTheLimits(t *testing.T) {
	c := validCrew("api", strings.Repeat("é", 60))
	c.Goal = strings.Repeat("ü", 2000) // runes, not bytes
	c.Cwd = "/" + strings.Repeat("d", 4095)
	c.ViewLinkTTLSeconds = 365 * 24 * 3600
	c.Members = nil
	for i := range 12 {
		c.Members = append(c.Members, Member{Name: fmt.Sprintf("m%d", i), AgentID: "shell", Prompt: strings.Repeat("ö", 4000), Start: Start{When: "manual"}})
	}
	c.Members[0].Name = "a" + strings.Repeat("b._-", 9) + "cde"                        // 40 characters
	c.Members[0].Args = slices.Repeat([]string{strings.Repeat("a", 256)}, 32)          // 32 entries, 8 KiB in all
	c.Members[1].Args = []string{strings.Repeat("a", 4096), strings.Repeat("a", 4096)} // the longest entries, 8 KiB in all
	c.Members[1].Start = Start{When: "after", Member: c.Members[0].Name}
	c.Members[2].Start = Start{When: "immediately"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	// As JSON, up to 512 KiB.
	if err := crewOfEncodedSize(t, "edge", 512<<10).Validate(); err != nil {
		t.Fatal(err)
	}
}

// A chain of after conditions is not a cycle, however long and in whatever
// order the members are listed.
func TestValidateAcceptsChainsOfStarts(t *testing.T) {
	c := validCrew("api", "API")
	c.Members = []Member{
		{Name: "d", AgentID: "shell", Start: Start{When: "after", Member: "c"}},
		{Name: "a", AgentID: "shell", Start: Start{When: "immediately"}},
		{Name: "c", AgentID: "shell", Start: Start{When: "after", Member: "b"}},
		{Name: "n", AgentID: "shell", Start: Start{When: "after", Member: "m"}},
		{Name: "b", AgentID: "shell", Start: Start{When: "after", Member: "a"}},
		{Name: "m", AgentID: "shell", Start: Start{When: "manual"}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	// Twelve members, each after the one before, listed last to first.
	c.Members = nil
	for i := 11; i >= 0; i-- {
		start := Start{When: "after", Member: fmt.Sprintf("m%02d", i-1)}
		if i == 0 {
			start = Start{When: "immediately"}
		}
		c.Members = append(c.Members, Member{Name: fmt.Sprintf("m%02d", i), AgentID: "shell", Start: start})
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExpandPrompt(t *testing.T) {
	const goal = "the v2 users API"
	cases := []struct{ prompt, want string }{
		{"Own the plan for $GOAL.", "Own the plan for the v2 users API."},
		{"${GOAL}: tests first", "the v2 users API: tests first"},
		{"$GOAL", "the v2 users API"},
		{"$GOAL and ${GOAL}", "the v2 users API and the v2 users API"},
		{"$GOALS stay, as do $GOAL_2, $GOAL9, ${GOALS} and $goal", "$GOALS stay, as do $GOAL_2, $GOAL9, ${GOALS} and $goal"},
		{"no goal here", "no goal here"},
	}
	for _, tc := range cases {
		if got := ExpandPrompt(tc.prompt, goal); got != tc.want {
			t.Errorf("ExpandPrompt(%q) = %q, want %q", tc.prompt, got, tc.want)
		}
	}
	// The goal goes in as it is: what looks like a reference in it is not expanded.
	if got := ExpandPrompt("Do $GOAL", "$1 and ${GOAL}"); got != "Do $1 and ${GOAL}" {
		t.Fatalf("goal with dollars: %q", got)
	}
}

// A prompt is typed with the goal in place of $GOAL and a carriage return,
// which must fit what a session takes at once (proto.MaxInput bytes): a
// valid prompt and goal can reach megabytes with 800 references.
func TestValidateRejectsAPromptTooLongToType(t *testing.T) {
	c := validCrew("api", "API")
	c.Goal = strings.Repeat("é", 2000) // 4000 bytes
	c.Members[1].Prompt = strings.Repeat("$GOAL ", 8)
	if err := c.Validate(); err != nil {
		t.Fatalf("8 references: %v", err)
	}
	c.Members[1].Prompt = strings.Repeat("${GOAL} ", 9)
	if err := c.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `"tests"`) || !strings.Contains(err.Error(), "goal") {
		t.Fatalf("9 references: %v", err)
	}
	// Counted as ExpandPrompt writes it.
	for _, prompt := range []string{"", "no goal", "$GOAL", "${GOAL}$GOAL", "$GOALS $GOAL_2 ${GOALS}", strings.Repeat("$GOAL ", 800)} {
		if got, want := typedPromptLen(prompt, c.Goal), len(ExpandPrompt(prompt, c.Goal))+1; got != want {
			t.Errorf("typedPromptLen(%.20q) = %d, want %d", prompt, got, want)
		}
	}
}

// The field names are what the web client and docs/protocol.md use.
func TestCrewJSON(t *testing.T) {
	b, err := json.Marshal(validCrew("api", "API"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"api","name":"API","goal":"ship /v1/users","cwd":"/srv/api","where":"server","isolation":"worktree",` +
		`"openAfterLaunch":true,"viewLinkTtlSeconds":28800,"members":[` +
		`{"name":"lead","agentId":"claude","prompt":"Own the plan for $GOAL.","start":{"when":"immediately"}},` +
		`{"name":"tests","agentId":"shell","prompt":"Write the tests.","args":["--model","x"],"start":{"when":"after","member":"lead"}}],` +
		`"createdAt":"2026-09-29T10:00:00Z","updatedAt":"2026-09-29T11:00:00Z"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s, st := newStore(t)
	if got := s.List(); len(got) != 0 {
		t.Fatalf("a store without crews.json lists %v", got)
	}
	for _, c := range []Crew{validCrew("zeta", "Zeta"), validCrew("alpha", "alpha")} {
		if err := s.Put(c); err != nil {
			t.Fatal(err)
		}
	}
	// By name, ignoring case: "alpha" before "Zeta".
	want := []Crew{validCrew("alpha", "alpha"), validCrew("zeta", "Zeta")}
	if got := s.List(); !reflect.DeepEqual(got, want) {
		t.Fatalf("list:\n got  %+v\n want %+v", got, want)
	}
	if got, ok := s.Get("zeta"); !ok || !reflect.DeepEqual(got, want[1]) {
		t.Fatalf("get: %v %+v", ok, got)
	}
	again, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.List(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reloaded:\n got  %+v\n want %+v", got, want)
	}
	var doc map[string][]map[string]any
	b, err := os.ReadFile(filepath.Join(st.Dir(), "crews.json"))
	if err != nil || json.Unmarshal(b, &doc) != nil || len(doc) != 1 || len(doc["crews"]) != 2 || doc["crews"][0]["id"] != "alpha" {
		t.Fatalf("crews.json: %v %s", err, b)
	}

	if ok, err := s.Delete("zeta"); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if ok, err := s.Delete("zeta"); ok || err != nil {
		t.Fatalf("second delete: %v %v", ok, err)
	}
	if _, ok := s.Get("zeta"); ok {
		t.Fatal("deleted crew still there")
	}
	again, err = NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.List(); !reflect.DeepEqual(got, want[:1]) {
		t.Fatalf("reloaded after delete: %+v", got)
	}
}

func TestStoreReturnsCopies(t *testing.T) {
	s, _ := newStore(t)
	c := validCrew("api", "API")
	if err := s.Put(c); err != nil {
		t.Fatal(err)
	}
	c.Members[0].Prompt = "changed after Put"
	c.Members[1].Args[0] = "changed after Put"
	got, ok := s.Get("api")
	list := s.List()
	if !ok || len(got.Members) != 2 || len(list) != 1 || len(list[0].Members) != 2 {
		t.Fatalf("stored: %v %+v %+v", ok, got, list)
	}
	got.Members[0].Prompt = "changed after Get"
	got.Members[1].Args[0] = "changed after Get"
	list[0].Members[1].Args[1] = "changed after List"
	list[0].Members = append(list[0].Members[:1], Member{Name: "extra"})
	if got, _ := s.Get("api"); !reflect.DeepEqual(got, validCrew("api", "API")) {
		t.Fatalf("the stored crew changed: %+v", got)
	}
}

func TestCreateDerivesUniqueIDs(t *testing.T) {
	s, st := newStore(t)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	clock(s, at)
	cases := []struct{ name, want, stored string }{ // stored: the name as kept, when it differs
		{"API sweep", "api-sweep", ""},
		{"API sweep", "api-sweep-2", ""},
		{"  api   SWEEP!! ", "api-sweep-3", "api   SWEEP!!"},
		{"Claude (opus) -- v2", "claude-opus-v2", ""},
		{"!!!", "crew", ""},
		{"日本語", "crew-2", ""},
		{strings.Repeat("x", 50), strings.Repeat("x", 40), ""},
		{strings.Repeat("x", 50), strings.Repeat("x", 40) + "-2", ""},
		// A 60-character name: its slug is cut at 40 characters, then loses
		// the dash left at the end.
		{strings.Repeat("abc ", 15), strings.Repeat("abc-", 9) + "abc", strings.Repeat("abc ", 14) + "abc"},
	}
	for _, tc := range cases {
		in := validCrew("ignored", tc.name)
		got, err := s.Create(in)
		if err != nil {
			t.Fatalf("%q: %v", tc.name, err)
		}
		want := validCrew(tc.want, cmp.Or(tc.stored, tc.name))
		want.CreatedAt, want.UpdatedAt = at, at
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%q:\n got  %+v\n want %+v", tc.name, got, want)
		}
	}
	again, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(again.List()); n != len(cases) {
		t.Fatalf("reloaded %d crews, want %d", n, len(cases))
	}
}

func TestCreateConcurrentlyGivesDistinctIDs(t *testing.T) {
	s, _ := newStore(t)
	const writers = 8
	ids := make(chan string, writers)
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			c, err := s.Create(validCrew("", "Same name"))
			if err != nil {
				t.Error(err)
				return
			}
			ids <- c.ID
		})
	}
	wg.Wait()
	close(ids)
	got := slices.Sorted(func(yield func(string) bool) {
		for id := range ids {
			if !yield(id) {
				return
			}
		}
	})
	want := []string{"same-name", "same-name-2", "same-name-3", "same-name-4", "same-name-5", "same-name-6", "same-name-7", "same-name-8"}
	if !slices.Equal(got, want) || len(s.List()) != writers {
		t.Fatalf("ids %v, %d crews", got, len(s.List()))
	}
}

func TestDuplicateCopiesUnderACopyID(t *testing.T) {
	s, _ := newStore(t)
	orig := validCrew("api-sweep", "API sweep")
	if err := s.Put(orig); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	clock(s, at)
	for _, tc := range []struct{ from, want, name string }{
		{"api-sweep", "api-sweep-copy", "API sweep copy"},
		{"api-sweep", "api-sweep-copy-2", "API sweep copy"},
		{"api-sweep-copy", "api-sweep-copy-copy", "API sweep copy copy"},
	} {
		got, err := s.Duplicate(tc.from)
		if err != nil {
			t.Fatalf("duplicate %s: %v", tc.from, err)
		}
		want := validCrew(tc.want, tc.name)
		want.CreatedAt, want.UpdatedAt = at, at
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("duplicate %s:\n got  %+v\n want %+v", tc.from, got, want)
		}
		if stored, _ := s.Get(tc.want); !reflect.DeepEqual(stored, want) {
			t.Fatalf("stored copy %+v", stored)
		}
	}
	if got, _ := s.Get("api-sweep"); !reflect.DeepEqual(got, orig) {
		t.Fatalf("the original changed: %+v", got)
	}
	if _, err := s.Duplicate("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown crew: %v", err)
	}

	// Names and IDs stay within their limits.
	long := validCrew(strings.Repeat("a", 64), strings.Repeat("é", 60))
	if err := s.Put(long); err != nil {
		t.Fatal(err)
	}
	got, err := s.Duplicate(long.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != strings.Repeat("a", 59)+"-copy" || got.Name != strings.Repeat("é", 55)+" copy" {
		t.Fatalf("long copy: %q %q", got.ID, got.Name)
	}
	if got, err = s.Duplicate(long.ID); err != nil || got.ID != strings.Repeat("a", 57)+"-copy-2" {
		t.Fatalf("second long copy: %q %v", got.ID, err)
	}
}

func TestUpdateKeepsTheIDAndCreationTime(t *testing.T) {
	s, st := newStore(t)
	created := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	clock(s, created)
	c, err := s.Create(validCrew("", "API sweep"))
	if err != nil {
		t.Fatal(err)
	}
	updated := created.Add(time.Hour)
	clock(s, updated)
	next := validCrew("other", "Renamed")
	next.Members[0].Prompt = "New plan for $GOAL."
	next.CreatedAt, next.UpdatedAt = time.Time{}, time.Time{}
	got, err := s.Update(c.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	want := validCrew("api-sweep", "Renamed")
	want.Members[0].Prompt = "New plan for $GOAL."
	want.CreatedAt, want.UpdatedAt = created, updated
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("update:\n got  %+v\n want %+v", got, want)
	}
	if _, err := s.Update("missing", next); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown crew: %v", err)
	}
	again, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	if list := again.List(); !reflect.DeepEqual(list, []Crew{want}) {
		t.Fatalf("stored: %+v", list)
	}
}

func TestStoreHoldsAtMost50Crews(t *testing.T) {
	s, st := newStore(t)
	for i := range 50 {
		if err := s.Put(validCrew(fmt.Sprintf("c%02d", i), fmt.Sprintf("Crew %02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put(validCrew("c50", "One too many")); !errors.Is(err, ErrTooManyCrews) {
		t.Fatalf("put: %v", err)
	}
	if _, err := s.Create(validCrew("", "One too many")); !errors.Is(err, ErrTooManyCrews) {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Duplicate("c00"); !errors.Is(err, ErrTooManyCrews) {
		t.Fatalf("duplicate: %v", err)
	}
	// Replacing a crew is not adding one.
	if err := s.Put(validCrew("c00", "Crew 00 again")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("c01", validCrew("", "Crew 01 again")); err != nil {
		t.Fatal(err)
	}
	again, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(again.List()); n != 50 {
		t.Fatalf("%d crews stored", n)
	}
}

func TestStoreRefusesInvalidCrews(t *testing.T) {
	s, st := newStore(t)
	for _, id := range []string{"", "Bad", "-x", "x y", "x/y", strings.Repeat("a", 65)} {
		if err := s.Put(validCrew(id, "x")); !errors.Is(err, ErrInvalid) {
			t.Errorf("id %q: %v", id, err)
		}
	}
	bad := validCrew("ok", "x")
	bad.Members[0].Name = "Lead!"
	if err := s.Put(bad); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("put: %v", err)
	}
	if _, err := s.Create(bad); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("create: %v", err)
	}
	if err := s.Put(validCrew("ok", "x")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("ok", bad); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("update: %v", err)
	}
	if got, _ := s.Get("ok"); !reflect.DeepEqual(got, validCrew("ok", "x")) || len(s.List()) != 1 {
		t.Fatalf("an invalid crew changed the store: %+v", s.List())
	}
	again, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	if list := again.List(); len(list) != 1 || list[0].ID != "ok" {
		t.Fatalf("stored: %+v", list)
	}
}

// A crews.json that cannot be used must stop startup: a store that started
// empty would overwrite the file on its next save.
func TestNewStoreRefusesAMalformedFile(t *testing.T) {
	crewJSON := func(id string) string {
		return `{"id": "` + id + `", "name": "x", "where": "server", "isolation": "none", "members": []}`
	}
	var many []string
	for i := range 51 {
		many = append(many, crewJSON(fmt.Sprintf("c%d", i)))
	}
	cases := []struct{ name, file, want string }{
		{"not JSON", `{oops`, "parse crews.json"},
		{"empty file", ``, "empty document"},
		{"unknown field", `{"crews": [], "bogus": true}`, `"bogus"`},
		{"invalid crew", `{"crews": [` + crewJSON("ok") + `, {"id": "bad", "name": "x", "where": "server", "isolation": "none", "members": [{"name": "Lead!", "agentId": "a", "prompt": "", "start": {"when": "manual"}}]}]}`, "crews[1]"},
		{"invalid id", `{"crews": [` + crewJSON("Bad Id") + `]}`, "crews[0]"},
		{"invalid agent id", `{"crews": [{"id": "x", "name": "x", "where": "server", "isolation": "none", "members": [{"name": "lead", "agentId": "Bad Id", "prompt": "", "start": {"when": "manual"}}]}]}`, `agentId "Bad Id"`},
		{"control character in a name", `{"crews": [{"id": "x", "name": "a\u0007b", "where": "server", "isolation": "none", "members": []}]}`, "control"},
		{"id used twice", `{"crews": [` + crewJSON("x") + `, ` + crewJSON("x") + `]}`, "crews[1]"},
		{"51 crews", `{"crews": [` + strings.Join(many, ", ") + `]}`, "at most 50"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(st.Dir(), "crews.json")
			if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := NewStore(st)
			if err == nil || s != nil {
				t.Fatalf("NewStore accepted the file: %v", err)
			}
			if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should name %s and contain %q", err, path, tc.want)
			}
			if b, _ := os.ReadFile(path); string(b) != tc.file {
				t.Fatalf("NewStore changed the file: %q", b)
			}
		})
	}
}

// A crew that went into crews.json without members, by hand, is listed with
// members [] as every saved crew is, never null.
func TestNewStoreListsNoMembersAsEmpty(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := `{"crews": [{"id": "draft", "name": "Draft", "where": "server", "isolation": "none"}]}`
	if err := os.WriteFile(filepath.Join(st.Dir(), "crews.json"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := s.Get("draft")
	if !ok {
		t.Fatal("draft not loaded")
	}
	if b, _ := json.Marshal(c); !strings.Contains(string(b), `"members":[]`) {
		t.Fatalf("listed as %s", b)
	}
}

// Names are kept without surrounding space, however they come in.
func TestStoreTrimsTheName(t *testing.T) {
	s, st := newStore(t)
	c, err := s.Create(validCrew("", "  API sweep \n"))
	if err != nil || c.Name != "API sweep" || c.ID != "api-sweep" {
		t.Fatalf("create: %q %q %v", c.Name, c.ID, err)
	}
	if u, err := s.Update(c.ID, validCrew("", "\tRenamed  ")); err != nil || u.Name != "Renamed" {
		t.Fatalf("update: %q %v", u.Name, err)
	}
	if err := s.Put(validCrew("put", " Put ")); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("put"); got.Name != "Put" {
		t.Fatalf("put: %q", got.Name)
	}
	// Trimmed, then held to the limit: 60 characters between spaces fit.
	if c, err := s.Create(validCrew("", " "+strings.Repeat("é", 60)+" ")); err != nil || c.Name != strings.Repeat("é", 60) {
		t.Fatalf("60 characters between spaces: %q %v", c.Name, err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "crews.json"), []byte(`{"crews": [{"id": "hand", "name": "  Hand  ", "where": "server", "isolation": "none", "members": []}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Get("hand"); got.Name != "Hand" {
		t.Fatalf("loaded: %q", got.Name)
	}
}

// The store checks a crew as it saves it, ID and times included, so a crew at
// the encoded limit is not copied into one past it: that copy would stop the
// next start.
func TestDuplicateRefusesACopyOverTheEncodedLimit(t *testing.T) {
	s, st := newStore(t)
	edge := crewOfEncodedSize(t, "edge", 512<<10-9) // a copy is 10 bytes longer: " copy" and "-copy"
	if err := s.Put(edge); err != nil {
		t.Fatal(err)
	}
	clock(s, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)) // times as long as edge's
	if _, err := s.Duplicate("edge"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "as JSON") {
		t.Fatalf("duplicate: %v", err)
	}
	if n := len(s.List()); n != 1 {
		t.Fatalf("%d crews", n)
	}
	if _, err := NewStore(st); err != nil {
		t.Fatalf("the store no longer loads: %v", err)
	}
}

func TestFailedSaveChangesNothing(t *testing.T) {
	s, st := newStore(t)
	if err := s.Put(validCrew("kept", "Kept")); err != nil {
		t.Fatal(err)
	}
	// The data directory disappears, so no save can succeed.
	dir := st.Dir()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(validCrew("lost", "Lost")); err == nil {
		t.Error("put succeeded")
	}
	if _, err := s.Create(validCrew("", "Lost")); err == nil {
		t.Error("create succeeded")
	}
	if _, err := s.Update("kept", validCrew("", "Changed")); err == nil {
		t.Error("update succeeded")
	}
	if _, err := s.Duplicate("kept"); err == nil {
		t.Error("duplicate succeeded")
	}
	if ok, err := s.Delete("kept"); ok || err == nil {
		t.Errorf("delete: %v %v", ok, err)
	}
	if list := s.List(); !reflect.DeepEqual(list, []Crew{validCrew("kept", "Kept")}) {
		t.Fatalf("a failed save changed the crews: %+v", list)
	}
	// Once the directory is back, the next save writes what the store still
	// holds plus the new crew, and nothing from the failed attempts.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(validCrew("back", "Back")); err != nil {
		t.Fatal(err)
	}
	again, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	if list := again.List(); !reflect.DeepEqual(list, []Crew{validCrew("back", "Back"), validCrew("kept", "Kept")}) {
		t.Fatalf("stored: %+v", list)
	}
}
