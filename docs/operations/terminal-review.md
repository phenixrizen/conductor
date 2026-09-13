# Review from the terminal

The Bubble Tea workbench is an interactive interface to the same API used by the
CLI and browser. It lets a developer import and submit a package, and lets an
independent reviewer inspect and approve its exact content. PostgreSQL retains the
shared record. Authenticated collaborators can also request repository context,
inspect shared source receipts, and attach them to a package as a new revision.
A local actor name is development identity, not authentication. The separate
[release workbench](release-terminal.md) adds graphs, coordinated agent plans,
publication artifacts, tracker synchronization and runtime evidence using the
same fixed identity.

## Authenticated workspace review

Configure the API and provision access using the
[authenticated review guide](authenticated-review.md). Obtain a supported API access
token from your identity provider and keep it in a protected regular file outside
the repository. Conductor does not acquire or refresh this token.

```bash
export CONDUCTOR_URL='https://conductor.example.invalid'
export CONDUCTOR_TOKEN_FILE='/private/path/conductor-access-token'
go run ./cmd/conductor session
go run ./cmd/conductor repositories --workspace team
export CONDUCTOR_WORKSPACE=team
export CONDUCTOR_REPOSITORY_ID=service
make tui
```

Use workspace and repository IDs from those discovery responses. An authenticated
terminal requires both selections; `CONDUCTOR_WORKSPACE` and
`CONDUCTOR_REPOSITORY_ID` can supply their defaults. Use `make tui CHANGE=CHG-...`
to open a change directly; the direct CLI accepts the ID as its final positional
argument. `--repository` remains an optional exact content-label filter and cannot
change canonical ownership or grant access. Do not pass `--actor` with a token.
`make tui` omits its local actor whenever `CONDUCTOR_TOKEN_FILE` or
`CONDUCTOR_TOKEN` is present and lets the CLI validate that credential. An empty
setting remains an invalid configured credential; it does not select local mode.
To start a release view, use, for example, `make tui VIEW=graphs` with the same
authenticated scope. `FILE` and `CHANGE` pass an optional request file and record ID.

Before loading packages, the terminal retrieves its server principal and the
selected repository's effective capabilities. It displays the human or agent kind
and the selected scope. Read access permits inspection; author access permits
creation, revision, and submission for humans or agents. Approval requires an
independent human with approval capability. The server checks every command again.
Discovery is bounded to 100 entries per response. A missing selection blocks access;
if the response is truncated, the terminal explains that it cannot establish access
from the incomplete list.

The credential, principal, workspace, and repository stay fixed for the session.
The token file is read once; replacing it does not change a running terminal.
Exit and start a new session to change credentials or scope. `CONDUCTOR_TOKEN` is
an alternative to the file, but setting both is an error. Credentials are bounded
and never printed or included in command arguments. Shared connections require
HTTPS; HTTP is permitted only for loopback testing, and redirects are rejected.

## Local development

Use a shell without authenticated token or scope settings. In the first terminal,
start the API and persistent local database:

```bash
make run
```

In a second terminal, connect the workbench:

```bash
make tui
```

`make tui` uses the local actor `developer` and defaults to the API at
`http://127.0.0.1:8080`. It does not start or stop that API. Press `q` to close the
workbench; Ctrl+C in the first terminal stops the API while retaining the database.
See [local development](local-development.md) for Docker Snap, database and port
configuration. Set `CONDUCTOR_URL` when connecting to a different running API.

To open a particular change as a reviewer, or preview a content file:

```bash
make tui ACTOR=reviewer CHANGE=CHG-...
make tui FILE=/tmp/package.json
```

The direct CLI remains available, including an exact repository-label filter:

```bash
go run ./cmd/conductor tui --actor reviewer --repository synthetic/service
```

The actor is fixed for the session. Exit and start another session to review as
another local actor. This does not prove a person's identity or grant permissions.
Local mode provides unscoped package review. Graph, coordinated execution,
delivery, tracker and runtime views require an authenticated workspace/repository;
choosing `VIEW` cannot enable them on a local API.

## One review loop

1. Prepare a JSON package file using the CLI or a text editor. Use `c` to select and
   preview that file, then `s` and type `create` to confirm creation.
   `--file /tmp/package.json` supplies a default path. The interface does not run
   repository scripts or an editor.
2. Inspect the created package's content, revision, and digest. Use `s` to confirm
   submission of that displayed revision.
3. Start a second session with an independent reviewer's token and the same
   workspace/repository. For local development, use `--actor reviewer` instead.
   Select the shared package and inspect its complete content. Use `a` to confirm
   approval of the displayed revision and digest. The approval action does not
   fetch a newer revision or revalidate identity inside the confirmation.
4. Back in the author session, inspect the latest revision, then use `e` to import
   and preview a revised JSON file. Use `s` and type `revise` to confirm the
   replacement. Confirmation sends the displayed expected
   revision. Replacing content creates a new revision and invalidates the earlier
   approval for the current package.

