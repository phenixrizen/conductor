// Package pty starts commands attached to a pseudo-terminal and manages their
// lifecycle. It is used by both `conductor serve` and `conductor host`.
package pty

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	creack "github.com/creack/pty"
)

// Spec describes the process to start.
type Spec struct {
	Argv []string
	Dir  string
	Env  []string
	Cols uint16
	Rows uint16
}

// ExitStatus describes how the process ended.
type ExitStatus struct {
	Code   int
	Signal string
	Err    error
}

// Process is a running command with its controlling PTY.
type Process struct {
	f    *os.File
	cmd  *exec.Cmd
	done chan struct{}

	mu   sync.Mutex
	exit ExitStatus
}

// ErrNoCommand is returned for an empty argv.
var ErrNoCommand = errors.New("pty: empty command")

// Start launches spec in a new session with a PTY of the requested size.
func Start(spec Spec) (*Process, error) {
	if len(spec.Argv) == 0 || strings.TrimSpace(spec.Argv[0]) == "" {
		return nil, ErrNoCommand
	}
	for _, a := range spec.Argv {
		if strings.ContainsRune(a, 0) {
			return nil, errors.New("pty: argument contains NUL")
		}
	}
	cols, rows := spec.Cols, spec.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	// creack/pty sets Setsid and Setctty so the child gets its own session and
	// the PTY as controlling terminal; the process group ID equals the PID.
	f, err := creack.StartWithSize(cmd, &creack.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, fmt.Errorf("pty: start %s: %w", spec.Argv[0], err)
	}
	p := &Process{f: f, cmd: cmd, done: make(chan struct{})}
	go p.wait()
	return p, nil
}

func (p *Process) wait() {
	err := p.cmd.Wait()
	st := ExitStatus{Code: -1}
	if err == nil {
		st.Code = 0
	} else {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
				if ws.Signaled() {
					st.Signal = ws.Signal().String()
					st.Code = 128 + int(ws.Signal())
				} else {
					st.Code = ws.ExitStatus()
				}
			} else {
				st.Code = ee.ExitCode()
			}
		} else {
			st.Err = err
		}
	}
	p.mu.Lock()
	p.exit = st
	p.mu.Unlock()
	close(p.done)
}

// Read reads PTY output. It returns an error once the process has exited and
// the kernel closed the slave side.
func (p *Process) Read(b []byte) (int, error) { return p.f.Read(b) }

// Write sends input to the process.
func (p *Process) Write(b []byte) (int, error) { return p.f.Write(b) }

// Resize changes the terminal window size and signals the process group.
func (p *Process) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return errors.New("pty: dimensions must be positive")
	}
	return creack.Setsize(p.f, &creack.Winsize{Cols: cols, Rows: rows})
}

// Done is closed when the process has exited.
func (p *Process) Done() <-chan struct{} { return p.done }

// Exit returns the exit status once Done is closed.
func (p *Process) Exit() ExitStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exit
}

// PID returns the child process ID.
func (p *Process) PID() int { return p.cmd.Process.Pid }

// Stop sends SIGTERM to the process group, waits up to grace, then SIGKILLs.
// It always closes the PTY afterwards.
func (p *Process) Stop(ctx context.Context, grace time.Duration) error {
	defer p.f.Close()
	select {
	case <-p.done:
		return nil
	default:
	}
	pgid := -p.cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
	case <-ctx.Done():
	}
	_ = syscall.Kill(pgid, syscall.SIGKILL)
	select {
	case <-p.done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("pty: process did not exit after SIGKILL")
	}
}

// Close releases the PTY without signalling the process.
func (p *Process) Close() error { return p.f.Close() }
