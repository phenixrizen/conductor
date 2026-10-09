package agents

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

var claudeProbe = Probe{Args: []string{"--version"}, Match: regexp.MustCompile(`(?m)^\s*(\d+\.\d+\.\d+)\s*\(Claude Code\)`), Verified: true}

func TestRunProbeIdentifies(t *testing.T) {
	p := script(t, `[ "$1" = --version ] && echo "2.1.287 (Claude Code)"`)
	res := RunProbe(context.Background(), []string{p}, claudeProbe, nil)
	if !res.Ran || !res.Identified || res.Version != "2.1.287" || res.Output != "" || res.Error != "" {
		t.Fatalf("%+v", res)
	}
}

func TestRunProbeRejectsAnotherProgram(t *testing.T) {
	goose := Probe{Args: []string{"--version"}, Match: regexp.MustCompile(`(?m)^\s*goose\s+v?(\d+\.\d+\.\d+)`), Reject: regexp.MustCompile(`(?m)^\s*goose version:\s*v`)}
	p := script(t, `echo "goose version: v3.22.1"`)
	res := RunProbe(context.Background(), []string{p}, goose, nil)
	if !res.Ran || res.Identified || !res.Impostor || res.Output != "goose version: v3.22.1" {
		t.Fatalf("%+v", res)
	}
	other := script(t, `echo "something else entirely"; exit 1`)
	res = RunProbe(context.Background(), []string{other}, claudeProbe, nil)
	if !res.Ran || res.Identified || res.Impostor || res.Output != "something else entirely" {
		t.Fatalf("non-zero exit with another output: %+v", res)
	}
}

