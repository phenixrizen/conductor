// Package databaseops contains trusted operator operations. It is never called
// by the public API or repository-controlled workers.
package databaseops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type Migration struct {
	Number            int
	Name, Digest, SQL string
}

var migrationName = regexp.MustCompile(`^([0-9]{3})_[a-z0-9_]+\.sql$`)

func ReadMigrations(directory string) ([]Migration, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, errors.New("migration directory unavailable")
	}
	out := []Migration{}
	for _, entry := range entries {
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil, errors.New("migration must be a bounded regular file")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("migration unreadable")
		}
		sum := sha256.Sum256(body)
		number, _ := strconv.Atoi(match[1])
		out = append(out, Migration{number, entry.Name(), hex.EncodeToString(sum[:]), string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	if len(out) == 0 || len(out) > 999 {
		return nil, errors.New("migration list is empty or too large")
	}
	for i, m := range out {
		if m.Number != i+1 {
			return nil, errors.New("migrations must have unique consecutive numbers starting at 001")
		}
	}
	return out, nil
}

// Migrate holds an advisory lock and commits new schema plus its checksums in one
// transaction. A legacy baseline is an explicit operator assertion, never guessed
// from a table name or silently treated as verified historical execution.
func Migrate(ctx context.Context, url string, migrations []Migration, baseline int, operator string) error {
	if baseline < 0 || baseline > len(migrations) || len(operator) == 0 || len(operator) > 128 || strings.ContainsAny(operator, "\r\n\x00") {
		return errors.New("invalid migration operator or baseline")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return errors.New("migration database unavailable")
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("migration transaction unavailable")
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(616065732114114)`); err != nil {
		return errors.New("migration lock unavailable")
	}
	var ledger, existing bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('public.conductor_migrations') IS NOT NULL, EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname!='conductor_migrations' AND c.relkind IN ('r','p','v','m','S','f'))`).Scan(&ledger, &existing); err != nil {
		return errors.New("migration schema inspection failed")
	}
	var recorded int
	if ledger {
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM public.conductor_migrations`).Scan(&recorded); err != nil {
			return errors.New("migration ledger unavailable")
		}
	}
	if recorded > 0 && baseline != 0 {
		return errors.New("baseline cannot replace recorded migration history")
	}
	if recorded == 0 && existing && baseline == 0 {
		return errors.New("existing untracked database requires an explicitly inspected --baseline")
	}
	if recorded == 0 && !existing && baseline != 0 {
		return errors.New("an empty database cannot claim a legacy baseline")
	}
	if _, err = tx.Exec(ctx, `SET LOCAL search_path TO public,pg_catalog`); err != nil {
		return errors.New("migration schema selection failed")
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.conductor_migrations(number integer PRIMARY KEY CHECK(number>0),name text NOT NULL UNIQUE,digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),operator text NOT NULL,origin text NOT NULL CHECK(origin IN ('executed','operator_baseline')),recorded_at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		return errors.New("migration ledger unavailable")
	}
	rows, err := tx.Query(ctx, `SELECT number,name,digest FROM public.conductor_migrations ORDER BY number`)
	if err != nil {
		return errors.New("migration ledger read failed")
	}
	applied := 0
	for rows.Next() {
		var number int
		var name, digest string
		if rows.Scan(&number, &name, &digest) != nil {
			rows.Close()
			return errors.New("migration ledger unreadable")
		}
		if number != applied+1 || number > len(migrations) || migrations[number-1].Name != name || migrations[number-1].Digest != digest {
			rows.Close()
			return errors.New("migration ledger differs from inspected release")
		}
		applied++
	}
	rows.Close()
	if rows.Err() != nil {
		return errors.New("migration ledger read failed")
	}
	for _, m := range migrations[applied:] {
		origin := "executed"
		if m.Number <= baseline {
			origin = "operator_baseline"
		} else if _, err = tx.Exec(ctx, m.SQL); err != nil {
			return fmt.Errorf("migration %03d failed; transaction rolled back", m.Number)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO public.conductor_migrations(number,name,digest,operator,origin) VALUES($1,$2,$3,$4,$5)`, m.Number, m.Name, m.Digest, operator, origin); err != nil {
			return errors.New("migration ledger write failed")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("migration commit outcome uncertain; inspect ledger before retry")
	}
	return nil
}
