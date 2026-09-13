// Package sourcebundle creates exact-commit Git bundles with a credential-free
// Git process. The caller supplies only a trusted ephemeral loopback proxy URL.
package sourcebundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

var ErrUnavailable = errors.New("source bundle unavailable")

type bounded struct {
	bytes.Buffer
	max int
}

func (b *bounded) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, ErrUnavailable
	}
	return b.Buffer.Write(p)
}
func git(ctx context.Context, dir string, max int, args ...string) ([]byte, error) {
	base := []string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.allow=never", "-c", "protocol.http.allow=always", "-c", "protocol.file.allow=never", "-c", "credential.helper=", "-c", "http.followRedirects=false", "-c", "http.proxy=", "-c", "fetch.fsckObjects=true", "-c", "transfer.fsckObjects=true", "-c", "fetch.recurseSubmodules=false", "-c", "maintenance.auto=false", "-c", "gc.auto=0"}
	boundedArgs := append([]string{"--as=1073741824", "--fsize=67108864", "--cpu=35", "--", "/usr/bin/git"}, append(base, args...)...)
	cmd := exec.CommandContext(ctx, "/usr/bin/prlimit", boundedArgs...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_TEMPLATE_DIR=" + filepath.Join(dir, "empty"), "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"}
	cmd.Stderr = io.Discard
	out := bounded{max: max}
	cmd.Stdout = &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	return out.Bytes(), nil
}

func Fetch(ctx context.Context, sourceURL, commit string) (domain.SourceBundleData, error) {
	var result domain.SourceBundleData
	u, err := url.Parse(sourceURL)
	ip := net.ParseIP(uHostname(u))
	if err != nil || u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !domain.IsLowerHex(commit, 40) {
		return result, domain.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "conductor-source-")
	if err != nil {
		return result, ErrUnavailable
	}
	defer os.RemoveAll(dir)
	if err = os.Mkdir(filepath.Join(dir, "empty"), 0700); err != nil {
		return result, ErrUnavailable
	}
	if _, err = git(ctx, dir, 4096, "init", "--bare", "repository.git"); err != nil {
		return result, err
	}
	repo := filepath.Join(dir, "repository.git")
	// Disk growth is bounded independently of compressed HTTP response size. A
	// pack can expand while index-pack runs; cancellation kills its process group.
	done := make(chan struct{})
	defer close(done)
	go func() {
		timer := time.NewTicker(20 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-timer.C:
				var total int64
				_ = filepath.WalkDir(repo, func(p string, e os.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					if !e.IsDir() {
						if info, err := e.Info(); err == nil {
							total += info.Size()
						}
					}
					if total > 2*domain.MaxSourceBundleBytes {
						cancel()
						return filepath.SkipAll
					}
					return nil
				})
			}
		}
	}()
	if _, err = git(ctx, repo, 4096, "fetch", "--no-tags", "--no-recurse-submodules", sourceURL, commit+":refs/heads/conductor-source"); err != nil {
		return result, err
	}
	actual, err := git(ctx, repo, 128, "rev-parse", "refs/heads/conductor-source^{commit}")
	if err != nil || strings.TrimSpace(string(actual)) != commit {
		return result, ErrUnavailable
	}
	tree, err := git(ctx, repo, 128, "rev-parse", commit+"^{tree}")
	if err != nil {
		return result, err
	}
	result.Commit, result.Tree = commit, strings.TrimSpace(string(tree))
	if !domain.IsLowerHex(result.Tree, 40) {
		return result, ErrUnavailable
	}
	if _, err = git(ctx, repo, 4096, "symbolic-ref", "HEAD", "refs/heads/conductor-source"); err != nil {
		return result, err
	}
	bundlePath := filepath.Join(dir, "source.bundle")
	if _, err = git(ctx, repo, 4096, "bundle", "create", bundlePath, "refs/heads/conductor-source", "HEAD"); err != nil {
		return result, err
	}
	file, err := os.Open(bundlePath)
	if err != nil {
		return result, ErrUnavailable
	}
	result.Bundle, err = io.ReadAll(io.LimitReader(file, domain.MaxSourceBundleBytes+1))
	file.Close()
	if err != nil || len(result.Bundle) > domain.MaxSourceBundleBytes {
		return domain.SourceBundleData{}, ErrUnavailable
	}
	digest := sha256.Sum256(result.Bundle)
	result.Digest = hex.EncodeToString(digest[:])
	entries, err := git(ctx, repo, 4<<20, "ls-tree", "-r", "-z", "--long", commit)
	if err != nil {
		return domain.SourceBundleData{}, err
	}
	total := 0
	result.Artifacts = []domain.ContextArtifact{}
	for _, raw := range bytes.Split(entries, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		parts := bytes.SplitN(raw, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return domain.SourceBundleData{}, ErrUnavailable
		}
		fields := strings.Fields(string(parts[0]))
		if len(fields) != 4 {
			return domain.SourceBundleData{}, ErrUnavailable
		}
		p := string(parts[1])
		result.FileCount++
		if len(result.Artifacts) >= domain.MaxSourceFiles {
			result.Truncated = true
			continue
		}
		if domain.ValidateContextPath(p) != nil {
			result.Truncated = true
			continue
		}
		artifact := domain.ContextArtifact{Path: p, BlobOID: fields[2]}
		reserved := false
		for _, part := range strings.Split(p, "/") {
			if part == ".git" || part == ".codegraph" {
				reserved = true
			}
		}
		if reserved {
			artifact.State = "unavailable"
			artifact.Message = "Parser-internal metadata is retained in the bundle but not used as configuration."
			result.Artifacts = append(result.Artifacts, artifact)
			continue
		}
		if fields[1] != "blob" || fields[0] != "100644" && fields[0] != "100755" {
			artifact.State = "unavailable"
			artifact.Message = "Symlink/submodule or unsupported mode is retained in the bundle but not followed."
			result.Artifacts = append(result.Artifacts, artifact)
			continue
		}
		size, err := strconv.Atoi(fields[3])
		if err != nil || size < 0 {
			return domain.SourceBundleData{}, ErrUnavailable
		}
		if size > domain.MaxContextArtifactBytes || total+size > domain.MaxSourceTextBytes {
			artifact.State = "truncated"
			artifact.Message = "Source file or cumulative text bound exceeded."
			result.Truncated = true
			result.Artifacts = append(result.Artifacts, artifact)
			continue
		}
		text, err := git(ctx, repo, domain.MaxContextArtifactBytes+1, "cat-file", "blob", fields[2])
		if err != nil || len(text) != size {
			return domain.SourceBundleData{}, ErrUnavailable
		}
		if !domain.IsContextText(text) || bytes.HasPrefix(text, []byte("version https://git-lfs.github.com/spec/v1\n")) {
			artifact.State = "unavailable"
			artifact.Message = "Binary content or Git LFS pointer is retained in the bundle but not indexed."
		} else {
			value := string(text)
			artifact.Text = &value
			artifact.State = "collected"
			sum := sha256.Sum256(text)
			artifact.Digest = hex.EncodeToString(sum[:])
			total += size
		}
		result.Artifacts = append(result.Artifacts, artifact)
	}
	return result, nil
}
func uHostname(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Hostname()
}

