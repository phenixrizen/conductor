// Package codegraph runs the pinned upstream parser in a disposable container.
// Only explicit source bytes enter; provider/API/database credentials do not.
package codegraph

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

const MaxInputBytes = 4 << 20

var ErrUnavailable = errors.New("CodeGraph indexing is unavailable")

type Adapter struct{ docker, image string }

func New(docker, image string) (*Adapter, error) {
	if !filepath.IsAbs(docker) || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(image) {
		return nil, domain.ErrInvalidInput
	}
	return &Adapter{docker: docker, image: image}, nil
}

type boundedBuffer struct {
	bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, ErrUnavailable
	}
	return b.Buffer.Write(p)
}

// Index accepts bounded regular-file artifacts independently of the 32-path
// collector limit, including trusted full-repository acquisition output.
// It never mounts a repository, host home, socket, or credential into the sandbox.
func (a *Adapter) Index(parent context.Context, artifacts []domain.ContextArtifact) (domain.CodeGraphIndex, error) {
	var index domain.CodeGraphIndex
	files := []map[string]string{}
	seen := map[string]bool{}
	total := 0
	for _, f := range artifacts {
		if f.State != "collected" {
			continue
		}
		if f.Text == nil || domain.ValidateContextPath(f.Path) != nil || seen[f.Path] || len(*f.Text) > domain.MaxContextArtifactBytes || !domain.IsContextText([]byte(*f.Text)) {
			return index, domain.ErrInvalidInput
		}
		seen[f.Path] = true
		total += len(*f.Text)
		if total > MaxInputBytes || len(files) >= 512 {
			return index, domain.ErrInvalidInput
		}
		files = append(files, map[string]string{"path": f.Path, "text": *f.Text})
	}
	input, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		return index, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 75*time.Second)
	defer cancel()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return index, ErrUnavailable
	}
	name := "conductor-codegraph-" + hex.EncodeToString(random[:])
	// The immutable image ID prevents a mutable tag from replacing the reviewed
	// adapter. Pull is forbidden at execution; installation is an operator action.
	cmd := exec.CommandContext(ctx, a.docker, "run", "--rm", "--pull=never", "--name", name, "--network=none", "--read-only", "--cap-drop=ALL", "--pids-limit=96", "--memory=1g", "--memory-swap=1g", "--cpus=1", "--tmpfs=/work:rw,nosuid,nodev,noexec,size=128m,uid=10001,gid=10001", "--tmpfs=/tmp:rw,nosuid,nodev,noexec,size=32m,uid=10001,gid=10001", "-i", a.image)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stderr = io.Discard
	output := boundedBuffer{max: 4 << 20}
	cmd.Stdout = &output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	// Killing a Docker client alone does not stop its container. Own the random
	// name and remove only that container on every outcome, including cancellation.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		remove := exec.CommandContext(cleanup, a.docker, "rm", "-f", name)
		remove.Env = cmd.Env
		remove.Stdout = io.Discard
		remove.Stderr = io.Discard
		_ = remove.Run()
	}()
	if err = cmd.Run(); err != nil {
		if parent.Err() != nil {
			return index, parent.Err()
		}
		return index, ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&index) != nil || domain.ValidateCodeGraphIndex(index, artifacts) != nil {
		return domain.CodeGraphIndex{}, ErrUnavailable
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return domain.CodeGraphIndex{}, ErrUnavailable
	}
	return index, nil
}
