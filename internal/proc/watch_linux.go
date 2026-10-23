package proc

import (
	"errors"

	"golang.org/x/sys/unix"
)

// watchPID calls exited once the process has exited and before it is
// reaped: waitid with WNOWAIT returns then and leaves the process to be
// reaped. ECHILD says the process is not a child to wait for: it has
// exited and been reaped, or was never ours.
func watchPID(pid int, exited func()) (stop func(), err error) {
	done := make(chan struct{})
	go func() {
		var info unix.Siginfo
		for {
			err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
			if err == nil || !errors.Is(err, unix.EINTR) {
				break
			}
		}
		exited()
		<-done
	}()
	return func() { close(done) }, nil
}
