//go:build windows

package dispatch

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// publish moves an upload's temp file tmp to dst, its content's name in
// its shard, making the shard directory when it is missing: MoveFileEx
// with MOVEFILE_WRITE_THROUGH, which returns once the move is on the disk,
// since a directory handle cannot be flushed on Windows, so syncDir is not
// called. A file already there is the same content, published before (one
// a download holds open could not be replaced), and the temp file goes. A
// move that finds no temp file is errUploadGone: the sweep took it.
func publish(tmp, dst string, _ func(string) error) error {
	if err := os.Mkdir(filepath.Dir(dst), 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
	if err == nil {
		return nil
	}
	if _, serr := os.Lstat(tmp); errors.Is(serr, fs.ErrNotExist) {
		return errUploadGone
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return os.Remove(tmp)
	}
	return &os.LinkError{Op: "movefileex", Old: tmp, New: dst, Err: err}
}

// syncDirectory does nothing on Windows: a directory handle cannot be
// flushed there, and publish writes its move through instead.
func syncDirectory(string) error { return nil }
