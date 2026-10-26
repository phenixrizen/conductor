package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"time"
)

// backfillWithdrawn fills tasks.withdrawn as a version 3 database becomes
// version 4 (#334; the owner chose this conservative way, 2026-10-25).
// Version 3 kept a marker for only some withdrawals, and continuations
// chain, so the exact history cannot be rebuilt. The subjects reopened in
// a run are seeded from everything it kept, not only its events (a failed
// commit could save rows without them): a task.reopened event, a
// succeeded review attempt whose verdict is needs_changes (its subject
// through the crew as launched), and an attempt withdrawn (its task's
// needs). Every task that needs a seeded subject, transitively, takes its
// highest attempt as withdrawn and loses its repair. So does a
// continuation's task whose ancestor's has any attempt withdrawn, and
// every task that needs it, transitively: version 3 started a
// continuation's first attempt at its ancestor's last checkpoint or
// refusal, withdrawn work among them, every later attempt there may rest
// on that one, and a dependent on its result, so none of them can be told
// fresh. It may drop good work of attempts made after a reopen; it never
// reuses withdrawn work. Each run it changes gets one "migrated" event.
func backfillWithdrawn(ctx context.Context, tx *sql.Tx) error {
	type runInfo struct{ crew, from string }
	runs := map[string]runInfo{}
	var ids []string
	rows, err := tx.QueryContext(ctx, "SELECT id, crew, continued_from FROM runs ORDER BY id")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var ri runInfo
		if err := rows.Scan(&id, &ri.crew, &ri.from); err != nil {
			rows.Close()
			return err
		}
		runs[id] = ri
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	// Each run's values once its ancestor's are known; a loop in the
	// links (never written, but read defensively) ends the walk.
	done := map[string]map[string]int{}
	walking := map[string]bool{}
	var visit func(id string) (map[string]int, error)
	visit = func(id string) (map[string]int, error) {
		if w, ok := done[id]; ok {
			return w, nil
		}
		ri, ok := runs[id]
		if !ok || walking[id] {
			return nil, nil
		}
		walking[id] = true
		parent, err := visit(ri.from)
		if err != nil {
			return nil, err
		}
		w, err := backfillRun(ctx, tx, id, ri.crew, parent)
		if err != nil {
			return nil, err
		}
		done[id] = w
		return w, nil
	}
	for _, id := range ids {
		if _, err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// backfillRun sets one run's tasks' withdrawn (backfillWithdrawn), given
// its ancestor's, and returns every task's value.
func backfillRun(ctx context.Context, tx *sql.Tx, runID, crewJSON string, parent map[string]int) (map[string]int, error) {
	// The crew as launched, as far as the needs go: the ledger reads only
	// these fields of it.
	var crew struct {
		Tasks []struct {
			ID    string   `json:"id"`
			Kind  string   `json:"kind"`
			Needs []string `json:"needs"`
		} `json:"tasks"`
	}
	_ = json.Unmarshal([]byte(crewJSON), &crew) // unreadable: no seeds, the ancestor's values still taken
	needs, kinds := map[string][]string{}, map[string]string{}
	for _, t := range crew.Tasks {
		needs[t.ID], kinds[t.ID] = t.Needs, t.Kind
	}
	seeds := map[string]bool{}
	evs, err := tx.QueryContext(ctx, "SELECT payload FROM events WHERE run_id = ? AND kind = 'task.reopened'", runID)
	if err != nil {
		return nil, err
	}
	for evs.Next() {
		var payload string
		if err := evs.Scan(&payload); err != nil {
			evs.Close()
			return nil, err
		}
		var p struct {
			Task string `json:"task"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil && p.Task != "" {
			seeds[p.Task] = true
		}
	}
	if err := evs.Close(); err != nil {
		return nil, err
	}
	highest := map[string]int{}
	ats, err := tx.QueryContext(ctx, "SELECT task_id, n, state, stage, result FROM attempts WHERE run_id = ?", runID)
	if err != nil {
		return nil, err
	}
	for ats.Next() {
		var task, state, stage, result string
		var n int
		if err := ats.Scan(&task, &n, &state, &stage, &result); err != nil {
			ats.Close()
			return nil, err
		}
		highest[task] = max(highest[task], n)
		var res struct {
			Verdict string `json:"verdict"`
		}
		reopened := state == AttemptSucceeded && kinds[task] == "review" && json.Unmarshal([]byte(result), &res) == nil && res.Verdict == "needs_changes"
		if reopened || stage == "withdrawn" {
			for _, n := range needs[task] {
				seeds[n] = true
			}
		}
	}
	if err := ats.Close(); err != nil {
		return nil, err
	}
	// What needs a seed, and what an ancestor's withdrawal reached, with
	// what needs that, transitively.
	dependent := map[string]bool{}
	for id := range needs {
		if parent[id] > 0 {
			dependent[id] = true
		}
	}
	for grew := true; grew; {
		grew = false
		for id, ns := range needs {
			if dependent[id] {
				continue
			}
			if slices.ContainsFunc(ns, func(n string) bool { return seeds[n] || dependent[n] }) {
				dependent[id], grew = true, true
			}
		}
	}
	type row struct {
		id                  string
		attempts, withdrawn int
	}
	var tasks []row
	trs, err := tx.QueryContext(ctx, "SELECT id, attempts, withdrawn FROM tasks WHERE run_id = ? ORDER BY id", runID)
	if err != nil {
		return nil, err
	}
	for trs.Next() {
		var r row
		if err := trs.Scan(&r.id, &r.attempts, &r.withdrawn); err != nil {
			trs.Close()
			return nil, err
		}
		tasks = append(tasks, r)
	}
	if err := trs.Close(); err != nil {
		return nil, err
	}
	out := map[string]int{}
	changed := map[string]int{}
	for _, r := range tasks {
		w := r.withdrawn
		if dependent[r.id] || parent[r.id] > 0 { // parent too: a task the crew read lacks
			w = max(w, r.attempts, highest[r.id])
		}
		out[r.id] = w
		if w == r.withdrawn {
			continue
		}
		if _, err := tx.ExecContext(ctx, "UPDATE tasks SET withdrawn = ?, repair = '' WHERE run_id = ? AND id = ?", w, runID, r.id); err != nil {
			return nil, err
		}
		changed[r.id] = w
	}
	if len(changed) == 0 {
		return out, nil
	}
	body, err := json.Marshal(map[string]any{"from": 3, "withdrawn": changed})
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO events (run_id, seq, at, kind, payload)
		VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE run_id = ?), ?, 'migrated', ?)`,
		runID, runID, stamp(time.Now()), string(body)); err != nil {
		return nil, err
	}
	return out, nil
}