func Validate(data domain.SourceBundleData) error {
	hash := sha256.Sum256(data.Bundle)
	if !domain.IsLowerHex(data.Commit, 40) || !domain.IsLowerHex(data.Tree, 40) || len(data.Bundle) < 1 || len(data.Bundle) > domain.MaxSourceBundleBytes || data.Digest != hex.EncodeToString(hash[:]) || len(data.Artifacts) > domain.MaxSourceFiles || data.FileCount < len(data.Artifacts) {
		return domain.ErrInvalidInput
	}
	total := 0
	seen := map[string]bool{}
	for _, a := range data.Artifacts {
		if domain.ValidateContextPath(a.Path) != nil || seen[a.Path] || !domain.IsLowerHex(a.BlobOID, 40) {
			return domain.ErrInvalidInput
		}
		seen[a.Path] = true
		if a.State == "collected" {
			if a.Text == nil || !domain.IsContextText([]byte(*a.Text)) || len(*a.Text) > domain.MaxContextArtifactBytes {
				return domain.ErrInvalidInput
			}
			hash := sha256.Sum256([]byte(*a.Text))
			if a.Digest != fmt.Sprintf("%x", hash) {
				return domain.ErrInvalidInput
			}
			total += len(*a.Text)
		} else if a.State != "unavailable" && a.State != "truncated" || a.Text != nil || a.Digest != "" || a.Message == "" {
			return domain.ErrInvalidInput
		}
	}
	if total > domain.MaxSourceTextBytes {
		return domain.ErrInvalidInput
	}
	return nil
}
