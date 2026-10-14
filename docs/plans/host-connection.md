# Plan: how a `conductor host` talks to its server

Branch `host-connection`. Reviewed before any code (AGENTS.md, Reviews):
two rounds; the second round's findings are taken into this revision.
#133 has merged (#192), so code may start; each pull request below starts
from `main` once the one before it has merged, since they touch the same
files (`hostagent/agent.go`, `peer.go`).

**The owner's decisions (2026-10-14).**
- No third review round of this plan: it stands as revised after round 2.
- The host vouches for the server only by its own answered ping (change
  3), and accepts the cost named there: while a server pauses its reading
  for longer than the bound, a link's line waits rather than being
  entered.
- A write already begun when a link goes is the one named exception
  (change 1): no write deadline is added to the PTY layer.
- Change 1 also covers a link viewer's file reads, editor opens and chat
  posts; change 5 also covers the admin client's `--server` (`conductor
  up`, `conductor crews`).

## The changes

1. **What a link's viewer sends passes the link's check.** A keystroke
   (INPUT) and a save's part (FILE_WRITE) from a viewer who joined through a link
   are written to the session, or to its folder, only while the link's
   credential stands (`linkCred`, #172) and the host vouches for the
   server (change 3), as a submission's text and its Enter are. The check
   is the session's (`internal/session`, shared by the server and the
   host): `input` asks `revoked(sub)` (AttachOptions.Authorize) as it
   queues the write and again as the writer takes it (`procWrite.over`),
   which never begins a write it finds refused; a write begun before the
   going (the program not reading) lands when the program reads, as a
   submission's text does (#172): at most one INPUT frame, or a
   submission's text or its Enter, per viewer, and nothing after it;
   `FileWrite` asks it at each part, and `saveFile` once more immediately
   before the rename that is the save's commit, after the path checks,
   the hash and the temporary file: a refusal there removes the temporary
   file and leaves the target as it was. A refusal answers the viewer: an
   INPUT error `revoked` or `expired` (`inputError` gains the two), a
   FILE error with code `revoked` or `expired` (new codes in
   docs/protocol.md, File saves; the browser shows the words it is sent).
   A viewer with no link (the owner's window, a paste invite's viewer,
   the host's own terminal) has no credential and is never refused by
   this check; the role check (`read_only`) comes first, as it does.
   The same check is asked, once as each is taken, of a file read
   (FILE_GET), an editor's open (`nvim_open`) and a chat post (`chat`,
   `chat_send`) from such a viewer: a refusal answers it with the same
   codes and does nothing, a read reading no byte of the file.
2. **The server's own messages travel ahead of relayed frames, and
   nothing of a closed viewer's follows its close.** A host's connection
   on the server gets two lanes: the server's own messages (`link_gone`,
   a `viewer_leave` that gives a reason, `stop`, `attention`, `activity`,
   the answers to the host's requests, `error`) in a lane the writer
   takes first, bounded by the room kept for them now (1 MiB, 512
   messages; one that finds it full closes the connection, as `send`
   does); the viewers' messages (`viewer_join`, `offer`, `ice`,
   `relay_start`, the RELAY envelopes, `viewer_behind`, a `viewer_leave`
   with no reason) in the viewers' share, as now, in their order. Since
   the lane lets a link's going pass a viewer's messages queued before
   it, two rules keep a closed viewer from reaching the host after its
   close: the server closes a link's viewers before it queues the link's
   `link_gone` (`closeLink`, the other order today), and the writer drops
   a message of a viewer closed for its link (its close's reason revoked
   or expired; `Outbound` names its viewer) as it takes it; a join counts
   as sent once written, and a leave follows a written join only. A
   viewer that left for any other reason (a quick reply's, which closes
   150 ms after it sent) has what was admitted before its leave written
   first, as now (`TestAViewersLastFramesReachTheHostBeforeItsLeave`
   stands). A viewer whose join was dropped is never known to the host,
   older or newer. The host keeps no record of links gone: the
   server's push-out of its oldest joined link names one gone that still
   opens the session, and a viewer joining through it later starts
   afresh, as now. No message or field changes; docs/protocol.md states
   the order.
3. **The host vouches for the server by its own answered ping.** A line,
   a key or a save of a link's viewer passes only while a ping the host
   sent within the last 15 s has been answered (`c.Ping` returns on the
   pong): the host pings at once when a viewer joins through a link
   (`addPeer`, before its hello can arrive) and every 5 s while a link is
   held, quiet or not, each waiting 15 s, side by side. Nothing else
   vouches: not a relayed frame, not the server's own ping, not bytes
   read, which keep only the two-minute give-up. The credential and the
   freshness part: a viewer's attach (`AttachWith`'s call of
   `AttachOptions.Authorize`) needs the credential alone, so the first
   viewer after a start, a reconnect or an idle spell attaches before any
   pong; the freshness is asked at writes (`AttachOptions.Fresh`, asked
   by `revoked` with the credential), and a write that finds no answered
   ping yet waits for the one under way within its own time rather than
   being refused at once. A pong follows everything the server wrote before
   it, so what the host read before the answer is at most as old as the
   ping; with the lanes, a going queued before the answer was written but
   not yet written follows it behind at most one relayed message and the
   lane's contents. The server's pings every 5 s while a link stands
   stay, for older hosts. The cost: a server whose reading pauses for
   longer than the bound (a switchyard's relay bound on open hosts at its
   worst, about 35 s; a large frame of the host's ahead of its ping on a
   slow uplink) withholds those lines meanwhile, where #182 let them
   through on the server's pings.
4. **Saves and answers run off the host's read loop (#183).** A save's
   part, over the relay or the data channel, goes on a `saving` queue of
   the viewer's (`requestQueue`, as its keys and editor requests, #118
   and #122): in the order sent, one at a time, at most 64 parts and
   2 MiB waiting (two whole saves: a part is at most 32 KiB, a save
   1 MiB); a part past the bound is refused `too_many_requests` and the
   connection stays. An offer's answer is made and sent on a goroutine of
   the peer's (`handleOffer` returns at once): the answer's wait for its
   turn on the writer (up to 15 s) holds nothing else; candidates that
   come before the remote description is set wait for it (`pending`, 64
   at most), as now; a second offer for the same viewer is refused while
   the first is under way.
5. **A plain-HTTP server needs saying so.** `conductor host --server
   http://…` (or `ws://`) to a host that is not loopback (`localhost`,
   `127.0.0.0/8`, `::1`, by name as `LoopbackServer` reads it) is refused
   before anything is dialed, with a message naming `--insecure`; with
   the flag it goes ahead and prints a warning on stderr, and logs one,
   that the server's messages travel in the clear and unverified on the
   way. Loopback needs no flag. A redirect the dial follows is held to
   the same rule as the URL given. The server's own uplink
   (`rendezvous.server`) follows the rule through config validation: a
   plain URL to a host that is not loopback is a config error unless
   `rendezvous.insecure: true` (`CONDUCTOR_RENDEZVOUS_INSECURE=1`), so
   `conductor serve` (and every crew `conductor up` launches through it)
   does not start; an existing config that names one is refused at start
   with that message, and nothing is rewritten. The desktop app's
   Settings page refuses such a switchyard as the field is saved, with
   the words why (a check in `web/app/utils/desktop.ts` the page runs,
   and `desktop/src/settings.ts`'s `validate` behind it); the default
   switchyard is `https` and unaffected. The admin client's `--server`
   (`conductor up`, `conductor crews`, and `CONDUCTOR_SERVER`), which
   sends the workbench token with every request, follows the same rule:
   a plain URL to a host that is not loopback is refused before any
   request, with a message naming `--insecure`, and with the flag a
   warning is printed.

(The brief's four items: 1; 2 and 3 are its second; 4 its third; 5 its
fourth.)

## The events that interact

- E1 a link revoked: the server marks the link, closes its viewers,
  queues `link_gone`; each viewer's `viewer_leave{revoked}` follows.
- E2 a link expired: on the server's clock, `link_gone` from the link's
  timer and `viewer_leave{expired}` from each viewer's.
- E3 the connection lost and noticed by the host (a read fails, the socket
  takes nothing for 2 min, nothing read for 2 min after its pings): every
  link counts as gone (`linkCreds.lost`), every peer is closed.
- E4 the connection half-open, or the server's reading paused: the host's
  pings go unanswered; only the 15 s bound speaks.
- E5 the host's ping answered: the host vouches 15 s from its send.
- E6 relayed traffic queued server→host (the viewers' share, up to 7 MiB,
  and the path's buffers) and host→server (the writer's relay lane).
- E7 a save: up to 32 parts, the last of which checks, writes a temporary
  file and renames it over the target.
- E8 an offer, and the answer that waits for its turn on the writer.
- E9 a viewer leaving for no reason of its link's: `viewer_leave` with no
  reason, in the viewers' share, after the viewer's frames.
- E10 the session ending: its links are ended, not revoked (no
  `link_gone`); the viewers are detached; the status goes last.
- E11 a keystroke: queued on the viewer's keys queue, 10 s from arrival.
- E12 a reconnect: a new connection, new credentials, the viewers rejoin.
- E13 a viewer's join still queued, or its frames, when its link goes.

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
- O4 E1 (or E3) while the last part's work is under way (its checks, its
  hash, its temporary file on a slow disk): the check before the rename
  refuses; the temporary file is removed; the target is as it was.
