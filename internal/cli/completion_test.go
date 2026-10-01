package cli

import (
	"bytes"
	"context"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fakeConductor is a conductor that answers `crews --ids` with ids, for the
// scripts to run as the command typed (words[1] / COMP_WORDS[0]).
func fakeConductor(t *testing.T, ids ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "conductor")
	body := "#!/bin/sh\n[ \"$1\" = crews ] && [ \"$2\" = --ids ] && cat <<'IDS'\n" + strings.Join(ids, "\n") + "\nIDS\nexit 0\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// runCompletionWith runs `conductor completion args...` and returns what it printed.
func runCompletionWith(t *testing.T, args ...string) (int, string, string, error) {
	t.Helper()
	return runWith(t, func(_ context.Context, a []string, o, e io.Writer) (int, error) { return runCompletion(a, o, e) }, args...)
}

// scriptFile writes the completion script for shell to a file.
func scriptFile(t *testing.T, shell string) string {
	t.Helper()
	code, stdout, stderr, err := runCompletionWith(t, shell)
	if code != 0 || err != nil {
		t.Fatalf("completion %s: exit %d %v\n%s", shell, code, err, stderr)
	}
	p := filepath.Join(t.TempDir(), "conductor."+shell)
	if err := os.WriteFile(p, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// complete runs the script under the real shell (skipped when absent) with
// the completion system stubbed, completing the last of words, and returns
// the candidates, one per line. dir is the working directory, for file
// completion.
func complete(t *testing.T, shell, script, dir string, words ...string) []string {
	t.Helper()
	if _, err := exec.LookPath(shell); err != nil {
		t.Skipf("%s is not installed", shell)
	}
	var cmd *exec.Cmd
	switch shell {
	case "zsh":
		cmd = exec.Command("zsh", "-f", "-c", `compdef() { :; }; _files() { print -r -- "<files>" }; compadd() { local a; for a; do [[ $a == -- ]] || print -r -- "$a"; done }; source "$1"; shift; words=("$@"); CURRENT=${#words}; _conductor`, "zsh", script)
	case "bash":
		cmd = exec.Command("bash", "--noprofile", "--norc", "-c", `source "$1"; shift; COMP_WORDS=("$@"); COMP_CWORD=$(( ${#COMP_WORDS[@]} - 1 )); _conductor; (( ${#COMPREPLY[@]} )) && printf '%s\n' "${COMPREPLY[@]}"; true`, "bash", script)
	}
	cmd.Args = append(cmd.Args, words...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", shell, err, out)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// Both scripts parse under their shell.
func TestCompletionScriptsParse(t *testing.T) {
	for _, sh := range []string{"zsh", "bash"} {
		t.Run(sh, func(t *testing.T) {
			if _, err := exec.LookPath(sh); err != nil {
				t.Skipf("%s is not installed", sh)
			}
			if out, err := exec.Command(sh, "-n", scriptFile(t, sh)).CombinedOutput(); err != nil {
				t.Fatalf("%s -n: %v\n%s", sh, err, out)
			}
		})
	}
}

// The scripts complete subcommands, flags, flag values, positional words
// and, for up, the crew ids the typed conductor prints.
func TestCompletionCompletes(t *testing.T) {
	for _, sh := range []string{"zsh", "bash"} {
		t.Run(sh, func(t *testing.T) {
			script := scriptFile(t, sh)
			bin := fakeConductor(t, "alpha", "beta")
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "x.json"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			has := func(words []string, want ...string) bool {
				for _, w := range want {
					if !slices.Contains(words, w) {
						return false
					}
				}
				return true
			}
			if got := complete(t, sh, script, dir, bin, ""); !has(got, "serve", "host", "notify", "up", "crews", "hooks", "skill", "completion", "version") {
				t.Fatalf("subcommands: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "up", ""); !slices.Equal(got, []string{"alpha", "beta"}) {
				t.Fatalf("crew ids: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "up", "--"); !has(got, "--server", "--token", "--open") {
				t.Fatalf("up flags: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "up", "--server", ""); len(got) != 0 {
				t.Fatalf("a free value completes nothing: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "serve", "--log-level", ""); !slices.Equal(got, []string{"debug", "info", "warn", "error"}) {
				t.Fatalf("log level: %v", got)
			}
			got := complete(t, sh, script, dir, bin, "serve", "--config", "")
			if (sh == "zsh" && !slices.Equal(got, []string{"<files>"})) || (sh == "bash" && !slices.Equal(got, []string{"x.json"})) {
				t.Fatalf("files: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "hooks", ""); !slices.Equal(got, []string{"install", "status"}) {
				t.Fatalf("hooks words: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "hooks", "install", ""); !has(got, "all", "claude", "codex") {
				t.Fatalf("adapters: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "completion", ""); !slices.Equal(got, []string{"zsh", "bash", "install"}) {
				t.Fatalf("completion words: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "notify", "--"); !has(got, "--claude-hook", "--goose-hook", "--quiet") {
				t.Fatalf("notify flags: %v", got)
			}
			if sh == "bash" {
				if got := complete(t, sh, script, dir, bin, "up", "a"); !slices.Equal(got, []string{"alpha"}) {
					t.Fatalf("bash filters by the typed prefix: %v", got)
				}
			}
		})
	}
}

// Review Focus 2: whatever `crews --ids` prints reaches the candidates as
// data. A line holding shell syntax is never run, and a typed prefix with
// pattern characters is compared as text.
func TestCompletionNeverRunsWhatTheServerSends(t *testing.T) {
	for _, sh := range []string{"zsh", "bash"} {
		t.Run(sh, func(t *testing.T) {
			script := scriptFile(t, sh)
			dir := t.TempDir()
			marker := filepath.Join(dir, "ran")
			bin := fakeConductor(t, "alpha", "$(touch "+marker+")", "`touch "+marker+"`", "a b", "*")
			got := complete(t, sh, script, dir, bin, "up", "")
			if _, err := os.Stat(marker); err == nil {
				t.Fatalf("an id was run as a command: %v", got)
			}
			if !slices.Contains(got, "alpha") || !slices.Contains(got, "*") || slices.Contains(got, "x.json") {
				t.Fatalf("candidates: %q", got)
			}
			if sh == "bash" {
				if got := complete(t, sh, script, dir, bin, "up", "*"); !slices.Equal(got, []string{"*"}) {
					t.Fatalf("a typed * is text, not a pattern: %q", got)
				}
			}
		})
	}
}

// The scripts know every flag the commands define, and no other: the table
// in completionSpec is checked against each command's -h.
func TestCompletionSpecMatchesEveryFlag(t *testing.T) {
	ctx := t.Context()
	helps := map[string][][]string{
		"serve": {{"serve", "-h"}}, "host": {{"host", "-h"}}, "notify": {{"notify", "-h"}}, "up": {{"up", "-h"}}, "crews": {{"crews", "-h"}},
		"hooks": {{"hooks", "install", "-h"}, {"hooks", "status", "-h"}}, "completion": {{"completion", "install", "-h"}},
	}
	flagLine := regexp.MustCompile(`(?m)^  -([a-z][a-z0-9-]*)`)
	for _, c := range completionSpec() {
		want := map[string]bool{}
		for _, args := range helps[c.name] {
			var stderr bytes.Buffer
			if code, err := Run(ctx, args, strings.NewReader(""), io.Discard, &stderr); code != 0 || err != nil {
				t.Fatalf("%v: exit %d %v", args, code, err)
			}
			for _, m := range flagLine.FindAllStringSubmatch(stderr.String(), -1) {
				want["--"+m[1]] = true
			}
		}
		got := map[string]bool{}
		for _, f := range c.flags {
			got[f.name] = true
		}
		if !maps.Equal(got, want) {
			t.Errorf("%s: the table has %v, the command %v", c.name, slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
		}
	}
}

// conductor completion prints a script for zsh or bash and refuses the rest.
func TestCompletionUsage(t *testing.T) {
	if code, out, _, err := runCompletionWith(t, "zsh"); code != 0 || err != nil || !strings.HasPrefix(out, "#compdef conductor\n") || !strings.Contains(out, "compdef _conductor conductor") {
		t.Fatalf("zsh: %d %v\n%s", code, err, out)
	}
	if code, out, _, err := runCompletionWith(t, "bash"); code != 0 || err != nil || !strings.Contains(out, "complete -F _conductor conductor") {
		t.Fatalf("bash: %d %v\n%s", code, err, out)
	}
	for _, args := range [][]string{{}, {"fish"}, {"zsh", "extra"}} {
		if code, _, _, err := runCompletionWith(t, args...); code != 2 || err == nil {
			t.Fatalf("%v: %d %v", args, code, err)
		}
	}
}

// completion install appends one marked line to the shell's rc file, once;
// the shell comes from $SHELL unless --shell says; a missing file is made.
func TestCompletionInstall(t *testing.T) {
	clearConductorEnv(t)
	home := os.Getenv("HOME")
	t.Setenv("SHELL", "/bin/zsh")
	install := func(args ...string) (int, string, string, error) {
		return runCompletionWith(t, append([]string{"install"}, args...)...)
	}
	rc := filepath.Join(home, ".zshrc")
	code, out, _, err := install()
	if code != 0 || err != nil || !strings.Contains(out, rc) {
		t.Fatalf("first: %d %v\n%s", code, err, out)
	}
	want := completionLine("zsh") + "\n"
	if b, _ := os.ReadFile(rc); string(b) != want {
		t.Fatalf("rc:\n%s", b)
	}
	if fi, _ := os.Stat(rc); fi.Mode().Perm() != 0o600 {
		t.Fatalf("a new rc file is %v", fi.Mode().Perm())
	}
	code, out, _, err = install()
	if code != 0 || err != nil || !strings.Contains(out, "nothing to change") {
		t.Fatalf("second: %d %v\n%s", code, err, out)
	}
	if b, _ := os.ReadFile(rc); string(b) != want {
		t.Fatalf("rc changed on the second install:\n%s", b)
	}
	// A file without a final newline gets one before the line; --shell and --rc override the defaults.
	custom := filepath.Join(home, "rc")
	if err := os.WriteFile(custom, []byte("alias l='ls'"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _, err := install("--shell", "bash", "--rc", custom); code != 0 || err != nil {
		t.Fatalf("custom: %d %v", code, err)
	}
	if b, _ := os.ReadFile(custom); string(b) != "alias l='ls'\n"+completionLine("bash")+"\n" {
		t.Fatalf("custom rc:\n%s", b)
	}
	t.Setenv("SHELL", "/usr/bin/fish")
	if code, _, _, err := install(); code != 2 || err == nil {
		t.Fatalf("fish: %d %v", code, err)
	}
}
