//go:build unix

package proc

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// serve starts a shell script as a service in dir: the tests' commands,
// never the product's, which takes argv arrays alone. The service is
// killed when the test ends, whatever the test did.
func serve(t *testing.T, dir, script string, opts ...func(*Spec)) *Service {
	t.Helper()
	s := Spec{Argv: []string{"sh", "-c", script}, Dir: dir}
	for _, o := range opts {
		o(&s)
	}
	svc, err := Serve(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		svc.Stop(ctx)
	})
	return svc
}

// pidFile waits up to 10 s for the file at path to hold a pid, and
// returns it: a service says it runs by writing its pid there.
func pidFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pid in %s after 10 s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// shortGrace makes Stop's grace d for the test.
func shortGrace(t *testing.T, d time.Duration) {
	old := serveGrace
	serveGrace = d
	t.Cleanup(func() { serveGrace = old })
}

// A service runs until it exits: Serve returns once the process exists,
// Started told its pid, group and start before; Done closes at its own
// exit, and Result says what became of it, its output's head and the
// process Started was told.
func TestAServiceRunsUntilItExits(t *testing.T) {
	dir := t.TempDir()
	gate, pid := filepath.Join(dir, "gate"), filepath.Join(dir, "pid")
	var started Process
	svc := serve(t, dir, `echo $$ > pid; echo serving; while [ ! -e gate ]; do sleep 0.05; done; echo leaving; exit 3`, func(s *Spec) {
		s.Started = func(p Process) { started = p }
	})
	if started.PID == 0 || started.Group != started.PID || started.StartedAt.IsZero() {
		t.Fatalf("Started was told %+v", started)
	}
	if got := pidFile(t, pid); got != started.PID {
		t.Fatalf("the service is %d, Started was told %d", got, started.PID)
	}
	select {
	case <-svc.Done():
		t.Fatal("the service is done before it exited")
	default:
	}
	if res := svc.Result(); res.Exit != -1 || res.Process != started {
		t.Fatalf("the result while it runs: %+v", res)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-svc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the service did not end after 10 s")
	}
	res := svc.Result()
	if res.Exit != 3 || res.Signal != "" || res.Err != "" || res.Canceled || res.TimedOut || string(res.Head) != "serving\nleaving\n" || res.Process != started || res.Duration <= 0 {
		t.Fatalf("%+v", res)
	}
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("a stop after the end: %v", err)
	}
}

