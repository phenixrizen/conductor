package execution

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Runner is configured by a trusted activity worker. Image must be an immutable
// Docker image ID or digest reference; repository input cannot select the image,
// credential, executable adapter, network policy or Docker flags.
type Runner struct {
	DockerBinary         string
	Image                string
	Profile              Profile
	CredentialFile       string
	AllowProviderNetwork bool
}

func (r Runner) Run(ctx context.Context, request Request) (Result, error) {
	if err := ValidateRequest(request); err != nil {
		return Result{}, err
	}
	if err := r.Profile.Validate(); err != nil {
		return Result{}, err
	}
	if !validImage(r.Image) {
		return Result{}, fmt.Errorf("%w: an immutable Docker image digest is required", ErrInvalid)
	}
	credential := ""
	if r.Profile.Adapter != "command/v1" {
		if !r.AllowProviderNetwork {
			return Result{}, fmt.Errorf("%w: provider network is disabled", ErrUnavailable)
		}
		var err error
		credential, err = readCredential(r.CredentialFile)
		if err != nil {
			return Result{}, err
		}
	} else if r.CredentialFile != "" || r.AllowProviderNetwork {
		return Result{}, fmt.Errorf("%w: command profile must be credential-free and offline", ErrInvalid)
	}
	// One deadline covers production and every fresh verification container.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutSeconds)*time.Second)
	defer cancel()
	network := "none"
	endpoint := ""
	if credential != "" {
		var cleanup func()
		var err error
		network, endpoint, credential, cleanup, err = r.startProxy(ctx, credential, request.TimeoutSeconds)
		if err != nil {
			return Result{}, err
		}
		defer cleanup()
	}
	w := wireRequest{Stage: "produce", Request: request, Profile: r.Profile, Credential: credential, ProviderURL: endpoint}
	result, err := r.sandbox(ctx, w, network)
	if err != nil {
		return Result{}, err
	}
	if result.InputDigest != InputDigest(request) || result.Adapter != r.Profile.Adapter {
		return Result{}, fmt.Errorf("%w: sandbox input binding mismatch", ErrSandbox)
	}
	profileJSON, _ := json.Marshal(r.Profile)
	result.ProfileDigest = Sum(profileJSON)
	result.Image = r.Image
	if result.Producer.State != "passed" {
		for _, check := range request.Checks {
			result.Checks = append(result.Checks, Evidence{ID: check.ID, RepositoryID: check.RepositoryID, Argv: check.Argv, State: "unexecuted", OutputDigest: Sum(nil), SourceDigest: result.InputDigest})
		}
		return result, nil
	}
	if len(request.Checks) > 0 {
		// Provider credentials never cross into the independent verification stage.
		verification, err := r.sandbox(ctx, wireRequest{Stage: "verify", Request: request, Profile: r.Profile, Patches: result.Patches}, "none")
		if err != nil {
			return result, err
		}
		if verification.InputDigest != result.InputDigest {
			return Result{}, fmt.Errorf("%w: verification input mismatch", ErrSandbox)
		}
		result.Checks = verification.Checks
		result.FinishedAt = verification.FinishedAt
	}
	return result, nil
}

func validImage(image string) bool {
	parts := strings.Split(image, "@sha256:")
	if len(parts) == 2 && parts[0] != "" && digest.MatchString(parts[1]) && !strings.ContainsAny(parts[0], " \r\n\t") {
		return true
	}
	return strings.HasPrefix(image, "sha256:") && digest.MatchString(strings.TrimPrefix(image, "sha256:"))
}

func readCredential(name string) (string, error) {
	if !strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%w: absolute provider credential file required", ErrInvalid)
	}
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("%w: provider credential file cannot be read", ErrUnavailable)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
		return "", fmt.Errorf("%w: private bounded regular credential file required", ErrInvalid)
	}
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(b) > 8192 {
		return "", fmt.Errorf("%w: credential read", ErrUnavailable)
	}
	s := strings.TrimSpace(string(b))
	if s == "" || strings.ContainsAny(s, "\x00\r\n") {
		return "", fmt.Errorf("%w: credential format", ErrInvalid)
	}
	return s, nil
}

func dockerArgs(name, image string, network bool) []string {
	mode := "none"
	if network {
		mode = "bridge"
	}
	return []string{"run", "--rm", "--interactive", "--name", name, "--pull", "never", "--read-only", "--cap-drop", "ALL", "--pids-limit", "128", "--cpus", "2", "--memory", "1g", "--memory-swap", "1g", "--network", mode, "--user", "10001:10001", "--tmpfs", "/work:rw,nosuid,nodev,size=512m,uid=10001,gid=10001,mode=0700", "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m,uid=10001,gid=10001,mode=0700", "--env", "HOME=/tmp/home", "--entrypoint", "/usr/local/bin/conductor-sandbox", image}
}

func (r Runner) sandbox(ctx context.Context, w wireRequest, network string) (Result, error) {
	input, err := json.Marshal(w)
	if err != nil || len(input) > MaxInputBytes {
		return Result{}, fmt.Errorf("%w: serialized task exceeds byte bound", ErrInvalid)
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return Result{}, fmt.Errorf("create sandbox identity: %w", err)
	}
	name := "conductor-task-" + hex.EncodeToString(random[:])
	docker := r.DockerBinary
	if docker == "" {
		docker = "docker"
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cleanupCtx, docker, "rm", "--force", name)
		cmd.Env = environment()
		_ = cmd.Run()
	}()
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &limitedOutput{cancel: cancel, limit: MaxInputBytes}
	stderr := &limitedOutput{cancel: cancel, limit: 4096}
	args := dockerArgs(name, r.Image, false)
	for i := range args {
		if args[i] == "--network" {
			args[i+1] = network
		}
	}
	if network != "none" {
		// The gateway has a literal internal IP. Repository commands do not need
		// DNS forwarding, which would otherwise be an outbound channel of its own.
		args = append(args[:1], append([]string{"--dns", "127.0.0.1"}, args[1:]...)...)
	}
	cmd := exec.CommandContext(commandCtx, docker, args...)
	cmd.Env = environment()
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("sandbox interrupted: %w", ctx.Err())
		}
		return Result{}, fmt.Errorf("%w: container failed or output exceeded bounds; no completion is established", ErrSandbox)
	}
	var result Result
	dec := json.NewDecoder(bytes.NewReader(stdout.data.Bytes()))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&result); err != nil {
		return Result{}, fmt.Errorf("%w: malformed bounded result", ErrSandbox)
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return Result{}, fmt.Errorf("%w: extra sandbox result", ErrSandbox)
	}
	return result, nil
}
