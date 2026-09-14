package acceptance_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/databaseops"
	"github.com/phenixrizen/conductor/internal/domain"
)

// Exercise the user-facing targets with real Compose, migration CLI, and API
// processes. Every resource belongs to a random project on a selected free port;
// neither the user's local project nor a configured test database is accessed.
func TestLocalMakeMigrationStartup(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_PROCESS_RESTART") != "1" {
		t.Skip("set CONDUCTOR_TEST_PROCESS_RESTART=1 for isolated real make/Compose migration startup")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("local make process acceptance requires Linux process groups")
	}
	for _, name := range []string{"docker", "go", "make"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("local make acceptance requires %s: %v", name, err)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := databaseops.ReadMigrations(filepath.Join(root, "migrations"))
	if err != nil || len(migrations) < 12 {
		t.Fatalf("read complete checked migration sequence: %v", err)
	}
	for _, mode := range []string{"fresh", "tracked-prefix", "legacy-001"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			t.Cleanup(cancel)
			f := newLocalMakeFixture(t, ctx, root)
			var oldLedger, retained string
			var expected domain.Package
			if mode != "fresh" {
				f.compose(t, ctx, "up", "--detach", "--wait")
				waitRestartDatabase(t, ctx, f.url)
				if mode == "tracked-prefix" {
					// A genuine older tracked installation, without rewriting the
					// checkout or deleting recorded rows to simulate a downgrade.
					prefix := t.TempDir()
					for _, m := range migrations[:11] {
						if err := os.WriteFile(filepath.Join(prefix, m.Name), []byte(m.SQL), 0600); err != nil {
							t.Fatal(err)
						}
					}
					f.run(t, ctx, nil, "go", "run", "./cmd/conductor-db", "migrate", "--directory", prefix, "--operator", "synthetic-prefix")
					oldLedger = localMakeQuery(t, ctx, f.url, `SELECT jsonb_agg(to_jsonb(m) ORDER BY number)::text FROM conductor_migrations m`)
				} else {
					// This is exactly the original four-table schema that the old
					// startup helper initialized, not a guessed table-name baseline.
					localMakeSQL(t, ctx, f.url, migrations[0].SQL)
				}
				expected = seedLocalMakeHistory(t, ctx, f.url)
				retained = localMakeHistory(t, ctx, f.url)
			}

			if mode == "legacy-001" {
				out, err := f.command(ctx, nil, "make", "--no-print-directory", "run", "BASELINE=1")
				if err == nil || !strings.Contains(out, "existing untracked database requires an explicitly inspected --baseline") ||
					!strings.Contains(out, "No API was started") || !strings.Contains(out, "make db-migrate BASELINE=") || !strings.Contains(out, "OPERATOR=") {
					t.Fatalf("untracked make run did not fail with explicit recovery instructions: %v\n%s", err, out)
				}
				f.assertAPIStopped(t)
				if localMakeHistory(t, ctx, f.url) != retained || localMakeQuery(t, ctx, f.url, `SELECT (to_regclass('public.conductor_migrations') IS NULL)::text`) != "true" {
					t.Fatal("refused startup changed legacy data or invented migration history")
				}
				// Default db-migrate must refuse too; only a supplied, inspected
				// baseline permits the legacy transition and records its origin.
				if out, err := f.command(ctx, nil, "make", "--no-print-directory", "db-migrate"); err == nil || !strings.Contains(out, "explicitly inspected --baseline") {
					t.Fatalf("db-migrate inferred a default baseline: %v\n%s", err, out)
				}
				f.run(t, ctx, nil, "make", "--no-print-directory", "db-migrate", "BASELINE=1", "OPERATOR=synthetic-inspected-local")
			}

			server := f.start(t, ctx)
			f.assertLedger(t, ctx, migrations, mode)
			if mode == "tracked-prefix" && localMakeQuery(t, ctx, f.url, `SELECT jsonb_agg(to_jsonb(m) ORDER BY number)::text FROM conductor_migrations m WHERE number<=11`) != oldLedger {
				t.Fatal("upgrade rewrote recorded prefix checksums, origin, operator, or timestamps")
			}
			apiClient := reviewClient(t, "http://"+f.address, "synthetic-local-reader")
			if mode == "fresh" {
				expected, err = apiClient.Create(ctx, domain.Content{"intent": "Synthetic package created through make run", "futureExtension": map[string]any{"keep": []any{true, nil, "unchanged"}}})
				if err != nil {
					t.Fatal(err)
				}
				retained = localMakeHistory(t, ctx, f.url)
			} else {
				if localMakeHistory(t, ctx, f.url) != retained {
					t.Fatal("migration changed retained revision content, approvals, or audit history")
				}
				got, err := apiClient.Get(ctx, expected.ID)
				if err != nil || got.Revision.Number != 2 || got.Revision.Digest != expected.Revision.Digest || got.Approved || !reflect.DeepEqual(got.Revision.Content, expected.Revision.Content) {
					t.Fatalf("upgraded API lost exact current revision or made historical approval effective: %+v, %v", got, err)
				}
				history, err := apiClient.Revision(ctx, expected.ID, 1)
				if err != nil || len(history.Approvals) != 1 || history.Approvals[0].Digest != history.Revision.Digest {
					t.Fatalf("upgraded API lost exact historical approval: %+v, %v", history, err)
				}
			}
			ledger := localMakeQuery(t, ctx, f.url, `SELECT jsonb_agg(to_jsonb(m) ORDER BY number)::text FROM conductor_migrations m`)
			postmaster := waitRestartDatabase(t, ctx, f.url)
			server.stop(t)
			f.assertAPIStopped(t)
			f.run(t, ctx, nil, "make", "--no-print-directory", "db-stop")
			f.start(t, ctx)
			if !waitRestartDatabase(t, ctx, f.url).After(postmaster) {
				t.Fatal("make db-stop/run did not restart the selected PostgreSQL postmaster")
			}
			if localMakeHistory(t, ctx, f.url) != retained || localMakeQuery(t, ctx, f.url, `SELECT jsonb_agg(to_jsonb(m) ORDER BY number)::text FROM conductor_migrations m`) != ledger {
				t.Fatal("make db-stop/run changed the persistent volume's data or migration ledger")
			}
			if _, err := apiClient.Get(ctx, expected.ID); err != nil {
				t.Fatalf("retained package unavailable through restarted make run API: %v", err)
			}

			if mode == "fresh" {
				// Even another *owned* database in DATABASE_URL must not redirect
				// startup migrations. The explicit operator target must honor it.
				localMakeSQL(t, ctx, f.url, `CREATE DATABASE unrelated_owned`)
				other := strings.Replace(f.url, "/conductor?", "/unrelated_owned?", 1)
				override := append(append([]string{}, f.env...), "DATABASE_URL="+other)
				f.run(t, ctx, override, "make", "--no-print-directory", "db-up")
				if localMakeQuery(t, ctx, other, `SELECT (to_regclass('public.conductor_migrations') IS NULL)::text`) != "true" {
					t.Fatal("startup migrated the inherited DATABASE_URL instead of its owned Compose endpoint")
				}
				f.run(t, ctx, override, "make", "--no-print-directory", "db-migrate")
				if localMakeQuery(t, ctx, other, `SELECT count(*)::text FROM conductor_migrations WHERE origin='executed' AND operator='local-development'`) != fmt.Sprint(len(migrations)) {
					t.Fatal("explicit db-migrate did not honor DATABASE_URL/default baseline/operator")
				}
			}
			t.Log("actual make/Compose startup, checked migration history, API reads, and persistent stop/start passed:", mode)
		})
	}
}

