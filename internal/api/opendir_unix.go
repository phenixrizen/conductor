//go:build unix

package api

import (
	"os"
	"syscall"
)

// openDir opens dir to read its entries, and only a directory: a FIFO or a
// device put in its place since it was checked fails at once (O_DIRECTORY)
// instead of waiting for a writer.
func openDir(dir string) (*os.File, error) {
	return os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY, 0)
}
