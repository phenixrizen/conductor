// Package repositorycontext collects explicit, bounded text evidence from local
// Git objects. It does not interpret specifications or grant approvals.
package repositorycontext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type Freshness struct {
	State           string `json:"state"`
	InspectedCommit string `json:"inspectedCommit"`
	CurrentCommit   string `json:"currentCommit,omitempty"`
	Message         string `json:"message,omitempty"`
}

func Collect(ctx context.Context, repoPath, repositoryIdentity, ref string, paths []string) (domain.RepositoryContext, error) {
	if len(paths) == 0 || len(paths) > domain.MaxContextArtifacts {
		return domain.RepositoryContext{}, fmt.Errorf("%w: select 1 to 32 explicit paths", domain.ErrInvalidInput)
	}
	snapshot := domain.RepositoryContext{
		SchemaVersion: 1, Repository: repositoryIdentity, RequestedRef: ref,
		Collector: domain.RepositoryContextCollector, CollectedAt: time.Now().UTC(),
		Artifacts: make([]domain.ContextArtifact, 0, len(paths)),
	}
	seen := make(map[string]bool)
	for _, path := range paths {
		if err := domain.ValidateContextPath(path); err != nil {
			return domain.RepositoryContext{}, err
		}
		if seen[path] {
			return domain.RepositoryContext{}, fmt.Errorf("%w: duplicate context path", domain.ErrInvalidInput)
		}
		seen[path] = true
	}
	commit, err := resolve(ctx, repoPath, ref)
	if err != nil {
		return domain.RepositoryContext{}, fmt.Errorf("resolve repository context: %w", err)
	}
	snapshot.Commit = commit
	total := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return domain.RepositoryContext{}, err
		}
		artifact := collectArtifact(ctx, repoPath, commit, path, domain.MaxContextTotalBytes-total)
		if err := ctx.Err(); err != nil {
			return domain.RepositoryContext{}, err
		}
		if artifact.Text != nil {
			total += len(*artifact.Text)
		}
		snapshot.Artifacts = append(snapshot.Artifacts, artifact)
	}
	if err := domain.ValidateRepositoryContext(snapshot); err != nil {
		return domain.RepositoryContext{}, err
	}
	return snapshot, nil
}

// CheckFreshness observes a ref without changing the snapshot or its approvals.
func CheckFreshness(ctx context.Context, repoPath, ref string, snapshot domain.RepositoryContext) Freshness {
	result := Freshness{State: "unavailable", InspectedCommit: snapshot.Commit}
	if !domain.ValidGitOID(snapshot.Commit) {
		result.Message = "The inspected commit is invalid."
		return result
	}
	commit, err := resolve(ctx, repoPath, ref)
	if err != nil {
		result.Message = "The requested ref could not be resolved locally."
		return result
	}
	result.CurrentCommit = commit
	result.State = "current"
	if !strings.EqualFold(commit, snapshot.Commit) {
		result.State = "stale"
		result.Message = "The requested ref no longer points to the inspected commit."
	}
	return result
}

func resolve(ctx context.Context, repoPath, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" || len(ref) > 1024 || strings.HasPrefix(ref, "-") || !domain.IsContextText([]byte(ref)) || strings.ContainsAny(ref, "\r\n\t") {
		return "", fmt.Errorf("%w: a bounded non-option Git ref is required", domain.ErrInvalidInput)
	}
	b, err := git(ctx, repoPath, 128, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(b))
	if !domain.ValidGitOID(commit) {
		return "", errors.New("Git returned an invalid commit object ID")
	}
	return commit, nil
}

func collectArtifact(ctx context.Context, repoPath, commit, path string, remaining int) domain.ContextArtifact {
	artifact := domain.ContextArtifact{Path: path, State: "unavailable"}
	b, err := git(ctx, repoPath, 4096, "ls-tree", "--full-tree", "-z", commit, "--", path)
	if err != nil {
		artifact.Message = "The selected path could not be inspected locally."
		return artifact
	}
	if len(b) == 0 {
		artifact.State, artifact.Message = "missing", "The selected path is absent from the inspected commit."
		return artifact
	}
	row := bytes.TrimSuffix(b, []byte{0})
	metadata, actualPath, ok := bytes.Cut(row, []byte{'\t'})
	fields := strings.Fields(string(metadata))
	if !ok || string(actualPath) != path || len(fields) != 3 || !domain.ValidGitOID(fields[2]) {
		artifact.Message = "The selected path did not identify one exact Git entry."
		return artifact
	}
	if fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
		artifact.Message = "Only regular text files are supported; directories, symlinks and submodules are unavailable."
		return artifact
	}
	artifact.BlobOID = fields[2]
	b, err = git(ctx, repoPath, 64, "cat-file", "-s", artifact.BlobOID)
	if err != nil {
		artifact.Message = "The selected blob is unavailable locally."
		return artifact
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || size < 0 {
		artifact.Message = "Git returned an invalid blob size."
		return artifact
	}
	if size > domain.MaxContextArtifactBytes || size > int64(remaining) {
		artifact.State, artifact.Message = "truncated", "The complete file exceeds the 64 KiB file limit or 256 KiB collection limit; no partial text was retained."
		return artifact
	}
	b, err = git(ctx, repoPath, domain.MaxContextArtifactBytes, "cat-file", "blob", artifact.BlobOID)
	if err != nil || int64(len(b)) != size {
		artifact.Message = "The complete selected blob could not be read locally."
		return artifact
	}
	if !domain.IsContextText(b) {
		artifact.Message = "The selected blob is binary or contains unsupported control bytes."
		return artifact
	}
	text := string(b)
	digest := sha256.Sum256(b)
	artifact.State, artifact.Text, artifact.Digest = "collected", &text, hex.EncodeToString(digest[:])
	return artifact
}

var errOutputLimit = errors.New("Git output exceeded its size limit")

type boundedOutput struct {
	buffer bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		b.cancel()
		return 0, errOutputLimit
	}
	return b.buffer.Write(p)
}

func git(ctx context.Context, repoPath string, limit int, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	gitArgs := []string{"--no-pager", "--literal-pathspecs", "-C", repoPath, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "protocol.allow=never"}
	cmd := exec.CommandContext(commandCtx, "git", append(gitArgs, args...)...)
	// Ignore inherited Git routing/configuration. Never fetch missing objects or
	// invoke transports, credential helpers, filters, hooks or repository scripts.
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GIT_") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=")
	stdout := &boundedOutput{limit: limit, cancel: cancel}
	stderr := &boundedOutput{limit: 4096, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("read local Git objects: %w", err)
	}
	return stdout.buffer.Bytes(), nil
}
