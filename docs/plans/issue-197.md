# Plan: a Codex member with an async question pending reads as working (#197)

Branch `issue-197`. Reviewed before any code: it changes how the attention
state machine reads Codex's window title, with the screen model as the
second witness. One pull request.

## What happens, and why

Codex has two kinds of question. A blocking one (a command approval, a form
it waits on) stops its turn until the person answers. An async one
(`request_user_input_async`) is posted and the turn goes on: Codex keeps
working, and the question waits in a collapsed row under its status line
for 30 s, then resolves by itself.

Codex writes the same window title for both: `[ ! ] Action Required`,
blinking with `[ . ] Action Required` (Codex `tui/src/chatwidget/status_surfaces.rs`,
`TERMINAL_TITLE_ACTION_REQUIRED_PREFIX`; `tui/src/bottom_pane/mod.rs`
`terminal_title_requires_action`: unanswered async questions, or a view that
requires action). Since #186 a Codex member's title is its state
(`session.Options.WorkingTitle`, `readTitle` in `internal/session/local.go`):
a title that does not match is read as the agent showing something for a
person, and a wait standing then stands (the settle timer is disarmed). Codex
rings its bell as the async question arrives (seen in the plan of #186), so
the member shows **Needs you**, and it stays so until the title says Working
again: when the question is answered, or 30 s later when it expires
(`bottom_pane/async_questions/state.rs`, `expires_at`). That is #186's one
case the title cannot tell, left for a look at the screen.

## What Codex draws

While async questions are pending and collapsed, the bottom pane draws a
summary row (`tui/src/bottom_pane/questions.rs`, `question_summary`; the
snapshot `question_tests__question_collapsed_countdown.snap`):

```
  ? 2 questions · 15s
    ⇧← to answer
```

The first line is two spaces, `? `, the count and `question` or `questions`,
then, only within the last 20 s of the 30 s (`AsyncQuestions::countdown`),
` · ` and the seconds left as `15s`. The second line, the key hint, is drawn
only while Codex has an edit binding for it. Once the person opens the
questions (the editor expands) the row goes and the editor shows; the title
still says Action Required, and now the person is at work on it.

A blocking question draws its dialog instead: the approval's rows end with
`press enter to confirm or esc to cancel` (the catalog's `questions` for
Codex, `anytime: true`, match them on the screen's tail).

## The change

**Two witnesses, kept apart.** The title stays what it is today: `titleWorking`
is the last title read, matched against `WorkingTitle`, and nothing else
writes it. Beside it the session keeps `asyncShowing`: whether the last
completed frame of the screen model shows Codex's async summary in its
bottom pane and no blocking question. What the attention rules take for "the
title says the agent works" is the two together: the title matches, or the
title does not match and `asyncShowing` is true. Every place that reads
`titleWorking` for that meaning (the working path of `readTitle`, the arming
of the settle timer in `settleLocked`, the settle's own check in
`setAttentionLooked` under `onlySettling`) reads the pair. A wait the title
cannot settle today (a held turn, the trust question, a question the session
holds on the screen, `Attention.Asked` under its own rules) is as untouched
by the pair as by the title alone: the eligible waits are exactly
`titleRules`' and `settleLocked`'s, whatever their source (the API's, a
hook's, the bell's, OSC's), with no rule of their own for the async case.

**What counts as the summary showing.** The screen is read where the asked
questions are read from: `s.asked`, the screen model watcher, through
`Look`/`Whole` and `DrawnAt`, a whole frame drawn at the current width with
nothing cut. The frame shows the summary when, in its last six rows, a row
matches `asyncQuestion` (the catalog pattern) and the next non-blank row is
Codex's key hint, ending in `to answer`, and no row below the hint but the
composer's and blank ones. That is where Codex's bottom pane draws the
collapsed questions, and the only place: a blocking view (an approval, a
`request_user_input` form, an MCP form) replaces the summary in the pane,
and a transcript line that happens to read `? 1 question` is never followed
by the hint in the pane's rows. A frame that shows the summary row without
the hint, or elsewhere than the pane, is ambiguous and counts as not
showing: the wait stands, as today.

The frame must also show no blocking question, checked against the same
frame text: the startup patterns (`s.lastStartup(whole)`, while the startup
watch lasts, which is what `startupOnScreen` checks) and the anytime
patterns (`s.askedQuestions`, each `q.words(whole)` at the frame's width, as
`fireAsked` matches them), whether or not the watcher has fired for them yet.
Codex's approvals are anytime questions; a form of Codex's that no pattern
knows is caught by the pane rule above (it replaces the summary).

**Where the words come from.** A catalog signal field, `asyncQuestion`, an
RE2 pattern (at most `maxSignalPattern` bytes, compiled as `workingTitle` is
by a twin of `catalog.CompileWorkingTitle`) for the summary row. Codex's
default entry gets `^\s*\?\s+\d+\s+questions?(?:\s+·\s+(?:\d+s|\d+:\d{2}))?\s*$`:
the row as Codex draws it today (`15s`) and as 0.161 drew it (`0:05`). The
hint row is Conductor's own rule (`\bto answer\s*$`), not configurable.
It travels as `workingTitle` does: `internal/api/sessions.go` compiles it
into `session.Options.AsyncQuestion`, the Agents form keeps it as it keeps
`workingTitle` (`web/app/utils/agentForm.ts` `signalOut`), the catalog's JSON
and `docs/protocol.md` name it. No frame or message changes.

**One commit path, serialized with the pump.** Everything the screen says
about the async question is committed in one place: the look that follows a
feed of the screen model in `pump`, which is where a title and a row read
in one chunk meet the screen (`scanOutput` precedes `s.asked.Feed`). A
title that does not match never clears a wait on the screen's say-so by
itself: `readTitle` records the raw title as today and, when the title does
not match, disarms the settle timer as today; the look that follows in the
same pump pass, or the next, decides with the frame it has.

The pump holds one lock, `chunkMu`, from before `scanOutput` to after the
feeds of a chunk, and every async-derived commit takes it too: the look
(already inside it, in the pump), and the settle timer's fire. A settle
that fires while a chunk is half processed waits until the chunk's bell has
been scanned and its frame fed, then decides on that frame, never on the
one before it. `chunkMu` is held for a scan and a feed only, no I/O, so the
timer waits a few microseconds at most; it is taken before `s.mu`, never
inside it, and the look takes `s.mu` under it for the transitions, as
`setAttentionRead` does.

**The look.** After the feeds, under `chunkMu`, the session takes the frame
(`Look`: text, settled, width; `DrawnAt`: uncut). `asyncShowing` is set
true only when the frame is whole (settled and uncut) and shows the
summary in the pane's shape with no blocking question; it is set false for
every other frame, a half frame and a frame clipped by a window made
smaller among them, not left as it was: the evidence is the latest frame
or nothing. After a resize, until Codex erases and redraws the whole
screen, there is no evidence and the wait stands, as today. The look acts
on transitions only:

- false to true, with the last title not matching: the pair now says the
  agent works, and the title is weighed as a matching one would be, with
  `titleRules`, in the turn the frame was read: a wait younger than its
  `TitleSettle` is left to the settle timer, armed for what is left of it; an
  older one gives way, here and now, on this frame. One attention event at
  most, as a Working title's.
- true to false (the summary replaced by a view, the questions expired, the
  editor opened, a half frame), with the last title not matching: the settle
  timer is disarmed and the wait showing stands. A wait raised by the view
  then (the bell of an approval, a hook's report) is not settled by anything
  the screen said before.

**Fresh evidence, and what makes it stale.** The look records the frame it
judged by the feed that completed it (the watcher's feed count). Three
things make that evidence stale for an async-derived clear, whatever the
frame still shows: a blocking signal scanned since (a bell, the one
`unlessWaiting` discards because a wait shows included, a hook's or the
API's `needs_input` report, a question asked or fired), a resize since
(`sized` changes the screen model under `s.mu` with no feed), and a feed
that completed a frame since. The session keeps the feed count at the last
blocking signal and at the last resize, and the clear requires the judged
frame to be newer than both: a frame completed after the bell and after the
resize, drawn by the program, that shows the summary again. A chunk that
only rings, or only writes a title, feeds the screen nothing new and renews
no evidence; the next frame the program draws does, or does not. Codex rings
before it draws an approval's overlay (the plan of #186, `title_test.go`
covers the separate reads): the bell makes the evidence stale, the overlay's
frame shows no summary, and nothing clears the wait in between. A resize
sets `asyncShowing` false where it changes the model (`sized`), and the
settle rejects a snapshot taken before it.

**Revalidation at the settle.** `settled` takes `chunkMu`, then runs the
settle under `onlySettling`, which today requires the last title to still
say working. With the pair it requires the title to match, or the summary
showing on fresh evidence as defined above, read as it is at that moment,
never a value stored earlier: a form that replaced the summary between the
arming and the fire has flipped `asyncShowing` by then; a bell that came
between made the evidence stale until a newer frame shows the summary
again; a resize did the same. Codex writes its title only when the text
changes, so an unchanged `[ ! ] Action Required` over a new blocking form
is the ordinary case, and this is what keeps the form's wait.

**Reconciling the timer, not only transitions.** Attention entries are
recorded on state changes alone (`setAttentionRead`, one event per
transition, no token). The settle timer is reconciled apart from that:
after every title read and after every look, whether or not `asyncShowing`
changed, the session asks whether an eligible wait shows (`titleRules`' and
`settleLocked`'s: `needs_input` with an id, not held, not a question the
session holds) and whether the pair says working on fresh evidence; if so
and the timer is not armed, it arms it for what is left of the wait's
`TitleSettle` (a wait older than that settles then and there); if not and
the timer is armed, it disarms it. Codex blinks the title (`[ ! ]`, `[ . ]`,
once a second): each title read that does not match disarms the timer as
today, and the reconciliation that follows the read re-arms it while the
summary shows on fresh evidence, so the wait settles after its second as
under a Working title; frames that keep the summary (true to true) re-arm
it as well. A title-only chunk feeds the screen nothing, and reconciles on
the last completed frame's evidence, fresh or stale as above.

**What is left as it is.** Hosted sessions read no titles and no screen
for this (today's rule). The 30 s expiry needs no timer of Conductor's: when
the questions expire the row goes (a negative transition, no change to the
wait showing) and the title says Working again, read as today.

## Interleavings and failure paths

- Hook report (needs_input, the API's) under Action Required with the
  summary and hint on screen (`TestActionRequiredHoldsAWaitUntilTheTitleSaysWorking`'s
  case, which this changes): the report raises the wait; the next frame, or
  the title read, finds the pair working; the wait settles after its
  `TitleSettle`, at the settle revalidated against the screen. Needs you
  shows for up to that second, as for every title-settled wait.
- Title first, row later, output going on (no quiet): the title alone
  disarms nothing new (no wait yet) or keeps a wait; the feed that completes
  the frame with the summary and hint flips `asyncShowing` and weighs the
  title again; the wait settles. The quiet-time watcher (`expire`,
  `missAsked`) plays no part.
- Title and row in one chunk: `scanOutput` reads the title as non-working
  (it never looks at the screen); the feed that follows in the same pump
  pass, under the same `chunkMu`, completes the frame and flips
  `asyncShowing`: the look weighs the title as working, in the same pass.
- The settle timer fires between a chunk's scan and its feed, the chunk
  carrying a blocking form and its bell: the fire waits on `chunkMu`; the
  bell changes nothing (a wait shows); the feed flips `asyncShowing` to
  false and disarms the timer; the fire, in its turn, finds the timer
  disarmed and no evidence: nothing settles, the wait stands over the form.
- The bell in one chunk, the form's overlay in a later one (Codex rings
  first), the timer firing between: the bell's chunk feeds nothing new and
  makes the evidence stale; the fire finds the summary's evidence older than
  the bell and settles nothing; the overlay's frame then shows no summary.
  A bell for something else, the summary persisting: the next frame the
  program draws shows it again, the evidence is fresh, and the wait settles
  after its second.
- A resize before the next feed, the timer firing: `sized` set
  `asyncShowing` false and moved the resize mark; the fire finds no fresh
  evidence; the first whole frame drawn after the resize looks again.
- Blinking `[ ! ]`/`[ . ] Action Required` over the summary, an eligible
  wait younger than `TitleSettle`: each title disarms, the reconciliation
  after it re-arms on the summary's fresh evidence; the wait settles after
  its second, as under a Working title.
- A half frame, or the screen clipped by a window made smaller, after the
  summary showed: the look sets `asyncShowing` false (no evidence) and
  disarms the timer; a blinking Action Required title read later changes
  nothing (as today); the first whole frame looks again.
- Summary showing, then a blocking view (an approval, or a form no pattern
  knows) replaces it under an unchanged title: the frame flips
  `asyncShowing` to false and disarms the timer; the view's wait, raised by
  its bell or report, stands. A timer already armed for an earlier wait
  finds, at its fire, the title not matching and the screen without the
  summary: nothing settles.
- Summary showing with an approval dialog on the same frame (both drawn):
  the anytime pattern matches the frame: not showing; the approval's wait
  stands. Answered, the dialog goes; a frame with the summary alone flips
  it back.
- Transcript text `? 1 question` on a row of the output, under Action
  Required: no hint row in the pane beneath it, or not in the last rows: not
  showing; as today.
- The person opens the editor: the summary goes (negative transition), the
  title stays; a wait standing stands; the person is at the keyboard.
- The questions expire: the row goes (negative transition), then the title
  says Working, read as today.
- Looks per frame and events: a look is bounded by the six rows it reads;
  `asyncShowing` changes at most once per frame, and only a change with a
  non-matching title weighs the title (one `setAttentionRead`, recorded only
  if it changes the state, no token). No loop: the look reads, it writes
  nothing the next feed reads back but the boolean. `chunkMu` orders the
  pump's chunk processing and the timer's fire; both take it before `s.mu`,
  and the hub's broadcast of the chunk happens before it is taken, so no
  viewer waits on it.

## Tests

- `internal/session` (`title_test.go`, the helpers of
  `TestActionRequiredHoldsAWaitUntilTheTitleSaysWorking`: `titled`,
  `codexAction`, `codexWorking`), frames reconstructed from Codex's source,
  under `-race`: (1) the API wait under Action Required with the summary and
  hint drawn: working within the settle, not before; (2) the same with the
  summary and hint in one chunk with the title, and split across chunks with
  output going on and no quiet at all: the same, no `expire` involved;
  (3) the summary without the hint, and the summary mid-screen with the
  composer rows below the hint missing: the wait stands; (4) the summary
  then a blocking form replacing it under an unchanged title before the
  settle: the wait stands, the timer disarmed, and a bell's wait raised for
  the form stands too; (5) the summary with Codex's approval dialog on the
  same frame: stands; (6) a held turn and the trust question under the
  summary: untouched; an `Ask`ed question under it: as under a Working
  title today; (7) the attention entries recorded: one working per
  transition, none for a look that changes nothing, the event bucket
  untouched (as `TestATitleSpendsNoTokenWhenTheBucketIsEmpty` checks the
  title); (8) hosted session: no look; (9) barrier-controlled, with the
  session's test hooks (`beforeScan`, `beforeSettle`, `afterTitle`): the
  settle timer fires while a chunk with a form and its bell is scanned and
  not yet fed (the fire held in `beforeSettle` until the scan has run, the
  feed released after): the wait stands and the timer is disarmed; (10) a
  clipped frame (a resize to fewer rows) after the summary showed, then an
  Action Required title after `TitleSettle`: the wait stands until a whole
  frame shows the summary again; (11) the bell in one chunk and the overlay
  in a later one, the settle timer fired between (held in `beforeSettle`
  until the bell's chunk is fed, released before the overlay's): the wait
  stands; the same bell with the summary persisting and a frame drawn after
  it: the wait settles after its second; (12) a resize (`Resize`) after the
  summary showed, the timer fired before the next feed: the wait stands;
  (13) blinking titles over the summary on unchanged frames, no partial
  frame: the wait settles after its second; (14) a title-only chunk renews
  no evidence: the summary erased, then a title alone: nothing settles.
- `internal/catalog`: the field validated and compiled; Codex's default
  pattern matches the snapshot's row, its singular, the row without a
  countdown and the 0.161 form, and not the editor's lines, the approval's
  rows or a transcript line with more words.
- `internal/api`: a session launched with the entry gets the compiled
  pattern (as `WorkingTitle` is tested).
- e2e: the stub's `async_question` draws the summary as Codex draws it
  today, two rows (`  ? 1 question · 15s`, then the key hint `to answer`),
  the question's words on their own row above, the title at Action Required
  throughout, its status row redrawn meanwhile; `web/e2e/codexworking.spec.ts`:
  `stub async` never shows Needs you (as today); `stub async-ask`, the agent's
  own question while one is pending, shows Needs you for its second and then
  Working while the question is still pending (today it shows Needs you until
  the question resolves: the assertion changes); `stub approve` under the
  same title shows Needs you with its choices until answered.
- `docs/protocol.md`: the Attention paragraph on the async question says
  how it is told; `docs/features.md`'s Codex row names `asyncQuestion`.

## Coverage limits

The frames are reconstructed from Codex's source (the summary from
`question_summary`, the title from `status_surfaces.rs`), not captured from
a live run: the live tier (`make test-live`) with Codex 0.163 is where the
real row and title are seen; run by hand before the release, not here. The
pattern is Codex's today and 0.161's; a wording change in Codex breaks the
detection, and the member reads as it does now (Needs you until the title
says Working), never worse: a frame that is not recognized is a frame that
changes nothing.

## Revision after review round 1 (2026-10-17)

The first version read the override off the title alone, checked blocking
dialogs with `startupOnScreen` (startup questions only, while that watch
lasts), re-read the screen in `missAsked` (the quiet-time pass, 500 ms after
output stops, never while output goes on), limited the recovery to the bell's
waits (Conductor's launch rings no bell for an async question; the waits are
hooks' reports and the agent's own questions), and accepted the summary row
anywhere on the screen. Each is replaced above: the pair of witnesses with
transitions and the settle revalidated against the current frame; both
pattern sets against the frame; a look after every feed of the screen model;
`titleRules`' eligible waits; the summary required in the pane's two-row
shape with no view below it.

## Revision after review round 2 (2026-10-17)

Round 2 found the revalidation still able to clear a blocking wait on stale
evidence: a settle firing between a chunk's scan and its feed decided on the
frame before the chunk, and a half or clipped frame left the earlier
positive standing for `readTitle` to act on. Replaced above: one commit path
(the look after the feed) serialized with the pump by `chunkMu`, which the
settle's fire takes too; `asyncShowing` set false for every frame that is
not whole; `readTitle` never clearing on the screen's say-so by itself; and
the two barrier-controlled tests. The plan's two reviews are spent: the
owner decides whether this revision is reviewed again before code, or the
code's initial review covers it.

## Revision after review round 3 (2026-10-17)

Round 3 (the owner's one extra review) found the evidence still able to go
stale between a bell and the frame that follows it (Codex rings before it
draws an overlay; a bell-only chunk feeds the screen nothing), a resize
bypassing the protection, and the blinking title disarming a timer that
nothing re-armed on frames that kept the summary. Replaced above: fresh
evidence defined by the feed count against the last blocking signal and
the last resize, a bell-only or title-only chunk renewing none; `sized`
invalidating the evidence; the settle rejecting pre-resize snapshots; the
timer reconciled after every title read and every look, apart from event
deduplication; tests 11 to 14. The plan's three reviews are spent.

## A second witness Codex now offers: its program status (2026-10-18)

Looked for while the owner asked for the plan to be worked on again: a
signal of Codex's that tells its two kinds of question apart without a
screen read. There is one, new in Codex since this plan was written.

**What Codex emits.** Since 0.163.0 (released 2026-10-15; the version
installed here; `tui/src/terminal_program_status.rs`, PR openai/codex#52725
of 2026-10-09) Codex writes a terminal program status whenever its
lifecycle state changes, to any terminal (the only condition is that stdout
is one, which Conductor's PTY is; Codex's terminal detection plays no part):

```
ESC ] 7501 ; state=<state>:app=codex[:kind=<kind>] ESC \
```

ST-terminated, once per change (a cache keeps repeats off), re-emitted after
a terminal handoff, and `state=clear` when Codex leaves the terminal.

- 0.163.0 knows `idle`, `working` and `blocked`, and no kind. Its `blocked`
  is `terminal_title_requires_action()` (`chatwidget/status_surfaces.rs`
  `desired_program_status` at `rust-v0.163.0`): a view that requires action
  **or an unanswered async question**. It mirrors the title, flaw included.
- 0.164 (alpha.3 of 2026-10-17, no stable release yet; PR openai/codex#54114
  merged 2026-10-14, "Report terminal completion states and blocking
  reasons") knows `idle`, `working`, `blocked`, `done` and `error`, and
  `blocked` carries `kind=permission`, `kind=question` or `kind=auth`.
  `blocked` is now `program_status_blocked_kind()` (`bottom_pane/mod.rs`):
  a view that requires action, an approval delayed while the person types,
  or async questions **only once the agent is no longer running** (the
  comment says so in those words). A turn that goes on with an async
  question pending reports `working`. That is this issue's distinction,
  made by Codex itself, independently of its title settings (the PR's own
  words).

**What it would replace.** Everything in "The change" above that reads
the screen: `asyncShowing`, the pane rule, the catalog pattern, `chunkMu`,
the evidence freshness against bells and resizes, the timer reconciliation
against blinking titles, and tests 1 to 14 in their screen-reading form.
The status is a state Codex declares, not a frame to be judged whole, so
the questions the three reviews raised (a half frame, a clipped frame, a
bell before the overlay, a resize, a title-only chunk) do not arise: a
chunk carrying the status carries the state; a chunk without one changes
nothing.

**The change, with the status as the witness.**

- `attention.go`'s scanner (`finishOSC`) learns `case "7501"`: the payload's
  `state=` and `kind=` fields (split on `:`; `app=` kept for the record,
  unknown fields ignored, the whole bounded by `maxOSCCapture` as today)
  become an `Event{Source: SourceStatus, Message: state, Kind: kind}`;
  anything but `idle`, `working`, `blocked`, `done`, `error` and `clear`
  is dropped. BEL and ST terminators and a sequence split across chunks
  are the scanner's today.
- `scanOutput` hands a status event to `readStatus(state, kind, turn)`,
  beside `readTitle`. The session keeps `statusSeen`: once a status has
  been read in the session, the window title is no longer weighed
  (`readTitle` returns at once): Codex 0.163 and later says in the status
  what the title said, and for 0.164 says it right. Sessions of agents that
  emit no status (Codex 0.162 and older, Claude Code, the rest) keep the
  title as their witness, unchanged.
- `working` is weighed exactly as a matching title is today: `titleRules`,
  the settle timer for a wait younger than `TitleSettle`, the settle's
  `onlySettling` check reading the last status instead of the last title.
  `blocked`, `idle`, `done`, `error` and `clear` are weighed as a title that
  does not match: a wait standing stands, the settle timer is disarmed.
  `done` is not Conductor's done: the turn's end stays the notify hook's
  (`agent-turn-complete`), as today.
- Open to the owner (question 2 below): whether `blocked` with a kind also
  **raises** a wait when none shows (`needs_input`, source `status`, the
  message naming the kind: "Codex waits for a permission"), which would
  catch a form no catalog pattern knows (an MCP elicitation, an auth
  prompt) the moment Codex draws it. Without it, `blocked` only keeps the
  waits the bell, the hooks and the patterns raise, as the title does.
- No catalog field: the status names its own program (`app=codex`) and
  its meaning is Codex's protocol, not a per-agent pattern. `docs/protocol.md`
  describes the new source; `docs/features.md`'s Codex row names it.
  `web/app/utils/protocol.ts` gains the source name if the attention
  source is typed there (checked at implementation; no new frame or
  message).
- Hosted sessions read no status, as they read no title.

**Versions.** With Codex 0.164 the stale Needs you of this issue is gone.
With 0.163.0 the status mirrors the title and nothing changes for the
person: the stale Needs you lasts, as today, at most the 30 s of the
question's life. With 0.162 and older there is no status and the title
rules as today. Codex's npm package publishes its alphas (`@openai/codex@alpha`
at 0.164.0-alpha.3 on 2026-10-17); the stable 0.164 is expected on
Codex's roughly weekly cadence (0.163.0 came 2026-10-15).

**Tests.**

- `internal/session` scanner tests: the 0.163 shape (`state=blocked:app=codex`)
  and the 0.164 shape with a kind, BEL- and ST-terminated, split between two
  reads at every byte, an unknown state dropped, a payload cut at
  `maxOSCCapture` dropped, `state=clear`.
- `internal/session` under `-race`, the helpers of
  `TestActionRequiredHoldsAWaitUntilTheTitleSaysWorking` extended with a
  `codexStatus(state, kind)` writer: (1) an API wait under an Action Required
  title with `state=working`: working after its `TitleSettle`, not before,
  and the attention entry's source is `status`; (2) the same wait under
  `state=blocked:kind=question`: stands until `working`; (3) the title read
  after a status is not weighed: a Working title under `blocked` settles
  nothing, an Action Required title under `working` disarms nothing;
  (4) 0.163's kindless `blocked` with the summary row on screen: the wait
  stands (the version's limit, asserted so a change shows); (5) `blocked`
  raising a wait, if the owner takes question 2; (6) a held turn, the trust
  question and an `Ask`ed question under `working`: untouched, as under a
  Working title; (7) the event bucket untouched (as the title's test);
  (8) hosted: nothing read; (9) a status in the same read as a bell for an
  approval: the bell's wait stands under `blocked`, and under `working`
  (an approval Codex rang for and then answered by the person before the
  status changed) gives way after its second, as a title would have it.
- e2e: `web/e2e/stub-agent.sh` writes the status as Codex 0.164 does, beside
  its title: `working` through a turn, `blocked:kind=permission` while
  `stub approve` waits, `blocked:kind=question` while `stub ask` waits,
  `working` while `stub async` has its question pending, `idle` at rest;
  `STUB_STATUS=0.163` writes the kindless 0.163 shape, `STUB_STATUS=` none.
  `web/e2e/codexworking.spec.ts`: `stub async-ask` shows Needs you for its
  second and then Working while the async question is still pending (the
  assertion that changes), `stub approve` as today, and the two older
  shapes keep today's behaviour.
- A live check with Codex 0.164 (alpha until the release) on the live tier:
  the status bytes captured in a Conductor session for a turn, an approval
  and an async question, kept as the fixtures' source; the plan's frames are
  otherwise reconstructed from Codex's source, as before.

**What this plan does not change.** The screen-read plan above stays
written down. It works for every Codex version, at the cost the three
reviews measured; the status works for 0.164 and later at a fraction of it.
The owner chooses (question 1).

## Questions for the owner (2026-10-18)

1. **Which witness?** The program status (0.164 and later; 0.163 and older
   as today), or the screen row (every version, the plan above, two
   findings still fixed-unverified after three reviews)? Recommended: the
   status.
2. **Should `blocked` raise a wait of its own** when nothing else has, with
   the kind as its words, or only keep the waits raised by the bell, the
   hooks and the patterns, as the title does? Raising is a small addition
   here and catches dialogs no pattern knows; it is also beyond the issue
   as written. Recommended: raise it, in this PR, as one more case of the
   same reader.
3. **When?** Code now against the 0.164 alpha's documented sequence and
   check live when 0.164 ships, or wait for the stable release first?
   Recommended: now.
4. **Review spending.** The three plan reviews are spent and this revision
   is a different design: one bounded plan review of it before code, or
   straight to code with the initial code review covering the design?
   Recommended: one plan review (the state machine is the part reviews
   have caught things in).
5. **The async question itself.** Conductor rings no bell for it and the
   member will read as working; the person has 30 s to notice it in the
   terminal before it resolves by itself. Leave it so (the issue's
   intent), or show something quieter than Needs you? Recommended: leave
   it so; a quieter signal would be its own issue.

## OSC 9 as Codex's notification method (the owner's question, 2026-10-18)

Facts, Codex 0.163.0 (`tui/src/chatwidget/notifications.rs`,
`tui/src/notifications/osc9.rs`, `tui/src/tui.rs`): with
`tui.notification_method="osc9"` Codex writes `ESC ] 9 ; <text> BEL` where
the bell rings today, one per notification, coalesced per redraw (a pending
notification of lower priority gives way to a higher one; the turn's end is
the lowest), posted at a draw, under the same focus condition as the bell.
The texts: `Approval requested: <command, 30 graphemes>` (an exec approval),
`Codex wants to edit <path or N files>` (an edit approval), `Approval
requested by <server>` (an MCP elicitation), `Plan mode prompt: <title>`
(a blocking `request_user_input` form, the first question's header),
`Question: <title>` (an async question), and for `agent-turn-complete` the
response's first 200 graphemes, or `Agent turn complete`. Conductor's
scanner reads OSC 9 today (`finishOSC`, source `osc`) and `scanOutput`
weighs it as a wait with words of its own, which replace a wordless bell's.

What it gives: words, and the kind at arrival. A Codex wait would read
"Approval requested: ls -la" instead of "terminal bell"; with
`async-question` back in `tui.notifications`, an async question's arrival
would be known as one ("Question: ..."), the thing a quieter signal
(question 5) would be built from, told apart from a blocking form by its
text. What it does not give: a state. A notification says a thing arrived,
not that it was answered or expired; this issue's stale Needs you is a wait
kept by the title after the person answered, and no notification ends it.
The turn's end over OSC 9 would read as a wait too, unless dropped from the
list (the notify hook reports it already) or told by its text, which has no
fixed prefix.

So OSC 9 complements the program status; it does not replace it: the status
tells working from blocked, OSC 9 would tell what blocks, in words, and
when an async question arrived. Taking it would change the launch
(`tui.notification_method="osc9"`, `tui.notifications` with `async-question`
and without `agent-turn-complete`), `scanOutput`'s weighing of OSC 9 for
Codex (a blocking text raises a wait with words; `Question:` records the
arrival without one), the e2e stub, every Codex wait's words in the
workbench, and `docs/protocol.md`; the live tier would see the real texts
first.

6. **OSC 9.** Switch Codex from the bell to OSC 9 notifications in this PR,
   as its own issue after this one, or not at all? Recommended: its own
   issue after this one, since it changes what every Codex wait says and
   is not needed for the fix; the status alone closes this issue.
