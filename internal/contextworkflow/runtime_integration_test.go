package contextworkflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/temporaltest"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// This fixture owns its CLI process and SQLite file. It never connects to or
// restarts a developer's Temporal server, and an explicit opt-in fails closed.
type temporalServer struct {
	t        *testing.T
	binary   string
	dir      string
	address  string
	cmd      *exec.Cmd
	done     chan error
	log      *os.File
	restarts int
}

func newTemporalServer(t *testing.T) *temporalServer {
	t.Helper()
	binary := os.Getenv("CONDUCTOR_TEMPORAL_CLI")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("temporal")
		if err != nil {
			t.Fatal("CONDUCTOR_TEST_TEMPORAL=1 requires the verified Temporal CLI v1.8.3")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "temporal version 1.8.3 (Server 1.31.2,") {
		t.Fatalf("expected CLI v1.8.3 / server 1.31.2: version=%q err=%v", version, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	s := &temporalServer{t: t, binary: binary, dir: t.TempDir(), address: address}
	t.Cleanup(s.stop)
	s.start()
	return s
}

func (s *temporalServer) start() {
	s.t.Helper()
	_, port, err := net.SplitHostPort(s.address)
	if err != nil {
		s.t.Fatal(err)
	}
	s.log, err = os.OpenFile(filepath.Join(s.dir, fmt.Sprintf("server-%d.log", s.restarts)), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		s.t.Fatal(err)
	}
	s.restarts++
	s.cmd = exec.Command(s.binary, "server", "start-dev", "--ip", "127.0.0.1", "--port", port,
		"--headless", "--namespace", "conductor-acceptance", "--db-filename", filepath.Join(s.dir, "temporal.sqlite"))
	s.cmd.Stdout, s.cmd.Stderr = s.log, s.log
	if err := s.cmd.Start(); err != nil {
		_ = s.log.Close()
		s.cmd = nil
		s.t.Fatal(err)
	}
	s.done = make(chan error, 1)
	go func(cmd *exec.Cmd, done chan error) { done <- cmd.Wait() }(s.cmd, s.done)
	readyCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ready := make(chan error, 1)
	go func() { ready <- temporaltest.AwaitReady(readyCtx, s.address, "conductor-acceptance") }()
	select {
	case err := <-s.done:
		s.cmd = nil
		_ = s.log.Close()
		s.t.Fatalf("owned Temporal process exited before readiness: %v", err)
	case err := <-ready:
		if err == nil {
			return
		}
		s.stop()
		s.t.Fatal(err)
	}
}

func (s *temporalServer) stop() {
	if s.cmd == nil {
		return
	}
	_ = s.cmd.Process.Signal(os.Interrupt)
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.done
	}
	_ = s.log.Close()
	s.cmd = nil
}

type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLogger) record(message string, pairs ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) < 10000 {
		l.lines = append(l.lines, message+fmt.Sprint(pairs...))
	}
}
func (l *recordingLogger) Debug(m string, p ...interface{}) { l.record(m, p...) }
func (l *recordingLogger) Info(m string, p ...interface{})  { l.record(m, p...) }
func (l *recordingLogger) Warn(m string, p ...interface{})  { l.record(m, p...) }
func (l *recordingLogger) Error(m string, p ...interface{}) { l.record(m, p...) }

func (s *temporalServer) connect(logger *recordingLogger) client.Client {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := client.DialContext(ctx, client.Options{
		HostPort: s.address, Namespace: "conductor-acceptance", Logger: logger,
		Identity:          "conductor-context-acceptance",
		ConnectionOptions: client.ConnectionOptions{MaxPayloadSize: 1 << 20},
	})
	if err != nil {
		s.t.Fatal("connect owned Temporal server:", err)
	}
	return c
}

func randomReference(t *testing.T) Reference {
	t.Helper()
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		t.Fatal(err)
	}
	return Reference{ID: hex.EncodeToString(data[:16]), Binding: hex.EncodeToString(data[16:])}
}

