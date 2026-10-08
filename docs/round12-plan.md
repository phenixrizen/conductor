# Round 12 plan: the Files tab as four sections, and a Monaco editor beside the terminal

Asked on 2026-10-08, from the design screens 4a–4g and 5a–5c in
"Conductor UI mockups" (the files "Files - Explorer", "Files - File open",
"Files - Several files", "Files - Changes", "Files - Touched and Commits",
"Files - States", "Files - Yard and guest and phone", "Files - Comment on a
line"). One place for everything about the files an agent works on, so
nobody leaves Conductor for an editor, a git client or a file manager.

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
- **Vim keys**: the owner asked for monaco-neovim-wasm (the real Neovim in
  WebAssembly driving Monaco). As of 2026-10-08 its packages and repository
  carry no license, so it cannot ship in Conductor until its author adds
  one; the keymap setting is designed so it slots in then, and monaco-vim
  (MIT) is the interim if the owner wants Vim keys before that.
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
| F8 Vim keys | nothing | nothing |

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

## F8. Vim keys

A keymap setting (Default, Vim) in the editor's chrome and in Settings,
kept per browser; loaded only when chosen. The implementation waits on the
owner's decision about monaco-neovim-wasm's license (see Decisions).

## Verification, end to end

Each layer's own tests, the full suites before each merge, headless renders
of the layer's screens at 1440 dark and light against the design's ids.
By hand: a real Claude Code session editing a file while it is open in the
editor (the changed-on-disk warning), a hosted session's files through the
switchyard, the editor on a phone.
