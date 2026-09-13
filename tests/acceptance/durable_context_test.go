package acceptance_test

import (
	"context"
	"crypto/sha1" // Synthetic Git blob identity, not an authentication primitive.
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/authn"
	"github.com/phenixrizen/conductor/internal/collectionworker"
	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/repositorycontext/remote"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/temporaltest"
	conductorclient "github.com/phenixrizen/conductor/pkg/client"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const durableProviderToken = "synthetic-durable-provider-token-canary"
const durableSourceText = "# synthetic-durable-source-canary\nShared design context.\n"
const durableCommit = "1111111111111111111111111111111111111111"
const durableTree = "2222222222222222222222222222222222222222"
const durableNamespace = "conductor-acceptance"

type durableHelperConfig struct {
	Mode, DatabaseURL, TemporalAddress, ProviderOrigin, Issuer, APIAddress string
}

type durableProcess struct {
	t       *testing.T
	cmd     *exec.Cmd
	done    chan error
	log     *os.File
	logPath string
}

func (p *durableProcess) stop() {
	if p.cmd == nil {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
		p.t.Error("owned durability subprocess exceeded its shutdown deadline")
	}
	_ = p.log.Close()
	p.cmd = nil
}

func (p *durableProcess) expectExit(code int) {
	p.t.Helper()
	select {
	case err := <-p.done:
		_ = p.log.Close()
		p.cmd = nil
		var exit *exec.ExitError
		if code == 0 && err != nil || code != 0 && (!errors.As(err, &exit) || exit.ExitCode() != code) {
			p.t.Fatalf("owned durability subprocess exited unexpectedly: %v; log %s", err, p.logPath)
		}
	case <-time.After(40 * time.Second):
		p.stop()
		p.t.Fatal("owned durability subprocess did not reach its intended boundary")
	}
}

func durableStartProcess(t *testing.T, command *exec.Cmd, directory, name string) *durableProcess {
	t.Helper()
	path := filepath.Join(directory, name+".log")
	log, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		_ = log.Close()
		t.Fatal("start owned durability subprocess:", err)
	}
	p := &durableProcess{t: t, cmd: command, done: make(chan error, 1), log: log, logPath: path}
	go func() { p.done <- command.Wait() }()
	t.Cleanup(p.stop)
	return p
}

func durableStartHelper(t *testing.T, config durableHelperConfig, directory, name string) *durableProcess {
	t.Helper()
	path := filepath.Join(directory, name+".json")
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-test.run=^TestDurableContextHelper$", "-test.timeout=4m")
	command.Env = append(os.Environ(), "CONDUCTOR_DURABLE_HELPER_CONFIG="+path)
	return durableStartProcess(t, command, directory, name)
}

func durableAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func durableStartTemporal(t *testing.T, directory, address, name string) *durableProcess {
	t.Helper()
	binary := os.Getenv("CONDUCTOR_TEMPORAL_CLI")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("temporal")
		if err != nil {
			t.Fatal("opted-in durability acceptance requires verified Temporal CLI v1.8.3")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "temporal version 1.8.3 (Server 1.31.2,") {
		t.Fatal("durability acceptance requires Temporal CLI1.8.3/server1.31.2")
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "server", "start-dev", "--ip", "127.0.0.1", "--port", port, "--headless", "--namespace", durableNamespace, "--db-filename", filepath.Join(directory, "temporal.sqlite"))
	p := durableStartProcess(t, command, directory, name)
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
	ready := make(chan error, 1)
	go func() { ready <- temporaltest.AwaitReady(readyCtx, address, durableNamespace) }()
	select {
	case err := <-p.done:
		_ = p.log.Close()
		p.cmd = nil
		t.Fatalf("owned Temporal process exited before readiness: %v", err)
	case err := <-ready:
		if err == nil {
			return p
		}
		p.stop()
		t.Fatal(err)
	}
	return nil
}

type durableSDKLogger struct{}

func (durableSDKLogger) Debug(m string, p ...interface{}) { fmt.Fprintln(os.Stderr, m, p) }
func (durableSDKLogger) Info(m string, p ...interface{})  { fmt.Fprintln(os.Stderr, m, p) }
func (durableSDKLogger) Warn(m string, p ...interface{})  { fmt.Fprintln(os.Stderr, m, p) }
func (durableSDKLogger) Error(m string, p ...interface{}) { fmt.Fprintln(os.Stderr, m, p) }

