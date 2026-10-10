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

These five came from a Codex review of the repository on 2026-10-10,
each reproduced with a Go test (round 15); none is fixed yet.

- **A stopped run can be resumed twice.** A run's page header offers
  "Resume as new run" for any stopped run (`CrewRunHeader.vue`), also once
  it has been resumed (the Runs tab hides it then). Pressed again while the
  first continuation runs, it launches a second one on the same agent
  conversations and worktrees: two agents on one branch, each appending
  its own history to one transcript, and the original run's `resumedBy`
  now names the second, so the link to the first is lost. Cause:
  `ResumeRun` (`internal/crew/run.go`) checks only that the run stopped,
  and sets `resumedBy` after letting go of the lock. Two more ways to the
  same place: a standalone session resume checks `liveAgentSession`
  (`internal/api/resume.go`) and then launches with nothing reserved, so
  concurrent requests each launch (8 of 8); and `ResumeMember` marks a
  member `starting` while keeping its ended session's id, so a second call
  at the same time passes too, and the run's Stop then misses one process.
  Expected: one live session per agent conversation; a second request
  answered `409` naming the continuation, which the page opens. The fix
  proposed: a lease per agent conversation taken before the process
  starts and released when the launch fails or the session ends, a
  run-level "resuming" reservation, `ResumeMember` refusing a member
  already `starting`, and the header's button hidden once a run was
  resumed.
- **A run's record offers Resume and Open, and both answer 404.** Crews →
  a crew → Runs lists the runs that ended before the server restarted
  from their records, with the same buttons as a live run's; every run
  route looks in memory only, and `CrewRunsPanel.vue` does not tell a
  record from a live run. Expected: a record opens read-only, its Resume
  hidden until "Resuming a run from its record" (below, Crews and runs)
  exists.
- **A slow shutdown loses run records.** With many runs live, stopping
  the server lost 17 to 20 of 20 records in the test. Cause: the 10 s
  shutdown budget (`internal/cli/serve.go`), runs stopped one at a time
  with up to 5 s each, and `Records.Close` dropping saves still pending.
  Expected: every run's record written before the server exits.
- **An agent's question is cleared by the end of its turn.** The Conductor
  skill tells an agent to ask with `needs_input` and says the session shows
  as needing input until someone replies (`internal/agents/skill.go`), but
  the turn's Stop hook then sets `done` (`internal/session/attention.go`):
  the amber state goes, and in a crew the next member starts with the
  question unanswered. Expected: an agent's `needs_input` holds until input
  arrives.
- **Opening a file of 1 MiB or more disconnects the viewer.** The Files
  tab cuts a file's body at exactly 1 MiB (`internal/session/files.go`);
  with its header the frame is over the viewer's 1 MiB queue mark, so the
  viewer is dropped as one that stopped reading, and the "truncated" badge
  never shows. Expected: the first part of the file, marked truncated. The
  fix proposed: cut the body at 1 MiB less the header's bound.

## Features

### The browser terminal

- **Mouse, checked by hand with the real agents.** xterm.js forwards mouse
  clicks, drags and the wheel to the agent whenever the agent asks for
  mouse reporting (vim, htop, Codex's TUI do; Claude Code mostly does not),
  and selects text by drag otherwise, with Shift+drag selecting while an
  app holds the mouse (not on a Mac, where nothing takes the mouse back); a
  right-click is the program's then too (round 15, with a probe program in
  Playwright). Still to do: clicks, drags, the
  wheel, Shift+drag and right-click with the real Codex and Claude Code in
  the installed app, with the next release candidate. Round 10.

### The sidebar

- **The sidebar, watched in use.** The 3-series sidebar was built in round
  11 (S1–S7, `docs/features.md`). The five tasks (find the agent asking
  you something; share one member; stop a run; open yesterday's run; tell
  a crew from a run) are written out in `docs/sidebar-tasks.md`. Still to
  do: the owner runs them with two coworkers, watched, to catch what the
  design missed.

### The desktop app

- **The app forwards the TLS listener into WSL and maps the router from
  Windows** (UPnP, PCP, NAT-PMP from the Windows side), so the public-address
  path works from WSL with nothing configured by hand; today only ICE's UDP
  port is forwarded (`desktop/src/udp-forwarder.ts`). Round 6. The person
  configures no networking, ever (AGENTS.md).
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
  holds the agent session ids and worktrees it needs. Round 5. The review
  of 2026-10-10 proposed how: a journal in `runs/<id>.json` from launch on
  (each member's prompt, arguments, base commit, worktree, agent
  conversation and state); at start, runs still marked running become
  `interrupted`, never relaunched on their own; resume reads the record.
  Terminal output stays unsaved.
- **An outcome, not only "done", for unattended runs.** A member's turn
  ending is `done` (an agent at rest), the documented contract that
  supervised crews start the next member on. A run nobody watches needs to
  know whether the work succeeded: `conductor notify --event outcome
  --status completed|blocked|failed` and an MCP tool, and per member an
  opt-in start condition (`start.on: done | outcome | check`). A product
  decision for the owner, when unattended runs are wanted. Round 15.
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
