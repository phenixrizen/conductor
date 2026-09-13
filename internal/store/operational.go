package store

import (
	"context"
	"errors"
	"github.com/phenixrizen/conductor/internal/operational"
)

// OperationalSnapshot is trusted, global and source-free. It never participates
// in command authorization. Each statement uses a two-second operator deadline.
func (p *Postgres) OperationalSnapshot(ctx context.Context) (operational.Snapshot, error) {
	out := operational.Snapshot{Queues: []operational.Queue{}}
	if err := p.pool.Ping(ctx); err != nil {
		return out, errors.New("database unavailable")
	}
	tables := []struct{ kind, table, created string }{
		{"context", "context_outbox", "available_at"}, {"coordination", "coordination_outbox", "created_at"}, {"publication", "delivery_outbox", "created_at"}, {"tracker", "tracker_sync_outbox", "created_at"}, {"runtime", "runtime_outbox", "created_at"},
	}
	for _, table := range tables {
		var present bool
		if err := p.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table.table).Scan(&present); err != nil || !present {
			return out, errors.New("release schema unavailable")
		}
		q := operational.Queue{Kind: table.kind}
		// Table/column identifiers come exclusively from the fixed list above.
		sql := `SELECT count(*) FILTER(WHERE delivered_at IS NULL AND unresolved_at IS NULL),count(*) FILTER(WHERE unresolved_at IS NOT NULL),COALESCE(GREATEST(0,extract(epoch FROM clock_timestamp()-min(` + table.created + `) FILTER(WHERE delivered_at IS NULL AND unresolved_at IS NULL))),0)::double precision FROM ` + table.table
		if err := p.pool.QueryRow(ctx, sql).Scan(&q.Pending, &q.Unresolved, &q.OldestSeconds); err != nil {
			return out, errors.New("outbox diagnostics unavailable")
		}
		out.Queues = append(out.Queues, q)
	}
	out.Ready = true
	return out, nil
}
