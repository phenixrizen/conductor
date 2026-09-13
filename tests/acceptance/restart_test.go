package acceptance_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

// Keep the database image reproducible independently of the moving development
// tag. This is the PostgreSQL 17 Alpine image exercised by this acceptance test.
const restartPostgresImage = "postgres:17-alpine@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73"

// TestProcessRestartDurability stops actual API and PostgreSQL processes. It owns
// its container and volume and deliberately never reads the caller's test or
// development database URL. Opting in requires working Docker, Go, and Git.
func TestProcessRestartDurability(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_PROCESS_RESTART") != "1" {
		t.Skip("set CONDUCTOR_TEST_PROCESS_RESTART=1 to run isolated Docker/API process restart acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	t.Cleanup(cancel)
	for _, tool := range []string{"docker", "go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("process restart acceptance requires %s: %v", tool, err)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "conductord")
	runRestartCommand(t, ctx, 90*time.Second, root, nil, "go", "build", "-race", "-o", binary, "./cmd/conductord")
	database := newRestartDatabase(t, ctx)
	server := startRestartServer(t, ctx, binary, database.url)
	author := reviewClient(t, server.url, "synthetic-restart-author")
	reviewer := reviewClient(t, server.url, "synthetic-restart-reviewer")
	content := domain.Content{
		"intent":            "Retain this synthetic design across process restarts",
		"repositoryContext": jsonObject(t, committedContext(t, ctx, "synthetic/restart-example")),
		"futureExtension":   map[string]any{"keep": []any{"unknown", true, nil}},
	}
	created, err := author.Create(ctx, content)
	if err != nil {
		t.Fatalf("create before restart: %v", err)
	}
	if _, err := author.Submit(ctx, created.ID, created.Revision.Number); err != nil {
		t.Fatalf("submit before restart: %v", err)
	}
	approved, err := reviewer.Approve(ctx, created.ID, created.Revision.Number, created.Revision.Digest)
	if err != nil || !approved.Approved || approved.Approval == nil {
		t.Fatalf("approve before restart: %+v, err=%v", approved, err)
	}
	beforeAPI := captureRestartState(t, ctx, reviewer, created.ID)
	if !reflect.DeepEqual(beforeAPI.current, approved) || !reflect.DeepEqual(beforeAPI.current.Revision.Content, content) || len(beforeAPI.revisions[0].Approvals) != 1 || !reflect.DeepEqual(beforeAPI.revisions[0].Approvals[0], *approved.Approval) {
		t.Fatal("initial persistence lost inspected content or its independent approval")
	}
	assertRestartEvents(t, beforeAPI.events, "package.created", "review.requested", "review.approved")
	firstPID := server.cmd.Process.Pid
	server.stop(t)
	server = startRestartServer(t, ctx, binary, database.url)
	if server.cmd.Process.Pid == firstPID {
		t.Fatal("API process identity did not change")
	}
	author = reviewClient(t, server.url, "synthetic-restart-author")
	reviewer = reviewClient(t, server.url, "synthetic-restart-reviewer")
	assertRestartState(t, beforeAPI, captureRestartState(t, ctx, reviewer, created.ID))
	t.Log("API-only process restart retained exact current content, approval, history, and audit events")

	revisedContent := domain.Content(jsonObject(t, content))
	revisedContent["intent"] = "A new synthetic design requires a new independent review"
	revised, err := author.Revise(ctx, created.ID, 1, revisedContent)
	if err != nil || revised.Revision.Number != 2 || revised.Approved || revised.Approval != nil || revised.Revision.SubmittedAt != nil {
		t.Fatalf("revise after API restart: %+v, err=%v", revised, err)
	}
	beforeDatabase := captureRestartState(t, ctx, reviewer, created.ID)
	if !reflect.DeepEqual(beforeDatabase.current, revised) || !reflect.DeepEqual(beforeDatabase.revisions[0], beforeAPI.revisions[0]) {
		t.Fatal("revision invalidation changed the historical inspected content or approval")
	}
	assertRestartEvents(t, beforeDatabase.events, "package.created", "review.requested", "review.approved", "package.revised")
	server.stop(t)
	database.restart(t, ctx)
	server = startRestartServer(t, ctx, binary, database.url)
	author = reviewClient(t, server.url, "synthetic-restart-author")
	reviewer = reviewClient(t, server.url, "synthetic-restart-reviewer")
	assertRestartState(t, beforeDatabase, captureRestartState(t, ctx, reviewer, created.ID))
	t.Log("PostgreSQL and API process restart retained both revisions, invalidated approval, and exact audit events")

	// Restored historical approval must never authorize the now-current revision.
	_, err = reviewer.Approve(ctx, created.ID, approved.Revision.Number, approved.Revision.Digest)
	assertAPIError(t, err, http.StatusConflict, "revision_conflict")
	_, err = author.Revise(ctx, created.ID, 1, domain.Content{"intent": "Rejected stale edit"})
	assertAPIError(t, err, http.StatusConflict, "revision_conflict")
	assertRestartState(t, beforeDatabase, captureRestartState(t, ctx, reviewer, created.ID))
	if _, err := author.Submit(ctx, created.ID, revised.Revision.Number); err != nil {
		t.Fatalf("submit after PostgreSQL restart: %v", err)
	}
	_, err = author.Approve(ctx, created.ID, revised.Revision.Number, revised.Revision.Digest)
	assertAPIError(t, err, http.StatusUnprocessableEntity, "approval_rejected")
	approvedAgain, err := reviewer.Approve(ctx, created.ID, revised.Revision.Number, revised.Revision.Digest)
	if err != nil || !approvedAgain.Approved || approvedAgain.Approval == nil || approvedAgain.Approval.Revision != 2 || approvedAgain.Approval.Digest != revised.Revision.Digest {
		t.Fatalf("independently approve after PostgreSQL restart: %+v, err=%v", approvedAgain, err)
	}
	afterWrites := captureRestartState(t, ctx, reviewer, created.ID)
	if !reflect.DeepEqual(afterWrites.revisions[0], beforeAPI.revisions[0]) {
		t.Fatal("post-restart writes changed historical revision or approval")
	}
	assertRestartEvents(t, afterWrites.events, "package.created", "review.requested", "review.approved", "package.revised", "review.requested", "review.approved")
	server.stop(t)
	server = startRestartServer(t, ctx, binary, database.url)
	assertRestartState(t, afterWrites, captureRestartState(t, ctx, reviewClient(t, server.url, "synthetic-restart-reviewer"), created.ID))
	t.Log("Post-restart writes remained durable; stale commands created no revisions or audit events")
}

type restartState struct {
	current   domain.Package
	discovery domain.ChangePage
	history   domain.HistoryPage
	revisions []domain.RevisionRecord
	events    domain.AuditPage
}

func captureRestartState(t *testing.T, ctx context.Context, c *client.Client, id string) restartState {
	t.Helper()
	var state restartState
	var err error
	state.current, err = c.Get(ctx, id)
	if err != nil {
		t.Fatalf("read current package: %v", err)
	}
	state.discovery, err = c.ListChanges(ctx, "synthetic/restart-example", "", 100)
	if err != nil || state.discovery.NextBefore != "" || len(state.discovery.Changes) != 1 {
		t.Fatalf("discover complete shared repository work: %+v, err=%v", state.discovery, err)
	}
	summary := state.discovery.Changes[0]
	if summary.ID != id || summary.Revision != state.current.Revision.Number || summary.Digest != state.current.Revision.Digest || summary.Approved != state.current.Approved || summary.Repository != "synthetic/restart-example" {
		t.Fatalf("shared discovery does not describe the current package: %+v", summary)
	}
	state.history, err = c.History(ctx, id, 0, 100)
	if err != nil || state.history.NextBeforeRevision != 0 || int64(len(state.history.Revisions)) != state.current.Revision.Number {
		t.Fatalf("read complete test history: %+v, err=%v", state.history, err)
	}
	for revision := int64(1); revision <= state.current.Revision.Number; revision++ {
		record, err := c.Revision(ctx, id, revision)
		if err != nil || record.ApprovalsTruncated {
			t.Fatalf("read complete revision %d: %+v, err=%v", revision, record, err)
		}
		digest, err := domain.Digest(record.Revision.Content)
		if err != nil || digest != record.Revision.Digest {
			t.Fatalf("revision %d lost content/digest integrity: %v", revision, err)
		}
		state.revisions = append(state.revisions, record)
	}
	state.events, err = c.Events(ctx, id, 0, 100)
	if err != nil || state.events.NextAfterSequence != 0 {
		t.Fatalf("read complete test audit history: %+v, err=%v", state.events, err)
	}
	return state
}

func assertRestartState(t *testing.T, want, got restartState) {
	t.Helper()
	// Compare every field, including timestamps, actors, exact extension content,
	// digests, approval references, audit data, sequence numbers, and pagination.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted state changed across restart or rejected command:\nwant: %+v\ngot:  %+v", want, got)
	}
}

func assertRestartEvents(t *testing.T, page domain.AuditPage, kinds ...string) {
	t.Helper()
	if len(page.Events) != len(kinds) {
		t.Fatalf("expected exactly %d committed events, got %+v", len(kinds), page.Events)
	}
	for i, kind := range kinds {
		event := page.Events[i]
		actor := "synthetic-restart-author"
		if kind == "review.approved" {
			actor = "synthetic-restart-reviewer"
		}
		if event.EventType != kind || event.Actor != actor || (i > 0 && event.Sequence <= page.Events[i-1].Sequence) {
			t.Fatalf("incorrect audit event %d: %+v", i, event)
		}
	}
}

type restartDatabase struct {
	name, url string
	startedAt time.Time
}

func newRestartDatabase(t *testing.T, ctx context.Context) *restartDatabase {
	t.Helper()
	runRestartCommand(t, ctx, 10*time.Second, "", nil, "docker", "info", "--format", "{{.ServerVersion}}")
	if _, err := restartCommand(ctx, 10*time.Second, "", nil, "docker", "image", "inspect", restartPostgresImage); err != nil {
		runRestartCommand(t, ctx, 90*time.Second, "", nil, "docker", "pull", restartPostgresImage)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	owner := hex.EncodeToString(nonce[:])
	db := &restartDatabase{name: "conductor-restart-" + owner}
	volume := db.name + "-data"
	label := "conductor.acceptance.restart=" + owner
	// Register before creation to handle a daemon creating a resource even if its
	// response is interrupted. Ownership labels guard every destructive cleanup.
	t.Cleanup(func() {
		if t.Failed() {
			output, _ := restartCommand(context.Background(), 10*time.Second, "", nil, "docker", "logs", "--tail", "60", db.name)
			t.Logf("isolated PostgreSQL logs:\n%s", output)
		}
		cleanupRestartResource(t, "container", db.name, owner)
		cleanupRestartResource(t, "volume", volume, owner)
	})
	runRestartCommand(t, ctx, 20*time.Second, "", nil, "docker", "volume", "create", "--label", label, volume)
	runRestartCommand(t, ctx, 30*time.Second, "", nil, "docker", "run", "--detach", "--name", db.name, "--label", label,
		"--publish", "127.0.0.1::5432", "--mount", "type=volume,source="+volume+",target=/var/lib/postgresql/data",
		"--env", "POSTGRES_USER=conductor", "--env", "POSTGRES_DB=conductor", "--env", "POSTGRES_PASSWORD=conductor_restart_test", restartPostgresImage)
	db.resolveURL(t, ctx)
	db.startedAt = waitRestartDatabase(t, ctx, db.url)
	migrations, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatalf("find ordered migrations: %v, err=%v", migrations, err)
	}
	var sql bytes.Buffer
	for _, path := range migrations {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		sql.Write(contents)
		sql.WriteByte('\n')
	}
	// Stdin works with Docker Snap when the checkout is outside its readable paths.
	runRestartCommand(t, ctx, 30*time.Second, "", &sql, "docker", "exec", "-i", db.name, "psql", "-U", "conductor", "-d", "conductor", "--set", "ON_ERROR_STOP=1", "--single-transaction")
	return db
}

func (db *restartDatabase) restart(t *testing.T, ctx context.Context) {
	t.Helper()
	runRestartCommand(t, ctx, 30*time.Second, "", nil, "docker", "stop", "--time", "10", db.name)
	runRestartCommand(t, ctx, 30*time.Second, "", nil, "docker", "start", db.name)
	// Docker may reassign a dynamically published port when restarting a stopped
	// container. Resolve its owned endpoint again before starting the next API.
	db.resolveURL(t, ctx)
	started := waitRestartDatabase(t, ctx, db.url)
	if !started.After(db.startedAt) {
		t.Fatalf("PostgreSQL postmaster did not restart: before=%s after=%s", db.startedAt, started)
	}
	t.Logf("PostgreSQL postmaster restart confirmed: %s -> %s", db.startedAt, started)
	db.startedAt = started
}

func (db *restartDatabase) resolveURL(t *testing.T, ctx context.Context) {
	t.Helper()
	address := strings.TrimSpace(runRestartCommand(t, ctx, 10*time.Second, "", nil, "docker", "port", db.name, "5432/tcp"))
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("expected one isolated localhost database port, got %q: %v", address, err)
	}
	db.url = "postgres://conductor:conductor_restart_test@" + address + "/conductor?sslmode=disable"
}

