//go:build unix

package dispatch

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// publish renames an upload's temp file tmp to dst, its content's name
// in its shard, and makes the new name durable before anything can name
// it, since a file's fsync does not carry its directory entry (fsync(2)):
// it makes the shard directory when it is missing (0700), renames into it
// (a file already there is the same content, which the rename replaces as
// a whole), then syncs the shard directory and then the attachments'
// directory, on every upload and not only on the one that made the shard,
// as SQLite syncs a directory after making a journal in it: a shard found
// already there may be one an upload made and then failed or stopped at
// its parent's sync, in this server or in another on the same directory,
// and nothing on disk tells a durable shard from one the page cache alone
// holds (a sync of a directory nothing changed since its last costs
// little). The syncs go through syncDir. A rename that finds no temp file
// is errUploadGone: the sweep took it.
func publish(tmp, dst string, syncDir func(string) error) error {
	shard := filepath.Dir(dst)
	if err := os.Mkdir(shard, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		if _, serr := os.Lstat(tmp); errors.Is(serr, fs.ErrNotExist) {
			return errUploadGone
		}
		return err
	}
	if err := syncDir(shard); err != nil {
		return err
	}
	return syncDir(filepath.Dir(shard))
}

// syncDirectory makes the entries of the directory at path durable: an
// fsync of the directory, which Go makes F_FULLFSYNC on darwin, where a
// plain fsync does not reach the disk.
func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
