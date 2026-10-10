package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/phenixrizen/conductor/internal/store"
)

// The workbench token file holds a workbench token the server generated, in
// the data directory, while that server runs: for whoever cannot read its
// terminal (a service, a container). The server that keeps it holds an
// exclusive lock on the lock file beside it for as long as it runs, so
// another server started on the same data directory leaves a live token file
// alone; the kernel releases the lock when its holder exits, however it
// exits, and the next start then takes the file as stale.
const (
	workbenchTokenFile = "workbench-token"
	workbenchTokenLock = "workbench-token.lock"
)

// errTokenFileHeld says another running server keeps the token file.
var errTokenFileHeld = errors.New("another server running on this data directory keeps the workbench token file")

// lockTokenFile opens the lock file in dir (made with mode 0600 when missing,
// never through a symbolic link) and takes its exclusive lock without
// waiting. It returns errTokenFileHeld when another server holds the lock.
func lockTokenFile(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, workbenchTokenLock), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errTokenFileHeld
		}
		return nil, err
	}
	return f, nil
}

// keepWorkbenchToken writes tok to the token file as the store writes its
// documents (a temp file of mode 0600, renamed into place), under the lock,
// and returns what removes the file and releases the lock when the server
// stops: the file this run wrote, not one put there since. errTokenFileHeld:
// another running server keeps the file, which is left as it is.
func keepWorkbenchToken(st *store.Store, tok string) (forget func(), err error) {
	lock, err := lockTokenFile(st.Dir())
	if err != nil {
		return nil, err
	}
	path := filepath.Join(st.Dir(), workbenchTokenFile)
	if err := st.WriteFile(workbenchTokenFile, []byte(tok+"\n")); err != nil {
		lock.Close()
		return nil, err
	}
	mine, err := os.Lstat(path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	return func() {
		if now, err := os.Lstat(path); err == nil && os.SameFile(mine, now) {
			_ = st.RemoveFile(workbenchTokenFile)
		}
		lock.Close()
	}, nil
}

// dropStaleWorkbenchToken removes the token file an earlier run left, for a
// server whose token is configured, under the lock (made when missing), so a
// server that generates its token meanwhile is never undone. With no token
// file there is nothing to remove and no lock is taken. errTokenFileHeld:
// another running server keeps the file, and it stays.
func dropStaleWorkbenchToken(st *store.Store) error {
	if _, err := os.Lstat(filepath.Join(st.Dir(), workbenchTokenFile)); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	lock, err := lockTokenFile(st.Dir())
	if err != nil {
		return err
	}
	defer lock.Close()
	return st.RemoveFile(workbenchTokenFile)
}
