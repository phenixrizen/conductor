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

**The rule.** A title that does not say Working is read as working all the
same when the screen shows the async question's summary row and no blocking
question: Codex works on, with a question a person may answer in passing.
It is read as before (a wait stands) when a blocking dialog is on the screen,
when the editor is open (no summary row), or when neither is there.

**Where the words come from.** A catalog signal field, `asyncQuestion`, an
RE2 pattern (at most `maxSignalPattern` bytes, compiled as `workingTitle` is
by `catalog.CompileWorkingTitle`'s twin) matched against the screen's rows.
Codex's default entry gets
`^\s*\?\s+\d+\s+questions?(?:\s+·\s+\d+s)?\s*$`. It travels as `workingTitle`
does: `internal/api/sessions.go` compiles it into `session.Options`, the
Agents form keeps it as it keeps `workingTitle` (`web/app/utils/agentForm.ts`
`signalOut`), the catalog's JSON and `docs/protocol.md` name it. No frame or
message changes.

**Where it is read.** `readTitle` keeps its shape. Its `working` becomes:
the title matches `WorkingTitle`, or the title does not match and the
session's screen shows the async row with no blocking question
(`asyncQuestionOnScreen`: `Screen.Rows()` of the settled screen matched
row by row against `Options.AsyncQuestion`, and `startupOnScreen()` false,
which is how a startup or anytime question on the screen is known today).
The rest of `readTitle` is unchanged: the working path with `titleRules`
(`unlessFresh` leaves a wait younger than its `TitleSettle` to the settle
timer), the non-working path disarms the timer.

**The frame after the title.** The row and the title arrive in separate
chunks, in either order, and the row stays while the title does not change
(Codex writes its title only when the text changes). So the per-frame pass
that already looks at the settled screen for a question that went away
(`missAsked`) also asks: is the last title read a non-working one, is a
bell's wait standing (`needs_input` from `SourceBell`, no held turn, no
question the session holds), and is the async row on the screen with no
blocking question? Then the title is weighed again as working, through the
same working path and rules, so the wait settles as a Working title would
settle it. A blocking dialog drawn later (the person opens the editor, or a
real approval comes) is a frame with no async row: the next title or bell
is read as today.

**What is left as it is.** A question the agent asked through its hooks
(`Attention.Asked`) is never touched by the title (today's rule). Hosted
sessions read no titles (today's rule). An expanded question editor shows
Needs you, since the person is answering. The 30 s expiry needs no timer of
Conductor's: when the questions expire the row goes and the title says
Working again, which is read as today.

## Interleavings and failure paths

- Bell, then row, then title Action Required: the bell raises the wait;
  the row changes nothing on its own; the title is read as working through
  the screen; the wait settles after its `TitleSettle`. Needs you shows for
  up to that second, as for every title-settled wait.
- Title first, then row: the title is read as non-working (no row yet) and
  disarms the settle timer; the frame that draws the row weighs the title
  again, as working; the wait settles.
- Row and title, then an approval dialog while the question is still
  pending: the frame with the dialog has the row too, but
  `startupOnScreen()` is true, so the title is not read as working; the
  approval's wait stands. The approval answered, the dialog goes; the row
  alone is read as working again.
- The person opens the editor: no row; the title stays Action Required;
  nothing is re-read; the wait raised by the bell (if still standing) stays,
  and the person is at the keyboard anyway.
- The questions expire: the row goes, the title says Working; `readTitle`
  as today.
- A false row: a program printing `? 1 question` on a line of its own
  under an Action Required title. Only Codex's entry carries the pattern,
  and only a non-working title opens the look; the cost of a false match is
  a wait settled a second after a bell, the same as a stale Working title's.
- The screen clipped or mid-frame (`Screen.Settled()` false): no look;
  the next settled frame looks.

## Tests

- `internal/session`: frames reconstructed from Codex's source (the title
  sequences of `status_surfaces.rs`, the summary row of
  `questions.rs`): (1) bell, row, Action Required title: the session shows
  working within its settle; (2) title before row: the same, settled by the
  frame; (3) row with the approval dialog on screen: `needs_input` stands;
  (4) the row alone with a Working title: no change; (5) the title alone,
  no row: the wait stands, as today; (6) a hooks-asked question is untouched.
  Under `-race`.
- `internal/catalog`: the field is validated and compiled; Codex's default
  pattern matches the snapshot's row, its singular, and the row without a
  countdown, and not the editor's lines or the approval's.
- `internal/api`: a session launched with the entry gets the compiled
  pattern (as `WorkingTitle` is tested).
- e2e stub: `stub ask-async <words>` rings the bell, draws the summary row
  with a countdown and sets the title to `[ ! ] Action Required`, keeps
  working (its working title thread stays), then clears both after 5 s; a
  Playwright spec sees the Codex member as working, never Needs you past
  the settle, and a `stub approve` under the same title as Needs you.
- `docs/protocol.md`: the Attention paragraph on the async question says
  how it is told; `docs/features.md`'s Codex row names `asyncQuestion`.

## Coverage limits

The frames are reconstructed from Codex's source, not captured from a live
run: the live tier (`make test-live`) with Codex 0.163 is where the real
row and title are seen; it is run by hand before the release, not here.
The pattern is Codex's today; a wording change in Codex breaks the
detection, and the member reads as it does now (Needs you until the title
says Working), never worse.