// Stop asks the service's group to end (SIGTERM) and kills it (SIGKILL)
// once the grace has passed: a service that ignores SIGTERM is killed
// after the grace, its child in the group with it; one that ends on
// SIGTERM ends before it, as its trap says. Stop returns once the process
// is reaped.
func TestStopEndsAServiceAndItsGroup(t *testing.T) {
	t.Run("one that ignores SIGTERM", func(t *testing.T) {
		const grace = 300 * time.Millisecond
		shortGrace(t, grace)
		dir := t.TempDir()
		svc := serve(t, dir, `trap "" TERM; sleep 300 & echo $! > child; echo $$ > pid; wait`)
		pid, child := pidFile(t, filepath.Join(dir, "pid")), pidFile(t, filepath.Join(dir, "child"))
		begin := time.Now()
		if err := svc.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if took := time.Since(begin); took < grace {
			t.Fatalf("Stop returned after %v, before the grace of %v", took, grace)
		}
		res := svc.Result()
		if res.Signal != "killed" || res.Exit != -1 || res.Canceled {
			t.Fatalf("%+v", res)
		}
		for _, p := range []int{pid, child} {
			if !gone(t, p, 3*time.Second) {
				t.Fatalf("%d outlived the stop", p)
			}
		}
	})
	t.Run("one that ends on SIGTERM", func(t *testing.T) {
		shortGrace(t, time.Hour)
		dir := t.TempDir()
		svc := serve(t, dir, `trap 'echo stopping; exit 0' TERM; sleep 300 & echo $! > child; echo $$ > pid; wait`)
		pid, child := pidFile(t, filepath.Join(dir, "pid")), pidFile(t, filepath.Join(dir, "child"))
		if err := svc.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		res := svc.Result()
		if res.Exit != 0 || res.Signal != "" || !strings.Contains(string(res.Head), "stopping") {
			t.Fatalf("not ended by its trap: %+v", res)
		}
		for _, p := range []int{pid, child} {
			if !gone(t, p, 3*time.Second) {
				t.Fatalf("%d outlived the stop", p)
			}
		}
	})
	t.Run("a context that ends", func(t *testing.T) {
		shortGrace(t, time.Hour)
		dir := t.TempDir()
		svc := serve(t, dir, `trap "" TERM; echo $$ > pid; while :; do sleep 0.05; done`)
		pid := pidFile(t, filepath.Join(dir, "pid"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := svc.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		if res := svc.Result(); res.Signal != "killed" {
			t.Fatalf("%+v", res)
		}
		if !gone(t, pid, 3*time.Second) {
			t.Fatalf("%d outlived the stop", pid)
		}
	})
}

// A stop's grace is the group's, not its first process's alone: a first
// process that ends on SIGTERM before what it started (a wrapper's shell)
// leaves the rest of the group the grace. A member that takes a moment to
// shut down finishes it, whether it holds the service's output or
// redirected its own, and Stop returns once it has ended, the grace an
// hour; one that ignores SIGTERM is killed when the grace ends, not when
// its first process did.
func TestStopGivesTheGraceToTheGroupNotItsFirstProcess(t *testing.T) {
	// The member, a shell the first one starts in the group: asked to end,
	// it takes a second, then says it is done.
	const slow = `trap 'sleep 1; echo done > done; exit 0' TERM; echo $$ > child; while :; do sleep 0.05; done`
	for _, tc := range []struct{ name, out string }{{"holding the service's output", ""}, {"its output redirected", " >/dev/null 2>&1"}} {
		t.Run("a member "+tc.name+" finishes its shutdown", func(t *testing.T) {
			shortGrace(t, time.Hour)
			dir := t.TempDir()
			svc := serve(t, dir, `sh -c "$1"`+tc.out+` & echo $$ > pid; wait`, func(s *Spec) { s.Argv = append(s.Argv, "sh", slow) })
			pid, child := pidFile(t, filepath.Join(dir, "pid")), pidFile(t, filepath.Join(dir, "child"))
			if err := svc.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(filepath.Join(dir, "done")); err != nil || string(b) != "done\n" {
				t.Fatalf("the member was killed before it finished its shutdown: %q %v", b, err)
			}
			if res := svc.Result(); res.Signal != "terminated" || res.Exit != -1 || res.Killed {
				t.Fatalf("%+v", res)
			}
			for _, p := range []int{pid, child} {
				if !gone(t, p, 3*time.Second) {
					t.Fatalf("%d outlived the stop", p)
				}
			}
		})
	}
	t.Run("a member that ignores SIGTERM is killed at the grace", func(t *testing.T) {
		const grace = time.Second
		shortGrace(t, grace)
		dir := t.TempDir()
		svc := serve(t, dir, `sh -c "$1" & echo $$ > pid; wait`, func(s *Spec) {
			s.Argv = append(s.Argv, "sh", `trap "" TERM; echo $$ > child; while :; do sleep 0.05; done`)
		})
		pidFile(t, filepath.Join(dir, "pid"))
		child := pidFile(t, filepath.Join(dir, "child"))
		begin := time.Now()
		if err := svc.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if took := time.Since(begin); took < grace {
			t.Fatalf("Stop returned after %v, before the grace of %v", took, grace)
		}
		if res := svc.Result(); res.Signal != "terminated" {
			t.Fatalf("%+v", res)
		}
		if !gone(t, child, 3*time.Second) {
			t.Fatalf("%d outlived the stop", child)
		}
	})
}

// Stop may be called from more than one goroutine at once: each returns
// once the service has ended, and it ends once.
func TestStopFromManyGoroutines(t *testing.T) {
	shortGrace(t, 200*time.Millisecond)
	dir := t.TempDir()
	svc := serve(t, dir, `trap "" TERM; echo $$ > pid; while :; do sleep 0.05; done`)
	pid := pidFile(t, filepath.Join(dir, "pid"))
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { svc.Stop(context.Background()) })
	}
	wg.Wait()
	select {
	case <-svc.Done():
	default:
		t.Fatal("a Stop returned before the service ended")
	}
	if !gone(t, pid, 3*time.Second) {
		t.Fatalf("%d outlived the stops", pid)
	}
}

