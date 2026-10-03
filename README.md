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
headless, or `--stun stun:host:3478` to override the ICE servers.
`--ice-udp-port 7877` puts every WebRTC connection on that one UDP port and
`--ice-public-ip 192.168.1.20` advertises that address as the host's own
(`CONDUCTOR_ICE_UDP_PORT`, `CONDUCTOR_ICE_PUBLIC_IP`): what a forwarder in
front of the host needs, such as the desktop app's on Windows, which carries
the port into WSL. An agent with
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
agent, where it runs, the working directory and the git branch, shows a
**yolo** badge for a session launched with its agent's yolo recipe (see
[Yolo](#yolo)) and the agent's own session id when Conductor knows it (click
to copy); an ended session offers **Resume** or **Relaunch** (see
[Resume and relaunch](#resume-and-relaunch)). A reply typed in a reply bar
goes in as Conductor types a crew prompt: the text, then Enter on its own a
quarter of a second later, so that an agent that reads a fast burst as a paste
still runs it.

## Sharing

On a session page press **Share** and create a link with the **View** (watch
and open files) or **Control** (types into the agent, answers prompts) role,
with a label and an expiry. The link URL is `<base>/join/<token>` and the
token is shown once. Guests type a display name before joining; nothing
connects until they press **Join**, so a fetched link never exposes terminal
content. Revoking a link disconnects everyone using it.

The base is the address you opened the workbench at (`http://192.168.1.20:8080`,
a tunnel's URL, a reverse proxy's host, with the scheme and host the proxy
forwards in `X-Forwarded-Proto` and `X-Forwarded-Host`), unless `publicUrl`
names another machine, in which case it is `publicUrl`; the default and the
example config name `localhost`, which no one else can reach, so they do not
count. Whoever you send the link to must reach the server at that address;
what reaches whom:

| The server runs | Who can open a link |
|---|---|
| on your machine, the link opened there | you |
| on your machine, a teammate on the same network | them, at your machine's address and port (`listen` binds every interface) |
| on your machine behind a home router | no one outside until the router forwards a port to the server and the link carries your public address. With `reach.mode` `auto` (the default) the server learns its public address from STUN and, once it has a TLS listener with a certificate, maps that port on the router and builds links on `https://<public address>`; until then links keep the address you opened the workbench at, and the Share dialog says so. `GET /api/reach` reports the state. A mapping is reported as mapped, never as reachable: the server's own check through the public address is refused by most home routers, so open a link once from a phone on mobile data |
| on your machine under WSL2 | for a plain link, Windows must expose the port (`netsh interface portproxy` plus a firewall rule); through a switchyard, nothing: the desktop app forwards WebRTC into the distribution itself, and WSL's default NAT mode stays as it is |
| on a machine with a public address, or behind a reverse proxy | anyone, at `publicUrl` or the proxy's forwarded host |
| behind carrier-grade NAT or a corporate network | `rendezvous`: this server publishes every session to a public Conductor through the host protocol, where it is listed as hosted by this machine and shared from; or a tunnel or a reverse proxy in front of the server; or `conductor host` from your machine against a server anyone can reach |

The ICE servers play no part in a link. STUN tells a browser or a `conductor
host` its own public address and port so the two can connect the terminal
channel of a hosted session directly over WebRTC once the join page has loaded
from the server; it carries no data and does not make the server's page
reachable, which only a forwarded port, a public address or a proxy does.

### Switchyard

When no one's machine is reachable, a small public Conductor run as a
**switchyard** introduces them: every Conductor publishes its sessions to it
(`rendezvous.server` and `rendezvous.token`, one of its host tokens), viewers
join by links minted there, and the terminal goes over WebRTC between the
viewer and the publishing machine, through the switchyard's relay only for
the pairs ICE cannot connect. The switchyard launches nothing of its own:

```bash
CONDUCTOR_HOST_TOKENS=a-host-token CONDUCTOR_TLS_ACME=1 CONDUCTOR_TLS_ACME_EMAIL=you@example.com conductor switchyard --listen :80
```

`conductor switchyard` is `conductor serve --switchyard`: the sessions,
crews, runs, catalog and integrations routes answer `403 switchyard`, the
Agents and Crews pages say so, and everything about hosted sessions, links
and the events stream works as on any server. `switchyard.relay: false`
(`CONDUCTOR_SWITCHYARD_RELAY=0`) turns the relay off: a viewer whose WebRTC
fails is told `relay_off` and a host that would use the relay alone is
refused. `switchyard.allowedOrigins` (host patterns, `127.0.0.1:*` and
`localhost:*` by default) lets the desktop app's own workbench join from its
loopback page: the join route answers those origins across origins and the
session WebSocket accepts them. A switchyard needs what any public server
does: a public address or a mapped port, and a certificate ([TLS](#tls)).
Its load is signaling, a few kilobytes per connection, plus the relayed
terminals; the smallest VPS carries hundreds.

### TLS

A link that leaves your network should be `https`. The server serves the
workbench over TLS on a second listener (`tls.listen`, `:8443`) with a
certificate from Let's Encrypt through [lego](https://github.com/go-acme/lego):

- **For your public address, with nothing to configure:** `tls.acme` with no
  `domains` (`CONDUCTOR_TLS_ACME=1`). With `reach.mode` `auto` the server asks
  STUN for its public address, maps port 443 on the router to the TLS
  listener, and orders an IP-address certificate for that address (Let's
  Encrypt issues those since January 2026, as six-day certificates under the
  `shortlived` profile, proven with `tls-alpn-01` on 443). It renews after
  four days and orders again when the address changes, which makes old links
  stale: the Share dialog says so. Until the first certificate is issued,
  links keep the address you opened the workbench at and the TLS listener
  refuses every handshake; nothing self-signed is ever served.
- **For a domain:** `tls.acme.domains: ["home.example.net"]`, proven with
  `dns-01` through `cloudflare`, `exec` (a script of your own, `EXEC_PATH`)
  or `httpreq`, with `http-01` on a mapped port 80 (`reach.publicPort80`), or
  with `tls-alpn-01` on the mapped 443. Keeping the name pointed at your
  address (dynamic DNS) is yours to arrange.
- **With your own certificate:** `tls.certFile` and `tls.keyFile`, re-read
  when they change.

The plain listener stays as it is for the desktop window, local hosts and
`http-01`; there is no redirect to TLS. `GET /api/reach` reports the
certificate (`tls`: identifiers, `notAfter`, `renewAt`, `ready`, the last
error); the account key and the certificates live under `dataDir/tls`, mode
`0600`. The certificate flow is tested against Let's Encrypt's Pebble in CI
(`make test-pebble`).

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
Each tile is the session's terminal at the tile's size, filling it: the last
viewer that attaches or resizes sets a session's size, so opening the wall
sizes each session to its tile, opening a session's page sizes it to that
page, and coming back to the grid sizes it to its tile again. Click into a
tile and type: the keys go to that session, the plain-key shortcuts pause
while the tile has focus and the **Alt** chords still work. The chips in the
header filter tiles (**All**, **Needs you**, **Running**). A queue on the left
lists every session waiting for input with its prompt: answer from there
(**J**/**K** select, **Enter** types a reply) without opening the session,
and see who answered what under **Answered**. The expand button in a tile's
header, a double-click on the header, or **Enter** on a tile whose frame has
keyboard focus expands it in place to a full-size terminal (`/wall?focus=<id>`,
so the view is linkable); **Esc**, the back arrow or the browser's Back button
return to the grid, and **Open page** goes to the full session page. The
fullscreen button turns a spare monitor into a status wall.

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

The sidebar collapses to an icon rail with the panel button in its header or
**Ctrl+B** (**⌘B** on a Mac). The rail keeps everything: the mark, a Launch
button, a search button that opens the full sidebar on its filter, the pages
and the sidebar's buttons as icons with tooltips (the Wall's count as an amber
chip), and every session as its agent's initials with the amber dot when it
needs you, the members of a running crew together under its name, which links
to the crew view. The full sidebar groups them the same way: inside each
section (**Needs you**, **Running**, **Exited**) the sessions of no crew come
first, then one group per crew run under a header naming the run, which links
to its crew view. A session launched with yolo carries the **yolo** badge
there, and an ended one a **Resume** (or **Relaunch**) button. Click one to
open it. The panel button at the bottom of the
rail brings the full sidebar back, as do **Ctrl+B** and, on a desktop-width
window, **/**, which then focuses the filter. The mode and the full sidebar's
width are remembered per browser (localStorage keys `conductor.sidebar.mode`
and `conductor.sidebar.size`; a hidden sidebar from an earlier version becomes
the rail). On a phone the sidebar is a drawer. **F** toggles fullscreen on
every page (**Alt+F** in a terminal); every page header has the button (where
the browser can do it), and the key is ignored while you type in a field or
have a select focused. Press **?** (or use **Shortcuts** in the sidebar) for
the list of shortcuts on the current screen.
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
`~/.local/state/conductor/hooks` is kept while it exists). `conductor serve`
warns and goes on when it cannot set the modes of the files in a `hooks/` of
its own (0600 files in a 0700 directory); a `hooks/` that belongs to another
user stops it before anything is written there, root included, since those
files choose the commands agents run.

**At rest is done, not a question.** An agent that finished its turn and waits
for its next line reports `done`, whatever its hooks call it: Codex's
`agent-turn-complete` and Claude Code's `idle_prompt` notification (sent after
a minute at rest) both do, as Claude Code's `Stop` does. Needs input is kept
for a real question: a permission request, a dialog, the bell. A crew relies
on this: a handoff or a broadcast is typed only into a member that is not
waiting on a question, and an idle member is not.

**Installed on demand.** The other agents read hooks only from their own
config, and so do the agents above when something other than Conductor starts
them. The **Events** page lists every adapter with what it reports and whether
its hooks are installed, the snippet to paste, and **Install on this machine**,
which adds Conductor's hooks to the agent's config in the server user's home,
next to your own settings and hooks and never over them: entries in its hook
lists, a marked block, or a file of Conductor's own. An entry of Conductor's
that names an older binary is rewritten where it is, but only when its program
is named `conductor`: a `conductor-dev` or `conductor.exe` entry is left as it
is. The same from a shell, for whoever runs it, a host included:

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
`--state clear` resets it. A question with a few answers takes them as
`--choices "Postgres|SQLite|Keep both"` (at most 6, each at most 40 bytes):
the session tile, the wall queue and the run page show them as buttons, and
a click types the choice into the session as a line, so an agent names the
choices in its terminal first and gets the same words back.

**The Conductor skill.** `conductor skill` prints a `SKILL.md` that teaches an
agent to report on its own: progress (`--event progress --message "4/7
handlers"`), an artifact such as a pull request (`--event artifact --url …`),
a handoff to another crew member (`--event handoff --to …`), a decision it
cannot make (`--state needs_input`) and a question with a few answers
(`--choices "a|b|c"`, answered with one click); and to form a crew around its own
session when the work splits (`conductor crew`, see
[Agents that form crews](#crews)). Every agent that reads skills gets it
without asking: the first time an agent is launched after the server starts,
the skill is put in that agent's skills directory in the server user's home
(`~/.claude/skills/conductor/` for Claude Code, `~/.codex/skills/conductor/`
for Codex, `~/.gemini/antigravity-cli/skills/conductor/` for the Antigravity
CLI, and `~/.agents/skills/conductor/`, the directory of the Agent Skills
convention, for pi, Goose, Cursor, Copilot, OpenCode, oh-my-pi, Amp and
DeepSeek Harness; aider reads none), one attempt per agent per server start,
and a `SKILL.md` of your own there is never touched (the log says so once).
`agents.installSkill: false` or `CONDUCTOR_AGENT_INSTALL_SKILL=0` turns that
off; installing the hooks (`conductor hooks install`, the Events page) puts
the skill in place too. The server keeps its copy in
`hooks/skills/conductor/SKILL.md`, and every session names it in
`CONDUCTOR_SKILL`, so an agent with no skills directory can be told to read
`$CONDUCTOR_SKILL` (a crew's member prompt can say so). Its commands run
`"${CONDUCTOR_BIN:-conductor}"`: every session carries `CONDUCTOR_BIN`, the
absolute path of the binary its hooks run, and `CONDUCTOR_AGENT`, the id of
its agent in the catalog, while the manual snippets below assume `conductor`
on the `PATH`.

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
as Go's HTTP client does. The Events page shows each webhook by its host.
`GET /api/integrations` returns its URL without its user info, query string and
fragment, and never its secret, but with its path: for a service that puts its
credential in the path (Slack, Discord), an admin reading that route sees it.
Logs name a webhook by its place in the list and its host.

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
typed. Crews are saved one file each, `crews/<id>.json` in the data directory
(a `crews.json` from an earlier version is split up at the first start), and run
on the server only for now (members are ordinary server sessions, under
`allowedRoots`); the **My machine** option is disabled.

The working-directory field completes as you type: the server lists the
directories under its allowed roots, at most 50 at a time (hidden ones once
you type the dot), and marks git repositories: "git" for one with a commit,
which a crew with worktrees needs, "git, no commit" for one without. Enter
keeps what you typed; to take an entry, move to it with the arrow keys or the
pointer and press Enter, or click it. A path outside the roots shows which
roots there are. The editor says under the field whether
the crew could launch there with worktrees; the launch itself still decides
(`409 not_a_repo`). The Launch dialog's working directory completes the same
way. A crew whose member's agent is not installed on the server is refused at
launch (`invalid_crew`, naming the member).

**Example crews.** `conductor serve --examples` (or `CONDUCTOR_EXAMPLES` set to
`1` or `true`) adds four example crews the first time: `example-todo-app` (a
lead that plans, two builders after it, a tester after the second builder),
`example-test-fixer`, `example-docs-writer` and `example-dependency-upgrade`,
each using Claude Code and Codex, the server's default working directory and a
worktree per member. They are ordinary crews once saved: edit or delete them
freely. A crew whose id has a file is never touched, so edits survive the
flag, and a deleted example comes back on the next `--examples`. The empty
**Crews** page offers **Load the examples**, which does the same
(`POST /api/crews/examples`). A worktree starts from the checkout's `HEAD`, so
what an earlier member wrote is on its branch only: a member that starts after
another begins by merging that member's branch (`crew/$CONDUCTOR_RUN/<member>`,
the run's id being in its environment), and the todo app's tester merges the
lead's and the second builder's, then waits for the first builder's and merges
that too. The todo app writes files in its working directory, so point it at a
fresh repository with one commit, not at a real project.

A member starts `immediately` at launch, `after <member>` once that member,
with its own prompt typed, first reports it is done (idle), or `manual`, when
you press **Start now** on its tile. A member's role prompt is typed into its
terminal as one line (line breaks and tabs become spaces, as in a handoff or a
broadcast; the crew keeps the prompt as you wrote it) once the agent is ready
for it: it reports that it waits for input or is done, or, at least two
seconds after the start, its output has been quiet for a second. An agent that
only animates a spinner in its window title counts as quiet, and one between
two screens of its start (Claude Code turns bracketed paste off while it
loads) is waited for. Conductor types the prompt as a terminal pastes text:
the text, as a bracketed paste when the agent asks for one, then Enter on its
own 250 ms later, so that Codex (which reads a fast burst of keys with Enter
in it as a paste) and Claude Code (which collapses a long one) both run it;
handoffs, broadcasts and the reply bars go in the same way. Claude Code reports
taking a prompt: when it has not within 3 seconds, Enter is pressed once more
and the run's log says so. A question that comes up in the 250 ms before the
Enter keeps the Enter back: the text waits in the agent's input, nothing is
typed twice, and the run's log tells you to press Enter once you have answered. For Claude Code
and Codex, whose trust question Conductor watches for on the screen, the pause
lasts until the screen has been quiet long enough for the watcher to have looked
at what was drawn since the text (at most two seconds more), so a trust
question drawn in that moment holds the Enter back too.
After 60 seconds the prompt is typed anyway and the run's log says so, except
while a trust question shows (see below). Every member's process sees
`CONDUCTOR_CREW` (the crew's id), `CONDUCTOR_RUN` (the run's id),
`CONDUCTOR_MEMBER` (its name) and `GOAL`, next to the usual
`CONDUCTOR_SESSION_ID` and notify variables.

**Trust the repository once.** Claude Code and Codex ask whether to trust a
folder the first time they start in it, yolo or not, and a worktree of a
repository they trust asks nothing. Before a crew's first run in a repository,
including the fresh one the todo-app example wants, open `claude` and `codex`
there once and accept. A member that meets the question anyway is held, never
typed into: its tile and the sidebar show that it needs you, with the
question's words, the run's log says so once, and its prompt is typed after
you answer it in its terminal with Enter (an arrow key that moves the
selection does not count as an answer). With yolo on, Codex is trusted for that
launch alone, through `-c projects={…}` naming the repository, and nothing is
written to `~/.codex/config.toml`; Claude Code has no such option, so trust it
once yourself.

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
own log, and how many members need input. A tile is the member's terminal at
the tile's size, as on the wall: click into it and type; its expand button, or
a double-click on its header, opens the session's page. The diff counts the
lines of tracked files the member's worktree adds and removes against the
commit it began from, committed or not; untracked files are not counted, and
the numbers are read again when the run changes, at most every 10 seconds. The
page follows the run through the event stream and polls nothing. A member that
has not started yet shows a placeholder, a pending one offers **Start now**,
and an ended one **Resume** (see [Resume and relaunch](#resume-and-relaunch)).
The header has **Add agent** (a member joins the run), **Share crew** and
**Stop all**; stopping ends every session and leaves the worktrees.

- **Broadcast.** Every member with a session is ticked, and one that starts
  later is ticked when its tile appears; untick the ones to leave out (the
  ticks stay when the page reads the run again). Type one line and it goes into
  all of them at once. The button counts the members the line will reach and
  says apart how many it will skip because they wait on a prompt: such a member
  is skipped, so the line cannot answer it by accident, as is one that is not
  running, and one where a question comes up in the 250 ms before its Enter
  keeps the line in its input without Enter. The toast names who got the line
  and who was skipped, and why. Each line is recorded as an input in the
  member's activity under your display name, or the server's user when you
  have not set one.
- **Share crew** creates a run link with the **View** or **Control** role. It
  grants that role on the session of every member of the run, members added
  later included, and on no other session; the join page lists the members.
  With a **Control** link their tiles take keys as the crew view's do; with a
  **View** link each tile shows the member's whole screen scaled to fit, and
  never resizes the session.
  Revoking it disconnects everyone who came in through it. A crew saved with
  **Create a view link (8h)** gets a view-only link, labelled `launch`, when it
  is launched. Its URL is shown once on the crew view and printed by `conductor
  up`; nothing shows it again.

**Runs on the Crews page.** Each crew in the list shows its runs, newest
first: the run's name and age, its state (**Running**; **Needs input** with
how many members wait; **Stopped** after a stop; **Finished** once every member
has ended and none is pending; a member that reports it is done keeps its run
running, for a done agent is idle, not gone), a member's error beside it, the
members as their agents' avatars with a dot for each one's status (pending,
starting, running, needs input, ended), **Open** for the crew view and **Stop**
while it runs; five at first, **Show more** for the rest. The crew's badge
reads **Running** while one of its runs is running or needs input
(**Draft changes** wins while you edit). The page follows the runs through the
event stream, as the crew view and the sidebar do. A crew's **Yolo** setting
(the server's default, on or off; see [Yolo](#yolo)) is fixed on the run when
it is launched, so every member, one started or added later included, follows
it.

**The graph.** The crew view has a **Graph** tab beside the tile grid: one
node per member (agent, name, status dot, a needs-input badge, branch and
diff, Start now or Resume where they apply), solid edges for "starts after X
is idle" and dashed, moving edges for the handoffs delivered in this run,
with a count and the last message on hover. Roots sit on the left (or on top:
the direction and the handoff switch are remembered by the browser), a click
selects, a double click or Enter opens the member's session. The crew editor
has the same graph as a second mode of the members section: drag from a
member's right handle to another's left and that one starts after it; the ×
on an edge removes the rule (the member starts immediately); a node's menu
sets a rule by hand or removes the member. A member waits for at most one
other, so a second parent, a self edge and a cycle are refused with a word
on why. On a phone the graph is a list indented by depth with the same
badges, and the Crews list shows each crew's shape in miniature.

**The timeline and the charts.** A **Timeline** tab beside the graph draws
one bar per member from its start to its end or now, amber where its session
waited for input (the feed's attention entries), a mark with a line to the
other member's row for each handoff, and the run's stopped line; a hover
reads the exact times. Runs that end (stopped or finished) are recorded as
`runs/<id>.json` in the data directory (the run as the API answers it, never
a terminal's contents; at most 500 kept) and read back after a restart, so
the Crews list charts each crew's last twenty runs as bars (minutes,
coloured by outcome, with how many members needed input), once there are two.
The Events page charts the last hour's activity per minute (attention
changes, reports, handoffs, tool calls, errors) once two events are in it,
and the Wall's header shows its sessions by attention state as a small
donut from two sessions on. Nothing is charted with fewer than two points.

**Handoffs.** A member passes work to another with an event, which the
[Conductor skill](#events-and-hooks) teaches the agent to send:

```bash
"${CONDUCTOR_BIN:-conductor}" notify --event handoff --to tests --message "/v1/users is ready"
```

Conductor types `Handoff from core: /v1/users is ready` into the member named
by `--to`, on one line and as a prompt is typed (the text, then Enter on its
own), as soon as that member is running and not waiting on a prompt; a
question that comes up before the Enter keeps the line in its input without
Enter, noted in the run's log. Up to 10 handoffs wait for a member; past that
the oldest is dropped.
A handoff to a name that is not in the run, or to a member that has not
started, is noted in the run's log and
nothing else happens.

**From a shell.**

```bash
conductor crews                        # one line per saved crew: id, name, members
conductor crews --ids                  # the ids only, one per line (for shell completion)
conductor up <crew-id> [--open]        # launch a crew; prints the run, its URL and its view link
```

Both talk to the server at `--server` (env `CONDUCTOR_SERVER`, default
`http://localhost:8080`) with the admin token from `--token` (env
`CONDUCTOR_ADMIN_TOKEN`). `--open` opens the run's page in your browser.
`conductor up` prints the run, the URL of its page and, for a crew set to create
a view link, that link on a third line, `view <url>`.

**Agents that form crews.** An agent running in a Conductor session can form a
crew around its own session, from inside it, with no admin token: the session's
own token (the one `conductor notify` uses) is accepted on four routes scoped to
that session, on by default (`agents.selfService: false` or
`CONDUCTOR_AGENT_SELF_SERVICE=0` turns them off). The skill teaches the agent
to write a `crew.json` and run:

```bash
"${CONDUCTOR_BIN:-conductor}" crew create crew.json --self lead --open   # the crew, launched, with this session as "lead"
"${CONDUCTOR_BIN:-conductor}" crew add member.json                       # one more member of this session's run
"${CONDUCTOR_BIN:-conductor}" crew status                                # the run and its members
"${CONDUCTOR_BIN:-conductor}" crew link --ttl 2h --label "for the PR"    # a view-only link to this session
```

`crew.json` is the crew as the API takes it (`name`, `goal`, `members` with
their `start` rules, `self` naming the member this session becomes); `-` reads
stdin. The crew's working directory and yolo setting are the session's own and
cannot be chosen, the session is adopted as the member named by `--self`
(running, with its prompt counted as typed, so members that start "after" it
wait for its next done, and handoffs reach it), and the other members start
as at a launch. `--open` makes the workbench offer the run: a toast on every
page with **Open**; nothing navigates on its own. Links are view-only and last a
day at most. Bounds: 2 crews per session per hour, 5 links per session per day,
the crew limits below; a hosted session cannot form a crew (its directory is not
on the server). Outside a session the commands exit 0 silently
(`--quiet=false` says why), as `conductor notify` does.

Limits:

- No limit on the number of crews (the Crews page and `conductor crews` page
  through them); 12 members a crew, and a run takes no more than that.
- Name 60 characters, goal 2000, role prompt 4000; with the goal in it a prompt
  is at most 32756 bytes, what one write carries with the paste markers around
  it.
- 32 extra arguments a member, 8 KiB in all; 1 MiB for a whole crew as its file;
  request bodies of 2 MiB on the crew routes.
- A broadcast line is at most 4096 bytes.
- 10 handoffs waiting for a member; 200 entries in a run's log; 100 links for a
  run.
- Runs live in memory: the server keeps up to 100, and a restart forgets them
  (the worktrees stay); the records of runs that ended survive, 500 at most.
  Saved crews survive.

## Yolo

With yolo on (`"yolo": true` in the config, `CONDUCTOR_YOLO` set to `1` or
`true`, or `conductor serve --yolo`), every agent the server launches skips its
permission prompts, and the server says so in a warning at startup.
`CONDUCTOR_YOLO` set to `0` or `false` turns the config file's `yolo` off. A
launch overrides the server: the Launch dialog has a **Yolo** switch, and
`POST /api/sessions` takes `yolo: true` or `false`; so does a crew (the
editor's **Yolo** select: the server's default, on or off). Each built-in
carries a yolo recipe, arguments put after its command and your extra
arguments, and variables set in its environment through the same filtered
environment as its own `env` (a recipe cannot set `CONDUCTOR_*`):

| Agent | Recipe | What it turns off |
|---|---|---|
| Claude Code | `--dangerously-skip-permissions`; the warning Claude Code shows before its first such launch is skipped through the hooks settings file | every permission prompt; protected paths such as `.git` and `.claude` become writable; deny rules still apply |
| Codex CLI | `--dangerously-bypass-approvals-and-sandbox`, and the repository trusted for this launch | every approval **and the sandbox**: the agent reaches the network and the whole filesystem as the server's user |
| Antigravity | `--dangerously-skip-permissions` | every tool permission request |
| Copilot CLI | `--yolo`, `COPILOT_ALLOW_ALL=true` | every tool, path and URL prompt, and its folder-trust question |
| Cursor CLI | `--yolo --trust` | command and MCP approvals, and its workspace-trust question |
| OpenCode | `--auto` | every permission that is not explicitly denied |
| oh-my-pi | `--yolo` | approvals, unless a deny policy matches (already its default) |
| aider | `--yes-always` | every confirmation, shell commands included |
| Goose | `GOOSE_MODE=auto` | tool approvals (already its default) |
| Amp | `--dangerously-allow-all` | every tool approval |
| DeepSeek Harness | `DSH_PERMISSION_MODE=danger-full-access` | its sandbox and every approval |

pi has no permission system to skip and Shell has nothing to ask, so they have
no recipe. The recipes of Claude Code, Codex and Copilot were checked against
the real CLIs; the others come from their documentation (the adapter matrix in
[docs/features.md](docs/features.md) says which). A session launched with its
agent's recipe shows a **yolo** badge in its header, its tile and the sidebar:
the badge means Conductor applied the recipe, not that no question can appear
(a trust question still can; see Crews). An agent without a recipe is launched
as it would be without yolo, with a note in its activity (and, for a crew
member, in the run's log) and no badge. Edit a recipe on the Agents page
(**Yolo recipe**) or in the catalog (`"yolo": {"args": [...], "env": {...}}`):
a saved agent that replaces a built-in and leaves `yolo` out keeps the
built-in's, and `"yolo": {}` gives it none. Sessions started with
`conductor host` take no yolo in this version.

## Resume and relaunch

An ended session (exited or stopped) offers **Resume** in its header, on its
tile on the crew view (the wall's grid shows only running sessions, so on the
wall it is in the expanded view's header), on its row in the sidebar's
**Exited** section, and on an ended member's tile on the crew view. Resume
starts a new session with the same agent, name, working directory, arguments
and yolo choice and, for a crew member, the same run, branch and worktree,
launched with the agent's resume arguments for its own session id:

| Agent | Its session id | Resumed with |
|---|---|---|
| Claude Code | chosen by Conductor at launch: `--session-id <uuid>` | `--resume <id>` |
| Codex CLI | reported by its notify payload (`thread-id`) | `codex resume <id>` |
| Copilot CLI | chosen by Conductor at launch: `--session-id <uuid>` | `--session-id <id>` |
| Antigravity | reported by its hooks (`conversationId`), once they are installed | `--conversation <id>` |
| Cursor CLI | reported by its hooks (`conversation_id`), once they are installed | `--resume <id>` |
| pi | chosen by Conductor at launch: `--session-id <uuid>` | `--session-id <id>` |
| Goose | chosen by Conductor at launch: `goose session --name cdr-<uuid>` | `goose session --resume --name <id>` |

The agent's conversation comes back; its permission mode and model come from
the new launch, which applies the yolo recipe again when the session had it.
An agent without a recipe (OpenCode, oh-my-pi, aider, Amp, DeepSeek Harness,
Shell), or one that has had no turn yet (Claude Code keeps nothing to resume
until it has been prompted), offers **Relaunch** instead: a fresh session, and
the toast says it started anew; a crew member that is relaunched gets its role
prompt again, while a resumed one gets none, since its conversation has it. The
original arguments are passed again, so a prompt given as an argument
(`claude "fix the tests"`) is sent again as well. One running session holds a
conversation: resuming it a second time while that one runs is refused. Resume
is there while the ended session is listed (`exitedRetention`, 10 minutes by
default) and, for a crew member, while the server keeps the run; a member of a
stopped run, and a hosted session, cannot be resumed in this version.

A stopped run can go on in two ways. **Resume** on an ended member of a run
whose stop completed resumes that member in place and reopens the run: the
run is running again, the other ended members stay ended until you resume
them one by one, and **Stop all** stops it again. **Resume run** (the crew
view's header, or the run's row on the Crews page) starts a new run of the
crew in which every member whose conversation is resumable continues it in
its kept worktree and branch, without a new prompt (its next done starts the
members after it), while the others start afresh under their start rules;
the new run names the old one it resumed, and the old one names it.

## Desktop app

Conductor also runs as a desktop app (macOS, Linux as deb, rpm and
AppImage, Windows through WSL 2): an Electron shell that starts the server
on a free loopback port with an admin token minted for the run, opens the
workbench in a window signed in with it, keeps the server in the tray when
the window closes, and stops it when the app quits. Its **Settings** page
holds what the server starts with (the data directory, the allowed roots,
the default directory, yolo, reach) and restarts the server when those
change; **Open in browser** opens the same server in your browser, signed
in. On Windows the server runs inside your WSL 2 distribution (the app sets
it up on first run; the agents are the ones installed there). `make
desktop-dev` runs it from a checkout; `desktop/README.md` has the rest.

**Windows.** There is no Windows build of the server: the installer bundles
the Linux binary and the app runs it inside your WSL 2 distribution
(`wsl --install` once, restart, open the distribution to make your user).
The agents must be installed inside the distribution: the app finds them on
its PATH, through your login shell, not on Windows's (`claude`, `codex`,
`npm` from Windows under `/mnt/c` are flagged, not used). Projects under
`/mnt/c` work but are slow; keep repositories in the distribution's home.
WSL's default NAT mode stays as it is (the app never asks for mirrored
networking, which changes WSL for Docker and every other tool): for WebRTC
the app forwards one UDP port into the distribution itself and the server
advertises the Windows address, so sharing through a switchyard works from
WSL with nothing to configure; the installer adds the firewall rule for the
port, and the Settings page offers it when it is missing. A plain share link
to the local server still needs Windows to expose the HTTP port. Closing the
window keeps the server running in the tray; quitting the app stops it
with `kill` inside the distribution, never `wsl --terminate`, so your
other WSL shells are left alone.

## Shell completion

`conductor completion zsh` or `conductor completion bash` prints a completion
script: subcommands, flags (`conductor serve`'s `--examples` and `--yolo`
among them) and their values, and for `conductor up` the crew
ids, read from the server as you type through `conductor crews --ids` (which
prints ids only, and nothing, with exit 0, when there is no admin token in
`CONDUCTOR_ADMIN_TOKEN` or the server in `CONDUCTOR_SERVER` does not answer
within two seconds). The ids are only ever offered as words: nothing the server
answers is run by your shell, and an id not shaped like a crew's is dropped on
both sides. Load it with `source <(conductor completion zsh)` in `~/.zshrc`
(after `compinit`), or let `conductor completion install` append a line that
does so, marked `# conductor completion`, to `~/.zshrc` or `~/.bashrc` (the
shell from `$SHELL`, or `--shell`; the file from `--rc`); run again it changes
nothing, since the installed line is matched as a whole line (spaces around it
aside; a comment of yours that contains the mark does not count), and it
refuses a file, a link or a directory that is not yours, naming it, or a link
of yours that leads to someone else's file. After a zsh install it says the rc
file must run `compinit` before that line.

## Configuration

`conductor serve --config conductor.json` reads a JSON file; every field has a
`CONDUCTOR_*` environment override. See [`conductor.example.json`](conductor.example.json).

| Key | Env | Default | Purpose |
|---|---|---|---|
| `listen` | `CONDUCTOR_LISTEN` | `:8080` | bind address |
| `publicUrl` | `CONDUCTOR_PUBLIC_URL` | `http://localhost:8080` | base for share links and the agents' notify URL; while it names localhost, a share link takes the address its request came through instead (see Sharing) |
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
| `dataDir` | `CONDUCTOR_DATA_DIR` | `~/.conductor` (an older `conductor.d` next to the config, or in the current directory, is kept while `~/.conductor` holds no server data, when it is a real directory owned by the server's user) | UI-managed state; must be writable, best outside `allowedRoots` |
| `webhooks` | `CONDUCTOR_WEBHOOKS` (a JSON array) | none | where the server POSTs events, see [Webhooks](#webhooks) |
| — | `conductor serve --print-listen`, `--exit-on-stdin-close` | off | for a parent process (the desktop app): once listening, print one JSON line to stdout, `{listen, publicUrl, pid, version, adminToken?, tlsListen?}` (the token only when generated for this run); and shut down when stdin closes, so a parent that dies takes the server with it. A `publicUrl` that names this machine follows the port the listener got (`--listen 127.0.0.1:0`) |
| — | `CONDUCTOR_EXAMPLES` | off | `1` or `true` seeds the example crews once at startup, as `conductor serve --examples` does; not a config-file key |
| `switchyard.enabled` | `CONDUCTOR_SWITCHYARD` (`1`/`true`), or `conductor switchyard` | `false` | coordinate hosted sessions and launch nothing: the launching routes answer `403 switchyard` (see [Switchyard](#switchyard)) |
| `switchyard.relay` | `CONDUCTOR_SWITCHYARD_RELAY` (`1`/`true` on, `0`/`false` off) | `true` | on a switchyard, relay the terminal for viewers whose WebRTC fails; off, they get `relay_off` and relay-only hosts are refused |
| `switchyard.allowedOrigins` | `CONDUCTOR_SWITCHYARD_ORIGINS` (comma-separated host patterns) | `127.0.0.1:*`, `localhost:*` | the browser origins a switchyard answers across origins on the join route and accepts on a hosted session's WebSocket: the desktop app's own workbench |
| `agents.selfService` | `CONDUCTOR_AGENT_SELF_SERVICE` (`1`/`true` on, `0`/`false` off) | `true` | an agent may form a crew around its own session, add members to its run, read its run and mint a view-only link to itself, with its session's own token (see [Agents that form crews](#crews)) |
| `agents.installSkill` | `CONDUCTOR_AGENT_INSTALL_SKILL` (`1`/`true` on, `0`/`false` off) | `true` | a launch puts the Conductor skill in the agent's skills directory, in the server user's home, once per agent per server start (see [The Conductor skill](#events-and-hooks)) |
| `yolo` | `CONDUCTOR_YOLO` (`1`/`true` on, `0`/`false` off) | `false` | launch every agent with its yolo recipe, skipping its permission prompts, unless a launch or a crew says otherwise; `conductor serve --yolo` turns it on (see [Yolo](#yolo)) |
| `reach.mode` | `CONDUCTOR_REACH` | `auto` | how the server finds out it can be reached from outside its network: `auto` asks a STUN server for the public address and, once there is a TLS listener, maps its port on the router (UPnP IGD, PCP or NAT-PMP, renewed and deleted on shutdown; plain http is never mapped); `manual` asks for the address only, for a port you forwarded yourself; `off` asks nothing. `GET /api/reach` reports the result; the Share dialog says what a link reaches |
| `reach.publicPort` | `CONDUCTOR_REACH_PUBLIC_PORT` | `443` | the port on the public address for the TLS listener: mapped in `auto`, forwarded by you in `manual` |
| `reach.publicPort80` | `CONDUCTOR_REACH_PUBLIC_PORT_80` | `false` | also map port 80 to the plain listener, for an ACME `http-01` challenge |
| `reach.stunServer` | `CONDUCTOR_REACH_STUN` | first `stun:` of `iceServers` | the STUN server that answers the public address |
| `tls.listen` | `CONDUCTOR_TLS_LISTEN` | `:8443` once a certificate source is set | the TLS listener, serving the same workbench; `reach.publicPort` (443) is mapped to it. HTTP/1.1 only; no redirect from the plain listener |
| `tls.acme` | `CONDUCTOR_TLS_ACME=1` and the keys below | off | a certificate from Let's Encrypt (or `caDirectory`) through ACME, kept under `dataDir/tls`, renewed on its own (at two thirds of a short certificate's life, thirty days before a long one's end, or when the CA suggests), see [TLS](#tls) |
| `tls.acme.email` | `CONDUCTOR_TLS_ACME_EMAIL` | none | the account's contact |
| `tls.acme.domains` | `CONDUCTOR_TLS_ACME_DOMAINS` (comma-separated) | none: the public address | DNS names or IP addresses the certificate is for; empty orders an IP-address certificate for the address reach finds, once its port is mapped |
| `tls.acme.challenge` | `CONDUCTOR_TLS_ACME_CHALLENGE` | `tls-alpn-01` | `tls-alpn-01` (answered on the TLS listener through the mapped 443), `http-01` (on the plain listener through a mapped 80: `reach.publicPort80`) or `dns-01` (names only, through `dnsProvider`) |
| `tls.acme.dnsProvider`, `tls.acme.dnsEnv` | `CONDUCTOR_TLS_ACME_DNS_PROVIDER`, `CONDUCTOR_TLS_ACME_DNS_ENV` (`K=V,K=V`) | none | `cloudflare`, `exec` or `httpreq`, with its settings under lego's names (`CLOUDFLARE_DNS_API_TOKEN`, `EXEC_PATH`, `HTTPREQ_ENDPOINT`, …); never logged or shown |
| `tls.acme.caDirectory`, `tls.acme.profile` | `CONDUCTOR_TLS_ACME_CA`, `CONDUCTOR_TLS_ACME_PROFILE` | Let's Encrypt; `shortlived` for IP addresses | the ACME directory (`https`), and the certificate profile |
| `rendezvous.server`, `rendezvous.token` | `CONDUCTOR_RENDEZVOUS_SERVER`, `CONDUCTOR_RENDEZVOUS_TOKEN` | none | a public Conductor every session is published to, through the host protocol (as `conductor host` does), with one of its host tokens; the session is listed there as hosted by this server (`rendezvous.hostName`, the machine's name by default), its viewers served over WebRTC or the relay (`rendezvous.relayOnly`), and share links minted there; the local session's activity records where it is published |
| `ice.udpPort`, `ice.publicIp` | `CONDUCTOR_ICE_UDP_PORT`, `CONDUCTOR_ICE_PUBLIC_IP` | none | how this server's published sessions (`rendezvous`) gather their WebRTC candidates: one UDP port for every connection, and the address advertised as this machine's in place of its interfaces' own, for a forwarder in front of it (the desktop app on Windows, in front of WSL) |
| `tls.certFile`, `tls.keyFile` | `CONDUCTOR_TLS_CERT_FILE`, `CONDUCTOR_TLS_KEY_FILE` | none | a certificate of your own (PEM, with its chain), re-read when the files change; exclusive with `tls.acme` |

### Upgrading

The server keeps UI-managed state (agents added on the **Agents** page, crews,
the hook files) in a data directory that it must be able to create and write
at startup. Unless `dataDir` or `CONDUCTOR_DATA_DIR` says otherwise, that is
`~/.conductor` in the home of the user running `conductor serve`. Earlier
versions used `conductor.d` next to the config file, or in the current
directory without one. A server that finds that old directory, and no server
data in `~/.conductor` (`catalog.json`, `crews/` or `crews.json`; the `hooks/`
that `conductor host` writes there does not count), keeps using it and logs a
warning naming both paths. It keeps it only when it is a real directory, not
a symbolic link, owned by the user running the server: its `catalog.json`
chooses the commands agents run, and anyone may make a `conductor.d` in a
shared directory. A symbolic link or another user's directory under that
name, when it would be kept, stops the server with an error naming it, its
owner and the two settings; set `dataDir` or `CONDUCTOR_DATA_DIR` instead.
To move it, stop the server, move the files in it
into `~/.conductor` (`hooks/` need not move: the server writes it at every
start) and start it again; to keep it, set `dataDir` or `CONDUCTOR_DATA_DIR`
to it. When both `~/.conductor` and the old directory hold server data, the
server uses `~/.conductor` and logs a warning naming the old directory, which
it does not read. Crews an earlier version kept in one `crews.json` move at
the first start to a file each in `crews/<id>.json`, and `crews.json` is
renamed `crews.json.migrated`, which keeps every crew as it was. An agent
override saved on the **Agents** page by an earlier version holds its env
values in full; at start the server stores `***` in
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

**Identity.** The catalog lists each agent as available when a program of
its command's name is on the server's `PATH`; a name proves little (`goose`
is also a Go migrations tool). For an agent with a hook adapter the server
also runs the program with its version flag, argv only in a spartan
environment, and matches the answer against what the agent prints: the
**Agents** page then shows "Claude Code 2.1.287" or "Codex CLI 0.159.0",
names an impostor by what it printed ("Not Goose: `goose version: v3.22.1`"),
and a crew with one is refused at launch; a plain launch goes ahead with the
note in the session's activity. Claude Code's and Codex's version lines were
verified live; the other adapters' come from their documentation and refuse
nothing until the nightly recipes job confirms them. `probe: false` on an
agent turns the check off; `POST /api/catalog/check` runs it for an adapter.
`GET /api/catalog` carries the result as `identity`.

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
entry inherits what it leaves out: the original's `adapter`, `signal`, `site`,
`yolo` recipe, `trustPrompt` and `session` recipe (an explicit `"yolo": {}` or
`"session": {}` says it has none), and every `env` value it holds as `***`,
which the Agents page stores
for a value the form did not change (or one equal to the original's), so a
change to that value in the config file reaches it. Env keys it does not list
are not inherited: an API client that omits `env` drops every value of the
original, and a key the config file adds later does not reach the entry. An
agent in the config file that replaces a built-in replaces it whole. Hiding an
agent removes it from the launch dialog and lists it under **Hidden** on the
Agents page, where **Restore** brings it back as it was; sessions already
running are not affected. The config file is never written. The server
refuses to start when `catalog.json` cannot be parsed or holds an invalid
agent, rather than overwrite it. The form checks the command against the
server's `PATH`, and an agent whose program is missing there can still be
saved, for use with `conductor host`; a command given as a relative path
(`./agent.sh`) is not looked up there, and the form says it is resolved at
launch in the session's directory.

**Installed agents.** The Agents page says which agents are installed on the
server (the program of their command resolves there, checked on every visit
and kept for 30 seconds; a check that takes more than two seconds, as on a
`PATH` that reaches a stalled network mount, shows the agent as installed
meanwhile) and links to the website of an agent that has one: all built-ins
but Shell name theirs, and the form takes one (`site`, an `https://`
address) for an agent you add. Any program of that name on the server's `PATH`
counts, so the Go migration tool `goose` makes the Goose agent show as
installed, and a command given as a relative path (`./agent.sh`) counts as
installed, because it resolves in the session's directory. The Launch dialog's
**Server** tab offers only the installed ones (**My machine** offers them all:
what is installed there is your machine's business), the crew editor marks the
others, and a crew whose member's agent is not installed is refused at launch.

Every agent, from the config file or the UI, is held to the same limits: the ID
matches `[a-z0-9-]{1,32}`, the name is at most 60 characters, the description
200, `command` has at most 32 elements of at most 4096 bytes each, `env` has at
most 32 keys, `envPassthrough` (names of server environment variables the agent
may inherit) at most 32 names, an `env` key or `envPassthrough` name is at most
128 bytes and an `env` value at most 16384 bytes, `cwd` is at most 4096 bytes
without NUL, `icon` matches `[a-z0-9][a-z0-9:-]{0,63}`, `site` is an `https://`
URL with a host name and a valid port, no user info or white space, of at most
200 bytes, and a signal `pattern` (a regular expression) at most 200 bytes that
does not match an empty line. An `adapter`, if an agent names one, matches
`[a-z0-9-]{1,32}` and must be one Conductor has, in the config file (or the
catalog file) as on the Agents page: the server refuses to start with an
unknown one. A `yolo` recipe has at most 16 `args` of 1 to 4096 bytes without
NUL and at most 16 `env` variables, each named like an `envPassthrough` name
but never `CONDUCTOR_*`, with a value of at most 4096 bytes; its values are
shown as they are, not masked: they are switches, not secrets. A `trustPrompt`
(the words of the agent's workspace-trust question, which holds a crew prompt)
is held to the rules of a signal pattern. A `session` recipe has at most 16
`startArgs` and 16 `resumeArgs` of 1 to 4096 bytes, `{id}` a whole argument and
once in each list that has any, `newId` `uuid` or `name`, `idFrom` `hook` or
nothing, `idPolicy` `latest` or `lowest`, startArgs or `idFrom` to say how the
id is known, and an `idPattern` anchored with `^` and `$`, at most 200 bytes,
that matches neither an empty id nor one that begins with a dash.

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
- Yolo launches agents without their permission prompts, as the user running
  `conductor serve`, and Codex's recipe turns its sandbox off too: turn it on
  only on a server whose allowed roots you would let an agent change unasked.
  Recipes are catalog data, held to the catalog's limits, passed as arguments
  and through the filtered environment. An agent session id that Resume passes
  to an agent must match its agent's `idPattern` and begin with a letter or a
  digit, so it can never read as a flag, and goes in as one argument.
- Terminal output is not persisted. Sessions and links live in memory and are
  lost on restart; hosted sessions reconnect and resume while the server is up.
- WebRTC needs UDP between the browser and the host; otherwise the relay is
  used automatically. No TURN credential minting yet.
- Share links outside a trusted network over TLS only: the `tls` listener
  with a certificate from Let's Encrypt (or your own), or a reverse proxy
  that terminates TLS. Plain http is never mapped on the router or
  advertised on the public address.

## Development

```bash
make run        # Go API on :8080 with --dev (CORS and origins for localhost:3000)
make web-dev    # Nuxt dev server on :3000 proxying /api and /ws to :8080
make test       # go test -race ./...
make lint       # gofmt + go vet
npm --prefix web run typecheck && npm --prefix web test
make test-e2e   # builds bin/conductor, then the Playwright suite (web/e2e) against a stub agent
```
The end-to-end suite starts its own server with its own home, data directory
and port, and a catalog whose `claude` and `codex` are a stub script; its
live test, which drives the real `claude` and `codex` with a tiny prompt,
runs only with `CONDUCTOR_E2E_LIVE=1` and both on the `PATH`.


If the Nuxt dev proxy does not upgrade WebSockets in your setup, run the dev
server with `NUXT_PUBLIC_API_BASE=http://localhost:8080`.

The wire protocol is documented in [docs/protocol.md](docs/protocol.md), the
architecture in [docs/architecture.md](docs/architecture.md), and the brand in
[docs/design/brand.md](docs/design/brand.md). A `Dockerfile` builds a server
image without agent CLIs; install them in a derived image or use `conductor host`.
The image keeps its data directory on the `/var/lib/conductor` volume.
