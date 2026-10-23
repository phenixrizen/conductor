// Package proc runs one command outside a PTY, bounded: an argv array and
// never a shell line, a directory, a timeout, the output capped at both
// ends, what the command leaves in its process group ended with it. The
// managed-run checks (docs/plans/agent-harness-order.md) are its callers; a
// probe and git have runners of their own.
package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	// WaitDelay is how long Run waits, once the process has exited, for
	// the output pipe to close: a child the process left behind holds it
	// open, and is killed with the group when the delay runs out
	// (Result.Killed). It is also how long Run waits for a stalled Log
	// writer before leaving it behind.
	WaitDelay = 2 * time.Second
	// DefaultHead and DefaultTail are the bytes of output kept in memory
	// when Spec says nothing: the first DefaultHead and the last DefaultTail.
	DefaultHead = 64 << 10
	DefaultTail = 16 << 10
	// DefaultLogLimit is how much of the output Spec.Log gets before the
	// dropped line, when Spec says nothing; LogTail is how much after it.
	DefaultLogLimit = 1 << 20
	LogTail         = 256 << 10
	// logQueue bounds the chunks of output queued for a Log writer that
	// does not keep up: past it the log is cut (Result.LogErr), never the
	// run held up.
	logQueue = 256
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
	// Log, when set, gets the output as it comes, on a goroutine of its
	// own, so a slow writer never holds the process up: the first LogLimit
	// bytes (DefaultLogLimit when zero), then a line saying how many bytes
	// were dropped, then the last LogTail bytes; everything, when there is
	// less. A writer that falls logQueue chunks behind, or does not return
	// within WaitDelay at the end, loses the rest (Result.LogErr).
	Log      io.Writer
	LogLimit int64
	// Started, when set, is called once the process exists and before any
	// of its output is read: a caller records the process before it waits.
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
	// run took from there, the wait for its pipe included.
	Started  time.Time
	Duration time.Duration
	// TimedOut says Timeout passed; Canceled, that the caller's context
	// ended first (cancelled, or its own deadline); Killed, that something
	// the process left behind still held its output pipe WaitDelay after
	// it exited, and the process group was killed then.
	TimedOut, Canceled, Killed bool
	// Err says why the run did not start, or what went wrong with it beyond
	// its exit status; "" otherwise. LogErr is the first error writing Log,
	// or why the log was cut.
	Err, LogErr string
}

// errTimeout is the cause of the run's own deadline, told apart from the
// caller's context ending (Canceled).
var errTimeout = errors.New("proc: timeout")

// Run runs s and reports what became of it. It returns once the process has
// exited (or was killed at the timeout or the context's end) and its output
// pipe is closed, or given up on after WaitDelay with the group killed; a
// Log writer still stalled then is left behind.
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
	if err := ctx.Err(); err != nil {
		res.Canceled = true
		res.Err = "proc: " + err.Error()
		return res
	}

	ctx, cancel := context.WithTimeoutCause(ctx, s.Timeout, errTimeout)
	defer cancel()
	// The output pipe is ours: the process and what it starts write to it,
	// Run reads it after Started, and its end of file says the last holder
	// is gone, which the wait after the exit relies on.
	r, w, err := os.Pipe()
	if err != nil {
		res.Err = err.Error()
		return res
	}
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Dir = s.Dir
	cmd.Stdin = nil
	cmd.Env = environment(s.Allow, s.Env)
	cmd.Stdout, cmd.Stderr = w, w
	attr(cmd)

	started := time.Now()
	if err := cmd.Start(); err != nil {
		w.Close()
		r.Close()
		res.Err = err.Error()
		classify(ctx, &res)
		return res
	}
	w.Close() // the process holds the write end now
	res.Started = started
	res.Process = Process{PID: cmd.Process.Pid, Group: group(cmd), StartedAt: started}
	if s.Started != nil {
		s.Started(res.Process)
	}

	mem := &capture{headMax: head, tailMax: tail}
	var sink io.Writer = mem
	var lg *logSink
	if s.Log != nil {
		lg = newLogSink(s.Log, logLimit)
		sink = io.MultiWriter(mem, lg)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		io.Copy(sink, r)
	}()

	err = cmd.Wait() // the process; the pipe is read on, by the goroutine
	res.Duration = time.Since(started)
	if cmd.ProcessState != nil {
		res.Exit, res.Signal = exitStatus(cmd.ProcessState)
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		res.Err = err.Error()
	}
	classify(ctx, &res)
	// The output pipe closes when its last holder is gone: the process,
	// and whatever it started that kept it. One that holds it past
	// WaitDelay is a child left behind, and the group is killed: a holder
	// of the pipe is a descendant of the process, so the group is still
	// theirs and no other's.
	select {
	case <-drained:
	case <-time.After(WaitDelay):
		killGroup(cmd)
		res.Killed = true
		r.Close() // ends the read, and the goroutine
		<-drained
	}
	r.Close()
	res.Head, res.Tail, res.Total, res.Truncated = mem.bytes()
	if lg != nil {
		res.LogErr = lg.finish(WaitDelay)
	}
	return res
}

// classify reads the context's end into TimedOut (the run's own deadline)
// or Canceled (the caller's context, however it ended).
func classify(ctx context.Context, res *Result) {
	if ctx.Err() == nil {
		return
	}
	if errors.Is(context.Cause(ctx), errTimeout) {
		res.TimedOut = true
		return
	}
	res.Canceled = true
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
// the rest, and counts them all.
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

// logSink feeds a Log writer on a goroutine of its own, through a bounded
// queue, so that a writer that blocks never holds the output pipe, the
// process or Run up: the first limit bytes go to the writer as they come,
// the rest is kept as a tail for finish. A queue that fills cuts the log.
type logSink struct {
	w     io.Writer
	limit int64
	ch    chan []byte
	done  chan struct{}

	mu      sync.Mutex
	cut     string // why the log was cut, once it was
	written int64
	over    int64
	tail    *capture
	err     error
}

func newLogSink(w io.Writer, limit int64) *logSink {
	l := &logSink{w: w, limit: limit, ch: make(chan []byte, logQueue), done: make(chan struct{}), tail: &capture{tailMax: LogTail}}
	go l.run()
	return l
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	cut := l.cut
	l.mu.Unlock()
	if cut != "" {
		return len(p), nil
	}
	chunk := append([]byte(nil), p...)
	select {
	case l.ch <- chunk:
	default:
		l.mu.Lock()
		l.cut = fmt.Sprintf("the log writer fell %d chunks behind: the log is cut", logQueue)
		l.mu.Unlock()
	}
	return len(p), nil
}

// run is the sink's goroutine: it writes the chunks within the limit and
// keeps the rest for the tail, then, once the queue closes, the dropped
// line and the tail.
func (l *logSink) run() {
	defer close(l.done)
	for p := range l.ch {
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
	}
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

// finish closes the queue and waits up to wait for the sink to write what
// is left; it reports why the log is short, "" when it is whole.
func (l *logSink) finish(wait time.Duration) string {
	close(l.ch)
	select {
	case <-l.done:
	case <-time.After(wait):
		return fmt.Sprintf("the log writer did not return within %s: the log is cut", wait)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err.Error()
	}
	return l.cut
}
