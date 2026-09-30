# Conductor wire protocol

Source of truth for `internal/proto` (Go) and `web/app/utils/protocol.ts`
(TypeScript). Change all three together.

## Framing

Every terminal-stream message is a binary frame. Byte 0 is the type, the rest is
the payload. Frames travel unchanged over WebSockets, WebRTC data channels and the
relay envelope, so a client handles them identically regardless of transport.

| Type | Name | Direction | Payload | Limit |
|---|---|---|---|---|
| `0x01` | OUTPUT | owner → client | raw PTY bytes | 16 KiB |
| `0x02` | INPUT | client → owner | raw keystrokes; rejected for `view` | 32 KiB |
| `0x03` | CONTROL | both | UTF-8 JSON with a `t` discriminator | 8 KiB |
| `0x04` | SCROLLBACK | owner → client | replay bytes, only between `welcome` and `ready` | 16 KiB |
| `0x05` | SIGNAL | viewer ↔ server | JSON WebRTC signaling | 64 KiB |
| `0x06` | FILE | owner → client | `[8-byte reqId][uint32 headerLen][JSON header][bytes]` | 1 MiB body |
| `0x07` | CHUNK | data channel only | `[uint16 msgId][uint32 total][uint32 offset][data]` | 32 KiB |
| `0x10` | RELAY | host ↔ server | `[16-byte viewerId][inner frame 0x01–0x04, 0x06]` | |

"Owner" is whichever process holds the PTY: the server for `server` sessions,
`conductor host` for `hosted` sessions.

CHUNK frames exist because browsers cap data channel messages (Chromium at
256 KiB). A host fragments any frame larger than 32 KiB; the browser reassembles
by `msgId` and then processes the inner frame.

## Control messages

Client → owner:

| `t` | Fields | Notes |
|---|---|---|
| `hello` | `proto:1, cols, rows, client, name?` | Must be the first frame, within 5 s. `name` is the display name other viewers see (≤ 160 bytes on the wire; cleaned to ≤ 40 runes, control characters stripped, empty → `guest`) |
| `resize` | `cols, rows` | Controllers only; 1–500 |
| `ping` | `ts` | Answered with `pong` |
| `file_get` | `reqId, path, stat?` | Answered with a FILE frame |

Owner → client:

| `t` | Fields |
|---|---|
| `welcome` | `proto, sessionId, role, subscriberId, cols, rows, status, scrollbackBytes, transport, fileView` (+ `viewerId, iceServers, relayTimeoutMs` on the signaling welcome) |
| `ready` | end of scrollback; live output follows |
| `resize` | `cols, rows, by` (subscriber that resized) |
| `status` | `status, exitCode?` |
| `attention` | `state, message?, source?, kind?, options?[{label, input}]` (see Attention) |
| `activity` | `at, type, by?, byName?, message?, url?, to?, tool?` — one activity-log entry: `attention`, `input`, `join`, `leave`, `link`, `status` or one of the six event types (see Events). The last 50 entries replay right after `ready`; new ones follow live. |
| `viewers` | `count, list[{id, name, role, link?, since, lastInputAt?}]` — the full roster, sent on every join and leave and at most every 2 s per viewer while they type. `link` is the share link's label. |
| `error` | `code, message` |
| `pong` | `ts` |

Error codes: `read_only`, `slow_consumer`, `bad_frame`, `hello_timeout`,
`revoked`, `session_ended`, `host_disconnected`, `file_denied`,
`too_many_requests`.

WebSocket close codes: `1000` normal, `1001` server shutdown, `4400` protocol
error, `4401` unauthorized, `4403` link revoked, `4404` unknown session, `4409`
too many viewers, `4410` session or host gone.

## Resize policy

The most recent resize from any `control` client wins and is broadcast to every
attached client, which resizes its terminal to match. `view` clients never
resize. Attaching as a controller applies the client's size immediately.

## Hosted sessions: signaling and relay

For a hosted session the viewer's WebSocket first receives
`welcome{transport:"webrtc", viewerId, iceServers, relayTimeoutMs, relayOnly?}`.
A host started with `--relay-only` sets `relayOnly`, and the viewer requests the
relay immediately instead of attempting WebRTC. The viewer
creates the data channel and the SDP offer; the host answers. Candidates trickle
in both directions.