func awaitState(t *testing.T, r *Runtime, ref Reference, want string) Observation {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		observation, err := r.Lookup(context.Background(), WorkflowName+"/"+ref.ID, "", ref)
		if err != nil {
			t.Fatal("lookup:", err)
		}
		if observation.State == want {
			return observation
		}
		if observation.State != "running" {
			t.Fatalf("expected %s, observed %s", want, observation.State)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("workflow did not reach %s", want)
	return Observation{}
}

func historyWithoutCanaries(t *testing.T, c client.Client, ref Reference, logger *recordingLogger) *historypb.History {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	iterator := c.GetWorkflowHistory(ctx, WorkflowName+"/"+ref.ID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	history := &historypb.History{}
	for iterator.HasNext() {
		if len(history.Events) >= 256 || proto.Size(history) > 1<<20 {
			t.Fatal("unexpectedly large fixture history")
		}
		event, err := iterator.Next()
		if err != nil {
			t.Fatal(err)
		}
		history.Events = append(history.Events, event)
	}
	// Walk actual protobuf bytes recursively, including payloads, details,
	// failures, headers and memo. JSON base64 display alone can hide a canary.
	var inspect func(protoreflect.Message)
	inspect = func(message protoreflect.Message) {
		message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			check := func(kind protoreflect.Kind, v protoreflect.Value) {
				switch kind {
				case protoreflect.MessageKind, protoreflect.GroupKind:
					inspect(v.Message())
				case protoreflect.BytesKind:
					if strings.Contains(string(v.Bytes()), "source-token-canary") {
						t.Fatal("private canary entered history byte field")
					}
				case protoreflect.StringKind:
					if strings.Contains(v.String(), "source-token-canary") {
						t.Fatal("private canary entered history text field")
					}
				}
			}
			if field.IsMap() {
				value.Map().Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
					check(field.MapKey().Kind(), k.Value())
					check(field.MapValue().Kind(), v)
					return true
				})
			} else if field.IsList() {
				for i := 0; i < value.List().Len(); i++ {
					check(field.Kind(), value.List().Get(i))
				}
			} else {
				check(field.Kind(), value)
			}
			return true
		})
	}
	inspect(history.ProtoReflect())
	logger.mu.Lock()
	defer logger.mu.Unlock()
	for _, line := range logger.lines {
		if strings.Contains(line, "source-token-canary") {
			t.Fatal("private canary entered SDK/worker log")
		}
	}
	return history
}

