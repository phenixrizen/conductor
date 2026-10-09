# Tasks to do

What Conductor still owes, in one place: the bugs known and the features set
aside. Each item names the round that found or deferred it (its decisions
are in [features.md](features.md)) and why it waits. An item leaves this
list when it ships, with its tests at both ends (AGENTS.md), or when the
owner drops it.

## Bugs

- **An "error" badge on a shell crew member with no error event.** Seen
  twice on 2026-10-08 in the rc.4 app: `lead` (the `shell` agent,
  `/bin/bash -l`, no prompt) wore the red "error" badge right after the
  run launched, and `review` wore it after `DELETE /api/sessions/{id}`
  ended it with exit 0; the Events feed listed no `error` line for either
  and the badge was gone after the Events page was opened. Find what
  records the entry (the mark's `title` would have said) and whether a
  shell's login output or the stop path yields an `error` type. Platform:
  Windows app, WSL server.

A bug goes here with how to see it (the page, the steps, the platform),
what was expected, what is known of the cause, and the round or pull
request that fixes it; one that a person found by hand says so, and gets a
test that would have caught it when it is fixed.

- **The fullscreen button does nothing in the desktop app.** The owner,
  2026-10-08, on the Windows app (rc.4): the maximize icon in every page
  header (`FullscreenButton`, the `F` key) has no effect. The button
  shows because `document.fullscreenEnabled` is true in Electron, but the
  page's `requestFullscreen` does not take the window fullscreen there;
  the app's View menu has its own "Toggle Full Screen" (`togglefullscreen`).
  Fix: in the app, the button and `F` call the window's fullscreen through
  the bridge (`setFullScreen`, with the change reported back so the icon
  follows), and the layout's fullscreen state reads the window's; a
  desktop e2e check in `desktop/e2e/smoke.spec.ts`. Platform: Windows app.
- **The shortcuts table is too narrow to read.** The owner, 2026-10-08,
  on the Windows app: the `?` modal's three columns (the action, the keys
  in a terminal, the keys outside one; `ShortcutsModal`) sit so close that
  the columns are hard to tell apart. Fix: a wider modal (`sm:max-w-3xl`
  or the viewport's width below it), a column gap that reads as columns,
  a rule or tint between them, the headers repeated per group; checked
  against the design's shortcuts screen at 1440 and 390.
- **The Launch dialog takes very long to list the agents the first
  time.** The owner, 2026-10-08, on the Windows app (the server in WSL):
  opening Launch agent for the first time after the app starts shows an
  empty list for a long while. Likely cause: `GET /api/catalog` looks up
  every agent's command and runs the version probes of the ones found
  (`probeWorkers` at a time, `probeWait` each, "pending" past that), and
  the dialog shows nothing until that reply lands; inside WSL the first
  probes of thirteen agents take long. Fix:
  answer the list at once with what is known, mark availability as it
  comes (a `checking` state per row, a stream or a second fetch), cache
  the result across dialogs for the server's lifetime, and never block
  the list on a probe; a Go test that a slow probe does not delay the
  catalog, a Playwright check that the dialog lists agents within a
  second of opening.
- **The directory picker's lists are cut short.** The owner, 2026-10-08:
  choosing a starting working directory in the Launch dialog, the
  folder list stops before the end of a large directory. Cause: `GET
  /api/paths` returns at most `maxPathEntries` (50) entries of a
  prefix, in name order, with no word that more exist and no way to page
  or narrow. Fix: say "N more; type to narrow" when the list is cut, match
  the typed prefix on the server so the cut applies after the filter, and
  list directories before files (or directories only, since this picks a
  working directory); a Go test for the cut's flag and a Playwright check
  of the note. Platform: Windows app, WSL server.
- **The Yard's focused header overlaps at phone width.** Seen 2026-10-08 in
  a headless render at 390 wide (round 12, F2b): the status, transport and
  viewers badges run under Open page and Stop, and the title is gone. Cause:
  the navbar's right slot keeps every button and badge at every width. Fix:
  below `sm` the badges fold into the title row and the buttons into a menu,
  as the session page's navbar does; Playwright at 390 checking nothing
  overlaps (bounding boxes) on `/yard?focus=<id>`.
- **Codex's "update available" dialog is not seen as needing input.** Seen
  2026-10-08 while probing the trust question with the real Codex 0.159: at
  launch Codex drew "Update available · 0.159.0 → 0.161.0 / 1. Update now
  2. Skip 3. Skip until next version / enter continue · esc skip", and the
  session stayed with no attention (no bell, no hook yet, no pattern match)
  until a person looked. Cause: the attention sources are the bell, the
  hooks and the configured prompt pattern; the update dialog matches none.
  Fix: a second screen pattern per agent, or the trust watcher's pattern
  extended (Codex: `Update\s*available`), with the dialog's answers as
  choices like the trust question's ("Skip" = Down, Down, Enter), and a
  catalog note that `codex --no-update-check` (or the config's
  `check_for_update_on_startup = false`) avoids it. Tests: the stub drawing
  the dialog; Playwright seeing needs_input with the choices.
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

### The Files tab, what is left of round 12

Explorer (F1), the Monaco editor beside the terminal (F2), Changes (F3)
and Touched (F4) landed on 2026-10-08 (`docs/round12-plan.md`, the design
screens 4a–4g). Still to build, each a pull request from main:

- **Comment on a line (F7, 5a–5c):** select lines, Comment or Ask the
  agent, the quote card in the chat thread, re-anchoring when the lines
  move.
- **Neovim in the editor, what is left after F8:** typing in insert mode round-trips to the machine before it shows
  (a local echo, as vscode-neovim does, is the next step); Neovim's
  columns are bytes, so the cursor sits off by the multibyte characters
  before it on a line; a swap file or a prompt at `:e` refuses the open
  with Neovim's words; the Settings page has no keymap row (the button in
  the tab strip is the switch).
