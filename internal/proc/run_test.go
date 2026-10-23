package proc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// sh runs a shell script through Run with the given timeout: the tests'
// commands, never the product's, which takes argv arrays alone.
func sh(t *testing.T, timeout time.Duration, script string, opts ...func(*Spec)) Result {
	t.Helper()
	s := Spec{Argv: []string{"sh", "-c", script}, Dir: t.TempDir(), Timeout: timeout}
	for _, o := range opts {
		o(&s)
	}
	return Run(context.Background(), s)
}

// gone waits until the process pid is no more, up to the deadline.
func gone(t *testing.T, pid int, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func pidIn(t *testing.T, out []byte) int {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("no pid in %q: %v", out, err)
	}
	return pid
}

func TestRunReportsExits(t *testing.T) {
	res := sh(t, time.Minute, "echo hi; echo there >&2; exit 0")
	if res.Exit != 0 || res.Err != "" || res.Signal != "" || string(res.Head) != "hi\nthere\n" || res.Total != 9 || res.Truncated || res.TimedOut || res.Canceled || res.Killed {
		t.Fatalf("%+v", res)
	}
	if res.Process.PID == 0 || res.Process.Group != res.Process.PID || res.Started.IsZero() || res.Duration <= 0 {
		t.Fatalf("process %+v started %v took %v", res.Process, res.Started, res.Duration)
	}
	if res := sh(t, time.Minute, "exit 3"); res.Exit != 3 || res.Err != "" || res.Signal != "" {
		t.Fatalf("exit 3: %+v", res)
	}
	if res := sh(t, time.Minute, "kill -TERM $$"); res.Exit != -1 || res.Signal != "terminated" || res.Err != "" {
		t.Fatalf("a signal: %+v", res)
	}
	if res := sh(t, time.Minute, "exit 0", func(s *Spec) { s.Argv = []string{"/nonexistent/program"} }); res.Exit != -1 || res.Err == "" || !res.Started.IsZero() {
		t.Fatalf("a program that is not there: %+v", res)
	}
}

func TestRunRefusesABadSpec(t *testing.T) {
	for name, s := range map[string]Spec{
		"no command":    {Dir: "/", Timeout: time.Second},
		"empty command": {Argv: []string{""}, Dir: "/", Timeout: time.Second},
		"no timeout":    {Argv: []string{"true"}, Dir: "/"},
		"long timeout":  {Argv: []string{"true"}, Dir: "/", Timeout: MaxTimeout + 1},
		"relative dir":  {Argv: []string{"true"}, Dir: ".", Timeout: time.Second},
		"conductor var": {Argv: []string{"true"}, Dir: "/", Timeout: time.Second, Env: map[string]string{"CONDUCTOR_Y": "1"}},
		"a loader":      {Argv: []string{"true"}, Dir: "/", Timeout: time.Second, Env: map[string]string{"LD_PRELOAD": "x"}},
		"not a name":    {Argv: []string{"true"}, Dir: "/", Timeout: time.Second, Env: map[string]string{"A=B": "x"}},
	} {
		if res := Run(context.Background(), s); res.Err == "" || res.Exit != -1 || !res.Started.IsZero() {
			t.Errorf("%s: %+v", name, res)
		}
	}
}

func TestRunKillsTheGroupAtTheTimeout(t *testing.T) {
	res := sh(t, 300*time.Millisecond, "sleep 30 & echo $!; wait")
	if !res.TimedOut || res.Canceled || res.Err != "" {
		t.Fatalf("%+v", res)
	}
	if pid := pidIn(t, res.Head); !gone(t, pid, 3*time.Second) {
		t.Fatalf("the sleep %d outlived the run", pid)
	}
}

func TestRunCancelIsNotATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	res := Run(ctx, Spec{Argv: []string{"sleep", "30"}, Dir: t.TempDir(), Timeout: time.Minute})
	if !res.Canceled || res.TimedOut || res.Exit != -1 || res.Signal != "killed" || res.Duration > 5*time.Second {
		t.Fatalf("%+v", res)
	}
}

