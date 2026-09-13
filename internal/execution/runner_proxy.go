package execution

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

func (r Runner) docker(ctx context.Context, args ...string) ([]byte, error) {
	name := r.DockerBinary
	if name == "" {
		name = "docker"
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	cmd.Env = environment()
	buf := &limitedOutput{cancel: cancel, limit: 16384}
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: trusted Docker operation failed", ErrUnavailable)
	}
	return buf.data.Bytes(), nil
}

func isolatedNetworkArgs(name string) []string {
	return []string{"network", "create", "--driver", "bridge", "--internal", "--opt", "com.docker.network.bridge.gateway_mode_ipv4=isolated", "--label", "conductor.execution=temporary", name}
}

func (r Runner) startProxy(ctx context.Context, key string, seconds int) (network, endpoint, token string, cleanup func(), err error) {
	var identity [32]byte
	if _, err = rand.Read(identity[:]); err != nil {
		return "", "", "", nil, err
	}
	return r.startProxyForAttempt(ctx, key, seconds, hex.EncodeToString(identity[:]))
}

func (r Runner) startProxyForAttempt(ctx context.Context, key string, seconds int, inputDigest string) (network, endpoint, token string, cleanup func(), err error) {
	if !digest.MatchString(inputDigest) {
		return "", "", "", nil, ErrInvalid
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", "", "", nil, err
	}
	token = hex.EncodeToString(random[:])
	network = "conductor-isolated-" + inputDigest[:32]
	name := "conductor-proxy-" + inputDigest[:32]
	var process *exec.Cmd
	ownedNetwork := network
	release := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = r.docker(cleanupCtx, "rm", "--force", name)
		if process != nil {
			_ = process.Wait()
		}
		_, _ = r.docker(cleanupCtx, "network", "rm", ownedNetwork)
	}
	cleanup = release
	ok := false
	defer func() {
		if !ok {
			release()
		}
	}()
	if _, err = r.docker(ctx, isolatedNetworkArgs(network)...); err != nil {
		return "", "", "", nil, err
	}
	// Verify Docker retained the requested mode instead of silently accepting an
	// unknown network option. Never fall back to a bridge with a host gateway.
	b, err := r.docker(ctx, "network", "inspect", network, "--format", `{{.Internal}} {{index .Options "com.docker.network.bridge.gateway_mode_ipv4"}}`)
	if err != nil || strings.TrimSpace(string(b)) != "true isolated" {
		return "", "", "", nil, fmt.Errorf("%w: Docker isolated gateway mode required", ErrUnavailable)
	}
	args := dockerArgs(name, r.Image, false)
	args[0] = "create"
	for i := range args {
		if args[i] == "--network" {
			args[i+1] = network
		}
	}
	args = append(args, "provider-proxy")
	// The proxy never forwards IP packets between its isolated and external NICs.
	args = append(args[:1], append([]string{"--sysctl", "net.ipv4.ip_forward=0"}, args[1:]...)...)
	if _, err = r.docker(ctx, args...); err != nil {
		return "", "", "", nil, err
	}
	if _, err = r.docker(ctx, "network", "connect", "bridge", name); err != nil {
		return "", "", "", nil, err
	}
	config, _ := json.Marshal(proxyConfig{Adapter: r.Profile.Adapter, Token: token, Key: key, Seconds: seconds})
	docker := r.DockerBinary
	if docker == "" {
		docker = "docker"
	}
	process = exec.CommandContext(ctx, docker, "start", "--attach", "--interactive", name)
	process.Env = environment()
	process.Stdin = bytes.NewReader(config)
	process.WaitDelay = time.Second
	if err = process.Start(); err != nil {
		process = nil
		return "", "", "", nil, fmt.Errorf("%w: provider proxy start", ErrUnavailable)
	}
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		if _, readyErr := r.docker(readyCtx, "exec", name, "/usr/local/bin/conductor-sandbox", "proxy-ready"); readyErr == nil {
			break
		}
		select {
		case <-readyCtx.Done():
			return "", "", "", nil, fmt.Errorf("%w: provider proxy readiness", ErrUnavailable)
		case <-time.After(100 * time.Millisecond):
		}
	}
	b, err = r.docker(ctx, "inspect", name, "--format", `{{(index .NetworkSettings.Networks "`+network+`").IPAddress}}`)
	ip := strings.TrimSpace(string(b))
	if err != nil || net.ParseIP(ip) == nil {
		return "", "", "", nil, fmt.Errorf("%w: proxy network identity", ErrUnavailable)
	}
	ok = true
	return network, "http://" + net.JoinHostPort(ip, "8787"), token, cleanup, nil
}
