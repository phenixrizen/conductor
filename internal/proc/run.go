// Package proc runs one command outside a PTY, bounded: an argv array and
// never a shell line, a directory, a timeout, the output capped at both
// ends, the process group ended when the command is. The managed-run checks
// (docs/plans/agent-harness-order.md) are its callers; a probe and git have
// runners of their own.
package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/pty"
)

// Bounds a run is held to.
const (
	// MaxTimeout bounds Spec.Timeout.
	MaxTimeout = 2 * time.Hour
	// WaitDelay is how long Wait waits for the pipes to close after the
	// process exits, or for the process to exit after its group is killed,
	// before it gives up on them: what the process left behind is then
	// killed with the group (Result.Killed).
	WaitDelay = 2 * time.Second
	// DefaultHead and DefaultTail are the bytes of output kept in memory
	// when Spec says nothing: the first DefaultHead and the last DefaultTail.
	DefaultHead = 64 << 10
	DefaultTail = 16 << 10
	// DefaultLogLimit is how much of the output Spec.Log gets before the
	// dropped line, when Spec says nothing; LogTail is how much after it.
	DefaultLogLimit = 1 << 20
	LogTail         = 256 << 10
)

// Spec is one command to run.
type Spec struct {
	// Argv is the program and its arguments, run as they are: never a
	// shell line.
	Argv []string
	// Dir is the working directory, absolute.
	Dir string
	// Allow names variables of Conductor's own environment passed on beside
	// the fixed allowlist (pty.BuildEnv): a server's env passthrough.
	Allow []string
	// Env is set for the command, after the environment BuildEnv gives.
	// A CONDUCTOR_* name, a blocked loader or a malformed name is refused:
	// the run does not start.
	Env map[string]string
	// Timeout is required: the process group is killed past it, and at
	// most MaxTimeout.
	Timeout time.Duration
	// Head and Tail are the bytes of output kept in memory, the first and
	// the last; DefaultHead and DefaultTail when zero.
	Head, Tail int
	// Log, when set, gets the output as it comes: the first LogLimit bytes
	// (DefaultLogLimit when zero), then a line saying how many bytes were
	// dropped, then the last LogTail bytes; everything, when there is less.
	Log      io.Writer
	LogLimit int64
	// Started, when set, is called once the process exists, before any of
	// its output is read: a caller records the process before it waits.
	Started func(Process)
}

// Process identifies the process a run started: its pid, its process group
// (its own, on unix) and when it started. A pid is reused once the process
// is gone; the start time tells a later process with the same pid apart.
type Process struct {
	PID       int
	Group     int
	StartedAt time.Time
}

// Result is what became of a run.
type Result struct {
	// Process is set once the process started.
	Process Process
	// Exit is the exit status; -1 when the process was ended by a signal
	// (Signal says which) or never started (Err says why).
	Exit   int
	Signal string
	// Head and Tail are the output's first and last bytes, stdout and
	// stderr together; Total is how many bytes there were, and Truncated
	// says Head and Tail do not hold them all.
	Head, Tail []byte
	Total      int64
	Truncated  bool
	// Started and Duration are when the process started and how long the
	// run took from there, the wait for its pipes included.
	Started  time.Time
	Duration time.Duration
	// TimedOut says Timeout passed; Canceled, that the caller's context
	// ended first; Killed, that something in the process group was still
	// alive after the wait and was killed then.
	TimedOut, Canceled, Killed bool
	// Err says why the run did not start, or what went wrong with it beyond
	// its exit status; "" otherwise. LogErr is the first error writing Log.
	Err, LogErr string
}

// Run runs s and reports what became of it. It returns once the process has
// exited (or was killed at the timeout or the context's end), its pipes are
// closed or given up on (WaitDelay), and what it left in its process group
// is killed.
func Run(ctx context.Context, s Spec) Result {
	res := Result{Exit: -1}
	if len(s.Argv) == 0 || s.Argv[0] == "" {
		res.Err = "proc: no command"
		return res
	}
	if s.Timeout <= 0 || s.Timeout > MaxTimeout {
		res.Err = fmt.Sprintf("proc: the timeout must be between 0 and %s", MaxTimeout)
		return res
	}
	if !filepath.IsAbs(s.Dir) {
		res.Err = "proc: the directory must be absolute"
		return res
	}
	for k := range s.Env {
		if err := checkName(k); err != nil {
			res.Err = err.Error()
			return res
		}
	}
	head, tail := s.Head, s.Tail
	if head <= 0 {
		head = DefaultHead
	}
	if tail <= 0 {
		tail = DefaultTail
	}
	logLimit := s.LogLimit
	if logLimit <= 0 {
		logLimit = DefaultLogLimit
	}

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Dir = s.Dir
	cmd.Stdin = nil
	cmd.Env = environment(s.Allow, s.Env)
	cmd.WaitDelay = WaitDelay
	mem := &capture{headMax: head, tailMax: tail}
	var w io.Writer = mem
	var lg *logCapture
	if s.Log != nil {
		lg = &logCapture{w: s.Log, limit: logLimit, tail: &capture{tailMax: LogTail}}
		w = &both{a: mem, b: lg}
	}
	cmd.Stdout, cmd.Stderr = w, w
	attr(cmd)

	started := time.Now()
	if err := cmd.Start(); err != nil {
		res.Err = err.Error()
		return res
	}
	res.Started = started
	res.Process = Process{PID: cmd.Process.Pid, Group: group(cmd), StartedAt: started}
	if s.Started != nil {
		s.Started(res.Process)
	}
	err := cmd.Wait()
	res.Duration = time.Since(started)
	switch ctx.Err() {
	case context.DeadlineExceeded:
		res.TimedOut = true
	case context.Canceled:
		res.Canceled = true
	}
	if cmd.ProcessState != nil {
		res.Exit, res.Signal = exitStatus(cmd.ProcessState)
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) && !errors.Is(err, exec.ErrWaitDelay) {
		res.Err = err.Error()
	}
	if endGroup(cmd) {
		res.Killed = true
	}
	res.Head, res.Tail, res.Total, res.Truncated = mem.bytes()
	if lg != nil {
		lg.finish()
		if lg.err != nil {
			res.LogErr = lg.err.Error()
		}
	}
	return res
}