Viewer → server (SIGNAL): `offer{sdp}`, `ice{candidate}`, `relay{reason}`.
Server → viewer (SIGNAL): `answer{sdp}`, `ice{candidate}`, `relay_ok`, `error`.

When the data channel opens the viewer sends `hello` over it and the host replies
with the normal welcome/scrollback/ready sequence (`transport:"webrtc"`).

If the channel has not opened after `relayTimeoutMs`, or ICE fails, the viewer
sends `relay`. After `relay_ok` the same WebSocket carries terminal frames: the
server wraps the viewer's frames in RELAY envelopes for the host and unwraps
the host's envelopes for the viewer. The welcome then reports `transport:"relay"`.
View-role INPUT and `resize` are dropped by the server before they reach the host.

## Host control connection

`GET /ws/host?token=…` (host token or admin token). Text frames are JSON; binary
frames are RELAY envelopes.

Host → server: `register{proto, host{name,version,user?}, session{name,agentId,command,cwd,cols,rows,relayOnly?,agentToken?,branch?}, resume?{sessionId,secret}}`
(`user` ≤ 64 bytes is the OS user for "hosted by"; `branch` ≤ 200 bytes is read from `.git/HEAD`),
`status{sessionId,status,exitCode?}`, `resize{sessionId,cols,rows}`,
`answer{viewerId,sdp}`, `ice{viewerId,candidate}`, `viewer_error{viewerId,code,message}`,
`viewer_closed{viewerId}`, `attention{sessionId,state,message?,source,kind?,options?}`,
`activity{sessionId,entry,state?}`.

Server → host: `registered{sessionId, secret, shareBaseUrl, resumed, iceServers}`,
`viewer_join{viewerId, role, linkId?, linkLabel?}`, `offer{viewerId,sdp}`, `ice{viewerId,candidate}`,
`relay_start{viewerId}`, `viewer_leave{viewerId}`, `stop{sessionId}`,
`attention{state,message?,source,kind?,options?}` (API-originated change to broadcast),
`activity{entry}` (an event reported through the API, for the host to record), `error`.

`activity` carries one activity-log entry, the fields of the `activity`
control message above (`entry` has its own `t`), in both directions. Host to
server, it carries every entry the host's session records, so that
`GET /api/events` shows a hosted session like a server one. The host queues
them (at most 256 waiting; a burst past that is dropped, and so is anything
recorded while the connection is down, for the session log has it all) and
sends them in order. With an `attention` entry the host sends `state`
(`needs_input`, `working` or `done`): the attention state its session was in
when it recorded the entry, read on the goroutine that recorded it, which is
the state the entry records. The host sends a change of state in its own
`attention` message on another path, so the server may receive the entry
first; `state` is how it types the entry for webhooks all the same (see
Events). The server takes the session from the connection and ignores
`sessionId`, drops an entry whose `type` it does not know instead of closing
the connection, cuts every field to the limits of Events (`by` too: it loses
its control characters and surrounding space, and one still over 64 bytes is
dropped rather than cut), keeps `state` only on an `attention` entry and only
as one of the three states (it ignores anything else), stamps the time of
receipt on an entry without a readable `at`, and never sends the entry back.
Server to host, it carries an event that an agent reported through
`POST /api/sessions/{id}/events` for the hosted session: the server has no
activity log for it, so the host records it (its own event limit applies)
and reports it back like any other entry. The server sends at most 20 of
these a second, 40 at once, together with the
`attention` messages it forwards (see Events); it never sends `state`. Both
directions are text frames under the host-message limit (64 KiB): `entry`
is bounded so that it fits one 8 KiB CONTROL frame, and the envelope,
`state` included, adds under 100 bytes. A peer that does not know `activity`
ignores it, so a hosted session of an older host produces no events; one that
does not know `state` ignores that field.

The host registers before it starts the process so the session ID can be
placed in the agent's environment.

A host that loses its connection reconnects with `resume` and the secret from
`registered`. The session shows `host_disconnected` in the meantime and is
removed after 60 s without the host.

## Attention

Each session carries `attention{state, message, source, since, kind?, options?}`
in its `Info`. States: `""` (nothing), `working`, `needs_input`, `done`.
Sources: `api` (the agent's own token), `admin`, `bell`, `osc`, `pattern` (the
screen-pattern detector, below), `input` (cleared by a controller typing).

