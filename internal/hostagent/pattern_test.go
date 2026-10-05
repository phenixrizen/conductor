package hostagent

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"testing"
	"time"
)

// A pattern given to the host marks its session needs_input once the prompt
// has sat on the last line, and the server hears of it like of a bell.
func TestHostPatternMarksTheSessionNeedsInput(t *testing.T) {
	srv, hs := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registered := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := Run(ctx, Options{
			ServerURL: hs.URL,
			Token:     hostToken,
			Name:      "pattern test",
			HostName:  "laptop",
			Argv:      []string{"/bin/sh", "-c", `printf 'Continue? (y/n) '; exec /bin/cat`},
			RelayOnly: true,
			Pattern:   regexp.MustCompile(`\(y/n\) $`),
			Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			Registered: func(id, _ string) {
				registered <- id
			},
		})
		if err != nil {
			t.Errorf("run: %v", err)
		}
	}()
	var id string
	select {
	case id = <-registered:
	case <-time.After(10 * time.Second):
		t.Fatal("host did not register")
	}
	d, ok := srv.Registry().Get(id)
	if !ok {
		t.Fatal("hosted session not listed")
	}
	deadline := time.Now().Add(5 * time.Second)
	for d.Info().Attention.State != "needs_input" {
		if time.Now().After(deadline) {
			t.Fatalf("server attention %+v, want needs_input", d.Info().Attention)
		}
		time.Sleep(20 * time.Millisecond)
	}
	att := d.Info().Attention
	if att.Source != "pattern" || att.Kind != "prompt" || att.Message != "prompt: Continue? (y/n)" {
		t.Fatalf("attention %+v", att)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("host did not stop")
	}
}