- **Touched everywhere:** the Yard's focused tile and the guest's join
  page keep no activity list, so their Files panes offer no Touched
  section and no dots; the activity replay is 50 entries, so a long
  session's Touched starts from what the page saw. A `since` on the
  replay, or a bounded `GET /api/sessions/{id}/activity`, would give both
  the whole list.
- **The Explorer's filter and unloaded folders:** the filter matches
  only the nodes the tree has fetched, so "users" typed before `internal`
  is expanded finds nothing (seen by hand on 2026-10-08). Either fetch
  the folders that are not loaded while a filter is typed (bounded: depth
  and count), or say "in the folders opened so far" under the box.
- **Files from more agents:** Codex names a file only through
  `apply_patch` (its shell reads and writes name none); the Copilot and
  Goose mappers yield no files, their PostToolUse payloads unverified
  against a real run; agy has no tool hook.

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
  viewers. Round 11 (C1–C6), with two differences `docs/features.md`
  records: chat lines are not activity entries, and the agent's own
  question is in the chat, with its choices.

### The sidebar

- **Simpler sessions and runs in the sidebar.** The owner, 2026-10-06: "it's
  still not the simplest UX for this and is still a bit confusing for me who
  helped to write this." Today the list is three sections by state (Needs
  you, Running, Exited), each holding loose sessions, crew runs under a run
  header, and hosted sessions under their machine, with Shared with you on
  top, a rail of avatars when collapsed, a filter box and the Launch button.
  The redesign was made with Claude Design and approved on 2026-10-06
  (the 3-series of "Conductor UI.dc.html": one mental model, actions on a
  row, the rail, phones, what to drop); the build runs as pull requests
  S1 (the model and the list), S2 (row actions), S3 (answer in the row),
  S4 (keyboard), S5 (header and foot), S6 (the rail), S7 (phone), each with
  Playwright for its screen. Still to do after the build: the five-task
  feedback script with two coworkers (find the agent asking you something;
  stop a run; share one member; open yesterday's run; tell a crew from a
  run), watched, to catch what the design missed. Round 10.

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

The rule for everything here (the owner, 2026-10-07): the terminal, the
chat and the files go over the WebRTC tunnel between the viewer and the
machine that runs the session; the switchyard introduces, relays only
when ICE fails, and serves as little as it can, so that its egress,
compute and cloud bill stay the cheapest possible.

- **A second factor for a control link.** The owner, 2026-10-08: joining
  a session with control through a share link should take a second
  secret beside the link, a short PIN or code the owner sets when the
  link is made (or one the app mints and shows next to the link), typed
  on the join card before the terminal opens; a view link needs none.
  The PIN never travels in the URL, is compared like a token
  (`share.Equal` on a hash), is rate-limited per address, and a wrong PIN
  three times revokes the link. The switchyard keeps the same check on its
  join page, since control links through it carry the same risk. Design
  screen for the join card and the link dialog; Go tests for the hash, the
  limit and the revoke; Playwright for the card.
- **A QR code to share a session to a phone.** The owner, 2026-10-08:
  the share link dialog shows a QR code of the link (view or control,
  with the PIN shown beside it, never inside the code), so a phone on the
  same room scans it instead of typing; the switchyard's own share page
  shows the same. Rendered in the browser from the link (a small QR
  encoder in `web/app/utils`, no new runtime dependency on the server);
  a vitest for the encoder's output against a known code and a Playwright
  check that the dialog carries `[data-share-qr]` with the link's text as
  its label. By hand: a phone scans it and joins.
