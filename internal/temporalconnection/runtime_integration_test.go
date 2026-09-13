package temporalconnection

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/temporaltest"
	"go.temporal.io/sdk/client"
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

// CLI 1.8.3 start-dev has no server TLS flags. This fixture terminates real mTLS
// in an owned gateway and forwards only to the owned loopback Temporal process.
// It tests the actual Temporal protocol, not production server TLS deployment.
func tlsGateway(t *testing.T, secure *tls.Config, backend string) string {
	t.Helper()
	secure = secure.Clone()
	secure.NextProtos = []string{"h2"}
	listener, e := tls.Listen("tcp", "127.0.0.1:0", secure)
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	active := map[net.Conn]bool{}
	var wg sync.WaitGroup
	closed := false
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			front, e := listener.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				_ = front.Close()
				return
			}
			active[front] = true
			wg.Add(1)
			mu.Unlock()
			go func() {
				defer wg.Done()
				defer func() { _ = front.Close(); mu.Lock(); delete(active, front); mu.Unlock() }()
				_ = front.SetDeadline(time.Now().Add(5 * time.Second))
				if e := front.(*tls.Conn).Handshake(); e != nil {
					return
				}
				_ = front.SetDeadline(time.Time{})
				back, e := net.DialTimeout("tcp", backend, 3*time.Second)
				if e != nil {
					return
				}
				defer back.Close()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(back, front); _ = back.Close(); close(done) }()
				_, _ = io.Copy(front, back)
				_ = front.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() {
		mu.Lock()
		closed = true
		_ = listener.Close()
		for c := range active {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return listener.Addr().String()
}
func TestTemporalTLSWorkflowSurvivesOwnedProcessRestart(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL_TLS") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL_TLS=1 for owned CLI plus real mTLS protocol acceptance")
	}
	server := newTemporalServer(t)
	p := newPKI(t)
	address := tlsGateway(t, p.serverTLS(t, true), server.address)
	env := p.env(t)
	env["CONDUCTOR_TEMPORAL_ADDRESS"] = address
	env["CONDUCTOR_TEMPORAL_NAMESPACE"] = "conductor-acceptance"
	transport, e := loadEnv(env)
	if e != nil {
		t.Fatal(e)
	}
	connect := func() client.Client {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		engine, e := client.DialContext(ctx, transport.Options("conductor-tls-acceptance", quietLogger{}))
		if e != nil {
			t.Fatal("connect owned TLS Temporal", e)
		}
		return engine
	}
	engine := connect()
	defer engine.Close()
	target, e := contextworkflow.RuntimeTarget(context.Background(), engine, transport.Namespace, address, contextworkflow.TaskQueue)
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := contextworkflow.NewBoundRuntime(engine, transport.Namespace, address, contextworkflow.TaskQueue, target)
	if e != nil {
		t.Fatal(e)
	}
	ref := contextworkflow.Reference{ID: strings.Repeat("a", 32), Binding: strings.Repeat("b", 32)}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- contextworkflow.RunWorker(workerCtx, engine, contextworkflow.TaskQueue, func(ctx context.Context, got contextworkflow.Reference) (contextworkflow.Result, error) {
			return contextworkflow.Result{ReceiptID: got.ID, Digest: strings.Repeat("c", 64)}, nil
		})
	}()
	stopped := false
	defer func() {
		stopWorker()
		if !stopped {
			<-done
		}
	}()
	started, e := runtime.Start(context.Background(), contextworkflow.WorkflowName+"/"+ref.ID, ref)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var result contextworkflow.Result
	if e := engine.GetWorkflow(ctx, contextworkflow.WorkflowName+"/"+ref.ID, started.RunID).Get(ctx, &result); e != nil {
		t.Fatal(e)
	}
	if result.ReceiptID != ref.ID || result.Digest != strings.Repeat("c", 64) {
		t.Fatal("wrong receipt result")
	}
	stopWorker()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	stopped = true
	engine.Close()
	server.stop()
	server.start()
	after := connect()
	defer after.Close()
	retained, e := contextworkflow.RuntimeTarget(ctx, after, transport.Namespace, address, contextworkflow.TaskQueue)
	if e != nil || retained != target {
		t.Fatal("owned restart changed retained binding", e)
	}
	runtime, e = contextworkflow.NewBoundRuntime(after, transport.Namespace, address, contextworkflow.TaskQueue, retained)
	if e != nil {
		t.Fatal(e)
	}
	observed, e := runtime.Lookup(ctx, contextworkflow.WorkflowName+"/"+ref.ID, started.RunID, ref)
	if e != nil || observed.State != "completed" || observed.Result == nil || *observed.Result != result {
		t.Fatal("TLS restart lost exact workflow receipt", observed, e)
	}
	duplicate, e := runtime.Start(ctx, contextworkflow.WorkflowName+"/"+ref.ID, ref)
	if e != nil || duplicate.RunID != started.RunID || duplicate.State != "completed" {
		t.Fatal("TLS retry replaced retained workflow", duplicate, e)
	}
}
