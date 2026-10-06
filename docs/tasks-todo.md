# Tasks to do

What Conductor still owes, in one place: the bugs known and the features set
aside. Each item names the round that found or deferred it (its decisions
are in [features.md](features.md)) and why it waits. An item leaves this
list when it ships, with its tests at both ends (AGENTS.md), or when the
owner drops it.

## Bugs

A bug goes here with how to see it (the page, the steps, the platform),
what was expected, what is known of the cause, and the round or pull
request that fixes it; one that a person found by hand says so, and gets a
test that would have caught it when it is fixed.

- **No newline in a Claude Code prompt from the browser terminal.** Seen by
  the owner, 2026-10-06, on Windows: Ctrl+Enter (and Shift+Enter) in a
  session's terminal submits the message instead of adding a line, as it
  would in Windows Terminal. Cause: xterm.js sends a plain carriage return
  for Enter whatever the modifier, and no key reaches Claude Code as a
  newline; Claude Code's own `/terminal-setup` teaches VS Code and iTerm2
  to send ESC CR (`\x1b\r`) for Shift+Enter, which it reads as a newline,
  and `\` then Enter is its fallback everywhere. Fix: the terminal's key
  handler (`TerminalView.vue`, `attachCustomKeyEventHandler`) sends
  `\x1b\r` for Shift+Enter and Ctrl+Enter, and the reply bar's
  multi-line submissions keep going through the paste path. Codex takes
  Ctrl+J for a newline; whether it reads ESC CR as one is to be checked,
  and a per-agent `newline` recipe added if the agents differ. Tests: vitest
  for the mapping; Playwright typing Shift+Enter into the stub and finding
  the sequence in its transcript.
- **The server log window is an empty dark box.** Seen by the owner,
  2026-10-06, on Windows, from the tray's Server log; it is the same on
  every platform. Cause: `showLog` in `desktop/src/main.ts` makes the window
  with no preload, so `window.conductorLog` (from `log-preload.ts`, which
  is built but never attached) is undefined and no line reaches the page;
  only the page's dark background shows. Fix: `preload:
  join(__dirname, 'log-preload.js')` on that window. Tests: a desktop unit
  test that the log window's options name the preload; the smoke test opens
  the log window and sees a line of the server's log.
- **A shared session takes the smallest window's size.** Seen by the owner,
  2026-10-06: a crew (the to-do example) shared with a coworker on a laptop
  shrinks to the laptop's columns and rows on the owner's 34-inch screen.
  Cause: the session has one PTY and one size, set by the latest
  control-role viewer that attached or resized (`Local.Resize`,
  "latest controller wins"); every viewer's terminal then follows that size
  (`TerminalView` on the `resize` message), so a control link opened on a
  small window resizes the session for everyone, and a view link scales
  instead. The fix, decided 2026-10-06 after weighing the options (largest
  wins punishes the small screen's typist; per-viewer sizes are impossible
  with one PTY):
  - the size belongs to one **driver**: by default the workbench that
    launched the session (the owner), never a link viewer, whatever its role;
  - every other viewer gets the `scale` fit the run tiles already use (the
    session's grid kept, the font scaled to the window), with **Fit to my
    window** in the terminal header to take the size over, which the header
    then says ("sized by Jane · 212 × 54") with one click to take it back;
  - a passive event (a join, a window resize) never moves the size; only
    that click does, which also ends the "whoever joined last" surprise
    between two owners' windows;
  - a crew run's members get the same, driver by member.
  Tests: Go for the driver rule in `Local` (a control viewer's resize refused
  until it takes the size, the hand-over, the owner's default); Playwright
  with two browser contexts, one wide and one narrow, checking the wide one's
  columns stay and the narrow one scales, then the hand-over.


## Features

### The browser terminal

- **Copy and paste wired up, as Windows Terminal has them.** Today xterm.js's
  own paste (Ctrl+V, Cmd+V, Shift+Insert into its textarea) reaches the
  session and Cmd+C copies a selection on macOS; nothing else is wired. To
  add: Ctrl+Shift+C and Ctrl+Shift+V (and Ctrl+Insert) everywhere; Ctrl+C
  with a selection copies it and clears it rather than interrupting the
  agent, without one it stays the interrupt; right-click pastes (a setting
  to turn it off) and middle-click on Linux; Copy and Paste in the
  terminal's own context menu; bracketed paste respected so a multi-line
  paste lands as one; a view-only viewer can copy and never paste; the
  Electron window's clipboard permission is already open for both. Tests:
  vitest for the key policy; Playwright with the clipboard permissions
  granted to the context, copying a line the stub printed and pasting into
  the stub's transcript. Round 10.
- **Mouse.** xterm.js forwards mouse clicks, drags and the wheel to the
  agent whenever the agent asks for mouse reporting (vim, htop, Codex's
  TUI do; Claude Code mostly does not), and selects text by drag otherwise,
  with Shift+drag selecting while an app holds the mouse. Nothing to build
  unless an agent's mouse mode is found wanting; a check of Codex and Claude
  Code with the mouse is on the by-hand list for the next round. Round 10.

### A Changes tab beside Files

What the agent changed and touched, on the session page and the join page,
beside Files (which stays the browser of the working directory; the
owner asked about `lsof` on 2026-10-06, which shows only the descriptors
open at that instant, never the source files an agent opens and closes in
milliseconds).

- **The list:** `git status` of the session's working directory, each file
  with its added and removed line counts, refreshed on a timer and on every
  tool-use event the agent's hooks report; a session outside a repository
  says so. For a crew member in a worktree the diff is against the run's
  base, as the run page's member counts already are (`DiffStat`).
- **The diff:** a file opens as its diff, highlighted the way the file
  viewer highlights, with the file viewer one click away; binary and very
  large diffs are capped and say so.
- **Touched files:** the hook events already name the tool and the path of
  every read, edit and write, so files the agent looked at get a mark in the
  Files tree and a "recently touched" group at the top of Changes, even
  outside git.
- **Hosted sessions:** git runs on the host, as file reads do: one new
  request and reply in `internal/proto`, `protocol.ts` and `docs/protocol.md`,
  bounded like the file reads, under the same `fileView` setting.
- Tests at both ends: Go for the status and diff reads (bounds, the
  worktree base, the deny list of the data directory, the host round trip in
  loopback); vitest for the list and diff models; Playwright with the stub
  editing a file in the scratch repository and the tab showing it, then its
  diff, on the session page and through a share link. Round 10.

### Chat beside the terminal

One chat per session and one per shared crew run, over the same path the
terminal takes (the viewer WebSocket for a server session, the WebRTC data
channel or the relay for a hosted one), so it works wherever a link works
and needs no other server. Decided 2026-10-06 after a brainstorm; the
design is being made with Claude Design (`docs/design/briefs/chat.md`).

- **Protocol:** `chat` (viewer → server, `{text}`, at most 2 KiB) and
  `chat` fan-out (server → every viewer, with the sender's name, role, time
  and an id), `chat_history` on `welcome` (the last 200 of a session, 500
  of a run), a rate bound per connection (10 a second, a burst of 20), in
  `internal/proto/control.go`, `protocol.ts` and `docs/protocol.md`.
- **The hub is where the terminal's hub is:** `session.Local` keeps the
  ring and fans out for a server session; `conductor host` (and the
  server's own uplink) does the same for a hosted one, across its peers and
  the relay; a crew run's chat rides any member's connection with
  `scope: run` and the engine fans it out to every member's viewers.
- **Who:** every viewer, view-only included, under the name they joined
  with and the role badge the roster shows; the agent itself is not in the
  chat, but a control viewer's **Send to agent** on a message submits it
  (the paste-then-Enter path), marked so in the thread.
- **Where it shows:** a Chat tab beside People, Files and Activity on the
  session page and the join page; a run-wide drawer on the run page and the
  crew join page; a sheet on phones; unread counts on the tab, the sidebar
  row and the rail avatar; chat lines in the session's activity log (type
  `chat`) and so in the Events feed, routed like "worth knowing".
- **Kept:** in memory, bounded; a run's chat goes into its record (`runs/`)
  so the Runs tab timeline shows it; a session's chat ends with the session.
- Tests at both ends: Go for the bounds, the fan-out, the run scope and the
  host hub (loopback); vitest for the thread model; Playwright with two
  contexts talking across a link, and a run chat seen by two members'
  viewers.

### The sidebar

- **Simpler sessions and runs in the sidebar.** The owner, 2026-10-06: "it's
  still not the simplest UX for this and is still a bit confusing for me who
  helped to write this." Today the list is three sections by state (Needs
  you, Running, Exited), each holding loose sessions, crew runs under a run
  header, and hosted sessions under their machine, with Shared with you on
  top, a rail of avatars when collapsed, a filter box and the Launch button.
  To do: gather feedback first (a five-task script given to two coworkers,
  watched: find the agent asking you something; stop a run; share one
  member; open yesterday's run; tell a crew from a run), then a redesign
  with Claude Design (`docs/design/briefs/sidebar.md`): one mental model
  (what is a session, a run, a crew, a machine), the actions a row needs
  without opening it, what the rail is for, what to drop, phones. Then the
  build, with Playwright for every row kind and action. Round 10.

### Releases

- **Dependency attributions for the shipped binaries.** The Go modules and
  the npm packages are permissively licensed, and Apache-2.0's NOTICE rule
  and their own terms want their copyright lines shipped with the binaries:
  a `THIRD_PARTY_NOTICES` generated at release (go-licenses for `go.mod`,
  license-checker for `web/` and `desktop/`), bundled into the packages, the
  Docker image and the switchyard pages, and linked from Settings → The
  app's License row; a CI check that it is current. Round 10.

### The desktop app

- **The app forwards the TLS listener into WSL and maps the router from
  Windows** (UPnP, PCP, NAT-PMP from the Windows side), so the public-address
  path works from WSL with nothing configured by hand; today only ICE's UDP
  port is forwarded (`desktop/src/udp-forwarder.ts`). Round 6. The person
  configures no networking, ever (AGENTS.md).
- **An invite pushed to the open window over a bridge event** instead of a
  full page load. Round 9. Needs an ack or a `did-finish-load` gate and a
  desktop test.
- **Share tokens in the OS keychain** instead of the browser's storage
  (`conductor.joined`). Round 9.
- **Signing and notarisation of the desktop builds**, once the secrets
  exist; the builds ship unsigned. Round 5.

### The switchyard and sharing

- **The join route answering a browser origin named in Settings**, so a
  browser at a LAN address (not the desktop window) can open a joined link;
  today such a link shows as unreachable. Round 9.
- **A page behind a machine's header in the sidebar** (the hosted sessions of
  one `conductor host` machine). Round 9.
- **A forgotten run revoking its links at the switchyard at once**; today
  they expire, or the orphan sweep takes them after seven days. Round 9.
- **Switchyard rosters and invite lifetimes beyond the share store's**: who
  is online, invites that name a person. Round 6.
- **TURN credential minting**, should a network need it; the relay covers
  the no-ICE case. Round 5.

### Crews and runs

- **Hosted crews** ("Runs on: my machine"), and with them the field in the
  crew editor. Round 8.
- **Resuming a run from its record after a server restart**; the record
  holds the agent session ids and worktrees it needs. Round 5.
- **A readiness wait for a resumed member**: it is `running` the moment its
  session exists, while its agent takes seconds to show its prompt, so a
  handoff typed meanwhile can be lost; the start path's `awaitReady` could
  hold it at `starting`. Round 5.
- **`conductor up --resume <run>`**; the API has
  `POST /api/runs/{run}/resume`. Round 4.

### Agents and sessions

- **The agent's session id through the plugin agents** (OpenCode, oh-my-pi,
  pi's mid-process changes, Amp, DeepSeek Harness): their plugins would pass
  it to `conductor notify`; until then they relaunch instead of resuming.
  Round 4.
- **aider's chat-history file as its session handle.** Round 4.
- **A resume record that outlives `exitedRetention`** (a bounded store in
  the data directory), so a session can be resumed after it leaves the
  list; runs have this (`runs/`), sessions do not. Round 4.

### Hosted sessions (`conductor host`)

- **Yolo and Resume for hosted sessions.** Round 4.
- **A multi-session host protocol**: one `conductor host` serving several
  sessions over one connection; today one session per connection (the
  server's uplink publishes the same way). Round 5.
- **A Windows build of `conductor host`**: the release builds the server
  for Linux and macOS only, and the Windows package runs the Linux binary
  inside WSL; a native Windows host would need a ConPTY-backed PTY.
  Round 10.