`kind` describes the shape of a prompt so clients can offer one-click answers:
`permission` (a numbered permission dialog), `prompt` (a free-text reply is
expected), `done` (the agent finished its turn), or empty when unknown.
`options` is at most 6 entries of `{label, input}`: `label` is shown on a
button (≤ 60 runes) and `input` (≤ 16 bytes) is sent verbatim as an INPUT
frame when the human picks it. Clearing the state drops `kind` and `options`.
`conductor notify --claude-hook` maps Claude Code `PermissionRequest` hooks
(and `permission_prompt` notifications) to `kind:"permission"` with the
options `Yes`/`1`, `Always for this session`/`2`, `No, explain…`/`3`; other
notifications are `kind:"prompt"` with no options. The digit inputs assume
Claude Code's permission dialog selects and confirms on the number key and
that the dialog has three choices; if a prompt only highlights the choice, or
offers two, `permissionOptions()` in `internal/notify/notify.go` is the one
place to change.

Automatic detection runs on whichever process owns the PTY. A bare BEL
(`0x07`) outside an escape sequence, `ESC ] 9 ; text ST` (iTerm2/ConEmu style)
or `ESC ] 777 ; notify ; title ; body ST` (urxvt style) marks the session
`needs_input`; the BEL that terminates an ordinary OSC (window title, hyperlink)
does not. Bursts are limited to one change per 500 ms. Successful input from a
`control` client clears the `needs_input` state that was showing when the input
began. One raised while the input was being written (a process that answers at
once, an echo that rings the bell) is left for the next input.

An agent with no hook and no bell can be given a screen pattern instead: a
catalog signal `{"kind": "pattern", "pattern": "<RE2>"}` for server sessions,
`conductor host --signal-pattern '<RE2>'` for hosted ones; the pattern is at most
200 bytes and must not match an empty line. The
process that owns the PTY keeps the text of the last screen line: escape
sequences are stripped, `\r` rewrites the line from column 0, `\b` deletes a
character, and only the last 4096 bytes of a longer line are kept. After 500 ms
without output the line is matched against the pattern; an empty line or one of
white space never counts. A match marks the session `needs_input` with
`source:"pattern"`, `kind:"prompt"` and the message `prompt: <line>` (of a line
longer than a message can be, its last 492 bytes, cut on a character boundary),
unless it is `needs_input` already: a prompt a hook or a bell raised keeps its
own message, kind and options, and the same prompt does not report itself twice.
Output that never pauses (a spinner, a TUI that redraws continuously) never
gives the 500 ms of silence, so such an agent is not served by a pattern. A
host reports the change to the server like any other attention change.

Explicit updates: `POST /api/sessions/{id}/attention` with
`{state: "needs_input"|"working"|"done"|"clear", message?, kind?, options?}` and
`Authorization: Bearer <agent token>` (or the admin token). Every session's
process receives `CONDUCTOR_SESSION_ID`, `CONDUCTOR_NOTIFY_URL` and
`CONDUCTOR_NOTIFY_TOKEN`, which `conductor notify` reads, and `CONDUCTOR_BIN`,
the absolute path of the conductor binary its hooks run, which the Conductor
skill runs its commands with. Only Conductor sets them: `CONDUCTOR_*` from the
server's or the developer's environment and from an agent's `env` never reach
a session. The token is stored
hashed and only ever authorizes this route and `POST /api/sessions/{id}/events`
for this one session. Each report, the agent's or the admin's, spends a token
of the bucket the events of its session spend (see Events), on a server
session and on a hosted session whose host is connected: with none left the
answer is `429 rate_limited` and the state does not change. For a hosted
session the state is also sent on to the host; while no host is connected
the report is applied on the server without a token. What a session sees for
itself (the bell, an OSC notification, the screen pattern) spends no token,
and a change of state a session applies always records its `attention`
activity entry: a state that shows has its entry (see Events).

Session `Info` also carries `branch` (the git branch of the working
directory, read from `.git/HEAD` at launch; server and host alike) and, for
hosted sessions, `hostUser`. `GET /api/sessions/{id}/links` adds `active`
to every link: the number of viewers currently attached through it.

