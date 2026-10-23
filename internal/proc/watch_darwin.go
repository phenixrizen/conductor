package proc

import (
	"errors"

	"golang.org/x/sys/unix"
)

// watchPID calls exited once the process has exited and before it is
// reaped: a kqueue reports NOTE_EXIT then. A process that exited before
// the watch is set up cannot be found (ESRCH): errExited.
func watchPID(pid int, exited func(err error)) (stop func(), err error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	ev := unix.Kevent_t{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}
	if _, err := unix.Kevent(kq, []unix.Kevent_t{ev}, nil, nil); err != nil {
		unix.Close(kq)
		if errors.Is(err, unix.ESRCH) {
			return nil, errExited
		}
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer unix.Close(kq)
		out := make([]unix.Kevent_t, 1)
		var err error
		for {
			var n int
			n, err = unix.Kevent(kq, nil, out, nil)
			if n > 0 {
				err = nil
				break
			}
			if err != nil && !errors.Is(err, unix.EINTR) {
				break
			}
		}
		exited(err)
		<-done
	}()
	return func() { close(done) }, nil
}
