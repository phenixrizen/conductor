package proc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ServeGrace is how long Service.Stop gives a service's group to end after
// SIGTERM before it sends SIGKILL.
const ServeGrace = 5 * time.Second

// serveGrace is ServeGrace, which a test shortens.
var serveGrace = ServeGrace

// groupPoll is how often a stopped service's group is looked at, once its
// process has exited, for what of it is still running (settleStopped).
const groupPoll = 50 * time.Millisecond

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
	l.stop = &stopping{asked: make(chan struct{}), cut: make(chan struct{})}
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
// has ended, whichever comes first. The grace is the group's, not its first
// process's alone: one that ends on SIGTERM before what it started (a
// wrapper, a make, a Procfile runner) leaves the rest of the group its
// grace to end, their output closed and none of them left (settleStopped),
// and what is still running at its end is killed. It returns once the
// process is reaped and its output pipe closed or given up on (Done), as
// Run returns at an exit, so nothing of the group is running when it
// returns but what left the group. A service that has ended already
// returns at once; Stop may be called more than once, and from more than
// one goroutine, and the group is asked to end once, by the first: a
// second SIGTERM, come after a process's handler has put the default back
// while it cleans up, would end it before its grace.
func (s *Service) Stop(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	default:
	}
	s.l.stop.ask(func() { s.l.g.signal(s.l.cmd, sigTerm) })
	grace := time.NewTimer(serveGrace)
	defer grace.Stop()
	select {
	case <-s.done:
		return nil
	case <-grace.C:
	case <-ctx.Done():
	}
	s.l.stop.cutNow()
	s.l.g.signal(s.l.cmd, sigKill)
	<-s.done
	return nil
}

// stopping is a service's stop as its end sees it (launched.settle): asked
// is closed once a Stop has asked the group to end (SIGTERM), cut once a
// Stop's grace has passed or its context has ended, when what is left of
// the group is killed.
type stopping struct {
	asked, cut       chan struct{}
	askOnce, cutOnce sync.Once
}

// ask closes asked and then asks the group to end (term, the stop's
// SIGTERM), the first time alone: a later Stop, or one beside it, sends
// no SIGTERM of its own. asked is closed before the signal, so the end of
// a process that SIGTERM ended is read as the stop's.
func (s *stopping) ask(term func()) {
	s.askOnce.Do(func() {
		close(s.asked)
		term()
	})
}

func (s *stopping) cutNow() { s.cutOnce.Do(func() { close(s.cut) }) }

// settleStopped gives a stopped service's group the stop's grace once its
// process has exited: what is left of the group, asked to end by the
// stop's SIGTERM, is waited for until none of it is left, or until the
// stop's cut or the end of the service's context, whereupon end kills what
// is left. The output is first given holdGrace to close, as at a Run's
// exit; then the group is listed once (lifecycle.members) and the
// processes found are followed (lifecycle.still) every groupPoll and when
// the output closes, the group listed once more when the last of them is
// gone, for a process one of them started as it ended. With none of the
// group left, an output still open is held by a process that left the
// group, which end leaves alone. Where the group cannot be listed, the
// output closed is taken for the group gone. held says the output was
// still open when the wait ended.
func (l *launched) settleStopped(ctx context.Context) (held bool) {
	eof := l.eof
	// until waits for c, the output's end, the stop's cut or ctx's end, and
	// says whether the wait is over (the cut, or ctx).
	until := func(c <-chan time.Time) (over bool) {
		select {
		case <-eof:
			eof = nil
		case <-c:
		case <-l.stop.cut:
			return true
		case <-ctx.Done():
			return true
		}
		return false
	}
	hold := time.NewTimer(holdGrace)
	defer hold.Stop()
	if until(hold.C) {
		return eof != nil
	}
	members, known := l.g.members(l.cmd)
	if !known {
		if eof != nil {
			until(nil)
		}
		return eof != nil
	}
	if len(members) == 0 {
		return eof != nil
	}
	tick := time.NewTicker(groupPoll)
	defer tick.Stop()
	for len(members) > 0 {
		if until(tick.C) {
			return eof != nil
		}
		if members = l.g.still(l.cmd, members); len(members) == 0 {
			members, _ = l.g.members(l.cmd)
		}
	}
	// The last of the group gone: its output closes now, unless a process
	// that left the group holds it.
	if eof != nil {
		last := time.NewTimer(holdGrace)
		defer last.Stop()
		until(last.C)
	}
	return eof != nil
}