Imported files replace the complete package content. Keep all fields you intend to
retain; unknown structured fields in the file are preserved. Input is limited to
1 MiB. The current database rejects unchanged content and exact content reverts
within a change; that limitation also applies to the terminal workbench.

## Shared repository context

The collection view uses the same credential, workspace, and repository as package
review. An operator must configure background collection using the
[durable context guide](durable-context.md). Readers can inspect existing requests
and receipts. Humans and agents with author permission can request new collections
after the operator enables that repository's read integration. Local mode does not
offer collection controls.

Save a request such as this in `/tmp/conductor-context-request.json`, replacing the
synthetic commit with a real full commit ID from the selected repository:

```json
{
  "commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "paths": ["README.md", "docs/README.md"],
  "idempotencyKey": "context-review-example-1"
}
```

The file must be a regular UTF-8 JSON file of at most 64 KiB with those three
required fields and an optional boolean `fullSource`. Set `fullSource: true` to
request a whole-source Git bundle and CodeGraph index for coordinated work. Use a
full lowercase 40-hex commit ID and 1–32 unique relative file paths. Branch names, abbreviated IDs, traversal, and directories are not supported.
The key is 1–128 printable ASCII characters without spaces or commas; keep it with
the input for recovery. Unknown, duplicate, case-aliased, or null fields are rejected.
The terminal sorts validated paths before preview and never runs the selected file.

1. Press `g` to browse shared collections, then `c` and enter the request file path.
   Inspect the preview, press `s`, and type `collect` to send that exact input and key.
2. Acceptance means the request was saved. It does not prove that the worker started
   or that every path was collected. Use `r` to recheck access and inspect later
   observations, or select a shared request with Enter or `o`. Other authorized
   collaborators see the same request and receipt.
3. Inspect each path's coverage and the complete escaped JSON. A receipt can contain
   missing, unavailable, or truncated files. Execution observations show their time
   and become stale after 30 seconds; the clock update does not poll or collect again.
4. To request cancellation of your own pending collection, press `x` and type
   `cancel-collection`. You must still have author permission. A cancellation request
   is separate from observed cancellation, and a receipt committed first remains
   available. An absent or unavailable execution observation cannot prove it stopped.
5. To attach a receipt, press `g` and inspect the latest target package. Press `g`
   again, inspect the collection, then press `t` and type `attach`. Confirmation
   shows the package revision and digest plus the receipt digest. It sends only
   those captured facts, with no refresh inside the action. The returned package
   has a new draft revision; previous approval does not apply to it. Authors may
   attach a collaborator's receipt in the same scope.

Every authenticated `r` clears prior inspections and rechecks access. Refreshing a
collection therefore clears a retained package target too; inspect the package
again before selecting the receipt for attachment. Scope and credentials stay
fixed throughout both views.

## Controls

In the package view:

| Key | Action |
|---|---|
| Arrow keys or `j` / `k` | Select a shared change or scroll inspected content |
| Enter | Open the selected change or accept a prompt |
| `n` / `p` | Move between pages of shared changes |
| `o` | Open a change by ID |
| `r` | Recheck authenticated access, then reload shared work or inspect the latest revision; local mode reloads directly |
| `c` | Import and preview a JSON file as a new package |
| `e` | Import and preview a replacement revision |
| `s` | Save a preview, or submit the displayed revision when no preview is open |
| `a` | Confirm approval of the displayed revision and digest |
| Page Up / Page Down, Home / End | Scroll inspected content |
| `b` | Return to shared browsing |
| `g` | Switch to shared collections in authenticated mode |
| Escape | Cancel a prompt, discard a preview, or cancel a pending request |
| `q` outside a prompt, or Ctrl+C anywhere | Exit |

Confirmation prompts require the displayed action word. Approval is unavailable
for an unsubmitted draft, an already approved package, or the current revision's
author. The API applies the actual approval policy. A terminal too small to display
the revision details cannot confirm a command; enlarge it to continue.

In the collection view:

| Key | Action |
|---|---|
| Arrow keys or `j` / `k` | Select a collection or scroll its complete record |
| Enter or `o` | Inspect the selected collection or enter its ID |
| `n` / `p` | Navigate bounded pages of 20 shared requests |
| `c` | Select and preview a collection request JSON file |
| `s` | Confirm `collect`, including an explicit retry with the same input and key |
| `x` | Confirm `cancel-collection` for your own request |
| `t` | Confirm `attach` to the separately inspected package revision |
| `r` | Clear inspections, recheck access, and reload the selected collection or list |
| `b` | Return to collection browsing |
| `g` | Return to packages |
| Page Up / Page Down, Home / End | Scroll the inspected record |
| Escape | Cancel a prompt, discard a preview, or cancel a pending request |
| `q` outside a prompt, or Ctrl+C anywhere | Exit |

Collection browsing retains at most 1,000 visited page cursors. Refresh restarts
discovery. Package approval and submission keys do not operate in this view.

## Access failures and recovery

