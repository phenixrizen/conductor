package runtimeworker

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"go.temporal.io/sdk/client"
)

type quietLogger struct{}

func (quietLogger) Debug(string, ...interface{}) {}
func (quietLogger) Info(string, ...interface{})  {}
func (quietLogger) Warn(string, ...interface{})  {}
func (quietLogger) Error(string, ...interface{}) {}
func TestRuntimeEvidenceRuntimeLive(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL=1 for owned Temporal CLI runtime-evidence acceptance")
	}
	binary := os.Getenv("CONDUCTOR_TEMPORAL_CLI")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("temporal")
		if err != nil {
			t.Fatal("verified Temporal CLI 1.8.3 required")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "temporal version 1.8.3 (Server 1.31.2,") {
		t.Fatal("unsupported Temporal CLI")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_, port, _ := net.SplitHostPort(address)
	listener.Close()
	cmd := exec.Command(binary, "server", "start-dev", "--ip", "127.0.0.1", "--port", port, "--headless", "--namespace", "runtime-evidence-acceptance", "--db-filename", filepath.Join(t.TempDir(), "temporal.sqlite"))
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	for {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Temporal readiness timed out")
		case <-time.After(100 * time.Millisecond):
		}
	}
	engine, err := client.DialContext(ctx, client.Options{HostPort: address, Namespace: "runtime-evidence-acceptance", Logger: quietLogger{}, ConnectionOptions: client.ConnectionOptions{MaxPayloadSize: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	target, err := contextworkflow.RuntimeTarget(ctx, engine, "runtime-evidence-acceptance", address, TaskQueue)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contextworkflow.NewBoundRuntime(engine, "runtime-evidence-acceptance", address, TaskQueue, target)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = runtime.ForWorkflow(WorkflowName)
	if err != nil {
		t.Fatal(err)
	}
	ref := contextworkflow.Reference{ID: strings.Repeat("a", 32), Binding: strings.Repeat("b", 32)}
	var calls atomic.Int32
	active, stop := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- RunWorker(active, engine, func(context.Context, contextworkflow.Reference) (contextworkflow.Result, error) {
			calls.Add(1)
			return contextworkflow.Result{ReceiptID: ref.ID, Digest: strings.Repeat("c", 64)}, nil
		})
	}()
	defer func() {
		stop()
		if err := <-workerDone; err != nil {
			t.Error(err)
		}
	}()
	observed, err := runtime.Start(ctx, WorkflowName+"/"+ref.ID, ref)
	if err != nil {
		t.Fatal(err)
	}
	for {
		result, err := runtime.Lookup(ctx, WorkflowName+"/"+ref.ID, observed.RunID, ref)
		if err != nil {
			t.Fatal(err)
		}
		if result.State == "completed" {
			if result.Result == nil || result.Result.Digest != strings.Repeat("c", 64) {
				t.Fatal("wrong receipt")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("runtime-evidence workflow timed out")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err = runtime.Start(ctx, WorkflowName+"/"+ref.ID, ref); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("completed runtime-evidence reran")
	}
	wrong := ref
	wrong.Binding = strings.Repeat("f", 32)
	if _, err = runtime.Lookup(ctx, WorkflowName+"/"+ref.ID, observed.RunID, wrong); err != contextworkflow.ErrBindingMismatch {
		t.Fatalf("binding accepted %v", err)
	}
}
