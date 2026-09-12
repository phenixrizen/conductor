package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

// Each test owns a schema, so an explicitly configured test database can also
// host other test runs. Closing and reopening a pool does not restart PostgreSQL.
func integrationStore(t *testing.T) (context.Context, *Postgres, func() *Postgres) {
	t.Helper()
	url := os.Getenv("CONDUCTOR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set CONDUCTOR_TEST_DATABASE_URL to run live PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if err := admin.Ping(ctx); err != nil {
		t.Fatalf("connect to configured PostgreSQL: %v", err)
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "conductor_test_" + hex.EncodeToString(random[:])
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 4
	open := func() *Postgres {
		t.Helper()
		pool, err := pgxpool.NewWithConfig(ctx, config.Copy())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		if err := pool.Ping(ctx); err != nil {
			t.Fatalf("connect to isolated schema: %v", err)
		}
		return &Postgres{pool: pool}
	}
	p := open()
	migrations, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatalf("find migrations: files=%v err=%v", migrations, err)
	}
	// Glob returns sorted paths; migration names encode their application order.
	for _, path := range migrations {
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", path, err)
		}
	}
	return ctx, p, open
}

func integrationCreate(t *testing.T, ctx context.Context, s *service.Service) domain.Package {
	t.Helper()
	pkg, err := s.Create(ctx, "author", domain.Content{
		"intent":      "Review a synthetic package",
		"futureField": map[string]any{"enabled": true, "items": []any{"preserve", "these"}},
	})
	if err != nil {
		t.Fatalf("create package: %v", err)
	}
	return pkg
}

func integrationCounts(t *testing.T, ctx context.Context, p *Postgres) [4]int {
	t.Helper()
	var counts [4]int
	err := p.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM changes),
		(SELECT count(*) FROM work_package_revisions),
		(SELECT count(*) FROM approvals),
		(SELECT count(*) FROM audit_events)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3])
	if err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestPostgresReviewLifecycle(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	s := service.New(p)
	created := integrationCreate(t, ctx, s)
	if created.Revision.Number != 1 || created.Revision.SubmittedAt != nil || created.Approved || created.Approval != nil {
		t.Fatalf("unexpected initial package: %+v", created)
	}
	digest, err := domain.Digest(created.Revision.Content)
	if err != nil || digest != created.Revision.Digest {
		t.Fatalf("persisted content/digest mismatch: digest=%s err=%v", digest, err)
	}
	if _, err := s.Approve(ctx, created.ID, "reviewer", 1, digest); !errors.Is(err, domain.ErrNotSubmitted) {
		t.Fatalf("unsubmitted approval: got %v", err)
	}
	if _, err := s.Submit(ctx, created.ID, "author", 2); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale submission: got %v", err)
	}
	submitted, err := s.Submit(ctx, created.ID, "author", 1)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if submitted.Revision.SubmittedAt == nil || submitted.Approved {
		t.Fatalf("unexpected submitted package: %+v", submitted)
	}
	if _, err := s.Submit(ctx, created.ID, "author", 1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate submission: got %v", err)
	}
	if _, err := s.Approve(ctx, created.ID, "author", 1, digest); !errors.Is(err, domain.ErrSelfApproval) {
		t.Fatalf("self approval: got %v", err)
	}
	if _, err := s.Approve(ctx, created.ID, "reviewer", 1, strings.Repeat("0", 64)); !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatalf("wrong digest approval: got %v", err)
	}
	approved, err := s.Approve(ctx, created.ID, "reviewer", 1, digest)
	if err != nil {
		t.Fatalf("independent approval: %v", err)
	}
	if !approved.Approved || approved.Approval == nil || approved.Approval.Reviewer != "reviewer" || approved.Approval.Revision != 1 || approved.Approval.Digest != digest {
		t.Fatalf("unexpected approved package: %+v", approved)
	}
	revised, err := s.Revise(ctx, created.ID, "author", 1, domain.Content{"intent": "Revised synthetic intent"})
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if revised.Revision.Number != 2 || revised.Revision.Digest == digest || revised.Revision.SubmittedAt != nil || revised.Approved || revised.Approval != nil {
		t.Fatalf("revision did not invalidate approval: %+v", revised)
	}
	if _, err := s.Approve(ctx, created.ID, "reviewer", 1, digest); !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatalf("approval of superseded revision: got %v", err)
	}
	if _, err := s.Revise(ctx, created.ID, "author", 1, domain.Content{"intent": "Stale edit"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale revision: got %v", err)
	}
	var original domain.Content
	var originalDigest string
	var submittedAt *time.Time
	err = p.pool.QueryRow(ctx, `SELECT content,digest,submitted_at FROM work_package_revisions WHERE change_id=$1 AND revision=1`, created.ID).Scan(&original, &originalDigest, &submittedAt)
	if err != nil || !reflect.DeepEqual(original, created.Revision.Content) || originalDigest != digest || submittedAt == nil {
		t.Fatalf("historical revision was changed: content=%v digest=%s submitted=%v err=%v", original, originalDigest, submittedAt, err)
	}
	if got := integrationCounts(t, ctx, p); got != [4]int{1, 2, 1, 4} {
		t.Fatalf("history counts (changes/revisions/approvals/events): %v", got)
	}
	rows, err := p.pool.Query(ctx, `SELECT event_type,actor,revision,COALESCE(data->>'digest','') FROM audit_events WHERE change_id=$1 ORDER BY sequence`, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type event struct {
		kind, actor string
		revision    int64
		digest      string
	}
	var events []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.kind, &e.actor, &e.revision, &e.digest); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []event{{"package.created", "author", 1, ""}, {"review.requested", "author", 1, ""}, {"review.approved", "reviewer", 1, digest}, {"package.revised", "author", 2, ""}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("audit history: got %+v, want %+v", events, want)
	}
}

