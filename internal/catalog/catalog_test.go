package catalog

import (
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
	}
	for i, a := range cases {
		if _, err := Load(File{Agents: []Agent{a}}); err == nil {
			t.Fatalf("case %d: expected error", i)
		} else if !strings.Contains(err.Error(), "agents[0]") {
			t.Fatalf("case %d: unexpected error %v", i, err)
		}
	}
}
