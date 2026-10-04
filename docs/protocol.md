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
| `hello` | `proto:1, cols, rows, client, name?` | Must be the first frame, within 5 s. `cols, rows`: a controller's two values in 1–500 set the session's size (see Resize policy); `0, 0` follows the session's size and changes nothing, as does any other pair out of range; a viewer's never changes it. The size never decides the role, which comes from the token. `name` is the display name other viewers see (≤ 160 bytes on the wire; cleaned to ≤ 40 runes, control characters stripped, empty → `guest`) |
| `resize` | `cols, rows` | Controllers only; 1–500 |
| `ping` | `ts` | Answered with `pong` |
| `file_get` | `reqId, path, stat?` | Answered with a FILE frame |
| `submit` | `text` | Controllers only; `text` at most 4096 bytes (`bad_frame` beyond, `read_only` from a view link, also on the relay). The owner submits it as a line: line breaks and tabs become spaces and other control characters are dropped, the text is written as a bracketed paste while the program has that mode on (`ESC[?2004h`), then a carriage return is written on its own 250 ms later, so that a TUI takes it as Enter rather than as part of a paste. The Enter answers the prompt that was showing when the text was typed; a prompt raised during the pause is not answered and the Enter is left out. The submission goes on if the client disconnects meanwhile. The reply boxes use it; keys typed into the terminal stay INPUT |

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
resize. Attaching as a controller applies the client's size immediately, unless its
`hello` says `0, 0`: a viewer that shows the session scaled at the session's
own size (the view-only run tiles, a quick reply) follows the size and never
resets it. A terminal's automatic reports (a cursor position report, device
attributes, focus in and out, colour replies; at most 256 bytes, nothing else
in the frame) are written to the process like any INPUT but do not answer the
prompt the session waits on. When a session's process ends, its attention is
cleared in the same change that sets `exited` or `stopped`.

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
sends `relay`. A switchyard without a relay (`switchyard.relay: false`)
answers a CONTROL `error{code:"relay_off"}` instead and keeps the connection,
so the viewer stays on its WebRTC attempt; such a switchyard also refuses a
host that registers with `relayOnly` (close 1002, `relay_off`). With
`switchyard.relayKBps` set, a host's relayed output is read no faster than
that many KiB a second (a burst of twice it): the host is slowed by its own
connection, and every frame still arrives whole and in order. After `relay_ok` the same WebSocket carries terminal frames: the
server wraps the viewer's frames in RELAY envelopes for the host and unwraps
the host's envelopes for the viewer. The welcome then reports `transport:"relay"`.
View-role INPUT, `resize` and `submit` are dropped by the server before they reach the host (`read_only`).

## Host control connection

`GET /ws/host?token=…` (host token or workbench token; on a switchyard with
`switchyard.openHosts`, none at all: an open host, limited per address in how
often it registers, how many live sessions it holds and what it relays). Text
frames are JSON; binary
frames are RELAY envelopes.

Host → server: `register{proto, host{name,version,user?}, session{name,agentId,command,cwd,cols,rows,relayOnly?,agentToken?,branch?}, resume?{sessionId,secret}}`
(`user` ≤ 64 bytes is the OS user for "hosted by"; `branch` ≤ 200 bytes is read from `.git/HEAD`),
`status{sessionId,status,exitCode?}`, `resize{sessionId,cols,rows}`,
`answer{viewerId,sdp}`, `ice{viewerId,candidate}`, `viewer_error{viewerId,code,message}`,
`viewer_closed{viewerId}`, `attention{sessionId,state,message?,source,kind?,options?}`,
`activity{sessionId,entry,state?}`,
`link{requestId, role, ttlSeconds?, label?}` (a share link to the session,
minted by the server: `requestId` ≤ 32 bytes, `label` ≤ 120 bytes,
`ttlSeconds` ≤ 86400, at most 5 requests a minute per connection; one
without a `requestId` is a protocol error), `link_revoke{requestId, linkId}`
(revoke a link the server minted for this session: `linkId` ≤ 64 bytes; never
counted against the link bucket; a missing or oversize field is a protocol
error).

Server → host: `registered{sessionId, secret, shareBaseUrl, resumed, iceServers}`,
`viewer_join{viewerId, role, linkId?, linkLabel?}`, `offer{viewerId,sdp}`, `ice{viewerId,candidate}`,
`relay_start{viewerId}`, `viewer_leave{viewerId}`, `stop{sessionId}`,
`attention{state,message?,source,kind?,options?}` (API-originated change to broadcast),
`activity{entry}` (an event reported through the API, for the host to record),
`link_created{requestId, url, invite, linkId, role, label?, expiresAt?}`
(the link on the server's public base, and the same as an invite,
`conductor://<host>/join/<token>` with `?http=1` for a plain-http base,
which the desktop app opens itself), `link_revoked{requestId, linkId}`
(the link is revoked and its viewers closed; `error{not_found, requestId}`
for a link the server does not hold), `error{code, message, requestId?}`
(`requestId` names the link request a refusal answers: `invalid_role`,
`invalid_request`, `rate_limited`, `link_refused`).

`activity` carries one activity-log entry, the fields of the `activity`
control message above (`entry` has its own `t`), in both directions. Host to
server, it carries every entry the host's session records, so that
`GET /api/events` shows a hosted session like a server one. The host queues
them (at most 256 waiting; a burst past that is dropped, and so is anything
recorded while the connection is down, for the session log has it all) and
sends them in order. With an `attention` entry the host sends `state`
(`needs_input`, `working` or `done`): the attention state the entry records,
which its session hands on with the entry (the state set with the entry's
stamp). The host sends a change of state in its own `attention` message on
another path, so the server may receive the entry first; `state` is how it
types the entry for webhooks all the same (see Events). The server takes the
session from the connection and ignores `sessionId`, drops an entry whose
`type` it does not know instead of closing the connection, cuts every field to
the limits of Events (`by` too: it loses its control characters and surrounding
space, and one still over 64 bytes is dropped rather than cut), keeps `state`
only on an `attention` entry and only as one of the three states (it ignores
anything else), stamps the time of receipt on an entry without a readable `at`,
and never sends the entry back.
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
removed after 60 s without the host. A server that no longer knows the session (a restarted switchyard keeps
hosted sessions in memory) closes the resume with `4404`; the host then
registers afresh under a new id, and the links minted at the old one are
gone with it.

## Attention

Each session carries `attention{state, message, source, since, kind?, options?}`
in its `Info`. States: `""` (nothing), `working`, `needs_input`, `done`.
Sources: `api` (the agent's own token), `admin`, `bell`, `osc`, `pattern` (the
screen-pattern detector, below), `trust` (the agent's workspace-trust
question, below), `input` (cleared by a controller typing).

`kind` describes the shape of a prompt so clients can offer one-click answers:
`permission` (a numbered permission dialog), `prompt` (a free-text reply is
expected), `done` (the agent finished its turn), or empty when unknown.
`options` is at most 6 entries of `{label, input}`: `label` is shown on a
button (≤ 60 runes) and `input` (≤ 48 bytes) is sent verbatim as an INPUT
frame when the human picks it. Clearing the state drops `kind` and `options`.
`conductor notify --state needs_input --message "…" --choices "a|b|c"` (at most
6 choices of 40 bytes) reports `kind:"prompt"` with one option per choice whose
`input` is the choice and a CR, so a click types it as a line; the agent is
told to name the choices in the terminal first, so a typed answer matches.
`conductor notify --claude-hook` maps Claude Code `PermissionRequest` hooks
(and `permission_prompt` notifications) to `kind:"permission"` with the
options `Yes`/`1`, `Always for this session`/`2`, `No, explain…`/`3`; other
notifications are `kind:"prompt"` with no options. The digit inputs assume
Claude Code's permission dialog selects and confirms on the number key and
that the dialog has three choices; if a prompt only highlights the choice, or
offers two, `permissionOptions()` in `internal/notify/mappers.go` is the one
place to change.

