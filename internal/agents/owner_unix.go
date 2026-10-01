//go:build unix

package agents

import (
	"io/fs"
	"syscall"
)

// FileOwner returns the user that owns the file fi describes. The hooks dir,
// a home Install writes to and an old data directory the server would keep
// (config.ResolveDataDir) are checked with it.
func FileOwner(fi fs.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// writable reports whether this process may create files in dir, which takes
// both write and search permission.
func writable(dir string) bool {
	return syscall.Access(dir, 0x2|0x1 /* W_OK|X_OK */) == nil
}
