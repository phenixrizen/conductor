# Plan: a Codex member shows Needs you only while Codex waits (#186)

Branch `codex-needs-you`. Reviewed before any code (AGENTS.md, Reviews): it
changes attention's state machine. Unreviewed.

## What happens, and why

A Codex member of a crew can show **Needs you** from early in its work until
its turn ends, while Codex plainly goes on. A crew member does its whole task
in one turn, so for the last member (the todo crew's tester) that is until
the run is complete. Meanwhile the member's handoffs wait too: a handoff is
typed with `UnlessWaiting`, which writes nothing while the session waits.

The cause is that Codex, as Conductor launches it, never says it is at work.
Conductor's launch gives Codex two signals and no more
(`internal/agents/codex.go`, `Inject`, the same in rc.8): `notify` (Codex's
notify program, which reports only `agent-turn-complete`, mapped to `done`)
and the terminal bell (`tui.notification_method="bel"`). The `PreToolUse`
hook that would report `working`, as Claude Code's does, lives in
`~/.codex/hooks.json`. Conductor writes that file only when there is none, and
Codex runs those hooks only with `features.hooks` on and once a person has
trusted them ("Hooks need review"). The owner's `hooks.json` is not
Conductor's: it has three empty lists. So once something sets
`needs_input` during a Codex turn, nothing but the turn's `done` (or a
person's key) clears it. Two things do set it while Codex goes on:

1. **Codex's bell for a question that does not wait.** The model's
   `request_user_input_async` tool posts a question and the turn goes on.
   Codex 0.161 shows it under its status row as "? 1 question · <countdown>"
   and, while its terminal is unfocused (a crew member nobody is looking
   at), rings the bell for it (`Notification::AsyncQuestion`,
   `tui/src/chatwidget/questions.rs`). The bell reaches `scanOutput`, which
   shows `needs_input` "terminal bell" (`source:"bell"`).
   Evidence: the owner's Codex rollout of a todo-crew tester on
   2026-10-02 (`~/.codex/sessions/2026/10/02/rollout-…01a0fd42…jsonl`,
   cwd `test_crew_todo`, Codex 0.159). It called `request_user_input_async`
   1 min 40 s into its turn ("The branches crew/example-todo-app-98c1d030/…
   are absent … Is there a different run or checkout whose branches I
   should use?") and worked 4 more minutes: a patch, a handoff to core,
   tests, then `done`.
2. **The agent's own question that does not wait.** The MCP tool `ask`
   (and `conductor notify --state needs_input`) returns at once. A Codex
   member that asks and goes on keeps `needs_input` (`source:"api"`) until
   its turn ends. With Claude Code, the next `PreToolUse` clears it.

Neither the screen's questions (#121, #192) nor the bell's turn rules (#170)
change this: a bell or a report shows `needs_input` until a state replaces
it, and Codex sends none during a turn.

The owner's run of 2026-10-14 is not in the record this machine's app
keeps. Its server log, run files and Codex rollouts hold no todo-crew run
after 2026-10-07, and no Codex session after 2026-10-12, so which of the two
it was is not known. Both reproduce in a Go test on `origin/main`: a
session with Codex's questions, a prompt's Enter, then the bell and
"? 1 question" (or a `needs_input` report), then 5 s of Codex's status row
redrawn. The session still shows `needs_input` at the end.

## The changes

1. **Codex's window title says what it is doing.** Conductor launches
   Codex with `-c tui.terminal_title=["activity","run-state"]`. Codex 0.161
   then writes its window title (`ESC ] 0 ; <title> BEL`,
   `tui/src/terminal_title.rs`) in one of these forms:
   - "⠋ Working", "⠋ Thinking" or "⠋ Waiting" (a background terminal) while
     a turn runs, the braille frame changing every 100 ms. With animations
     off there is no frame: "Working".
   - "[ ! ] Action Required" while a view that needs a person shows (an
     approval, an MCP form, a `request_user_input` question), blinking with
     "[ . ] Action Required" every second.
   - "⠋ Starting" during its MCP start.
   - "Ready" at rest.

   (`tui/src/chatwidget/status_surfaces.rs`, `bottom_pane/title_setup.rs`;
   to be checked against the real Codex by hand, below.) The workbench shows
   no program's title, so the override costs nothing visible. A user's own
   `tui.terminal_title` is overridden in Conductor's sessions only.
2. **Codex rings no bell for a question that does not wait.** The same
   launch gets `-c tui.notifications=["approval-requested","plan-mode-prompt","agent-turn-complete"]`.
   That is every notification Codex 0.161 has (`notifications.rs`) except
   `async-question`. Approvals, MCP forms (`approval-requested`) and
   `request_user_input` (`plan-mode-prompt`) still ring, and so does the
   turn's end (#121 keeps it from undoing the `done`). Without this change
   the title would clear such a bell within 100 ms, but the bell would still
   be recorded as an attention entry and as a question in the run's chat.
3. **A title that says the agent works is a `working` report.** The
   catalog's signal gains `workingTitle` (RE2, at most 200 bytes, must not
   match an empty title). Codex's built-in is
   `^(?:\S+\s+)?(?:Working|Thinking|Waiting)$`. `session.Options` gains
   `WorkingTitle`, set from the catalog in `internal/api/sessions.go`. The
   `Scanner` reports a window title (OSC 0 or 2) as an `Event` with source
   `title` and the title as its text: control characters dropped, bounded
   by the 256 bytes it captures, a title cut by that bound matching
   nothing. `scanOutput` weighs a title only when `WorkingTitle` is set; a
   title it does not match changes nothing. A matching title is applied as
   `setAttentionRead(AttentionWorking, "", SourceTitle, "", nil,
   unlessTurnEnded|unlessHeld|unlessAsked, read)`:
   - **unlessTurnEnded** (as for the bell): it does not undo a `done` until
     the next Enter or another state. A frame drawn before the turn ended
     and read after its `done` is not a new turn.
   - **unlessHeld** (new): it changes nothing while the session's turn is
     held for a typed text (`s.held`). The held branch of `setAttention`
     records every change it hides as an entry, and the title comes ten
     times a second.
   - **unlessAsked** (new): it changes nothing while the session shows a
     question it holds (`heldSource`: a startup question, a dialog on the
     screen). The screen decides those; this change leaves them alone.

   Everything else gives way to it, as to Claude Code's `PreToolUse`: a
   bell, an OSC notification, the agent's own report with or without
   choices, a screen pattern, the empty state, and `working` itself (no
   change, nothing recorded). A title spends no token of the event bucket
   (it is the session's own observation, like the bell) and records an
   entry only when it changes the state (`recordOwn`). It does not stamp
   the bell's pace (`lastBell`): the bells of a burst after it stay paced
   by the first. `SourceTitle = "title"` is a new attention source:
   `docs/protocol.md` (Attention) says what it means;
   `web/app/utils/protocol.ts` types the source as a string and needs no
   change.

## The events that interact

- E1 a title that matches `workingTitle`, about every 100 ms while Codex
  works.
- E2 a title that does not match (Action Required, Ready, Starting, another
  program's title).
- E3 a bell (an approval, an MCP form, `request_user_input`, the turn's
  end), paced to one change per 500 ms.
- E4 the agent's report through the API: `needs_input` (`ask`, `notify`),
  `working`, `done` (notify's `agent-turn-complete`), `clear`.
- E5 a dialog asked during the work, found on a quiet screen (`fireAsked`).
  A title alone does not break the quiet: the pump feeds neither the trust
  watcher nor the dialogs' watcher a read that only sets the window title
  (`titleOnly`, which already exists for Codex's title spinner). A title in
  the same read as other output feeds them, as it does today.
- E6 a startup question (trust, hooks review, update), `source:"trust"`.
- E7 a person's key, or Conductor's own submission and its Enter
  (`nextTurn`).
- E8 the turn held for a typed text (`holdTyped`, `source:"submit"`).
- E9 the session's end.
- E10 a title split between two reads (`Scanner.Carried`, the read turn).

## The orders they come in, and what must happen

- O1 E1 then E3 (approval): Codex's draw rings the bell before it draws the
  overlay, then its title becomes Action Required (E2), which clears
  nothing. The bell's `needs_input` stands until E5 replaces it with the
  dialog's question.
- O2 E3 then a stray E1 before the overlay's title (a spinner frame written
  between the bell and the overlay's draw; Codex's code writes none, but
  nothing promises it): the bell gives way to `working`. E5 still asks the
  dialog once the screen is quiet. A form the catalog does not know (an MCP
  form, `request_user_input`) shows nothing then. Accepted; the test pins
  that the dialog is still asked.
- O3 E4 `needs_input` (ask) then E1: `working`. The question stays in the
  chat and the activity log, where `askInChat` and `recordOwn` put it.
- O4 E3 (an async question's bell, on a Codex that ignores change 2) then
  E1: `working`. The bell was recorded once; the burst after it is paced.
- O5 E5 (question shown) then E1: nothing changes (unlessAsked). The person
  answers (E7) and `answer` clears it; the next E1 shows `working`. If the
  dialog leaves without a key, `missAsked`'s rules stand, unchanged here.
- O6 E6 then E1 (a title written while Codex starts behind its trust
  question): nothing changes (unlessAsked).
- O7 E8 then E1: nothing is shown or recorded (unlessHeld). The hold ends
  by a person's key; the next E1 shows `working`.
- O8 E4 `done` then E1 (a frame drawn before the turn ended, read after the
  `done`; or Codex going on by itself): nothing changes until the next
  Enter (unlessTurnEnded), as for the bell. After the Enter (E7: a crew's
  next prompt, a person's), E1 shows `working`.
- O9 E1 read in the ended turn and looked at once the next has begun (the
  read turn, `lastTurns`): nothing changes.
- O10 E1 then E4 `done`: `done`. Then a bell (the turn's own): nothing
  (#121).
- O11 E1 many times in a row: one change at most; a title that changes
  nothing records nothing and spends nothing.
- O12 E10: a title split between two reads is applied once whole, with the
  turn its first part was read in (as an OSC notification split today).
- O13 E9: an ended session shows nothing. The pump scans the program's last
  output before it calls `markEnded`, which clears the state in the step
  that ends the session, and scans nothing after. A test pins that a title
  in the last output leaves the ended session with the empty state.
- O14 E2 at any time: nothing changes. In particular Ready after a `done`
  stays `done`, and Action Required raises nothing by itself (out of scope).
- O15 another agent, or a shell, writes "Working" as its title: nothing,
  since only an agent whose catalog has `workingTitle` weighs titles.

## Invariants

- A Codex session never shows `needs_input` from a bell or a report for
  longer than one title frame after Codex's title says it is at work, while
  no question it holds is on its screen.
- A title never hides or clears a question the session holds (`trust`,
  `question`) nor a held turn (`submit`), and never undoes the `done` of the
  turn it was read in.
- A title never writes to the program, never spends a token, and records an
  activity entry only when it changes the state.
- Nothing changes for an agent without `workingTitle`, and the bell's rules
  (#121, #170) stand as they are.

## Out of scope

- Using Action Required as a question of its own. MCP forms and
  `request_user_input` still show as "terminal bell", as today.
- Showing Codex's async question's words. They are drawn only when the
  question editor is opened. A follow-up issue: an event in the activity
  log rather than Needs you.
- `conductor host` sessions. The host takes no catalog questions today
  (`internal/cli/host.go`) and takes no title signal either. A follow-up
  issue, together with the questions.
- The skill's advice to Codex on `ask`. Codex cannot wait in the middle of
  a turn: to wait it ends the turn, and the `done` replaces the question
  for every agent. That is the behaviour since #121, unchanged here.
- Codex's own hooks (`features.hooks`): they stay what `conductor hooks
  install` offers.

## Tests that pin the orderings

Go (`go test -race`):

- `internal/session`: the scanner reports OSC 0 and 2 titles with BEL and ST
  ends, split between reads, cut at the bound (no match), with control
  characters dropped. Each of O1 to O15 with a `fakeProc`, Codex's questions
  and `WorkingTitle`, using the titles Codex 0.161 writes. A title-only
  stream does not break the quiet that the dialogs' watcher and the trust
  watcher wait for (E5). The two reproductions of #186 (the async
  question's bell, the agent's own ask, each followed by 5 s of titles)
  end in `working`. Every one of them fails on `origin/main`, which has no
  `WorkingTitle`.
- `internal/catalog`: `workingTitle` is validated (length, RE2, no empty
  match), and Codex's built-in matches every working form above and none
  of the others.
- `internal/agents`: Codex's `Inject` carries both `-c` overrides, as TOML
  Codex reads (a TOML parse of each value).
- `internal/crew`: a Codex member that asks and goes on is no longer
  counted in the run's `needsInput` once its title says it works, and a
  handoff waiting for it is typed.
- `internal/api`: a session launched from the catalog gets `WorkingTitle`.

Stub and Playwright:

- `web/e2e/stub-agent.sh`, as Codex (`STUB_IDENTITY=codex`), writes Codex's
  titles: "⠋ Working" frames while it answers or spins, "[ ! ] Action
  Required" during `stub approve`, "Ready" at rest. Two new lines:
  `stub async <question>` draws "? 1 question" and spins without a bell
  (rung only when its argv's `tui.notifications` lists `async-question`),
  and `stub ask <question>` runs `conductor notify --state needs_input` and
  then spins.
- A Playwright spec (`codexworking.spec.ts`) with a Codex stub session and
  with the todo example crew: after `stub ask …`, the member's row and the
  Yard show Needs you, then Working while it spins, then done. After
  `stub async …`, Needs you never shows. During `stub approve`, the
  approval still shows with its choices, and Working follows the answer.
  The spec reads state from the rows and from the stub's files, never from
  `.xterm-rows`. `approval.spec.ts` and `trust.spec.ts` pass unchanged.
- vitest: nothing in the browser's logic changes. If the row's words for a
  `working` state from `title` turn out to differ, `useAttention`'s test
  covers it.

By hand (the real Codex is not run by an agent): with the capture script in
an isolated home, Codex 0.161 launched with change 1's override writes the
titles above while it works, at an approval and at rest. A crew of the todo
example in the next release candidate's app shows the members' Needs you
only at approvals. Recorded in `docs/features.md` when done.

## Alternatives weighed

- **Only change 2** (no bell for async questions) is one place and adds no
  state. It mends the first cause alone: the agent's own `ask`, and a bell
  Codex rings for an MCP form it declines by itself
  (`handle_elicitation_request_now` rings, then auto-declines OpenAI forms
  and URLs), still stay until the turn's end. It could ship first, on its
  own, if the owner prefers.
- **Codex's status row on the screen** ("(12s • esc to interrupt)"): the
  row redraws continuously, so the quiet-screen watcher never looks while
  Codex works. Looking at the screen on output instead would be a new
  watcher, and the row's words vary with the key map.
- **Codex's hooks injected at launch**: Codex keeps a trust hash per hook
  and asks "Hooks need review" for new ones. Injected hooks would ask at
  every launch, an answer a person must give.
- **Output after a bell clears it**: every agent draws while it waits (a
  countdown, a blinking prompt), so the rule would be wrong for all of
  them.
