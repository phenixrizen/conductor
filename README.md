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
make deps                 # checks go/node/npm/python3, then go mod download + npm ci
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
headless, or `--stun stun:host:3478` to override the ICE servers. An agent with
no hooks and no bell can still raise the needs-input badge:
`--signal-pattern '<regexp>'` (RE2, at most 200 bytes, not matching an empty
line) is matched against the last line of the terminal after 500 ms without
output; the Launch dialog's command carries it for agents whose catalog entry
has a pattern signal.

## The workbench

The sidebar is the session list, grouped into **Needs you**, **Running** and
**Exited**, with a filter box (**/**) and a **Launch agent** button (**N**).
Launch offers **Server** or **My machine**: the latter shows the exact
`conductor host` command to paste into a terminal (it carries your admin
token; keep it private) and the dialog closes by itself when that session
connects. The session page shows the terminal, a reply bar whenever the agent
is waiting (type an answer, or press the numbered buttons a Claude Code
permission prompt offers), and an inspector with **People** (who is attached,
their role and link, who is typing), **Files** (the file browser) and
**Activity** (joins, answers, signals and link changes). The header names the
agent, where it runs, the working directory and the git branch.

## Sharing

On a session page press **Share** and create a link with the **View** (watch
and open files) or **Control** (types into the agent, answers prompts) role,
with a label and an expiry. The link URL is `<publicUrl>/join/<token>` and the
token is shown once. Guests type a display name before joining; nothing
connects until they press **Join**, so a fetched link never exposes terminal
content. Revoking a link disconnects everyone using it. Set `publicUrl` to the
address your teammates use.

## Clickable links and file viewer

URLs printed by an agent are clickable: a click offers **Open in new tab** or
**Preview in pane** (a sandboxed iframe; sites that forbid embedding stay blank).
File locations such as `internal/api/server.go:42`, `./README.md` or Python
traceback lines are underlined when the file exists in the session's working
directory; clicking opens it in a side panel at that line with syntax
highlighting, directory browsing and a copy-path button. Reads go through the
terminal connection, so for hosted sessions the file comes from the developer's
machine. Limit reads with the `fileView` setting (`view`, `control` or `off`;
`--file-view` for `conductor host`). Server sessions never serve the server's
data directory, config file or catalog file (`catalogPath`), even inside a
working directory. A hosted session serves everything under its working
directory, so do not run `conductor host` from a directory that holds a
server's data directory or config file: anyone the session is shared with
could read the catalog's env secrets and the admin token.

## The wall

`/wall` is a grid of live tiles, one per active session, sized so that every
session fits on screen without scrolling; tiles shrink as sessions are added.
Each tile shows the session's whole screen scaled down. The chips in the
header filter tiles (**All**, **Needs you**, **Running**). A queue on the left
lists every session waiting for input with its prompt: answer from there
(**J**/**K** select, **Enter** types a reply) without opening the session,
and see who answered what under **Answered**. Click a tile and it expands in
place to a full-size, typeable terminal (`/wall?focus=<id>`, so the view is
linkable); **Esc**, the back arrow or the browser's Back button return to the
grid, and **Open page** goes to the full session page. The fullscreen button
turns a spare monitor into a status wall.

## The carousel

`/carousel` rotates through the active sessions one at a time, full size and
interactive: click into the terminal and type. Rotation pauses while the mouse
is over the pane or a terminal has keyboard focus, and the interval, auto-rotate
and pause controls are in the navbar. With **Follow input requests** on, the
carousel jumps to any session that needs input and holds there for two
intervals (at least 20 s) before rotating on; click **Holding** to release it
sooner. It never jumps away while you are typing. A film strip under the
terminal shows every session with the rotation progress, and the footer says
which session is next.

## Sidebar and keyboard shortcuts