func TestRunProbeTimesOutAHang(t *testing.T) {
	p := script(t, `sleep 30 & wait`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	res := RunProbe(ctx, []string{p}, claudeProbe, nil)
	if res.Ran || !strings.Contains(res.Error, "timeout") {
		t.Fatalf("%+v", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %s: the process group was not killed", d)
	}
}

func TestRunProbeBoundsOutputAndReadsStderr(t *testing.T) {
	p := script(t, `head -c 100000 /dev/zero | tr '\0' 'x'; echo; echo "2.1.287 (Claude Code)" >&2`)
	res := RunProbe(context.Background(), []string{p}, claudeProbe, nil)
	if !res.Ran || res.Identified {
		t.Fatalf("the version past the bound must not be read: %+v", res)
	}
	if len(res.Output) > probeMaxLine {
		t.Fatalf("output line %d bytes", len(res.Output))
	}
	p = script(t, `echo "2.1.287 (Claude Code)" >&2`)
	if res := RunProbe(context.Background(), []string{p}, claudeProbe, nil); !res.Identified {
		t.Fatalf("stderr: %+v", res)
	}
}

func TestRunProbeRunsNoShellAndASpartanEnvironment(t *testing.T) {
	p := script(t, `echo "args=$# first=$1"; echo "HOME=$HOME CONDUCTOR_X=${CONDUCTOR_X:-unset} MINE=${MINE:-unset} TERM=$TERM"`)
	probe := Probe{Args: []string{"--version", "; echo injected"}, Match: regexp.MustCompile(`args=(\d+) first=--version`)}
	t.Setenv("CONDUCTOR_X", "secret")
	res := RunProbe(context.Background(), []string{p}, probe, map[string]string{"MINE": "yes", "CONDUCTOR_Y": "no"})
	if !res.Identified || res.Version != "2" {
		t.Fatalf("argv: %+v", res)
	}
	p2 := script(t, `echo "HOME=$HOME CONDUCTOR_X=${CONDUCTOR_X:-unset} CONDUCTOR_Y=${CONDUCTOR_Y:-unset} MINE=${MINE:-unset} TERM=$TERM"`)
	res = RunProbe(context.Background(), []string{p2}, Probe{Match: regexp.MustCompile(`^(never)$`)}, map[string]string{"MINE": "yes", "CONDUCTOR_Y": "no"})
	if !strings.Contains(res.Output, "CONDUCTOR_X=unset") || !strings.Contains(res.Output, "CONDUCTOR_Y=unset") || !strings.Contains(res.Output, "MINE=yes") || !strings.Contains(res.Output, "TERM=dumb") {
		t.Fatalf("env: %+v", res)
	}
}

func TestRunProbeKeepsTheCommandsOwnArguments(t *testing.T) {
	stub := script(t, `[ "$1" = agent.sh ] && [ "$2" = --version ] && echo "2.1.287 (Claude Code)"`)
	res := RunProbe(context.Background(), []string{stub, "agent.sh"}, claudeProbe, nil)
	if !res.Identified || res.Version != "2.1.287" {
		t.Fatalf("bash stub.sh --version: %+v", res)
	}
}

func TestRunProbeReportsAMissingProgram(t *testing.T) {
	res := RunProbe(context.Background(), []string{filepath.Join(t.TempDir(), "missing")}, claudeProbe, nil)
	if res.Ran || res.Error == "" {
		t.Fatalf("%+v", res)
	}
}

func TestEveryAdapterProbeHasArgsAndAPattern(t *testing.T) {
	for _, a := range All() {
		if a.Probe == nil {
			continue
		}
		if len(a.Probe.Args) == 0 || (a.Probe.Match == nil && a.Probe.Reject == nil) {
			t.Errorf("%s: probe %+v", a.ID, a.Probe)
		}
	}
	for _, id := range []string{"claude", "codex", "goose", "copilot", "agy"} {
		if p := ProbeFor(id); p == nil || !p.Verified {
			t.Errorf("%s: the probe seen live must be verified", id)
		}
	}
	if p := ProbeFor("goose"); p == nil || p.Reject == nil || !p.Reject.MatchString("goose version: v3.22.1") || p.Match.MatchString("goose version: v3.22.1") {
		t.Fatalf("goose: %+v", p)
	}
	// The migrations tool as installed by go install prints no space after the colon.
	if p := ProbeFor("goose"); !p.Reject.MatchString("goose version:v3.5.3") || p.Match.MatchString("goose version:v3.5.3") {
		t.Fatalf("goose without the space: %+v", p)
	}
	// The version lines seen live on 2026-10-09.
	for id, out := range map[string]string{"copilot": "GitHub Copilot CLI 1.0.91.", "agy": "1.2.14"} {
		if m := ProbeFor(id).Match.FindStringSubmatch(out); m == nil {
			t.Fatalf("%s %q", id, out)
		}
	}
	// Block's goose 1.54.0 prints its bare version; older releases "goose 1.0.21". Both are Goose.
	for _, out := range []string{" 1.54.0", "1.54.0\n", "goose 1.0.21"} {
		if m := ProbeFor("goose").Match.FindStringSubmatch(out); m == nil || (m[1] != "1.54.0" && m[1] != "1.0.21") {
			t.Fatalf("goose %q: %v", out, m)
		}
	}
	if ProbeFor("nope") != nil {
		t.Fatal("unknown adapter has a probe")
	}
}

// TestProbeFixtures matches each adapter's probe against the outputs seen,
// live or in the vendor's docs, so a pattern change is a deliberate one.
func TestProbeFixtures(t *testing.T) {
	fixtures := map[string]map[string]string{
		"claude": {"2.1.287 (Claude Code)": "2.1.287", "\x1b[1m2.2.0 (Claude Code)\x1b[0m": "2.2.0"},
		"codex":  {"codex-cli 0.159.0": "0.159.0", "codex-cli 0.160.0\n": "0.160.0"},
		"aider":  {"aider 0.86.1": "0.86.1"},
		"omp":    {"oh-my-pi 0.4.2": "0.4.2"},
	}
	for id, cases := range fixtures {
		p := ProbeFor(id)
		if p == nil {
			t.Fatalf("%s has no probe", id)
		}
		for out, want := range cases {
			m := p.Match.FindStringSubmatch(stripANSI(out))
			if m == nil || m[1] != want {
				t.Errorf("%s: %q gave %v, want %s", id, out, m, want)
			}
		}
	}
	if p := ProbeFor("codex"); p.Match.MatchString("2.1.287 (Claude Code)") {
		t.Error("codex's pattern takes Claude Code's output")
	}
	if p := ProbeFor("pi"); p.Match.MatchString("oh-my-pi 0.4.2") {
		t.Error("pi's pattern takes oh-my-pi's output")
	}
}
