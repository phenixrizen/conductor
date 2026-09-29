package hostagent

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// logBuffer collects what the host logs while the test reads it.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// hostProbe hosts a command that writes its arguments and AIDER_NOTIFICATIONS
// to a file, with adapter selecting the hooks, and returns what it wrote and
// the command the server lists for the session. What the host logs goes to
// logs when it is not nil.
func hostProbe(t *testing.T, adapter, hooks string, logs ...io.Writer) (string, []string) {
	t.Helper()
	logOut := io.Discard
	if len(logs) > 0 {
		logOut = logs[0]
	}
	srv, hs := startServer(t)
	out := filepath.Join(t.TempDir(), "probe")
	t.Setenv("PROBE_OUT", out)
	t.Setenv("AIDER_NOTIFICATIONS", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registered := make(chan string, 1)
	done := make(chan struct{})
	argv := []string{"/bin/sh", "-c", `printf 'ARGS[%s] N=[%s]' "$*" "$AIDER_NOTIFICATIONS" > "$PROBE_OUT"; exec /bin/cat`, "sh"}
	go func() {
		defer close(done)
		_, err := Run(ctx, Options{
			ServerURL: hs.URL,
			Token:     hostToken,
			Name:      "hooks " + adapter,
			HostName:  "laptop",
			Argv:      argv,
			Adapter:   adapter,
			HooksDir:  hooks,
			RelayOnly: true,
			Log:       slog.New(slog.NewTextHandler(logOut, nil)),
			Registered: func(id, _ string) {
				registered <- id
			},
		})
		if err != nil {
			t.Errorf("run: %v", err)
		}
	}()
	var id string
	select {
	case id = <-registered:
	case <-time.After(10 * time.Second):
		t.Fatal("host did not register")
	}
	d, ok := srv.Registry().Get(id)
	if !ok {
		t.Fatal("hosted session not listed")
	}
	var wrote []byte
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(out); err == nil && strings.HasSuffix(string(b), "]") {
			wrote = b
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the command did not run")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("host did not stop")
	}
	return string(wrote), d.Info().Command
}

// --agent names the adapter on the host too: the host writes the hook assets
// to its hooks dir and launches the command with the adapter's flags, which
// the server lists as part of the command. The assets name the conductor on
// PATH, which is this binary through a link, as a package manager installs it.
func TestHostInjectsTheAdaptersFlags(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(bin, "conductor")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	hooks := filepath.Join(t.TempDir(), "state", "hooks")
	wrote, command := hostProbe(t, "claude", hooks)
	settings := filepath.Join(hooks, "claude.json")
	if want := "ARGS[--settings " + settings + "] N=[]"; wrote != want {
		t.Fatalf("the command saw %q, want %q", wrote, want)
	}
	if n := len(command); n < 2 || !slices.Equal(command[n-2:], []string{"--settings", settings}) {
		t.Fatalf("server lists %q", command)
	}
	b, err := os.ReadFile(settings)
	if err != nil || !strings.Contains(string(b), `"`+filepath.Join(bin, "conductor")+` notify --claude-hook"`) {
		t.Fatalf("asset: %v %s", err, b)
	}
	if fi, err := os.Stat(hooks); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("hooks dir: %v %v", fi.Mode(), err)
	}
}

func TestHostInjectsTheAdaptersEnvironment(t *testing.T) {
	wrote, _ := hostProbe(t, "aider", t.TempDir())
	if wrote != "ARGS[] N=[true]" {
		t.Fatalf("the command saw %q", wrote)
	}
}

// A label that names no adapter changes nothing and writes nothing.
func TestHostWithoutAnAdapterInjectsNothing(t *testing.T) {
	hooks := filepath.Join(t.TempDir(), "hooks")
	for _, adapter := range []string{"", "my-tool"} {
		wrote, command := hostProbe(t, adapter, hooks)
		if wrote != "ARGS[] N=[]" || len(command) != 4 {
			t.Fatalf("%q: the command saw %q, server lists %q", adapter, wrote, command)
		}
	}
	if _, err := os.Stat(hooks); !os.IsNotExist(err) {
		t.Fatalf("hooks dir: %v", err)
	}
}

// Hooks are a convenience: when the host cannot write them it says so and
// hosts the command as it is.
func TestHostRunsWithoutHooksItCannotWrite(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var logs logBuffer
	wrote, command := hostProbe(t, "claude", filepath.Join(file, "hooks"), &logs)
	if wrote != "ARGS[] N=[]" || len(command) != 4 {
		t.Fatalf("the command saw %q, server lists %q", wrote, command)
	}
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "adapter=claude") {
		t.Fatalf("host log:\n%s", out)
	}
}
