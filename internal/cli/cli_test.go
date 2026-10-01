package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/version"
)

// runHooksWith runs `conductor hooks` with args, as a new conductor process
// would: with no binary recorded, and what it records forgotten when the
// test ends. HOME is a temporary directory, so that no test can write to the
// real one.
func runHooksWith(t *testing.T, args ...string) (code int, stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Cleanup(agents.ForgetBinary())
	var out, errOut bytes.Buffer
	code, err = runHooks(t.Context(), args, &out, &errOut)
	return code, out.String(), errOut.String(), err
}

// serverAssets writes the hook assets that a server naming bin writes to the
// data dir data. Writing them records bin for this process; the next
// runHooksWith forgets it, as a separate conductor process never knew it.
func serverAssets(t *testing.T, data, bin string) {
	t.Helper()
	t.Cleanup(agents.ForgetBinary())
	if err := agents.WriteAssets(filepath.Join(data, "hooks"), bin); err != nil {
		t.Fatal(err)
	}
}

// fakeBinary returns the path of a conductor binary that exists, for the
// hooks dir of a server to name: install and status adopt only a binary they
// find. It is never run.
func fakeBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "conductor")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// conductor hooks install puts an adapter's hooks into the home it is given,
// copied from the hooks dir of the data dir it is given, and prints the files
// it wrote; run again, it changes nothing and says so.
func TestHooksInstallCLI(t *testing.T) {
	clearConductorEnv(t)
	home, data, bin := t.TempDir(), t.TempDir(), fakeBinary(t)
	serverAssets(t, data, bin)
	code, stdout, stderr, err := runHooksWith(t, "install", "copilot", "--home", home, "--data-dir", data)
	file := filepath.Join(home, ".copilot", "hooks", "conductor.json")
	if code != 0 || err != nil || !strings.Contains(stdout, file) {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	if b, err := os.ReadFile(file); err != nil || !strings.Contains(string(b), `"`+bin+` notify --copilot-hook"`) {
		t.Fatalf("%s: %v\n%s", file, err, b)
	}
	code, stdout, _, err = runHooksWith(t, "install", "copilot", "--home", home, "--data-dir", data)
	if code != 0 || err != nil || strings.Contains(stdout, file) || !strings.Contains(stdout, "nothing to change") {
		t.Fatalf("second install: exit %d %v\n%s", code, err, stdout)
	}
}

// Without --data-dir the data dir is the one conductor serve would use:
// CONDUCTOR_DATA_DIR, else ~/.conductor, or an old conductor.d in the current
// directory while ~/.conductor holds no server data, as in the new home
// runHooksWith gives every run.
func TestHooksInstallFindsTheDataDirLikeServe(t *testing.T) {
	clearConductorEnv(t)
	env, fromEnv := t.TempDir(), fakeBinary(t)
	serverAssets(t, env, fromEnv)
	cwd, fromCwd := t.TempDir(), fakeBinary(t)
	serverAssets(t, filepath.Join(cwd, "conductor.d"), fromCwd)
	t.Chdir(cwd)
	for _, tc := range []struct{ env, want string }{
		{env, fromEnv},
		{"", fromCwd},
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

// conductor hooks takes the hooks from the data directory conductor serve
// would use: an old ./conductor.d while ~/.conductor holds only hooks/ (which
// conductor host writes there too), and ~/.conductor once it holds a
// server's data.
func TestHooksInstallTakesTheHomeDataDir(t *testing.T) {
	clearConductorEnv(t)
	t.Cleanup(agents.ForgetBinary())
	home := os.Getenv("HOME")
	fromHome := fakeBinary(t)
	serverAssets(t, filepath.Join(home, ".conductor"), fromHome)
	cwd, fromCwd := t.TempDir(), fakeBinary(t)
	serverAssets(t, filepath.Join(cwd, "conductor.d"), fromCwd)
	t.Chdir(cwd)
	install := func(want string) {
		t.Helper()
		target := t.TempDir()
		var out, errOut bytes.Buffer
		if code, err := runHooks(t.Context(), []string{"install", "copilot", "--home", target}, &out, &errOut); code != 0 || err != nil {
			t.Fatalf("exit %d %v\n%s%s", code, err, &out, &errOut)
		}
		b, _ := os.ReadFile(filepath.Join(target, ".copilot", "hooks", "conductor.json"))
		if !strings.Contains(string(b), `"`+want+` notify --copilot-hook"`) {
			t.Fatalf("installed, want %s:\n%s", want, b)
		}
	}
	install(fromCwd) // ~/.conductor holds hooks/ only
	if err := os.WriteFile(filepath.Join(home, ".conductor", "catalog.json"), []byte(`{"agents": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	install(fromHome)
}

// Without a home directory, --data-dir or CONDUCTOR_DATA_DIR, conductor
// hooks names the settings it takes, not a config file it has none of. An
// old ./conductor.d is still found, as conductor serve finds it.
func TestHooksWithoutAHomeNameTheDataDirFlag(t *testing.T) {
	clearConductorEnv(t)
	t.Cleanup(agents.ForgetBinary())
	t.Setenv("HOME", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	install := func() (int, string, error) {
		t.Helper()
		target := t.TempDir()
		var out, errOut bytes.Buffer
		code, err := runHooks(t.Context(), []string{"install", "copilot", "--home", target}, &out, &errOut)
		b, _ := os.ReadFile(filepath.Join(target, ".copilot", "hooks", "conductor.json"))
		return code, string(b), err
	}
	code, _, err := install()
	if code != 1 || err == nil || !strings.Contains(err.Error(), "--data-dir") || !strings.Contains(err.Error(), "CONDUCTOR_DATA_DIR") || strings.Contains(err.Error(), "dataDir in the config") {
		t.Fatalf("without a home: exit %d %v", code, err)
	}
	bin := fakeBinary(t)
	serverAssets(t, filepath.Join(cwd, "conductor.d"), bin)
	if code, installed, err := install(); code != 0 || err != nil || !strings.Contains(installed, `"`+bin+` notify --copilot-hook"`) {
		t.Fatalf("with an old ./conductor.d: exit %d %v\n%s", code, err, installed)
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
	t.Cleanup(agents.ForgetBinary())
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

// install and status name the binary the data dir's hooks were written for,
// whichever conductor runs them: what install puts in place, status reads as
// installed, and installing again changes nothing.
func TestHooksStatusAgreesWithInstall(t *testing.T) {
	clearConductorEnv(t)
	home, data, bin := t.TempDir(), t.TempDir(), fakeBinary(t)
	serverAssets(t, data, bin)
	file := filepath.Join(home, ".copilot", "hooks", "conductor.json")
	code, stdout, stderr, err := runHooksWith(t, "install", "copilot", "--home", home, "--data-dir", data)
	if code != 0 || err != nil || stderr != "" || !strings.Contains(stdout, file) {
		t.Fatalf("install: exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), `"`+bin+` notify --copilot-hook"`) {
		t.Fatalf("%s:\n%s", file, b)
	}
	code, stdout, stderr, err = runHooksWith(t, "status", "--home", home, "--data-dir", data)
	row := statusRow(stdout, "copilot")
	if code != 0 || err != nil || stderr != "" || !strings.Contains(row, " installed ") || strings.Contains(row, "not installed") {
		t.Fatalf("status: exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	if code, stdout, _, err = runHooksWith(t, "install", "copilot", "--home", home, "--data-dir", data); code != 0 || err != nil || !strings.Contains(stdout, "nothing to change") {
		t.Fatalf("second install: exit %d %v\n%s", code, err, stdout)
	}
}

// statusRow is the row of `conductor hooks status` output for adapter id.
func statusRow(stdout, id string) string {
	for _, l := range strings.Split(stdout, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == id {
			return l + " "
		}
	}
	return ""
}

// A data dir whose hooks name no binary (no server has written them) leaves
// install and status to this binary, and both say so; they agree with each
// other all the same.
func TestHooksWithoutServerAssetsNameThisBinary(t *testing.T) {
	clearConductorEnv(t)
	home, data := t.TempDir(), t.TempDir()
	t.Cleanup(agents.ForgetBinary())
	bin, err := agents.Binary() // a process that recorded none: this binary
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"install", "copilot", "--home", home, "--data-dir", data},
		{"status", "--home", home, "--data-dir", data},
	} {
		code, stdout, stderr, err := runHooksWith(t, args...)
		if code != 0 || err != nil || !strings.Contains(stderr, filepath.Join(data, "hooks")) || !strings.Contains(stderr, bin) {
			t.Fatalf("%q: exit %d %v\nstdout:\n%s\nstderr:\n%s", args, code, err, stdout, stderr)
		}
		if args[0] == "status" && strings.Contains(statusRow(stdout, "copilot"), "not installed") {
			t.Fatalf("status after install:\n%s", stdout)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".copilot", "hooks", "conductor.json")); !strings.Contains(string(b), bin+" notify --copilot-hook") {
		t.Fatalf("installed:\n%s", b)
	}
}

// Without --home the home is the user's own, which must be an absolute path:
// a relative HOME is no home, and nothing is checked or written.
func TestHooksNeedAKnownHome(t *testing.T) {
	clearConductorEnv(t)
	t.Chdir(t.TempDir())
	t.Setenv("HOME", "relative/home")
	t.Cleanup(agents.ForgetBinary())
	for _, args := range [][]string{{"install", "copilot"}, {"status"}} {
		var stdout, stderr bytes.Buffer
		code, err := runHooks(t.Context(), append(args, "--data-dir", t.TempDir()), &stdout, &stderr)
		if code != 1 || err == nil || !strings.Contains(err.Error(), "--home") || stdout.Len() != 0 {
			t.Fatalf("%q: exit %d %v\n%s", args, code, err, stdout.String())
		}
	}
	if _, err := os.Stat("relative"); !os.IsNotExist(err) {
		t.Fatalf("wrote into the relative home: %v", err)
	}
}

// Installing into a home of another user is refused before anything is
// written: the files would belong to the user running conductor, and the
// home's owner could not use them. Checking it is fine.
func TestHooksRefuseAHomeOfAnotherUser(t *testing.T) {
	clearConductorEnv(t)
	if os.Geteuid() != 0 {
		t.Skip("only root can make a directory another user's")
	}
	home := t.TempDir()
	if err := os.Chown(home, 65534, 65534); err != nil {
		t.Skipf("cannot make a directory another user's here: %v", err)
	}
	code, stdout, stderr, err := runHooksWith(t, "install", "all", "--home", home, "--data-dir", t.TempDir())
	if code != 1 || err == nil || !strings.Contains(err.Error(), "sudo -u ") || strings.Contains(stdout, "wrote") {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("wrote %v", entries)
	}
	if code, _, _, err := runHooksWith(t, "status", "--home", home, "--data-dir", t.TempDir()); code != 0 || err != nil {
		t.Fatalf("status: exit %d %v", code, err)
	}
}

// A hooks dir whose binary is gone (conductor moved or removed since the
// server wrote it) is not adopted: install and status say so and use the
// conductor that runs them, as for a data dir without hooks, and agree with
// each other.
func TestHooksAdoptOnlyABinaryThatExists(t *testing.T) {
	clearConductorEnv(t)
	home, data := t.TempDir(), t.TempDir()
	gone := filepath.Join(t.TempDir(), "conductor")
	serverAssets(t, data, gone)
	t.Cleanup(agents.ForgetBinary())
	bin, err := agents.Binary() // a process that recorded none: this binary
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"install", "copilot", "--home", home, "--data-dir", data},
		{"status", "--home", home, "--data-dir", data},
	} {
		code, stdout, stderr, err := runHooksWith(t, args...)
		if code != 0 || err != nil || !strings.Contains(stderr, gone) || !strings.Contains(stderr, "does not exist") || !strings.Contains(stderr, bin) {
			t.Fatalf("%q: exit %d %v\nstdout:\n%s\nstderr:\n%s", args, code, err, stdout, stderr)
		}
		if args[0] == "status" && strings.Contains(statusRow(stdout, "copilot"), "not installed") {
			t.Fatalf("status after install:\n%s", stdout)
		}
	}
	b, _ := os.ReadFile(filepath.Join(home, ".copilot", "hooks", "conductor.json"))
	if !strings.Contains(string(b), bin+" notify --copilot-hook") || strings.Contains(string(b), gone) {
		t.Fatalf("installed:\n%s", b)
	}
}

// install and status refuse a hooks dir that is not 0700, as conductor serve
// writes it, naming it: the hook commands in it would go into the agents'
// configs. A directory others may write to is refused, and so is one of a
// checkout, which others may read (0755). Nothing is written.
func TestHooksRefuseAHooksDirOthersMayWrite(t *testing.T) {
	clearConductorEnv(t)
	if runtime.GOOS == "windows" {
		t.Skip("the hooks dir is checked on Unix only")
	}
	for _, mode := range []os.FileMode{0o775, 0o755} {
		home, data := t.TempDir(), t.TempDir()
		serverAssets(t, data, fakeBinary(t))
		hooks := filepath.Join(data, "hooks")
		if err := os.Chmod(hooks, mode); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"install", "copilot", "--home", home, "--data-dir", data},
			{"install", "all", "--home", home, "--data-dir", data},
			{"status", "--home", home, "--data-dir", data},
		} {
			code, stdout, stderr, err := runHooksWith(t, args...)
			if code != 1 || err == nil || !strings.Contains(err.Error(), hooks) || !strings.Contains(err.Error(), "chmod 700") || stdout != "" {
				t.Fatalf("mode %o, %q: exit %d %v\nstdout:\n%s\nstderr:\n%s", mode, args, code, err, stdout, stderr)
			}
		}
		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Fatalf("mode %o: wrote %v", mode, entries)
		}
	}
}

// The hooks dir records the version of the conductor that wrote it. conductor
// hooks says so when that is another version than its own, or unknown, and
// goes on.
func TestHooksWarnWhenTheServerIsAnotherVersion(t *testing.T) {
	clearConductorEnv(t)
	data, bin := t.TempDir(), fakeBinary(t)
	serverAssets(t, data, bin)
	code, _, stderr, err := runHooksWith(t, "install", "copilot", "--home", t.TempDir(), "--data-dir", data)
	if code != 0 || err != nil || strings.Contains(stderr, "were written by") {
		t.Fatalf("same version: exit %d %v\n%s", code, err, stderr)
	}
	record := filepath.Join(data, "hooks", ".version")
	if err := os.WriteFile(record, []byte("v0.0.1"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr, err = runHooksWith(t, "install", "copilot", "--home", t.TempDir(), "--data-dir", data)
	if code != 0 || err != nil || !strings.Contains(stderr, "conductor v0.0.1") || !strings.Contains(stderr, "conductor "+version.Version) {
		t.Fatalf("another version: exit %d %v\n%s", code, err, stderr)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if _, _, stderr, _ = runHooksWith(t, "status", "--data-dir", data); !strings.Contains(stderr, "did not record its version") {
		t.Fatalf("no record:\n%s", stderr)
	}
}

// A skill file of the user's own is the only step left: conductor hooks says
// so and prints no settings snippet.
func TestHooksInstallByHandPrintsOnlyWhatIsNeeded(t *testing.T) {
	clearConductorEnv(t)
	home := t.TempDir()
	skill := filepath.Join(home, ".claude", "skills", "conductor", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr, err := runHooksWith(t, "install", "claude", "--home", home, "--data-dir", t.TempDir())
	if code != 1 || err != nil || !strings.Contains(stdout, "is not Conductor's skill") || strings.Contains(stdout, "notify --claude-hook") {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
}
