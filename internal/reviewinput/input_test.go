package reviewinput

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStrictPreviewRetainsNormalizedInputAndKey(t *testing.T) {
	raw := `{"idempotencyKey":"preview-1","input":{"sources":[{"repositoryId":"z","collectionId":"` + strings.Repeat("a", 32) + `","digest":"` + strings.Repeat("b", 64) + `"},{"repositoryId":"a","collectionId":"` + strings.Repeat("c", 32) + `","digest":"` + strings.Repeat("d", 64) + `"}]}}`
	d, err := Decode([]byte(raw), "graph")
	if err != nil {
		t.Fatal(err)
	}
	if d.IdempotencyKey != "preview-1" || len(d.Digest) != 64 || strings.Index(string(d.Input), `"repositoryId":"a"`) > strings.Index(string(d.Input), `"repositoryId":"z"`) {
		t.Fatalf("preview changed key or failed normalization: %+v", d)
	}
	for _, bad := range []string{
		strings.Replace(raw, `"sources":`, `"sources":[],"sources":`, 1),
		strings.Replace(raw, `"repositoryId":`, `"RepositoryID":`, 1),
		strings.Replace(raw, `"idempotencyKey":`, `"idempotencyKey":"another","idempotencyKey":`, 1),
		strings.Replace(raw, `"input":`, `"unused":true,"input":`, 1),
		strings.Replace(raw, `"sources":[`, `"sources":null,"shadow":[`, 1),
		raw + `{}`, string([]byte{0xff}), strings.Repeat(" ", MaxBytes+1),
	} {
		if _, err = Decode([]byte(bad), "graph"); err == nil {
			t.Fatal("ambiguous preview accepted")
		}
	}
	changed, err := Decode([]byte(strings.Replace(raw, "preview-1", "different-key", 1)), "graph")
	if err != nil || changed.Digest == d.Digest {
		t.Fatal("preview digest did not bind idempotency key", err)
	}
	data, _ := json.Marshal(map[string]any{"idempotencyKey": d.IdempotencyKey, "input": d.Input})
	next, err := Decode(data, "graph")
	if err != nil || next.Digest != d.Digest {
		t.Fatal("repeat changed normalized preview", err)
	}
}
func TestRequestFileBoundsAndCancellation(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := Read(context.Background(), fifo, "run"); err == nil {
		t.Fatal("FIFO accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("FIFO blocked")
	}
	path := filepath.Join(dir, "large")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), path, "run"); err == nil {
		t.Fatal("large file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Read(ctx, path, "run"); err != context.Canceled {
		t.Fatal(err)
	}
}
