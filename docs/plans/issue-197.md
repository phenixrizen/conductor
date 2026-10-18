# Plan: a Codex member with an async question pending reads as working (#197)

Branch `issue-197`. Reviewed before any code: it changes how the attention
state machine learns that Codex works or waits, with a new source. One pull
request. The design below is the owner's choice of 2026-10-18 (the decisions
are recorded at the end); the screen-reading design the first three reviews
examined is summarized in the appendix and kept whole in the git history
(cb52293).

## What happens, and why

Codex has two kinds of question. A blocking one (a command approval, a form
it waits on) stops its turn until the person answers. An async one
(`request_user_input_async`) is posted and the turn goes on: Codex keeps
working, and the question waits in a collapsed row under its status line
for 30 s, then resolves by itself.

Codex writes the same window title for both: `[ ! ] Action Required`,
blinking with `[ . ] Action Required` (`tui/src/chatwidget/status_surfaces.rs`,
`TERMINAL_TITLE_ACTION_REQUIRED_PREFIX`; `tui/src/bottom_pane/mod.rs`
`terminal_title_requires_action`: unanswered async questions, or a view that
requires action). Since #186 a Codex member's title is its state
(`session.Options.WorkingTitle`, `readTitle` in `internal/session/local.go`):
a title that does not match is read as the agent showing something for a
person, and a wait standing then stands (the settle timer is disarmed). So a
wait raised while an async question is pending (a hook's or the API's
`needs_input` report, the agent's own question, an approval the person then
answers) shows **Needs you** until the title says Working again: when the
async question is answered, or 30 s later when it expires
(`bottom_pane/async_questions/state.rs`, `expires_at`). Conductor's launch
rings no bell for the async question itself (`tui.notifications` without
`async-question`, #186); the stale wait is another signal's, kept by the
title.

## What Codex emits: its program status

Since 0.163.0 (released 2026-10-15, the version installed here; PR
openai/codex#52725 of 2026-10-09; `tui/src/terminal_program_status.rs`)
Codex writes a terminal program status whenever its lifecycle state changes,
to any terminal: the only condition is that stdout is one, which Conductor's
PTY is; Codex's terminal detection plays no part. The sequence:

```
ESC ] 7501 ; state=<state>:app=codex[:kind=<kind>] ESC \
```

ST-terminated, written once per change (a process-wide cache keeps repeats
off), re-emitted after a terminal handoff, and `state=clear` when Codex
leaves the terminal.

- **0.163.0** knows `idle`, `working` and `blocked`, and no kind. Its
  `blocked` is `terminal_title_requires_action()`
  (`chatwidget/status_surfaces.rs` `desired_program_status` at
  `rust-v0.163.0`): a view that requires action **or an unanswered async
  question**. It mirrors the title, flaw included.
- **0.164** (alpha.3 of 2026-10-17, no stable release yet; PR
  openai/codex#54114 merged 2026-10-14, "Report terminal completion states
  and blocking reasons") knows `idle`, `working`, `blocked`, `done` and
  `error`, and `blocked` carries `kind=permission`, `kind=question` or
  `kind=auth`. `blocked` is `program_status_blocked_kind()`
  (`bottom_pane/mod.rs`): a view that requires action, an approval delayed
  while the person types, or async questions **only once the agent is no
  longer running** (the comment says so in those words). A turn that goes
  on with an async question pending reports `working`; `done` and `error`
  are kept until a key, a paste or a click acknowledges them; an
  interrupted or replayed turn reads `idle`. This is the distinction the
  issue needs, made by Codex itself, independently of its title settings
  (the PR's own words).

The status is a state Codex declares, not a frame to be judged whole. A
chunk carrying it carries the state; a chunk without one changes nothing.
The questions the earlier reviews raised about a screen read (a half or
clipped frame, a bell before an overlay, a resize, a title-only chunk) do
not arise.

## The change

**The scanner.** `internal/session/attention.go`'s `finishOSC` learns
`case "7501"`. The payload after the number is `key=value` fields joined by
`:`; `state` is required and must be one of `idle`, `working`, `blocked`,
`done`, `error`, `clear`, `kind` is optional and must be one of
`permission`, `question`, `auth` when present, `app` is kept with the event
and any other field is ignored. A payload the scanner does not understand (a
malformed one, an unknown state or kind, one cut at `maxOSCCapture`,
`dropped`) yields `Event{Source: SourceStatus, Message: "unknown"}`: the
session learns that Codex declared a state it cannot read, which is not the
same as no status, and treats it conservatively below. BEL and ST
terminators and a sequence split across reads are the scanner's today. The
event is `Event{Source: SourceStatus, Message: state, Kind: kind, App: app}`
(two fields added to `Event`, empty for the other sources). A new source
name, `SourceStatus = "status"`, beside `bell`, `osc` and `title`.

**The session.** `scanOutput` hands a status event to `readStatus(state,
kind, turn)`, beside `readTitle`. The session keeps two more facts under
`s.mu`: `statusSeen`, set by the first status read in the session and never
cleared, and `lastStatus`, the latest status read (state and kind, `unknown`
for one not understood). `lastStatus` is Codex's current state and persists
across Conductor's turns: Codex writes a status only when its state changes
(0.164 keeps `working` across completed turns with background work or an
active goal, and its process-wide cache sends no second copy), unlike the
animated title, which is written again and again. So the state is never
forgotten at a turn boundary; the rules that gate what a status does
(`unlessTurnEnded` among them) apply when it is read and when a wait is
settled, not by resetting it.

- `working` is weighed exactly as a matching title is today: `titleRules`,
  one `setAttentionRead(AttentionWorking, "", SourceStatus, ...)` per read,
  the settle timer for a wait younger than its `TitleSettle`, no token, no
  bell pace. The settle's own check (`settled` → `setAttentionLooked` under
  `onlySettling`) and `settleLocked`'s arming read one predicate,
  `agentSaysWorking()`: `lastStatus` is `working` once `statusSeen`,
  `titleWorking` before. A `working` read just after the turn's done is not
  applied (`unlessTurnEnded`, as a Working title's), but it stays the state:
  the wait raised in the next turn (the Enter, a new report) settles against
  it after its `TitleSettle` with no further emission from Codex. The
  attention entry records source `status`.
- `blocked`, `idle`, `done`, `error`, `clear` and `unknown` are weighed as a
  title that does not match: `agentSaysWorking()` false, the settle timer
  disarmed, a wait standing stands. `unknown` (a state or kind this build of
  Conductor does not know, or a cut sequence) is the conservative reading:
  a `working` read earlier no longer counts, the title stays retired, and
  waits stand until a status Conductor understands says `working`; the
  member reads as it does today under an Action Required title, never worse.
  `done` is not Conductor's done: the turn's end stays the notify hook's
  (`agent-turn-complete`), as today, and a `done` status changes no state by
  itself.
- **A wait the status raised is the status's to withdraw.** A later status
  that is not `blocked` and not `working` (`idle`, `done`, `error`, `clear`,
  `unknown`: a form cancelled, a turn interrupted, Codex gone) withdraws a
  wait whose source is `status` at once, the attention returning to what it
  would be without it (none, or the turn's done); waits of other sources
  (the API's, a hook's, a question the session holds, a bell's) are not
  touched by it. `working` after `blocked` takes the ordinary path: the wait
  gives way after its `TitleSettle`. And a status's wait is withdrawn as an
  OSC notification's is today: by the person typing into the session
  (`rawKeys`) and by the screen going quiet with nobody answering
  (`screenQuiet`), `SourceStatus` added beside `SourceOSC` in both.
- **`blocked` with a kind raises a wait** (the owner's decision 2):
  `setAttentionRead(AttentionNeedsInput, words, SourceStatus, kind, nil,
  rules, turn)` with the words "Codex waits for a permission", "Codex asks a
  question", "Codex needs you to sign in" for `permission`, `question`,
  `auth`, and the rules an OSC notification's wait has in `scanOutput`
  today: words of its own that replace a wordless bell's, `unlessTurnEnded`
  (a turn just ended is done, not a question), and a wait that already
  shows with words of the screen's or the agent's (a question the session
  holds, the API's report) kept, since those words are better. A second
  `blocked` while the wait shows changes nothing (one entry per transition,
  as every source). A **kindless `blocked` (0.163.0) raises nothing**: it
  may be an async question, and raising there would make a new false Needs
  you for the version installed today; it only keeps waits, as the title
  does.
- **Reconciliation when a held question goes.** Whatever the rules did with
  a status as it was read, `lastStatus` keeps it, because Codex will not say
  it again. When a question the session holds on the screen is withdrawn
  (`missAsked`, the dialog gone) or answered (its pick taken), or an API
  question is resolved, the session looks at `lastStatus` once, under
  `s.mu`, before it settles on the next state: `blocked` with a kind and no
  wait left showing raises the status's wait then, with its words (an
  approval A replaced by an unrecognized form B whose `blocked` arrived while
  A still showed and was kept out by A's better words: B's wait appears as A
  goes); `working` with an eligible wait left standing arms the settle timer
  as a `working` read would have (a `working` kept out by `unlessOnScreen`
  while A showed is not lost). One reconciliation per withdrawal, no new
  event, no token.
- **The title after a status.** Once `statusSeen`, `readTitle` returns at
  once: Codex 0.163 and later says in the status what the title said, and
  0.164 says it right. Sessions of agents that emit no status (Codex 0.162
  and older, Claude Code, the rest) keep the title as their witness,
  unchanged. A title and a status in one read: both are events of that
  `scanOutput` pass, in output order; the title is not weighed either way
  once the status in the same pass has set `statusSeen`, so the order
  within the chunk does not matter.
- **Scope of the status.** Every local session reads it; no catalog field:
  the sequence names its program (`app=`) and its meaning is the protocol's,
  not a per-agent pattern. A program other than the agent writing it in the
  agent's PTY (a shell command the agent runs) would be read as the agent;
  that is the title's situation today too, and the status is written by TUIs
  that own the terminal.
- Hosted sessions (`conductor host`) read no status, as they read no title.

**What the person sees.** With Codex 0.164 the stale Needs you of this issue
is gone: an approval answered while an async question is pending gives way
to Working after its `TitleSettle`, as it does today when no async question
is pending; a form no pattern knows shows Needs you with the kind's words the
moment Codex draws it. With 0.163.0 nothing changes for the person: the
status mirrors the title and the stale Needs you lasts, as today, at most
the 30 s of the question's life. With 0.162 and older there is no status and
the title rules as today. While an async question is pending the member
shows Working and nothing else (the owner's decision 5); Codex's bell and
OSC 9 notifications stay as they are (decision 6: `tui.notification_method`
stays `bel`).

**Surfaces.** `internal/session` (the scanner, `readStatus`, the predicate,
the source name); `web/app/utils/protocol.ts` types the attention source as
a string already (`source?: string`), so no change there unless a union is
found at implementation; `docs/protocol.md`'s Attention paragraphs describe
the source and the title's retirement after it; `docs/features.md`'s Codex
row names it; `web/e2e/stub-agent.sh` and `web/e2e/codexworking.spec.ts` as
under Tests. No new frame or message; no catalog or API change; no desktop
change.

## Interleavings and failure paths

- The API's or a hook's `needs_input` under an Action Required title with
  `state=working` (the issue's case, `TestActionRequiredHoldsAWaitUntilTheTitleSaysWorking`'s
  first branch, which changes): the wait is raised; the status, read before
  or after it, says working; the wait settles after its `TitleSettle`, the
  settle revalidated against `agentSaysWorking()`. Needs you shows for up to
  that second, as for every title-settled wait.
- An approval under an async question (0.164): Codex rings, draws the
  dialog, writes `blocked:kind=permission`; the bell's wait (or the
  pattern's, with the dialog's words, or the status's, whichever is read
  first) shows; the person answers; Codex writes `working`; the wait gives
  way after its second. Today it stands until the async question goes.
- The same on 0.163.0: `blocked` without a kind, and `blocked` still after
  the answer (the async question pending): nothing settles until `working`,
  when the question goes: today's behaviour.
- A blocking form no pattern knows (an MCP elicitation, a sign-in): on 0.164
  `blocked:kind=auth` raises the wait with its words; answered, `working`
  settles it. On 0.163.0 the bell (`approval-requested` covers MCP forms) or
  nothing, as today.
- A status and the bell in one read, the bell first: the bell's wordless
  wait, then the status's words replace them. The status first: the status's
  wait, then the bell is discarded (`unlessWaiting`). Either way one wait,
  with words.
- `working` read while a question the session holds on the screen shows
  (`Attention.Asked`, an approval matched by the catalog's pattern): the
  question is kept under its own rules (`unlessOnScreen`), as under a Working
  title today; answered, the pattern's `missAsked` lets it go.
- The settle timer fires between a chunk's scan and its feed: the status is
  read in the scan, under `s.mu`, before the feed; the timer's check reads
  `agentSaysWorking()` under `s.mu` too. There is no frame to judge, so the
  feed's timing is immaterial; the chunk lock of the earlier design is not
  needed.
- A blinking title (`[ ! ]`, `[ . ]`) over a `working` status: the title is
  not weighed; the timer armed by the status runs out and settles the wait.
- The turn ends (the notify hook's done) with `blocked` unread or stale: a
  status of the ended turn is that turn's; the next turn begins with none
  (as the title), and a `blocked` read in the new turn is weighed anew.
- Codex exits: `clear`, then the PTY closes; the session's end is as today.
- A resize, a half frame, a clipped frame: no effect; the status is not a
  frame.
- An approval A held on the screen, then an unrecognized form B in its place
  (0.164): B's `blocked:kind=auth` is read while A shows and kept out by A's
  words; Codex does not repeat it; `missAsked` withdraws A as its dialog is
  gone; the reconciliation raises B's wait from `lastStatus`. Answered,
  `working` settles it.
- A form the status raised, cancelled by the person in the terminal (Esc)
  with the turn interrupted: `idle` without `working`; the wait is
  withdrawn at once. Ended by an error: `error`, the same.
- A turn's done (the hook), the person's Enter, a new report raising a wait,
  Codex's status still `working` from before and not written again: the
  wait settles after its second against `lastStatus`.
- `working`, then a `blocked` with a kind added in a later Codex: read as
  `unknown`; a bell's wait for the form stands; the title stays retired; the
  next `working` Conductor understands settles it.
- Events and tokens: a status asks the event bucket for nothing (as the
  title); `working` records an entry only when it changes the state; a
  `blocked` with a kind records one needs_input entry per transition, as a
  bell would.

## Tests

- `internal/session` scanner tests (`attention_test.go`): the 0.163 shape
  (`state=blocked:app=codex`) and the 0.164 shape with a kind, BEL- and
  ST-terminated, split between two reads at every byte, an unknown state or
  kind dropped, a payload cut at `maxOSCCapture` dropped, `state=clear`,
  fields in another order, an unknown field ignored.
- `internal/session` under `-race`, `title_test.go`'s helpers extended with
  `codexStatus(state, kind)`: (1) an API wait under an Action Required title
  with `state=working`: working after its `TitleSettle`, not before, the
  entry's source `status`; (2) the same wait under `blocked:kind=question`:
  stands until `working`, then gives way after its second; (3) the title is
  not weighed after a status: a Working title under `blocked` settles
  nothing, an Action Required title under `working` disarms nothing, the
  blink over `working` settles; (4) 0.163's kindless `blocked`: raises
  nothing, keeps a wait, and the summary row on screen changes nothing (the
  version's limit, asserted so a change shows); (5) `blocked:kind=auth`
  with no bell and no pattern raises needs_input with its words; a second
  `blocked` records nothing more; `working` settles it after its second;
  (6) a held turn, the trust question and a question the session holds on
  the screen (`SourceTrust`, `SourceQuestion`, kept by `unlessOnScreen`)
  under `working`: untouched, as under a Working title; an `Ask`
  (`Local.Ask`, `Attention.Asked`) under `working` is set aside and comes
  back at done exactly as `ask_test.go` has it today; (7) the event bucket
  untouched by `working` (as the title's test) and spent once by a raising
  `blocked`; (8) hosted: nothing read; (9) the status and a bell in one
  read, both orders: one wait with the status's words; (10) a status of
  the ended turn read after the turn's done settles nothing; the next turn
  begins with none; (11) the settle timer fired between scan and feed with
  a `blocked` in the chunk: stands (a barrier-controlled test with the
  session's hooks, `beforeScan`/`beforeSettle`); (12) sessions with no
  status: every existing title test unchanged; (13) a held screen approval A
  replaced by an unrecognized form B, B's `blocked:kind=auth` read once while
  A shows and never again: A withdrawn by `missAsked`, B's wait raised from
  `lastStatus` with its words; the same with `working` read once while A
  shows and an API wait standing: A answered, the wait settles after its
  second; (14) a status's wait (`kind=auth`) followed by `idle`, by `error`
  and by `clear` with no `working`: withdrawn at once, an API wait standing
  beside it untouched; typing withdraws it as a bell's; (15) `done`, then
  the Enter and a new API wait with no new status: the wait settles after
  its `TitleSettle` against the persisting `working`; a `blocked` persisting
  into the new turn keeps the wait; (16) `working`, then a status with a kind
  Conductor does not know: `lastStatus` unknown, a bell's wait stands, the
  Action Required title still not weighed, and `working` again settles it;
  the same for a sequence cut at `maxOSCCapture`.
- e2e: `web/e2e/stub-agent.sh` writes the status as Codex 0.164 does, beside
  its title: `working` through a turn, `blocked:kind=permission` while
  `stub approve` waits, `working` while `stub ask` and `stub ask-wait` spin
  (they do not block: the question is reported and the work goes on, and
  `codexworking.spec.ts` requires it to settle meanwhile), `working` while
  `stub async` has its question pending, `idle` at rest, `clear` at exit;
  two new blocking modes, `stub form <question>` (`blocked:kind=question`,
  a `request_user_input` form drawn and waited on until answered) and `stub
  auth` (`blocked:kind=auth`, a sign-in prompt no pattern knows);
  `STUB_STATUS=0.163` writes the kindless shape, `STUB_STATUS=none` writes
  none. `web/e2e/codexworking.spec.ts`: `stub async-ask` shows Needs you for
  its second and then Working while the async question is still pending
  (the assertion that changes); `stub approve` as today; `stub auth` shows
  Needs you from the status alone, with its words, and Working after the
  answer; `stub form` the same with its question's words; the 0.163 and
  no-status shapes keep today's behaviour for `stub async-ask`.
- `docs/protocol.md` and `docs/features.md` as under Surfaces.
- A live check with Codex 0.164 on the live tier (`make test-live`) when it
  ships, the alpha before if available: the status bytes of a turn, an
  approval and an async question captured in a Conductor session and kept as
  the fixtures' source; until then the frames are from Codex's source at
  `rust-v0.164.0-alpha.3`.

## Coverage limits

The sequence and its semantics are read from Codex's source at the alpha
tag, not captured live; the live tier is where the real bytes are seen,
before the release. A wording change in Codex's protocol (a new state, a
new kind) is dropped by the scanner and reads as no status: the member reads
as it does today, never worse. Codex 0.163.0, installed today, gains nothing
from this change and loses nothing; the fix shows when 0.164 ships.

## Decisions (the owner, 2026-10-18)

Asked as multiple choice after the plan gained the status design:

1. The witness: **the program status** (not the screen row; not parking).
2. `blocked` with a kind **raises a Needs you in this PR**.
3. **Now**, against the 0.164 alpha; the live check when 0.164 ships.
4. **One plan review** of this design before code.
5. An async question pending shows **Working and no signal**.
6. OSC 9 notifications for Codex: **not at all**; the bell stays. (Facts
   gathered for the question: with `tui.notification_method="osc9"` Codex
   0.163.0 writes `ESC ] 9 ; <text> BEL` per notification, texts "Approval
   requested: <command>", "Codex wants to edit <path>", "Approval requested
   by <server>", "Plan mode prompt: <title>", "Question: <title>" and the
   turn's response preview; an arrival signal with words, never a state.)

## Revision after review round 4 (2026-10-18)

The owner's granted review of the status design found five things, folded
in above: a `blocked` or `working` kept out by a held screen question's
better words was lost for good once that question went, since Codex does not
repeat a status (`lastStatus` kept, and reconciled when a held question is
withdrawn or answered); a wait the status raised had no way out when Codex
went idle or failed without `working` (withdrawn by a later non-blocked
status, by typing and by the quiet screen, as an OSC notification's); the
state was reset at Conductor's turn boundary although Codex keeps `working`
across turns and writes it once (it persists; the turn rules apply at read
and settle time); the stub's `stub ask` does not block and must keep
`working`, and test 6 confused `Local.Ask` with the screen-held questions
(a `stub form` mode for the blocking form; test 6 corrected); a status not
understood was dropped as if absent, leaving a stale `working` under a
retired title (read as `unknown`, conservative). The review confirmed the
two findings carried from the screen-row design moot in their original
forms. The plan's four reviews are spent; the owner decides whether this
revision goes to code with the initial code review covering it, or is
reviewed once more.

## Appendix: the screen-reading design (not taken)

The plan's first design (commits 5fa32f1 to cb52293, three reviews on
2026-10-17) read Codex's collapsed async summary row (`  ? N question[s]
[· <n>s]` over a `to answer` hint, `tui/src/bottom_pane/questions.rs`) off
the screen model as a second witness beside the title: `asyncShowing`, set
by a look after every feed of the screen model on a whole frame showing the
row in the pane's shape with no blocking question, cleared on every other
frame; a catalog pattern `asyncQuestion` for the row; a chunk lock
serializing the look with the settle timer; evidence freshness against
bells, resizes and title-only chunks; the timer reconciled after every read.
The reviews found, in turn, the screen read's exposure to half and clipped
frames, stale evidence between a bell and the overlay it precedes, and the
blinking title disarming a timer nothing re-armed; two findings were still
fixed-unverified when the status design replaced it. It worked for every
Codex version at that cost; the status works for 0.164 and later at a
fraction of it, and the owner chose it.