// The context Serve is given bounds the service's life: its end kills the
// group, and the result says the context ended (Canceled).
func TestAServicesContextBoundsItsLife(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc, err := Serve(ctx, Spec{Argv: []string{"sh", "-c", `trap "" TERM; echo $$ > pid; while :; do sleep 0.05; done`}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	pid := pidFile(t, filepath.Join(dir, "pid"))
	cancel()
	select {
	case <-svc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the service outlived its context by 10 s")
	}
	if res := svc.Result(); !res.Canceled || res.Signal != "killed" {
		t.Fatalf("%+v", res)
	}
	if !gone(t, pid, 3*time.Second) {
		t.Fatalf("%d outlived its context", pid)
	}
}

// A service is refused as a run is: a bad spec, a token's name in Inject,
// a CONDUCTOR_ name in Env, a context already ended; one whose program is
// not there is an error with nothing running. Conductor's own names are
// given to it. Its timeout is its start's: one too short for any start
// ends it at once.
func TestServeRefusesWhatRunRefuses(t *testing.T) {
	dir := t.TempDir()
	done, cancel := context.WithCancel(context.Background())
	cancel()
	for name, tc := range map[string]struct {
		ctx context.Context
		s   Spec
	}{
		"no command":       {context.Background(), Spec{Dir: dir}},
		"relative dir":     {context.Background(), Spec{Argv: []string{"true"}, Dir: "."}},
		"long timeout":     {context.Background(), Spec{Argv: []string{"true"}, Dir: dir, Timeout: MaxTimeout + 1}},
		"a token":          {context.Background(), Spec{Argv: []string{"true"}, Dir: dir, Inject: map[string]string{"CONDUCTOR_NOTIFY_TOKEN": "x"}}},
		"a CONDUCTOR_ env": {context.Background(), Spec{Argv: []string{"true"}, Dir: dir, Env: map[string]string{"CONDUCTOR_PORT_WEB": "1"}}},
		"a loader":         {context.Background(), Spec{Argv: []string{"true"}, Dir: dir, Env: map[string]string{"LD_PRELOAD": "x"}}},
		"ended":            {done, Spec{Argv: []string{"true"}, Dir: dir}},
		"not there":        {context.Background(), Spec{Argv: []string{"/nonexistent/program"}, Dir: dir}},
	} {
		if svc, err := Serve(tc.ctx, tc.s); err == nil || svc != nil {
			t.Errorf("%s: %v %v", name, svc, err)
		}
	}
	svc := serve(t, dir, `env > env.txt; echo $$ > pid; while :; do sleep 0.05; done`, func(s *Spec) {
		s.Inject = map[string]string{"CONDUCTOR_PORT_WEB": "41000", "CONDUCTOR_PREVIEW_URL": "http://127.0.0.1:41000/"}
	})
	pidFile(t, filepath.Join(dir, "pid"))
	b, err := os.ReadFile(filepath.Join(dir, "env.txt"))
	if err != nil || !strings.Contains(string(b), "CONDUCTOR_PORT_WEB=41000\n") || !strings.Contains(string(b), "CONDUCTOR_PREVIEW_URL=http://127.0.0.1:41000/\n") {
		t.Fatalf("the service's environment: %q %v", b, err)
	}
	svc.Stop(context.Background())
	// A start bound no start meets: the service is ended and Serve says so.
	var p Process
	svc, err = Serve(context.Background(), Spec{Argv: []string{"sleep", "300"}, Dir: dir, Timeout: time.Nanosecond, Started: func(got Process) { p = got }})
	if err == nil || svc != nil || !strings.Contains(err.Error(), "did not start within") {
		t.Fatalf("a start past its bound: %v %v", svc, err)
	}
	if p.PID == 0 || !gone(t, p.PID, 3*time.Second) {
		t.Fatalf("the service %d outlived a start past its bound", p.PID)
	}
}

// Where no service runner is (off unix: a service is stopped through its
// process group), Serve refuses before anything starts. proc does not build
// off unix (internal/pty does not), so the refusal is tested through the
// seam Serve consults, made the other platforms' (run_other.go).
func TestServeIsRefusedWhereNoServiceRunnerIs(t *testing.T) {
	old := noServiceRunner
	noServiceRunner = func() error { return errNoServiceRunner }
	t.Cleanup(func() { noServiceRunner = old })
	started := false
	svc, err := Serve(context.Background(), Spec{Argv: []string{"sleep", "300"}, Dir: t.TempDir(), Started: func(Process) { started = true }})
	if err == nil || svc != nil || err.Error() != "proc: no service runner on this platform" || started {
		t.Fatalf("%v %v, started %v", svc, err, started)
	}
}

// A process that left the service's group is not the service's: its stop
// leaves it alone, as Run does (TestRunLeavesAloneAChildThatLeftTheGroup).
func TestAServicesStopLeavesAloneWhatLeftTheGroup(t *testing.T) {
	shortGrace(t, time.Hour)
	dir := t.TempDir()
	svc := serve(t, dir, `"$0" -test.run='^TestHelperLeavesTheGroup$' >/dev/null 2>&1 & echo $! > child; echo $$ > pid; wait`, func(s *Spec) {
		s.Argv = append(s.Argv, os.Args[0])
		s.Env = map[string]string{"PROC_HELPER": "setsid"}
	})
	pidFile(t, filepath.Join(dir, "pid"))
	child := pidFile(t, filepath.Join(dir, "child"))
	// The helper writes "ready" once it has left the group.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper did not leave the group")
		}
		time.Sleep(10 * time.Millisecond)
	}
	svc.Stop(context.Background())
	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("the child that left the group was ended with the service: %v", err)
	}
	syscall.Kill(child, syscall.SIGKILL)
}
