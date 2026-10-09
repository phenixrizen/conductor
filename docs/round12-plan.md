# Round 12 plan: the Files tab as four sections, and a Monaco editor beside the terminal

Asked on 2026-10-08, from the design screens 4a–4g and 5a–5c in
"Conductor UI mockups" (the files "Files - Explorer", "Files - File open",
"Files - Several files", "Files - Changes", "Files - Touched and Commits",
"Files - States", "Files - Yard and guest and phone", "Files - Comment on a
line"). One place for everything about the files an agent works on, so
nobody leaves Conductor for an editor, a git client or a file manager.

**Status (2026-10-09): done.** F1–F4 landed on 2026-10-08 (PRs #58–#62),
F8 Neovim (#63), F5 Commits (#64), F6 Editing (#65), F7 Comment on a line
(#66) and the remainder (the Explorer's find past unopened folders, Touched
on the Yard and the guest page, Neovim's byte columns) on 2026-10-09. What
is set aside is in `docs/tasks-todo.md` under "The Files tab, after round
12"; each layer's decisions and checks are in `docs/features.md`.

**Decisions (2026-10-08):**

- **The editor is Monaco** (the VS Code editor core), chosen by the owner
  over lighter editors; the phone takes what fits and does not drive the
  desktop's design. Monaco loads only when the first file opens, as its own
  chunk with its workers, so the first load of the join page and the
  workbench stays as it is.
- **Editing is for controllers**, on a session whose `fileEdit` setting
  allows it (a setting of its own beside `fileView`, off for guests by
  default). A save goes over the terminal's connection like a read, lands in
  Activity, and warns when the file changed on disk since it was opened. A
  view-only guest gets the same editor read-only, with no Save.
- **go-git for the git features in Go** (log, the commits' diffs, tree
  reads); the existing CLI wrapper (`internal/crew/worktree.go`) stays for
  what go-git does slowly (status, which hashes the working tree) or cannot
  do (the crews' linked worktrees, to be verified at the first commit).
  Pinned in AGENTS.md's table with that reason: no git binary needed beside
  Conductor on a host.
- **File events come from the hooks first**: the mappers already read the
  PostToolUse payloads that name the file (Claude Code, Codex, Cursor,
  Copilot, Goose); `conductor notify --event file` and an MCP tool are the
  fallback for agents without hooks; git status is the truth for creates
  and deletes nothing reported. Not a filesystem watcher (inotify limits
  under WSL on a large repository).
- **Vim keys are the real Neovim on the machine that runs the session**
  (the owner, 2026-10-08, after the license question): monaco-neovim-wasm
  carries no license (public code without one grants only viewing and
  forking on GitHub, not redistribution), so F8 is a bridge instead, the
  way vscode-neovim works: `nvim --embed` on the server or the host, the
  person's own config and plugins, the buffer mirrored into Monaco, the
  keys sent to Neovim, over the terminal's connection like file reads.
  Neovim itself is Apache 2.0; the bridge is Conductor's. monaco-vim is
  not used. Where `nvim` is missing the editor says so and stays Monaco.
- **Layers land one at a time, each a pull request from main merged before
  the next**, never a stack: GitHub's stack merge rebases the layers above
  and reruns CI for each, which the owner has asked to avoid.

## What is on the wire today, and what each layer adds

Reads: `file` requests over the terminal's connection (bounded, through
`session.ResolvePath`; the data directory and config never served; the
`fileView` setting says who may read). Nothing writes; nothing speaks git;
the hook events carry a tool's name and no path.

| Layer | Adds to the protocol | Adds to the server |
|---|---|---|
| F1 Explorer | nothing | nothing |
| F2 the editor area | nothing | nothing |
| F3 Changes | `git_status`, `git_diff` requests and replies (bounded) | git status and diffs, on the server and in `conductor host` |
| F4 Touched | the `file` activity type with `op`, `path`, `tool` | the mappers carry paths; `notify --event file`; the MCP tool; coalescing; quiet routing |
| F5 Commits | `git_log`, `git_show` | the log since the session started; a commit's diff |
| F6 Editing | `file_write` (chunked, bounded, control only) and its reply; `fileEdit` in the welcome | the write with the changed-on-disk check; the setting; the activity entry; the host side |
| F7 Comment on a line | `quote{path, from, to, lines}` on a chat message | the quote kept with the message, bounded |
| F8 Neovim | `nvim_open`, `nvim_input`, `nvim_close`, `nvim_event` (bounded); `fileEdit` and `nvim` in the welcome | `nvim --embed` per editor on the server and in `conductor host`; the `fileEdit` setting; the write as a file event |

Every layer: Go tests where the server changes, vitest for the browser's
logic, Playwright for what a person sees; headless renders checked against
the screen's id; `docs/protocol.md`, `protocol.ts` and `internal/proto`
together for every wire change; the README's "Clickable links and file
viewer" section rewritten as the layers land; `docs/features.md` with the
round's decisions.

## F1. Explorer (4a)

The Files tab opens on the working directory. `FileBrowser` lists the
session's working directory as soon as the tab shows (and again when the
connection comes back), as a tree: folders expand in place with their own
listing fetched on the first expansion, files with their size; the
breadcrumb from the working directory's folder is there from the start; the
box at the top is both a filter for the tree (names and folders matched as
you type, case-insensitive) and a go-to field (a path, or `path:line`, Enter
opens it); the hint "Paths the agent prints in the terminal are clickable"
is one line at the foot. A file click opens it in the pane as today (F2
moves it to the editor area). The Yard's slide-over and the join page get
the same. Pure logic in `web/app/utils/fileTree.ts` (the tree's nodes, the
filter, what counts as a path); vitest; Playwright `files.spec.ts`.

## F2. The editor area (4b, 4c, 4f, 4g)

Landed in two pull requests: F2a the session page (the area, the tabs,
the split, the fold, the states), F2b the Yard's focused tile, the guest's
join page and the phone.

A file opens in a Monaco editor above a shrunk terminal, split by a bar you
can drag (the terminal keeps at least six lines); a tab strip (several
files, Ctrl+Tab to switch, Ctrl+W and × to close, the strip's × for all;
closing the last tab folds the editor), breadcrumbs down to the symbol
where Monaco knows the language's symbols, the gutter with folding, the
minimap, find and replace (Ctrl+F, Ctrl+H), go to line (Ctrl+G), Ln/Col,
copy path, open raw, the fold button (T, Alt+T in the terminal: the strip
stays as one line above the terminal, "3 files open"). Themes from the
brand palette, dark and light. The states as 4f draws them: binary (size,
Open raw), an image on a checker with its dimensions, a URL preview (the
sandboxed iframe, New tab, Refresh, Copy), a read refused (view only, or
file viewing off), a hosted session whose host is away (the files pane
says so, last read when). The Yard's focused tile and a guest's join page
open a file the same way, the Files pane beside the editor in place of
today's slide-over; a view-only guest gets the editor read-only. On a
phone the editor takes the screen and the terminal folds to a bar at the
foot that says what the agent is doing and opens as a sheet. Monaco in
`web/app/components/CodeEditor.vue` (lazy import; workers through Vite);
the area in `EditorArea.vue`; tabs and split in `utils/editorTabs.ts` with
vitest; Playwright for opening, switching, closing, folding, the split and
the states; the `desktop` smoke test unchanged.

## F3. Changes (4d)

Git status of the working directory with each file's added and removed
lines, refreshed on a timer and on every file event, "Refreshed as the
agent works · 8s ago" at the foot with the totals; a file's diff in the
Monaco diff editor, side by side or inline, "working directory vs HEAD",
Open file beside it; a crew member's changes against the run's base
("crew/users-api-…/core vs main @ 3f2a1c"), as the run page counts them;
"Not a git repository" when there is none (Touched still fills); the
Explorer shows M and A beside changed files. The Files tab gains its
section switch (Explorer, Changes with its count). Server: `git_status` and
`git_diff` over the terminal's connection (the viewer WebSocket, the host's
data channel), bounded (a diff cut at a size with `truncated`), the data
directory refused, the CLI wrapper for status until go-git's is measured
fast enough, go-git for the diffs; the same in `conductor host`. Go tests
with a scratch repository; vitest for the list's model; Playwright with
the stub editing files in the scratch repository.

## F4. Touched (4e)

The files the agent created, edited, read or deleted, newest first, each
with the tool, the agent and the time; a dot on a touched file in the
Explorer; the same events in the Activity tab and on the Events page, quiet
there by default; "Outside a repository this is the only list that still
fills". Server: the hook mappers read the path from the PostToolUse payloads
(Claude Code's `tool_input.file_path` and `notebook_path`; Codex's,
Cursor's, Copilot's and Goose's equivalents) into a `file` activity entry
with `op` (read, create, edit, delete), `path` (bounded) and `tool`;
coalesced (the same path and op within a few seconds is one entry); its
own bound, not the attention bucket; `conductor notify --event file --op
edit --path …` and the MCP tool `report_file` for agents without hooks; the
skill asks for it. Go tests for every mapper and the coalescing; Playwright
with the stub reporting file events.

## F5. Commits (4e)

`git log` since the session started on the working directory's branch (a
member's worktree branch), each commit with its message, time and author;
a commit opens as its diff against its parent in the diff editor, the tab
named by the short sha; "Commits the agent made here; nothing is pushed
from Conductor". Server: `git_log` and `git_show` through go-git (the
linked-worktree check here), bounded. The section switch gains Commits
with its count.

## F6. Editing (4b, 4f)

Save and Ctrl+S for a controller on a session with editing on; the tab's
dot for an unsaved file; "Save users.go?" on closing it; the save lands in
Activity ("Nate saved internal/api/users.go"); "Changed on disk since you
opened it · 08:34:10 · codex (Edit). Saving would overwrite that." with
Compare, Reload and Save anyway, from the file's modification time and
hash in the read's header compared before the write; a truncated read is
never saved. Server: `file_write` chunked under the frame size and bounded
in total, control role only, `ResolvePath` and the deny list as reads,
refused with `read_only` or `file_denied`; the `fileEdit` setting (`off`,
`control`; `CONDUCTOR_FILE_EDIT`; `--file-edit` for `conductor host`),
reported in the welcome; the host side writing on the developer's machine.
Go tests for the write, the bounds, the check and the setting; Playwright
saving through the stub's session and seeing the file change.

## F7. Comment on a line (5a, 5b, 5c)

Select lines in the editor: a bar offers Comment, Ask the agent and Copy
(Ctrl+Shift+M, Ctrl+Shift+A; the gutter shows a glyph on hover); the
composer anchored under the selection carries the quote (path, range, the
lines) and the words, and sends To chat, To the agent, or both; in the
thread a quote card (path and range, the lines, Open: the editor goes there
and highlights the range for a moment), the Sent to agent mark when the
agent got it, and "Lines moved since · now 18–20" when the lines are found
elsewhere in the current file (client-side, nothing new on the wire for
that); to the agent it is typed as `internal/api/users.go:14-16` with the
quote. Anyone here can comment to chat; Ask the agent needs control. The
chat message gains `quote` (bounded: a path, two line numbers, at most
twelve lines of at most 200 bytes each), kept with the message and in the
run's record. Go tests for the bound; vitest for the re-anchoring;
Playwright for the bar, the composer and the card.

## F8. Neovim in the editor (the real one, on the session's machine)

Proved on 2026-10-08 against Neovim 0.10.4 with the official Go client
(`github.com/neovim/go-client`, Apache 2.0): a child `nvim --embed` with a
UI attached (`ext_linegrid`, `ext_cmdline`, `ext_messages`, `ext_popupmenu`)
reports mode changes, the command line as it is typed, and messages such
as `"users.go" 20L, 400B written`; `nvim_buf_attach` streams every change
as `nvim_buf_lines_event` (first, last, lines); an autocmd calling
`rpcnotify` reports the cursor on `CursorMoved`/`CursorMovedI` and the
write on `BufWritePost`; `nvim_input` takes keys in Neovim notation
(`dd`, `jA appended<Esc>`, `:w<CR>`, `<lt>` for a literal `<`).

**What a person sees.** A keymap switch in the editor chrome (Default,
Neovim; kept per browser in `conductor.editor.keymap`). With Neovim
chosen, a file opened on a session whose machine has `nvim` is a Neovim
buffer shown in Monaco: Monaco draws the text, Neovim owns it. Every key
in the editor goes to Neovim; the mode drives the cursor (block, line,
underline) and a status line under the tabs shows `-- INSERT --`, the
command line as it is typed (`:w`), and Neovim's messages; visual mode
shows as Monaco's selection. `:w` writes the file on that machine (a
`file` event `write` by the person, so Changes and Touched follow), `:q`
closes the tab, `:e other` switches the buffer in place. The diff view,
the images and the URL preview are unchanged. A view-only guest gets no
Neovim (the editor stays read-only Monaco); a controller gets it when the
session's `fileEdit` setting allows editing. Where `nvim` is missing the
switch says "Neovim is not installed on <machine>" and the keymap stays
Default. The session page first; the Yard's focused tile and the guest
page later (the todo).

**Wire** (`internal/proto`, `protocol.ts`, `docs/protocol.md`), control
messages ≤ 8 KiB each: viewer → owner `nvim_open{reqId, path}` (control
role, `fileEdit` on, `nvim` on the machine; the path through
`ResolvePath` and the deny list like a read; at most 2 per connection and
8 per session), `nvim_input{id, keys}` (≤ 256 bytes, a per-connection
bucket), `nvim_close{id}`; owner → viewer `nvim_event{id, kind, …}` with
kinds `opened` (the id, `changedtick`), `lines` (`first`, `last`,
`lines[]`, cut into events that fit the bound, a line longer than 4 KiB
cut with `truncated`), `cursor` (`line`, `col`, `mode`, the visual
anchor), `mode`, `cmdline` (`show`, `content`, `pos`, `prompt`),
`message` (`text`, `kind`), `written` (`path`), `closed` (`reason`),
`error` (`code`, `message`); a refused open is `error{nvim_unavailable |
file_denied | too_many_requests}`. The server and `conductor host` dispatch
the same three messages (`ws_viewer.go`, `hostagent/peer.go`); the
switchyard relays them opaquely and drops `nvim_*` from view-role
connections as it drops `submit`.

**Server.** `internal/nvim`: `Available()`, `Open(ctx, dir, path,
Handler)`, `Input`, `Close`; the child's cwd is the session's working
directory, its environment the server's (the person's config loads), the
UI 80×24. `internal/session/nvim.go`: the per-subscription editors, the
policy, the bounds, the frames; `Detach` and `Stop` close them. `Options.
FileEdit` (`control`, `off`; config `fileEdit`, `CONDUCTOR_FILE_EDIT`,
`conductor host --file-edit`), reported in the welcome as `fileEdit` (this
role may edit) and `nvim` (installed here). The write lands in Activity as
a `file` event by the person with tool `nvim`.

