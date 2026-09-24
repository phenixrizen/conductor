package hostagent

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

func terminalSize(f *os.File) (uint16, uint16, error) {
	if !term.IsTerminal(int(f.Fd())) {
		return 0, 0, errors.New("not a terminal")
	}
	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0, err
	}
	if w <= 0 || h <= 0 {
		return 0, 0, errors.New("unknown terminal size")
	}
	return uint16(min(w, proto.MaxTerminalDimension)), uint16(min(h, proto.MaxTerminalDimension)), nil
}

// stdoutSink writes terminal output to the developer's own terminal.
type stdoutSink struct{ out *os.File }

func (s *stdoutSink) Transport() string { return "local" }

func (s *stdoutSink) WriteFrame(frame []byte) error {
	f, err := proto.Decode(frame)
	if err != nil {
		return nil
	}
	switch f.Type {
	case proto.TypeOutput, proto.TypeScrollback:
		_, err := s.out.Write(f.Payload)
		return err
	}
	return nil
}

func (s *stdoutSink) Close(error) {}

// attachLocal puts stdin in raw mode and connects it to the PTY as a
// controller. It returns a function that restores the terminal.
func (a *agent) attachLocal(ctx context.Context) (func(), error) {
	fd := int(a.opts.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, errors.New("stdin is not a terminal")
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	cols, rows, _ := terminalSize(a.opts.Stdin)
	sub, err := a.local.Attach("local-"+session.NewID()[:10], session.RoleControl, "", cols, rows, &stdoutSink{out: a.opts.Stdout})
	if err != nil {
		term.Restore(fd, state)
		return nil, err
	}
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-winch:
				if c, r, err := terminalSize(a.opts.Stdin); err == nil {
					_ = a.local.Resize(sub, c, r)
				}
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := a.opts.Stdin.Read(buf)
			if n > 0 {
				if ierr := a.local.Input(sub, buf[:n]); ierr != nil && errors.Is(ierr, session.ErrSessionEnded) {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return func() {
		signal.Stop(winch)
		a.local.Detach(sub)
		term.Restore(fd, state)
	}, nil
}
