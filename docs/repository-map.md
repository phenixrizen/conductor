# Repository map


| Path | Responsibility |
|---|---|
| `cmd/conductor` | entry point (`serve`, `switchyard`, `host`, `notify`, `crew`, `mcp`, `hooks`, `skill`, `up`, `crews`, `completion`, `version`) |
| `internal/cli` | flags, help and completion text; no business logic |
| `internal/config`, `internal/catalog` | JSON config with `CONDUCTOR_*` overrides; argv-based agent catalog with each agent's yolo, trust and session recipes |
| `internal/store` | atomic JSON documents in the data directory (`dataDir`) |
| `internal/agents` | per-agent hook adapters: assets under `dataDir/hooks`, launch injection, on-demand install into the agent's own config, the Conductor skill and where each agent reads it (`SkillPath`, installed at launch), hook payload mappers |
| `internal/crew` | crews (saved teams of agents) and their runs: model, persistence in `crews/<id>.json`, run engine Managed runs (#334, row 5): `task.go` dispatches a crew's tasks as attempts (a fresh session in a worktree at the commit the task starts from) and takes their results, `verify.go` runs a result's checks in a detached checkout, one at a time across the server, `taskfile.go` writes the attempt's `.conductor/task.md` and `task.json`, `commands.go` is a person's commands with receipts (pause, cancel, retry, accept) and an attempt's task as read (row 6); a review's `needs_changes` reopens the task it reviewed and withdraws what rested on its result; `continue.go` continues a run after a restart (row 8); `messages.go` carries the members' messages (row 9) and `checkpoints.go` an attempt's blocker and checkpoints (row 10); every transition goes to the ledger before its side effect. |
| `internal/crew/ledger` | the managed runs' ledger (#334): one SQLite database per data directory, `runs/managed.sqlite`, the one importer of `modernc.org/sqlite`; a run's transitions as transactions, its events in sequence, its commands' receipts, its checks' evidence, the members' messages and the attempts' checkpoints (schema version 3); the owner lock `runs/owner.lock`; recovery at start |
| `internal/gitcli`, `internal/gitrepo` | git for the Files tab: the working tree's status and a file at a revision through the git binary (argv, the C locale); the commit log and a commit's files through go-git (no binary needed; a crew's linked worktree opens through its common directory) |
| `internal/nvim` | the editor's Neovim: one `nvim --embed` per open file on the machine that runs the session, driven over msgpack-rpc (the official Go client); the buffer's changes, the cursor, the mode, the command line and the messages to a handler, keys in; `internal/session/nvim.go` puts it on the terminal's connection under the `fileEdit` policy |
| `internal/proto` | binary framing and JSON messages |
| `internal/pty`, `internal/session` | process lifecycle; ring buffer, fan-out, roles, resize policy, bounded file reads, viewer roster, activity log, attention, submissions (a paste, then Enter: the one way Conductor types into a session), the agent's own session |
| `internal/proc` | one command outside a PTY, bounded: an argv array (never a shell), a directory, a timeout, the output's head and tail, the process group ended with it; the runner of the managed-run checks (#334) |
| `internal/reach` | how the server is reached from outside: the public address by STUN, the TLS port mapped on the gateway (UPnP IGD, PCP, NAT-PMP), the self-check; mapped is never reported as reachable |
| `internal/certs` | the TLS listener's certificate: from Let's Encrypt through lego (the public address or domains; tls-alpn-01, http-01, dns-01), kept under `dataDir/tls`, renewed on its own; or from files; nothing served before the first issuance |
| `internal/share`, `internal/signal`, `internal/api` | share tokens; hosted-session brokering; HTTP + WebSocket surface |
| `internal/hostagent` | `conductor host`: pion WebRTC peers, relay sink, local terminal; the ICE UDP mux and address rewrite; the session's side of a paste invite (`AnswerPaste`) |
| `internal/paste` | the paste-invite blob (`cpi1.`: an SDP with every candidate, deflated, base64url), shared by the server and `web/app/utils/paste.ts` |
| `internal/notify` | `conductor notify`: attention and event reports from inside a session; hook payload mappers (Claude Code, Codex, agy, Copilot, Cursor, Goose), the agent's own session id among what they read |
| `internal/web` | embedded SPA (`internal/web/dist`, generated, never hand-edited) |
| `web/` | Nuxt 4 + @nuxt/ui 4 + xterm 6 workbench |
| `desktop/` | the Electron shell: starts `conductor serve` with a minted token and the settings it keeps, the handshake, health and restarts, the bridge the workbench reads the token from, WSL 2 on Windows |
| `THIRD_PARTY_NOTICES`, `notices.go`, `scripts/notices.py` | the licences of what ships, generated (never hand-edited) from the binary's Go modules, the web bundle's npm packages (`web/build/bundledPackages.ts` lists them during the build) and the desktop app's; the root package embeds it and the server serves it at `/third-party-notices.txt` |
| `docs/` | `protocol.md`, `architecture.md`, `features.md` (what each round decided and verified), `plans/` (a change's plan, reviewed before its code; see Reviews), `design/brand.md`; the bugs known and the features set aside are GitHub issues (see Issues and the project board) |

`internal/session.Local` is shared by the server and the host. Anything that
changes what a viewer sees belongs there, not in a transport. Attention
(bell/OSC detection, agent reports, clearing on input) lives in
`internal/session/attention.go` and `Local`; the browser keeps one live
session store in `web/app/composables/useAttention.ts` (streaming fetch of
`/api/events`), which every page reads instead of polling on its own.


## Adding things

- **An agent**: add a catalog entry in config (or `internal/catalog/defaults.go`
  for built-ins) with `id`, `name`, `command` and `allowArgs`.
- **An API route**: handler in `internal/api`, auth via `requireAdmin` or
  `authenticate`, a test in `internal/api` (`api_test.go` or the route family's
  `*_test.go`), and the client call in `web/app/composables/useSessions.ts`.
- **A control message**: struct in `internal/proto/control.go`, dispatch in
  `ws_viewer.go` and `hostagent/peer.go`, TypeScript type in `protocol.ts`,
  handling in `transport/base.ts`, and a row in `docs/protocol.md`.
