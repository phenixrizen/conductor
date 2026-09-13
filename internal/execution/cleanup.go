package execution

import (
	"context"
	"fmt"
	"strings"
)

// CleanupAttempt is a trusted recovery operation. The input digest comes from a
// persisted task-attempt fact, never a public arbitrary Docker resource name.
// It removes only that exact attempt's disposable resources and proves absence.
func (r Runner) CleanupAttempt(ctx context.Context, inputDigest string) (bool, error) {
	if !digest.MatchString(inputDigest) {
		return false, ErrInvalid
	}
	key := inputDigest[:32]
	for _, name := range []string{"conductor-task-" + key + "-produce", "conductor-task-" + key + "-verify", "conductor-proxy-" + key} {
		_, _ = r.docker(ctx, "rm", "--force", name)
		b, err := r.docker(ctx, "ps", "--all", "--filter", "name=^/"+name+"$", "--format", "{{.ID}}")
		if err != nil || strings.TrimSpace(string(b)) != "" {
			return false, fmt.Errorf("%w: attempt container cleanup is unconfirmed", ErrUnavailable)
		}
	}
	name := "conductor-isolated-" + key
	_, _ = r.docker(ctx, "network", "rm", name)
	b, err := r.docker(ctx, "network", "ls", "--filter", "name=^"+name+"$", "--format", "{{.ID}}")
	if err != nil || strings.TrimSpace(string(b)) != "" {
		return false, fmt.Errorf("%w: attempt network cleanup is unconfirmed", ErrUnavailable)
	}
	return true, nil
}
