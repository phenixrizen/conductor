# Review from the terminal

The Bubble Tea workbench is an interactive interface to the same API used by the
CLI and browser. It lets a developer import and submit a package, and lets an
independent reviewer inspect and approve its exact content. PostgreSQL retains the
shared record. A local actor name is development identity, not authentication.

Start the API using the [local development guide](local-development.md), then run:

```bash
go run ./cmd/conductor tui --actor developer
```

Set `CONDUCTOR_URL` if the API is elsewhere. To open a particular change or filter
the shared browser by an exact repository label:

```bash
go run ./cmd/conductor tui --actor reviewer CHG-...
go run ./cmd/conductor tui --actor reviewer --repository synthetic/service
```

The actor is fixed for the session. Exit and start another session to review as
another local actor. This does not prove a person's identity or grant permissions.

## One review loop

1. Prepare a JSON package file using the CLI or a text editor. Use `c` to select and
   preview that file, then `s` and type `create` to confirm creation.
   `--file /tmp/package.json` supplies a default path. The interface does not run
   repository scripts or an editor.
2. Inspect the created package's content, revision, and digest. Use `s` to confirm
   submission of that displayed revision.
3. In a second session, start with `--actor reviewer`. Select the shared package
   and inspect its complete content. Use `a` to confirm approval of the displayed
   revision and digest. The approval action does not fetch a newer revision.
4. Back in the author session, inspect the latest revision, then use `e` to import
   and preview a revised JSON file. Use `s` and type `revise` to confirm the
   replacement. Confirmation sends the displayed expected
   revision. Replacing content creates a new revision and invalidates the earlier
   approval for the current package.

Imported files replace the complete package content. Keep all fields you intend to
retain; unknown structured fields in the file are preserved. Input is limited to
1 MiB. The current database rejects unchanged content and exact content reverts
within a change; that limitation also applies to the terminal workbench.

## Controls

| Key | Action |
|---|---|
| Arrow keys or `j` / `k` | Select a shared change or scroll inspected content |
| Enter | Open the selected change or accept a prompt |
| `n` / `p` | Move between pages of shared changes |
| `o` | Open a change by ID |
| `r` | Explicitly reload shared work or inspect the latest revision |
| `c` | Import and preview a JSON file as a new package |
| `e` | Import and preview a replacement revision |
| `s` | Save a preview, or submit the displayed revision when no preview is open |
| `a` | Confirm approval of the displayed revision and digest |
| Page Up / Page Down, Home / End | Scroll inspected content |
| `b` | Return to shared browsing |
| Escape | Cancel a prompt, discard a preview, or cancel a pending request |
| `q` outside a prompt, or Ctrl+C anywhere | Exit |

Confirmation prompts require the displayed action word. Approval is unavailable
for an unsubmitted draft, an already approved package, or the current revision's
author. The API applies the actual approval policy. A terminal too small to display
the revision details cannot confirm a command; enlarge it to continue.

## Conflicts and uncertain results

If another client edits the package, a stale submission, revision, or approval
fails. Use `r` to inspect the latest content before another mutation. The workbench
does not silently refresh and approve content you have not inspected.

A lost response can occur after the server commits a command. A timeout, connection
failure, or unexpected redirect therefore requires inspection, not an automatic
retry. Error messages remain visible; missing responses never look like approval.
Requests have deadlines and leaving the workbench cancels pending operations.

Repository text and server messages are displayed as inert text. Terminal control
characters cannot become terminal commands. The complete JSON remains available by
scrolling; a small viewport does not change the content or digest being reviewed.

Historical revisions and audit events remain available through the CLI and web
workbench. See the [context and history walkthrough](context-review.md). The initial
terminal workbench inspects current packages and does not implement history or
revision comparison views.

## Acceptance checks

The Go tests exercise confirmation, stale and uncertain responses, cancellation,
and terminal rendering. The real PTY script additionally drives the compiled CLI
through the API to PostgreSQL. It records requests to prove that approval sends
the displayed tuple without a refresh, including when another client edits first.

Start a local API backed by a disposable test database, then run:

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
supports the project's Go 1.24 toolchain. The adapter uses its
[model/update/view interface](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/README.md)
and [program options](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/options.go)
for cancellation and the alternate screen. This is a local terminal adapter;
it adds no authentication, workflow execution, or repository publication.
