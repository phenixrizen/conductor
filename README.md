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
and pause controls are in the navbar. With **Follow routed events** on, the
carousel follows what the **Wall jump** column on the Events page routes to
it, input requests and handoffs unless you change it. It jumps to a session
that needs input and holds there for two intervals (at least 20 s) before
rotating on; click **Holding** to release it sooner. Any other routed event
moves it to its session once, without holding, and never while it holds on a
session that needs input. It never jumps away while you are typing. A film
strip under the terminal shows every session with the rotation progress, and
the footer says which session is next.

## Sidebar and keyboard shortcuts

The sidebar hides completely with the panel button in its header or
**Ctrl+B** (**⌘B** on a Mac); the choice is remembered per browser, and a
button in every page's navbar brings it back. Press **?** (or use
**Shortcuts** in the sidebar) for the list of shortcuts on the current screen.
Plain keys reach the agent while a terminal has focus, so they only work
outside it; hold **Alt** with the same key (**Alt+N**, **Alt+W**, **Alt+←**)
to use a shortcut without leaving the terminal. Your display name defaults to
the server's user and can be changed from the person button in the sidebar.

## Events and hooks

Every session's process gets `CONDUCTOR_NOTIFY_URL` and `CONDUCTOR_NOTIFY_TOKEN`
in its environment, and `conductor notify` reports with them: an attention
state (`--state needs_input`, `working`, `done` or `clear`) or an event
(`--event progress`, `artifact`, `handoff`, `tool_use`, `tool_denied` or
`error`). Outside a Conductor session the command exits silently, so it is
safe to install globally. An attention report (the state flags, `--event` with
an attention word, or a hook mapped to one) that the server answers `429` is
tried again after 0.1, 0.25, 0.5, 1 and 2 s, within the 5 s the command takes
at most; an event is tried once. Sessions that need a human show an amber
**needs input** badge, move to the top of the sidebar, count in the tab title,
can raise a browser notification, and appear in the wall queue. Claude Code
permission requests arrive with their options, so **Yes / Always / No** buttons
appear wherever the prompt is shown. Every report also lands in the session's
activity log and in the live feed of the **Events** page.

**Routing.** The Events page's matrix sets, per event type and per browser,
what an event does: a labelled badge on its session in the sidebar and on its
wall tile (until the session reports `working` or `needs_input`, or you open
it), a browser notification with the chime (as switched on under the sidebar's
alerts), a carousel jump while it follows, and a line in the live feed, which
keeps the last 500 events in memory. Routes are saved in the browser. An
artifact's URL becomes a link only when it is `http(s)`.

**Wired at launch.** Conductor has a hook adapter for every built-in agent but
the shell. At startup `conductor serve` writes the hook files into `hooks/` in
its data directory, naming its own binary, and an agent whose catalog signal
is `hook` gets them when the server launches it, with nothing written to the
agent's own config: Claude Code through `--settings`, Codex through
`-c notify=…`, pi through `--extension`, aider through its notification
environment variables. `conductor host --agent <id> -- <command>` does the same
on your machine, with the hook files in `~/.conductor/hooks` (an older
`~/.local/state/conductor/hooks` is kept while it exists).

**Installed on demand.** The other agents read hooks only from their own
config, and so do the agents above when something other than Conductor starts
them. The **Events** page lists every adapter with what it reports and whether
its hooks are installed, the snippet to paste, and **Install on this machine**,
which adds Conductor's hooks to the agent's config in the server user's home,
next to your own settings and hooks and never over them: entries in its hook
lists, a marked block, or a file of Conductor's own. The same from a shell, for
whoever runs it, a host included:

```bash
conductor hooks install claude    # one adapter, or: conductor hooks install all
conductor hooks status            # which agents have the hooks, and where
```

