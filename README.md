# Conductor

<img src="web/public/brand/conductor-logo.svg" alt="Conductor" width="320">

**Shared agent terminals.** Launch coding agents such as Claude Code, Codex or
Antigravity in real terminals, watch and drive them from the browser, and share
a live session with other developers through a link. Inspired by
[webtty](https://github.com/maxmcd/webtty), rebuilt as a small server with a
Nuxt UI workbench.

Two ways to run a session:

| | Server-hosted | Developer-hosted |
|---|---|---|
| Where the agent runs | On the machine running `conductor serve` | On your laptop via `conductor host` |
| How browsers reach it | WebSocket relay through the server | WebRTC data channel straight to your machine, with an automatic relay fallback through the server |
| Who starts it | Anyone with the admin token, from the UI or API | You, from your shell |

Both kinds show up in the same session list, use the same share links, the same
terminal and the same in-browser file viewer.

## Quick start

Requirements: Go 1.26+, Node 22+, and the agent CLIs you want to launch on the
`PATH` of whichever machine runs them.

```bash
make web-install          # npm ci
make build                # generates the SPA and builds bin/conductor with it embedded
CONDUCTOR_ADMIN_TOKEN=change-me ./bin/conductor serve
```

Open <http://localhost:8080>, paste the admin token when prompted, and press
**Launch agent**. Without `CONDUCTOR_ADMIN_TOKEN` the server prints a random
token at startup.

Host a session from your own machine instead:

```bash
CONDUCTOR_HOST_TOKEN=host-token ./bin/conductor host --server http://localhost:8080 -- claude
```

Your terminal is attached as a controller; the session appears in the UI as
`hosted`. Add `--relay-only` to skip WebRTC entirely, `--no-local` to run it
headless, or `--stun stun:host:3478` to override the ICE servers.

## Sharing

On a session page press **Share** and create a link with the `view` (read-only)
or `control` (can type) role, optionally with an expiry and a label. The link
URL is `<publicUrl>/join/<token>` and the token is shown once. Revoking a link
disconnects everyone using it. Set `publicUrl` to the address your teammates use.

## Clickable links and file viewer

URLs printed by an agent are clickable: a click offers **Open in new tab** or
**Preview in pane** (a sandboxed iframe; sites that forbid embedding stay blank).
File locations such as `internal/api/server.go:42`, `./README.md` or Python
traceback lines are underlined when the file exists in the session's working
directory; clicking opens it in a side panel at that line with syntax
highlighting, directory browsing and a copy-path button. Reads go through the
terminal connection, so for hosted sessions the file comes from the developer's
machine. Limit reads with the `fileView` setting (`view`, `control` or `off`).

## Let agents tell Conductor they need you

Every session's process gets `CONDUCTOR_NOTIFY_URL` and `CONDUCTOR_NOTIFY_TOKEN`
in its environment, and `conductor notify` posts a state with them. Outside a
Conductor session the command exits silently, so it is safe to install
globally. Sessions that need a human show a **needs input** badge, count in the
sidebar and tab title, can raise a browser notification, and the wall jumps to
them.

Claude Code (`~/.claude/settings.json` or a project's `.claude/settings.json`):

```json
{
  "hooks": {
    "Notification": [{ "hooks": [{ "type": "command", "command": "conductor notify --claude-hook" }] }],
    "Stop": [{ "hooks": [{ "type": "command", "command": "conductor notify --claude-hook" }] }],
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "conductor notify --claude-hook" }] }]
  }
}
```

Codex (`~/.codex/config.toml`):

```toml
notify = ["conductor", "notify", "--codex"]
```

Any tool that rings the terminal bell or emits an OSC 9 / OSC 777 notification is
detected with no configuration at all (for Codex set
`tui.notification_method = "bel"`). Run `conductor notify --state needs_input
--message "approve?"` from a script for anything else; `--state clear` resets it.

## Configuration

`conductor serve --config conductor.json` reads a JSON file; every field has a
`CONDUCTOR_*` environment override. See [`conductor.example.json`](conductor.example.json).

| Key | Env | Default | Purpose |
|---|---|---|---|
| `listen` | `CONDUCTOR_LISTEN` | `:8080` | bind address |
| `publicUrl` | `CONDUCTOR_PUBLIC_URL` | `http://localhost:8080` | base for share links |
| `adminToken` | `CONDUCTOR_ADMIN_TOKEN` | generated | protects management routes |
| `hostTokens` | `CONDUCTOR_HOST_TOKENS` | admin token only | tokens accepted from `conductor host` |
| `allowedRoots` | `CONDUCTOR_ALLOWED_ROOTS` | current directory | where server sessions may run |
| `defaultCwd` | `CONDUCTOR_DEFAULT_CWD` | current directory | working directory when a launch omits one |
| `allowedOrigins` | `CONDUCTOR_ALLOWED_ORIGINS` | same host | extra WebSocket origin patterns |
| `iceServers` | `CONDUCTOR_ICE_SERVERS` | Google STUN | handed to browsers and hosts |
| `relayTimeoutMs` | `CONDUCTOR_RELAY_TIMEOUT_MS` | `8000` | wait before falling back to relay |
| `fileView` | `CONDUCTOR_FILE_VIEW` | `view` | who may read session files |
| `scrollbackBytes`, `maxSessions`, `maxViewersPerSession`, `exitedRetention`, `envPassthrough` | matching `CONDUCTOR_*` | see example | limits |
| `catalog` / `catalogPath` | `CONDUCTOR_CATALOG_PATH` | built-ins | launchable agents |

### Agent catalog

Built-in entries: `claude`, `codex`, `agy` and `shell`. Add your own or override
an entry by ID:

```json
{
  "catalog": {
    "agents": [
      { "id": "claude", "name": "Claude Code", "command": ["claude", "--model", "opus"], "allowArgs": true },
      { "id": "my-tool", "name": "My tool", "command": ["/opt/tool/bin/run"], "env": { "TOOL_HOME": "/opt/tool" } }
    ]
  }
}
```

Commands are argv arrays and never pass through a shell. `allowArgs` lets the
launch form append extra arguments. `disableDefaults: true` drops the built-ins.

## Security model and limits

- The admin token gates launching, listing, stopping and link management; share
  tokens grant one role on one session; host tokens only allow registering hosted
  sessions. Tokens are compared in constant time and stored hashed.
- Server sessions run with an allowlisted environment and a working directory
  under `allowedRoots`. Hosted sessions run as you, with your environment.
- Terminal output is not persisted. Sessions and links live in memory and are
  lost on restart; hosted sessions reconnect and resume while the server is up.
- WebRTC needs UDP between the browser and the host; otherwise the relay is
  used automatically. No TURN credential minting yet.
- Put the server behind HTTPS before sharing links outside a trusted network.

## Development

```bash
make run        # Go API on :8080 with --dev (CORS and origins for localhost:3000)
make web-dev    # Nuxt dev server on :3000 proxying /api and /ws to :8080
make test       # go test -race ./...
make lint       # gofmt + go vet
npm --prefix web run typecheck && npm --prefix web test
```

If the Nuxt dev proxy does not upgrade WebSockets in your setup, run the dev
server with `NUXT_PUBLIC_API_BASE=http://localhost:8080`.

The wire protocol is documented in [docs/protocol.md](docs/protocol.md), the
architecture in [docs/architecture.md](docs/architecture.md), and the brand in
[docs/design/brand.md](docs/design/brand.md). A `Dockerfile` builds a server
image without agent CLIs; install them in a derived image or use `conductor host`.