type localMakeFixture struct {
	root, project, port, url, address string
	env                               []string
	composeInput                      []byte
}

func newLocalMakeFixture(t *testing.T, ctx context.Context, root string) *localMakeFixture {
	t.Helper()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	f := &localMakeFixture{root: root, project: "conductor-make-test-" + hex.EncodeToString(nonce[:])}
	_, f.port, _ = net.SplitHostPort(localMakeAddress(t))
	f.address = localMakeAddress(t)
	f.url = "postgres://conductor:conductor@127.0.0.1:" + f.port + "/conductor?sslmode=disable"
	var err error
	f.composeInput, err = os.ReadFile(filepath.Join(root, "deploy/local/compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "CONDUCTOR_") || strings.HasPrefix(key, "COMPOSE_") || strings.HasPrefix(key, "PG") || key == "DATABASE_URL" || key == "MAKEFLAGS" || key == "MAKEOVERRIDES" || key == "MFLAGS" || key == "BASELINE" || key == "OPERATOR" || key == "GOFLAGS" {
			continue
		}
		f.env = append(f.env, e)
	}
	f.env = append(f.env, "CONDUCTOR_LOCAL_PROJECT="+f.project, "CONDUCTOR_POSTGRES_PORT="+f.port, "CONDUCTOR_ADDR="+f.address, "CONDUCTOR_AUTH_MODE=local", "GOFLAGS=-race")
	// Direct CLI fixture setup needs an explicit URL; make run itself is tested
	// without one below so its documented selected-port default is exercised.
	f.run(t, ctx, nil, "docker", "info", "--format", "{{.ServerVersion}}")
	t.Cleanup(func() {
		// Compose ownership labels are checked before deleting any resource. A
		// random project is reserved by this fixture; never run default-project down.
		for _, kind := range []string{"container", "volume", "network"} {
			list := []string{kind, "ls", "--quiet", "--filter", "label=com.docker.compose.project=" + f.project}
			if kind == "container" {
				list = append(list, "--all")
			}
			out, err := f.command(context.Background(), nil, "docker", list...)
			if err != nil {
				t.Errorf("list owned Compose %s for cleanup: %v: %s", kind, err, out)
				continue
			}
			for _, id := range strings.Fields(out) {
				format := `{{index .Labels "com.docker.compose.project"}}`
				if kind == "container" {
					format = `{{index .Config.Labels "com.docker.compose.project"}}`
				}
				label, err := f.command(context.Background(), nil, "docker", kind, "inspect", "--format", format, id)
				if err != nil || strings.TrimSpace(label) != f.project {
					t.Errorf("refuse cleanup of %s %s with unverified project label: %v", kind, id, err)
					continue
				}
				args := []string{kind, "rm"}
				if kind == "container" {
					args = append(args, "--force")
				}
				if out, err := f.command(context.Background(), nil, "docker", append(args, id)...); err != nil {
					t.Errorf("remove owned %s: %v: %s", kind, err, out)
				}
			}
		}
	})
	return f
}

func localMakeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func (f *localMakeFixture) compose(t *testing.T, ctx context.Context, args ...string) {
	t.Helper()
	f.run(t, ctx, nil, "docker", append([]string{"compose", "--project-name", f.project, "--file", "-"}, args...)...)
}

func (f *localMakeFixture) cmd(ctx context.Context, env []string, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = f.root, f.env
	if env != nil {
		cmd.Env = env
	}
	if name == "go" {
		cmd.Env = append(append([]string{}, cmd.Env...), "DATABASE_URL="+f.url)
	}
	cmd.Stdin = bytes.NewReader(f.composeInput)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func (f *localMakeFixture) command(ctx context.Context, env []string, name string, args ...string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := f.cmd(bounded, env, name, args...)
	var output boundedRestartOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if bounded.Err() != nil {
		err = bounded.Err()
	}
	return output.String(), err
}

func (f *localMakeFixture) run(t *testing.T, ctx context.Context, env []string, name string, args ...string) string {
	t.Helper()
	out, err := f.command(ctx, env, name, args...)
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return out
}

type localMakeProcess struct {
	cmd     *exec.Cmd
	logs    boundedRestartOutput
	done    chan struct{}
	err     error
	stopped bool
}

func (f *localMakeFixture) start(t *testing.T, ctx context.Context) *localMakeProcess {
	t.Helper()
	p := &localMakeProcess{cmd: f.cmd(ctx, nil, "make", "--no-print-directory", "run"), done: make(chan struct{})}
	p.cmd.Stdout, p.cmd.Stderr = &p.logs, &p.logs
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() { p.stop(t) })
	ready, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	c := reviewClient(t, "http://"+f.address, "synthetic-local-readiness")
	for {
		select {
		case <-p.done:
			t.Fatalf("make run exited before API readiness: %v\n%s", p.err, p.logs.String())
		default:
		}
		attempt, end := context.WithTimeout(ready, time.Second)
		_, err := c.ListChanges(attempt, "", "", 1)
		end()
		if err == nil {
			return p
		}
		select {
		case <-ready.Done():
			t.Fatalf("make run API unavailable: %v\n%s", ready.Err(), p.logs.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (p *localMakeProcess) stop(t *testing.T) {
	t.Helper()
	if p.stopped {
		return
	}
	p.stopped = true
	// make and go run both spawn children. Signalling only the direct child can
	// leave an API process serving after the fixture thinks it has stopped.
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("stop owned make process group: %v", err)
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			t.Error("owned make process group did not exit")
		}
	}
	if t.Failed() || strings.Contains(p.logs.String(), "WARNING: DATA RACE") {
		t.Logf("owned make run logs:\n%s", p.logs.String())
	}
	if strings.Contains(p.logs.String(), "WARNING: DATA RACE") {
		t.Error("race reported by make run child")
	}
}

func (f *localMakeFixture) assertAPIStopped(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		l, err := net.Listen("tcp", f.address)
		if err == nil {
			_ = l.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("make left an API listener after refusal/stop: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func localMakeSQL(t *testing.T, ctx context.Context, url, sql string, args ...any) {
	t.Helper()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if _, err := c.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
}

func localMakeQuery(t *testing.T, ctx context.Context, url, sql string) string {
	t.Helper()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var value string
	if err := c.QueryRow(ctx, sql).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func localMakeHistory(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	// Explicit legacy projections exclude new ownership columns introduced by
	// migration002 while retaining every original persisted field and timestamp.
	return localMakeQuery(t, ctx, url, `SELECT jsonb_build_object(
	 'changes',(SELECT jsonb_agg(jsonb_build_object('id',id,'created_at',created_at) ORDER BY id) FROM changes),
	 'revisions',(SELECT jsonb_agg(to_jsonb(r) ORDER BY change_id,revision) FROM work_package_revisions r),
	 'approvals',(SELECT jsonb_agg(to_jsonb(a) ORDER BY change_id,revision,reviewer) FROM approvals a),
	 'events',(SELECT jsonb_agg(to_jsonb(e) ORDER BY sequence) FROM audit_events e))::text`)
}

func seedLocalMakeHistory(t *testing.T, ctx context.Context, url string) domain.Package {
	t.Helper()
	id := "CHG-1111111111111111"
	localMakeSQL(t, ctx, url, `INSERT INTO changes(id,created_at) VALUES($1,'2026-01-01T00:00:00Z')`, id)
	var current domain.Package
	for revision := int64(1); revision <= 2; revision++ {
		content := domain.Content{"intent": fmt.Sprintf("Synthetic local migration revision %d", revision), "futureExtension": map[string]any{"retain": []any{"original", true, nil}}}
		digest, err := domain.Digest(content)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(content)
		if err != nil {
			t.Fatal(err)
		}
		localMakeSQL(t, ctx, url, `INSERT INTO work_package_revisions(change_id,revision,schema_version,digest,content,author,created_at,submitted_at) VALUES($1,$2,1,$3,$4,'synthetic-local-author','2026-01-01T00:00:00Z','2026-01-01T00:00:01Z')`, id, revision, digest, body)
		if revision == 1 {
			localMakeSQL(t, ctx, url, `INSERT INTO approvals(change_id,revision,digest,reviewer,created_at) VALUES($1,1,$2,'synthetic-local-reviewer','2026-01-01T00:00:02Z')`, id, digest)
		}
		current = domain.Package{ID: id, Revision: domain.Revision{Number: revision, Digest: digest, Content: content}}
	}
	for _, event := range []struct {
		kind     string
		revision int64
	}{{"package.created", 1}, {"review.requested", 1}, {"review.approved", 1}, {"package.revised", 2}, {"review.requested", 2}} {
		actor := "synthetic-local-author"
		if event.kind == "review.approved" {
			actor = "synthetic-local-reviewer"
		}
		localMakeSQL(t, ctx, url, `INSERT INTO audit_events(change_id,event_type,actor,revision,data,created_at) VALUES($1,$2,$3,$4,'{"synthetic":"retained"}','2026-01-01T00:00:03Z')`, id, event.kind, actor, event.revision)
	}
	return current
}

func (f *localMakeFixture) assertLedger(t *testing.T, ctx context.Context, migrations []databaseops.Migration, mode string) {
	t.Helper()
	var got []struct {
		Number                         int
		Name, Digest, Operator, Origin string
	}
	text := localMakeQuery(t, ctx, f.url, `SELECT jsonb_agg(to_jsonb(m) ORDER BY number)::text FROM conductor_migrations m`)
	if err := json.Unmarshal([]byte(text), &got); err != nil || len(got) != len(migrations) {
		t.Fatalf("complete migration ledger unavailable: %v, %s", err, text)
	}
	for i, m := range migrations {
		operator, origin := "local-development", "executed"
		if mode == "tracked-prefix" && m.Number <= 11 {
			operator = "synthetic-prefix"
		}
		if mode == "legacy-001" {
			operator = "synthetic-inspected-local"
			if m.Number == 1 {
				origin = "operator_baseline"
			}
		}
		if got[i].Number != m.Number || got[i].Name != m.Name || got[i].Digest != m.Digest || got[i].Operator != operator || got[i].Origin != origin {
			t.Fatalf("migration %d lacks exact checked identity and truthful origin: %+v", m.Number, got[i])
		}
	}
}