func durableEngine(ctx context.Context, address string) (client.Client, error) {
	return client.DialContext(ctx, client.Options{HostPort: address, Namespace: durableNamespace,
		Identity: "conductor-durable-acceptance", Logger: durableSDKLogger{},
		ConnectionOptions: client.ConnectionOptions{MaxPayloadSize: 1 << 20}})
}

type crashStartRuntime struct{ collectionworker.Runtime }

func (r crashStartRuntime) Start(ctx context.Context, id string, ref contextworkflow.Reference) (contextworkflow.Observation, error) {
	result, err := r.Runtime.Start(ctx, id, ref)
	if err == nil {
		os.Exit(23) // Real RPC accepted; no observation/outbox acknowledgment yet.
	}
	return result, err
}

type crashReceiptStore struct{ *store.Postgres }

func (s crashReceiptStore) CompleteCollection(ctx context.Context, id, binding string, artifacts []domain.ContextArtifact) (domain.CollectionReceipt, error) {
	receipt, err := s.Postgres.CompleteCollection(ctx, id, binding, artifacts)
	if err == nil {
		os.Exit(24) // Actual receipt and audit transaction committed; SDK has no result.
	}
	return receipt, err
}

// Subprocesses use the production verifier, service, store, dispatcher, runtime,
// activity and remote collector. Only owned fixture origins and exact crash
// boundaries differ from deployed components; there are no production hooks.
func TestDurableContextHelper(t *testing.T) {
	path := os.Getenv("CONDUCTOR_DURABLE_HELPER_CONFIG")
	if path == "" {
		t.Skip("internal durable collection subprocess helper")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 32<<10 {
		t.Fatal("invalid owned helper configuration")
	}
	var config durableHelperConfig
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal("invalid owned helper configuration")
	}
	ctx, cancel := signalContext()
	defer cancel()
	db, err := store.Open(ctx, config.DatabaseURL)
	if err != nil {
		t.Fatal("owned PostgreSQL fixture unavailable")
	}
	defer db.Close()
	if config.Mode == "api" {
		verifier, err := authn.New(ctx, authn.Config{Issuer: config.Issuer, Audience: "conductor-acceptance", AllowInsecureLoopback: true})
		if err != nil {
			t.Fatal("owned signed issuer unavailable")
		}
		server := &http.Server{Addr: config.APIAddress, Handler: api.NewAuthenticated(service.NewAuthenticated(db).WithCollections(), verifier), ReadHeaderTimeout: 5 * time.Second}
		done := make(chan error, 1)
		go func() { done <- server.ListenAndServe() }()
		select {
		case <-ctx.Done():
			shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = server.Shutdown(shutdown)
			<-done
		case err := <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Fatal("owned API failed")
			}
		}
		return
	}
	engine, err := durableEngine(ctx, config.TemporalAddress)
	if err != nil {
		t.Fatal("owned Temporal fixture unavailable")
	}
	defer engine.Close()
	resolve := func(ctx context.Context) (string, error) {
		return contextworkflow.RuntimeTarget(ctx, engine, durableNamespace, config.TemporalAddress, contextworkflow.TaskQueue)
	}
	target, err := resolve(ctx)
	if err != nil {
		t.Fatal("owned runtime identity unavailable")
	}
	runtime, err := contextworkflow.NewBoundRuntime(engine, durableNamespace, config.TemporalAddress, contextworkflow.TaskQueue, target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(config.Mode, "dispatcher") {
		var execution collectionworker.Runtime = runtime
		if config.Mode == "dispatcher-crash" {
			execution = crashStartRuntime{runtime}
		}
		dispatcher, err := collectionworker.NewDispatcher(db, execution, durableNamespace, target, resolve)
		if err != nil {
			t.Fatal(err)
		}
		if config.Mode == "dispatcher-run" {
			err = dispatcher.Run(ctx, func(status string) { fmt.Fprintln(os.Stderr, status) })
		} else {
			_, err = dispatcher.Step(ctx)
		}
		if err != nil {
			t.Fatal("dispatcher fixture failed")
		}
		return
	}
	var workStore collectionworker.WorkStore = db
	if config.Mode == "worker-crash" {
		workStore = crashReceiptStore{db}
	}
	activity, err := collectionworker.NewActivity(workStore, func(context.Context, domain.ContextIntegration) (string, error) { return durableProviderToken, nil }, func(configured remote.Config) (collectionworker.Collector, error) {
		configured.APIOrigin, configured.AllowInsecureLoopback = config.ProviderOrigin, true
		return remote.New(configured)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = contextworkflow.RunWorker(ctx, engine, contextworkflow.TaskQueue, activity.Collect); err != nil {
		t.Fatal("worker fixture failed")
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-interrupt:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(interrupt)
	}()
	return ctx, cancel
}

type durableBlobHold struct {
	started, release, cancelled chan struct{}
	startOnce, cancelOnce       sync.Once
}

type durableProvider struct {
	t        *testing.T
	server   *httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	hold     *durableBlobHold
	blob     string
}

func newDurableProvider(t *testing.T) *durableProvider {
	p := &durableProvider{t: t}
	object := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(durableSourceText), durableSourceText)))
	p.blob = hex.EncodeToString(object[:])
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	return p
}