// What the leader leaves behind, holding its pipe or not, is killed once
// the wait is over, after exit 0 and after exit 3 alike.
func TestRunEndsWhatTheLeaderLeaves(t *testing.T) {
	for _, exit := range []int{0, 3} {
		res := sh(t, time.Minute, "sleep 30 & echo $!; exit "+strconv.Itoa(exit))
		if res.Exit != exit || !res.Killed || res.TimedOut || res.Err != "" {
			t.Fatalf("exit %d: %+v", exit, res)
		}
		if pid := pidIn(t, res.Head); !gone(t, pid, 3*time.Second) {
			t.Fatalf("exit %d: the sleep %d outlived the run", exit, pid)
		}
	}
}

func TestRunKeepsHeadAndTail(t *testing.T) {
	var log bytes.Buffer
	res := sh(t, time.Minute, "echo BEGIN; head -c 1048576 /dev/zero | tr '\\000' x; echo; echo END", func(s *Spec) {
		s.Head, s.Tail, s.Log, s.LogLimit = 100, 50, &log, 4096
	})
	if res.Exit != 0 || res.Err != "" || res.LogErr != "" {
		t.Fatalf("%+v", res)
	}
	const total = 6 + 1048576 + 1 + 4
	if res.Total != total || !res.Truncated || len(res.Head) != 100 || len(res.Tail) != 50 {
		t.Fatalf("total %d truncated %v head %d tail %d", res.Total, res.Truncated, len(res.Head), len(res.Tail))
	}
	if !bytes.HasPrefix(res.Head, []byte("BEGIN\nxxxx")) || !bytes.HasSuffix(res.Tail, []byte("xxxx\nEND\n")) {
		t.Fatalf("head %q tail %q", res.Head, res.Tail)
	}
	text := log.String()
	dropped := total - 4096 - LogTail
	if !strings.HasPrefix(text, "BEGIN\n") || !strings.HasSuffix(text, "xxxx\nEND\n") || !strings.Contains(text, "\n… "+strconv.Itoa(dropped)+" bytes dropped …\n") || len(text) > 4096+LogTail+64 {
		t.Fatalf("log of %d bytes: %.40q … %q", len(text), text, text[len(text)-40:])
	}

	// Less than the bounds: everything kept, nothing truncated, no
	// dropped line, the tail the bytes after the head.
	log.Reset()
	res = sh(t, time.Minute, "echo 0123456789", func(s *Spec) { s.Head, s.Tail, s.Log = 4, 100, &log })
	if res.Truncated || string(res.Head) != "0123" || string(res.Tail) != "456789\n" || res.Total != 11 || log.String() != "0123456789\n" {
		t.Fatalf("%+v log %q", res, log.String())
	}
}

func TestRunStripsTheEnvironment(t *testing.T) {
	t.Setenv("CONDUCTOR_X", "secret")
	t.Setenv("LD_PRELOAD", "loader")
	t.Setenv("OUTSIDE_THE_LIST", "1")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("ALLOWED_BY_THE_CALLER", "yes")
	res := sh(t, time.Minute, "env", func(s *Spec) {
		s.Env = map[string]string{"MY_VAR": "v"}
		s.Allow = []string{"ALLOWED_BY_THE_CALLER"}
	})
	if res.Exit != 0 {
		t.Fatalf("%+v", res)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(res.Head), "\n") {
		have[line] = true
	}
	for _, want := range []string{"TERM=dumb", "NO_COLOR=1", "MY_VAR=v", "ALLOWED_BY_THE_CALLER=yes", "PATH=" + os.Getenv("PATH")} {
		if !have[want] {
			t.Errorf("missing %s in %q", want, res.Head)
		}
	}
	for _, line := range strings.Split(string(res.Head), "\n") {
		k, _, _ := strings.Cut(line, "=")
		switch {
		case strings.HasPrefix(k, "CONDUCTOR_"), k == "LD_PRELOAD", k == "OUTSIDE_THE_LIST", k == "COLORTERM":
			t.Errorf("leaked %s", line)
		}
	}
}

func TestRunStartedReportsTheProcess(t *testing.T) {
	var got Process
	res := sh(t, time.Minute, "echo $$", func(s *Spec) { s.Started = func(p Process) { got = p } })
	if res.Exit != 0 || got.PID == 0 || got.PID != pidIn(t, res.Head) || got != res.Process || got.StartedAt.IsZero() {
		t.Fatalf("started %+v result %+v", got, res)
	}
}
