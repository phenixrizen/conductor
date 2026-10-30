package proc

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ServeGrace is how long Service.Stop gives a service's group to end after
// SIGTERM before it sends SIGKILL.
const ServeGrace = 5 * time.Second

// serveGrace is ServeGrace, which a test shortens.
var serveGrace = ServeGrace

// errNoServiceRunner is Serve's refusal on a platform without a process
// group to stop a service through (noServiceRunner).
var errNoServiceRunner = errors.New("proc: no service runner on this platform")

// Service is a command Serve started: it runs until it exits on its own or
// is stopped (Stop), or until the context Serve was given ends, which kills
// its group. A project manifest's services are its callers: a dev server, a
// database, what a tree needs running beside it.
type Service struct {
	l    *launched
	done chan struct{}
	// res is what became of the service, written before done is closed.
	res Result
}

// Serve starts s's command as Run does (a process group of its own, the
// same environment, stdin from nothing, the output's head and tail and
// the optional log, Started once the process exists, the exit watched
// without reaping) and returns once the process exists. Done is closed
// when it has exited and its end is read: what it left in its group killed
// with it, as Run kills it at an exit, and the process reaped. ctx bounds
// its life: its end kills the group, as Run's caller's context does. s's
// Timeout is the start's, not a lifetime (a service lives until Stop): when
// given, a start that took longer (the process made and Started returned)
// ends the service and is an error. A command that could not start is an
// error, with nothing left running.
func Serve(ctx context.Context, s Spec) (*Service, error) {
	if err := noServiceRunner(); err != nil {
		return nil, err
	}
	head, tail, logLimit, why := s.bounds(true)
	if why != "" {
		return nil, errors.New(why)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("proc: %w", err)
	}
	began := time.Now()
	l, res, ok := launch(ctx, s, head, tail, logLimit)
	if !ok {
		return nil, errors.New(res.Err)
	}
	svc := &Service{l: l, done: make(chan struct{})}
	go func() {
		svc.res = l.end(ctx)
		close(svc.done)
	}()
	if s.Timeout > 0 && time.Since(began) > s.Timeout {
		l.g.signal(l.cmd, sigKill)
		<-svc.done
		return nil, fmt.Errorf("proc: the service did not start within %s", s.Timeout)
	}
	return svc, nil
}

// Done is closed once the service has exited and its end is read (Result).
func (s *Service) Done() <-chan struct{} { return s.done }

// Result is what became of the service once Done is closed: its process,
// its exit or the signal that ended it, its output's head and tail, how
// long it ran. Before, it is its process alone, with Exit -1.
func (s *Service) Result() Result {
	select {
	case <-s.done:
		return s.res
	default:
		return Result{Exit: -1, Process: s.l.res.Process, Started: s.l.res.Started}
	}
}

// Stop ends the service: SIGTERM to its group (and to the process, should
// it have joined another), then SIGKILL once ServeGrace has passed or ctx
// has ended, whichever comes first. It returns once the process is reaped
// and its output pipe closed or given up on (Done), as Run returns at an
// exit, so nothing of the group is running when it returns but what left
// the group. A service that has ended already returns at once; Stop may
// be called more than once, and from more than one goroutine.
func (s *Service) Stop(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	default:
	}
	s.l.g.signal(s.l.cmd, sigTerm)
	grace := time.NewTimer(serveGrace)
	defer grace.Stop()
	select {
	case <-s.done:
		return nil
	case <-grace.C:
	case <-ctx.Done():
	}
	s.l.g.signal(s.l.cmd, sigKill)
	<-s.done
	return nil
}
