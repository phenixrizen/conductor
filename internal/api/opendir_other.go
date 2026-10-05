//go:build !unix

package api

import "os"

// openDir opens dir to read its entries. Without O_DIRECTORY it is os.Open.
func openDir(dir string) (*os.File, error) { return os.Open(dir) }