func TestTemporalRuntimeLive(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL=1 to run the owned persistent Temporal CLI v1.8.3 process")
	}
	server := newTemporalServer(t)
	logger := &recordingLogger{}
	c := server.connect(logger)
	r, err := NewRuntime(c, "conductor-acceptance", TaskQueue)
	if err != nil {
		t.Fatal(err)
	}
	ref := randomReference(t)
	first, err := r.Start(context.Background(), WorkflowName+"/"+ref.ID, ref)
	if err != nil || first.State != "running" {
		t.Fatalf("start: %#v %v", first, err)
	}
	duplicate, err := r.Start(context.Background(), WorkflowName+"/"+ref.ID, ref)
	if err != nil || duplicate.RunID != first.RunID {
		t.Fatalf("running duplicate: %#v %v", duplicate, err)
	}
	wrong := ref
	wrong.Binding = strings.Repeat("0", 32)
	if _, err := r.Start(context.Background(), WorkflowName+"/"+ref.ID, wrong); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("wrong binding: %v", err)
	}
	if err := r.Cancel(context.Background(), WorkflowName+"/"+ref.ID, first.RunID, wrong); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("wrong binding cancel: %v", err)
	}
	// Stop the actual server process with a pending workflow, then start a new
	// process on the same owned SQLite file. Reconnecting alone is insufficient.
	c.Close()
	server.stop()
	server.start()
	c = server.connect(logger)
	t.Cleanup(c.Close)
	r, err = NewRuntime(c, "conductor-acceptance", TaskQueue)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := r.Lookup(context.Background(), WorkflowName+"/"+ref.ID, first.RunID, ref)
	if err != nil || restored.RunID != first.RunID || restored.State != "running" {
		t.Fatalf("actual server restart: %#v %v", restored, err)
	}
	cancelRef, panicRef, revokedRef := randomReference(t), randomReference(t), randomReference(t)
	var calls atomic.Int32
	var writes atomic.Int32
	marker := filepath.Join(server.dir, "committed-receipt")
	cancelStarted := make(chan struct{})
	workerContext, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- RunWorker(workerContext, c, TaskQueue, func(ctx context.Context, got Reference) (Result, error) {
			switch got.ID {
			case cancelRef.ID:
				close(cancelStarted)
				<-ctx.Done()
				return Result{}, ctx.Err()
			case panicRef.ID:
				panic("source-token-canary panic")
			case revokedRef.ID:
				return Result{}, fmt.Errorf("source-token-canary db details: %w", Failure{Code: "permission_revoked"})
			case ref.ID:
				calls.Add(1)
				if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
					if err := os.WriteFile(marker, []byte("source-token-canary retained in activity storage only"), 0600); err != nil {
						return Result{}, err
					}
					writes.Add(1)
					return Result{}, Failure{Code: "database_unavailable", Retryable: true}
				}
			}
			return Result{ReceiptID: got.ID, Digest: strings.Repeat("a", 64)}, nil
		})
	}()
	t.Cleanup(func() {
		stopWorker()
		select {
		case err := <-workerDone:
			if err != nil {
				t.Error("worker shutdown:", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("worker did not stop within its bound")
		}
	})
	completed := awaitState(t, r, ref, "completed")
	if completed.Result == nil || completed.Result.ReceiptID != ref.ID || calls.Load() != 2 || writes.Load() != 1 {
		t.Fatalf("receipt retry: %#v calls=%d writes=%d", completed, calls.Load(), writes.Load())
	}
	closedDuplicate, err := r.Start(context.Background(), WorkflowName+"/"+ref.ID, ref)
	if err != nil || closedDuplicate.RunID != first.RunID || closedDuplicate.State != "completed" {
		t.Fatalf("closed duplicate: %#v %v", closedDuplicate, err)
	}
	for _, failedRef := range []Reference{panicRef, revokedRef} {
		if _, err := r.Start(context.Background(), WorkflowName+"/"+failedRef.ID, failedRef); err != nil {
			t.Fatal(err)
		}
		awaitState(t, r, failedRef, "failed")
		historyWithoutCanaries(t, c, failedRef, logger)
	}
	if _, err := r.Start(context.Background(), WorkflowName+"/"+cancelRef.ID, cancelRef); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelStarted:
	case <-time.After(15 * time.Second):
		t.Fatal("cancellable activity did not start")
	}
	if err := r.Cancel(context.Background(), WorkflowName+"/"+cancelRef.ID, "", cancelRef); err != nil {
		t.Fatal(err)
	}
	awaitState(t, r, cancelRef, "cancelled")
	historyWithoutCanaries(t, c, cancelRef, logger)
	history := historyWithoutCanaries(t, c, ref, logger)
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(collectionWorkflow, workflow.RegisterOptions{Name: WorkflowName})
	if err := replayer.ReplayWorkflowHistory(logger, history); err != nil {
		t.Fatal("replay real retained history:", err)
	}
	missing := randomReference(t)
	if _, err := r.Lookup(context.Background(), WorkflowName+"/"+missing.ID, "", missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing execution: %v", err)
	}
}

