package agents

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The skill tells an agent when and how to report to Conductor: its
// frontmatter names it and says when it applies, its body gives the commands.
func TestSkillText(t *testing.T) {
	rest, ok := strings.CutPrefix(Skill, "---\n")
	front, body, closed := strings.Cut(rest, "\n---\n")
	if !ok || !closed {
		t.Fatalf("no frontmatter:\n%s", Skill)
	}
	if want := "name: conductor\ndescription: Report progress, artifacts, blockers and handoffs to Conductor while working in a Conductor session"; front != want {
		t.Fatalf("frontmatter:\n%s\nwant\n%s", front, want)
	}
	for _, want := range []string{
		`conductor notify --event progress --message "4/7 handlers"`,
		`conductor notify --event artifact --url <url>`,
		`conductor notify --event handoff --to <member> --message "…"`,
		`conductor notify --state needs_input --message "…"`,
		"ever call notify outside a Conductor session; the command exits silently there",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the skill does not say %q", want)
		}
	}
	if strings.Contains(Skill, binPlaceholder) || !strings.HasSuffix(Skill, "\n") {
		t.Fatalf("skill:\n%s", Skill)
	}
}

// WriteAssets puts the skill in the hooks dir next to the adapters' assets,
// held to the same modes.
func TestWriteAssetsWritesTheSkill(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAssets(dir, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "skills", "conductor", "SKILL.md")
	b, err := os.ReadFile(p)
	if err != nil || string(b) != Skill {
		t.Fatalf("%s: %v\n%s", p, err, b)
	}
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%s: %v %v", p, fi.Mode(), err)
	}
	for _, d := range []string{filepath.Join(dir, "skills"), filepath.Join(dir, "skills", "conductor")} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("directory %s: %v %v", d, fi.Mode(), err)
		}
	}
}

// skillFor is where Install puts the skill for each agent that reads skills:
// Claude Code and Codex have their own directory, pi and Goose read the
// shared ~/.agents/skills.
var skillFor = map[string]string{
	"claude": ".claude/skills/conductor/SKILL.md",
	"codex":  ".codex/skills/conductor/SKILL.md",
	"pi":     ".agents/skills/conductor/SKILL.md",
	"goose":  ".agents/skills/conductor/SKILL.md",
}

// Install copies the skill for the agents that read skills, and for no other.
// It is part of the install: without it Status reads not installed, and
// Install puts it back, from the hooks dir when a server wrote it there.
func TestInstallCopiesTheSkill(t *testing.T) {
	useBin(t, "/opt/conductor")
	hooks := t.TempDir()
	if err := WriteAssets(hooks, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	for _, a := range All() {
		if a.Install == nil || a.ID == "dsh" {
			continue
		}
		t.Run(a.ID, func(t *testing.T) {
			home := t.TempDir()
			touched, err := a.Install(home, hooks)
			if err != nil {
				t.Fatal(err)
			}
			var skills []string
			for _, p := range touched {
				if filepath.Base(p) == "SKILL.md" {
					skills = append(skills, p)
				}
			}
			rel, reads := skillFor[a.ID]
			if !reads {
				if len(skills) != 0 {
					t.Fatalf("%s does not read skills, and got %q", a.ID, skills)
				}
				return
			}
			p := filepath.Join(home, filepath.FromSlash(rel))
			if !slices.Equal(skills, []string{p}) {
				t.Fatalf("skills written %q, want %q", skills, p)
			}
			if b, err := os.ReadFile(p); err != nil || string(b) != Skill {
				t.Fatalf("%s: %v\n%s", p, err, b)
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if ok, _ := a.Status(home); ok {
				t.Fatal("status reads installed without the skill")
			}
			if again, err := a.Install(home, hooks); err != nil || !slices.Equal(again, []string{p}) {
				t.Fatalf("install after the skill was removed: %q %v", again, err)
			}
			if ok, _ := a.Status(home); !ok {
				t.Fatal("status after the skill came back")
			}
		})
	}
	// pi and Goose share the skill: the second install finds it in place.
	home := t.TempDir()
	pi, _ := Get("pi")
	goose, _ := Get("goose")
	if _, err := pi.Install(home, hooks); err != nil {
		t.Fatal(err)
	}
	touched, err := goose.Install(home, hooks)
	if err != nil || !slices.Equal(touched, under(home, installed["goose"].files[0])) {
		t.Fatalf("goose after pi: %q %v", touched, err)
	}
}
