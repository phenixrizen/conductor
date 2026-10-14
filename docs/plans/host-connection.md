# Plan: how a `conductor host` talks to its server

Branch `host-connection`. Reviewed before any code (AGENTS.md, Reviews).
No code is written until #133 (branch `quick-reply-question`, which also
changes `internal/hostagent/peer.go`) has merged; each pull request below
starts from `main` once the one before it has merged, since they touch
the same files (`hostagent/agent.go`, `peer.go`).

## The four changes

1. **A link's keys and saves pass the link's check.** A keystroke (INPUT)
   and a save's part (FILE_WRITE) from a viewer who joined through a link
   are written to the session, or to its folder, only while the link's
   credential stands (`linkCred`, #172) and the server was heard from
   within the bound (`vouches`, #182), as a submission's text and its
   Enter are. The check is the session's (`internal/session`, shared by
   the server and the host, as AGENTS.md puts anything that changes what a
   viewer gets): `input` asks `revoked(sub)` (AttachOptions.Authorize)
   as it queues the write and again as the writer takes it (`procWrite.
   over`), and `FileWrite` asks it at each part, before `saveFile`. A
   refusal answers the viewer: an INPUT error `revoked` or `expired`
   (`inputError` gains the two), a FILE error of kind `error` with code
   `revoked` or `expired` (new codes in docs/protocol.md, File saves; the
   browser shows the words it is sent, no change there). A viewer with no
   link (the owner's window, a paste invite's viewer, the host's own
   terminal) has no credential and is never refused by this check; the
   role check (`read_only`) comes first, as it does.
2. **The server's own messages travel ahead of relayed frames.** A host's
   connection on the server gets two lanes: the server's own messages
   (`link_gone`, a `viewer_leave` that gives a reason, `stop`,
   `attention`, `activity`, the answers to the host's requests,
   `error`) in a lane the writer takes first, bounded by the room kept
   for them now (1 MiB, 512 messages; one that finds it full closes the
   connection, as `send` does); the viewers' messages (`viewer_join`,
   `offer`, `ice`, `relay_start`, the RELAY envelopes, `viewer_behind`,
   a `viewer_leave` with no reason) in the viewers' share, as now, in
   their order. The server's keepalive ping waits for its lane to be
   empty. No message or field changes: `internal/proto` and
   `protocol.ts` stay; docs/protocol.md states the order.
3. **Saves and answers run off the host's read loop (#183).** A save's
   part, over the relay or the data channel, goes on a `saving` queue of
   the viewer's (`requestQueue`, as its keys and editor requests, #118
   and #122): in the order sent, one at a time, at most 64 parts and
   2 MiB waiting (two whole saves: a part is at most 32 KiB, a save
   1 MiB); a part past the bound is refused `too_many_requests` and the
   connection stays. An offer's answer is made and sent on a goroutine
   of the peer's (`handleOffer` returns at once): the answer's wait for
   its turn on the writer (up to 15 s) holds nothing else; candidates
   that come before the remote description is set wait for it
   (`pending`, 64 at most), as now; a second offer for the same viewer
   is refused while the first is under way.