func waitRestartDatabase(t *testing.T, ctx context.Context, databaseURL string) time.Time {
	t.Helper()
	readyCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var started time.Time
	var lastErr error
	for {
		attemptCtx, attemptCancel := context.WithTimeout(readyCtx, 2*time.Second)
		conn, err := pgx.Connect(attemptCtx, databaseURL)
		if err == nil {
			err = conn.QueryRow(attemptCtx, "SELECT pg_postmaster_start_time()").Scan(&started)
			_ = conn.Close(attemptCtx)
		}
		attemptCancel()
		lastErr = err
		if err == nil {
			return started
		}
		select {
		case <-readyCtx.Done():
			t.Fatalf("isolated PostgreSQL was not ready: %v (last error: %v)", readyCtx.Err(), lastErr)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func cleanupRestartResource(t *testing.T, kind, name, owner string) {
	t.Helper()
	format := `{{index .Labels "conductor.acceptance.restart"}}`
	if kind == "container" {
		format = `{{index .Config.Labels "conductor.acceptance.restart"}}`
	}
	output, err := restartCommand(context.Background(), 10*time.Second, "", nil, "docker", kind, "inspect", "--format", format, name)
	if err != nil {
		// Missing resources need no cleanup. An unavailable daemon is a failure,
		// not permission to remove a resource whose ownership was not established.
		if !strings.Contains(strings.ToLower(output), "no such "+kind) {
			t.Errorf("inspect owned %s %s for cleanup: %v: %s", kind, name, err, output)
		}
		return
	}
	if strings.TrimSpace(output) != owner {
		t.Errorf("refusing to remove %s %s: ownership label mismatch", kind, name)
		return
	}
	args := []string{kind, "rm"}
	if kind == "container" {
		args = append(args, "--force")
	}
	args = append(args, name)
	if output, err := restartCommand(context.Background(), 30*time.Second, "", nil, "docker", args...); err != nil {
		t.Errorf("remove owned %s %s: %v: %s", kind, name, err, output)
	}
}

type restartServer struct {
	cmd     *exec.Cmd
	url     string
	logs    boundedRestartOutput
	done    chan struct{}
	waitErr error
	stopped bool
}

func startRestartServer(t *testing.T, ctx context.Context, binary, databaseURL string) *restartServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	server := &restartServer{cmd: exec.CommandContext(ctx, binary), url: "http://" + address, done: make(chan struct{})}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "DATABASE_URL=") && !strings.HasPrefix(value, "CONDUCTOR_ADDR=") &&
			!strings.HasPrefix(value, "CONDUCTOR_AUTH_MODE=") && !strings.HasPrefix(value, "CONDUCTOR_OIDC_") {
			server.cmd.Env = append(server.cmd.Env, value)
		}
	}
	server.cmd.Env = append(server.cmd.Env, "DATABASE_URL="+databaseURL, "CONDUCTOR_ADDR="+address, "CONDUCTOR_AUTH_MODE=local")
	server.cmd.Stdout, server.cmd.Stderr = &server.logs, &server.logs
	if err := server.cmd.Start(); err != nil {
		t.Fatalf("start conductord child: %v", err)
	}
	go func() {
		server.waitErr = server.cmd.Wait()
		close(server.done)
	}()
	t.Cleanup(func() {
		server.stop(t)
		if t.Failed() {
			t.Logf("conductord PID %d logs:\n%s", server.cmd.Process.Pid, server.logs.String())
		}
	})
	readyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := reviewClient(t, server.url, "synthetic-readiness")
	for {
		select {
		case <-server.done:
			t.Fatalf("conductord exited before readiness: %v\n%s", server.waitErr, server.logs.String())
		default:
		}
		if _, err := c.ListChanges(readyCtx, "", "", 1); err == nil {
			return server
		}
		select {
		case <-readyCtx.Done():
			t.Fatalf("conductord was not ready: %v\n%s", readyCtx.Err(), server.logs.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (server *restartServer) stop(t *testing.T) {
	t.Helper()
	if server.stopped {
		return
	}
	server.stopped = true
	select {
	case <-server.done:
		t.Errorf("conductord exited unexpectedly: %v\n%s", server.waitErr, server.logs.String())
		return
	default:
	}
	if err := server.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("interrupt conductord: %v", err)
	}
	select {
	case <-server.done:
	case <-time.After(5 * time.Second):
		_ = server.cmd.Process.Kill()
		select {
		case <-server.done:
		case <-time.After(5 * time.Second):
			t.Error("conductord did not exit after kill")
		}
	}
	if strings.Contains(server.logs.String(), "WARNING: DATA RACE") {
		t.Errorf("race detected in conductord child:\n%s", server.logs.String())
	}
}

// Commands and diagnostics are bounded even when an opted-in prerequisite fails.
func restartCommand(ctx context.Context, timeout time.Duration, dir string, stdin io.Reader, name string, args ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	cmd.Dir, cmd.Stdin = dir, stdin
	cmd.WaitDelay = 2 * time.Second
	var output boundedRestartOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if commandCtx.Err() != nil {
		err = fmt.Errorf("%s exceeded its deadline: %w", name, commandCtx.Err())
	}
	return output.String(), err
}

func runRestartCommand(t *testing.T, ctx context.Context, timeout time.Duration, dir string, stdin io.Reader, name string, args ...string) string {
	t.Helper()
	output, err := restartCommand(ctx, timeout, dir, stdin, name, args...)
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return output
}

type boundedRestartOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (output *boundedRestartOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	n := len(data)
	remaining := (128 << 10) - output.buf.Len()
	if len(data) > remaining {
		data = data[:remaining]
	}
	_, _ = output.buf.Write(data)
	return n, nil
}

func (output *boundedRestartOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buf.String()
}
