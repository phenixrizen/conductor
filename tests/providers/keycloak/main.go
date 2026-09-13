// This launcher belongs only to the owned provider qualification container.
// Snap Docker may reject executable transitions when no_new_privs is applied by
// the daemon. Apply and verify it after the trusted entry-point transition and
// before Keycloak starts; the provider still runs without privilege escalation.
package main

import (
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func main() {
	// prctl is per-thread: keep validation and exec on the same OS thread.
	runtime.LockOSThread()
	if os.Getpid() != 1 || os.Geteuid() == 0 {
		os.Exit(2)
	}
	if unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != nil {
		os.Exit(2)
	}
	if value, e := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0); e != nil || value != 1 {
		os.Exit(2)
	}
	if syscall.Exec("/opt/keycloak/bin/kc.sh", append([]string{"/opt/keycloak/bin/kc.sh"}, os.Args[1:]...), os.Environ()) != nil {
		os.Exit(2)
	}
}
