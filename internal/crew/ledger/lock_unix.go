//go:build unix

package ledger

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ownerLock is the owner lock, held for the process's life: the file,
// locked exclusively (flock), with the owner's pid and start time written
// into it for the error messages of a server that finds it held. Nothing
// trusts those two.
type ownerLock struct{ f *os.File }

// takeOwnerLock takes the lock at path, retrying for wait while another
// process holds it. It returns no lock and the reason when the lock stays
// held, or when the filesystem does not support locks (a network or 9p
// mount): managed runs are then off for this server. An error is for
// trouble with the file itself.
func takeOwnerLock(path string, wait time.Duration) (*ownerLock, string, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, "", err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			if time.Now().Before(deadline) {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			holder, _ := os.ReadFile(path)
			f.Close()
			return nil, fmt.Sprintf("another server holds %s (%s)", path, strings.TrimSpace(string(holder))), nil
		}
		f.Close()
		if errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOLCK) || errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EINVAL) {
			return nil, fmt.Sprintf("the filesystem of %s does not support locks (%v)", path, err), nil
		}
		return nil, "", fmt.Errorf("ledger: lock %s: %w", path, err)
	}
	// The lock is ours: say who, for the next server's message.
	if err := f.Truncate(0); err == nil {
		f.WriteAt([]byte("pid "+strconv.Itoa(os.Getpid())+" since "+time.Now().UTC().Format(time.RFC3339)+"\n"), 0)
	}
	return &ownerLock{f: f}, "", nil
}

func (o *ownerLock) release() {
	syscall.Flock(int(o.f.Fd()), syscall.LOCK_UN)
	o.f.Close()
}
