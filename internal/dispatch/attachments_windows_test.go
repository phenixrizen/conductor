//go:build windows

package dispatch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publish on Windows (docs/plans/dispatch.md, PR-7): MoveFileEx written
// through, a file already there kept (one a download holds open cannot be
// replaced) and the temp file removed, and errUploadGone when the temp
// file is gone. syncDir is never called.

func TestPublishOnWindows(t *testing.T) {
	dir := t.TempDir()
	tmp, dst := filepath.Join(dir, "tmp-1"), filepath.Join(dir, "ab", strings.Repeat("ab", 32))
	never := func(string) error {
		t.Fatal("publish synced a directory on Windows")
		return nil
	}
	if err := os.WriteFile(tmp, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publish(tmp, dst, never); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "content" || exists(t, tmp) {
		t.Fatalf("published: %q %v, the temp file left %v", b, err, exists(t, tmp))
	}
	// The same content again, the published file held open as a download
	// holds it: kept, and the temp file goes.
	held, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := os.WriteFile(tmp, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publish(tmp, dst, never); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "content" || exists(t, tmp) {
		t.Fatalf("published again: %q %v, the temp file left %v", b, err, exists(t, tmp))
	}
	if err := publish(tmp, dst, never); !errors.Is(err, errUploadGone) {
		t.Fatalf("no temp file: %v", err)
	}
}