`install` prints the files it wrote, and a second run changes nothing. What it
cannot do from a file it leaves to you, and says how: DeepSeek Harness, a
developer preview, is always installed by hand, and a Codex `hooks.json` or a
`SKILL.md` of your own is never overwritten. It prints the snippet only when
the snippet is what is missing, the agent's hooks; a `SKILL.md` of your own
gets its own instruction instead (`conductor skill` prints Conductor's). Both
commands take the hooks, and the conductor binary they run, from `hooks/` in
the data directory `--data-dir DIR` names, by default the one
`conductor serve` uses without `dataDir`: `CONDUCTOR_DATA_DIR`, else
`~/.conductor` (or an older `./conductor.d` while `~/.conductor` holds no
server data); when no server wrote hooks there, or the binary they name is
gone, they say so and use the binary you run; when the hooks there were
written by another version of Conductor than the one you run, or by one too
old to record its version, they warn and go on. They refuse a `hooks/` that is
not yours or not 0700, as
`conductor serve` writes it (so a `conductor.d/hooks` that a checkout made 0755
is refused too, should an older `./conductor.d` still be the data directory),
since its commands would go into your agents' configs. `--home DIR` names
another home directory of yours. A home that belongs to another user is
refused, because what Conductor wrote there would belong to you: install for
that user as that user (`sudo -u <user> conductor hooks install …`). The one
exception is your own home, the one `HOME` names, when you may write it, as in
a container whose home belongs to another uid; it never applies to root, since
under `sudo` `HOME` may still name the invoking user's home.

**Agents without hooks.** Any tool that rings the terminal bell or emits an
OSC 9 / OSC 777 notification is detected with no configuration at all. A
catalog signal of kind `pattern` watches the last line of the screen instead,
as the built-in Cursor CLI entry does for its prompt. Run `conductor notify
--state needs_input --message "approve?"` from a script for anything else;
`--state clear` resets it.

**The Conductor skill.** `conductor skill` prints a `SKILL.md` that teaches an
agent to report on its own: progress (`--event progress --message "4/7
handlers"`), an artifact such as a pull request (`--event artifact --url …`),
a handoff to another crew member (`--event handoff --to …`) and a decision it
cannot make (`--state needs_input`). Installing the hooks for Claude Code,
Codex, pi or Goose also puts the skill in their skills directory
(`~/.claude/skills/conductor/`, `~/.codex/skills/conductor/`, or
`~/.agents/skills/conductor/` for pi and Goose), and the server keeps a copy
in `hooks/skills/conductor/SKILL.md`. Its commands run
`"${CONDUCTOR_BIN:-conductor}"`: every session carries `CONDUCTOR_BIN`, the
absolute path of the binary its hooks run, while the manual snippets below
assume `conductor` on the `PATH`.

<details>
<summary>Wiring the hooks by hand</summary>

`conductor hooks install` and the Events page write these for you, naming the
binary by its absolute path. Written by hand as below, `conductor` must be on
the `PATH` the agent runs its hooks with. A session Conductor launches already
gets Claude Code's hooks through `--settings`: add them to your own settings
for Claude Code started elsewhere.

- Claude Code (`~/.claude/settings.json` or a project's `.claude/settings.json`):

  ```json
  {
    "hooks": {
      "Notification": [{ "hooks": [{ "type": "command", "command": "conductor notify --claude-hook" }] }],
      "Stop": [{ "hooks": [{ "type": "command", "command": "conductor notify --claude-hook" }] }],
      "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "conductor notify --claude-hook" }] }]
    }
  }
  ```

- Codex (`~/.codex/config.toml`, above its first `[table]`; set
  `tui.notification_method = "bel"` too for the bell):

  ```toml
  notify = ["conductor", "notify", "--codex"]
  ```

- Every other agent: the snippet on its card on the Events page, which is also
  its file in `hooks/` in the server's data directory.

</details>

### Webhooks

The server can also pass events on to other services. A webhook in the
config is a URL the server POSTs to for every entry, from any session, of
the event types the webhook lists. The **Events** page shows the webhooks,
read-only, in the routing matrix's **Webhook** column.

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/conductor",
      "events": ["needs_input", "exit_nonzero", "artifact", "error"],
      "secret": "a long random string"
    }
  ]
}
```

`events` takes the Events page's names (`needs_input`, `done`, `working`,
`tool_denied`, `progress`, `artifact`, `handoff`, `error`, `exit_nonzero`,
`tool_use`) and the entry types (`attention`, which covers all three
attention states, `status`, `join`, `leave`, `input` and `link`).
`exit_nonzero` is a process that exited on its own with a non-zero code,
never one an admin stopped. There are at most 16 webhooks, and a URL is
`http` or `https` of at most 2048 bytes. `CONDUCTOR_WEBHOOKS` takes the same
array as JSON and replaces the config file's.

Each entry is one request, naming its session and carrying the entry as the
Events feed streams it, without `state` (an attention entry's state is the
request's `X-Conductor-Event`):

```http
POST /conductor HTTP/1.1
Content-Type: application/json
X-Conductor-Event: needs_input
X-Conductor-Signature: sha256=<hex HMAC-SHA256 of the body, keyed with the secret>