The sidebar hides completely with the panel button in its header or
**Ctrl+B** (**⌘B** on a Mac); the choice is remembered per browser, and a
button in every page's navbar brings it back. Press **?** (or use
**Shortcuts** in the sidebar) for the list of shortcuts on the current screen.
Plain keys reach the agent while a terminal has focus, so they only work
outside it; hold **Alt** with the same key (**Alt+N**, **Alt+W**, **Alt+←**)
to use a shortcut without leaving the terminal. Your display name defaults to
the server's user and can be changed from the person button in the sidebar.

## Let agents tell Conductor they need you

Every session's process gets `CONDUCTOR_NOTIFY_URL` and `CONDUCTOR_NOTIFY_TOKEN`
in its environment, and `conductor notify` posts a state with them. Outside a
Conductor session the command exits silently, so it is safe to install
globally. Sessions that need a human show an amber **needs input** badge, move
to the top of the sidebar, count in the tab title, can raise a browser
notification, and appear in the wall queue. Claude Code permission requests
arrive with their options, so **Yes / Always / No** buttons appear wherever
the prompt is shown.

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
| `dataDir` | `CONDUCTOR_DATA_DIR` | `conductor.d` next to the config, else in the current directory | UI-managed state; must be writable, best outside `allowedRoots` |

### Upgrading

The server now keeps UI-managed state, such as agents added on the **Agents**
page, in a data directory, and must be able to create and write it at startup.
Unless `dataDir` or `CONDUCTOR_DATA_DIR` says otherwise, that is `conductor.d`
next to the config file, or in the current directory when there is none. If
the config lives somewhere the server user cannot write, such as `/etc`,
`conductor serve` now refuses to start with `data directory … is not usable`:
set `dataDir` in the config, or `CONDUCTOR_DATA_DIR`, to a writable directory
outside `allowedRoots`, for example `/var/lib/conductor`. The Docker image
already uses `/var/lib/conductor`, declared as a volume. At startup the server
logs the directory it uses, and warns when it overlaps an allowed root: agents
working there can read and commit its secrets.

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

**Adding agents from the UI.** Admins can add, change and hide agents from the
**Agents** page (**Add agent**) without editing the config file. The changes are
saved as `catalog.json` in the data directory (`dataDir`) and layered over the
configured catalog at startup: an agent with the ID of a built-in or configured
one replaces it, and deleting that entry brings the original back. Hiding an
agent removes it from the launch dialog and lists it under **Hidden** on the
Agents page, where **Restore** brings it back as it was; sessions already
running are not affected. The config file is never written. The server
refuses to start when `catalog.json` cannot be parsed or holds an invalid
agent, rather than overwrite it. The form checks the command against the
server's `PATH`, and an agent whose program is missing there can still be
saved, for use with `conductor host`.

Every agent, from the config file or the UI, is held to the same limits: the ID
matches `[a-z0-9-]{1,32}`, the name is at most 60 characters, the description
200, `command` has at most 32 elements of at most 4096 bytes each, `env` has at
most 32 keys, `envPassthrough` (names of server environment variables the agent
may inherit) at most 32 names, and a signal `pattern` (a regular expression) at
most 200 bytes that does not match an empty line.

## Security model and limits

- The admin token gates launching, listing, stopping, link management and
  editing the agent catalog; share tokens grant one role on one session; host
  tokens only allow registering hosted sessions. Tokens are compared in
  constant time and stored hashed.
- Editing the catalog is as powerful as the server user. An admin can add or
  replace any agent, built-in and configured ones included, with any argv and
  env, and it runs as the user running `conductor serve`; `disableDefaults` or
  a curated `catalog` does not limit what the **Agents** page can add. Env
  values saved from the UI, secrets included, are stored in `catalog.json`
  (mode 0600) in the data directory. The file viewer of a server session never
  serves that directory, the config file or the catalog file, but agents run
  as the same user and can read them. Changing a built-in or configured agent
  saves a full copy of it, env values included, that replaces the original
  until it is deleted: a secret rotated in the config file does not reach that
  agent while the copy exists.
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
The image keeps its data directory on the `/var/lib/conductor` volume.