func (p *durableProvider) serve(w http.ResponseWriter, r *http.Request) {
	p.requests.Add(1)
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+durableProviderToken || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" || r.URL.RawQuery != "" {
		p.t.Error("bounded provider fixture received unexpected request")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var value any
	switch r.URL.Path {
	case "/repos/synthetic/application":
		value = map[string]any{"id": 101}
	case "/repos/synthetic/application/git/commits/" + durableCommit:
		value = map[string]any{"sha": durableCommit, "tree": map[string]string{"sha": durableTree}}
	case "/repos/synthetic/application/git/trees/" + durableTree:
		value = map[string]any{"sha": durableTree, "truncated": false, "tree": []any{map[string]any{"path": "README.md", "sha": p.blob, "mode": "100644", "type": "blob", "size": len(durableSourceText)}}}
	case "/repos/synthetic/application/git/blobs/" + p.blob:
		p.mu.Lock()
		hold := p.hold
		p.mu.Unlock()
		if hold != nil {
			hold.startOnce.Do(func() { close(hold.started) })
			select {
			case <-hold.release:
			case <-r.Context().Done():
				hold.cancelOnce.Do(func() { close(hold.cancelled) })
				return
			}
		}
		value = map[string]any{"sha": p.blob, "size": len(durableSourceText), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(durableSourceText))}
	default:
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func (p *durableProvider) blockBlob() *durableBlobHold {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hold = &durableBlobHold{started: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{})}
	return p.hold
}

func durableWait(t *testing.T, reason string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("durable collection did not reach %s", reason)
}

