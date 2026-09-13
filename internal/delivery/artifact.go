// Package delivery publishes exact verified artifacts through trusted provider
// activities. Coding workers and repository-controlled commands never receive its
// publication credentials.
package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

const MaxChangedFiles = 128
const MaxFileBytes = 8 << 20
const MaxArtifactBytes = 32 << 20

var ErrArtifact = errors.New("publication artifact failed exact-source verification")

type FileChange struct {
	Path, OldMode, Mode, Blob string
	Content                   []byte
}
type Prepared struct {
	BaseCommit, BaseTree, ResultTree, PatchDigest string
	Files                                         []FileChange
}

// Prepare applies a cumulative patch to the immutable original Git bundle using
// a bare object database and index. It never checks out source, runs hooks,
// launches repository commands or accesses the network. Provider credentials are
// deliberately absent from this interface and the Git subprocess environment.
func Prepare(ctx context.Context, bundle []byte, patch execution.Patch) (Prepared, error) {
	var out Prepared
	if len(bundle) == 0 || len(bundle) > MaxArtifactBytes || len(patch.Patch) == 0 || len(patch.Patch) > execution.MaxPatchBytes || execution.Sum(patch.Patch) != patch.Digest || !domain.IsLowerHex(patch.BaseCommit, 40) || !domain.IsLowerHex(patch.BaseTree, 40) || !domain.IsLowerHex(patch.ResultTree, 40) || len(patch.Paths) < 1 || len(patch.Paths) > MaxChangedFiles {
		return out, ErrArtifact
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "conductor-publish-")
	if err != nil {
		return out, ErrArtifact
	}
	defer os.RemoveAll(root)
	if err = os.WriteFile(filepath.Join(root, "source.bundle"), bundle, 0600); err != nil {
		return out, ErrArtifact
	}
	run := func(dir string, input []byte, args ...string) ([]byte, error) {
		return git(ctx, root, dir, input, args...)
	}
	if _, err = run(root, nil, "clone", "--bare", "--no-local", "--", filepath.Join(root, "source.bundle"), filepath.Join(root, "objects.git")); err != nil {
		return out, err
	}
	repo := filepath.Join(root, "objects.git")
	tree, err := run(repo, nil, "rev-parse", "--verify", "--end-of-options", patch.BaseCommit+"^{tree}")
	if err != nil || strings.TrimSpace(string(tree)) != patch.BaseTree {
		return out, ErrArtifact
	}
	if _, err = run(repo, nil, "read-tree", patch.BaseCommit); err != nil {
		return out, err
	}
	if _, err = run(repo, patch.Patch, "apply", "--cached", "--binary", "--whitespace=nowarn", "-"); err != nil {
		return out, err
	}
	tree, err = run(repo, nil, "write-tree")
	if err != nil || strings.TrimSpace(string(tree)) != patch.ResultTree {
		return out, ErrArtifact
	}
	raw, err := run(repo, nil, "diff-tree", "-r", "--raw", "-z", "--no-commit-id", "--no-renames", "--no-ext-diff", "--full-index", patch.BaseTree, patch.ResultTree, "--")
	if err != nil {
		return out, err
	}
	fields := bytes.Split(raw, []byte{0})
	if len(fields)%2 != 1 {
		return out, ErrArtifact
	}
	paths := []string{}
	total := 0
	for i := 0; i < len(fields)-1; i += 2 {
		metadata := strings.Fields(string(fields[i]))
		path := string(fields[i+1])
		if len(metadata) != 5 || !safePath(path) || len(out.Files) >= MaxChangedFiles {
			return out, ErrArtifact
		}
		oldMode, mode := strings.TrimPrefix(metadata[0], ":"), metadata[1]
		if !fileMode(oldMode) || !fileMode(mode) || !domain.IsLowerHex(metadata[2], 40) || !domain.IsLowerHex(metadata[3], 40) {
			return out, ErrArtifact
		}
		change := FileChange{Path: path, OldMode: oldMode, Mode: mode, Blob: metadata[3]}
		if mode != "000000" {
			change.Content, err = run(repo, nil, "cat-file", "blob", change.Blob)
			if err != nil || len(change.Content) > MaxFileBytes {
				return out, ErrArtifact
			}
			total += len(change.Content)
			if total > MaxArtifactBytes {
				return out, ErrArtifact
			}
		}
		out.Files = append(out.Files, change)
		paths = append(paths, path)
	}
	expected := append([]string(nil), patch.Paths...)
	sort.Strings(expected)
	sort.Strings(paths)
	if len(expected) != len(paths) {
		return out, ErrArtifact
	}
	for i, path := range paths {
		if expected[i] != path {
			return out, ErrArtifact
		}
	}
	out.BaseCommit, out.BaseTree, out.ResultTree, out.PatchDigest = patch.BaseCommit, patch.BaseTree, patch.ResultTree, patch.Digest
	return out, nil
}
func safePath(path string) bool {
	if path == "" || len(path) > 1024 || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\\x00\r\n\t") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}
func fileMode(mode string) bool { return mode == "100644" || mode == "100755" || mode == "000000" }

type boundedBuffer struct {
	mu       sync.Mutex
	data     bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.data.Len()+len(p) > b.limit {
		b.exceeded = true
		return 0, ErrArtifact
	}
	return b.data.Write(p)
}
func git(ctx context.Context, root, dir string, input []byte, args ...string) ([]byte, error) {
	argv := append([]string{"--no-pager", "--literal-pathspecs", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.attributesFile=/dev/null", "-c", "protocol.allow=never", "-c", "protocol.file.allow=always", "-c", "diff.external="}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "XDG_CONFIG_HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_LFS_SKIP_SMUDGE=1", "LC_ALL=C"}
	cmd.Stdin = bytes.NewReader(input)
	stdout := &boundedBuffer{limit: MaxFileBytes + 1}
	stderr := &boundedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil || stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("%w: bounded offline Git operation failed", ErrArtifact)
	}
	return append([]byte(nil), stdout.data.Bytes()...), nil
}

var _ io.Writer = (*boundedBuffer)(nil)
