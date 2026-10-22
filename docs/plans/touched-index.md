# A cross-session Touched index for the palette's `:` prefix (#304)

The follow-up the command palette brief names (#302), taken up in the
second UI batch with #315, #321 and #322. In the palette, `:` searches
the page's open tabs, this session's Changes and Commits, and "Touched
lately" from the feed's `file` entries: `useEvents().entries`, the last
500 events across every session. A file a session touched before those
500 is not found. The server already keeps each session's own index
(`op: touched`, `session.MaxTouched` = 2,000 files, the least recently
touched evicted first), read for one session by the Files tab; this is
the same index, across the sessions the caller may see.

## A route, not a stream index

`GET /api/touched` (admin, as `GET /api/sessions` is): the live
sessions' indexes in one reply. A stream index (every `file` entry the
events stream carries, kept by the page) would rebuild 2,000 × N entries
in every tab and lose what came before the tab opened; the route reads
what the server has. The palette asks for it once after its first
paint and caches it for the open (principle 1 of the brief: nothing is
fetched on open), as the crews and the catalog are, and reads it again
when a `file` event arrives (debounced to one read in two seconds) or
when the cached reply is a minute old at the next open.

- Reply: `{ sessions: [{ id, name, agentId, kind, cwd, touched:
  TouchedFile[], truncated }], truncated }`, `touched` the most recently
  touched first (the index's order), `TouchedFile` as the `touched` file
  header carries it (`internal/proto`: path, op, ops, count, tool, by,
  first, last). A session with nothing touched is left out.
- Bounds: `?limit=` per session, default 500, at most 2,000 (the index's
  own cap); the whole reply at most 2 MiB of JSON, the sessions filled
  in the registry's order, a session cut short marked `truncated` and
  the reply too; the session's file deny list applied as `op: touched`
  applies it. The reply is a snapshot under each session's lock, one
  session at a time.
- A hosted session keeps no activity log on the server (the host keeps
  it), but the server sees every `file` entry the host reports
  (`HostedSession.HostActivity`): it feeds a `session.TouchedIndex` of
  its own from them, the same type `Local` keeps (exported for it), so
  the route lists hosted sessions too, from the entries reported since
  the host connected. The host's own index (`op: touched` over the
  terminal) stays what the Files tab reads.
- `docs/protocol.md`: the route's row in the table; `web/app/utils/
  protocol.ts` gains nothing (the type exists); `internal/proto` gains
  nothing but the reply's documented shape.

## The palette

- `useSessions().touched(limit?)` for the route; `usePalette` keeps the
  reply beside the crews and the catalog (`touchedAt`, `loadTouched`),
  reads it after the first paint and again on a `file` event
  (`useEvents().onEvent`, debounced) or at an open a minute later.
- `utils/touchedIndex.ts`, pure, vitest beside it: `mergeTouched(index,
  feed, now)` lays the feed's live `file` entries over the cached index
  (the feed is newer than the cache by up to two seconds), one row per
  (session, path), the most recently touched first, bounded at 2,000
  rows for the list; `touchedWords` as the Files tab says them.
- The `:` group "Touched lately" becomes "Touched" with every session's
  files: `path · session · op with tool · when`; the query matches the
  path, the session's name and the tool. Enter on a row of this page's
  session opens the file in its editor; on another session's, opens
  that session's page with `?open=<path>` (new on the session page,
  read-only like `?tab=`: the editor opens the file as the Files box
  would; a line as `?open=path:line`).
- "Open a session page first" stays for a typed `:path` on a page with
  no editor.

## Tests

- Go, race: `internal/api/touched_test.go`: admin only; two local
  sessions with file events (through `POST /api/sessions/{id}/events`)
  listed newest first; `limit` and the 2 MiB cap mark `truncated`; the
  deny list leaves a file out; a hosted session's reported `file`
  entries listed (through the host connection the existing hosted tests
  set up). `internal/session/touched_test.go` gains the exported index's
  cases.
- vitest: `utils/touchedIndex.test.ts` for the merge, its order and its
  bound.
- Playwright, `palette-touched.spec.ts`: 600 file events reported for a
  session (past the feed's 500), then `:` with the first file's name
  finds it from the index; a row of another session opens that page
  with the file open.

## Not here

The host's own index stays on the host; the server's hosted index
begins when the host connects (files touched before are the host's
alone, which the Files tab's Touched still shows). No new frame on the
terminal protocol.
