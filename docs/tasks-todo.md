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

- **The shortcuts table is too narrow to read.** The owner, 2026-10-08,
  on the Windows app: the `?` modal's three columns (the action, the keys
  in a terminal, the keys outside one; `ShortcutsModal`) sit so close that
  the columns are hard to tell apart. Fix: a wider modal (`sm:max-w-3xl`
  or the viewport's width below it), a column gap that reads as columns,
  a rule or tint between them, the headers repeated per group; checked
  against the design's shortcuts screen at 1440 and 390.
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

### The Files tab, after round 12

Every layer of round 12 landed on 2026-10-08 and 2026-10-09
(`docs/round12-plan.md`, design screens 4a–4g and 5a–5c): the Explorer,
the Monaco editor, Changes, Touched, Commits, editing, comments on lines
and the Neovim keymap. What is set aside:

- **Codex's hook trust, how long it lasts:** Codex 0.161 runs hooks only
  once trusted, and Conductor now holds its "Hooks need review" question
  with its answers as choices (round 13, G2c). Where Codex keeps the trust,
  and whether one trust lasts past the session, is not known: one trust in
  a throwaway home did not make `codex exec` run the hooks afterwards. A
  live check in the person's own Codex settles it; if it never lasts, the
  question comes at every Codex launch.

### The sidebar

- **The sidebar, watched in use.** The 3-series sidebar was built in round
  11 (S1–S7, `docs/features.md`). Still to do: the five-task feedback
  script with two coworkers (find the agent asking you something; stop a
  run; share one member; open yesterday's run; tell a crew from a run),
  watched, to catch what the design missed.

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
