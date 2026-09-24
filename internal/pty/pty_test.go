package pty

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

func TestStartEchoResizeStop(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("/bin/cat not available")
	}
	p, err := Start(Spec{Argv: []string{"/bin/cat"}, Env: BuildEnv(os.Environ(), nil, nil), Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	var got []byte
	deadline := time.Now().Add(5 * time.Second)
	for !bytes.Contains(got, []byte("hello")) && time.Now().Before(deadline) {
		n, err := p.Read(buf)
		if err != nil {
			t.Fatalf("read: %v (got %q)", err, got)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Contains(got, []byte("hello")) {
		t.Fatalf("echo not observed: %q", got)
	}
	if err := p.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	if err := p.Resize(0, 1); err == nil {
		t.Fatal("expected resize error")
	}
	if err := p.Stop(context.Background(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	<-p.Done()
	if st := p.Exit(); st.Signal == "" && st.Code == 0 {
		t.Fatalf("expected signalled exit, got %+v", st)
	}
}

func TestStartRejectsEmpty(t *testing.T) {
	if _, err := Start(Spec{}); err != ErrNoCommand {
		t.Fatalf("expected ErrNoCommand, got %v", err)
	}
	if _, err := Start(Spec{Argv: []string{"/bin/cat", "a\x00b"}}); err == nil {
		t.Fatal("expected NUL rejection")
	}
}

func TestNormalExit(t *testing.T) {
	p, err := Start(Spec{Argv: []string{"/bin/sh", "-c", "exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
	if p.Exit().Code != 3 {
		t.Fatalf("exit code %+v", p.Exit())
	}
	p.Close()
}