**Client.** `utils/nvimKeys.ts` (a KeyboardEvent to Neovim notation;
printable keys as they are, `<` as `<lt>`, `<Esc>`, `<CR>`, `<BS>`,
`<Tab>`, arrows, `<C-x>`, `<M-x>`, `<S-Tab>`, F keys; nothing for a lone
modifier), `utils/nvimLines.ts` (a `lines` event to one Monaco edit),
`utils/editorTabs.ts` (the keymap setting), `composables/useNvim.ts` (one
editor's state: mode, cursor, visual, cmdline, message, closed; `input`),
`CodeEditor.vue` (keymap `nvim`: keys intercepted and sent, the model
updated from events, the cursor and selection set, the cursor style by
mode), `EditorArea.vue` (the switch, the status line, the notes, `:q`
closing the tab, Ctrl+W to Neovim in that keymap). Insert-mode typing
round-trips to the machine (local echo is a later step).

**Tests.** Go: `internal/nvim` against the real `nvim` (skipped with a
message where it is missing; CI installs 0.10.4 from the pinned release),
`internal/session` for the policy, the bounds, the chunking and the
frames, `internal/proto` for the shapes and sizes, `ws_e2e` and the host
loopback for a key reaching Neovim and a `lines` event coming back.
vitest for the keys, the edits and the setting. Playwright `vim.spec.ts`
with the real `nvim` on the e2e server: the switch, `dd` removing a line,
`i` typing and `<Esc>`, `:w` changing the file on disk with the message,
`:q` closing the tab, a view link getting no Neovim. By hand: the person's
own config and plugins, and a hosted session through the switchyard.

## Verification, end to end

Each layer's own tests, the full suites before each merge, headless renders
of the layer's screens at 1440 dark and light against the design's ids.
By hand: a real Claude Code session editing a file while it is open in the
editor (the changed-on-disk warning), a hosted session's files through the
switchyard, the editor on a phone.