- O5 E1 during E4: no `link_gone` arrives; 15 s after the last ping the
  host sent that was answered, keys, parts and lines are refused as for
  a link gone (`serverQuiet`, a kind of `ErrRevoked`); the next answered
  ping lets them pass unless the going arrived meanwhile.
- O6 E1 while E6 holds 7 MiB and the path holds more: `link_gone` is
  written after the message being written when it was queued and before
  every relayed frame queued after it; a pong written before it is read
  before it and vouches at most 15 s from its ping's send; so no line is
  entered more than 15 s after the host's last answered ping was sent,
  whatever the path still holds, and the going is read behind that
  answer after at most one relayed message and the lane.
- O7 E13: E1 lands while a viewer's join, `relay_start`, hello or frames
  wait in the viewers' share. The viewer is closed for its link before
  `link_gone` is queued, so the writer drops each of them as it takes
  it; the host, older or newer, never knows the viewer, and
  `linkCreds.gone` forgetting the id mints no credential for it. A join
  written before the close is followed by its frames taken before the
  close, then the going; what was queued and not yet taken is dropped.
  E9 instead (the viewer left, its link standing): its admitted frames
  go, then its leave; a quick reply's submit queued under a congested
  writer reaches the host.
- O7b a viewer joins through a link after a start, a reconnect or an idle
  spell longer than the bound: the host pings at the join; the hello
  attaches on the credential alone; its first line or key waits for that
  ping's answer within its own time, and passes on it.
