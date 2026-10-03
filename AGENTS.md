# Conductor repository instructions

Conductor launches coding agents in PTYs, shows them in a browser terminal and
shares sessions through links. One Go module, one Nuxt app, one binary.

## Map

| Path | Responsibility |
|---|---|
| `cmd/conductor` | entry point (`serve`, `host`, `notify`, `hooks`, `skill`, `up`, `crews`, `completion`, `version`) |
| `internal/cli` | flags, help and completion text; no business logic |
| `internal/config`, `internal/catalog` | JSON config with `CONDUCTOR_*` overrides; argv-based agent catalog with each agent's yolo, trust and session recipes |
| `internal/store` | atomic JSON documents in the data directory (`dataDir`) |
| `internal/agents` | per-agent hook adapters: assets under `dataDir/hooks`, launch injection, on-demand install into the agent's own config, the Conductor skill and where each agent reads it (`SkillPath`, installed at launch), hook payload mappers |
| `internal/crew` | crews (saved teams of agents) and their runs: model, persistence in `crews/<id>.json`, run engine |
| `internal/proto` | binary framing and JSON messages |
| `internal/pty`, `internal/session` | process lifecycle; ring buffer, fan-out, roles, resize policy, bounded file reads, viewer roster, activity log, attention, submissions (a paste, then Enter: the one way Conductor types into a session), the agent's own session |
| `internal/reach` | how the server is reached from outside: the public address by STUN, the TLS port mapped on the gateway (UPnP IGD, PCP, NAT-PMP), the self-check; mapped is never reported as reachable |
| `internal/certs` | the TLS listener's certificate: from Let's Encrypt through lego (the public address or domains; tls-alpn-01, http-01, dns-01), kept under `dataDir/tls`, renewed on its own; or from files; nothing served before the first issuance |
| `internal/share`, `internal/signal`, `internal/api` | share tokens; hosted-session brokering; HTTP + WebSocket surface |
| `internal/hostagent` | `conductor host`: pion WebRTC peers, relay sink, local terminal |
| `internal/notify` | `conductor notify`: attention and event reports from inside a session; hook payload mappers (Claude Code, Codex, agy, Copilot, Cursor, Goose), the agent's own session id among what they read |
| `internal/web` | embedded SPA (`internal/web/dist`, generated, never hand-edited) |
| `web/` | Nuxt 4 + @nuxt/ui 4 + xterm 6 workbench |
| `desktop/` | the Electron shell: starts `conductor serve` with a minted token and the settings it keeps, the handshake, health and restarts, the bridge the workbench reads the token from, WSL 2 on Windows |
| `docs/` | `protocol.md`, `architecture.md`, `features.md` (scope and deferred work), `design/brand.md` |

`internal/session.Local` is shared by the server and the host. Anything that
changes what a viewer sees belongs there, not in a transport. Attention
(bell/OSC detection, agent reports, clearing on input) lives in
`internal/session/attention.go` and `Local`; the browser keeps one live
session store in `web/app/composables/useAttention.ts` (streaming fetch of
`/api/events`), which every page reads instead of polling on its own.

## Rules

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
```

The e2e stub (`web/e2e/stub-agent.sh`) answers as Claude Code, Codex or an
impostor by `STUB_IDENTITY`, reports as its agent's hooks do by
`STUB_REPORT`, keeps a transcript per agent session, and draws the trust
question with `STUB_TRUST_DIALOG=1`; `web/e2e/conductor.e2e.json` wires it
with the real recipes. Run the narrowest package tests while iterating, then
the full set before finishing. A check that cannot run (no Docker, no network, no browser) is a
reported limitation, not a pass.

## Pinned versions

| Dependency | Version |
|---|---|
| Go | `go 1.26` (module); CI takes the latest 1.26 patch (`~1.26.0`, check-latest), which the standard library's vulnerability fixes need |
| honnef.co/go/tools (staticcheck, `make lint-static`) | v0.8.1 (2026.2.1) |
| golang.org/x/vuln (govulncheck, `make vuln`) | v1.8.0 |
| github.com/pion/webrtc/v4 | v4.2.21 |
| github.com/creack/pty | v1.1.24 |
| github.com/coder/websocket | v1.8.15 |
| github.com/pion/stun/v4 (direct for the reach lookup) | v4.0.1 |
| github.com/go-acme/lego/v5 (core, tls-alpn-01, http-01, dns-01 with cloudflare, exec, httpreq) | v5.5.2 |
| github.com/letsencrypt/pebble/v2 (test CA, `make test-pebble`; not a module dependency) | v2.10.1 |
| nuxt / @nuxt/ui / vue | 4.5.2 / 4.11.2 / 3.5.43 |
| @xterm/xterm (+ fit, webgl, web-links) | 6.0.0 (0.11.0, 0.19.0, 0.12.0) |
| shiki | 4.4.3 |
| @vue-flow/core (+ background, controls) | 1.48.2 (1.3.2, 1.1.3) |
| nuxt-charts (renders with Unovis through vue-chrts; v3 is the `next` tag) | 2.2.3 |
| @iconify-json/lucide (icon client bundle) | 1.2.137 |
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
