package crew

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/store"
)

// taskMember is a member of a managed crew: started by its tasks.
func taskMember(name string) Member {
	return Member{Name: name, AgentID: "claude", Prompt: "Do your part.", Start: Start{When: StartTask}}
}

// managedCrew is a crew with tasks: a builder and a reviewer, the review
// needing the build, a check on the build.
func managedCrew() Crew {
	c := testCrew(taskMember("builder"), taskMember("reviewer"))
	c.Isolation = IsolationWorktree
	c.Tasks = []Task{
		{ID: "build", Member: "builder", Kind: TaskWork, Instructions: "Build it.", Checks: []Check{{ID: "unit", Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 600}}},
		{ID: "review", Member: "reviewer", Kind: TaskReview, Instructions: "Review it.", Needs: []string{"build"}},
	}
	c.Setup = []Check{{ID: "deps", Argv: []string{"npm", "ci"}}}
	return c
}

func TestValidateAcceptsACrewWithTasks(t *testing.T) {
	c := managedCrew()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !c.Managed() || testCrew(immediate("lead", "x")).Managed() {
		t.Fatal("Managed is wrong")
	}
	// Launching is refused until a run can dispatch tasks (#334, rows 5 and 6).
	err := c.Launchable()
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "cannot be launched yet") {
		t.Fatalf("launchable: %v", err)
	}
	if err := testCrew(immediate("lead", "x")).Launchable(); err != nil {
		t.Fatalf("a legacy crew: %v", err)
	}
}

func TestValidateRejectsBadTasks(t *testing.T) {
	long := strings.Repeat("x", maxInstructions+1)
	cases := []struct {
		name  string
		edit  func(c *Crew)
		words string
	}{
		{"a member not started by its tasks", func(c *Crew) { c.Members[0].Start = Start{When: StartImmediately} }, `start.when must be "task"`},
		{"a task-started member in a legacy crew", func(c *Crew) { c.Tasks, c.Setup = nil, nil }, `only for a crew with tasks`},
		{"setup without tasks", func(c *Crew) {
			c.Tasks = nil
			c.Members[0].Start, c.Members[1].Start = Start{When: StartImmediately}, Start{When: StartImmediately}
		}, "setup is only set with tasks"},
		{"no worktree isolation", func(c *Crew) { c.Isolation = IsolationNone }, `needs isolation "worktree"`},
		{"too many tasks", func(c *Crew) {
			for i := range maxTasks {
				c.Tasks = append(c.Tasks, Task{ID: "t" + strings.Repeat("a", i%30) + string(rune('a'+i%26)), Member: "builder", Kind: TaskWork})
			}
		}, "too many tasks"},
		{"a bad task id", func(c *Crew) { c.Tasks[0].ID = "Build.1" }, "id must match"},
		{"a task id used twice", func(c *Crew) { c.Tasks[1].ID = "build" }, "used twice"},
		{"a bad kind", func(c *Crew) { c.Tasks[0].Kind = "chore" }, `kind "chore" must be`},
		{"an unknown member", func(c *Crew) { c.Tasks[0].Member = "ghost" }, "not a member of this crew"},
		{"instructions too long", func(c *Crew) { c.Tasks[0].Instructions = long }, "at most 8000 characters"},
		{"NUL in instructions", func(c *Crew) { c.Tasks[0].Instructions = "a\x00b" }, "contain NUL"},
		{"too many checks", func(c *Crew) {
			for i := range maxChecks {
				c.Tasks[0].Checks = append(c.Tasks[0].Checks, Check{ID: "c" + string(rune('a'+i)), Argv: []string{"true"}})
			}
		}, "too many checks"},
		{"a review with checks", func(c *Crew) { c.Tasks[1].Checks = []Check{{ID: "x", Argv: []string{"true"}}} }, "a review has no checks"},
		{"a check with no program", func(c *Crew) { c.Tasks[0].Checks[0].Argv = nil }, "argv must name a program"},
		{"a check id used twice", func(c *Crew) {
			c.Tasks[0].Checks = append(c.Tasks[0].Checks, Check{ID: "unit", Argv: []string{"true"}})
		}, "check id \"unit\" is used twice"},
		{"a check timeout past the bound", func(c *Crew) { c.Tasks[0].Checks[0].TimeoutSeconds = maxCheckTimeout + 1 }, "timeoutSeconds must be between 1 and 3600"},
		{"a check argv with NUL", func(c *Crew) { c.Tasks[0].Checks[0].Argv = []string{"go", "te\x00st"} }, "argv[1] contains NUL"},
		{"a setup entry with a bad id", func(c *Crew) { c.Setup[0].ID = "Deps" }, "setup[0]: id"},
		{"too many setup entries", func(c *Crew) {
			for i := range maxChecks {
				c.Setup = append(c.Setup, Check{ID: "s" + string(rune('a'+i)), Argv: []string{"true"}})
			}
		}, "too many setup commands"},
		{"a need of itself", func(c *Crew) { c.Tasks[0].Needs = []string{"build"} }, "cannot need itself"},
		{"an unknown need", func(c *Crew) { c.Tasks[0].Needs = []string{"ghost"} }, `needs "ghost", which is not a task`},
		{"a review needing two tasks", func(c *Crew) {
			c.Tasks = append(c.Tasks, Task{ID: "docs", Member: "builder", Kind: TaskWork, Instructions: "Write the docs."})
			c.Tasks[1].Needs = []string{"build", "docs"}
		}, "a review needs exactly one work task"},
		{"a review needing a review", func(c *Crew) {
			c.Tasks = append(c.Tasks, Task{ID: "rereview", Member: "reviewer", Kind: TaskReview, Instructions: "Again.", Needs: []string{"review"}})
		}, "a review needs exactly one work task"},
		{"a cycle", func(c *Crew) {
			c.Tasks = append(c.Tasks, Task{ID: "docs", Member: "builder", Kind: TaskWork, Instructions: "Docs.", Needs: []string{"build"}})
			c.Tasks[0].Needs = []string{"docs"}
		}, "go round in a cycle"},
		{"a member with no task", func(c *Crew) { c.Members = append(c.Members, taskMember("idle")) }, `member "idle": has no task`},
	}
	for _, tc := range cases {
		c := managedCrew()
		tc.edit(&c)
		err := c.Validate()
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.words) {
			t.Errorf("%s: %v (want %q)", tc.name, err, tc.words)
		}
	}
}

// A crew with tasks goes through the store whole; a crew without encodes
// as it did, with no tasks or setup key.
func TestACrewWithTasksRoundTrips(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, problems, err := NewStore(st)
	if err != nil || len(problems) != 0 {
		t.Fatal(err, problems)
	}
	c := managedCrew()
	created, err := s.Create(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tasks) != 2 || got.Tasks[0].Checks[0].TimeoutSeconds != 600 || got.Tasks[1].Needs[0] != "build" || len(got.Setup) != 1 || got.Setup[0].Argv[1] != "ci" {
		t.Fatalf("%+v", got)
	}
	legacy, err := json.Marshal(testCrew(immediate("lead", "x")))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "tasks") || strings.Contains(string(legacy), "setup") {
		t.Fatalf("a legacy crew's JSON names tasks: %s", legacy)
	}
	if !strings.Contains(string(legacy), `"when":"immediately"`) {
		t.Fatalf("legacy JSON: %s", legacy)
	}
}
