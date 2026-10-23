package proc

import (
	"errors"

	"golang.org/x/sys/unix"
)

// waitid is unix.Waitid; the tests make it fail.
var waitid = unix.Waitid

// watchPID calls exited once the process has exited and before it is
// reaped: waitid with WNOWAIT returns then and leaves the process to be
// reaped. An error from waitid (ENOSYS under a seccomp filter, ECHILD for
// a process that is not a child to wait for) is given to exited as it is:
// it says the exit was not seen, not that it happened.
func watchPID(pid int, exited func(err error)) (stop func(), err error) {
	done := make(chan struct{})
	go func() {
		var info unix.Siginfo
		var err error
		for {
			err = waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
			if err == nil || !errors.Is(err, unix.EINTR) {
				break
			}
		}
		exited(err)
		<-done
	}()
	return func() { close(done) }, nil
}
