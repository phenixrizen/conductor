//go:build !unix

package agents

import "io/fs"

// fileOwner reports no owner where the system does not say who owns a file
// the Unix way.
func fileOwner(fs.FileInfo) (int, bool) { return 0, false }

// writable is false where the system cannot say: the exception it allows is never needed there.
func writable(string) bool { return false }
