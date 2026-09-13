package codegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/phenixrizen/conductor/internal/domain"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAdapterRequiresImmutableImageAndAbsoluteDocker(t *testing.T) {
	for _, v := range [][2]string{{"docker", "sha256:" + strings.Repeat("a", 64)}, {"/usr/bin/docker", "conductor-codegraph:latest"}} {
		if _, err := New(v[0], v[1]); err == nil {
			t.Fatal("unpinned execution accepted")
		}
	}
}
func TestCodeGraphNativeContainer(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_CODEGRAPH") != "1" {
		t.Skip("set CONDUCTOR_TEST_CODEGRAPH=1 for actual pinned native container acceptance")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(docker, os.Getenv("CONDUCTOR_CODEGRAPH_IMAGE"))
	if err != nil {
		t.Fatal(err)
	}
	source := "package fixture\nfunc Called(){}\nfunc Caller(){Called()}\n"
	hash := sha256.Sum256([]byte(source))
	artifact := domain.ContextArtifact{Path: "fixture.go", State: "collected", Text: &source, BlobOID: strings.Repeat("a", 40), Digest: hex.EncodeToString(hash[:])}
	got, err := adapter.Index(context.Background(), []domain.ContextArtifact{artifact})
	if err != nil {
		t.Fatal(err)
	}
	if got.NativeFiles != 1 || got.KernelVersion == "" || len(got.Nodes) < 2 {
		t.Fatalf("native extraction absent: %+v", got)
	}
	found := false
	for _, edge := range got.Edges {
		if edge.Kind == "calls" {
			found = true
		}
	}
	if !found {
		t.Fatalf("real CodeGraph call relation absent: %+v", got)
	}
}
