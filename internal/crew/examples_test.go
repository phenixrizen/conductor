package crew

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Every example is a crew the store would accept: the four ids in order,
// claude and codex members with role prompts that use the goal, the server's
// working directory, a worktree per member.
func TestExamplesAreValidCrews(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	list := Examples("/srv/work", now)
	var ids []string
	for _, c := range list {
		ids = append(ids, c.ID)
		if err := c.validateWithID(); err != nil {
			t.Errorf("%s: %v", c.ID, err)
		}
		if c.Cwd != "/srv/work" || c.Isolation != IsolationWorktree || c.Where != WhereServer || !c.OpenAfterLaunch || !c.CreatedAt.Equal(now) || !c.UpdatedAt.Equal(now) {
			t.Errorf("%s: %+v", c.ID, c)
		}
		if len(c.Members) < 2 || !strings.HasPrefix(c.Name, "Example: ") {
			t.Errorf("%s: %d members, name %q", c.ID, len(c.Members), c.Name)
		}
		for _, m := range c.Members {
			if m.AgentID != "claude" && m.AgentID != "codex" {
				t.Errorf("%s/%s: agent %q", c.ID, m.Name, m.AgentID)
			}
			if !strings.Contains(m.Prompt, "$GOAL") {
				t.Errorf("%s/%s: the prompt does not use $GOAL", c.ID, m.Name)
			}
		}
	}
	if !slices.Equal(ids, ExampleIDs) {
		t.Fatalf("ids: %v", ids)
	}
}

// The todo app: a lead that plans, two builders after the lead, a tester
// after cli (a start condition names one member), told to wait for core's
// branch and merge both.
func TestTodoAppStartsLeadThenBuildersThenTester(t *testing.T) {
	todo := Examples("/w", time.Now())[0]
	want := map[string]Start{
		"lead":   {When: StartImmediately},
		"core":   {When: StartAfter, Member: "lead"},
		"cli":    {When: StartAfter, Member: "lead"},
		"tester": {When: StartAfter, Member: "cli"},
	}
	if len(todo.Members) != len(want) {
		t.Fatalf("members: %+v", todo.Members)
	}
	for _, m := range todo.Members {
		if m.Start != want[m.Name] {
			t.Errorf("%s: %+v", m.Name, m.Start)
		}
	}
	tester := todo.Members[3].Prompt
	for _, part := range []string{"crew/<run>/core", "crew/<run>/cli", "wait until"} {
		if !strings.Contains(tester, part) {
			t.Errorf("the tester's prompt does not say %q: %s", part, tester)
		}
	}
}

// Seed adds what is missing and leaves what exists alone: a second seed adds
// nothing and an edit survives it; a deleted example comes back.
func TestSeedAddsOnceAndLeavesEditsAlone(t *testing.T) {
	s, _ := newStore(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	added, skipped, err := s.Seed(Examples("/w", now))
	if err != nil || !slices.Equal(added, ExampleIDs) || len(skipped) != 0 {
		t.Fatalf("first seed: %v %v %v", added, skipped, err)
	}
	c, err := s.Get("example-todo-app")
	if err != nil {
		t.Fatal(err)
	}
	c.Goal = "edited"
	if _, err := s.Update(c.ID, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete("example-docs-writer"); err != nil {
		t.Fatal(err)
	}
	added, skipped, err = s.Seed(Examples("/w", now))
	if err != nil || !slices.Equal(added, []string{"example-docs-writer"}) || !slices.Equal(skipped, []string{"example-todo-app", "example-test-fixer", "example-dependency-upgrade"}) {
		t.Fatalf("second seed: %v %v %v", added, skipped, err)
	}
	if got, err := s.Get("example-todo-app"); err != nil || got.Goal != "edited" {
		t.Fatalf("the edit was lost: %q %v", got.Goal, err)
	}
}

// A file with an example's id that the store cannot read is skipped and left
// as it is, never overwritten by the seed.
func TestSeedLeavesAnUnreadableFileAlone(t *testing.T) {
	s, st := newStore(t)
	path := filepath.Join(st.Dir(), "crews", "example-test-fixer.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	added, skipped, err := s.Seed(Examples("/w", time.Now()))
	if err != nil || !slices.Equal(skipped, []string{"example-test-fixer"}) || len(added) != 3 {
		t.Fatalf("%v %v %v", added, skipped, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Fatalf("the unreadable file was overwritten: %s", b)
	}
}

// A crew the store refuses stops the seed, naming it; the ones before it stay.
func TestSeedStopsAtAnInvalidCrew(t *testing.T) {
	s, _ := newStore(t)
	list := Examples("/w", time.Now())
	list[1].Cwd = strings.Repeat("x", maxCwd+1)
	added, _, err := s.Seed(list)
	if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "example-test-fixer") || !slices.Equal(added, []string{"example-todo-app"}) {
		t.Fatalf("%v %v", added, err)
	}
	if _, err := s.Get("example-docs-writer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("seeding went on past the error: %v", err)
	}
}