// checkName refuses a variable name Env may not set: Conductor's own, a
// loader the sessions block too, or one that is not a name.
func checkName(k string) error {
	switch {
	case k == "" || strings.ContainsAny(k, "=\x00"):
		return fmt.Errorf("proc: %q is not a variable name", k)
	case strings.HasPrefix(k, "CONDUCTOR_"):
		return fmt.Errorf("proc: %s: a CONDUCTOR_ variable is never set for a command", k)
	case pty.Blocked(k):
		return fmt.Errorf("proc: %s is never set for a command", k)
	}
	return nil
}

// environment is the command's environment: a session's, as BuildEnv builds
// it from Conductor's own (the allowlist and allow, CONDUCTOR_* and the
// blocked loaders out), plus set, then TERM=dumb and NO_COLOR=1 and no
// COLORTERM, since nothing reads the output but a log.
func environment(allow []string, set map[string]string) []string {
	env := pty.BuildEnv(pty.ParentEnv(), allow, set, nil)
	out := env[:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "TERM", "COLORTERM", "NO_COLOR":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "NO_COLOR=1", "TERM=dumb")
}

// capture keeps the first headMax bytes written and the last tailMax of
// the rest, and counts them all. Stdout and stderr share one writer, so
// exec gives them one pipe and one reader; the lock is for a caller that
// does not.
type capture struct {
	mu               sync.Mutex
	headMax, tailMax int
	head             []byte
	tail             []byte // a ring of tailMax bytes once full
	pos, filled      int
	total            int64
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	c.total += int64(n)
	if room := c.headMax - len(c.head); room > 0 {
		k := min(room, len(p))
		c.head = append(c.head, p[:k]...)
		p = p[k:]
	}
	if len(p) == 0 || c.tailMax <= 0 {
		return n, nil
	}
	if c.tail == nil {
		c.tail = make([]byte, c.tailMax)
	}
	if len(p) >= c.tailMax {
		copy(c.tail, p[len(p)-c.tailMax:])
		c.pos, c.filled = 0, c.tailMax
		return n, nil
	}
	k := copy(c.tail[c.pos:], p)
	if k < len(p) {
		copy(c.tail, p[k:])
	}
	c.pos = (c.pos + len(p)) % c.tailMax
	c.filled = min(c.filled+len(p), c.tailMax)
	return n, nil
}

// bytes returns the head, the tail (the bytes after the head, the last
// tailMax of them), the total and whether anything between the two is lost.
func (c *capture) bytes() (head, tail []byte, total int64, truncated bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	head = append([]byte(nil), c.head...)
	if c.filled < c.tailMax {
		tail = append([]byte(nil), c.tail[:c.filled]...)
	} else {
		tail = append(append([]byte(nil), c.tail[c.pos:]...), c.tail[:c.pos]...)
	}
	return head, tail, c.total, c.total > int64(len(head)+len(tail))
}

// logCapture writes the first limit bytes to w as they come and keeps the
// last LogTail of the rest for finish, which writes the dropped line and
// them.
type logCapture struct {
	w       io.Writer
	limit   int64
	written int64
	over    int64
	tail    *capture
	err     error
}

func (l *logCapture) Write(p []byte) (int, error) {
	n := len(p)
	if room := l.limit - l.written; room > 0 {
		m := int(min(room, int64(len(p))))
		if l.err == nil {
			if _, err := l.w.Write(p[:m]); err != nil {
				l.err = err
			}
		}
		l.written += int64(m)
		p = p[m:]
	}
	if len(p) > 0 {
		l.over += int64(len(p))
		l.tail.Write(p)
	}
	return n, nil
}

func (l *logCapture) finish() {
	if l.over == 0 || l.err != nil {
		return
	}
	_, tail, _, _ := l.tail.bytes()
	if dropped := l.over - int64(len(tail)); dropped > 0 {
		if _, err := fmt.Fprintf(l.w, "\n… %d bytes dropped …\n", dropped); err != nil {
			l.err = err
			return
		}
	}
	if _, err := l.w.Write(tail); err != nil {
		l.err = err
	}
}

// both writes to a and b; a pointer of its own, so exec sees one writer
// for stdout and stderr and makes one pipe.
type both struct{ a, b io.Writer }

func (b *both) Write(p []byte) (int, error) {
	b.a.Write(p)
	return b.b.Write(p)
}
