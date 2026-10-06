# Future features

What Conductor has set aside, in one place. Each item names the round that
deferred it (its decisions are in [features.md](features.md)) and why it
waits. An item leaves this list when it ships, with its tests at both ends
(AGENTS.md), or when the owner drops it.

## The desktop app

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

## The switchyard and sharing

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

## Crews and runs

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

## Agents and sessions

- **The agent's session id through the plugin agents** (OpenCode, oh-my-pi,
  pi's mid-process changes, Amp, DeepSeek Harness): their plugins would pass
  it to `conductor notify`; until then they relaunch instead of resuming.
  Round 4.
- **aider's chat-history file as its session handle.** Round 4.
- **A resume record that outlives `exitedRetention`** (a bounded store in
  the data directory), so a session can be resumed after it leaves the
  list; runs have this (`runs/`), sessions do not. Round 4.

## Hosted sessions (`conductor host`)

- **Yolo and Resume for hosted sessions.** Round 4.
- **A multi-session host protocol**: one `conductor host` serving several
  sessions over one connection; today one session per connection (the
  server's uplink publishes the same way). Round 5.
- **A Windows build of `conductor host`**: the release builds the server
  for Linux and macOS only, and the Windows package runs the Linux binary
  inside WSL; a native Windows host would need a ConPTY-backed PTY.
  Round 10.
