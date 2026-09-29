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
| `activity` | `at, type, by?, byName?, message?` — one activity-log entry (`attention`, `input`, `join`, `leave`, `link`, `status`). The last 50 entries replay right after `ready`; new ones follow live. |
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
`viewer_closed{viewerId}`, `attention{sessionId,state,message?,source,kind?,options?}`.

Server → host: `registered{sessionId, secret, shareBaseUrl, resumed, iceServers}`,
`viewer_join{viewerId, role, linkId?, linkLabel?}`, `offer{viewerId,sdp}`, `ice{viewerId,candidate}`,
`relay_start{viewerId}`, `viewer_leave{viewerId}`, `stop{sessionId}`,
`attention{state,message?,source,kind?,options?}` (API-originated change to broadcast), `error`.

The host registers before it starts the process so the session ID can be
placed in the agent's environment.

A host that loses its connection reconnects with `resume` and the secret from
`registered`. The session shows `host_disconnected` in the meantime and is
removed after 60 s without the host.

## Attention

Each session carries `attention{state, message, source, since, kind?, options?}`
in its `Info`. States: `""` (nothing), `working`, `needs_input`, `done`.
Sources: `api` (the agent's own token), `admin`, `bell`, `osc`, `input`
(cleared by a controller typing).

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
does not. Bursts are limited to one change per 500 ms. Any successful input from
a `control` client clears a `needs_input` state.

Explicit updates: `POST /api/sessions/{id}/attention` with
`{state: "needs_input"|"working"|"done"|"clear", message?, kind?, options?}` and
`Authorization: Bearer <agent token>` (or the admin token). Every session's
process receives `CONDUCTOR_SESSION_ID`, `CONDUCTOR_NOTIFY_URL` and
`CONDUCTOR_NOTIFY_TOKEN`; `conductor notify` reads them. The token is stored
hashed and only ever authorizes this one route for this one session.

Session `Info` also carries `branch` (the git branch of the working
directory, read from `.git/HEAD` at launch; server and host alike) and, for
hosted sessions, `hostUser`. `GET /api/sessions/{id}/links` adds `active`
to every link: the number of viewers currently attached through it.

When a controller's input clears `needs_input`, the session records
`lastAnswer{by, byName, at, message}` in its `Info` (the prompt that was
answered and who answered it) and an `input` activity entry.

Changes are pushed to attached clients as the `attention` control message and
to admins as `session` events on `GET /api/events` (Server-Sent Events over a
header-authenticated `fetch`: `snapshot` with the full list first, then
`session` per change and `removed{id}` when a session leaves the registry).

## File reads

`file_get` resolves `path` against the session working directory (`~` expands to
the owner's home). After symlink resolution the target must stay inside the
working directory; `.git/objects` is never readable. Responses carry a JSON header
`{reqId, path, kind:"file"|"dir"|"error", size, truncated, binary, mime, exists, entries?, error?}`
followed by up to 1 MiB of bytes for text files. Files with a NUL byte in the
first 8 KiB are reported `binary` without bytes; images are sent as bytes. Up to
four requests may be in flight per client. `stat:true` returns only the header.

The `fileView` server setting decides who may read: `view` (both roles, the
default), `control` (controllers only) or `off`.

## HTTP API

Every `/api/...` route, with the credential it needs. Admin means
`Authorization: Bearer <admin token>`; a share token is also accepted where the
table says so. JSON request bodies are limited to 64 KiB and unknown fields are
rejected. Errors are `{"error":{"code","message"}}`. The WebSocket routes,
`GET /ws/sessions/{id}` and `GET /ws/host`, are described above.

| Route | Auth | Purpose |
|---|---|---|
| `GET /api/health` | none | liveness: `ok`, `version`, `commit`, `sessions` |
| `GET /api/whoami` | admin | OS user running the server (the default display name) |
| `GET /api/catalog` | admin | launchable agents; `env` values are masked as `***` |
| `POST /api/catalog` | admin | add an agent or replace the one with the same `id` (a built-in too); body is the agent, reply `{agent}`; `400 invalid_agent` carries the validation message, `503 store_unavailable` when there is no data directory |
| `DELETE /api/catalog/{id}` | admin | remove the saved override with that `id`, which restores a built-in it replaced; an agent with no override is hidden instead; `204`, `404` when unknown |
| `POST /api/catalog/check` | admin | body `{command}`, reply `{found, path?}`: whether `command[0]` resolves on the server (`exec.LookPath`); nothing is run, and a missing program is `found:false`, not an error |
| `GET /api/sessions` | admin | list sessions |
| `POST /api/sessions` | admin | launch a server session: `{agentId, name?, cwd?, args?, cols?, rows?}`, reply `201` with the session `Info` |
| `GET /api/sessions/{id}` | admin or share token | one session with the caller's `role`; admins also get its `links` |
| `DELETE /api/sessions/{id}` | admin | stop a running session; on an ended session, remove it from the list |
| `GET /api/sessions/{id}/links` | admin | share links of a session, each with `active` viewers |
| `POST /api/sessions/{id}/links` | admin | create a share link: `{role, label?, ttlSeconds?}`, reply `201 {link, token, url}` |
| `DELETE /api/sessions/{id}/links/{linkId}` | admin | revoke a share link; `204` |
| `GET /api/sessions/{id}/files` | admin or share token | read a file of a server session (`path`, `stat`, `raw` query), see File reads |
| `POST /api/sessions/{id}/attention` | agent token or admin | report an attention state, see Attention |
| `GET /api/events` | admin | Server-Sent Events of session changes, see Attention |
| `GET /api/join/{token}` | share token in the path | resolve a share link for the join page (rate limited) |

The catalog routes persist their changes as `catalog.json` in the data
directory (`dataDir`): `{"agents": [...], "hidden": [...]}`. At startup that
overlay is applied over the configured catalog, and the server refuses to start
when the file cannot be parsed or an agent in it is invalid. Agents are held to
these limits everywhere: `id` matches `^[a-z0-9-]{1,32}$`, `name` at most 60
characters, `description` 200, `command` 1 to 32 elements of at most 4096 bytes
each, `env` 32 keys, `envPassthrough` 32 names, a signal `pattern` 200 bytes.
