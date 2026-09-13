package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadTokenBoundsAndFileType(t *testing.T) {
	directory := t.TempDir()
	for _, example := range []struct{ name, value, want string }{
		{"plain", "synthetic-token", "synthetic-token"},
		{"newline", "synthetic-token\n", "synthetic-token"},
		{"windows", "synthetic-token\r\n", "synthetic-token"},
	} {
		t.Run(example.name, func(t *testing.T) {
			path := filepath.Join(directory, example.name)
			if err := os.WriteFile(path, []byte(example.value), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := readToken(path)
			if err != nil || got != example.want {
				t.Fatalf("read token: %q %v", got, err)
			}
		})
	}
	tooLarge := filepath.Join(directory, "too-large")
	if err := os.WriteFile(tooLarge, []byte(strings.Repeat("x", (16<<10)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(directory, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, tooLarge, fifo, filepath.Join(directory, "absent")} {
		done := make(chan error, 1)
		go func() { _, err := readToken(path); done <- err }()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("accepted invalid token file")
			}
		case <-time.After(time.Second):
			t.Fatal("token read blocked on a nonregular file")
		}
	}
}
