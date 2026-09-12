//go:build unix

package tui

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFileDescriptorValidationRejectsFIFOWithoutWaitingForWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replaced-package.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		// Test the open itself, as if the path changed after readContent's stat.
		f, err := openContentFile(path)
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("opened a FIFO as package content")
		}
	case <-time.After(time.Second):
		t.Fatal("file open waited for a FIFO writer instead of rejecting the descriptor")
	}
}
