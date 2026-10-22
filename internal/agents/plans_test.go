package agents

import (
	"os/user"
	"path/filepath"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// Claude Code's plans directory follows the environment the agent runs
// with: CLAUDE_CONFIG_DIR first (a relative one from the working
// directory), else ~/.claude, HOME or USERPROFILE naming the home; an
// adapter that keeps no plans Conductor knows of has none.
func TestPlansDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	cwd := filepath.Join(t.TempDir(), "work")
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"home", map[string]string{"HOME": home}, filepath.Join(home, ".claude", "plans")},
		{"userprofile", map[string]string{"USERPROFILE": home}, filepath.Join(home, ".claude", "plans")},
		{"config dir", map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, "cfg")}, filepath.Join(home, "cfg", "plans")},
		{"relative config dir", map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": "cfg"}, filepath.Join(cwd, "cfg", "plans")},
	}
	for _, c := range cases {
		if got := PlansDir("claude", envOf(c.env), cwd); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := PlansDir("codex", envOf(map[string]string{"HOME": home}), cwd); got != "" {
		t.Errorf("codex keeps no plans directory Conductor knows of, got %q", got)
	}
	if got := PlansDir("nothing", envOf(nil), cwd); got != "" {
		t.Errorf("an unknown adapter: %q", got)
	}
	// With no home in the agent's environment, the process's own; with none
	// there either (a service started without HOME), the account database's.
	if got := PlansDir("claude", envOf(nil), cwd); got == "" || filepath.Base(got) != "plans" {
		t.Errorf("the process's home: %q", got)
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skipf("no account database here: %v", err)
	}
	if got, want := PlansDir("claude", envOf(nil), cwd), filepath.Join(u.HomeDir, ".claude", "plans"); got != want {
		t.Errorf("the account's home: %q, want %q", got, want)
	}
}