{"sessionId":"…","session":{"id":"…","name":"api-sweep","agentId":"claude"},"entry":{"at":"2026-09-29T12:00:00.123Z","type":"attention","message":"Claude needs your permission to use Bash"}}
```

`X-Conductor-Event` is the event type the webhook listed that the entry is:
an attention entry is the attention state it records, the `status` entry of
a failed process is `exit_nonzero`, and when a webhook lists both, the
Events page's name wins. `X-Conductor-Signature` is sent only when the
webhook has a `secret`. Check it against the body as received, before
parsing it:

```go
func signed(body []byte, header, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal([]byte(header), []byte("sha256="+hex.EncodeToString(mac.Sum(nil))))
}
```

or by hand, with the body saved byte for byte in `body.json`:

```bash
echo "sha256=$(openssl dgst -sha256 -hmac "$SECRET" -r < body.json | cut -d' ' -f1)"
```

Each webhook has a queue of 256 entries, which drops its oldest when it is
full, and delivers them one at a time, so a slow endpoint delays neither the
sessions nor the other webhooks. A delivery is tried once, with 5 seconds to
answer; redirects are not followed, and an answer other than 2xx is logged as
a warning. When the server shuts down it stops the webhooks first, so the
`stopped` statuses of the sessions it stops then are not delivered. User info
in a URL (`https://user:password@host/…`) is sent as `Authorization: Basic`,
as Go's HTTP client does. The Events page and `GET /api/integrations` show a
webhook's URL without its user info, query string and fragment, and never its
secret, but with its path: for a service that puts its credential in the path
(Slack, Discord), admins see it there. Logs name a webhook by its place in the
list and its host.

**Private addresses.** A webhook may not point at a loopback, link-local,
private (unique-local in IPv6), shared (CGNAT, `100.64.0.0/10`, where some
clouds keep their metadata service) or unspecified address, nor at a
deprecated IPv4-compatible IPv6 address (`::a.b.c.d`); an IPv6 address that
carries an IPv4 address counts as the IPv4 address it reaches: NAT64
(`64:ff9b::/96`, or a /96 in `64:ff9b:1::/48`), 6to4 (`2002::/16`), Teredo
(`2001::/32`, its server and its client) and SIIT (`::ffff:0:a.b.c.d`). An
address of `64:ff9b:1::/48` is read as a /96; one in the form a shorter NAT64
prefix gives (u octet zero and the last three bytes zero), or with a non-zero
u octet, is refused; `allowPrivate` sends to it. That keeps webhooks away from
the services of the server's machine and network that are not published; it
does not refuse the server's own public address. At startup the server looks
up the webhooks' hosts, all at once and for at most 2 seconds, and refuses to
start when one resolves to such an address; a host that does not resolve in
time is only logged as a warning, since DNS may be down while the server
starts. Before every connection it looks the host up again and connects only
to the addresses that pass, trying the next one when an address does not
answer (and the other address family in parallel), so a name pointed at a
private address later (DNS rebinding) reaches nothing; it never goes through
a proxy.
`"allowPrivate": true` lifts the rule for one webhook, for an endpoint on
your own network.

