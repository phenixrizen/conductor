package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExpiredAttemptCannotReadCredentialsOrStartDocker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Runner{Image: "sha256:" + strings.Repeat("a", 64), Profile: Profile{Adapter: "codex/0.154.0", Model: "synthetic"}, AllowProviderNetwork: true, CredentialFile: "/does-not-exist/synthetic-model-token", DockerBinary: "/does-not-exist/docker"}
	if _, err := r.Run(ctx, validRequest()); !errors.Is(err, context.Canceled) {
		t.Fatal("expired attempt reached credential/Docker I/O", err)
	}
}
