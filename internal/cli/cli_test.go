package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/agents"
)

// runHooksWith runs `conductor hooks` with args. HOME is a temporary
// directory, so that no test can write to the real one.
func runHooksWith(t *testing.T, args ...string) (code int, stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var out, errOut bytes.Buffer
	code, err = runHooks(t.Context(), args, &out, &errOut)
	return code, out.String(), errOut.String(), err
}

// conductor hooks install puts an adapter's hooks into the home it is given,
// copied from the hooks dir of the data dir it is given, and prints the files
// it wrote; run again, it changes nothing and says so.
func TestHooksInstallCLI(t *testing.T) {
	clearConductorEnv(t)
	home, data := t.TempDir(), t.TempDir()
	// The assets a server with this data dir wrote, naming its binary.
	if err := agents.WriteAssets(filepath.Join(data, "hooks"), "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr, err := runHooksWith(t, "install", "copilot", "--home", home, "--data-dir", data)
	file := filepath.Join(home, ".copilot", "hooks", "conductor.json")
	if code != 0 || err != nil || !strings.Contains(stdout, file) {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	if b, err := os.ReadFile(file); err != nil || !strings.Contains(string(b), `"/opt/conductor notify --copilot-hook"`) {
		t.Fatalf("%s: %v\n%s", file, err, b)
	}
	code, stdout, _, err = runHooksWith(t, "install", "copilot", "--home", home, "--data-dir", data)
	if code != 0 || err != nil || strings.Contains(stdout, file) || !strings.Contains(stdout, "nothing to change") {
		t.Fatalf("second install: exit %d %v\n%s", code, err, stdout)
	}
}

// Without --data-dir the data dir is the one conductor serve would use:
// CONDUCTOR_DATA_DIR, else conductor.d in the current directory.
func TestHooksInstallFindsTheDataDirLikeServe(t *testing.T) {
	clearConductorEnv(t)
	env := t.TempDir()
	if err := agents.WriteAssets(filepath.Join(env, "hooks"), "/opt/from-env/conductor"); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if err := agents.WriteAssets(filepath.Join(cwd, "conductor.d", "hooks"), "/opt/from-cwd/conductor"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	for _, tc := range []struct{ env, want string }{
		{env, "/opt/from-env/conductor"},
		{"", "/opt/from-cwd/conductor"},
	} {
		t.Setenv("CONDUCTOR_DATA_DIR", tc.env)
		home := t.TempDir()
		if code, stdout, stderr, err := runHooksWith(t, "install", "copilot", "--home", home); code != 0 || err != nil {
			t.Fatalf("exit %d %v\n%s%s", code, err, stdout, stderr)
		}
		b, _ := os.ReadFile(filepath.Join(home, ".copilot", "hooks", "conductor.json"))
		if !strings.Contains(string(b), `"`+tc.want+` notify --copilot-hook"`) {
			t.Fatalf("CONDUCTOR_DATA_DIR=%q: installed\n%s", tc.env, b)
		}
	}
}

// install all installs every adapter that has a file to install, with the
// skill for the agents that read skills, says what is left by hand (dsh) and
// what has nothing to install (aider), and exits 0.
func TestHooksInstallAll(t *testing.T) {
	clearConductorEnv(t)
	home := t.TempDir()
	code, stdout, stderr, err := runHooksWith(t, "install", "all", "--home", home, "--data-dir", t.TempDir())
	if code != 0 || err != nil {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	for _, a := range agents.All() {
		if a.Status == nil {
			continue
		}
		if ok, where := a.Status(home); !ok || !strings.Contains(stdout, where) {
			t.Errorf("%s: installed %v at %s\n%s", a.ID, ok, where, stdout)
		}
	}
	for _, want := range []string{"dsh: install by hand", "developer preview", "aider: nothing to install"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not say %q:\n%s", want, stdout)
		}
	}
	for _, rel := range []string{".claude/skills/conductor/SKILL.md", ".codex/skills/conductor/SKILL.md", ".agents/skills/conductor/SKILL.md"} {
		if b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel))); err != nil || string(b) != agents.Skill {
			t.Errorf("%s: %v", rel, err)
		}
	}
}