When a controller's input clears `needs_input`, the session records
`lastAnswer{by, byName, at, message}` in its `Info` (the prompt that was
answered and who answered it) and an `input` activity entry. What Conductor
types itself (a crew member's prompt) clears `needs_input` the same way and
always records an `input` entry, `byName` `crew` and the text typed, less its
line break, as `message`.

Session `Info` carries `crew{runId, crewId, member}` for a member of a crew
run (see Crew runs), and no `crew` otherwise.

Changes are pushed to attached clients as the `attention` control message and
to admins as `session` events on `GET /api/events` (Server-Sent Events over a
header-authenticated `fetch`: `snapshot` with the full list first, then
`session` per change, `removed{id}` when a session leaves the registry, and
`activity` for every activity entry, see Events).

## Events

An event is an activity entry that an agent reports about its work, next to
the entries the session records itself (`attention`, `input`, `join`, `leave`,
`link`, `status`). All twelve types travel as `activity` control messages,
live and in the replay after `ready`. The six event types:

| `type` | Meaning | Fields |
|---|---|---|
| `progress` | a step finished | `message` says which |
| `artifact` | something was produced | `url` points at it, `message` describes it |
| `handoff` | work passed to another member | `to` names them, `message` says why |
| `tool_use` | the agent ran a tool | `tool` names it |
| `tool_denied` | a tool call was refused | `tool` names it |
| `error` | the agent hit an error | `message` says what, `tool` names the tool involved |

The reporter chooses which fields an event carries; none is required.

Limits, applied to every entry as the session stores it: control characters
are dropped and surrounding space is trimmed. `message` is at most 500 bytes
and `tool` at most 100; both keep line breaks and tabs. `url` (at most 2048
bytes), `to` and `byName` (at most 40 runes each) are single lines. `url`,
`to`, `tool` and `byName` are cut at a character boundary. The session stores
`url` as text and does not look at its scheme, so a client links it only when
it is `http:` or `https:`. JSON writes `&`, `<` and `>` as six bytes each, so
a URL made of them could push the message past the 8 KiB CONTROL limit, which
a relay rejects: such a URL is dropped and the rest of the entry kept.

Each session records at most 20 entries a second on average and 40 at once (a
token bucket that refills continuously). An entry beyond that is not stored or
broadcast; the session counts it and logs the first drop and every 100th at
debug level. `join`, `leave`, `input`, `link` and `status` entries, which the
session and the server produce themselves, skip the limit, so a chatty hook
cannot crowd out the roster or the final `status` row. So does the
`attention` entry of a change of attention state the session applied: a
report through the API paid its token before it changed anything (below), and
the bell, an OSC notification and the screen pattern are the session's own
observations, which spend none. A state that shows always has its entry, in
the log, on the stream and at the webhooks, however many tool events came
before it.

Reporting: `POST /api/sessions/{id}/events` with `Authorization: Bearer
<agent token>` (or the admin token) and the body `{type, message?, url?, to?,
tool?, kind?, options?}`. `type` is one of the six event types or an
attention word: `needs_input`, `working`, `done` or `clear`. An attention word
is applied exactly as `POST /api/sessions/{id}/attention` applies it, with the
same checks, `kind`, `options` and effect, and the same `source` (`api` for the
agent token, `admin` for the admin); only the reply differs. The entries a
session records itself (`attention`, `input`, `join`, `leave`, `link`,
`status`) cannot be reported: they skip the limit above, and only the session
may make them. The reply is `202 {"accepted": true}`, and the entry has
`byName` `agent`. Errors: `400 invalid_type`, `400 invalid_request` (a body
that is not one JSON object of known fields, and for the attention words the
checks of `/attention`), `400 invalid_kind`, `401 unauthorized`, `404
not_found`, `409 session_ended` and `429 rate_limited`, which has two sources.
One is the session's bucket, when it has no token left (below). The other is the
per-client limiter that the API's credential checks share: a client refused with
`401` or `404` 20 times in quick succession (and 5 times a second after that) is
answered `429` in place of the next refusal.

A server session spends a token of its bucket on each event it records and
on each attention word, by this route or by `/attention`, before the word
changes anything: with no token the answer is `429 rate_limited` and neither
the state nor an entry is left behind. A hosted session keeps no log on the
server, but the server holds a bucket for it, of the same size (20 a second,
40 at once), and spends a token on every event and every attention word it
sends on to the host, by this route or by `/attention`: the host's connection
also carries its viewers' input, and closes when its queue is full, so a flood
of reports must not reach it. With no token the answer is `429 rate_limited`
and nothing is sent or changed. Only a report that goes to a connected host
spends one, and the host's own reports do not. The host applies an attention
word it is sent without a token of its own and records its entry, as a server
session does. An event the server sends to the host (see Host control
connection) is answered `202` once it is on its way. The server cannot know
whether the host's own bucket takes it, so an event the host drops is dropped
without a word to the caller; `409 host_disconnected` says that no host is
connected.

`conductor notify --event <type> [--message M] [--url U] [--to T] [--tool N]`
sends an event from inside a session: it turns the `CONDUCTOR_NOTIFY_URL` of
the session (`…/attention`) into `…/events`. `--event` cannot be combined with
`--state`, `--codex` or a `--<agent>-hook` flag, and `--url`, `--to` and
`--tool` are refused without it: nothing else carries them. A mistake exits 2,
or 1 in a hook's command line, where 2 would block the agent.

Streaming: every entry a session records, whether an agent reported it or the
session made it, reaches admins as an `activity` event on `GET /api/events`,
next to `session` and `removed`:
`data: {"sessionId", "at", "type", "by"?, "byName"?, "message"?, "url"?, "to"?, "tool"?}`.
Entries arrive as the sessions record them, so a few can be out of order; `at`
says when each happened. The feed is live, not a log: a client that has fallen
behind (its queue is three quarters full) misses entries and keeps its
stream, with room left for the `session` and `removed` events it cannot do
without. The session's own `activity` control messages replay the log.

Webhooks: the server also POSTs every entry whose event type a configured
webhook lists (README, Webhooks) to its URL, one entry a request, with the
body `{"sessionId", "session": {"id", "name", "agentId"}, "entry": {…}}`, the
entry as the stream above sends it. The headers are `Content-Type:
application/json`, `X-Conductor-Event` and, for a webhook with a secret,
`X-Conductor-Signature: sha256=<hex HMAC-SHA256 of the body>`.
`X-Conductor-Event` is the type the webhook listed: an entry type, or the
Events page's name for the entry, which wins when both are listed. An
attention entry is the attention state it records, the state its session was
in when it recorded the entry: a server session's is read then, a hosted
session's comes with the entry (`state` of the host's `activity` message).
An entry that comes without one, from an older host, is the state it names as
its message, if it names one (as it does for a report without a message), and
otherwise only an `attention` entry. A `status` entry
`exited (exit N)` with N other than 0 is `exit_nonzero`. Each webhook queues
at most 256 entries and drops its oldest; a delivery is tried once, for at
most 5 s, without following redirects.

