package dispatch

import (
	"context"
	"database/sql"
)

// Version is the schema this binary writes. A database of a later version
// opens read-only; an earlier one is brought to it (migrations).
const Version = 1

// migrations bring a database from the version keyed to the next, in the
// transaction that read the version, with the version's bump; version 1
// is the first, so there is none. A later migration follows the ledger's
// shape and the conservative rule: it never drops or reinterprets what an
// older version kept; a value it cannot derive is left at its default and
// said in one changes row of op migrate per entity it touched, so a later
// reader can tell derived from recorded.
var migrations = map[int][]string{}

// migrationsGo run after a version's statements, in the same transaction:
// what SQL alone cannot work out.
var migrationsGo = map[int]func(context.Context, *sql.Tx) error{}

// schema is version 1, whole: every table of docs/plans/dispatch.md's
// schema table, the FTS index included, its indexes, and the unique
// indexes of the keys never minted twice and nothing else (projects.key,
// tickets.key and (project_id, number), people.name, changes (actor,
// op_id, n), ops (actor, id), beside every row's own id); every other
// uniqueness the plan's table names (a live label's name in its project,
// one live relation per (from_id, to_id, kind), one live link per
// (ticket_id, kind, ref)) is a rule checked on a local write inside the
// operation's transaction, so that no index refuses a replicated add: the
// plan's "Field rules for replication" table, which rules.go is, says
// what a merge makes of a second one.
//
// Conventions: times are RFC 3339 text in UTC with nine fractional digits
// always (stamp), so at orders as text; JSON is text; rev is on every row
// with a scalar field that can change (projects, tickets, labels,
// milestones, comments, views), written 0 by a create and bumped by every
// operation that writes a changes row for the row, the create's included,
// so every entity's rev starts at 1; the rows of a set (ticket_assignees,
// ticket_labels, relations, links, attachments) are added and removed,
// never edited, and carry none: their ticket's rev is bumped instead;
// deleted_at is on every row with an id of its own, and a row is live
// while it is empty; an empty reference is none. tickets_fts is written
// by the store in the transaction that writes the rows it indexes (PR-4),
// its rowid the ticket's rowid (tickets' id is text, so the table has an
// implicit one, which only VACUUM could renumber and the store never
// runs). changes.seq is local to this database: the event's and the
// history's paging cursor, never replicated; a change row's identity
// across replicas is (actor, op_id, n), and the one order of the log is
// (at, actor, op_id, n).
var schema = []string{
	`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
	`CREATE TABLE projects (
		id TEXT PRIMARY KEY,
		key TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '',
		tracker TEXT NOT NULL DEFAULT 'native',
		tracker_url TEXT NOT NULL DEFAULT '',
		close_requires TEXT NOT NULL DEFAULT '[]',
		next_number INTEGER NOT NULL DEFAULT 1,
		number_from INTEGER NOT NULL DEFAULT 0,
		number_to INTEGER NOT NULL DEFAULT 0,
		archived_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		rev INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE people (
		name TEXT PRIMARY KEY,
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE tickets (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(id),
		number INTEGER NOT NULL,
		key TEXT NOT NULL UNIQUE,
		title TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		acceptance TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'backlog',
		priority INTEGER NOT NULL DEFAULT 0,
		horizon TEXT NOT NULL DEFAULT '',
		owner TEXT NOT NULL DEFAULT '',
		parent_id TEXT NOT NULL DEFAULT '',
		milestone_id TEXT NOT NULL DEFAULT '',
		accepted_at TEXT NOT NULL DEFAULT '',
		accepted_by TEXT NOT NULL DEFAULT '',
		accepted_for TEXT NOT NULL DEFAULT '',
		accepted_criteria TEXT NOT NULL DEFAULT '',
		accepted_note TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT 'native',
		external_id TEXT NOT NULL DEFAULT '',
		external_url TEXT NOT NULL DEFAULT '',
		external_snapshot TEXT NOT NULL DEFAULT '',
		authority TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		created_by TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL,
		closed_at TEXT NOT NULL DEFAULT '',
		deleted_at TEXT NOT NULL DEFAULT '',
		rev INTEGER NOT NULL DEFAULT 0,
		UNIQUE (project_id, number)
	)`,
	`CREATE TABLE ticket_assignees (
		ticket_id TEXT NOT NULL REFERENCES tickets(id),
		actor TEXT NOT NULL,
		added_at TEXT NOT NULL,
		PRIMARY KEY (ticket_id, actor)
	)`,
	`CREATE TABLE labels (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(id),
		name TEXT NOT NULL,
		color TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		deleted_at TEXT NOT NULL DEFAULT '',
		rev INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE ticket_labels (
		ticket_id TEXT NOT NULL REFERENCES tickets(id),
		label_id TEXT NOT NULL REFERENCES labels(id),
		PRIMARY KEY (ticket_id, label_id)
	)`,
	`CREATE TABLE milestones (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(id),
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL DEFAULT 'open',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		deleted_at TEXT NOT NULL DEFAULT '',
		rev INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE relations (
		id TEXT PRIMARY KEY,
		from_id TEXT NOT NULL REFERENCES tickets(id),
		to_id TEXT NOT NULL REFERENCES tickets(id),
		kind TEXT NOT NULL,
		created_at TEXT NOT NULL,
		created_by TEXT NOT NULL DEFAULT '',
		deleted_at TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE comments (
		id TEXT PRIMARY KEY,
		ticket_id TEXT NOT NULL REFERENCES tickets(id),
		author TEXT NOT NULL,
		by TEXT NOT NULL DEFAULT '',
		body TEXT NOT NULL,
		created_at TEXT NOT NULL,
		edited_at TEXT NOT NULL DEFAULT '',
		deleted_at TEXT NOT NULL DEFAULT '',
		rev INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE attachments (
		id TEXT PRIMARY KEY,
		ticket_id TEXT NOT NULL REFERENCES tickets(id),
		name TEXT NOT NULL,
		size INTEGER NOT NULL,
		sha256 TEXT NOT NULL,
		content_type TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		created_by TEXT NOT NULL DEFAULT '',
		deleted_at TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE uploads (
		id TEXT PRIMARY KEY,
		sha256 TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE links (
		id TEXT PRIMARY KEY,
		ticket_id TEXT NOT NULL REFERENCES tickets(id),
		kind TEXT NOT NULL,
		ref TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		created_by TEXT NOT NULL DEFAULT '',
		deleted_at TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE views (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		query TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		deleted_at TEXT NOT NULL DEFAULT '',
		rev INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE changes (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		entity TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		ticket_id TEXT NOT NULL DEFAULT '',
		project_id TEXT NOT NULL DEFAULT '',
		field TEXT NOT NULL DEFAULT '',
		op TEXT NOT NULL,
		value TEXT NOT NULL DEFAULT '',
		prior TEXT NOT NULL DEFAULT '',
		at TEXT NOT NULL,
		actor TEXT NOT NULL,
		by TEXT NOT NULL DEFAULT '',
		op_id TEXT NOT NULL,
		n INTEGER NOT NULL,
		UNIQUE (actor, op_id, n)
	)`,
	`CREATE TABLE ops (
		actor TEXT NOT NULL,
		id TEXT NOT NULL,
		intent TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		status TEXT NOT NULL,
		receipt TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		PRIMARY KEY (actor, id)
	)`,
	`CREATE VIRTUAL TABLE tickets_fts USING fts5(key, title, description, acceptance, comments, tokenize = 'unicode61 remove_diacritics 2')`,
	`CREATE INDEX tickets_project_status_updated ON tickets (project_id, status, updated_at)`,
	`CREATE INDEX tickets_project_priority ON tickets (project_id, priority)`,
	`CREATE INDEX tickets_milestone ON tickets (milestone_id)`,
	`CREATE INDEX tickets_parent ON tickets (parent_id)`,
	`CREATE INDEX tickets_owner ON tickets (owner)`,
	`CREATE INDEX tickets_deleted ON tickets (deleted_at)`,
	`CREATE INDEX ticket_assignees_actor ON ticket_assignees (actor)`,
	`CREATE INDEX relations_to ON relations (to_id)`,
	`CREATE INDEX comments_ticket_created ON comments (ticket_id, created_at)`,
	`CREATE INDEX changes_entity ON changes (entity, entity_id, seq)`,
	`CREATE INDEX changes_ticket ON changes (ticket_id, seq)`,
	`CREATE INDEX links_kind_ref ON links (kind, ref)`,
	`CREATE INDEX attachments_sha256 ON attachments (sha256)`,
	`CREATE INDEX uploads_sha256 ON uploads (sha256)`,
}

// tables is every table of the schema but the FTS index's own and
// SQLite's, by name: what a test lists sqlite_master against.
var tables = []string{
	"attachments", "changes", "comments", "labels", "links", "milestones", "ops", "people", "projects",
	"relations", "schema_version", "ticket_assignees", "ticket_labels", "tickets", "tickets_fts", "uploads", "views",
}