// One adapter failing does not stop install all: the others are installed,
// and the command exits 1 naming the one that failed.
func TestHooksInstallAllReportsAFailure(t *testing.T) {
	clearConductorEnv(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".cursor"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr, err := runHooksWith(t, "install", "all", "--home", home, "--data-dir", t.TempDir())
	if code != 1 || err == nil || !strings.Contains(err.Error(), "cursor") || !strings.Contains(stderr, "cursor:") {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(home, ".copilot", "hooks", "conductor.json")); err != nil {
		t.Fatalf("the adapters after it were not installed: %v", err)
	}
}

// An adapter whose hooks cannot be installed from a file prints what is left
// to do and the snippet to do it with, and exits 1.
func TestHooksInstallByHand(t *testing.T) {
	clearConductorEnv(t)
	for _, tc := range []struct{ id, note, snippet string }{
		{"dsh", "developer preview", "permission-requested"},
		{"aider", "nothing to install", "AIDER_NOTIFICATIONS=true"},
	} {
		home := t.TempDir()
		code, stdout, stderr, err := runHooksWith(t, "install", tc.id, "--home", home, "--data-dir", t.TempDir())
		if code != 1 || !strings.Contains(stdout, tc.note) || !strings.Contains(stdout, tc.snippet) {
			t.Fatalf("%s: exit %d %v\nstdout:\n%s\nstderr:\n%s", tc.id, code, err, stdout, stderr)
		}
		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Fatalf("%s wrote %v", tc.id, entries)
		}
	}
}

// status lists every adapter: whether its hooks are installed in the home
// and where, whether a launch wires them in, and what has nothing to install.
func TestHooksStatus(t *testing.T) {
	clearConductorEnv(t)
	home := t.TempDir()
	if code, stdout, stderr, err := runHooksWith(t, "install", "copilot", "--home", home, "--data-dir", t.TempDir()); code != 0 || err != nil {
		t.Fatalf("install: exit %d %v\n%s%s", code, err, stdout, stderr)
	}
	code, stdout, stderr, err := runHooksWith(t, "status", "--home", home)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if code != 0 || err != nil || len(lines) != 1+len(agents.All()) {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	row := map[string]string{}
	for _, l := range lines[1:] {
		row[strings.Fields(l)[0]] = l
	}
	if strings.Contains(row["copilot"], "not installed") {
		t.Errorf("copilot: %q", row["copilot"])
	}
	for id, want := range map[string][]string{
		"copilot": {" installed ", filepath.Join(home, ".copilot", "hooks", "conductor.json")},
		"claude":  {" not installed ", " at launch ", filepath.Join(home, ".claude", "settings.json")},
		"aider":   {" nothing to install ", " at launch"},
		"dsh":     {" by hand "},
	} {
		for _, w := range want {
			if !strings.Contains(row[id]+" ", w) {
				t.Errorf("%s: %q does not say %q", id, row[id], w)
			}
		}
	}
}

func TestHooksUsage(t *testing.T) {
	clearConductorEnv(t)
	for _, args := range [][]string{
		nil,
		{"install"},
		{"install", "gemini"},
		{"install", "copilot", "claude"},
		{"install", "copilot", "--bogus"},
		{"status", "copilot"},
		{"uninstall", "copilot"},
	} {
		if code, stdout, _, err := runHooksWith(t, args...); code != 2 || err == nil || stdout != "" {
			t.Errorf("%q: exit %d %v, stdout %q", args, code, err, stdout)
		}
	}
	if _, _, _, err := runHooksWith(t, "install", "gemini"); err == nil || !strings.Contains(err.Error(), `"gemini"`) || !strings.Contains(err.Error(), "copilot") {
		t.Errorf("unknown adapter: %v", err)
	}
	for _, args := range [][]string{{"-h"}, {"install", "-h"}, {"status", "-h"}} {
		if code, _, stderr, err := runHooksWith(t, args...); code != 0 || err != nil || !strings.Contains(stderr, "conductor hooks") {
			t.Errorf("%q: exit %d %v\n%s", args, code, err, stderr)
		}
	}
}

// conductor skill prints the Conductor skill; hooks and skill are commands of
// conductor.
func TestSkillCommand(t *testing.T) {
	clearConductorEnv(t)
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code, err := Run(t.Context(), []string{"skill"}, strings.NewReader(""), &stdout, &stderr); code != 0 || err != nil || stdout.String() != agents.Skill {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout.String(), stderr.String())
	}
	if code, err := Run(t.Context(), []string{"skill", "extra"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); code != 2 || err == nil {
		t.Fatalf("skill extra: exit %d %v", code, err)
	}
	stdout.Reset()
	if code, err := Run(t.Context(), []string{"hooks", "status", "--home", t.TempDir()}, strings.NewReader(""), &stdout, &stderr); code != 0 || err != nil || !strings.Contains(stdout.String(), "copilot") {
		t.Fatalf("hooks status: exit %d %v\n%s", code, err, stdout.String())
	}
	stdout.Reset()
	if code, _ := Run(t.Context(), []string{"help"}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "conductor hooks") || !strings.Contains(stdout.String(), "conductor skill") {
		t.Fatalf("usage:\n%s", stdout.String())
	}
}