## File reads

`file_get` resolves `path` against the session working directory (`~` expands to
the owner's home). After symlink resolution the target must stay inside the
working directory, and `.git/objects` is never readable. Server sessions also
refuse the server's data directory (`dataDir`, which holds agent secrets) with
everything in it, its config file (admin and host tokens) and its catalog file
(`catalogPath`), even inside the working directory. Every one of these rules
answers `denied`. A hosted session applies only the first two: the host serves
everything else under its working directory. Responses carry a JSON header
`{reqId, path, kind:"file"|"dir"|"error", size, truncated, binary, mime, exists, entries?, error?}`
followed by up to 1 MiB of bytes for text files. Files with a NUL byte in the
first 8 KiB are reported `binary` without bytes; images are sent as bytes. Up to
four requests may be in flight per client. `stat:true` returns only the header.

The `fileView` server setting decides who may read: `view` (both roles, the
default), `control` (controllers only) or `off`; `conductor host --file-view`
does the same for a hosted session.

## HTTP API

Every `/api/...` route, with the credential it needs. Admin means
`Authorization: Bearer <admin token>`; a share token is also accepted where the
table says so. JSON request bodies are limited to 64 KiB (1 MiB on the crew
routes, 128 KiB when adding a member to a run) and unknown fields are rejected. Errors are
`{"error":{"code","message"}}`, with more fields where the table says so. The
WebSocket routes, `GET /ws/sessions/{id}` and `GET /ws/host`, are described
above.

| Route | Auth | Purpose |
|---|---|---|
| `GET /api/health` | none | liveness: `ok`, `version`, `commit`, `sessions` |
| `GET /api/whoami` | admin | OS user running the server (the default display name) |
| `GET /api/catalog` | admin | `{agents, hidden}`: the launchable agents, `env` values masked as `***`, and the IDs hidden from the catalog |
| `POST /api/catalog` | admin | add an agent or replace the one with the same `id` (a built-in too); body is the agent, reply `{agent}`; an `env` value of `***` (what `GET /api/catalog` shows) keeps the value stored for that key and is rejected for a key the agent does not have; `400 invalid_agent` carries the validation message, an unknown `adapter` included; `503 store_unavailable` when there is no data directory |
| `DELETE /api/catalog/{id}` | admin | remove the saved override with that `id`, which restores a built-in it replaced; an agent with no override is hidden instead; `204`, `404` when unknown |
| `POST /api/catalog/{id}/unhide` | admin | take a hidden `id` off the hidden list, which brings back the agent it hid as it was; reply `{agent}`, or `{}` when no agent has that `id` any more; `404` when the `id` is not hidden |
| `POST /api/catalog/check` | admin | body `{command}`, reply `{found, path?}`: whether `command[0]` resolves on the server (`exec.LookPath`); nothing is run, and a missing program is `found:false`, not an error |
| `GET /api/crews` | admin | `{crews}`: the saved crews ordered by name (ignoring case), each `{id, name, goal, cwd, where, isolation, openAfterLaunch, viewLinkTtlSeconds?, members, createdAt, updatedAt}`, a member being `{name, agentId, prompt, args?, start: {when, member?}}`; `[]` when there is no data directory |
| `POST /api/crews` | admin | create a crew: the body is a crew without `id`, `createdAt` and `updatedAt`, which the server sets and rejects like any unknown field; the `id` comes from the name (lower case, every other run of characters a `-`, at most 40 characters, `crew` when nothing is left), then `-2`, `-3`… when taken; reply `201 {crew}`; `400 invalid_crew` carries the validation message, an agent the catalog does not have included; `409 too_many_crews` past 50 crews; `503 store_unavailable` when there is no data directory |
| `PUT /api/crews/{id}` | admin | replace a crew's fields with the body, shaped as for create; `id` and `createdAt` never change, `updatedAt` is now; reply `{crew}`; `400 invalid_crew` as for create; `404` when unknown |
| `DELETE /api/crews/{id}` | admin | delete a crew; `204`, `404` when unknown |
| `POST /api/crews/{id}/duplicate` | admin | save a copy of a crew as `<id>-copy` (then `<id>-copy-2`…) named `<name> copy`, with new times; reply `201 {crew}`; `400 invalid_crew` when one of its agents is no longer in the catalog or the copy would be over 512 KiB; `404` when unknown; `409 too_many_crews` |
| `POST /api/crews/{id}/launch` | admin | launch a crew as a run (see Crew runs); reply `201 {run}` once the session of every member that starts immediately exists, each member `starting` until its prompt is typed; `400 invalid_crew` for a crew with no members, one that runs on a host, an agent the catalog does not have, arguments to an agent that takes none, or with `isolation: worktree` a `.conductor` or `.conductor/worktrees` in `cwd` that is a symbolic link; `400 invalid_cwd` as for a session; `409 not_a_repo` with `isolation: worktree` when `cwd` is in no git working tree (`git -C <cwd> rev-parse --show-toplevel` fails), or in one whose `HEAD` is no commit, each with its own message; `409 run_stopped` when the run is stopped while its sessions start (the run stays, stopped); a member whose session cannot be created answers as `POST /api/sessions` would, naming the member; `500 launch_failed` otherwise; `404` when unknown; `503 store_unavailable` without a data directory |
| `GET /api/runs` | admin | `{runs}`: the runs in the server's memory, newest first |
| `GET /api/runs/{run}` | admin | `{run}` with each worktree member's `diff{added, removed}` (see Crew runs); `404` when unknown |
| `POST /api/runs/{run}/members` | admin | add a member mid-run: body a crew member; reply `201 {run}`, once the session of a member that starts immediately exists; `400 invalid_crew` for an invalid member, a name the run has (`the name is used twice`), an `after` naming no member of the run, a 13th member or an agent as at launch; a member whose session cannot be created answers as at launch, and stays in the run, `ended` with its `error`, its name taken: adding it again under that name is `400 invalid_crew`; `409 run_stopped`; `404` when unknown |
| `POST /api/runs/{run}/members/{name}/start` | admin | start a pending member by hand, whatever its start condition; reply `{run}` once its session exists, the member `starting` until its prompt is typed; a session that cannot be created answers as at launch, the member `ended` with its `error`; `409 member_started`, `409 run_stopped`; `404` for an unknown run or member |
| `POST /api/runs/{run}/stop` | admin | stop every member's session; reply `{run}` with `stoppedAt`; the worktrees stay; stopping again changes nothing; a session that does not stop cleanly is logged by the server and the run is stopped all the same; `404` when unknown |
| `GET /api/integrations` | admin | `{integrations, host, webhooks}`: every hook adapter in a stable order, each `{id, name, events, launchInjection, installsSkill, installed, where, snippet, experimental}`, the server's host name (`""` when it cannot tell), and the configured webhooks, each `{url, events}` with the URL as `scheme://host[:port]/path` (no user info, query or fragment) and never its secret; `installsSkill` is true for an agent whose install also brings the Conductor skill; `installed` and `where` check the home of the user running the server, writing nothing, and are `false` and `""` for an adapter with no file to install |
| `POST /api/integrations/{id}/install` | admin | install the adapter's hooks (and, for Claude Code, Codex, pi and Goose, the Conductor skill) into the agent's own config in the server user's home, and nowhere else; reply `{changed}`, the files written, `[]` when all was in place; `400 no_file_route` when there is no file to install into or a step is left to do by hand, the error carrying `snippet` and `changed` (files already written); `500 install_failed` with `changed`; `404` for an unknown `id` |
| `GET /api/sessions` | admin | list sessions |
| `POST /api/sessions` | admin | launch a server session: `{agentId, name?, cwd?, args?, cols?, rows?}`, reply `201` with the session `Info` |
| `GET /api/sessions/{id}` | admin or share token | one session with the caller's `role`; admins also get its `links` |
| `DELETE /api/sessions/{id}` | admin | stop a running session; on an ended session, remove it from the list |
| `GET /api/sessions/{id}/links` | admin | share links of a session, each with `active` viewers |
| `POST /api/sessions/{id}/links` | admin | create a share link: `{role, label?, ttlSeconds?}`, reply `201 {link, token, url}` |
| `DELETE /api/sessions/{id}/links/{linkId}` | admin | revoke a share link; `204` |
| `GET /api/sessions/{id}/files` | admin or share token | read a file of a server session (`path`, `stat`, `raw` query), see File reads |
| `POST /api/sessions/{id}/attention` | agent token or admin | report an attention state, see Attention |
| `POST /api/sessions/{id}/events` | agent token or admin | report an event or an attention word, reply `202 {accepted}`, see Events |
| `GET /api/events` | admin | Server-Sent Events of session changes (`snapshot`, `session`, `removed`) and of activity entries (`activity`), see Attention and Events |
| `GET /api/join/{token}` | share token in the path | resolve a share link for the join page (rate limited) |

The catalog routes persist their changes as `catalog.json` in the data
directory (`dataDir`): `{"agents": [...], "hidden": [...]}`. At startup that
overlay is applied over the configured catalog, and the server refuses to start
when the file cannot be parsed or an agent in it is invalid. Agents are held to
these limits everywhere: `id` matches `^[a-z0-9-]{1,32}$`, `name` at most 60
characters, `description` 200, `command` 1 to 32 elements of at most 4096 bytes
each, `env` 32 keys, `envPassthrough` 32 names, a signal `pattern` 200 bytes that
does not match an empty line.

The crew routes persist their changes as `crews.json` in the data directory:
`{"crews": [...]}`. The server refuses to start when the file cannot be parsed
or holds an invalid crew, an `id` twice or more than 50 crews. Crews are held to
these limits: at most 50 crews; `id` matches `^[a-z0-9][a-z0-9-]{0,63}$`; `name`
not blank, at most 60 characters and without control characters (surrounding
space is trimmed), `goal` at most 2000 characters, `cwd` at most 4096 bytes;
`where` is `server` or `host` and `isolation` is `none` or `worktree`;
`viewLinkTtlSeconds` 0 to 31536000 (a year); at most 12 members, each with a
`name` matching `^[a-z0-9][a-z0-9._-]{0,39}$`, unique in its crew and one git
takes for a branch (no `..`, no `.` or `.lock` at the end), an `agentId`
matching `^[a-z0-9-]{1,32}$` that the catalog has when the crew is saved or
copied, a `prompt` of at most 4000 characters and at most 32 `args` of at most
4096 bytes, 8 KiB in all; `start.when` is `immediately`, `after` or `manual`,
`start.member`, set with `after` alone, names another member of the crew, and
following `start.member` from member to member never goes round in a cycle.
The whole crew, as the server writes it with its `id` and times, is at most
512 KiB of JSON. Create and update check the body first: a crew that breaks one
of these rules is refused with `400 invalid_crew` for that rule, ahead of an
agent the catalog does not have, the 50-crew limit (`409`) and an unknown `id`
(`404`); an agent the catalog does not have is `400 invalid_crew` whatever else
holds. An error message quotes at most 80 characters of a value, with `…` where
it was cut.

### Crew runs

`POST /api/crews/{id}/launch` starts a run: every member becomes an ordinary
server session, created as `POST /api/sessions` creates one (its `cwd` through
the same check, its agent from the catalog, its hooks injected), named after the
member and tagged with `crew`. Its process also receives `CONDUCTOR_CREW`,
`CONDUCTOR_RUN`, `CONDUCTOR_MEMBER` and `GOAL`. A run's `id` is `<crew id>-<8
hex>`. Members that start `immediately` start at launch; an `after` member
starts when the member it names first reports `done` once its own prompt is
typed; a `manual` member waits for its start route. With `isolation: worktree`,
`cwd` is the top of a git working tree or a directory in one, and each member
gets `git -C <cwd> worktree add -b crew/<run>/<member>
<cwd>/.conductor/worktrees/<run>/<member> HEAD`, a worktree of the whole
repository, and starts in its directory that `cwd` is of the repository (`git
rev-parse --show-prefix`), made when no commit has a file there. The first
worktree also adds a `.conductor/` line, once, to the file `git -C <cwd>
rev-parse --git-path info/exclude` names (making `info/` when it is missing), so
that the worktrees stay out of the main checkout's `git status`. A `.conductor`
or `.conductor/worktrees` in `cwd` that is a symbolic link is refused before git
runs. Conductor never deletes a worktree or a branch.

A launch, a start or an added member answers once the member's session exists;
the member is `starting` until its prompt is typed, then `running`. A member is
ready for its prompt when its agent reports `needs_input` or `done`, or after
one second without output that follows its first output and at least two seconds
after its start, checked every 250 ms; after 60 seconds the prompt is typed
anyway and the run log says so. The prompt, `$GOAL` and `${GOAL}` replaced by
the goal, is typed with a carriage return; a `done` the member reports as its
prompt is written counts for the members after it. A member whose process ends
first gets no prompt: it is `ended` with its exit in `error`, and the run goes
on; a member whose prompt cannot be typed ends alone, its session stopped, the
reason in `error`. When a member's session cannot be created at launch, the ones
started are stopped and no run is kept; when the run is stopped while its
sessions start, it stays, stopped. A member whose start fails in a run that goes
on stays in the run, `ended`, and keeps its name: `POST /api/runs/{run}/members`
with that name is refused as a name used twice. A run is `{id, crewId, name,
goal, cwd, isolation, startedAt, stoppedAt?, members, log}`, a member `{name,
agentId, start, sessionId?, branch?, worktree?, status, startedAt?, endedAt?,
error?, diff?}` with `status` `pending`, `starting`, `running` or `ended`;
`diff` counts the lines of tracked files the member's worktree adds and removes
against the commit it began from, committed or not (`git diff --shortstat
<base>`, read at most every 10 s, the last value kept when a read fails);
untracked files do not count, and a branch merged into the member's (main, say)
counts with it. `log` is the run's own, at most 200 activity entries. Runs live
in memory: a server restart forgets them, and past 100 runs a launch forgets the
oldest with nothing running.
