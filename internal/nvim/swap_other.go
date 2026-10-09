//go:build !unix

package nvim

// processRuns cannot tell here: the process is taken to run, so recovering
// and deleting a swap file are not offered.
func processRuns(pid int) bool { return pid > 0 }
