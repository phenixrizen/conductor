package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func TestContextSchemaReadinessPreservesReviewBeforeMigration004(t *testing.T) {
	ctx, current, _ := integrationStore(t)
	if err := current.CheckContextSchema(ctx); err != nil {
		t.Fatalf("fully migrated schema is not ready: %v", err)
	}
	// Apply only shipped migrations 001-003 in a second owned schema. No user
	// database or existing development data is downgraded to test the upgrade.
	schema := current.pool.Config().ConnConfig.RuntimeParams["search_path"] + "_pre_context"
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := current.pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	config := current.pool.Config().Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := current.pool.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop pre-context schema: %v", err)
		}
	})
	for _, path := range []string{"001_work_packages.sql", "002_access.sql", "003_browser_sessions.sql"} {
		migration, err := os.ReadFile("../../migrations/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	legacy := &Postgres{pool: pool}
	err = legacy.CheckContextSchema(ctx)
	if !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "004_context_collections.sql") || strings.Contains(err.Error(), schema) || strings.Contains(err.Error(), "SELECT") || len(err.Error()) > 200 {
		t.Fatalf("missing migration did not produce a bounded typed readiness error: %v", err)
	}
	local := service.New(legacy)
	pkg := integrationCreate(t, ctx, local)
	if _, err := local.Submit(ctx, pkg.ID, "author", 1); err != nil {
		t.Fatal(err)
	}
	approved, err := local.Approve(ctx, pkg.ID, "reviewer", 1, pkg.Revision.Digest)
	if err != nil || !approved.Approved {
		t.Fatalf("disabled context broke existing review: %+v %v", approved, err)
	}
	if err := legacy.ApplyAccessConfig(ctx, "synthetic-operator", syntheticAccessConfig()); err != nil {
		t.Fatal(err)
	}
	secure := service.NewAuthenticated(legacy)
	access := accessContext(ctx, "author", "workspace-one", "repo-one")
	if _, err := secure.Create(access, "ignored", domain.Content{"intent": "Authenticated review before context migration"}); err != nil {
		t.Fatalf("disabled context broke authenticated package creation: %v", err)
	}
	if _, err := secure.GetCollection(access, strings.Repeat("a", 32)); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("disabled collection service did not remain unavailable: %v", err)
	}
	migration, err := os.ReadFile("../../migrations/004_context_collections.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if err := legacy.CheckContextSchema(ctx); err != nil {
		t.Fatalf("applied context migration did not satisfy readiness: %v", err)
	}
	recovered, err := local.Get(ctx, pkg.ID)
	if err != nil || !reflect.DeepEqual(recovered, approved) {
		t.Fatalf("context upgrade changed reviewed content or approval: %+v %v", recovered, err)
	}
}
