package proc

import (
	"golang.org/x/sys/unix"
)

// watchPID calls exited once the process has exited, before it is reaped:
// a pidfd becomes readable then. False when the kernel has no pidfd
// (before 5.3).
func watchPID(pid int, exited func()) (stop func(), ok bool) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, false
	}
	done := make(chan struct{})
	go func() {
		defer unix.Close(fd)
		for {
			n, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, -1)
			if n > 0 || (err != nil && err != unix.EINTR) {
				break
			}
		}
		exited()
		<-done
	}()
	return func() { close(done) }, true
}