Automatic detection runs on whichever process owns the PTY. A bare BEL
(`0x07`) outside an escape sequence, `ESC ] 9 ; text ST` (iTerm2/ConEmu style)
or `ESC ] 777 ; notify ; title ; body ST` (urxvt style) marks the session
`needs_input`; the BEL that terminates an ordinary OSC (window title, hyperlink)
does not. Bursts are limited to one change per 500 ms. Successful input from a
`control` client clears the `needs_input` state that was showing when the input
began. One raised while the input was being written (a process that answers at
once, an echo that rings the bell) is left for the next input. An input that is
only a terminal's own automatic reports, at most 256 bytes of them, is written
and clears nothing, nor stamps its viewer as typing: a cursor position report
(`ESC[<row>;<col>R`, with or without `?`), a status report (`ESC[0n`…`ESC[3n`),
device attributes (`ESC[?…c`, `ESC[>…c`), a mode report (`ESC[?<mode>;<n>$y`),
a window report (`ESC[<n>;…t`), a colour query's reply (`ESC]4;…`, `ESC]10;`
to `ESC]12;` with `rgb:…`, ended by BEL or ST), a DCS reply such as XTVERSION
(`ESC P >|… ESC \`), or focus in and out (`ESC[I`, `ESC[O`): a browser's xterm
answers Codex's cursor position query after every turn, and that answer is not
a person answering Codex. A trust question (`source:"trust"`) is cleared only
by an input with a carriage return in it, so an arrow key that moves its
selection leaves it showing. When a session's process exits or is stopped, its
attention state is cleared in the step that makes it `exited` or `stopped`, and
viewers get an `attention` message with the empty state: an ended session never
shows `needs_input`, and its activity log keeps the entries. A hosted session's
state is cleared when its host reports it ended; a host that disconnects leaves
it as it was, since the host may come back.

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

An agent's catalog entry can carry a `trustPrompt`, the words of its
workspace-trust question (RE2, at most 200 bytes, not matching an empty text).
A server session of that agent keeps the end of its screen's text, each escape
sequence and control character read as one space and every run of white space
as one, its last 1024 bytes, so that words a TUI draws with cursor moves
between them still read as a sentence. After 500 ms without output, output
that only sets the window title (OSC 0 or 2) not counting, the text is matched
against the pattern; a match marks the session `needs_input` with
`source:"trust"`, `kind:"prompt"` and the matched words as `message`, unless
it is `needs_input` already. Watching ends with the session's first
submission (below). Once a controller answers the question with Enter, only the
question drawn again raises it again. The built-ins carry the questions of
Claude Code 2.1.287 (`Is this a project you created or one you trust?`) and
Codex 0.159.0 (`Trust this folder?`). It exists for crew runs: a member's
prompt is never typed while it shows (see Crew runs).

Explicit updates: `POST /api/sessions/{id}/attention` with
`{state: "needs_input"|"working"|"done"|"clear", message?, kind?, options?, agentSession?, turn?}` and
`Authorization: Bearer <agent token>` (or the workbench token). Every session's
process receives `CONDUCTOR_SESSION_ID`, `CONDUCTOR_NOTIFY_URL` and
`CONDUCTOR_NOTIFY_TOKEN`, which `conductor notify` and `conductor crew` read,
`CONDUCTOR_BIN`, the absolute path of the conductor binary its hooks run,
which the Conductor skill runs its commands with, and, for a server session,
`CONDUCTOR_AGENT`, the id of its agent in the catalog, and `CONDUCTOR_SKILL`,
the server's copy of the skill (`<dataDir>/hooks/skills/conductor/SKILL.md`). Only Conductor sets them: `CONDUCTOR_*` from the
server's or the developer's environment and from an agent's `env` never reach
a session. The token is stored
hashed and only ever authorizes this route and `POST /api/sessions/{id}/events`
for this one session. Each report, the agent's or the admin's, spends a token
of the bucket the events of its session spend (see Events), on a server
session and on a hosted session, its host connected or not: with none left the
answer is `429 rate_limited` and the state does not change. For a hosted
session the state is also sent on to the host; while no host is connected
the report is applied on the server, and spends its token all the same. What
a session sees for itself (the bell, an OSC notification, the screen pattern)
spends no token, and a change of state a session applies always records its
`attention` activity entry: a state that shows has its entry (see Events).

`agentSession` is the agent's own session id as its hook payload names it, at
most 128 bytes (`400 invalid_request` past that), and `turn` says the payload
reports a turn: a prompt taken or a turn finished. They change no attention:
for a server session whose agent's `session` recipe takes ids from its hooks
(`idFrom: "hook"`, see HTTP API), an id that begins with a letter or a digit,
holds only letters, digits and `._:-`, and matches the recipe's `idPattern`
becomes the session's `agentSession.id` when it has none, and afterwards as the
recipe's `idPolicy` says: `latest` takes each new id, `lowest` keeps the
smallest (Codex's hidden title thread reports with a later id than the
conversation's). `turn` makes it `resumable`. Any other id is dropped without
an error, and a hosted session ignores both fields. `conductor notify` fills
them from Claude Code's `session_id` (with `turn` on `Stop` and
`UserPromptSubmit`), Codex's notify `thread-id` (every
`agent-turn-complete`; the turn of the hidden thread that names a Codex
conversation, whose first input begins `Generate a concise, single-line task
title`, is not reported at all), Copilot's `sessionId`, Cursor's
`conversation_id` and Antigravity's `conversationId`. A server that refuses
the two fields as unknown (an older one) gets the report again without them.

Session `Info` also carries `branch` (the git branch of the working
directory, read from `.git/HEAD` at launch; server and host alike) and, for
hosted sessions, `hostUser`. A server session carries `yolo: true` when its
launch applied its agent's yolo recipe (absent otherwise, an agent without a
recipe included), `agentSession{id, resumable, source}` once Conductor knows
the agent's own session (`source` `set` when Conductor chose the id at launch,
`hook` when the agent reported it, `resumed` when this session resumed it;
`resumable` once the agent has reported a turn, and always for a resumed one),
and `resumedFrom`, the session it resumed or relaunched.
`GET /api/sessions/{id}/links` adds `active` to every link: the number of
viewers currently attached through it.

When a controller's input clears `needs_input`, the session records
`lastAnswer{by, byName, at, message}` in its `Info` (the prompt that was
answered and who answered it) and an `input` activity entry.

What Conductor types itself (a crew member's prompt, a handoff or a
broadcast) and a reply sent with the `submit` control message are submitted as
a terminal sends a paste and then a key press, one submission at a time per
session. The text has each line break, carriage return and tab made a space,
every other control character (escape among them) and any invalid UTF-8
dropped, and its surrounding space trimmed; once cleaned it is at most 32756
bytes. It is written wrapped in `ESC[200~` and `ESC[201~` while the program
has bracketed paste on (the session follows `ESC[?2004h` and `ESC[?2004l` in
the output, as a terminal does, a sequence split between two reads included),
and as it is otherwise; then, 250 ms later, a carriage return is written on its
own. A TUI that reads a text and its carriage return in one read takes the
carriage return as part of a paste (Codex's paste-burst detector makes it a
newline; Claude Code collapses a read of over 800 bytes into a pasted block),
so written apart it is Enter. The carriage return answers the `needs_input`
that showed when the submission began, as typing does; when a new one comes up
during the 250 ms, it is left out, and the text, already written, waits in the
program's input and is never written again; in a session with a `trustPrompt`
the pause lasts until the screen watcher has had its 500 ms look at what was
drawn since the text (at most 2 s more), so a trust question drawn then holds
the carriage return back too. No lock of the session is held
across the pause, and a person's keystrokes (INPUT) are written as they come,
not held back. What Conductor submits always records one `input` entry as the
text is written, `byName` `crew` (for a broadcast, the admin's display name)
and the text as `message`, cut to the 500 bytes of Events (the terminal gets
all of it); a `submit` reply records the answer as a controller's input does.
A handoff or a broadcast is never submitted while the session is
`needs_input` (see Crew runs).

Session `Info` carries `crew{runId, crewId, member}` for a member of a crew
run (see Crew runs), and no `crew` otherwise.

Changes are pushed to attached clients as the `attention` control message and
to admins as `session` events on `GET /api/events` (Server-Sent Events over a
header-authenticated `fetch`: `snapshot` with the full list first, then
`session` per change, `removed{id}` when a session leaves the registry,
`activity` for every activity entry, see Events, and `run{id}` when a crew run
changes in a way no session change carries, or `run{id, removed: true}` when
the server forgets it, see Crew runs).

## Events

An event is an activity entry that an agent reports about its work, next to
the entries the session records itself (`attention`, `input`, `join`, `leave`,
`link`, `status`). All twelve types travel as `activity` control messages,
live and in the replay after `ready`. The six event types:

| `type` | Meaning | Fields |
|---|---|---|
| `progress` | a step finished | `message` says which |
| `artifact` | something was produced | `url` points at it, `message` describes it |
| `handoff` | work passed to another member | `to` names them, `message` says why; in a crew run it is typed into that member (see Crew runs) |
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
before it. That entry's `at` is the change's `since`, taken as the state is
set, so no one sees the state before the time its entry gives.

Reporting: `POST /api/sessions/{id}/events` with `Authorization: Bearer
<agent token>` (or the workbench token) and the body `{type, message?, url?, to?,
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
and nothing is sent or changed. Every attention word through the routes spends
one, its host connected or not; an event needs a connected host (below) and
spends nothing without one. The host's own reports spend none. The host
applies an attention word it is sent without a token of its own and records
its entry, as a server session does. An event the server sends to the host
(see Host control connection) is answered `202` once it is on its way. The
server cannot know whether the host's own bucket takes it, so an event the
host drops is dropped without a word to the caller; `409 host_disconnected`
says that no host is connected.

`conductor notify --event <type> [--message M] [--url U] [--to T] [--tool N]`
sends an event from inside a session: it turns the `CONDUCTOR_NOTIFY_URL` of
the session (`…/attention`) into `…/events`. `--event` cannot be combined with
`--state`, `--codex` or a `--<agent>-hook` flag, and `--url`, `--to` and
`--tool` are refused without it: nothing else carries them. A mistake exits 2,
or 1 in a hook's command line, where 2 would block the agent. An attention
report (the state flags, `--event` with an attention word, or a hook mapped to
one) that the server answers `429` is tried again after 0.1, 0.25, 0.5, 1 and
2 s, within the 5 s the command takes at most. An event is tried once.

Streaming: every entry a session records, whether an agent reported it or the
session made it, reaches admins as an `activity` event on `GET /api/events`,
next to `session` and `removed`:
`data: {"sessionId", "at", "type", "by"?, "byName"?, "message"?, "url"?, "to"?, "tool"?, "state"?}`,
where `state` is, for an attention entry, the state it records (`needs_input`,
`working` or `done`), absent for other entries and for an attention entry from
a host that does not send one. Entries arrive as the sessions record them, so
a few can be out of order; `at` says when each happened. The feed is live, not
a log: a client that has fallen behind (its queue is three quarters full)
misses entries and keeps its stream, with room left for the `session` and
`removed` events it cannot do without. The session's own `activity` control
messages replay the log.

Webhooks: the server also POSTs every entry whose event type a configured
webhook lists (README, Webhooks) to its URL, one entry a request, with the
body `{"sessionId", "session": {"id", "name", "agentId"}, "entry": {…}}`, the
entry as the stream above sends it less `sessionId` (beside it in the body)
and `state`, which the body does not carry: an attention entry's state reaches
a webhook that lists it as `X-Conductor-Event` (below). The headers are
`Content-Type: application/json`, `X-Conductor-Event` and, for a webhook with
a secret, `X-Conductor-Signature: sha256=<hex HMAC-SHA256 of the body>`.
`X-Conductor-Event` is the type the webhook listed: an entry type, or the
Events page's name for the entry, which wins when both are listed. An
attention entry is the attention state it records, which comes with the entry:
a server session hands it on as it records the entry (the state set with the
entry's stamp), and a hosted session's host sends it (`state` of the host's
`activity` message).
An entry that comes without one is the state it names as its message, if it
names one (as it does for a report without a message), and otherwise only an
`attention` entry. That happens for two reasons: the host is older and sends no
state, or it sent a state the server does not take (anything but `needs_input`,
`working` or `done`), which the server drops. A `status` entry
`exited (exit N)` with N other than 0 is `exit_nonzero`. Each webhook queues
at most 256 entries and drops its oldest; a delivery is tried once, for at
most 5 s, without following redirects.

## File reads

`file_get` resolves `path` against the session working directory (`~` expands to
the owner's home). After symlink resolution the target must stay inside the
working directory, and `.git/objects` is never readable. Server sessions also
refuse the server's data directory (`dataDir`, which holds agent secrets) with
everything in it, its config file (admin and host tokens) and its catalog file
(`catalogPath`), and the editor and backup copies beside those two: any file or
directory in the same directory (and in the target's directory, when either path
is a symbolic link) whose name contains theirs, ignoring case as a
case-insensitive file system does (`conductor.json.bak`, `conductor.json~`,
`.conductor.json.swp`, `#conductor.json#`), even inside the working directory.
The two files themselves are matched as files, so a link to them is refused as
well; the copies are matched by name alone. Every one of these rules answers
`denied`. A hosted session applies only the first two: the host serves
everything else under its working directory. Responses carry a JSON header
`{reqId, path, kind:"file"|"dir"|"error", size, truncated, binary, mime, exists, entries?, error?}`
followed by up to 1 MiB of bytes for text files. Files with a NUL byte in the
first 8 KiB are reported `binary` without bytes; images are sent as bytes. Up to
four requests may be in flight per client. `stat:true` returns only the header.

The `fileView` server setting decides who may read: `view` (both roles, the
default), `control` (controllers only) or `off`; `conductor host --file-view`
does the same for a hosted session.

## HTTP API

On a switchyard (`switchyard.enabled`, `conductor switchyard`) every route
that launches, edits or lists what the server launches, the sessions'
creation and resume, the catalog, the crews, the runs, the paths, the git
check, the integrations and an agent's self-service crew routes, answers
`403 {error: {code: "switchyard"}}`; the rest, hosted sessions, links,
`/api/join`, the events stream, health, reach and whoami, is as below. A
switchyard serves no workbench either: `GET /` is its landing page, every
workbench path is its 404 page (both server-rendered HTML), and the app is
served only under `/join/` and `/paste` and for its assets.

Every `/api/...` route, with the credential it needs. Admin means
`Authorization: Bearer <workbench token>`; a share token is also accepted where the
table says so. JSON request bodies are limited to 64 KiB (2 MiB on the crew
routes, 128 KiB when adding a member to a run) and unknown fields are rejected. Errors are
`{"error":{"code","message"}}`, with more fields where the table says so. The
WebSocket routes, `GET /ws/sessions/{id}` and `GET /ws/host`, are described
above. A run link's token is a share token on the session of every member of
its run, and on no other.

| Route | Auth | Purpose |
|---|---|---|
| `GET /api/health` | none | liveness: `ok`, `version`, `commit`, `sessions`, `instance` (a random id of this server process, which the reach self-check matches); `switchyard` (whether this server is one) and `relay` (whether it relays hosted sessions: always on a plain server) |
| `GET /api/reach` | admin | what the server knows about being reached from outside its network (`reach.mode`): `{mode, mapped, method?, publicUrl?, externalIp?, externalPort?, listenPort?, expiresAt?, renewedAt?, verified?, error?, tls?}`; `mapped` says a port on the public address reaches the server (`method` `upnp`, `pcp` or `nat-pmp`; `manual` with `mapped` false names a port the person forwarded); `publicUrl` is `https://<externalIp>[:port]`, the base share links take once `tls.ready`; `verified` is `ok` when the server reached itself through that URL and `unverified` when it did not, which is usual from inside the network and proves nothing; `error` is the last failure in words; `tls` is the TLS listener's certificate, absent without a TLS listener: `{mode (acme or files), identifiers?, challenge?, notAfter?, renewAt?, ready, lastError?, nextTry?}`, `ready` saying a certificate is served (the discovered address becomes a link's base only then) |
| `GET /.well-known/acme-challenge/{token}` | none | the key authorization of an open ACME `http-01` order (RFC 8555 §8.3), on the plain listener; `404` for any other token, a token over 128 bytes, or without a TLS listener |
| `GET /api/switchyard/status` | admin | on a switchyard only (`404` elsewhere): the landing page's operator figures, `{hosts, sessions, viewers, relayBytesHour, hostList: [{name, since, sessions, viewers}]}`, the live hosted sessions grouped by the publishing host, earliest first, and the bytes the relay carried in the last sixty minutes |
| `GET /api/whoami` | admin | OS user running the server (the default display name); `host` is the machine's name (`""` when it cannot tell) and `switchyard` whether this server launches nothing |
| `GET /api/catalog` | admin | `{agents, hidden, yoloDefault}`: the launchable agents, `env` values masked as `***`, and the IDs hidden from the catalog; each agent, here and in the replies of the catalog routes below, also carries `source` (`built-in`, `config` or `saved`) and, for a saved agent that replaces a built-in or configured one, `replaces` (where that one came from); `available`, whether `command[0]` resolves on the server (the check of `POST /api/catalog/check`; any program of that name on the server's `PATH` counts, and a relative `command[0]` with a path separator counts without a lookup, since it resolves in the session's directory, which the server's own cannot stand for; the answer is kept 30 s per program, and the programs with no fresh answer are looked up together, at most 8 lookups at a time across all requests; a lookup that does not answer within 2 s (a `PATH` entry on a mount that stalls), or before the client goes away, counts as available, so the launch of such an agent is allowed and its spawn fails if the program is missing, and the lookup's answer is kept when it lands); and `site` when the agent has a website (an `https` URL: a built-in's, or the saved agent's); each agent also carries, when it has them, its `yolo` recipe `{args?, env?}` (its `env` values shown as they are: switches, not secrets), its `trustPrompt` and its `session` recipe `{startArgs?, newId?, idFrom?, idPolicy?, resumeArgs?, idPattern?, resumeNeedsCwd?}` (limits below); `yoloDefault` is the server's `yolo` setting, which a launch or a crew without `yolo` follows; each agent whose adapter has an identity probe (the real agent's `--version` line, checked against what it prints: Claude Code and Codex verified live, the others from their docs until the nightly recipes job confirms them) and whose program the lookup found at an absolute path also carries `identity` `{ran, pending?, identified, impostor?, verified, name?, version?, output?, error?}`: the program on the server run with its version flag (argv only, a spartan environment, 3 s, 4 KiB), its answer kept 10 min; `identified` with `version` when it is the agent, `impostor` when it is a known other program of that name (the Go migrations tool called goose), `output` the first line it printed otherwise, `pending` when the probe is still under way (ask again), `verified` whether the adapter's expectation was checked against the real CLI (a verified probe that matches nothing means the program is not the agent; an unverified one leaves it unidentified and refuses nothing); an agent with `probe: false` is not probed |
| `POST /api/catalog` | admin | add an agent or replace the one with the same `id` (a built-in too); body is the agent, reply `{agent}`; `source`, `replaces` and `available` in the body are ignored; an `env` value of `***` (what `GET /api/catalog` shows) keeps the value the saved entry holds for that key; for an agent that replaces a built-in or configured one, a `***` value the saved entry does not hold is stored as `***` and means "the replaced agent's value", so a change in the config reaches it, and a value equal to the replaced agent's is stored as `***` too; a saved agent that leaves out `adapter`, `signal`, `site`, `yolo`, `trustPrompt` or `session` takes those of the agent it replaces, and `"yolo": {}` or `"session": {}` says it has none; `***` is rejected for a key neither has; `site`, when present, must be an `https://` URL with a host name, a valid port if it has one, no user info and no white space, at most 200 bytes, a rule that also holds for the config's agents and for `catalog.json`; `400 invalid_agent` carries the validation message, an unknown `adapter` or a bad `site` included (the adapter check also runs for the config's agents and for `catalog.json` at startup); `503 store_unavailable` when there is no data directory |
| `DELETE /api/catalog/{id}` | admin | remove the saved override with that `id`, which restores a built-in it replaced; an agent with no override is hidden instead; `204`, `404` when unknown |
| `POST /api/catalog/{id}/unhide` | admin | take a hidden `id` off the hidden list, which brings back the agent it hid as it was; reply `{agent}`, or `{}` when no agent has that `id` any more; `404` when the `id` is not hidden |
| `POST /api/catalog/check` | admin | body `{command, adapter?, env?}`, reply `{found, path?, unknown?, identity?}` (`identity` as `GET /api/catalog` carries it, when `adapter` names an adapter with a probe and the program was found: the probe runs now on the program found, with `env` as the agent's variables; `400 invalid_request` for an unknown adapter): whether `command[0]` resolves on the server (`exec.LookPath`), asked now and kept 30 s for the `available` of `GET /api/catalog`; nothing is run, and a missing program is `found:false`, not an error; `unknown` (with `found:false`) says the server did not judge the program, which `available` counts as installed: `relative` for a relative `command[0]` with a path separator, resolved at launch in the session's directory and not looked up here, and `timeout` when the lookup does not answer within 2 s (its answer is kept when it lands) |
| `GET /api/paths` | admin | `prefix` (≤ 4096 bytes, no NUL; a relative one resolves against the server's own working directory, as a session's `cwd` does) and `limit` (a positive integer, default 50, at most 50: more counts as 50) in the query; reply `{dir, entries, truncated}`: `dir` is the longest leading part of `prefix` that is an existing directory under an allowed root (symbolic links resolved, as a session's `cwd` is checked; the server's default working directory for an empty `prefix`), `entries` its child directories whose names start with the path element typed after `dir` (all of them when `prefix` names a directory), in name order, at most `limit` of the first 2000 entries the directory gives, hidden ones only when that element starts with a dot (a lone `.` after the last separator included), a symbolic link only when it leads to a directory under an allowed root, each `{name, path, git: {repo, commits}}` (`repo`: in a git working tree; `commits`: its `HEAD` is a commit; a child with no `.git` of its own carries its parent's marks, a symbolic link its target's; neither without `git` on the server; git is asked at most once per entry, one call at a time), `truncated` when more match than `limit`, when the directory has more than 2000 entries (the rest are not read), when the listing stopped at a symbolic link (the one after 50 that led nowhere usable, or any once the deadline has passed), or when the deadline passed before every entry was marked; the 5 s deadline covers the whole request, and when it passes after the directory is found the listing keeps its entries, the git marks not yet asked for left false and `truncated` set, which is not an error; a directory the server cannot read lists nothing (`200`, no entries); the listing and the git check below refuse a chain of symbolic links longer than the kernel follows (about 40), which a launch's own resolution of a `cwd` would still accept; `400 invalid_cwd` when no part of `prefix` is such a directory, its message naming the allowed roots (`no directory in the prefix is under the allowed roots: <root>, <root>`), which the picker shows as it is; `400 invalid_request` for a bad `prefix` or `limit`; `500 list_failed` when the deadline passes before the directory is found (a prefix of very many elements through symbolic links); the path asked about is logged at debug level only |
| `GET /api/git/check` | admin | `cwd` in the query (≤ 4096 bytes, no NUL; the server's default working directory when empty; resolved as the listing resolves it); reply `{inRepo, toplevel?, hasCommit, message}` by the rules a launch with `isolation: worktree` applies (`git -C <cwd> rev-parse`, under the allowed roots): `inRepo` when `cwd` is in a git working tree, `toplevel` its top, left out when it is not under an allowed root (a root may lie inside a repository, and a repository's `core.worktree` can name any directory), `hasCommit` when `HEAD` is a commit, `message` the words the launch's refusal would use, or that worktrees can be made: that `cwd` is in no repository, or in one with no commit, or that `git` is not installed (then `inRepo:false`), or, for a repository git refuses (owned by another user, unreadable), `git cannot use the working directory's repository:` and git's first line of stderr, which may name a configuration file or a repository path outside the roots; in a repository with a commit, a `.conductor` or `.conductor/worktrees` in `cwd` that is a symbolic link, which the launch refuses first, answers `inRepo:true, hasCommit:true` with that refusal in `message`, so `message` is the verdict, not the two booleans; a preview: the launch's own refusal stays the authority (`409 not_a_repo`, or `400 invalid_crew` for the symbolic link); `400 invalid_cwd` as for a session; `500 git_failed` when git does not answer within 5 s (another failure to run it answers `200` with git's message) |
| `GET /api/crews` | admin | `{crews, total}`: a page of the saved crews' summaries ordered by name (ignoring case), then id, each `{id, name, cwd, where, isolation, members: [{name, agentId, start}], updatedAt}`; `?offset=` from 0 (default 0), `?limit=` 1 to 500 (default 100), `400 invalid_request` otherwise; `total` counts every crew; `{crews: [], total: 0}` without a data directory |
| `GET /api/crews/{id}` | admin | one crew in full, `{crew}`, shaped as before: `{id, name, goal, cwd, where, isolation, openAfterLaunch, viewLinkTtlSeconds?, yolo?, members, createdAt, updatedAt}` (`yolo` absent: the server's default; `true` or `false`: the crew's own choice), a member being `{name, agentId, prompt, args?, start: {when, member?}}`; `404` when unknown; `409 crew_unreadable` when its file cannot be used, the message saying why; `503 store_unavailable` |
| `POST /api/crews` | admin | create a crew: the body is a crew without `id`, `createdAt` and `updatedAt`, which the server sets and rejects like any unknown field; the `id` comes from the name (lower case, every other run of characters a `-`, at most 40 characters, `crew` when nothing is left), then `-2`, `-3`… when taken; reply `201 {crew}`; `400 invalid_crew` carries the validation message, an agent the catalog does not have included; `503 store_unavailable` when there is no data directory |
| `POST /api/crews/examples` | admin | seed the example crews (`example-todo-app`, `example-test-fixer`, `example-docs-writer`, `example-dependency-upgrade`), as `conductor serve --examples` (or `CONDUCTOR_EXAMPLES` set to `1` or `true`) does at startup: each is saved unless an entry named `<id>.json` exists in `crews/`, usable or not (a symbolic link included), which is left alone whatever it holds; reply `200 {added, skipped}`, the ids each way in that order, each a list (`[]` when none); their `cwd` is the server's default working directory, their members use the `claude` and `codex` built-ins (not checked against the catalog: the launch checks), `where` is `server` and `isolation` `worktree`; each member that starts `after` another has a role prompt that begins by merging that member's branch, `crew/$CONDUCTOR_RUN/<member>`; `503 store_unavailable` without a data directory; `500 store_failed` when the directory cannot be read or a crew cannot be saved (the ones saved before it stay) |
| `PUT /api/crews/{id}` | admin | replace a crew's fields with the body, shaped as for create; `id` and `createdAt` never change, `updatedAt` is now; reply `{crew}`; `400 invalid_crew` as for create; `404` when unknown; `409 crew_unreadable` when its file cannot be used (it is not replaced) |
| `DELETE /api/crews/{id}` | admin | delete a crew; `204`, `404` when unknown |
| `POST /api/crews/{id}/duplicate` | admin | save a copy of a crew as `<id>-copy` (then `<id>-copy-2`…) named `<name> copy`, with new times; reply `201 {crew}`; `400 invalid_crew` when one of its agents is no longer in the catalog or the copy would be over 1 MiB; `404` when unknown; `409 crew_unreadable` when its file cannot be used |
| `POST /api/crews/{id}/launch` | admin | launch a crew as a run (see Crew runs); reply `201 {run}` once the session of every member that starts immediately exists, each member `starting` until its prompt is typed; the run's `yolo` is the crew's, else the server's, fixed for every member of the run, one started or added later included; a crew with `viewLinkTtlSeconds` also gets a run link with role `view`, label `launch` and that lifetime, created as `POST /api/runs/{run}/links` would (noted in the run log), and the reply is `201 {run, viewLink: {link, token, url}}`: the token is in this reply only (a link the store refuses leaves the launch standing, without `viewLink`, with an `error` entry in the run log); `400 invalid_crew` for a crew with no members, one that runs on a host, an agent the catalog does not have, an agent whose program is not installed on the server (`command[0]` does not resolve, by the check of `GET /api/catalog`'s `available`, so a lookup that does not answer within 2 s does not refuse; the message names the member and the agent), arguments to an agent that takes none, or with `isolation: worktree` a `.conductor` or `.conductor/worktrees` in `cwd` that is a symbolic link, or a symbolic link on the way to a member's directory in its worktree; `400 invalid_cwd` as for a session; `409 not_a_repo` with `isolation: worktree` when `cwd` is in no git working tree (`git -C <cwd> rev-parse --show-toplevel` fails), or in one whose `HEAD` is no commit, each with its own message, and a `rev-parse` failure other than 'not a git repository' (a repository owned by another user, or one that cannot be read) says what git said; `500 launch_failed` with `git is not installed on the server` with `isolation: worktree` when `git` is not on the server's `PATH`, before anything is made; `409 run_stopped` when the run is stopped while its sessions start (the run stays, stopped); a member whose session cannot be created answers as `POST /api/sessions` would, naming the member; `500 launch_failed` otherwise; `404` when unknown; `409 crew_unreadable` when its file cannot be used; `503 store_unavailable` without a data directory; `400 invalid_crew` also for a member whose agent's program the identity probe found to be another one (a known impostor, or a verified probe that matched nothing), the message naming the member, the agent, what the program is not and what it printed; a pending or failed probe refuses nothing |
| `GET /api/crews/{id}/runs` | admin | `{runs, live}`: the crew's runs, newest first, each as `GET /api/runs/{run}` answers it (without diffs): the `live` first ones the server holds, then the records of runs that ended (stopped or finished) and were saved under `runs/<id>.json` in the data directory, at most 500 kept (the oldest by start dropped), never a terminal's contents; a record is written each time a run ends (a reopened run again when it ends again) and read back after a restart; `404 not_found` for a crew neither saved nor recorded |
| `POST /api/sessions/{id}/paste` | admin | answer a viewer's paste invite for a server session: `{offer, role, label?}`, `offer` a `cpi1.` blob (the viewer's SDP with every candidate gathered, deflated and base64url-encoded, ≤ 64 KiB), reply `201 {answer, role}` with the session's blob of the same form, which the viewer pastes back; the data channel then runs with no server between the two, as a host's does (hello over it, welcome, scrollback, input by role); at most 16 such peers a session, idle ones dropped after two minutes; `400 invalid_offer` for a blob that is not an offer, `400 hosted_session` for a hosted session (shared from its own machine), `409 session_ended` |
| `GET /api/ice` | none (rate-limited) | `{stun}`: the configured ICE servers' `stun:` URLs, for a viewer gathering a paste invite's offer before it has any session; a TURN server's credentials never leave the server |
| `GET /api/runs` | admin | `{runs}`: the runs in the server's memory, newest first, each with its `state` and `needsInput` (see Crew runs) |
| `GET /api/runs/{run}` | admin | `{run}` with each worktree member's `diff{added, removed}` and the run's `log`, whose entries Crew runs lists; `404` when unknown |
| `POST /api/runs/{run}/members` | admin | add a member mid-run: body a crew member; reply `201 {run}`, once the session of a member that starts immediately exists; `400 invalid_crew` for an invalid member, a name the run has (`the name is used twice`), a prompt over 32756 bytes with the run's goal in it, an `after` naming no member of the run, a 13th member or an agent as at launch; a member whose session cannot be created answers as at launch (`409 not_a_repo`, `500 launch_failed`), and stays in the run, `ended` with its `error`, its name taken: adding it again under that name is `400 invalid_crew`; `409 run_stopped`; `404` when unknown |
| `POST /api/runs/{run}/members/{name}/start` | admin | start a pending member by hand, whatever its start condition; reply `{run}` once its session exists, the member `starting` until its prompt is typed; a session that cannot be created answers as at launch (`409 not_a_repo`, `500 launch_failed`), the member `ended` with its `error`; `409 member_started`, `409 run_stopped`; `404` for an unknown run or member |
| `POST /api/runs/{run}/members/{name}/resume` | admin | resume an ended member of the run (no body), in its working directory, its worktree and branch kept: a session tagged with the run that resumes the member's last `agentSession` with its agent's `session` recipe when the agent has one and the session was `resumable` (`resumed: true`; no prompt is typed, its conversation has it), and otherwise a fresh one whose role prompt is typed again once it is ready (`resumed: false`, with a `notice`); the member is `running` with the new `sessionId` (or `starting` until that prompt), noted in the run log; reply `201 {session, resumed, notice?}`, the new session's `Info` with `resumedFrom`; `404` for an unknown run or member; `409 still_running` for a member that is pending, starting or running; `409 run_stopped` only while a stop is under way or was cut short: an ended member of a run whose stop completed is resumed in place, which reopens the run (`stoppedAt` cleared, the log noting `reopened: <member> resumes`; the other ended members stay ended until resumed one by one, and a later stop stops it again); `409 already_resumed` when a running session holds that agent session; `400 invalid_agent` when the member's agent is no longer in the catalog; a session that cannot be created answers as at launch, the member `ended` with its `error` |
| `POST /api/runs/{run}/resume` | admin | start a new run of a stopped (or finished) run's crew in which every member whose last `agentSession` is `resumable` continues its conversation in its kept worktree and branch, without a prompt (its next done starts the members after it), and the others start afresh under their start rules; reply `201 {run}`, the new run with `resumedFrom`, the old run from then on carrying `resumedBy` and staying stopped; `409 run_running` while the run goes; `409 run_stopped` when its stop was cut short; `404` when unknown; a member whose session cannot be made ends with its `error`, as at launch |
| `POST /api/runs/{run}/stop` | admin | stop every member's session; reply `{run}` with `stoppedAt`; the worktrees stay; stopping again changes nothing; a session that does not stop cleanly is logged by the server and the run is stopped all the same; `404` when unknown |
| `POST /api/runs/{run}/broadcast` | admin | type a line into members of the run: body `{text, members?, byName?}`, `members` empty or missing for every member; reply `{sent, skipped}`, `sent` the names typed into and `skipped` entries `{member, reason}` with `reason` `needs_input`, `not_running`, `unknown` or `no_enter` (typed, its carriage return left out because a question came up; see Crew runs); `400 invalid_request` for a text with nothing left, or over 4096 bytes, once made one line; `404` when unknown |
| `GET /api/runs/{run}/links` | admin | `{links}`: the run's share links, each with `runId` and `active`, the viewers attached through it to its members' sessions; `404` when unknown |
| `POST /api/runs/{run}/links` | admin | create a run link: `{role, label?, ttlSeconds?}`, reply `201 {link, token, url}` as for a session link (its `url` on the same base); it grants its role on the session of every member of the run, a member added later included (see Crew runs); `409 too_many_links` past 100 links for the run; `404` when unknown |
| `DELETE /api/runs/{run}/links/{linkId}` | admin | revoke a run link, which closes every viewer attached through it (`4403`); `204`, `404` when the link is not the run's |
| `GET /api/integrations` | admin | `{integrations, host, webhooks}`: every hook adapter in a stable order, each `{id, name, events, launchInjection, installable, installsSkill, installed, where, snippet, experimental}`, the server's host name (`""` when it cannot tell), and the configured webhooks, each `{url, events}` with the URL as `scheme://host[:port]/path` (no user info, query or fragment) and never its secret; `installable` is true for an adapter with a file to install into (`POST …/install` can do something); `installsSkill` is true for an agent whose install also brings the Conductor skill; `installed` and `where` check the home of the user running the server, writing nothing, and are `false` and `""` for an adapter with no file to install |
| `POST /api/integrations/{id}/install` | admin | install the adapter's hooks (and, for Claude Code, Codex, pi and Goose, the Conductor skill) into the agent's own config in the server user's home, and nowhere else; reply `{changed}`, the files written, `[]` when all was in place; `400 no_file_route` when there is no file to install into or a step is left to do by hand, the error carrying `changed` (files already written) and `snippet`, which is present only when what is left by hand is the agent's hooks, not a skill file (the message says what to do about a `SKILL.md` of the user's own); `500 install_failed` with `changed`; `404` for an unknown `id` |
| `GET /api/sessions` | admin | list sessions |
| `POST /api/sessions` | admin | launch a server session: `{agentId, name?, cwd?, args?, cols?, rows?, yolo?}`, reply `201` with the session `Info`; `yolo` overrides the server's `yolo` for this launch: with it on, the agent's yolo recipe is applied (its `args` after the command and `args`, its `env` over the agent's own, through the filtered environment; for Codex also `-c projects={"<dir>"={trust_level="trusted"}}` naming the launch directory's repository and, for a worktree, its main repository), and `Info.yolo` says so; an agent with no recipe is launched without one and its activity says so; an agent with a `session` recipe and `startArgs` gets a fresh id there, right after its command (`Info.agentSession`, `source:"set"`) |
| `GET /api/sessions/{id}` | admin or share token | one session with the caller's `role`; admins also get its `links` |
| `DELETE /api/sessions/{id}` | admin | stop a running session; on an ended session, remove it from the list |
| `POST /api/sessions/{id}/resume` | admin | resume an ended server session (no body), while it is listed: a new session with the same agent, name, working directory, arguments, environment and yolo choice, launched with its agent's `resumeArgs` for the stored `agentSession`, right after the command, when the agent has a `session` recipe and the session was `resumable` (`resumed: true`), and plainly otherwise, with a `notice`: `<agent> has no session recipe: started anew`, or `nothing to resume yet (the agent had no turn): started anew`; reply `201 {session, resumed, notice?}`, the new session's `Info` with `resumedFrom`; a crew member's session is resumed as its member, as `POST /api/runs/{run}/members/{name}/resume` does; `404` when unknown; `400 hosted_session` for a hosted session; `409 still_running`; `409 already_resumed` when a running session holds that agent session; `400 invalid_agent` when its agent is no longer in the catalog; `400 invalid_cwd` when its working directory no longer passes the check; otherwise as `POST /api/sessions` answers |
| `POST /api/sessions/{id}/crew` | the session's agent token, or admin | an agent forms a crew around its own session (`agents.selfService`, on by default; `403 self_service_off`): body `{name, goal, members, self, isolation?, open?}` (the crew's `cwd`, `where` and `yolo` are the session's own, never in the body; `self` names the member this session becomes, whose `agentId` is the session's agent or empty, meaning that); the crew is saved, its run launched with the session adopted as `self` (`running`, its prompt its own: the members after it start on its next done, handoffs reach it) and the others started as at launch; reply `201 {run, crew, member, url}`, `url` the run's page; the session is tagged with the run and records `formed crew <name>` (a `link` entry with `(open)` when asked, which the workbench offers as a toast); at most 2 per session per hour (`429`); `409 in_a_run` while the session is a member of a run, `409 session_ended`, `400 hosted_session`, `400 invalid_crew` as a launch answers (an impostor among the members included) |
| `POST /api/sessions/{id}/run/members` | the session's agent token, or admin | add a member (one `crew.Member`) to the session's own run, as `POST /api/runs/{run}/members` does; `201 {run}`; `409 no_run` |
| `GET /api/sessions/{id}/run` | the session's agent token, or admin | the session's run with the last 50 log entries, and the session's member name: `{run, member}`; `404 no_run` |
| `POST /api/sessions/{id}/links/agent` | the session's agent token, or admin | a view-only link to the session for an agent to hand out (a PR, a message): body `{ttlSeconds?, label?}`, the TTL two hours unless given, a day at most (`400`); reply as `POST /api/sessions/{id}/links`; at most 5 per session per day (`429`); recorded in the session's activity |
| `GET /api/sessions/{id}/links` | admin | share links of a session, each with `active` viewers; for a session published to a rendezvous, the links minted there too, with `remote: true` and no `active` (their viewers are counted there); a rendezvous restart that re-registered the session drops those records |
| `POST /api/sessions/{id}/links` | admin | create a share link: `{role, label?, ttlSeconds?}`, reply `201 {link, token, url, invite}` (`invite` is the same link as `conductor://<host>/join/<token>`, which the desktop app opens itself); for a session published to a rendezvous the link is minted there over the host connection and the reply is `201 {link{id, role, label, expiresAt, sessionId, remote: true}, url, invite, remote: true}` with the rendezvous's URL and no `token` (`502 rendezvous_unavailable` when it does not answer within 15 s); `url` is `<base>/join/<token>`, where the base is `publicUrl` when it names another machine, and otherwise (unset, or localhost, as the default is) the address the request came through: `X-Forwarded-Proto` and `X-Forwarded-Host` when a reverse proxy sets them, else the request's scheme and `Host` (a malformed host falls back to `publicUrl`) |
| `DELETE /api/sessions/{id}/links/{linkId}` | admin | revoke a share link; `204`; revoking a revoked link answers `204` and records nothing ; a link minted at the rendezvous is revoked there over the host connection (`link_revoke`), `204` also when the rendezvous no longer holds it, `502 rendezvous_unavailable` when it does not answer|
| `GET /api/sessions/{id}/files` | admin or share token | read a file of a server session (`path`, `stat`, `raw` query), see File reads |
| `POST /api/sessions/{id}/attention` | agent token or admin | report an attention state, and with it the agent's own session id (`agentSession`, at most 128 bytes) and whether the report is of a turn (`turn`), see Attention |
| `POST /api/sessions/{id}/events` | agent token or admin | report an event or an attention word, reply `202 {accepted}`, see Events |
| `GET /api/events` | admin | Server-Sent Events of session changes (`snapshot`, `session`, `removed`), of activity entries (`activity`, with `state`, the state an attention entry records, absent for other entries and for an attention entry from a host that does not send one), and of run changes no session event carries (`run`: `{id}`, read the run again with `GET /api/runs/{run}`, or `{id, removed: true}` when the server forgot it; at most 128 bytes; sent for a member reserved, started, prompted, failed or ended early, every entry of the run's log, and a stop; a client that cannot keep up is dropped as for a session change, and reads every run again on its next `snapshot`), see Attention and Events |
| `GET /api/join/{token}` | share token in the path | resolve a share link for the join page (rate limited): `{session, role, label}` for a session link; `{run: {id, name, members}, role, label}` for a run link, each with `switchyard` (whether this server is one, which the join page names), each member `{name, sessionId?, agentId, status}` in the run's order, with `sessionId` only while its session runs (`agentId` and `status` then the session's) and otherwise its state in the run, `pending`, `starting` or `ended`; `404` with `invalid_link`, `revoked`, `expired`, `session_gone` or `run_gone` |

The catalog routes persist their changes as `catalog.json` in the data
directory (`dataDir`): `{"agents": [...], "hidden": [...]}`. At startup that
overlay is applied over the configured catalog, and the server refuses to start
when the file cannot be parsed or an agent in it is invalid. An env value of an
entry that equals the replaced agent's is saved back as `***` at startup (once;
a failure to save stops the server), as a save stores it. An entry of
`catalog.json` with the ID of a built-in or configured agent replaces it but
inherits what it leaves out (`adapter`, `signal`, `site`, `yolo`,
`trustPrompt`, `session`, `***` env values and env values equal to the
replaced agent's; `"yolo": {}` and `"session": {}` say it has none); env keys it does not list are not
inherited, so an API client that omits `env` drops every value of the replaced
agent, and a key the config adds later does not reach it. An agent in the
config file that replaces a built-in replaces it whole. Agents are
held to these limits everywhere: `id` matches `^[a-z0-9-]{1,32}$`, `name` at
most 60 characters, `description` 200, `command` 1 to 32 elements of at most
4096 bytes each, `env` 32 keys, `envPassthrough` 32 names, a signal `pattern`
200 bytes that does not match an empty line, `cwd` at most 4096 bytes without
NUL, `icon` matching `^[a-z0-9][a-z0-9:-]{0,63}$`, `site` an `https://` URL
with a host name, a valid port if any and no user info or white space, at most
200 bytes, `adapter` matching `^[a-z0-9-]{1,32}$` and naming an adapter
Conductor has, an `env` key or `envPassthrough` name at most 128 bytes and an
`env` value at most 16384 bytes. A `yolo` recipe: at most 16 `args` of 1 to
4096 bytes without NUL, at most 16 `env` variables named like an
`envPassthrough` name and never `CONDUCTOR_*`, each value at most 4096 bytes
without NUL. A `trustPrompt`: as a signal `pattern`. A `session` recipe: at
most 16 `startArgs` and 16 `resumeArgs` of 1 to 4096 bytes without NUL, `{id}`
only as a whole argument, once in `resumeArgs` and once in `startArgs` when it
has any; `newId` `uuid` (the default) or `name` (`cdr-` and a UUID); `idFrom`
`hook` or absent; `idPolicy` `latest` (the default) or `lowest`; `startArgs`
or `idFrom: "hook"`, so that the id can be known; `idPattern` anchored with `^`
and `$`, at most 200 bytes, matching neither an empty id nor one that begins
with a dash; `resumeNeedsCwd` documents an agent that resumes only in the
directory its session ran in (a resume always runs there). The recipe's
arguments go right after the agent's command, before the launch's `args`, so
that Codex resumes with its subcommand. The empty recipe `{}` passes and means
none.

The crew routes keep each crew in a file of its own, `crews/<id>.json` in the
data directory, written whole (a temp file renamed into place). The list is
read from that directory, so a file added, changed or removed by hand shows at
the next listing. A file that cannot be used, a symbolic link included (it is
not followed), is named in the server's log at startup and left out of the
list; it is never overwritten (a new crew of the same name takes the next id),
and `DELETE` removes it. A file that cannot be read for now (its mode, say) is
left out of that listing and read again at the next. A `crews.json` from an
earlier version is split into these files at the first start and renamed
`crews.json.migrated`, which keeps every crew; the move never overwrites a crew
file, and a crew whose file exists with something else in it is not moved,
which the log says naming both files.
A `crews.json` that cannot be parsed stops startup, as before. So does one that
holds an invalid crew or an `id` twice; nothing is moved then. Without a data
directory the listing is empty and every other crew route answers
`503 store_unavailable`. A failure of the store is `500 store_failed`: "could
not save the crews" for a write, which changes nothing, and "could not read the
crews" for a read. `openAfterLaunch` is for the workbench, which opens the crew
view after a launch when it is set; the server stores it and does nothing else
with it.
Crews are held to these limits: `id` matches `^[a-z0-9][a-z0-9-]{0,63}$`; `name`
not blank, at most 60 characters and without control characters (surrounding
space is trimmed), `goal` at most 2000 characters, `cwd` at most 4096 bytes;
`where` is `server` or `host` and `isolation` is `none` or `worktree`;
`viewLinkTtlSeconds` 0 to 31536000 (a year); at most 12 members, each with a
`name` matching `^[a-z0-9][a-z0-9._-]{0,39}$`, unique in its crew and one git
takes for a branch (no `..`, no `.` or `.lock` at the end), an `agentId`
matching `^[a-z0-9-]{1,32}$` that the catalog has when the crew is saved or
copied, a `prompt` of at most 4000 characters that, with the goal in place of
`$GOAL` and `${GOAL}`, is at most 32756 bytes (it is submitted as one line,
which changes no length, inside the 12 bytes of the bracketed-paste markers,
and a session takes at most 32 KiB at once), and at most 32 `args` of at most
4096 bytes, 8 KiB in all; `yolo`, when present, is `true` or `false`;
`start.when` is `immediately`, `after` or
`manual`, `start.member`, set with `after` alone, names another member of the crew, and
following `start.member` from member to member never goes round in a cycle.
A crew's file, as the server writes it with its `id` and times, is at most
1 MiB; a create or update body may be up to 2 MiB, so a crew sent indented
still fits. There is no limit on the number of crews. Create and update check
the body first: a crew that breaks one of these rules is refused with
`400 invalid_crew` for that rule, ahead of an agent the catalog does not have
and an unknown `id` (`404`); an agent the catalog does not have is `400 invalid_crew` whatever else
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
repository, and starts in the directory of that worktree which `cwd` is of the
repository (`git rev-parse --show-prefix`), made when no commit has a file
there. The first worktree also adds a `.conductor/` line, once, to the file
`git -C <cwd> rev-parse --git-path info/exclude` names (making `info/` when it
is missing), so that the worktrees stay out of the main checkout's `git status`. A `.conductor`
or `.conductor/worktrees` in `cwd` that is a symbolic link is refused before git
runs, and so is a member whose directory in its worktree lies through a
symbolic link that a commit holds, before anything is made through it.
Conductor never deletes a worktree or a branch.

A launch, a start or an added member answers once the member's session exists;
the member is `starting` until its prompt is typed, then `running`. A member is
ready for its prompt when its agent reports `needs_input` or `done`, or after
one second without output that follows its first output and at least two seconds
after its start, checked every 250 ms. Output that only sets the window title
(OSC 0 or 2: a spinner in the title) is not output for this. A program that
has turned bracketed paste on and then off again is between screens (Claude
Code does so while it loads, and what is typed then loses its Enter) and is not
ready until it turns it on again or reports `needs_input` or `done`. After 60
seconds the prompt is typed anyway and the run log says so. A trust question on
the member's screen (`source:"trust"`, see Attention) holds the prompt
instead: the member's session shows `needs_input` with the question's words,
the run log says once that the member asks it, the 60 seconds do not run, and
once a person has answered it with Enter the wait for readiness starts over.
The prompt, `$GOAL` and `${GOAL}` replaced by the goal, is one line, as a
handoff and a broadcast are (each line break, carriage return and tab, in the
prompt or in the goal, becomes a space; the crew keeps the prompt as written),
submitted as Attention describes: the text, as a bracketed paste while the
program has that mode on, then a carriage return 250 ms later. For an agent
whose hooks report `working` as it takes a prompt (Claude Code with the `hook`
signal), a prompt not taken within 3 seconds of its carriage return (no change
of attention, other than typing's, stamped after it) gets one more carriage
return, unless the session is `needs_input` by then; the run log says so. The
member is prompted as its carriage return is written: a `done` whose
`attention` entry is stamped after that counts for the members after it, and
one stamped before it never does, however late the entry reaches the run: it
is the idle state the prompt has not reached. When a question comes up during
the 250 ms (a new `needs_input`), the carriage return is left out: the text
waits in the agent's input, is never typed again, the member is `running` and
prompted as of then, and the run log says to press Enter in its terminal once
the question is answered. A member whose process ends first gets no prompt: it
is `ended` with its exit in `error`, and the run goes on; a member whose prompt
cannot be typed ends alone, its session stopped, the reason in `error`. When a
member's session cannot be created at launch, the ones started are stopped and
no run is kept; when the run is stopped while its sessions start, it stays,
stopped. A member whose start fails in a run that goes on stays in the run,
`ended`, and keeps its name: `POST /api/runs/{run}/members`
with that name is refused as a name used twice. A run is `{id, crewId, name,
goal, cwd, isolation, startedAt, stoppedAt?, members, log, state, needsInput,
yolo}`, a member `{name, agentId, start, sessionId?, branch?, worktree?,
status, startedAt?, endedAt?, error?, needsInput?, agentSession?, diff?}` with
`status` `pending`, `starting`, `running` or `ended`. A run's `state` is
derived as it is read: `stopped` once stopped; `finished` when every member
has ended and none is pending; `needs_input` while the session of a starting
or running member is `needs_input` (`needsInput` counts them, and each such
member has `needsInput: true`); `running` otherwise (a member's `done` keeps it
running: a done agent is idle, not gone). `yolo` is the run's yolo choice,
fixed at launch. A member's `agentSession` is the one its latest session last
showed (see Attention), kept after that session leaves the server, for
`POST /api/runs/{run}/members/{name}/resume`;
`diff` counts the lines of tracked files the member's worktree adds and removes
against the commit it began from, committed or not (`git diff --shortstat
<base> --`, read at most every 10 s, the last value kept when a read fails);
untracked files do not count, and a branch merged into the member's (main, say)
counts with it. `log` is the run's own, at most 200 activity entries (below);
the `status` entry of a delivered handoff (`handoff delivered from a to b`)
carries `byName` (who sent it) and `to` (who got it), which the crew graph
draws from.
Runs live in memory: a server restart forgets them, and past 100 runs a launch
forgets the oldest with nothing running.

A `handoff` event a member reports (see Events) goes to the member of its run
that `to` names. When that member is `running` and not `needs_input`,
Conductor submits `Handoff from <member>: <message>` into its session (the
text, then its carriage return 250 ms later, as a prompt is), recorded as an
`input` entry by `crew`; the line breaks and tabs the message keeps become
spaces, so a handoff is one line.
While the member waits for input, or is still `starting`, the handoff waits
for it: at most 10 wait per member, and another drops the oldest. They are
typed in order, one line each, once the member runs and no longer waits for
input, looked at again whenever its session records an entry or changes (a
prompt cleared records no entry, only a change) and when the member starts
running. The session looks at `needs_input` in the same step in which it
finds the prompt an input would answer, so a handoff never answers a prompt:
one the session shows when a handoff is about to be typed sends the handoff
back to the head of the queue, and one raised in the 250 ms before its
carriage return leaves the handoff typed without it, noted in the run log and
never typed again.
A handoff to a name no member of the run has, to the member that reports it,
or to a member with no session yet or whose session has ended, is only noted
in the run log, and so is each waiting handoff dropped when its member's
session ends or the run stops. A handoff from a session outside any run, or
reported as its run stops, is an event like any other and nothing more.

`POST /api/runs/{run}/broadcast` types a line into the members of the run
that `members` names, a name given twice once, or into every member when it
is empty. The text has its line breaks and tabs made spaces, as a handoff's
message has, its other control characters dropped and surrounding space
trimmed; what is left must not be empty or over 4096 bytes (`400
invalid_request`). It is submitted to the members together, each as a prompt
is (the text, then its carriage return 250 ms later), and the submissions go on
for up to 15 s when the client goes away, so that none is cut between its text
and its carriage return; each is recorded as an
`input` entry by `byName`, cleaned as a viewer's display name is (`guest` when
empty; the web client sends the display name, or the server's user from
`GET /api/whoami` when none is set). A member is skipped, and listed in `skipped` with the reason, when its
session waits on a prompt (`needs_input`: as for a handoff, the session looks
at its state in the same step in which it finds the prompt the text would
answer, so a broadcast never answers a prompt), when it is not `running`
(`not_running`: `pending`, `starting` with its prompt not typed yet, or
`ended`), or when no member of the run has the name (`unknown`). A write to a
member's process that fails before the text is written, as it does when the
process has just exited, is reported as `not_running` too; the server logs it,
without the text. A member where a question comes up in the 250 ms before the
carriage return, or whose carriage return cannot be written, gets the text
without it (`no_enter`). `sent` and `skipped` keep the order of `members`, or
of the run.

A run link (`POST /api/runs/{run}/links`) grants its role on the session of
every member of the run, one added later included, and on no other session:
the viewer WebSocket, `GET /api/sessions/{id}` and the file route take its
token as they take a session link's for its session, and `GET
/api/join/{token}` answers the run and its members. A run link is listed,
capped at 100 and revoked through its run alone; revoking it closes the
viewers attached through it on every session (`4403`). Creating and revoking
one are noted in the run log; revoking a revoked link answers `204` and logs
nothing. When the server forgets the run (past 100 runs, the oldest with
nothing running), its links go with it, as a session's go with the session:
they open nothing, the join route answers `404 invalid_link`, and the viewers
still attached through them, to the ended sessions of its members, are closed
with `4403` as on a revoke. The join route answers `404 run_gone` only when
the run is forgotten as the link is being resolved. A restart forgets links
and runs alike.

The run log is the run's own record beside its sessions' activity, oldest
first; each entry is an activity entry of type `status`, `error` or `link`
whose `message` is one of these:

| Entry | `type` | `message` |
|---|---|---|
| launched | `status` | `launched <crew>: <n> members, <k> starting now` |
| member started | `status` | `<member> started` |
| member joined | `status` | `<member> joined the run` (added mid-run) |
| not ready | `status` | `<member> was not ready after 1m0s: typing its prompt anyway` |
| prompt typed | `status` | `typed <member>'s prompt` |
| trust question | `status` | `<member> asks "<question>": answer it in <member>'s terminal; its prompt waits`, once each time the question holds the prompt |
| prompt without its Enter | `error` | `typed <member>'s prompt without its Enter: <member> waits on a question; answer it, then press Enter in its terminal` |
| Enter again | `status` | `pressed Enter again for <member>: it did not report taking its prompt within 3s` |
| no yolo recipe | `status` | `<member>: yolo is on, but <agent name> has no yolo recipe: launched without one` |
| resumed | `status` | `<member> resumed its conversation` |
| relaunched | `status` | `<member> started anew` (its prompt is typed again once it is ready) |
| ended before its prompt | `status` | `<member> ended before its prompt was typed: <how it ended>` |
| could not start | `error` | `<member> could not start: <why>` |
| done | `status` | `<member> is done: starting <members>` |
| handoff queued | `status` | `handoff queued from <a> to <b>: <b> is waiting for input`, or `: <b> has not had its prompt yet`; see below |
| handoff delivered | `status` | `handoff delivered from <a> to <b>` |
| handoff without its Enter | `error` | `handoff from <a> to <b> typed without its Enter: <b> waits on a question; answer it, then press Enter in its terminal`, or `: <the error>` when the run stopped or the write failed during the pause |
| handoff dropped | `error` | `handoff dropped from <a> to <b>: <why>`, where why is `10 already waiting` (the oldest waiting is dropped), `<b> is not running`, `the run is stopped` or the error writing it |
| handoff to unknown member | `error` | `handoff to unknown member "<name>" from <a>` |
| handoff to itself | `error` | `handoff to itself from <a>` |
| handoff to a member that is not running | `error` | `handoff to a member that is not running, from <a> to <b>` |
| exclude | `error` | `could not add .conductor/ to the repository's info/exclude: <why>`, at most once a run |
| stopped | `status` | `stopped` |
| link created | `link` | `link created: <label> (<role>)`, the label `unlabelled` when there is none, as for a session's link |
| link revoked | `link` | `link revoked: <label>`, likewise |
| view link refused | `error` | `the view link could not be created: <why>` (at launch) |

Every handoff a member reports while its run is not stopping is noted once as
delivered, dropped, to an unknown member, to itself or to a member that is not
running. A handoff that waits is noted as queued once, when it is found
waiting rather than when it arrives: Conductor looks at a member's queue as
handoffs arrive and as the member changes, and notes every handoff in it not
noted yet when the member has not had its prompt, or is `needs_input` as the
handoff at the head of the queue is about to be typed. A handoff dropped to
make room before it was found waiting is noted as dropped only.

Limits of runs, beside those of crews above: a run has at most 12 members (an
added one included), a log of at most 200 entries (the oldest goes first) and
at most 100 links; the server keeps 100 runs, and a launch past that forgets
the oldest with nothing running, never one whose launch has not answered yet;
at most 10 handoffs wait for a member; a broadcast is at most 4096 bytes once
made one line; and whatever Conductor
submits into a session, a prompt, a handoff or a broadcast, is one write of at
most 32756 bytes (32 KiB with the paste markers) and then its carriage return,
250 ms later. A launch, a start, an added or resumed member has 2 minutes to
make its worktrees and start its sessions, a broadcast's submissions 15
seconds, and a stop 30 seconds.
A member's diff is read at most every 10 s, four reads at a time, and a member
not ready for its prompt gets it after 60 s.

An ended member can be resumed (`POST /api/runs/{run}/members/{name}/resume`,
or `POST /api/sessions/{id}/resume` on its session): a new session in its
working directory, its worktree and branch untouched, tagged with the run and
launched with the run's yolo choice. It resumes the member's agent session
when the agent has a `session` recipe and that session had a turn, and gets no
prompt; otherwise it starts anew and its role prompt is typed again once it is
ready, and only a `done` after that prompt counts for the members after it. A
member of a stopped run is not resumed (`409 run_stopped`).
