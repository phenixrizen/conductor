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

**When the screen is looked at.** After every feed of the screen model in
`pump` (`s.asked.Feed`, which `scanOutput` precedes, so a title and a row
read in one chunk meet the screen only here), the session looks once the
frame is whole (`Settled` and `DrawnAt` uncut): a look is a regular
expression over at most six rows, spends nothing from the event bucket and
records nothing by itself. The look sets `asyncShowing` and acts on its
transitions only:

- false to true, with the last title not matching: the pair now says the
  agent works, and the title is weighed again as a matching one would be,
  with `titleRules`, in the turn the frame was read: a wait younger than its
  `TitleSettle` is left to the settle timer, armed for what is left of it; an
  older one gives way. One attention event at most, as a Working title's.
- true to false (the summary replaced by a view, the questions expired, the
  editor opened), with the last title not matching: as a title that does
  not match is treated today, the settle timer is disarmed and the wait
  showing stands. A wait raised by the view then (the bell of an approval,
  a hook's report) is not settled by anything the screen said before.

`readTitle` reads the same pair: a title that does not match arriving while
`asyncShowing` is true (the row drawn before the title) takes the working
path at once, rather than disarming the timer.

**Revalidation at the settle.** `settled` runs the settle under
`onlySettling`, which today requires the last title to still say working.
With the pair it requires the title to match, or `asyncShowing` as recomputed
from the screen as it is at that moment (the same look, on the current
frame), never the value stored earlier: a form that replaced the summary
between the arming and the fire leaves the wait standing, title unchanged
or not. Codex writes its title only when the text changes, so an unchanged
`[ ! ] Action Required` over a new blocking form is the ordinary case, and
this is what keeps it from settling the form's wait.

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
- Title and row in one chunk: `scanOutput` reads the title against the
  previous frame (`asyncShowing` false): non-working, as today; the feed
  that follows in the same pump pass completes the frame and flips it: the
  same outcome, in the same pass.
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
- A half frame (`Settled` false, or `DrawnAt` cut): no look; the next whole
  frame looks. The screen clipped by a window made smaller: the same.
- Looks per frame and events: a look is bounded by the six rows it reads;
  `asyncShowing` changes at most once per frame, and only a change with a
  non-matching title weighs the title (one `setAttentionRead`, recorded only
  if it changes the state, no token). No loop: the look reads, it writes
  nothing the next feed reads back but the boolean.

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
  title); (8) hosted session: no look.
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
