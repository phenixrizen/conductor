# Dispatch: projects, tickets and the views over them (Program item DS1)

Dispatch is Conductor's own work tracking: projects, tickets with stable
keys, their discussion and their history, kept in a SQLite file of their
own in the data directory and shown as list, board and roadmap views in
the workbench. This plan is Program item DS1 (the Program, section 4.3),
written against `main` at b6d017df (PR #381, 2026-10-28), after reading
the store, the crew engine, the ledger, the API, the palette and the pages
the item touches. It is reviewed before any of its code; the PRs at the
end are cut into issues after the review, one issue, one branch `issue-N`,
one PR each (AGENTS.md).

The names follow the railroad (the Program, section 1.2): Conductor is the
application, Yardmaster the agent harness that runs a crew's tasks,
Dispatch the ticketing and project tracking component, and its items are
tickets. "Workspace" is never used here for a worktree. Dispatch owns what
was asked and what became of it; Yardmaster's ledger stays the truth of
execution, and Dispatch links to it rather than copying it.

Later Program items this plan leaves room for, in plain words: DS2 links a
ticket to the runs, candidates, checks and findings that worked on it;
IN1a imports and links GitHub issues with the user's own token; DR1 and
HM1 add durable decision records and people as crew members; CS1
replicates a team's Dispatch between machines; BK1 lets Brakeman draft
tickets; C6 adds `conductor export`, a zip of the data directory.

## Articles it serves

1. No licence check: Dispatch has no plan, tier or provider check; every
   route is the admin's and works the same on every build.
2. The open-source build is fully useful on its own: a team can run its
   backlog in Dispatch with no account and no network.
3. Nothing is gated by a flag: there is no switch that turns a paid part
   on; there is no paid part.
4. Inspect and export stay free: every Dispatch read is a `GET` the
   workbench token answers; the store is two plain things in the data
   directory (one SQLite file, one directory of attachment files), so the
   export of item C6 carries it whole and a restore reads it back.
5. Dependencies point one way: `internal/dispatch` imports the standard
   library and `modernc.org/sqlite`; nothing private, nothing new.
6. No feature build tag: a plain `go build` carries Dispatch.
7. The free default of every interface is what main does today: Dispatch
   touches none of the eight interfaces; it offers `Store.OnChange`, the
   seam C6's continuity store hooks into, and calls no provider.
8. Not touched: no hosted dependency exists in this item, so there is
   nothing to answer as the free default during an outage.
9. `CGO_ENABLED=0` stays: the store is `modernc.org/sqlite`, SQLite
   translated to Go, as the ledger's is (checked on 2026-10-28: the pinned
   v1.61.0 reports SQLite 3.54.1 with FTS5 and JSON1 compiled in).
10. The repository contract holds: this plan before the code; race tests
    per package; vitest and Playwright for the UI; no new dependency, so
    `make notices` has nothing to regenerate; no protocol frame changes,
    so `internal/proto` and `protocol.ts` are untouched and `docs/
    protocol.md` gains the HTTP rows and one event row.
11. The railroad names keep their meaning: the component is Dispatch, its
    items are tickets, a run's tasks stay Yardmaster's; no word of the
    repo's is reused for another thing.
12. Execution stays behind one seam: Dispatch starts no process, opens no
    session and runs no check; section "Boxcar constraints" says so item
    by item.

## Why and what happens

Today the repository tracks its work in GitHub issues, and a run's tasks
live only inside the managed run that made them: nothing in Conductor
holds a durable objective, its acceptance criteria, who is accountable for
it, the discussion around it and what became of it (the Program, section
1.4). Dispatch is that record. It is local first: one SQLite file the
server owns, no account, no network, written and read on the machine that
runs `conductor serve`, so an individual developer has it on day one and a
team shares it later through replication (Program item CS1, decision D3),
not through a hosted service.

After this item ships, a person sees:

- **Dispatch** in the sidebar's foot (`G D`; `Alt+D` from a terminal),
  listing the projects. A project has a short key (`CND`), a name, a
  description, a repository folder, a close policy and either a native
  tracker or a pointer at an external one (a GitHub repository's URL, say).
- A project's tickets as a **list** (fast, filtered and sorted on the
  server, pages of 200), a **board** (one column per status, cards moved by
  drag or by the keyboard) and a **roadmap** (Now, Next, Later), the filter
  in the URL, saved views as chips, and the keys Linear-style ticketing
  taught people: `j` and `k`, `Enter`, `c` for a new ticket, `s` `p` `a`
  `l` `m` `o` for status, priority, assignee, label, milestone and owner.
- A **ticket page** at `/dispatch/CND/12`: `CND-12` and its title, the
  description and the acceptance criteria as Markdown with a preview, the
  status, priority and Now/Next/Later horizon, the accountable owner (a
  person) apart from the assignees (people or agents), labels, milestone,
  parent and children, what it blocks and what blocks it, comments,
  attachments, links (a pull request, a release, a run), and its history
  as the store recorded it, field by field.
- The **close policy** on the page: "To close: the owner's acceptance ·
  a delivery link", each with its state, and a Close that is refused, in
  words, until the policy is met. Acceptance is its own fact, recorded by
  the operator for the owner, never implied by a status.
- The **run Tasks view** inside Dispatch: a ticket linked to a managed run
  shows that run's tasks on its page, with the same rows the run page
  has, and the run page says which ticket it belongs to. Launching a run
  from a ticket, with its snapshot in the attempt's task file, is Program
  item DS2; here the link is made by hand or by the CLI.
- Tickets in the **command palette**: a key or a title typed finds the
  ticket, `>` offers "New ticket in CND", and the Dispatch pages have
  their own group of rows.
- `conductor dispatch` for the shell and two MCP tools for an attempt
  whose run is linked to a ticket: read the ticket, comment on it.

Every change is a typed operation with an id the client mints and a
receipt the store keeps (the Program, section 1.5), the way a managed
run's commands already work, so a reply lost to the network is asked for
again without a second change, and the same code serves the UI, the CLI
and the MCP tools.

## The changes

### `internal/dispatch` (new): the store

One package, no HTTP, no sessions: the API, the CLI and the tests are its
callers. It is the second importer of `modernc.org/sqlite` after
`internal/crew/ledger` (`docs/dependencies.md` says so; the ledger is not
touched). Files: `dispatch.go` (open, pragmas, the schema check, close,
the WAL checkpoint, `OnChange`), `schema.go` (version 1 and the migration
table), `ids.go`, `model.go` (the row types and every bound), `ops.go`
(the typed operation, its receipt and the changes log), `rules.go` (the
field classes and the merge rules, pure), `projects.go`, `tickets.go`,
`comments.go`, `labels.go`, `milestones.go`, `people.go`, `relations.go`,
`links.go`, `views.go`, `query.go` (the list, its filters, its cursor,
full-text search), `attachments.go` (the files), `sweep.go` (orphans and
old receipts); a test beside each.

**Where.** `<dataDir>/dispatch/dispatch.sqlite`, mode 0600, and
`<dataDir>/dispatch/attachments/<sha256>` (0600, content addressed, the
first two hex characters as a sub-directory). The data directory is
already refused to every session's file reads (`config.FileDenyFunc`), so
nothing an agent does reaches the store.

**Open.** `dispatch.Open(dir)` makes `dir` (0700) when missing, refuses a
database file that is a symbolic link, opens with the ledger's pragmas
(`busy_timeout(5000)`, `foreign_keys(1)`, `synchronous(FULL)`,
`journal_mode(WAL)`, on darwin `fullfsync(1)` and
`checkpoint_fullfsync(1)`, `_txlock=immediate`, `_time_format=sqlite`),
one connection (`SetMaxOpenConns(1)`), runs `PRAGMA quick_check`, and
brings the schema to `Version` (1 here) or opens read-only: a database a
newer Conductor wrote, or one that fails its check, is read when it can
be and never written, and `ReadOnly()` says why. `Open` fails only when
the directory cannot be made or the file is a link. There is no owner
lock (see Owner decisions, 2): SQLite's own locking orders writers across
processes, and every revision is read and bumped inside the transaction
that uses it. `Close` checkpoints the WAL (`wal_checkpoint(TRUNCATE)`)
and closes; `Checkpoint(ctx)` does the first alone, for C6's export of a
running server. `OnChange(func(Change))` is called after every commit with
the global sequence, the entity and its id; the API turns it into the SSE
event below, C6's continuity store will hook it too.

