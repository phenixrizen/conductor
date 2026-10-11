//go:build unix

package session

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// returnsSoon fails t unless fn returns within a few seconds.
func returnsSoon(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s waited (on a FIFO)", what)
	}
}

// A FIFO where a folder or a file was is refused at once, never waited on
// for a writer: as the folder of a session, as a folder on a path, as the
// file a look found to be a regular one.
func TestFolderOpensWaitOnNoFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFO here:", err)
	}
	os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o600)
	returnsSoon(t, "OpenFolder", func() {
		if f, err := OpenFolder(fifo); err == nil {
			f.Close()
			t.Error("a FIFO opened as a folder")
		}
	})
	returnsSoon(t, "OpenFolderIn", func() {
		if f, err := OpenFolderIn(dir, "fifo"); err == nil {
			f.Close()
			t.Error("a FIFO opened as a folder")
		}
	})
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	returnsSoon(t, "a folder on the way", func() {
		// As stepDir opens one its look found to be a folder.
		if sub, err := r.OpenRoot(openDirOnly("fifo")); err == nil {
			sub.Close()
			t.Error("a FIFO opened as a folder")
		}
	})
	returnsSoon(t, "a file", func() {
		fi, err := r.Lstat("file")
		if err != nil {
			t.Error(err)
			return
		}
		r.Remove("file")
		syscall.Mkfifo(filepath.Join(dir, "file"), 0o600)
		if f, _, err := openFileIn(r, "file", fi); err == nil {
			f.Close()
			t.Error("a FIFO put in the place of a file was opened")
		}
	})
}