func TestPostgresConcurrentRevisions(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	s := service.New(p)
	created := integrationCreate(t, ctx, s)
	type result struct {
		pkg domain.Package
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, intent := range []string{"Concurrent edit A", "Concurrent edit B"} {
		go func() {
			<-start
			pkg, err := s.Revise(ctx, created.ID, "author", 1, domain.Content{"intent": intent})
			results <- result{pkg, err}
		}()
	}
	close(start)
	var winner domain.Package
	var successes, conflicts int
	for range 2 {
		r := <-results
		switch {
		case r.err == nil:
			successes++
			winner = r.pkg
		case errors.Is(r.err, domain.ErrConflict):
			conflicts++
		default:
			t.Errorf("concurrent revise: %v", r.err)
		}
	}
	if successes != 1 || conflicts != 1 || winner.Revision.Number != 2 {
		t.Fatalf("expected one revision-2 winner and one conflict: successes=%d conflicts=%d winner=%+v", successes, conflicts, winner)
	}
	current, err := s.Get(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(current, winner) {
		t.Fatalf("persisted winner: got %+v, err=%v, want %+v", current, err, winner)
	}
	if got := integrationCounts(t, ctx, p); got != [4]int{1, 2, 0, 2} {
		t.Fatalf("concurrent edit persisted extra facts: %v", got)
	}
}

func TestPostgresMissingPackage(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	s := service.New(p)
	commands := map[string]func() error{
		"inspect": func() error {
			_, err := s.Get(ctx, "CHG-missing")
			return err
		},
		"revise": func() error {
			_, err := s.Revise(ctx, "CHG-missing", "author", 1, domain.Content{"intent": "Missing package"})
			return err
		},
		"submit": func() error {
			_, err := s.Submit(ctx, "CHG-missing", "author", 1)
			return err
		},
		"approve": func() error {
			_, err := s.Approve(ctx, "CHG-missing", "reviewer", 1, strings.Repeat("0", 64))
			return err
		},
	}
	for name, command := range commands {
		if err := command(); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s missing package: got %v, want ErrNotFound", name, err)
		}
	}
	if got := integrationCounts(t, ctx, p); got != [4]int{} {
		t.Fatalf("missing package commands persisted facts: %v", got)
	}
}

func TestPostgresAuditFailureRollsBack(t *testing.T) {
	for _, command := range []string{"create", "revise", "submit", "approve"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			ctx, p, _ := integrationStore(t)
			s := service.New(p)
			var before domain.Package
			if command != "create" {
				before = integrationCreate(t, ctx, s)
			}
			if command == "approve" {
				var err error
				before, err = s.Submit(ctx, before.ID, "author", before.Revision.Number)
				if err != nil {
					t.Fatal(err)
				}
			}
			counts := integrationCounts(t, ctx, p)
			if _, err := p.pool.Exec(ctx, `ALTER TABLE audit_events ADD CONSTRAINT fail_test_audit CHECK (actor <> 'audit-failure')`); err != nil {
				t.Fatal(err)
			}
			var err error
			switch command {
			case "create":
				_, err = s.Create(ctx, "audit-failure", domain.Content{"intent": "Rolled back creation"})
			case "revise":
				_, err = s.Revise(ctx, before.ID, "audit-failure", before.Revision.Number, domain.Content{"intent": "Rolled back revision"})
			case "submit":
				_, err = s.Submit(ctx, before.ID, "audit-failure", before.Revision.Number)
			case "approve":
				_, err = s.Approve(ctx, before.ID, "audit-failure", before.Revision.Number, before.Revision.Digest)
			}
			var databaseError *pgconn.PgError
			if !errors.As(err, &databaseError) || databaseError.Code != "23514" || databaseError.ConstraintName != "fail_test_audit" {
				t.Fatalf("expected injected audit constraint failure, got %v", err)
			}
			if got := integrationCounts(t, ctx, p); got != counts {
				t.Fatalf("failed %s persisted partial facts: got %v, want %v", command, got, counts)
			}
			if command != "create" {
				after, err := s.Get(ctx, before.ID)
				if err != nil || !reflect.DeepEqual(after, before) {
					t.Fatalf("failed %s changed current package: got %+v, err=%v, want %+v", command, after, err, before)
				}
			}
		})
	}
}

func TestPostgresPoolReopenDurability(t *testing.T) {
	t.Parallel()
	ctx, p, reopen := integrationStore(t)
	s := service.New(p)
	created := integrationCreate(t, ctx, s)
	if _, err := s.Submit(ctx, created.ID, "author", 1); err != nil {
		t.Fatal(err)
	}
	approved, err := s.Approve(ctx, created.ID, "reviewer", 1, created.Revision.Digest)
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	p = reopen()
	s = service.New(p)
	recovered, err := s.Get(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(recovered, approved) {
		t.Fatalf("approval after pool reopen: got %+v, err=%v, want %+v", recovered, err, approved)
	}
	revised, err := s.Revise(ctx, created.ID, "author", 1, domain.Content{"intent": "Revise after reconnect"})
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	p = reopen()
	recovered, err = service.New(p).Get(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(recovered, revised) || recovered.Approved || recovered.Approval != nil {
		t.Fatalf("invalidation after pool reopen: got %+v, err=%v, want %+v", recovered, err, revised)
	}
	if got := integrationCounts(t, ctx, p); got != [4]int{1, 2, 1, 4} {
		t.Fatalf("history after pool reopen: %v", got)
	}
}
