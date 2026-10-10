# Conductor repository instructions

Conductor launches coding agents in PTYs, shows them in a browser terminal and
shares sessions through links. One Go module, one Nuxt app, one binary.

## Map

| Path | Responsibility |
|---|---|
| `cmd/conductor` | entry point (`serve`, `switchyard`, `host`, `notify`, `crew`, `mcp`, `hooks`, `skill`, `up`, `crews`, `completion`, `version`) |
| `internal/cli` | flags, help and completion text; no business logic |
| `internal/config`, `internal/catalog` | JSON config with `CONDUCTOR_*` overrides; argv-based agent catalog with each agent's yolo, trust and session recipes |
| `internal/store` | atomic JSON documents in the data directory (`dataDir`) |
| `internal/agents` | per-agent hook adapters: assets under `dataDir/hooks`, launch injection, on-demand install into the agent's own config, the Conductor skill and where each agent reads it (`SkillPath`, installed at launch), hook payload mappers |
| `internal/crew` | crews (saved teams of agents) and their runs: model, persistence in `crews/<id>.json`, run engine |
| `internal/gitcli`, `internal/gitrepo` | git for the Files tab: the working tree's status and a file at a revision through the git binary (argv, the C locale); the commit log and a commit's files through go-git (no binary needed; a crew's linked worktree opens through its common directory) |
| `internal/nvim` | the editor's Neovim: one `nvim --embed` per open file on the machine that runs the session, driven over msgpack-rpc (the official Go client); the buffer's changes, the cursor, the mode, the command line and the messages to a handler, keys in; `internal/session/nvim.go` puts it on the terminal's connection under the `fileEdit` policy |
| `internal/proto` | binary framing and JSON messages |
| `internal/pty`, `internal/session` | process lifecycle; ring buffer, fan-out, roles, resize policy, bounded file reads, viewer roster, activity log, attention, submissions (a paste, then Enter: the one way Conductor types into a session), the agent's own session |
| `internal/reach` | how the server is reached from outside: the public address by STUN, the TLS port mapped on the gateway (UPnP IGD, PCP, NAT-PMP), the self-check; mapped is never reported as reachable |
| `internal/certs` | the TLS listener's certificate: from Let's Encrypt through lego (the public address or domains; tls-alpn-01, http-01, dns-01), kept under `dataDir/tls`, renewed on its own; or from files; nothing served before the first issuance |
| `internal/share`, `internal/signal`, `internal/api` | share tokens; hosted-session brokering; HTTP + WebSocket surface |
| `internal/hostagent` | `conductor host`: pion WebRTC peers, relay sink, local terminal; the ICE UDP mux and address rewrite; the session's side of a paste invite (`AnswerPaste`) |
| `internal/paste` | the paste-invite blob (`cpi1.`: an SDP with every candidate, deflated, base64url), shared by the server and `web/app/utils/paste.ts` |
| `internal/notify` | `conductor notify`: attention and event reports from inside a session; hook payload mappers (Claude Code, Codex, agy, Copilot, Cursor, Goose), the agent's own session id among what they read |
| `internal/web` | embedded SPA (`internal/web/dist`, generated, never hand-edited) |
| `web/` | Nuxt 4 + @nuxt/ui 4 + xterm 6 workbench |
| `desktop/` | the Electron shell: starts `conductor serve` with a minted token and the settings it keeps, the handshake, health and restarts, the bridge the workbench reads the token from, WSL 2 on Windows |
| `docs/` | `protocol.md`, `architecture.md`, `features.md` (what each round decided and verified), `tasks-todo.md` (the bugs known and the features set aside, in one place), `design/brand.md` |

`internal/session.Local` is shared by the server and the host. Anything that
changes what a viewer sees belongs there, not in a transport. Attention
(bell/OSC detection, agent reports, clearing on input) lives in
`internal/session/attention.go` and `Local`; the browser keeps one live
session store in `web/app/composables/useAttention.ts` (streaming fetch of
`/api/events`), which every page reads instead of polling on its own.

## Rules

- **Every feature ships with tests at both ends.** The server or CLI side
  gets a Go test (`go test -race`), and the UI side a vitest unit test for
  its logic and a Playwright spec for what a person sees and does; a change
  with one and not the other is not done. Only what no harness can reach (a
  phone, a router, a real GPU, a Windows box) goes on the by-hand list, and
  it is recorded in `docs/features.md` when a person has done it.
- Protocol changes touch three places together: `internal/proto`,
  `web/app/utils/protocol.ts` and `docs/protocol.md`. Every new frame or message
  gets a size limit and a test.
- Stdlib first: `net/http` mux with method patterns, `log/slog`, `encoding/json`
  with `DisallowUnknownFields`. New Go dependencies need a reason in the PR.
- Commands are argv arrays. Never build a shell string from user input.
- Compare tokens with `share.Equal`; store only hashes; never log query strings.
- Server session working directories go through `resolveCwd`; file reads go
  through `session.ResolvePath`. Do not bypass either.
- Keep per-connection bounds (read limits, queues, in-flight file requests).
- UI components come from Nuxt UI; use the brand palette in `web/app/app.config.ts`
  and `docs/design/brand.md`. The junction mark is artwork, never a status light.
- Do not commit `internal/web/dist` contents (only `.gitkeep`), `web/.nuxt` or
  `web/.output`. Commit `web/package-lock.json`.
- **Windows and WSL: the person configures no networking, ever.** Never ask
  for `netsh interface portproxy`, `networkingMode=mirrored` in
  `.wslconfig`, a `.wslconfig` edit, or any other manual step to expose the
  server inside WSL. Whatever must cross Hyper-V's NAT, the desktop app
  forwards itself (today UDP for ICE, `desktop/src/udp-forwarder.ts`;
  forwarding the TLS listener and mapping the router from Windows are the
  app's job too, not yet done). Docs, setup screens, Settings text, test
  instructions and by-hand checklists follow this; a check that would need
  such a step on WSL is done from a machine that is not WSL, or waits for the
  app. The owner has said this twice; do not raise it again.

## Checks

```bash
make lint                      # gofmt -l, go vet
go test -race -count=1 ./...   # includes PTY, WebSocket and pion loopback tests
npm --prefix web run typecheck
npm --prefix web test          # vitest (link detection)
make web-build                 # nuxt generate + copy into internal/web/dist
make build-go                  # embeds whatever is in internal/web/dist
make test-e2e                  # Playwright (web/e2e): builds both, then a server with stub agents in Chromium 1117
make test-pebble               # certificates end to end against Let's Encrypt's Pebble (go install'ed when missing)
make test-live                 # the real Claude Code and Codex (CONDUCTOR_E2E_LIVE_REPO, keys or logins); weekly in CI
make desktop-test              # the Electron shell: tsc --noEmit and vitest (desktop/)
make lint-static               # staticcheck, pinned
make vuln                      # govulncheck (network); the standard library's findings need the latest Go patch
make test-network              # the built-in agents' sites on the network (nightly)
make test-recipes              # the recipe flags against the CLIs on this machine (nightly)
python3 scripts/brand_assets.py --check
python3 -m unittest discover -s scripts -p '*_test.py'  # the scripts' own tests (codex_review.py)
```

The e2e stub (`web/e2e/stub-agent.sh`) answers as Claude Code, Codex or an
impostor by `STUB_IDENTITY`, reports as its agent's hooks do by
`STUB_REPORT`, keeps a transcript per agent session, draws the trust
question with `STUB_TRUST_DIALOG=1` and Codex's "Hooks need review" when
`$HOME/.codex/stub-hooks-review` exists, and on the line `stub tools codex`
(or `copilot`, `agy`) replays that agent's captured hook payloads through
`conductor notify`; `web/e2e/conductor.e2e.json` wires it
with the real recipes. Run the narrowest package tests while iterating, then
the full set before finishing. Every harness that launches sessions sets
`CONDUCTOR_RENDEZVOUS=0` (the cli tests' `clearConductorEnv`, `web/e2e/server.ts`):
a server publishes its sessions to the public switchyard by default, and a
test's must stay on the machine. A check that cannot run (no Docker, no network, no browser) is a
reported limitation, not a pass.

## Reviews

Every pull request gets a Codex adversarial review before it is opened: a
second model questioning the design, not only the lines. It comes from the
Codex plugin for Claude Code (`openai/codex-plugin-cc`; `/plugin marketplace
add openai/codex-plugin-cc`, `/plugin install codex@openai-codex`,
`/codex:setup`, a Codex login). A person types
`/codex:adversarial-review --base origin/main <focus>`; an agent cannot
invoke that command and runs the same review through the plugin's script,
which `scripts/codex_review.py` finds (the installation Claude Code's
registry names for this repository, else the user's; never the newest
cached copy, and an ambiguous registry is refused):

```bash
python3 scripts/codex_review.py "<focus: the risks this change touches>"
```

- `--base` reviews the commits `origin/main...HEAD`, not the working tree:
  commit everything first. The pull request's body names the commit
  reviewed; commits after it (the review's own fixes included) get another
  review before the merge.
- The focus names the risks the change touches: races and ownership,
  link scopes and revocation, credentials in logs, what the switchyard can
  see, recovery after a restart, per-connection bounds.
- Its findings are claims, not facts. Each is reproduced (a test, or a run
  with `CONDUCTOR_RENDEZVOUS=0` and an isolated home) or traced in the code
  before it is fixed; one the code refutes is answered with the lines that
  refute it.
- The pull request's body lists the findings and what became of each. One
  outside the change's scope goes to `docs/tasks-todo.md`, never dropped
  silently.
- A security finding (a way to gain access, read what one should not, or
  deny service) never goes into this public repository, a pull request, a
  commit message or the todo until its fix has shipped: it goes to the
  owner's private security-findings document. The fix's pull request
  describes the change, not the attack.
- When the review cannot run (no login, no network), the pull request says
  so: a limitation, not a pass.

## Pinned versions

| Dependency | Version |
|---|---|
| Go | `go 1.26` (module) with `toolchain go1.26.9` pinned in `go.mod`, so every machine and CI run the patch the standard library's vulnerability fixes need (the go command fetches it on its own; CI's `~1.26.0` with check-latest lags a new patch by days) |
| honnef.co/go/tools (staticcheck, `make lint-static`) | v0.8.1 (2026.2.1) |
| golang.org/x/vuln (govulncheck, `make vuln`) | v1.8.0 |
| github.com/pion/webrtc/v4 | v4.2.21 |
| github.com/creack/pty | v1.1.24 |
| github.com/coder/websocket | v1.8.15 |
| github.com/pion/stun/v4 (direct for the reach lookup) | v4.0.1 |
| github.com/pion/ice/v4 (direct for the UDP mux type) | v4.4.4 |
| github.com/go-acme/lego/v5 (core, tls-alpn-01, http-01, dns-01 with cloudflare, exec, httpreq) | v5.5.2 |
| github.com/neovim/go-client (the editor's Neovim bridge: `nvim --embed` over msgpack-rpc) | v1.2.1 |
| github.com/go-git/go-git/v5 (the Files tab's Commits: the log and a commit's files; status and show stay with the git binary, which is faster for them) | v5.19.3 |
| Neovim (`nvim`, found on PATH at runtime; CI installs it for the bridge's tests) | v0.10.4 in CI; 0.10 or later on a machine |
| github.com/letsencrypt/pebble/v2 (test CA, `make test-pebble`; not a module dependency) | v2.10.1 |
| nuxt / @nuxt/ui / vue | 4.5.2 / 4.11.2 / 3.5.43 |
| @xterm/xterm (+ fit, webgl, web-links) | 6.0.0 (0.11.0, 0.19.0, 0.12.0) |
| shiki | 4.4.3 |
| @vue-flow/core (+ background, controls) | 1.48.2 (1.3.2, 1.1.3) |
| @iconify-json/lucide (icon client bundle) | 1.2.137 |
| monaco-editor (the Files editor, loaded with the first file) | 0.57.0 |
| @playwright/test (e2e; Chromium revision 1117, 125.0.6422.26) | 1.44.1 |
| electron / electron-builder (desktop) | 44.5.1 / 26.15.3 |
| @types/node (e2e type check) | 22.20.5 |

Upgrade deliberately and update this table.

## Adding things

- **An agent**: add a catalog entry in config (or `internal/catalog/defaults.go`
  for built-ins) with `id`, `name`, `command` and `allowArgs`.
- **An API route**: handler in `internal/api`, auth via `requireAdmin` or
  `authenticate`, a test in `internal/api` (`api_test.go` or the route family's
  `*_test.go`), and the client call in `web/app/composables/useSessions.ts`.
- **A control message**: struct in `internal/proto/control.go`, dispatch in
  `ws_viewer.go` and `hostagent/peer.go`, TypeScript type in `protocol.ts`,
  handling in `transport/base.ts`, and a row in `docs/protocol.md`.