// Invoked only by the owned subprocess test below. Exiting between the local
// durable effect and the SDK completion RPC models a real lost acknowledgement.
func TestTemporalWorkerCrashHelper(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEMPORAL_CRASH_HELPER") != "1" {
		t.Skip("internal subprocess helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := client.DialContext(ctx, client.Options{
		HostPort: os.Getenv("CONDUCTOR_TEMPORAL_HELPER_ADDRESS"), Namespace: "conductor-acceptance",
		Logger: &recordingLogger{}, Identity: "conductor-crash-fixture",
		ConnectionOptions: client.ConnectionOptions{MaxPayloadSize: 1 << 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = RunWorker(ctx, c, TaskQueue, func(context.Context, Reference) (Result, error) {
		marker, err := os.OpenFile(os.Getenv("CONDUCTOR_TEMPORAL_HELPER_MARKER"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return Result{}, err
		}
		if _, err := marker.WriteString("source-token-canary committed before process exit"); err != nil {
			_ = marker.Close()
			return Result{}, err
		}
		if err := marker.Sync(); err != nil {
			_ = marker.Close()
			return Result{}, err
		}
		if err := marker.Close(); err != nil {
			return Result{}, err
		}
		os.Exit(23)
		return Result{}, errors.New("unreachable")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("worker helper returned without the intended process exit")
}

func TestTemporalWorkerCrashRecoveryLive(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL=1 to run actual worker-process recovery")
	}
	server := newTemporalServer(t)
	logger := &recordingLogger{}
	c := server.connect(logger)
	t.Cleanup(c.Close)
	r, err := NewRuntime(c, "conductor-acceptance", TaskQueue)
	if err != nil {
		t.Fatal(err)
	}
	ref := randomReference(t)
	first, err := r.Start(context.Background(), WorkflowName+"/"+ref.ID, ref)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(server.dir, "crash-receipt")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestTemporalWorkerCrashHelper$", "-test.timeout=35s")
	command.Env = append(os.Environ(), "CONDUCTOR_TEMPORAL_CRASH_HELPER=1", "CONDUCTOR_TEMPORAL_HELPER_ADDRESS="+server.address, "CONDUCTOR_TEMPORAL_HELPER_MARKER="+marker)
	output, err := command.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 23 {
		t.Fatalf("owned worker did not exit at the intended boundary: %v", err)
	}
	if strings.Contains(string(output), "source-token-canary") {
		t.Fatal("private canary entered child worker logs")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("worker exited without its durable receipt:", err)
	}
	var recoveries atomic.Int32
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- RunWorker(workerCtx, c, TaskQueue, func(_ context.Context, got Reference) (Result, error) {
			if _, err := os.Stat(marker); err != nil {
				return Result{}, err
			}
			recoveries.Add(1)
			return Result{ReceiptID: got.ID, Digest: strings.Repeat("a", 64)}, nil
		})
	}()
	t.Cleanup(func() {
		stopWorker()
		select {
		case err := <-workerDone:
			if err != nil {
				t.Error("recovery worker shutdown:", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("recovery worker did not stop within its bound")
		}
	})
	completed := awaitState(t, r, ref, "completed")
	if completed.RunID != first.RunID || completed.Result == nil || completed.Result.ReceiptID != ref.ID || recoveries.Load() != 1 {
		t.Fatalf("crash recovery: %#v recoveries=%d", completed, recoveries.Load())
	}
	historyWithoutCanaries(t, c, ref, logger)
}

func TestTemporalRuntimeTargetLive(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL=1 to verify actual runtime identity across restart/recreation")
	}
	server := newTemporalServer(t)
	logger := &recordingLogger{}
	c := server.connect(logger)
	first, err := RuntimeTarget(context.Background(), c, "conductor-acceptance", server.address, TaskQueue)
	if err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()
	server.stop()
	server.start()
	c = server.connect(logger)
	restarted, err := RuntimeTarget(context.Background(), c, "conductor-acceptance", server.address, TaskQueue)
	if err != nil || restarted != first {
		c.Close()
		t.Fatalf("persistent process restart changed runtime identity: %q %q %v", first, restarted, err)
	}
	defer c.Close()
	bound, err := NewBoundRuntime(c, "conductor-acceptance", server.address, TaskQueue, restarted)
	if err != nil {
		t.Fatal(err)
	}
	server.stop()
	// Replace only this test's server with a new empty owned SQLite directory at
	// the same address. Keep the original database intact to distinguish this
	// negative recreation case from normal persistent process recovery.
	server.dir = t.TempDir()
	server.start()
	// Keep the original SDK client alive across replacement. A cached startup
	// fingerprint would miss this backend change after gRPC reconnects.
	var recreated string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		recreated, err = RuntimeTarget(context.Background(), c, "conductor-acceptance", server.address, TaskQueue)
		if !errors.Is(err, ErrUnavailable) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || recreated == first {
		t.Fatalf("same-address server recreation retained runtime identity: %q %v", recreated, err)
	}
	ref := randomReference(t)
	if _, err = bound.Start(context.Background(), WorkflowName+"/"+ref.ID, ref); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("connected runtime started work on recreated server: %v", err)
	}
	unbound, err := NewRuntime(c, "conductor-acceptance", TaskQueue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = unbound.Lookup(context.Background(), WorkflowName+"/"+ref.ID, "", ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mismatched start created an execution: %v", err)
	}
}