**Ids and keys.** Every row has an opaque id of 16 lower-case hex
characters from 8 random bytes (`crypto/rand`, the shape of
`session.NewID`, without importing `internal/session`). A project also has
a key, `^[A-Z][A-Z0-9]{1,5}$`, unique, chosen at creation and never
changed; a ticket has a number, allocated from `projects.next_number` in
the transaction that creates it, and its key is `<project key>-<number>`
(`CND-12`), unique, never reassigned, kept by a soft delete. Two columns
reserved for replication, `number_from` and `number_to` (0 in this item),
let CS1 hand each replica a block of numbers so concurrent creation never
collides (Owner decisions, 7).

**Schema, version 1** (`schema.go`; times are RFC 3339 text in UTC with
nanoseconds; JSON is text; every row that can change carries `rev`,
bumped by every operation that touches it):

| Table | Columns (bounds in `model.go`) |
|---|---|
| `schema_version` | `version` |
| `projects` | `id` PK, `key` UNIQUE, `name` (1 to 60 characters, no control characters), `description` (Markdown, ≤ 16 KiB), `cwd` (≤ 4,096 bytes, a path kept as text, resolved only when something launches from it, which is DS2's), `tracker` (`native` or `external`), `tracker_url` (≤ 2 KiB, an `http` or `https` URL, with `external` only), `close_requires` (JSON array of `acceptance` and `delivery`; `verified` is refused until DS2 defines it), `next_number`, `number_from`, `number_to`, `archived_at`, `created_at`, `updated_at`, `rev` |
| `people` | `id` PK, `name` UNIQUE (1 to 60 characters), `created_at`: the directory the owner and person pickers offer; a name typed that is not there is added (decision D4: people are local labels until identity exists) |
| `tickets` | `id` PK, `project_id` FK, `number`, `key` UNIQUE, `title` (1 to 200 characters), `description` (Markdown, ≤ 64 KiB), `acceptance` (Markdown, ≤ 16 KiB), `status`, `priority` (0 to 4), `horizon` (`''`, `now`, `next`, `later`), `owner` (a person's name or `''`), `parent_id` (a ticket of the same project or `''`), `milestone_id`, `accepted_at`, `accepted_by`, `accepted_note` (≤ 1,000 characters), `source` (`native`; IN1a adds its own), `external_id`, `external_url`, `external_snapshot`, `authority` (the last four `''` here, reserved for IN1a so it adds no column), `created_at`, `created_by`, `updated_at`, `closed_at`, `deleted_at`, `rev`; UNIQUE(`project_id`, `number`) |
| `ticket_assignees` | `ticket_id`, `actor` (`person:<name>` or `agent:<catalog id>`), `added_at`; PK of the two; at most 8 per ticket |
| `labels` | `id` PK, `project_id`, `name` (1 to 40 characters), `color` (one of the twelve names the UI maps to the brand's status scales, never a hex value from the client), `created_at`, `rev`; UNIQUE(`project_id`, `name`); at most 100 per project |
| `ticket_labels` | `ticket_id`, `label_id`; PK of the two; at most 20 per ticket |
| `milestones` | `id` PK, `project_id`, `name` (1 to 60), `description` (≤ 4 KiB), `target` (`YYYY-MM-DD` or `''`), `state` (`open` or `closed`), `created_at`, `updated_at`, `rev`; at most 100 per project |
| `relations` | `id` PK, `from_id`, `to_id`, `kind` (`blocks` or `relates`), `created_at`, `created_by`; UNIQUE(`from_id`, `to_id`, `kind`); both tickets of one project; at most 100 per ticket; "depends on" is `blocks` read from the other end |
| `comments` | `id` PK, `ticket_id`, `seq` (1 from each ticket's first), `author` (an actor as above, with `by`, the display name), `body` (Markdown, ≤ 16 KiB), `created_at`, `edited_at`, `deleted_at`, `rev`; UNIQUE(`ticket_id`, `seq`); at most 2,000 per ticket |
| `attachments` | `id` PK, `ticket_id`, `name` (1 to 255 bytes, no `/`, `\`, NUL or control character, not `.` or `..`), `size`, `sha256`, `content_type` (as given, ≤ 100 bytes; served only when on the allow list below), `created_at`, `created_by`, `deleted_at`; at most 50 per ticket, 16 MiB each |
| `links` | `id` PK, `ticket_id`, `kind` (`delivery`, `run`, `external`; DS2 adds `plan`, `candidate`, `check`, `finding`, DR1 `decision`), `ref` (≤ 200 bytes: a run id, a PR number), `url` (≤ 2 KiB, `http` or `https`), `note` (≤ 1,000), `created_at`, `created_by`, `deleted_at`; UNIQUE(`ticket_id`, `kind`, `ref`); at most 50 per ticket |
| `views` | `id` PK, `project_id` (`''` for every project), `name` (1 to 60), `kind` (`list`, `board`, `roadmap`), `query` (JSON, ≤ 8 KiB, the filter, sort and grouping of `query.go`), `created_at`, `updated_at`, `rev`; at most 200 |
| `changes` | `seq` INTEGER PRIMARY KEY AUTOINCREMENT, `entity` (`project`, `ticket`, `comment`, `label`, `milestone`, `relation`, `link`, `attachment`, `view`, `person`), `entity_id`, `field`, `op` (`set`, `add`, `remove`, `create`, `delete`), `value` (JSON, ≤ 64 KiB), `prior` (JSON, the value before a `set`), `at`, `actor`, `by`, `op_id`: the operation log, the ticket's history and what CS1 replicates |
| `ops` | `actor`, `id`, `intent`, `fingerprint`, `status` (`done` or `failed`), `receipt` (JSON, ≤ 64 KiB), `created_at`; PK(`actor`, `id`); the newest 20,000 kept |
| `tickets_fts` | FTS5 over `key`, `title`, `description`, `acceptance` and the ticket's comments, maintained by the store in the same transaction as the rows it indexes (a comment added or edited rewrites its ticket's entry); the `unicode61` tokenizer with `remove_diacritics 2` |

Indexes: `tickets(project_id, status, updated_at)`, `tickets(project_id,
priority)`, `tickets(milestone_id)`, `tickets(parent_id)`,
`tickets(owner)`, `tickets(deleted_at)`, `ticket_assignees(actor)`,
`relations(to_id)`, `comments(ticket_id, seq)`, `changes(entity,
entity_id, seq)`, `links(kind, ref)`, `attachments(sha256)`.

Vocabularies: `status` is `backlog`, `ready`, `in_progress`, `in_review`,
`done`, `cancelled` (six, fixed in this item; Owner decisions, 4);
`priority` is `none` 0, `low` 1, `medium` 2, `high` 3, `urgent` 4;
`horizon` is empty, `now`, `next`, `later`.

**Operations** (`ops.go`). Every write is `Apply(ctx, op Op, intent
string, args any, change func(*tx) error)`: `Op{Actor, ID,
ExpectedRev}`, the id `^[A-Za-z0-9._~-]{1,32}$` as the ledger's command
ids, the fingerprint the SHA-256 of the intent and the arguments as JSON.
One transaction: the receipt is looked up (the same id and content again
answers it with `repeat: true`; other content is `op_conflict`); the
entity's `rev` is compared with `ExpectedRev` when one is given
(`stale_revision`, nothing changed); the change is made; one `changes` row
per field it set, added or removed; `rev` is bumped; the receipt is
written; the commit; then `OnChange`. A refusal (the store's rules:
`not_found`, `invalid`, `stale_revision`, `close_policy`, `cycle`,
`bound`, `archived`) is a failed receipt written in a transaction with
nothing else, so the same id answers it again, as a refused task command
is kept; a failure of the database itself, or a read-only store, writes
nothing, and `op_conflict` is answered without a row. One transaction
suffices where the ledger's commands take two: Dispatch has no side
effect outside the database but an attachment's file, which is written
before its row (see below). The receipt is `{opId, intent, status, rev, repeat?, error?,
refusal?, ticket?}` with the refusals `not_found`, `invalid`,
`stale_revision`, `op_conflict`, `close_policy`, `cycle`, `bound`,
`read_only`, `archived`. The intents: `project.create`, `project.edit`,
`project.archive`, `project.unarchive`; `ticket.create`, `ticket.edit`
(any of title, description, acceptance, status, priority, horizon, owner,
parent, milestone, and `add`/`remove` of assignees and labels, in one
operation), `ticket.delete`, `ticket.undelete`, `ticket.accept`,
`ticket.unaccept`; `comment.add`, `comment.edit`, `comment.delete`;
`label.create`, `label.edit`, `label.delete`; `milestone.create`,
`milestone.edit`, `milestone.delete`; `person.add`; `relation.add`,
`relation.remove`; `link.add`, `link.remove`; `attachment.add`,
`attachment.remove`; `view.create`, `view.edit`, `view.delete`.

**Rules** (`rules.go`): the field classes of the table in "Events,
orderings and invariants" as code, `Class(entity, field)`, and
`Merge(local, remote Change) Winner`, pure and tested here, used by CS1
later; the schema comment names the table.

**Queries** (`query.go`): `List(ctx, Query) (Page, error)` with `Query{
Project, Status[], Priority[], Labels[], Owner, Assignees[], Milestone,
Horizon[], Parent, Blocked bool, Q string, Sort (updated, created,
priority, status, title, key, milestone), Desc bool, Limit (1 to 200),
Cursor}`: one SQL statement from the filter, the FTS5 match joined when
`Q` is set (≤ 200 characters, `"` and `*` quoted so a typed query is words,
never FTS syntax), the cursor the last row's sort key and id, encoded
opaque; the page carries summaries without the description. `Ticket(ctx,
id)` reads one with its assignees, labels, relations both ways, links,
attachments and the last 50 comments; `History(ctx, id, after, limit ≤
500)` pages the changes; `Children`, `Blockers`. A benchmark,
`BenchmarkList`, over 10,000 tickets in three projects: the targets to
measure in PR-3 and name in its body are a filtered, sorted page under
20 ms and a full-text page under 50 ms on the Linux box; a target missed
is tuned in that PR (an index, the statement), not waived.

**Attachments** (`attachments.go`): `Add(ctx, op, ticket, name,
contentType, r io.Reader) (Receipt, Attachment, error)` reads at most
16 MiB + 1 (`bound` past it), into a temp file in `attachments/` while
hashing, fsyncs, renames it to its hash (a file already there is the same
content; the temp file goes), then runs the operation that writes the row;
a crash between the rename and the commit leaves a file no row names,
which `sweep.go` deletes at open. `Remove` deletes the row (a tombstone)
and the file when no live row shares the hash. `Open(ctx, id)` gives the
row and an `*os.File` of the content, opened by its hash, never by a
client's path.

### `internal/api`: the routes and the event

`server.go`: a `dispatch *dispatch.Store` field, opened in `New` beside
the ledger (`openDispatch(st, log)`: logged and left nil when the
directory cannot be made; read-only logged with why), closed in `Shutdown`
and `WaitBackground` after the ledger; `s.dispatch.OnChange` wired to the
event hub. Routes, in `dispatch.go` and `dispatch_attachments.go`, every
one `s.launching(...)` (the workbench token; `403 switchyard` on a
switchyard, which keeps no tickets): without a data directory `503
store_unavailable`; a store opened read-only answers every write `503
dispatch_unavailable` with why and every read as it can. Bodies decode as
the other routes do (`DisallowUnknownFields`), 64 KiB but where the table
says more; every write takes `{opId, expectedRev?, byName?}` beside its fields and
answers `200 {receipt, <entity>}`, `201` on a create; a refused operation
is `409 {error, receipt}` with the refusal as the code, as the task
commands answer; `400 invalid_request` for a body the store would not
even look at (a bad op id, a bad key).

| Route | Body and reply |
|---|---|
| `GET /api/dispatch/projects?archived=1` | `{projects}`, each with `open` and `done` counts |
| `POST /api/dispatch/projects` | `{opId, key, name, description?, cwd?, tracker?, trackerUrl?, closeRequires?}` → `201 {receipt, project}`; `409 key_taken` |
| `GET /api/dispatch/projects/{key}` | `{project, labels, milestones, views}` |
| `POST /api/dispatch/projects/{key}/edit`, `…/archive`, `…/unarchive` | `{opId, expectedRev?, …}` → `{receipt, project}` |
| `POST /api/dispatch/projects/{key}/labels`, `…/labels/{id}/edit`, `…/labels/{id}/delete` | `{opId, name, color}`; a deleted label leaves every ticket |
| `POST /api/dispatch/projects/{key}/milestones`, `…/milestones/{id}/edit`, `…/milestones/{id}/delete` | `{opId, name, description?, target?, state?}`; a deleted milestone leaves every ticket |
| `GET /api/dispatch/people`, `POST /api/dispatch/people` | `{people}`; `{opId, name}` |
| `GET /api/dispatch/tickets?project=&status=&priority=&label=&owner=&assignee=&milestone=&horizon=&parent=&blocked=&q=&sort=&desc=&limit=&cursor=` | `{tickets, next?, total}` (repeatable parameters as `status=a&status=b`; `limit` 1 to 200, default 100; `400 invalid_request` for a bad value) |
| `POST /api/dispatch/tickets` | `{opId, project, title, description?, acceptance?, status?, priority?, horizon?, owner?, assignees?, labels?, milestone?, parent?}` (192 KiB) → `201 {receipt, ticket}` |
| `GET /api/dispatch/tickets/{key}` | `{ticket}` in full: assignees, labels, relations both ways, children, links, attachments, the last 50 comments, the close policy's state (`close: {requires, met: {acceptance, delivery}, missing}`) |
| `POST /api/dispatch/tickets/{key}/edit` | `{opId, expectedRev?, set?: {…}, add?: {assignees?, labels?}, remove?: {…}}` (192 KiB) → `{receipt, ticket}`; a `set.status` of `done` refused by the policy is `409 close_policy` with `missing` in the receipt; a `set.parent` that would cycle or pass depth 8 `409 cycle` |
| `POST …/{key}/accept`, `…/unaccept` | `{opId, note?}`: records `accepted_at`, `accepted_by` (the admin's display name, `by`) and the note; `unaccept` clears them |
| `POST …/{key}/delete`, `…/undelete` | a tombstone; a deleted ticket is listed only with `?deleted=1`, its key kept |
| `GET …/{key}/history?after=&limit=` | `{changes, next?}` |
| `GET …/{key}/comments?after=&limit=` | `{comments}` beyond the page the ticket read carries |
| `POST …/{key}/comments`, `…/comments/{id}/edit`, `…/comments/{id}/delete` | `{opId, body}`; the author is the admin's display name, recorded as `actor: admin`, `by: <name>` |
| `POST …/{key}/relations`, `…/relations/{id}/delete` | `{opId, kind, to}` (`to` a key of the same project); a `blocks` that would cycle `409 cycle` |
| `POST …/{key}/links`, `…/links/{id}/delete` | `{opId, kind, ref?, url?, note?}`: `delivery` needs `url`; `run` needs `ref`, a run the engine holds or has a record of (`404 not_found` otherwise); `external` needs `url` |
| `GET /api/dispatch/links?run={id}` | `{tickets}`: the tickets a run is linked to, for the run page's chip |
| `POST …/{key}/attachments` | `multipart/form-data`, one part `file` (its filename the name, its type the content type; 16 MiB plus 64 KiB of envelope, `413 bound` past it) → `201 {receipt, attachment}` |
| `GET /api/dispatch/attachments/{id}` | the content, `Content-Disposition: attachment; filename*=…`, `X-Content-Type-Options: nosniff`, `Content-Type` the stored type when it is `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `text/plain`, `application/pdf` or `application/json`, else `application/octet-stream` (an SVG is served as a download, never inline, since it can script); `Content-Length` |
| `POST …/attachments/{id}/delete` | `{opId}` |
| `GET /api/dispatch/views?project=`, `POST /api/dispatch/views`, `…/{id}/edit`, `…/{id}/delete` | `{views}`; `{opId, project?, name, kind, query}` |
| `GET /api/dispatch/ops/{opId}` | a receipt of the admin's without acting, as `GET /api/runs/{run}/commands/{command}` does; `404` when unknown |

The admin's operations are kept under the actor `admin`, as the task
commands are; `by` carries the display name the workbench sends
(`?by=` is never used: the name travels in the body as `byName` does for a
broadcast).

`events.go`: `dispatch(seq int64, entity, id string)` queues `event:
dispatch` with `{"seq", "entity", "id"}` for every client, bounded by
`maxDispatchEvent = 128` bytes (a test asserts the longest payload fits),
as droppable as `activity` (a client past `activityQueueLimit` misses it
and keeps its stream; a page re-reads what it shows on its next read),
never a sink's: webhooks and the run engine do not see it.

`docs/protocol.md`: the rows above in the HTTP API table, a "Dispatch
operations" paragraph beside "Commands with receipts", and the `dispatch`
row in Events. `docs/repository-map.md`: a row for `internal/dispatch`.
`docs/architecture.md`: a "Dispatch" section after "Crew runs" and a
paragraph under "Persistence".

### `internal/cli`, `internal/agents`: the CLI, the tools, the skill

`cli/dispatch.go`: `conductor dispatch projects`, `tickets [--project KEY]
[--status S]... [--q TEXT] [--json]`, `show KEY-n`, `create --project KEY
--title T [--description-file F] [--priority P] [--owner NAME] [--label
L]...`, `edit KEY-n [--status S] [--priority P] [--owner NAME] [--assignee
+x|-x]... [--label +l|-l]... [--milestone NAME] [--horizon H]`, `comment
KEY-n TEXT...`, `accept KEY-n [--note TEXT]`, `link KEY-n --kind
delivery|run|external [--url U] [--ref R]`, with `--server` and `--token`
as `conductor crews` takes them (the plain-`http` refusal included); each
write mints its op id and asks once more on a lost reply, as the web does.
`cli/task.go`: `conductor task ticket` prints the ticket this attempt's run
is linked to, `conductor task ticket comment TEXT...` comments on it as
`agent:<member>`. `cli/mcp.go`: `ticket_read` and `ticket_comment` over
the same two routes, `GET /api/sessions/{id}/ticket` and `POST
/api/sessions/{id}/ticket/comments` (the session's own token or the
admin's, through `taskSession`; `404 no_ticket` when the session's run is
linked to none). The usage in `root.go`, the completion table, and the
skill's new "Your ticket" section (`agents/skill.go`, with its test). No
agent creates or edits a ticket in this item (Owner decisions, 13).

### `web/app`: the pages, the views, the palette

- `composables/useDispatch.ts`: the client over `useApi` with the types of
  every reply (as `useSessions.ts` holds the crews' and runs'), and
  `useDispatchOps.ts`: a minted op id, `expectedRev` from the entity
  shown, a lost reply asked for once with the same id, a refusal read from
  the receipt (`sendCommand` of `utils/tasks.ts`, generalised to take the
  op body, used by both), toasts in the task commands' words.
- `composables/useDispatchLive.ts`: the `dispatch` events, taken in
  `useAttention.handleBlock` beside `run`, handed to listeners; a page
  re-reads the entity it shows when its id is named, the list it shows at
  most once per 250 ms (`RUN_EVENT_WINDOW_MS`'s rule), and never polls.
- `utils/dispatch.ts` (vitest): the key parser (`CND-12`), the
  vocabularies with their words, colours and icons, `ticketWords`, the
  close policy's words from `close`, the list's sort and the board's
  columns, the roadmap's columns, the keyboard model of the list (a roving
  focus by key, as the palette's rows). `utils/dispatchQuery.ts`: the
  filter to and from the URL (`?status=in_progress&label=api&q=…`) and to
  and from a saved view's `query`, one codec both ways.
- `pages/dispatch/index.vue`: the projects (key, name, open and done
  counts, the tracker) and New project (a dialog: key, name, folder with
  `DirInput`, tracker, close policy). `pages/dispatch/[project]/index.vue`:
  the views (`?view=list|board|roadmap`, kept per browser as the run
  page keeps its view), the filter bar (`DispatchFilters.vue`: status,
  priority, labels, owner, assignees, milestone, horizon, blocked, a
  search box), the saved views as chips (`DispatchViewsBar.vue`: save,
  rename, delete), `DispatchTicketList.vue` (rows: key, title, status,
  priority, owner, assignees as avatar chips, labels, milestone, updated;
  `x` selects, the page's palette group offers "Set status of 3
  selected…" and the like, one operation per ticket), `DispatchBoard.vue`
  (a column per status, cards with key, title, priority, labels and
  assignees, moved by HTML5 drag and drop or by `←` `→` on a focused
  card, a move to Done refused by the policy with the policy's words,
  the card snapping back), `DispatchRoadmap.vue` (Now, Next, Later and
  a folded Not planned, moved the same way; the milestones beside with
  `done/total`). `pages/dispatch/[project]/[number].vue`: the ticket page
  (`DispatchTicketPage.vue` with `TicketMarkdown.vue`, a textarea and
  the existing `MarkdownPreview.vue` with no folder and relative links
  disabled, `attachment:<id>` images resolved to object URLs the page
  fetched with the token, since an `img` cannot carry a header;
  `DispatchProperties.vue`, `DispatchComments.vue`,
  `DispatchAttachments.vue`, `DispatchRelations.vue`,
  `DispatchHistory.vue`, `DispatchClosePolicy.vue` with Accept and Close;
  `DispatchRuns.vue` listing the linked runs with `RunTasks.vue` embedded
  for the run the live store holds or its record). Pickers:
  `PersonPicker.vue` (the people, a new name added), `AssigneePicker.vue`
  (people and the catalog's agents, the agent chip as the sidebar's),
  `LabelPicker.vue`, `MilestonePicker.vue`, `TicketPicker.vue` (parent,
  relations: a search over the project's tickets by key and title).
- `layouts/default.vue`: Dispatch in `nav` (`i-lucide-ticket`, after
  Crews), `g-d` and `alt_d` (`KeyD` added to `ALT_PASSTHROUGH_CODES`, so
  `Alt+D` in a terminal reaches the page as `Alt+C` does; `GLOBAL_SHORTCUTS`
  gains the row). `useShortcuts.ts`: `DISPATCH_SHORTCUTS` (the list's and
  the page's keys above), registered by the Dispatch pages.
- `pages/runs/[run].vue` and `CrewRunHeader.vue`: a chip "CND-12" when
  `GET /api/dispatch/links?run=` names a ticket, opening its page.
- The palette (`usePaletteRows.ts`, `utils/palette.ts`): a Tickets group
  in the search (the open tickets of every project, read after the first
  paint and cached a minute as the crews are, read again on a `dispatch`
  event while the palette is open; a key or a word of the title matches;
  `→` offers Open, Copy key, Set status…, Set priority…, Assign to me),
  `>` gains "New ticket in <project>" per project and "Go to Dispatch",
  `go dispatch` joins the `PAGES` of the `go` verb, and the Dispatch pages
  register their group (the views, New ticket, the selection's actions).
  No new prefix and no new verb (Owner decisions, 11).
- Phone widths: the list is rows that wrap, the board scrolls sideways,
  the ticket page is one column; nothing of the desktop layout decides for
  the phone.
- No new npm package: drag and drop is the browser's, Markdown is the
  editor's renderer, icons are the Lucide set already bundled.

### Docs and the rest

`README.md`: a "Dispatch" section in its voice (the pages, the keys, the
CLI). `docs/features.md`: the entry, in the last PR, recording what was
verified. `AGENTS.md`: Dispatch added to the railroad-names line. No
config key, no workflow change, no change under `desktop/`.

## Events, orderings and invariants

**The store.**

- One connection; every operation one `BEGIN IMMEDIATE … COMMIT`; a
  reader never sees an operation half done.
- `changes.seq` is monotonic and gapless within the database; it is the
  cursor the SSE event carries and the order CS1 replays.
- Every entity's `rev` starts at 1 and is bumped by every operation that
  writes a `changes` row for it, inside that operation; `expectedRev`
  compares with it before anything changes.
- A ticket's `key` and `number` never change, not by a delete, a project
  rename (the project's key cannot change) or a move (a ticket is never
  moved between projects: it is cancelled and recreated, and a `relates`
  link may say so).
- Deletes are tombstones (`deleted_at`): a deleted ticket, comment,
  attachment or link keeps its row and its history; lists leave it out
  unless asked; a hard delete exists for nothing in this item.
- `tickets_fts` is written in the transaction that writes the row it
  indexes; a read never finds a ticket the search does not, nor the
  reverse.
- An attachment's file is on disk, fsynced and named by its hash before
  the row that names it is committed; the sweep at open deletes a file no
  live or tombstoned row names; a remove deletes the file only when no
  other row names the hash.
- The receipt is written in the operation's transaction, a refusal's in a
  transaction with nothing else; a database failure writes none; a repeat
  answers the receipt, a conflicting repeat `op_conflict`; the newest 20,000 receipts are kept (older ones go at
  open and once a day), so only a resend older than 20,000 later
  operations could run twice.
- `OnChange` runs after the commit, outside the transaction, on the
  caller's goroutine; the event hub's `dispatch` never waits.
- The store refuses a write while read-only; a read works whenever the
  database opens.

**Status.** `backlog`, `ready`, `in_progress`, `in_review`, `done`,
`cancelled`. Any status may follow any other except that `done` is
entered only when the project's `close_requires` is met: `acceptance`
needs `accepted_at` set, `delivery` needs a live `delivery` link. A move
to `done` or `cancelled` sets `closed_at`; a move out of them clears it.
Acceptance is a fact of its own: `accept` sets it, `unaccept` clears it,
no status change touches it, and a ticket may be accepted while open or
closed. The policy's state is computed on every ticket read (`close`),
never stored, so a policy edited later reads true at once.

**Hierarchy and relations.** A parent is a ticket of the same project; a
parent chain is at most 8 deep and never a cycle, checked in the
operation by walking up the chain (at most 8 reads). `blocks` edges never
form a cycle, checked by a depth-first walk from `to` under the
transaction (a project's blocks graph is small; the walk is bounded by
the 100 relations per ticket and the tickets of the project). A ticket
whose blockers are not all `done` or `cancelled` is `blocked` in the list
filter and on its page; nothing stops its status from changing (a person
decides), the page only says so.

**The SSE event.** `event: dispatch`, `data: {"seq":N,"entity":"ticket",
"id":"…"}`, at most 128 bytes, after the commit; a page re-reads the
entity when the id is the one it shows, a list re-reads itself (debounced
250 ms) when the entity is a ticket of its project; a client that misses
events (dropped and reconnected) re-reads what it shows on the stream's
next `snapshot`, as the runs are read again. No `internal/proto` and no
`protocol.ts` change: this is an HTTP event, documented in
`docs/protocol.md`'s Events section with its bound and tested in
`api/events_test.go`.

**Migrations.** Version 1 is new: `prepare` makes the schema whole in one
transaction. Later versions follow the ledger's shape, `migrations[v]`
statements and `migrationsGo[v]` steps in one transaction with the
version bump, and the conservative rule the owner set for the ledger on
2026-10-25: a migration never drops or reinterprets what an older
version kept; a value it cannot derive is left at its default and said
in one `changes` row of `op: migrate` per entity it touched, so a later
reader can tell derived from recorded. A database of a later version
opens read-only.

**Field rules for replication (O10).** Every `changes` row is an
operation on one field of one entity; the row tables are the state those
rows make. CS1 ships `changes` rows between replicas and applies each by
its field's class, below; the rules are `rules.go`'s `Class` and `Merge`,
tested there, and this table is their text.

| Class | Fields | Rule when two replicas changed the same field |
|---|---|---|
| immutable | every `id`, `project_id`, `number`, `key`, `created_at`, `created_by`, a comment's `seq`, a change's `seq` | never written twice; a replica that receives a second `create` of an id it has drops it |
| scalar | `title`, `description`, `acceptance`, `status`, `priority`, `horizon`, `owner`, `parent_id`, `milestone_id`, `accepted_at`, `accepted_by`, `accepted_note`, `closed_at`, `deleted_at`, `external_*`, `authority`; a project's `name`, `description`, `cwd`, `tracker`, `tracker_url`, `close_requires`, `archived_at`; a label's `name`, `color`; a milestone's fields; a view's fields; a comment's `body`, `edited_at`, `deleted_at` | last writer wins per field: the later `at` wins; equal, the greater `actor`; equal, the greater `op_id`. A field is merged alone: concurrent edits of `title` and `priority` both survive |
| set | `ticket_assignees`, `ticket_labels`, `relations`, `links`, `attachments` | add wins over a remove that did not see it: an `add` carries its `at`; a `remove` carries the `at` of the add it removes, and applies only to that add; an add made later stays (an observed-remove set) |
| log | `comments`, `changes` | append only, ordered by (`at`, `actor`, `op_id`); never merged, never lost |
| allocation | `number` | taken from the replica's own block (`number_from` to `number_to`), which the sync service hands out, so two replicas never mint one key; a replica with no block creates no ticket until it has one |
| gated | `status` set to `done` | the writer applied the close policy; a merge takes the scalar rule and never re-applies the policy, since the facts it rests on travel in the same log; the page shows the policy's state as read |

`at` is the writer's clock, UTC with nanoseconds; CS1 may replace it with
a hybrid logical clock without changing a rule, since every rule compares
`at`, `actor` and `op_id` and nothing else. The schema reserves nothing
more for it: a replica's identity is its `actor` prefix, which CS1
defines.

## Out of scope

- Launching a crew from a ticket, the launch package in an attempt's task
  file, plan revisions, candidates, checks and findings on the ticket
  page, and the reserved link kinds filled in (Program item DS2); human
  review and repair (RV1); importing or publishing GitHub issues and the
  per-field authority of an external tracker (IN1a: this item records
  `source`, `external_*` and `authority` as empty columns and shows an
  external project as such); decision records (DR1); people as crew
  members with identities (HM1, ID1); replication (CS1: this item writes
  the rules and the log it needs); Brakeman drafting tickets (BK1); the
  `verified` close requirement (DS2).
- Custom statuses or workflows per project, manual ordering of cards (a
  rank), cycles or sprints, estimates and points, time tracking, ticket
  templates, recurring tickets, a trash with restore beyond `undelete`,
  bulk import, search inside attachments, inline SVG attachments.
- Tickets for share-link viewers: every Dispatch route is the admin's; a
  guest's join page shows none.
- Routing `dispatch` events to notifications, the Roundhouse or webhooks:
  the event feeds the pages alone.
- A shared SQLite helper package for the ledger and Dispatch, and a shared
  owner lock (Owner decisions, 2 and 3).
- Anything under `desktop/`, any config key, any protocol frame.

## Tests per surface

Per `docs/checks.md`: a Go change runs `make lint`, `go test -race
-count=1 ./...` (the whole module) and `make lint-static`; a UI change
runs `npm --prefix web run typecheck`, `npm --prefix web test` and `make
test-e2e` (which builds the web app and the binary first; the spec alone
while iterating, after `make web-build build-go`); a PR spanning both runs
both; a docs-only PR runs nothing and says so. No Python script changes,
so the scripts suite is not run by this item. `make preflight` before the
last push of every PR; the Mac and Windows rows (`python3
scripts/preflight.py --machines mac,windows`) on PR-6 and PR-11, the last
Go PRs of each half, and on PR-10, the last UI PR. A check that did not
run is reported as not run.

**Go, race, `internal/dispatch`** (each fails without its part; the
timing-free ones `-count=1`, the sweep and the pruning `-count=30`):

- open and schema: a new database made whole; version 1 reads back; a
  database marked version 2 opens read-only and a write is `read_only`
  with the words; a damaged file (a byte over the header) is read-only
  with the check's words; a symbolic link in the file's place refuses
  `Open`; the pragmas in force (`journal_mode`, `foreign_keys`); `Close`
  leaves no `-wal` behind; `Checkpoint` on an open store.
- ids and keys: a project key refused outside the pattern; `CND-1`,
  `CND-2` allocated in order under 50 goroutines creating at once (no
  duplicate, no gap); a key kept through a delete; `number_from`/`_to`
  read back as 0.
- operations: the same op id and content answers the receipt with
  `repeat`; other content `op_conflict`; `expectedRev` behind
  `stale_revision` kept as a failed receipt and nothing changed; a refused
  change (a bound passed) leaves a failed receipt and no change; a commit
  made to fail by a test seam leaves neither; `rev` bumped once per
  operation whatever it set; `changes` rows one per field with `prior`;
  20,001 receipts prune to 20,000 oldest first; `OnChange` called once
  per commit, after it, with the sequence.
- every bound in `model.go`, at the bound and one past it (a table test).
- rules: `Class` for every field named in the table; `Merge` for each
  class: the later scalar wins, the tie-breakers in order, an add that a
  remove did not see survives, an observed add is removed, a log is never
  merged, a second create is dropped.
- tickets: create with every field; edit setting several fields in one
  operation; `done` refused by `acceptance` then allowed once accepted;
  refused by `delivery` then allowed with a link; `closed_at` set and
  cleared; `unaccept`; a parent cycle and a depth of 9 refused; a parent
  of another project refused; delete and undelete with the key kept;
  assignees past 8, labels past 20 refused.
- relations: `blocks` cycle refused (A blocks B blocks C blocks A); a
  cross-project relation refused; `blocked` computed from live blockers
  only.
- comments: `seq` gapless under concurrent adds; edit and delete as
  tombstones; the FTS entry rewritten (a word in a new comment found,
  gone after its delete).
- queries: each filter alone and three together against a seeded set;
  every sort both ways; the cursor pages 1,000 tickets without a repeat
  or a gap; `q` with FTS syntax characters matched as words; a deleted
  ticket out unless asked; `BenchmarkList` with its two targets.
- attachments: the file written and hashed before the row; a second
  upload of the same content shares the file; a remove keeps a shared
  file; the sweep deletes an orphan made by hand and leaves a named file;
  a name with `/` or `..` refused; 16 MiB + 1 refused with no file left.
- views: create, edit, delete; a `query` past 8 KiB refused; a view of
  every project.

**Go, race, `internal/api`** (`dispatch_test.go`, `dispatch_attachments_
test.go`, `events_test.go`): every route of the table with its 2xx and
each 4xx named there, through the real store in a temp data directory;
the workbench token required on every route (a share token refused);
`403 switchyard` on a switchyard; `503` without a data directory and
`503 dispatch_unavailable` for a write on a read-only store (a database
of a later version planted); the `dispatch` event on the stream after a
create, at most 128 bytes, and the longest payload the server can make
measured under it; a receipt read back through `GET /api/dispatch/ops/
{opId}`; an upload past the bound `413`; the download headers (nosniff,
attachment, an `image/svg+xml` served as `application/octet-stream`); the
`links?run=` route against a live run of the engine (a fake launcher) and
a run's record. `internal/cli` (`dispatch_test.go`, `task_test.go`,
`mcp_test.go`): each command and tool against a test server, a lost
reply asked for once with the same op id, `no_ticket` for a session in
no linked run, the usage. `internal/agents`: the skill's section present
and its marker intact.

**vitest** (`web/app/utils/*.test.ts`, `composables/*.test.ts`):
`dispatch.test.ts` (the key parser, the vocabularies' words, `ticketWords`,
the close policy's words, the board and roadmap columns, the keyboard
model: `j` `k` wrap at the ends, `x` toggles, a row gone hands the focus
on), `dispatchQuery.test.ts` (the URL codec both ways, a saved view's
query both ways, an unknown parameter ignored), `palette.test.ts` (a key
typed ranks its ticket first in Tickets; `go dispatch`), `useShortcuts.
test.ts` (`KeyD`, the rows), `tasks.test.ts` (`sendCommand` with an op
body unchanged for the task commands).

**Playwright** (`web/e2e`, the stub agents, one worker):

- `dispatch.spec.ts`: New project (key, name, policy); a ticket made from
  the list's `c`, its key `E2E-1`; the ticket page edits the title, the
  description (preview shown), the priority and the owner; a comment
  posted and shown; the history lists the fields set; the project page's
  counts.
- `dispatch-views.spec.ts`: the filter in the URL (`?status=`, `?q=`),
  the list filtered; a saved view made, its chip applying the filter,
  renamed and deleted; the board's columns with the ticket in its status;
  a drag to In review; the roadmap's columns; Next and Now by `→` on a
  focused card; the phone width (390 px) for the list and the page
  (`phone.spec.ts`'s viewport).
- `dispatch-keys.spec.ts`: `j` `k` `Enter` on the list, `Esc` back, `s`
  opening the status picker and a choice applying, `x` selecting two and
  the palette's page group setting their status, `G D` from the Yard,
  `Alt+D` from a focused terminal (the terminal did not get it).
- `dispatch-close.spec.ts`: Done refused on the page with the policy's
  words; a board drop to Done snapping back with the same words; Accept
  then Done allowed; a `delivery` link required by a second project; the
  policy card's states.
- `dispatch-links.spec.ts`: a managed run launched (`tasks.spec.ts`'s
  crew), linked to a ticket by the link form; the ticket page's Runs
  section shows the run's task rows (`[data-task-row]`); the run page's
  chip names the ticket and opens it.
- `palette.spec.ts` and `palette-rows.spec.ts` gain the Tickets group (a
  key typed opens the ticket on Enter; `→` Copy key by its toast; "New
  ticket in E2E" opens the dialog) and the Dispatch page's rows.

Every spec names what it made (keys of its own project, `stopStrays`
first where it launches a run) and never counts a list it did not fill.

**By hand** (what tests cannot): the acceptance boundary below, a week of
real use; the desktop app on Windows (WSL) and the Mac opening Dispatch
after an upgrade with an empty store and after a restore of the data
directory; `conductor export` once C6 lands; Markdown pasted from a
GitHub issue rendering as expected; an attachment of 15 MiB over a slow
link.

## Boxcar constraints

1. The engine starts an attempt only through `crew.Launcher`: not touched;
   Dispatch starts nothing, and the link from a ticket to a run is a row
   that names a run the engine made.
2. `session.Local` drives a `session.Process`: not touched; no new code
   reaches a session, a pid or a tty.
3. An attempt's context is files in its worktree: not touched here; DS2
   will put a ticket's snapshot into `.conductor/task.json` through the
   same `os.Root` the task file uses, never into the server's memory
   alone.
4. The agent talks to the server over the network: honoured; `conductor
   task ticket` and the `ticket_read` and `ticket_comment` tools go over
   HTTP with `CONDUCTOR_NOTIFY_TOKEN`, the address-based path every other
   task tool takes.
5. Checks go through `internal/proc`: not touched; Dispatch runs no
   command.
6. Platform splits stay platform tags and the UI shows capabilities: not
   touched; Dispatch has no platform split and no capability, it is
   always there.
7. The ledger's evidence rows gain `where` in C10: not touched; Dispatch
   adds no evidence row and reads none; a run link is a reference.
8. hostagent is a launch site as much as api: not touched; Dispatch lives
   on the server, and a `conductor host` session reaches it only through
   the same routes a server session does.

## Open-core checklist

- [x] Article 1: no licence or plan check anywhere in `internal/dispatch`,
  the routes or the pages; the only condition is whether a data
  directory exists.
- [x] Article 2: the open-source build gets the whole of Dispatch: store,
  views, policy, history, CLI and tools, with no account.
- [x] Article 3: nothing is gated; there is no flag a fork could flip
  because there is nothing behind one.
- [x] Article 4: every read is a `GET` the admin makes; the store is plain
  files in the data directory, exported whole by C6 and restored by
  copying them back; no subscription exists to lapse.

## The PRs

Each PR is one issue and one branch cut from `main`, a draft PR at its
first commit, the checks for its surfaces before every push, its body
ticking the open-core checklist and naming the agent that made it; each
leaves `main` working and green on its own (a route without its page is
reachable by the CLI and the tests; a page without its later parts says
what is not there yet). Parallel means the two touch no common package or
directory, `docs/` included; a PR that edits `docs/protocol.md` is not
parallel with another that does. Sizes are estimates of changed lines
with tests.

| PR | Title | Touches | Depends on | Parallel with | Size |
|---|---|---|---|---|---|
| PR-1 | dispatch: the store, its schema, ids and keys, and the typed operations with receipts | `internal/dispatch`, `docs/dependencies.md`, `docs/repository-map.md` | none | none | ~1,400 |
| PR-2 | dispatch: comments, labels, milestones, people, relations, links, acceptance and the close policy | `internal/dispatch` | PR-1 | none | ~1,300 |
| PR-3 | dispatch: the list query, full-text search, saved views and the benchmark | `internal/dispatch` | PR-2 | none | ~1,100 |
| PR-4 | api: projects and tickets over HTTP, the `dispatch` event and the receipts route | `internal/api`, `docs/protocol.md`, `docs/architecture.md` | PR-3 | none | ~1,300 |
| PR-5 | api: comments, labels, milestones, people, relations, links, acceptance and views over HTTP | `internal/api`, `docs/protocol.md` | PR-4 | PR-7 | ~1,400 |
| PR-6 | dispatch, api: attachments as content-addressed files, uploaded and served | `internal/dispatch`, `internal/api`, `docs/protocol.md` | PR-5 | PR-7, PR-8 | ~900 |
| PR-7 | web: the Dispatch client, the projects page, the list view and the ticket page | `web/app`, `web/e2e` | PR-4 | PR-5, PR-6 | ~1,400 |
| PR-8 | web: the board and roadmap views, filters, saved views, the keyboard and the palette rows | `web/app`, `web/e2e` | PR-7, PR-5 | PR-6, PR-11 | ~1,400 |
| PR-9 | web: comments, attachments, parent and relations, labels and milestones on the ticket page | `web/app`, `web/e2e` | PR-8, PR-6 | PR-11 | ~1,400 |
| PR-10 | web: the close policy and acceptance, delivery links, the run Tasks view inside a ticket, the run page's chip | `web/app`, `web/e2e` | PR-9 | PR-11 | ~1,000 |
| PR-11 | cli, mcp, skill: `conductor dispatch`, `conductor task ticket`, `ticket_read` and `ticket_comment` | `internal/api`, `internal/cli`, `internal/agents`, `docs/protocol.md`, `README.md` | PR-6 | PR-8, PR-9, PR-10 | ~1,100 |
| PR-12 | docs: the features entry, the README section, the architecture section, the names line, the by-hand record | `docs`, `README.md`, `AGENTS.md` | every PR above | none | ~300 |

**PR-1** (`internal/dispatch`): `Open` with the pragmas, the quick check,
version 1 of the schema (every table above, the FTS table included so
version 1 is whole), the migration table empty, read-only on a newer or
damaged database, `Close`, `Checkpoint`, `OnChange`; ids and keys; the
row types and bounds of `model.go`; `Apply` with receipts, the `changes`
log and the pruning of receipts; `rules.go` with `Class` and `Merge`;
projects (create, edit, archive) and tickets (create, edit of every
scalar field, assignees and labels as sets, delete, undelete); the
`docs/dependencies.md` row for `modernc.org/sqlite` naming two importers
and the `docs/repository-map.md` row. Tests: open and schema, ids and
keys, operations, bounds, rules, the ticket tests that need no relations,
comments or policy. Go only.

**PR-2** (`internal/dispatch`): comments with `seq`; labels and
milestones with their bounds, a delete leaving every ticket; people;
relations with the cycle checks and `blocked`; links with the run kind's
reference checked by a `RunExists func(id string) bool` the API gives the
store; `accept` and `unaccept`; the close policy's `close` state on a
read and the gate on `done`. Tests: relations, comments, the policy and
acceptance, labels and milestones, people. Go only.

**PR-3** (`internal/dispatch`): `List` with every filter, sort and the
cursor; FTS5 maintained in the transaction (tickets, acceptance,
comments); `Ticket`, `History`, `Children`, `Blockers`; views; the sweep
of orphan files is PR-6's, the receipt pruning's daily run is here;
`BenchmarkList` with the two targets measured on the Linux box and named
in the PR body. Tests: queries, views, the benchmark. Go only.

**PR-4** (`internal/api`, docs): `openDispatch` in `New`, the field, the
close; the projects, people, tickets (create, read, edit, accept,
unaccept, delete, undelete, history, comments read), `links?run=` and
`ops/{opId}` routes; the `dispatch` event with its bound; `docs/
protocol.md` rows and the Events row; the architecture section. Tests:
those routes' 2xx and 4xx, auth, switchyard, the two 503s, the event, the
receipt route. Go only.

**PR-5** (`internal/api`, docs): comments (add, edit, delete), labels,
milestones, people, relations, links, views routes; their `docs/
protocol.md` rows. Tests: each. Go only.

**PR-6** (`internal/dispatch`, `internal/api`, docs): `attachments.go`
(the file first, the hash, the row), `sweep.go` (orphans at open),
`Open(ctx, id)`; the upload route (multipart, the bound), the download
route (the headers, the allow list), the delete; the protocol rows.
Tests: the attachment tests of the store, the two routes, the headers.
Go only; the Mac and Windows preflight rows run here.

**PR-7** (`web/app`, `web/e2e`): `useDispatch.ts`, `useDispatchOps.ts`
(`sendCommand` generalised), `useDispatchLive.ts` and the `dispatch`
branch in `useAttention.handleBlock`; `utils/dispatch.ts` and
`dispatchQuery.ts`; the nav item, `G D`, `Alt+D`, the shortcuts rows;
`pages/dispatch/index.vue` with New project; `pages/dispatch/[project]/
index.vue` with the list view and the filter bar (the board and roadmap
tabs present and saying "comes with the next change" until PR-8); the
ticket page with title, description and acceptance (edit and preview),
status, priority, horizon, owner, assignees, history. Tests: vitest
`dispatch.test.ts`, `dispatchQuery.test.ts`, `useShortcuts.test.ts`;
Playwright `dispatch.spec.ts`. UI only.

**PR-8** (`web/app`, `web/e2e`): the board and the roadmap with drag and
keyboard moves; the saved views bar; the list's keys and selection with
the page's palette group; the palette's Tickets group, the `>` rows, `go
dispatch`; phone widths. Tests: vitest additions to `dispatch.test.ts`
and `palette.test.ts`; Playwright `dispatch-views.spec.ts`,
`dispatch-keys.spec.ts`, the `palette.spec.ts` and `palette-rows.spec.ts`
additions. UI only.

**PR-9** (`web/app`, `web/e2e`): comments (post, edit, delete, the
author's name), attachments (upload with progress, the list, images
inline through object URLs, download, delete), parent and children,
relations with `TicketPicker.vue`, labels and milestones management on
the project page and their pickers on the ticket page. Tests: Playwright
`dispatch.spec.ts` additions (comments, an attachment uploaded and
downloaded byte for byte, a parent set, a `blocks` relation shown both
ways); vitest for the pickers' pure parts. UI only.

**PR-10** (`web/app`, `web/e2e`): `DispatchClosePolicy.vue` with Accept,
Unaccept and Close, the refusal's words; delivery links; `DispatchRuns.
vue` with `RunTasks.vue` embedded and the run link form; the run page's
chip; the project dialog's close policy editor. Tests: Playwright
`dispatch-close.spec.ts`, `dispatch-links.spec.ts`; `tasks.spec.ts`
unchanged and still green. UI only; the Mac and Windows preflight rows
run here.

**PR-11** (`internal/api`, `internal/cli`, `internal/agents`, docs,
README): `GET /api/sessions/{id}/ticket` and `POST /api/sessions/{id}/
ticket/comments` through `taskSession`; `conductor dispatch` and
`conductor task ticket`; the two MCP tools; the usage, the completion
table, the skill's section; the protocol rows and the README's CLI lines.
Tests: `cli/dispatch_test.go`, `task_test.go`, `mcp_test.go`, the skill
test, `api/dispatch_test.go` additions. Go only; the Mac and Windows
preflight rows run here.

**PR-12** (docs): the `docs/features.md` entry recording what each PR
verified and the by-hand checks run; the README's Dispatch section; the
AGENTS.md names line; the acceptance observation below once it is made,
else the entry says it is pending. Docs only; no check applies.

## Owner decisions

Dated 2026-10-28. Each is a default the owner can overturn with a
status-log row (the Program, section 3.1's convention); the reasoning is
why it was taken.

1. **O10, the conflict rules.** Dispatch keeps an operation log
   (`changes`) beside its tables and classifies every field as immutable,
   scalar (last writer wins per field), set (observed-remove, add wins),
   log (append only), allocation (number blocks) or gated (a close
   through the policy); the table in "Events, orderings and invariants"
   and `rules.go` are the decision, and CS1 replays the log by them.
   Per-field rules were chosen over per-row ones because two people
   editing a title and a priority at once is the common case and neither
   should lose; an observed-remove set because a label removed on one
   machine and added on another should end added, which is what a person
   expects of the later action.
2. **No owner lock for Dispatch.** The ledger needs one because two
   servers would each recover the other's runs; Dispatch has no process
   to recover, SQLite's locking orders writers across processes, and
   every revision is read and bumped inside its transaction, so a second
   server on the same data directory (already refused unless each has a
   token of its own) changes nothing wrongly; it only misses the other's
   SSE events, which the next read repairs.
3. **A second importer of `modernc.org/sqlite`, no shared helper.** The
   open, pragma and migration code is about sixty lines; copying them
   into `internal/dispatch` keeps the ledger untouched by this stream. A
   shared package can come when a third store needs it.
4. **Six statuses, five priorities, three horizons, fixed.** Linear's
   vocabulary without custom workflows: `backlog`, `ready`, `in_progress`,
   `in_review`, `done`, `cancelled`; per-project statuses are out of
   scope until a project needs one.
5. **People are a local directory of names** (decision D4): the owner is
   one name, assignees are names or catalog agents (`person:`,
   `agent:`); nothing authenticates them until Program item ID1, and an
   acceptance records the operator's display name as `by` beside the
   owner it was made for.
6. **The close policy's default is `["acceptance"]`** for a new native
   project and `[]` for an external one (its tracker decides there);
   `delivery` is offered; `verified` is refused until DS2 defines it.
7. **Keys are `KEY-n`**, the project key `^[A-Z][A-Z0-9]{1,5}$`, numbers
   allocated by the store with two columns reserved for CS1's number
   blocks; a ticket is never renumbered and never moves between projects.
8. **Attachments are content-addressed files beside the database**,
   16 MiB each, 50 per ticket, served as downloads with an allow list
   for inline types; the page fetches with the token and shows images
   through object URLs. Files rather than blobs keep the database small
   and the export plain.
9. **Routes under `/api/dispatch/`, pages under `/dispatch/`, the nav item
   "Dispatch" with `G D` and `Alt+D`**; a ticket page at
   `/dispatch/<KEY>/<n>`.
10. **The list's keys are Linear's letters** (`j` `k` `Enter` `x` `c` `s`
    `p` `a` `l` `m` `o`), plain keys that pause in text fields and
    terminals as every page key does.
11. **No new palette prefix or verb.** A key or a title finds a ticket in
    the search; `>` offers the new-ticket rows; the pages register their
    group. The twelve verbs stay twelve.
12. **External-tracker projects are a kind and a URL in this item.** A
    ticket of such a project is made by hand with an external reference;
    every field is native until IN1a records which system owns it; the
    `authority` and `external_*` columns are reserved now so IN1a adds
    no migration for them.
13. **Agents read and comment, nothing more.** An attempt whose run is
    linked to a ticket may read it and comment on it with its own token;
    creating or editing tickets is the admin's, and Brakeman's drafts
    come later through the same typed operations.
14. **Search is FTS5**, compiled into the pinned driver (checked
    2026-10-28); a typed query is words, never FTS syntax.
15. **Receipts: the newest 20,000 kept**, pruned at open and daily.
16. **Deletes are tombstones**; projects are archived, never deleted; keys
    are kept.
17. **Every Dispatch route is the admin's**; share-link viewers see no
    ticket.
18. **The `dispatch` event reaches the pages alone**, never the Events
    page's routing, notifications or webhooks, until someone asks.

No question is left open: each answer above lets the plan stand, and the
owner overturns any of them with one row.

## Acceptance

The acceptance boundary (the Program, section 4.3) is a team using
Dispatch alone for a week of real work, with the run Tasks view one view
inside it. It will be observed, not ticked:

- Once PR-10 is merged, the owner and one other person (or the owner and
  a second machine) run this repository's own next stream in Dispatch: a
  native project `CND` holding the issues of the item that follows (DS2's
  PRs, or EN1's), each a ticket with its acceptance criteria, owner,
  priority and horizon; the managed runs that work on them linked to
  their tickets; comments, labels, a milestone for the release, a saved
  view per person; closings through the policy. GitHub issues stay the
  backlog of record for the repository (AGENTS.md) and are mirrored by
  hand for the week; nothing is published back (IN1a).
- After the seven days the owner writes what was missing, what was slow
  and what was wrong into `docs/features.md` (PR-12 carries it when the
  week fits before it; else a later docs PR of the stream), a bug the
  stream caused fixed in the stream, a product defect outside it filed.
- Done means: every PR merged, the entry written, the week recorded with
  the owner's words that Dispatch was the tracker in use and that a run's
  tasks were looked at from the ticket; the Program's status row moves to
  Done then, never on the table alone.

What needs the owner: the review of this plan and its decisions; cutting
the twelve issues; the week of use and its record; the merges.
