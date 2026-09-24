# Conductor repository instructions

Conductor launches coding agents in PTYs, shows them in a browser terminal and
shares sessions through links. One Go module, one Nuxt app, one binary.

## Map

| Path | Responsibility |
|---|---|
| `cmd/conductor` | entry point (`serve`, `host`, `version`) |
| `internal/cli` | flags only; no business logic |
| `internal/config`, `internal/catalog` | JSON config with `CONDUCTOR_*` overrides; argv-based agent catalog |
| `internal/proto` | binary framing and JSON messages |
| `internal/pty`, `internal/session` | process lifecycle; ring buffer, fan-out, roles, resize policy, bounded file reads |
| `internal/share`, `internal/signal`, `internal/api` | share tokens; hosted-session brokering; HTTP + WebSocket surface |
| `internal/hostagent` | `conductor host`: pion WebRTC peers, relay sink, local terminal |
| `internal/web` | embedded SPA (`internal/web/dist`, generated, never hand-edited) |
| `web/` | Nuxt 4 + @nuxt/ui 4 + xterm 6 workbench |
| `docs/` | `protocol.md`, `architecture.md`, `design/brand.md` |

`internal/session.Local` is shared by the server and the host. Anything that
changes what a viewer sees belongs there, not in a transport.

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
python3 scripts/brand_assets.py --check
```

Run the narrowest package tests while iterating, then the full set before
finishing. A check that cannot run (no Docker, no network, no browser) is a
reported limitation, not a pass.

## Pinned versions

| Dependency | Version |
|---|---|
| Go | `go 1.26` (module), toolchain 1.27 tested |
| github.com/pion/webrtc/v4 | v4.2.21 |
| github.com/creack/pty | v1.1.24 |
| github.com/coder/websocket | v1.8.15 |
| nuxt / @nuxt/ui / vue | 4.5.2 / 4.11.2 / 3.5.43 |
| @xterm/xterm (+ fit, webgl, web-links) | 6.0.0 (0.11.0, 0.19.0, 0.12.0) |
| shiki | 4.4.3 |

Upgrade deliberately and update this table.

## Adding things

- **An agent**: add a catalog entry in config (or `internal/catalog/defaults.go`
  for built-ins) with `id`, `name`, `command` and `allowArgs`.
- **An API route**: handler in `internal/api`, auth via `requireAdmin` or
  `authenticate`, a test in `api_test.go`, and the client call in
  `web/app/composables/useSessions.ts`.
- **A control message**: struct in `internal/proto/control.go`, dispatch in
  `ws_viewer.go` and `hostagent/peer.go`, TypeScript type in `protocol.ts`,
  handling in `transport/base.ts`, and a row in `docs/protocol.md`.