- O7c a write begun before E1 while the program is not reading (a key, a
  submission's text, its Enter): it lands when the program reads; the
  writes queued behind it are refused at their turn, never begun.
- O8 E1 while the host's read loop is in a save (E7) or an answer (E8):
  neither is on the loop any more; `link_gone` is read as it arrives, the
  save's remaining parts then refused.
- O9 E8 while E6 and E1 arrive: the loop reads them while the answer
  waits; an answer whose turn never comes within 15 s is dropped and the
  viewer falls back to the relay on its own timeout, as now.
- O10 E2: the link's `link_gone` and each viewer's `viewer_leave{expired}`
  fire from two timers in either order; both go in the server's lane;
  the credential is gone at the first of them.
- O11 E3 then E12: the old viewers' keys and parts still queued are
  dropped (peer closed), nothing of theirs written; a viewer joining
  through the same link on the new connection starts afresh.
- O12 E10: a viewer detached by the end gets nothing written (the session
  refuses as ended); a part for a detached subscription is dropped in
  `handle`, as now; a save whose last part was taken before the end
  commits (the folder is the person's own). E9 while parts wait: dropped
  unanswered, the viewer being gone (`request.live`).
- O13 a viewer with no link, in every order above: never refused by the
  credential or the vouching bound; still refused `read_only` by role,
  as is a view-role viewer's part, by the server (`RelayToHost`) and the
  session, before the credential is asked. Two viewers of one link: one's
  E9 ends nothing of the other's; `link_gone` ends both. The hold
  (`viewer_behind`) is host→server and does not touch the server's lane.
- O14 the `saving` queue full: the part refused `too_many_requests`, the
  save fails in the editor with the words, the connection stays, the
  other viewers' frames and the server's messages go on being read.
- O15 `--server http://host.example`: refused before any dial, exit 2,
  the message naming `--insecure`; with it, a warning, then as for
  `https`; `http://localhost:8080` and `http://127.0.0.1:…` need nothing.
  A dial of `https://a` redirected to `http://b` is refused as `--server
  http://b` would be; with `--insecure`, followed.
- O16 `rendezvous.server: http://host.example` without `insecure`: the
  config fails validation; `conductor serve` and `conductor switchyard`
  stop at start with the message; the desktop app shows the server
  failed and its Settings page shows the words at save. With `insecure:
  true` the uplink dials it and warns once per start.

## Invariants

- I1 Nothing a link's viewer sends (a submission's text and its Enter, a
  keystroke, a save) is begun on the session or committed to its folder
  once the link went (`link_gone`, a `viewer_leave` with a reason, the
  connection lost) or the host's last answered ping is older than the
  bound. The one exception is a write the program had been handed
  before the going and has not read: it lands when the program reads,
  as a submission's text does today; a save has no such exception, its
  commit being the rename.
- I2 The check is asked at the write, not only on arrival: what waited in
  a queue across the going is refused at its turn, and a save's commit
  (the rename) asks last, after its slow work.
- I3 A viewer with no link is never refused by I1's check; a viewer's role
  still bounds what it may do, checked first.
- I4 A message of the server's own waits behind at most the one message
  being written when it was queued, never behind the viewers' queue; the
  lane holds at most 1 MiB and 512 messages.
- I5 Nothing of a viewer's reaches the host after the server closed it
  for its link: its queued messages are dropped as the writer takes
  them, and a link's viewers are closed before the link's going is
  queued. A viewer that left otherwise has its admitted messages written
  before its leave. The viewers' messages keep their order among
  themselves.
- I6 The host vouches on its own answered pings alone: no line is entered
  more than 15 s after the send of the last ping that was answered, and
  nothing read vouches past the answer that followed it. An attach needs
  no pong; a first write waits for the first answer within its own time.
- I7 The host's read loop waits on nothing but the read: no disk, no
  viewer's transport, no turn on the writer; every handler returns at
  once or hands off to a queue bounded in count and bytes.
- I8 A viewer's parts run in the order sent, one save at a time; a full
  queue refuses a part and never the connection.
- I9 A host never dials a plain URL to a host off this machine unless
  told `--insecure` (or `rendezvous.insecure`), and then says so once on
  stderr and in the log; a redirect counts as the URL it leads to.
- I10 A config that names a plain switchyard off this machine without
  `insecure` is refused at start, unchanged on disk.
- I11 No new message, field or frame; the message bounds stay; the browser
  sees nothing new; docs/protocol.md records the lane's order, the
  vouching rule and the two refusal codes.

## Out of scope

- The hold (#185), the switchyard's bound on what a host relays, the
  relayed frame sizes and the server's write bound: unchanged.
- Windows and WSL networking: nothing is asked of the person.

## Tests that pin the orderings

Go (`go test -race`), each named for what it proves:

- `internal/session`: `TestInputRefusedOnceCredentialGone` (O1),
  `TestInputQueuedBeforeCredentialGoneIsNotWritten` (O2, the writer's
  `over`), `TestFileWriteRefusedOnceCredentialGone` (O1),
  `TestFileWriteRefusedWhenCredentialGoesMidSave` (O3),
  `TestSaveRefusedWhenCredentialGoesBeforeTheRename` (O4: the
  `afterCheckHook` seam holds the last part, the credential goes, the
  seam releases; the target unchanged, the temporary file gone),
  `TestBegunWriteLandsAndNothingQueuedBehindItDoes` (O7c: a program that
  stops reading, a key and an Enter blocked, the credential goes, the
  program reads: the begun write lands, the next is refused),
  `TestAttachNeedsNoFreshnessAndAWriteDoes` (O7b, `AttachOptions.Fresh`),
  `TestInputAndFileWriteNeedNoCredentialWithoutALink` (O13).
- `internal/hostagent`: `TestLinkKeysRefusedAfterLinkGone`,
  `TestLinkSaveRefusedAfterLinkGone` (O1, both transports),
  `TestLinkKeysRefusedWhenPingsGoUnanswered`,
  `TestLinkSaveRefusedWhenPingsGoUnanswered`,
  `TestLinkLinePassesAgainOnAnAnsweredPing` (O5, `liveWithin` short),
  `TestBufferedFramesAndServerPingsDoNotVouch` (I6: relayed frames and
  the server's pings trickle in past the bound, no pong; the line is
  withheld), `TestFirstLinkViewerAttachesBeforeAnyPong` and
  `TestFirstLineWaitsForTheFirstPong` (O7b: after a start, a reconnect
  and an idle spell past the bound; the ping goes at the join),
  `TestOwnerWindowAndPasteViewerKeysNeedNoVouch` (O13),
  `TestKeysOfAViewerThatLeftAreDropped` (O11, O12);
  `TestRelayedSaveRunsOffTheReadLoop` (O8: a folder whose write blocks
  through a seam while another viewer's keys and a `link_gone` are read
  and acted on), `TestSaveQueueOrderedAndBounded` (I8, O14),
  `TestSaveOfAViewerThatLeftIsDropped` (O12),
  `TestDataChannelSaveUsesTheSameQueue`, `TestAnswerWaitsOffTheReadLoop`
  (O9), `TestAnswerDroppedWhenItsTurnNeverComes` (O9),
  `TestSecondOfferRefusedWhileTheFirstIsUnderWay`;
  `TestRunRefusesPlainServerOffThisMachine`,
  `TestRunAllowsPlainServerOnLoopback`,
  `TestInsecureAllowsPlainServerAndWarns`,
  `TestRedirectToPlainServerRefused` (O15).
- `internal/signal`: `TestServerOwnMessagesGoAheadOfRelayedFrames` (O6:
  the viewers' share full, `link_gone` taken next),
  `TestViewersAreClosedBeforeLinkGoneIsQueued`,
  `TestAClosedViewersQueuedMessagesAreDropped` (O7, I5: a join, a
  `relay_start`, a hello and frames queued, then the revoke; the writer
  takes none; no leave for the unwritten join),
  `TestALeavingViewersAdmittedMessagesStillGo` and
  `TestQuickReplyReachesTheHostUnderACongestedWriter` (O7's E9 half, I5;
  the api's `TestAViewersLastFramesReachTheHostBeforeItsLeave` kept),
  `TestReasonedLeaveInTheServerLane` (O10),
  `TestViewersMessagesKeepTheirOrder` (I5),
  `TestServerLaneFullClosesTheConnection` (I4).
- `internal/api`: `TestLinkGoneReachesHostAheadOfQueuedFrames` (O6 over
  a loopback WebSocket: a host that reads slowly, a viewer that floods, a
  revoke; `link_gone` read within one message),
  `TestRevokeOvertakingAQueuedJoinReachesNoHost` (O7, with a host stub
  that reads as an older host does), `TestLinkLineRefusedAfterRevokeBehindQueue`
  (O6 end to end, as `hostlive_test.go` does it),
  `TestLinkLineWithheldWhileTheServerPausesReading` (change 3's cost,
  pinned as a decision).
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

Docs: docs/protocol.md (the lane's order and what a closed viewer's
messages become; the vouching rule; the `revoked`/`expired` codes on
INPUT and FILE errors; the host's queues), docs/architecture.md
(Developer-hosted sessions; the plain-HTTP rule among the model's
rules), `conductor host --help` and the Settings card's words.

## Pull requests

1. Change 1 on its own: `internal/session` (`input.go`, `write.go`,
   the read, open and chat paths), `internal/hostagent/peer.go`
   (`inputError`), the tests above, with one test each for a refused
   read, open and chat post.
2. Changes 2, 3 and 4 together: `internal/signal/hosted.go`,
   `internal/api/ws_host.go`, `internal/hostagent` (`agent.go`,
   `liveness.go`, `peer.go`, `queue.go`), docs; they share the host's
   read loop, the lanes and the tests that time the going against the
   queues; the older-host test is part of it, not after it.
3. Change 5 on its own: `internal/hostagent/agent.go` (`controlURL`,
   `Options.Insecure`, the dial's redirect check), `uplink.go`,
   `internal/cli/host.go`, the admin client in `internal/cli` (`up`,
   `crews`), `internal/config`, `desktop/src/settings.ts`,
   `web/app/utils/desktop.ts`, `web/app/pages/settings.vue`, the specs.

In that order, each from `main` after the one before merged. Each pull request's body names this plan's last reviewed
commit apart from the code's.