- **Egress off the switchyard: the next pull request, on its own, not in
  the round 11 stack.** Measured 2026-10-07 on a phone-sized headless
  Chromium against a local server: a first join page load is 1.9 MiB on
  the wire, uncompressed (every `/_nuxt/` asset is immutable for a year,
  so a later link on the same browser costs 8 KiB, until a release changes
  the hashes), and the viewer and host WebSockets accept with the
  library's default, no permessage-deflate, so a relayed terminal's
  redraws leave the switchyard byte for byte; a TUI that redraws while it
  works is the likelier cost than the page. (1) `CompressionContextTakeover`
  on the viewer and host accepts and in `conductor host`'s dial: a relayed
  terminal compresses several times over, and the direct path is
  untouched. (2) Pre-compressed assets at build (`.br` and `.gz` beside
  each `/_nuxt/` file, `make web-build`) that `internal/web/embed.go`
  serves by `Accept-Encoding`, with `Vary`; the first load should come to
  about a quarter. (3) A list of what the join page loads first (xterm is
  half of it and needed) and the rest deferred. Tests: Go for the handler
  (the compressed file with its headers when accepted, the plain one
  otherwise) and for a viewer accept negotiating compression; Playwright
  that the join page's responses carry `content-encoding`. The
  switchyard's status page reports the bytes relayed this hour: the number
  to read before and after.
- **The workbench on a CDN.** The same `web/` build on Netlify, or another
  CDN, whichever costs the least for reasonable performance (Cloudflare
  Pages and GitHub Pages are the other candidates), so a join page and the
  workbench load from the edge and the switchyard serves only the API, the
  links and the relay. What it needs: the SPA built with its API origin
  configurable (the join page's `?server=` is the start; the workbench
  reads its server from where it was loaded), the switchyard's
  `allowedOrigins` naming the CDN origin for the join route and the
  session WebSocket, cache headers the CDN keeps (the immutable `/_nuxt/`
  ones), a release step that publishes the build, and a by-hand check from
  a phone. The desktop app and a self-hosted server keep serving their
  own copy. The owner, 2026-10-07.
- **The join route answering a browser origin named in Settings**, so a
  browser at a LAN address (not the desktop window) can open a joined link;
  today such a link shows as unreachable. Round 9.
- **A forgotten run revoking its links at the switchyard at once**; today
  they expire, or the orphan sweep takes them after seven days. Round 9.
- **Switchyard rosters and invite lifetimes beyond the share store's**: who
  is online, invites that name a person. Round 6.
- **TURN credential minting**, should a network need it; the relay covers
  the no-ICE case. Round 5.

### Crews and runs

- **Progress reported by the agents: how far a run's goal is, and each
  member's part of it.** The owner, 2026-10-07. The report comes from the
  agent, since only it knows: `conductor notify --event progress --done 3
  --of 7 --message "handlers done; tests next"`, steps done of steps
  planned and a few words, never a bare percentage (a model's "90 %" sits
  at 90 % for an hour; a count of steps moves when something ships), the
  same as an MCP tool, and the Conductor skill asks for it at each step
  done. Two agents give it for free: Claude Code's plan is its `TodoWrite`
  tool, whose `PostToolUse` payload the hook mapper already reads
  (`tool_name`; `tool_input.todos`, a status each), and Codex's
  `update_plan` is the same shape, so a session on either reports its
  steps without being asked; the others go through the skill. The server
  keeps `Progress{Done, Of, Note, At}` on the session (the note bounded as
  an activity message is), records a `progress` activity entry (routed as
  "worth knowing", so the feed and the webhooks see it), and on the run
  the goal's progress: the members' steps summed, equal weights unless the
  crew definition weights a member, a `done` report counting as complete,
  a report older than a bound marked stale, and never a substitute for
  `done`. Where it shows: a thin neutral bar (never a status colour; the
  state stays amber, green and grey) under a member's name on the run
  page's tiles, the graph's nodes and the timeline's rows; the goal's bar
  in the run header with "3 of 7 · handlers done; tests next · 4 min ago";
  the run's subtitle in the sidebar and the Yard's card; and for a single
  session working through a plan, the same bar in its header and on its
  row. Tests at both ends: Go for the report and its bounds, the mappers
  (a `TodoWrite` payload becomes steps), the aggregate and the staleness;
  vitest for the aggregate and the words; Playwright with the stub
  reporting steps and the bar moving on a tile and in the header.
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

### A mobile app

- **A Capacitor app** ([capacitorjs.com](https://capacitorjs.com)) wrapping
  the same workbench for iOS and Android: the sessions list, a session's
  page, the quick replies, the chat and the join page as an installed app,
  with push notifications for "needs you", the links in the OS keychain
  and the terminal over the WebRTC tunnel as the browser does it; the
  bundle ships inside the app, so the switchyard serves it nothing but the
  API and, when ICE fails, the relay. Needs the SPA built with its API
  origin configurable (shared with the CDN item), a Capacitor project under
  `mobile/`, the store accounts, and by-hand checks on a phone, as the
  phone layout's (3e) are. The owner, 2026-10-07.

### Hosted sessions (`conductor host`)

- **Yolo and Resume for hosted sessions.** Round 4.
- **A multi-session host protocol**: one `conductor host` serving several
  sessions over one connection; today one session per connection (the
  server's uplink publishes the same way). Round 5.
- **A Windows build of `conductor host`**: the release builds the server
  for Linux and macOS only, and the Windows package runs the Linux binary
  inside WSL; a native Windows host would need a ConPTY-backed PTY.
  Round 10.
