package proc

import (
	"golang.org/x/sys/unix"
)

// watchPID calls exited once the process has exited, before it is reaped:
// a kqueue reports NOTE_EXIT then.
func watchPID(pid int, exited func()) (stop func(), ok bool) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, false
	}
	ev := unix.Kevent_t{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}
	if _, err := unix.Kevent(kq, []unix.Kevent_t{ev}, nil, nil); err != nil {
		unix.Close(kq)
		return nil, false
	}
	done := make(chan struct{})
	go func() {
		defer unix.Close(kq)
		out := make([]unix.Kevent_t, 1)
		for {
			n, err := unix.Kevent(kq, nil, out, nil)
			if n > 0 || (err != nil && err != unix.EINTR) {
				break
			}
		}
		exited()
		<-done
	}()
	return func() { close(done) }, true
}
