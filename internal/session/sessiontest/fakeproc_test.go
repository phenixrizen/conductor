package sessiontest

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/phenixrizen/conductor/internal/session"
)

var _ session.Process = (*FakeProc)(nil)

// quiet keeps the sessions' logs out of the test output.
var quiet = session.Options{Log: slog.New(slog.DiscardHandler)}

// A session over a FakeProc sees what the test prints, the test sees what the
// session types, and the session ends with the process.
func TestFakeProcRunsASession(t *testing.T) {
	p := NewFakeProc()
	s := session.NewLocal(session.Info{ID: "fake", Cols: 80, Rows: 24}, p, quiet)
	t.Cleanup(func() { p.End(0) })
	if err := p.Print("ready> "); err != nil {
		t.Fatal(err)
	}
	if err := s.Type("go\r", "crew"); err != nil {
		t.Fatal(err)
	}
	if got := string(<-p.Input); got != "go\r" {
		t.Fatalf("typed %q", got)
	}
	p.End(3)
	<-s.Ended()
	if info := s.Info(); info.Status != session.StatusExited || info.ExitCode == nil || *info.ExitCode != 3 {
		t.Fatalf("ended as %+v", info)
	}
	if err := p.Print("late"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("print after the end: %v", err)
	}
	if _, err := p.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) || p.Stopped() {
		t.Fatalf("write after the end: %v, stopped %v", err, p.Stopped())
	}
}

// Stop ends the process with -1 and says so; a later End changes nothing.
func TestFakeProcStop(t *testing.T) {
	p := NewFakeProc()
	s := session.NewLocal(session.Info{ID: "fake", Cols: 80, Rows: 24}, p, quiet)
	if err := s.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	p.End(5)
	if info := s.Info(); info.Status != session.StatusStopped || !p.Stopped() || p.Exit().Code != -1 {
		t.Fatalf("stopped as %+v, stopped %v, exit %+v", info, p.Stopped(), p.Exit())
	}
}
