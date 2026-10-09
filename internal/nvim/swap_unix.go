//go:build unix

package nvim

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processRuns reports whether a process pid exists on this machine and is
// not a zombie (a dead process its parent has not waited for yet, whose
// swap file is as stale as a gone one's).
func processRuns(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	// Linux says a process's state in /proc; elsewhere the signal's answer stands.
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		// pid (comm) state …: the state follows the last ')'.
		if i := strings.LastIndexByte(string(b), ')'); i >= 0 && i+2 < len(b) && b[i+2] == 'Z' {
			return false
		}
	}
	return true
}
