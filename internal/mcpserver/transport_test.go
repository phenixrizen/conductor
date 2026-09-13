package mcpserver

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestFrameBoundsDepthAndDuplicateKeys(t *testing.T) {
	for _, frame := range []string{strings.Repeat("x", MaxFrameBytes+1), `{"jsonrpc":"2.0","id":1,"id":2,"method":"ping"}`, strings.Repeat("[", MaxJSONDepth+2) + strings.Repeat("]", MaxJSONDepth+2), `{} {}`} {
		t.Run(frame[:min(20, len(frame))], func(t *testing.T) {
			r, w := io.Pipe()
			out, writer := io.Pipe()
			defer out.Close()
			transport := &Transport{Reader: r, Writer: writer}
			connection, err := transport.Connect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			go func() { _, _ = io.WriteString(w, frame+"\n"); _ = w.Close() }()
			if _, err := connection.Read(context.Background()); err == nil {
				t.Fatal("accepted excessive or ambiguous frame")
			}
		})
	}
	if err := validateJSON([]byte(`{"unknownExtension":{"value":[true,null,123,"text"]}}`)); err != nil {
		t.Fatal(err)
	}
}
func TestCloseUnblocksStdio(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	out, writer := io.Pipe()
	defer out.Close()
	transport := &Transport{Reader: r, Writer: writer}
	connection, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := connection.Read(context.Background()); done <- err }()
	_ = connection.Close()
	_ = connection.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock read")
	}
}