func durableAPIClient(t *testing.T, f *accessFixture, address, actor string) *conductorclient.Client {
	t.Helper()
	c, err := conductorclient.NewAuthenticated("http://"+address, f.tokens[actor], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func durableWaitCollection(t *testing.T, ctx context.Context, c *conductorclient.Client, id, state string) domain.Collection {
	t.Helper()
	var result domain.Collection
	durableWait(t, state, func() bool {
		var err error
		result, err = c.GetCollection(ctx, id)
		if err != nil {
			t.Fatal("inspect durable collection:", err)
		}
		return result.Execution != nil && result.Execution.State == state
	})
	return result
}

func assertDurableNoCanary(t *testing.T, value protoreflect.Message) {
	t.Helper()
	var inspect func(protoreflect.Message)
	inspect = func(message protoreflect.Message) {
		message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			check := func(kind protoreflect.Kind, value protoreflect.Value) {
				var text string
				switch kind {
				case protoreflect.MessageKind, protoreflect.GroupKind:
					inspect(value.Message())
				case protoreflect.BytesKind:
					text = string(value.Bytes())
				case protoreflect.StringKind:
					text = value.String()
				}
				if strings.Contains(text, "synthetic-durable-source-canary") || strings.Contains(text, durableProviderToken) {
					t.Fatal("source or provider token entered decoded Temporal history")
				}
			}
			if field.IsMap() {
				value.Map().Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
					check(field.MapKey().Kind(), key.Value())
					check(field.MapValue().Kind(), value)
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
	inspect(value)
}

func TestDurableContextProcessRecovery(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL=1 for actual API/dispatcher/worker/Temporal process acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in durable context acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newAccessFixtureWithIssuer(t, nil, nil, 3*time.Minute)
	provider := newDurableProvider(t)
	integration := domain.ContextIntegrationConfig{RepositoryID: "application", WorkspaceID: "team", Profile: remote.GitHubProfile, Locator: "synthetic/application", CredentialID: "fixture", Enabled: true}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{ContextIntegrations: []domain.ContextIntegrationConfig{integration}}); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	config := durableHelperConfig{DatabaseURL: f.databaseURL, TemporalAddress: durableAddress(t), ProviderOrigin: provider.server.URL, Issuer: f.issuer.url, APIAddress: durableAddress(t)}
	startHelper := func(mode, name string) *durableProcess {
		child := config
		child.Mode = mode
		return durableStartHelper(t, child, directory, name)
	}
	apiProcess := startHelper("api", "api-before-restart")
	author, reviewer := durableAPIClient(t, f, config.APIAddress, "author"), durableAPIClient(t, f, config.APIAddress, "reviewer")
	durableWait(t, "API readiness", func() bool {
		_, err := reviewer.Session(f.ctx)
		return err == nil
	})
	pkg, err := author.Create(f.ctx, domain.Content{"intent": "Review synthetic shared context", "futureExtension": map[string]any{"keep": true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = author.Submit(f.ctx, pkg.ID, pkg.Revision.Number); err != nil {
		t.Fatal(err)
	}
	if _, err = reviewer.Approve(f.ctx, pkg.ID, pkg.Revision.Number, pkg.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	// No Temporal process exists yet: the API commits intent without external I/O.
	first, err := author.CreateCollection(f.ctx, "durable-crash", domain.CollectionInput{Commit: durableCommit, Paths: []string{"missing.md", "README.md"}})
	if err != nil || first.Receipt != nil || first.Execution != nil {
		t.Fatalf("offline accepted intent: %#v %v", first, err)
	}
	duplicate, err := author.CreateCollection(f.ctx, "durable-crash", domain.CollectionInput{Commit: durableCommit, Paths: []string{"README.md", "missing.md"}})
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("idempotent offline request changed identity: %#v %v", duplicate, err)
	}
	if _, err = author.CreateCollection(f.ctx, "durable-crash", domain.CollectionInput{Commit: durableCommit, Paths: []string{"README.md"}}); err == nil {
		t.Fatal("same idempotency key accepted different immutable input")
	}
	temporalProcess := durableStartTemporal(t, directory, config.TemporalAddress, "temporal-before-restart")
	startHelper("dispatcher-crash", "dispatcher-lost-start-ack").expectExit(23)
	var binding string
	if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, first.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	engine, err := durableEngine(f.ctx, config.TemporalAddress)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	runtime, err := contextworkflow.NewRuntime(engine, durableNamespace, contextworkflow.TaskQueue)
	if err != nil {
		t.Fatal(err)
	}
	ref := contextworkflow.Reference{ID: first.ID, Binding: binding}
	accepted, err := runtime.Lookup(f.ctx, contextworkflow.WorkflowName+"/"+first.ID, "", ref)
	if err != nil || accepted.State != "running" {
		t.Fatalf("lost ACK did not leave a real running execution: %#v %v", accepted, err)
	}
	// Only this isolated lease is advanced after its owner has exited. The live
	// store tests separately prove expiry and stale-token fencing.
	if _, err = f.sql.Exec(f.ctx, `UPDATE context_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE collection_id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	startHelper("dispatcher-step", "dispatcher-restarted").expectExit(0)
	running, err := reviewer.GetCollection(f.ctx, first.ID)
	if err != nil || running.Execution == nil || running.Execution.RunID != accepted.RunID {
		t.Fatalf("dispatcher restart replaced the execution: %#v %v", running, err)
	}
	startHelper("worker-crash", "worker-lost-receipt-ack").expectExit(24)
	committed, err := reviewer.GetCollection(f.ctx, first.ID)
	if err != nil || committed.Receipt == nil || len(committed.Receipt.Snapshot.Artifacts) != 2 || committed.Receipt.Snapshot.Artifacts[0].Text == nil || *committed.Receipt.Snapshot.Artifacts[0].Text != durableSourceText || committed.Receipt.Snapshot.Artifacts[1].State != "missing" {
		t.Fatalf("actual activity did not commit bounded shared evidence: %#v %v", committed, err)
	}
	providerCalls := provider.requests.Load()
	if providerCalls != 5 {
		t.Fatalf("expected identity/commit/tree/blob/final identity reads, got %d", providerCalls)
	}
	setAuthor := func(enabled bool) {
		if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-author", CanRead: true, CanAuthor: enabled, CanApprove: enabled}}}); err != nil {
			t.Fatal(err)
		}
	}
	setAuthor(false)
	workerProcess := startHelper("worker", "worker-restarted")
	dispatcherProcess := startHelper("dispatcher-run", "dispatcher-observing")
	completed := durableWaitCollection(t, f.ctx, reviewer, first.ID, "completed")
	if completed.Execution.RunID != accepted.RunID || completed.Receipt.Digest != committed.Receipt.Digest || provider.requests.Load() != providerCalls {
		t.Fatal("lost receipt acknowledgment caused a new execution, changed receipt, or repeated provider reads")
	}
	for _, actor := range []string{"reader", "agent"} {
		shared := durableAPIClient(t, f, config.APIAddress, actor)
		inspected, err := shared.GetCollection(f.ctx, first.ID)
		if err != nil || inspected.Receipt == nil || inspected.Receipt.Digest != completed.Receipt.Digest {
			t.Fatalf("shared %s inspection did not resolve the same receipt: %v", actor, err)
		}
		if _, err = shared.CancelCollection(f.ctx, first.ID); err == nil {
			t.Fatalf("%s cancelled another principal's request", actor)
		}
	}
	page, err := reviewer.ListCollections(f.ctx, "", 1)
	if err != nil || len(page.Collections) != 1 || page.Collections[0].ID != first.ID || page.Collections[0].ReceiptDigest != completed.Receipt.Digest || page.NextBefore != "" {
		t.Fatalf("shared collection discovery lost receipt identity: %#v %v", page, err)
	}
	foreign, err := conductorclient.NewAuthenticated("http://"+config.APIAddress, f.tokens["other"], "other-team", "other-application")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = foreign.GetCollection(f.ctx, first.ID); err == nil {
		t.Fatal("foreign workspace inspected a collection by known ID")
	}
	foreignPage, err := foreign.ListCollections(f.ctx, "", 1)
	if err != nil || len(foreignPage.Collections) != 0 || foreignPage.NextBefore != "" {
		t.Fatalf("collection discovery crossed workspace scope: %#v %v", foreignPage, err)
	}
	var receipts, receiptAudits, requests int
	if err = f.sql.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM context_receipts WHERE collection_id=$1),(SELECT count(*) FROM context_audit_events WHERE collection_id=$1 AND event_type='collection.receipt_recorded'),(SELECT count(*) FROM context_audit_events WHERE collection_id=$1 AND event_type='collection.requested')`, first.ID).Scan(&receipts, &receiptAudits, &requests); err != nil || receipts != 1 || receiptAudits != 1 || requests != 1 {
		t.Fatalf("duplicate effects after crashes: receipt=%d audit=%d request=%d err=%v", receipts, receiptAudits, requests, err)
	}
	attached, err := reviewer.AttachCollection(f.ctx, pkg.ID, pkg.Revision.Number, first.ID, completed.Receipt.Digest)
	if err != nil || attached.Revision.Number != 2 || attached.Approved || attached.Approval != nil || attached.Revision.Content["futureExtension"] == nil {
		t.Fatalf("shared receipt attachment lost version/approval/extension invariants: %#v %v", attached, err)
	}
	if _, err = reviewer.AttachCollection(f.ctx, pkg.ID, 1, first.ID, completed.Receipt.Digest); err == nil {
		t.Fatal("stale receipt attachment was accepted")
	}
	historical, err := reviewer.Revision(f.ctx, pkg.ID, 1)
	if err != nil || len(historical.Approvals) != 1 || historical.Approvals[0].Digest != pkg.Revision.Digest || !reflect.DeepEqual(historical.Revision.Content, pkg.Revision.Content) {
		t.Fatalf("receipt attachment changed inspected historical content or approval: %v", err)
	}
	apiProcess.stop()
	apiProcess = startHelper("api", "api-after-restart")
	durableWait(t, "restarted API", func() bool { _, err := reviewer.Session(f.ctx); return err == nil })
	recovered, err := reviewer.GetCollection(f.ctx, first.ID)
	if err != nil || recovered.Receipt == nil || recovered.Receipt.Digest != completed.Receipt.Digest {
		t.Fatal("actual API restart lost the shared receipt")
	}
	recoveredPackage, err := reviewer.Get(f.ctx, pkg.ID)
	if err != nil || recoveredPackage.Revision.Digest != attached.Revision.Digest || recoveredPackage.Approved || !reflect.DeepEqual(recoveredPackage.Revision.Content, attached.Revision.Content) {
		t.Fatal("actual API restart changed attached context, extensions, or approval invalidation")
	}
	workerProcess.stop()
	dispatcherProcess.stop()
	engine.Close()
	temporalProcess.stop()
	setAuthor(true)
	queued, err := author.CreateCollection(f.ctx, "while-temporal-down", domain.CollectionInput{Commit: durableCommit, Paths: []string{"README.md"}})
	if err != nil || queued.Execution != nil || queued.Receipt != nil {
		t.Fatalf("API could not retain request while Temporal was stopped: %#v %v", queued, err)
	}
	temporalProcess = durableStartTemporal(t, directory, config.TemporalAddress, "temporal-after-restart")
	engine, err = durableEngine(f.ctx, config.TemporalAddress)
	if err != nil {
		t.Fatal(err)
	}
	workerProcess = startHelper("worker", "worker-after-server-restart")
	dispatcherProcess = startHelper("dispatcher-run", "dispatcher-after-server-restart")
	durableWaitCollection(t, f.ctx, reviewer, queued.ID, "completed")
	checkHeld := func(name string, revoke bool) domain.Collection {
		hold := provider.blockBlob()
		collection, err := author.CreateCollection(f.ctx, name, domain.CollectionInput{Commit: durableCommit, Paths: []string{"README.md"}})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-hold.started:
		case <-time.After(20 * time.Second):
			t.Fatal("bounded provider operation did not start")
		}
		want := "cancelled"
		if revoke {
			setAuthor(false)
			close(hold.release)
			want = "failed"
		} else {
			intent, err := author.CancelCollection(f.ctx, collection.ID)
			if err != nil || intent.CancelRequestedAt == nil {
				t.Fatalf("cancellation intent was not committed: %#v %v", intent, err)
			}
		}
		observed := durableWaitCollection(t, f.ctx, reviewer, collection.ID, want)
		if observed.Receipt != nil {
			t.Fatal("cancellation/revocation published a receipt")
		}
		if !revoke {
			select {
			case <-hold.cancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("Temporal cancellation did not cancel in-flight provider HTTP")
			}
		}
		provider.mu.Lock()
		provider.hold = nil
		provider.mu.Unlock()
		return observed
	}
	cancelled := checkHeld("cancel-active-provider-read", false)
	revoked := checkHeld("revoke-before-receipt-commit", true)
	for _, collection := range []domain.Collection{completed, queued, cancelled, revoked} {
		iterator := engine.GetWorkflowHistory(f.ctx, contextworkflow.WorkflowName+"/"+collection.ID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		events, bytes := 0, 0
		for iterator.HasNext() {
			event, err := iterator.Next()
			if err != nil {
				t.Fatal(err)
			}
			events++
			bytes += proto.Size(event)
			if events > 256 || bytes > 1<<20 {
				t.Fatal("fixture workflow history exceeded its bound")
			}
			assertDurableNoCanary(t, event.ProtoReflect())
		}
	}
	workerProcess.stop()
	dispatcherProcess.stop()
	apiProcess.stop()
	temporalProcess.stop()
	logs, err := filepath.Glob(filepath.Join(directory, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range logs {
		log, err := os.ReadFile(path)
		if err != nil || len(log) > 1<<20 || strings.Contains(string(log), "synthetic-durable-source-canary") || strings.Contains(string(log), durableProviderToken) {
			t.Fatalf("durability log exceeded bounds or exposed a canary: %s", path)
		}
	}
	t.Log("real signed API, PostgreSQL, provider HTTP, dispatcher/worker crashes, server/API restarts, cancellation, revocation, receipt attachment and decoded history/log isolation passed")
}