`Access unavailable:` means the terminal could not establish access. `Action
blocked:` explains why a control is unavailable. No failure switches to a local
actor or assumes that missing capabilities are granted.

An authentication or permission response (`401` or `403`) clears inspected content,
shared pages, capabilities, imported previews, collection receipts and request keys,
and pending confirmations. It also
discards superseded results so an older response cannot restore the cleared state.
After the operator restores access, use `r` to retrieve the session and repository
capabilities again and explicitly inspect fresh work. Recovery uses the original
credential; it cannot renew an expired token. Obtain a new token and restart the
terminal if that credential has expired or must change.

Every explicit `r` rechecks authenticated access before loading content, even when
the previous request succeeded. Approval always sends only the already displayed
revision and digest. It does not perform either discovery or a package reload as
part of the command.

## Conflicts and uncertain results

If another client edits the package, a stale submission, revision, or approval
fails. Use `r` to inspect the latest content before another mutation. The workbench
does not silently refresh and approve content you have not inspected.

A lost response can occur after the server commits a command. A timeout, connection
failure, or unexpected redirect therefore requires inspection, not an automatic
retry. Error messages remain visible; missing responses never look like approval.
Requests have deadlines and leaving the workbench cancels pending operations.

Collection creation retains its exact preview and idempotency key after a lost
response or cancellation of the pending HTTP request. Press `s` and confirm
`collect` to retry that same request explicitly. The server returns the original
request when the key and input match; no automatic retry occurs. Discarding the
preview does not undo a request that may have committed. After an access failure
or explicit refresh clears the preview, use the original file and key to recover
the same request. Do not replace them merely because its response was lost.

An uncertain collection cancellation requires `r` and fresh collection inspection.
A stale or uncertain attachment blocks package writes; return to packages with
`g`, use `r`, then inspect the collection again before another attachment. Neither
path silently replaces the content or revision that was confirmed.

Repository text and server messages are displayed as inert text. Terminal control
characters cannot become terminal commands. The complete JSON remains available by
scrolling; a small viewport does not change the content or digest being reviewed.

Historical revisions and audit events remain available through the CLI and web
workbench. See the [context and history walkthrough](context-review.md). The
terminal workbench inspects current packages and does not implement history or
revision comparison views.

## Acceptance checks

The Go model tests exercise confirmation, stale and uncertain responses,
cancellation, and terminal rendering. Real PTY acceptance additionally drives the
compiled CLI through the API to PostgreSQL and records requests to prove that
approval sends the displayed tuple without a refresh.

For authenticated acceptance, run:

```bash
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  CONDUCTOR_TEST_TERMINAL=1 \
  go test -race ./tests/acceptance -run '^TestAuthenticatedTerminal(Workbench|Collections)$' -count=1 -v
```

This requires Linux PTYs, Go, and Python 3's standard library. Set
`CONDUCTOR_TERMINAL_PYTHON` if Python is not available as `python3`. The test builds
its CLI and owns a signed synthetic issuer, temporary credential files, API servers,
and an isolated database schema that it removes afterward. It checks shared human
review, agent and capability restrictions, exact approval after a concurrent edit,
access revocation and explicit recovery, scope isolation, and token-file stability.
It does not certify an identity provider or acquire a real user's credentials.
Missing opt-in is an explicit skip; missing database configuration or dependencies
after opt-in is a failure.

`TestAuthenticatedTerminalCollections` drives
[`tests/terminal/collection_review.py`](../../tests/terminal/collection_review.py)
through real PTYs. It checks shared paging and missing evidence, stale and successful
attachment, a lost response after an actual committed request, explicit same-key
recovery without duplicate work, requester cancellation, revocation/recovery, and
reader/agent controls. Its receipts are trusted database fixtures; provider reads
and Temporal process recovery are exercised separately by the durable context suite.

Also retain the local-mode regression. Start an explicitly local API backed by a
disposable test database, then run:

```bash
go build -o /tmp/conductor ./cmd/conductor
CONDUCTOR_API_URL=http://127.0.0.1:8080 CONDUCTOR_BIN=/tmp/conductor \
  python3 tests/terminal/review.py
```

The script requires Linux PTYs and Python 3's standard library. Both environment
variables are required. It creates uniquely named synthetic packages and retains
their history in the selected API; the API has no delete operation. It checks file
creation and revision, independent approval, stale conflicts, a small viewport,
terminal-control escaping, historical approval retention, and clean terminal exit.

Bubble Tea is pinned to `v1.3.10`, whose
[module definition](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/go.mod)
is used with the current module's Go 1.25 minimum and pinned Go 1.26.8 toolchain.
The adapter uses its
[model/update/view interface](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/README.md)
and [program options](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/options.go)
for cancellation and the alternate screen. Authenticated terminal review and
collection controls use the existing API credential and permission boundary; these
controls add no server routes or migrations. Background collection uses the
Feature 006 worker. The separate release workbench exposes graphs, coding and
publication review, tracker synchronization and runtime evidence through the same
authenticated API. Repository commands run only in the trusted workers.