4. **A plain-HTTP server needs saying so.** `conductor host --server
   http://…` (or `ws://`) to a host that is not loopback (`localhost`,
   `127.0.0.0/8`, `::1`, by name as `LoopbackServer` reads it) is refused
   before anything is dialed, with a message naming `--insecure`; with
   the flag it goes ahead and prints a warning on stderr, and logs one,
   that the server's messages travel in the clear and unverified on the
   way. Loopback needs no flag. A redirect the dial follows is held to
   the same rule as the URL given: one to a plain URL off this machine
   is refused. The server's own uplink (`rendezvous.server`) follows the
   rule through config validation: a plain URL to a host that is not
   loopback is a config error unless `rendezvous.insecure: true`
   (`CONDUCTOR_RENDEZVOUS_INSECURE=1`), so `conductor serve` (and so
   every crew `conductor up` launches through it) does not start; an
   existing config that names one is refused at start with that message,
   and nothing is rewritten. The desktop app's Settings page refuses
   such a switchyard as the field is saved, with the words why (a check
   in `web/app/utils/desktop.ts` the page runs, and `desktop/src/
   settings.ts`'s `validate` behind it); the default switchyard is
   `https` and unaffected.

## The events that interact

- E1 a link revoked: the server marks the link, sends `link_gone`, closes
  the link's viewers (`viewer_leave{revoked}` each).
- E2 a link expired: on the server's clock, `link_gone` from the link's
  timer and `viewer_leave{expired}` from each viewer's; the host keeps no
  clock of its own for it.
- E3 the connection lost and noticed by the host (a read fails, the socket
  takes nothing for 2 min, nothing heard for 2 min after its pings): every
  link counts as gone (`linkCreds.lost`), every peer is closed.
- E4 the connection half-open (nothing arrives, nothing has failed yet):
  only the hearing bound (15 s) speaks.
- E5 a ping or a pong, either side's: counts as heard.
- E6 relayed frames queued server→host (the viewers' share, up to 7 MiB)
  and host→server (the writer's relay lane).
- E7 a save: up to 32 parts, the last of which writes the folder.
- E8 an offer, and the answer that waits for its turn on the writer.
- E9 a viewer leaving for no reason of its link's: `viewer_leave` with no
  reason, in the viewers' share, after the viewer's frames.
- E10 the session ending: its links are ended, not revoked (no
  `link_gone`); the viewers are detached; the status goes last.
- E11 a keystroke: queued on the viewer's keys queue, 10 s from arrival.
- E12 a reconnect: a new connection, new credentials, the viewers rejoin.
- E13 a relayed viewer behind (`viewer_behind`): the host holds its frames.

## The orders they come in, and what must happen

- O1 E1 or E2, then a key or a part arrives (over the data channel, which
  the server does not carry): `link_gone` read, the credential gone, the
  key refused `revoked`/`expired`, the part refused the same, the folder
  untouched.
- O2 a key or a part arrives and waits (E11, E7), then E1 lands before its
  turn: refused at its turn, never written; a save whose last part was
  queued before the going is refused at that part.
- O3 E1 in the middle of a save (parts 1..n-1 taken): the next part is
  refused, the pending write dropped with the viewer.
- O4 E1 during E4: no `link_gone` arrives; 15 s after the last thing
  heard, keys and parts are refused as a submission is (`serverQuiet`,
  a kind of `ErrRevoked`); the moment something is heard they pass again
  unless the going has arrived meanwhile.
- O5 E1 while E6 holds 7 MiB server→host: `link_gone` is written after
  the one message being written when it was queued and before any
  relayed frame queued after it; the server's next ping waits for it.
  What the host hears after the going was written before it, but for
  that one message, whose write the server bounds (15 s, then it closes
  the connection, E3 for the host). So the going reaches a host that
  reads within about a message's time on an ordinary path, and on the
  slowest path a line of the link's viewer can be entered at most about
  30 s after it (the server's write bound and the host's hearing bound).
- O6 E1 while the host's read loop is in a save (E7) or an answer (E8):
  neither is on the loop any more; `link_gone` is read as it arrives, the
  save's remaining parts then refused.
- O7 E8 while E6 and E1 arrive: the loop reads them while the answer
  waits; an answer whose turn never comes within 15 s is dropped and the
  viewer falls back to the relay on its own timeout, as now.
- O8 E2: the link's `link_gone` and each viewer's `viewer_leave{expired}`
  fire from two timers in either order; both go in the server's lane;
  the credential is gone at the first of them.
- O9 E3 then E12: the old viewers' keys and parts still queued are
  dropped (peer closed), nothing of theirs written; a viewer joining
  through the same link on the new connection starts with a fresh
  credential.
- O10 E10: a viewer detached by the end gets nothing written (the session
  refuses as ended); a part for a detached subscription is dropped in
  `handle`, as now.
- O11 E9 while parts wait: dropped unanswered, the viewer being gone
  (`request.live`); the folder untouched.
- O12 a viewer with no link, in every order above: never refused by the
  credential or the hearing bound; still refused `read_only` by role, as
  is a view-role viewer's part, by the server (`RelayToHost`) and the
  session, before the credential is asked.
- O13 two viewers of one link: one's E9 ends nothing of the other's;
  `link_gone` ends both. E13 with E1: the hold is host→server and does
  not touch the server's lane; `link_gone` arrives whatever is held.
- O14 the `saving` queue full: the part refused `too_many_requests`, the
  save fails in the editor with the words, the connection stays, the
  other viewers' frames and the server's messages go on being read.
- O15 `--server http://host.example`: refused before any dial, exit 2,
  the message naming `--insecure`; with it, a warning, then as for
  `https`; `http://localhost:8080` and `http://127.0.0.1:…` need nothing.
- O16 `rendezvous.server: http://host.example` without `insecure`: the
  config fails validation; `conductor serve` and `conductor switchyard`
  stop at start with the message; the desktop app shows the server
  failed and its Settings page shows the words at save. With `insecure:
  true` the uplink dials it and warns once per start.
- O17 a dial of `https://a` redirected to `http://b`: refused as
  `--server http://b` would be; with `--insecure`, followed.

## Invariants

- I1 Nothing a link's viewer sends (a submission's text and its Enter, a
  keystroke, a save) is written to the session or its folder once the
  link went (`link_gone`, a `viewer_leave` with a reason, the connection
  lost) or the server was not heard from within the bound.
- I2 The check is asked at the write, not only on arrival: what waited in
  a queue across the going is refused at its turn.
- I3 A viewer with no link is never refused by I1's check; a viewer's role
  still bounds what it may do, checked first.
- I4 A message of the server's own waits behind at most the one message
  being written when it was queued, never behind the viewers' queue; the
  lane holds at most 1 MiB and 512 messages; a server's ping is written
  only while that lane is empty.
- I5 `link_gone` is written before the leaves of the link's viewers and
  before any relayed frame queued after it; the viewers' messages keep
  their order among themselves (a join before the viewer's offer, frames
  and leave).
- I6 The host's read loop waits on nothing but the read: no disk, no
  viewer's transport, no turn on the writer; every handler returns at
  once or hands off to a queue bounded in count and bytes.
- I7 A viewer's parts run in the order sent, one save at a time; a full
  queue refuses a part and never the connection.
- I8 A host never dials a plain URL to a host off this machine unless
  told `--insecure` (or `rendezvous.insecure`), and then says so once on
  stderr and in the log; a redirect counts as the URL it leads to.
- I9 A config that names a plain switchyard off this machine without
  `insecure` is refused at start, unchanged on disk.
- I10 No new message, field or frame; the message bounds stay; the browser
  sees nothing new; docs/protocol.md records the lane's order and the
  two refusal codes.

## Out of scope

- The hold (#185), the switchyard's bound on what a host relays, the
  relayed frame sizes and the server's write bound: unchanged.
- The admin API client's `--server` (`conductor up`, `conductor crews`):
  not a host connection; its rule (http or https with a host) stays.
- File reads, editor opens and chat posts: not part of this change.
- Windows and WSL networking: nothing is asked of the person.

## Tests that pin the orderings

Go (`go test -race`), each named for what it proves:

- `internal/session`: `TestInputRefusedOnceCredentialGone` (O1: nothing
  reaches the PTY), `TestInputQueuedBeforeCredentialGoneIsNotWritten`
  (O2, the writer's `over`), `TestFileWriteRefusedOnceCredentialGone`
  (O1), `TestFileWriteRefusedWhenCredentialGoesMidSave` (O3, the file
  untouched), `TestInputAndFileWriteNeedNoCredentialWithoutALink` (O12).
- `internal/hostagent`: `TestLinkKeysRefusedAfterLinkGone`,
  `TestLinkSaveRefusedAfterLinkGone` (O1, both transports),
  `TestLinkKeysRefusedWhenServerQuiet`, `TestLinkSaveRefusedWhenServerQuiet`
  (O4, `liveWithin` short), `TestLinkKeysPassAgainOnceHeard` (O4),
  `TestOwnerWindowAndPasteViewerKeysNeedNoVouch` (O12),
  `TestKeysOfAViewerThatLeftAreDropped` (O9, O11);
  `TestRelayedSaveRunsOffTheReadLoop` (O6: a folder whose write blocks
  through a test seam while another viewer's keys and a `link_gone` are
  read and acted on), `TestSaveQueueOrderedAndBounded` (I7, O14),
  `TestSaveOfAViewerThatLeftIsDropped` (O11),
  `TestDataChannelSaveUsesTheSameQueue`, `TestAnswerWaitsOffTheReadLoop`
  (O7: the writer's turn held, the next messages read meanwhile),
  `TestAnswerDroppedWhenItsTurnNeverComes` (O7),
  `TestSecondOfferRefusedWhileTheFirstIsUnderWay`;
  `TestRunRefusesPlainServerOffThisMachine`,
  `TestRunAllowsPlainServerOnLoopback`,
  `TestInsecureAllowsPlainServerAndWarns`,
  `TestRedirectToPlainServerRefused` (O15, O17).
- `internal/signal`: `TestServerOwnMessagesGoAheadOfRelayedFrames` (O5:
  the viewers' share full, `link_gone` taken next),
  `TestLinkGoneBeforeTheLeaves`, `TestReasonedLeaveInTheServerLane` (I5,
  O8), `TestViewersMessagesKeepTheirOrder` (I5),
  `TestServerLaneFullClosesTheConnection` (I4).
- `internal/api`: `TestLinkGoneReachesHostAheadOfQueuedFrames` (O5 over
  a loopback WebSocket: a host that reads slowly, a viewer that floods, a
  revoke; the host reads `link_gone` within one message),
  `TestServerPingWaitsForItsLane` (I4),
  `TestLinkLineRefusedAfterRevokeBehindQueue` (O5 end to end, as
  `hostlive_test.go` does it: short bounds, the queue full, the submit
  refused).
- `internal/cli`: `TestHostRefusesPlainServerWithoutInsecure` (exit 2, the
  message, no dial), `TestHostInsecurePrintsTheWarning`.
- `internal/config`: `TestRendezvousPlainServerOffThisMachineRefused`,
  `TestRendezvousPlainServerOnLoopbackAllowed`,
  `TestRendezvousInsecureAllowsIt` (the key and the variable; O16).

UI: `desktop/test/settings.test.ts` (`validate` refuses a plain
switchyard off this machine with the words, keeps a loopback one and an
`https` one); `web/app/utils/desktop.test.ts` (the page's check says the
same words); `web/e2e/settings.spec.ts`: "a plain switchyard on another
machine is refused at save with the words why, and a loopback one is
kept" (what a person sees, under the bridge stand-in).

Docs: docs/protocol.md (the lane's order; the `revoked`/`expired` codes
on INPUT and FILE errors; the host's queues), docs/architecture.md
(Developer-hosted sessions; the plain-HTTP rule among the model's
rules), `conductor host --help` and the Settings card's words.

## Pull requests

1. Change 1 on its own: `internal/session` (`input.go`, `write.go`),
   `internal/hostagent/peer.go` (`inputError`), the tests above.
2. Changes 2 and 3 together: `internal/signal/hosted.go`,
   `internal/api/ws_host.go`, `internal/hostagent` (`agent.go`,
   `peer.go`, `queue.go`), docs; they share the host's read loop and the
   tests that time the going against the queues.
3. Change 4 on its own: `internal/hostagent/agent.go` (`controlURL`,
   `Options.Insecure`, the dial's redirect check), `uplink.go`,
   `internal/cli/host.go`, `internal/config`, `desktop/src/settings.ts`,
   `web/app/utils/desktop.ts`, `web/app/pages/settings.vue`, the specs.

In that order, each from `main` after the one before merged, and none
before #133. Each pull request's body names this plan's last reviewed
commit apart from the code's.