## Crews

A crew is a saved team of agents; launching one starts a **run**, with a
session for each member as it starts. Build crews on the **Crews** page (**G**
then **R**): a name, a shared goal, a working directory, and up to 12 members.
Each member has a name, an agent from the catalog, optional extra arguments (for
agents that take them), a role prompt and a start condition. The goal reaches the role prompts
as `$GOAL` (or `${GOAL}`), which is replaced by the goal before the prompt is
typed. Crews are saved as `crews.json` in the data directory and run on the
server only for now (members are ordinary server sessions, under
`allowedRoots`); the **My machine** option is disabled.

A member starts `immediately` at launch, `after <member>` once that member,
with its own prompt typed, first reports it is done (idle), or `manual`, when
you press **Start now** on its tile. A member's role prompt is typed into its
terminal as one line (line breaks and tabs become spaces, as in a handoff or a
broadcast; the crew keeps the prompt as you wrote it), with Enter, once the
agent is ready for it: it reports that it waits
for input or is done, or, at least two seconds after the start, its output has
been quiet for a second. After 60 seconds the prompt is typed anyway and the
run's log says so. Every member's process sees `CONDUCTOR_CREW` (the crew's id),
`CONDUCTOR_RUN` (the run's id), `CONDUCTOR_MEMBER` (its name) and `GOAL`, next
to the usual `CONDUCTOR_SESSION_ID` and notify variables.

**Worktrees.** With isolation set to *Git worktree per agent*, the working
directory must be in a git repository that has a commit (otherwise the launch
answers `not_a_repo`), and `git` must be on the server's `PATH` (otherwise it
answers `launch_failed`: git is not installed on the server). Each member gets its own checkout and branch:

```
git worktree add -b crew/<run>/<member> <cwd>/.conductor/worktrees/<run>/<member> HEAD
```

The run's id is the crew's id and eight hex digits. A crew whose `cwd` is
below the top of the repository starts in the same subdirectory of its
worktree. The first worktree adds a `.conductor/` line to the repository's
`.git/info/exclude`, so the worktrees stay out of `git status` without touching
a tracked file. Conductor never deletes a worktree or a branch, not when the
run stops and not when the crew is deleted: remove them yourself with `git
worktree remove` and `git branch -d` once you have merged what you want. With
*Shared working directory* every member works in the same directory.

**The crew view.** `/runs/<id>` shows a live tile for every member, with its
branch and a diff count (`+12 −3`), a feed of the members' events and the run's
own log, and how many members need input. The diff counts the lines of tracked
files the member's worktree adds and removes against the commit it began from,
committed or not; untracked files are not counted, and the numbers refresh at
most every 10 seconds. A member that has not started yet shows a placeholder,
and a pending one offers **Start now**. The header has **Add agent** (a member
joins the run), **Share crew** and **Stop all**; stopping ends every session
and leaves the worktrees.

- **Broadcast.** Tick the tiles of the members you want and type one line into
  all of them at once. A member that waits on a prompt is skipped, so the line
  cannot answer it by accident, as is one that is not running; the toast names
  who got the line and who was skipped, and why. Each line is recorded as an
  input in the member's activity under your display name, or the server's
  user when you have not set one.
- **Share crew** creates a run link with the **View** or **Control** role. It
  grants that role on the session of every member of the run, members added
  later included, and on no other session; the join page lists the members.
  Revoking it disconnects everyone who came in through it. A crew saved with
  **Create a view link (8h)** gets a view-only link, labelled `launch`, when it
  is launched. Its URL is shown once on the crew view and printed by `conductor
  up`; nothing shows it again.

**Handoffs.** A member passes work to another with an event, which the
[Conductor skill](#events-and-hooks) teaches the agent to send:

```bash
"${CONDUCTOR_BIN:-conductor}" notify --event handoff --to tests --message "/v1/users is ready"
```

Conductor types `Handoff from core: /v1/users is ready` into the member named
by `--to`, on one line, as soon as that member is running and not waiting on a
prompt. Up to 10 handoffs wait for a member; past that the oldest is dropped.
A handoff to a name that is not in the run, or to a member that has not
started, is noted in the run's log and
nothing else happens.

**From a shell.**

```bash
conductor crews                        # one line per saved crew: id, name, members
conductor up <crew-id> [--open]        # launch a crew; prints the run, its URL and its view link
```

Both talk to the server at `--server` (env `CONDUCTOR_SERVER`, default
`http://localhost:8080`) with the admin token from `--token` (env
`CONDUCTOR_ADMIN_TOKEN`). `--open` opens the run's page in your browser.
`conductor up` prints the run, the URL of its page and, for a crew set to create
a view link, that link on a third line, `view <url>`.

Limits:

- 50 crews; 12 members a crew, and a run takes no more than that.
- Name 60 characters, goal 2000, role prompt 4000; with the goal in it a prompt
  is at most 32 KiB, the most a session takes in one write.
- 32 extra arguments a member, 8 KiB in all; 512 KiB for a whole crew as saved;
  request bodies of 1 MiB on the crew routes.
- A broadcast line is at most 4096 bytes.
- 10 handoffs waiting for a member; 200 entries in a run's log; 100 links for a
  run.
- Runs live in memory: the server keeps up to 100, and a restart forgets them
  (the worktrees stay). Saved crews survive.

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
| `dataDir` | `CONDUCTOR_DATA_DIR` | `~/.conductor` (an older `conductor.d` next to the config, or in the current directory, is kept while `~/.conductor` holds no server data) | UI-managed state; must be writable, best outside `allowedRoots` |
| `webhooks` | `CONDUCTOR_WEBHOOKS` (a JSON array) | none | where the server POSTs events, see [Webhooks](#webhooks) |

### Upgrading

The server keeps UI-managed state (agents added on the **Agents** page, crews,
the hook files) in a data directory that it must be able to create and write
at startup. Unless `dataDir` or `CONDUCTOR_DATA_DIR` says otherwise, that is
`~/.conductor` in the home of the user running `conductor serve`. Earlier
versions used `conductor.d` next to the config file, or in the current
directory without one. A server that finds that old directory, and no server
data in `~/.conductor` (`catalog.json`, `crews/` or `crews.json`; the `hooks/`
that `conductor host` writes there does not count), keeps using it and logs a
warning naming both paths. To move it, stop the server, move the files in it
into `~/.conductor` (`hooks/` need not move: the server writes it at every
start) and start it again; to keep it, set `dataDir` or `CONDUCTOR_DATA_DIR`
to it. When both `~/.conductor` and the old directory hold server data, the
server uses `~/.conductor` and logs a warning naming the old directory, which
it does not read. An agent override saved on the **Agents** page by an earlier
version holds its env values in full; at start the server stores `***` in
`catalog.json` for every one equal to the config's (and logs the agents it
changed), so that key follows the config from then on and a secret rotated
after the upgrade reaches the agent. A server started without a home directory
(no `HOME`, as some services run) keeps using an old directory too, and needs
`dataDir` or `CONDUCTOR_DATA_DIR` only when there is none. A directory the
server cannot create stops it with `data directory … is not usable`. The Docker
image sets `CONDUCTOR_DATA_DIR=/var/lib/conductor`, declared as a volume. At startup the
server logs the directory it uses, and warns when it overlaps an allowed root:
agents working there can read and commit its secrets.

### Agent catalog

Built-in entries, one for each agent with a hook adapter (see
[Events and hooks](#events-and-hooks)): `claude`, `codex`, `agy`, `copilot`,
`cursor` (runs `cursor-agent`), `opencode`, `pi`, `omp`, `aider`, `goose`,
`amp` and `dsh`, then `shell` (`/bin/bash -l`, which takes no extra
arguments). Add your own or override an entry by ID:

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
`icon` names a Lucide icon (`i-lucide-…`). The workbench carries the icons it
uses and fetches none at runtime: the built-in agents' icons, `i-lucide-wrench`
and `i-lucide-bot` (`web/app/utils/agentIcons.ts`). Any other name shows the
generic agent icon.

**Adding agents from the UI.** Admins can add, change and hide agents from the
**Agents** page (**Add agent**) without editing the config file. The changes are
saved as `catalog.json` in the data directory (`dataDir`) and layered over the
configured catalog at startup: an agent with the ID of a built-in or configured
one replaces it, and deleting that entry brings the original back. Such an
entry inherits what it leaves out: the original's `adapter` and `signal`, and
every `env` value it holds as `***`, which the Agents page stores for a value
the form did not change (or one equal to the original's), so a change to that
value in the config file reaches it. Env keys it does not list are not
inherited: an API client that omits `env` drops every value of the original,
and a key the config file adds later does not reach the entry. An agent in the
config file that replaces a built-in replaces it whole. Hiding an agent removes
it from the launch dialog and lists it under **Hidden** on the Agents page, where **Restore** brings it back as it was; sessions already
running are not affected. The config file is never written. The server
refuses to start when `catalog.json` cannot be parsed or holds an invalid
agent, rather than overwrite it. The form checks the command against the
server's `PATH`, and an agent whose program is missing there can still be
saved, for use with `conductor host`.

Every agent, from the config file or the UI, is held to the same limits: the ID
matches `[a-z0-9-]{1,32}`, the name is at most 60 characters, the description
200, `command` has at most 32 elements of at most 4096 bytes each, `env` has at
most 32 keys, `envPassthrough` (names of server environment variables the agent
may inherit) at most 32 names, an `env` key or `envPassthrough` name is at most
128 bytes and an `env` value at most 16384 bytes, `cwd` is at most 4096 bytes
without NUL, `icon` matches `[a-z0-9][a-z0-9:-]{0,63}`, and a signal `pattern`
(a regular expression) at most 200 bytes that does not match an empty line. An
`adapter`, if an agent names one, matches `[a-z0-9-]{1,32}` and must be one
Conductor has, in the config file (or the catalog file) as on the Agents page:
the server refuses to start with an unknown one.

## Security model and limits

- The admin token gates launching, listing, stopping, link management and
  editing the agent catalog; share tokens grant one role on one session; host
  tokens only allow registering hosted sessions. Tokens are compared in
  constant time and stored hashed. A crew run's link grants its role on every
  member session of that run, members added to the run later included, and
  on no other session.
- Editing the catalog is as powerful as the server user. An admin can add or
  replace any agent, built-in and configured ones included, with any argv and
  env, and it runs as the user running `conductor serve`; `disableDefaults` or
  a curated `catalog` does not limit what the **Agents** page can add. Env
  values saved from the UI, secrets included, are stored in `catalog.json`
  (mode 0600) in the data directory. The file viewer of a server session never
  serves that directory, the config file or the catalog file, nor an editor or
  backup copy beside those two: any file there whose name contains theirs,
  ignoring case (`conductor.json.bak`, `conductor.json~`,
  `.conductor.json.swp`, `#conductor.json#`), but agents run as the same user
  and can read them.
  Changing a built-in or configured agent stores the form's command, cwd, icon,
  adapter and signal in full; only the env values left unchanged follow the
  original, so a secret rotated in the config file reaches the agent at the
  next start, while a value set on the Agents page is stored in `catalog.json`.
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
