# Plan: Needs you only while the agent waits (#186, #193)

Branch `codex-needs-you`. Reviewed before any code (AGENTS.md, Reviews):
both parts change attention's state machine. Two parts, each its own pull
request:

- **Part A (#186).** A Codex member shows Needs you while Codex works.
- **Part B (#193).** An agent's question stops showing Needs you when its
  turn ends, which is when the agent waits for the answer.

No code is written for either until the host-connection change has merged:
it changes `internal/session`'s input path and the host's messages, and both
parts touch those. The order of the pull requests is at the end.

Revised 2026-10-14: the real Codex 0.161 checked, the bell setting moved to
its own pull request, Part B added; the findings of review rounds 1 and 2
taken in (the end of this plan). The revision after round 2 is
unreviewed: the owner decides whether it is reviewed again.

# Part A: a Codex member shows Needs you only while Codex waits (#186)

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
trusted them ("Hooks need review"). A person's own `hooks.json` is not
Conductor's and may hold none of them. So once something sets
`needs_input` during a Codex turn, nothing but the turn's `done` (or a
person's key) clears it. Two things do set it while Codex goes on:

1. **Codex's bell for a question that does not wait.** The model's
   `request_user_input_async` tool posts a question and the turn goes on.
   Codex 0.161 shows it under its status row as "? 1 question · <countdown>"
   and, while its terminal is unfocused (a crew member nobody is looking
   at), rings the bell for it (`Notification::AsyncQuestion`,
   `tui/src/chatwidget/questions.rs`). The bell reaches `scanOutput`, which
   shows `needs_input` "terminal bell" (`source:"bell"`).
   Evidence: a Codex rollout of a todo-crew tester on 2026-10-02 (Codex
   0.159). It called `request_user_input_async` 1 min 40 s into its turn
   ("The branches crew/example-todo-app-98c1d030/… are absent … Is there a
   different run or checkout whose branches I should use?") and worked 4
   more minutes: a patch, a handoff to core, tests, then `done`.
2. **The agent's own question that does not wait.** The MCP tool `ask`
   (and `conductor notify --state needs_input`) returns at once. A Codex
   member that asks and goes on keeps `needs_input` (`source:"api"`) until
   its turn ends. With Claude Code, the next `PreToolUse` clears it.

Neither the screen's questions (#121, #192) nor the bell's turn rules (#170)
change this: a bell or a report shows `needs_input` until a state replaces
it, and Codex sends none during a turn.

The owner's run of 2026-10-14 is not in the record the app keeps: its
server log, run files and Codex rollouts hold no todo-crew run after
2026-10-07, so which of the two it was is not known. Both reproduce in a Go
test on `origin/main`: a session with Codex's questions, a prompt's Enter,
then the bell and "? 1 question" (or a `needs_input` report), then 5 s of
Codex's status row redrawn. The session still shows `needs_input` at the
end.

## Checked with the real Codex 0.161 (2026-10-14)

By hand, in a pseudo-terminal: Codex 0.161 in a person's own home and
login, in this repository, read-only sandbox, approvals never, one prompt
that asked for a one-word answer, launched with both settings this part
names (A1 and A2).

- **The titles.** Codex wrote its window title as `ESC ] 0 ; <title> BEL`:
  "Ready" at 1.4 s; a braille spinner frame and "Starting" (the frame
  changing about every 100 ms) while its MCP servers started, to 4.5 s;
  a frame and "Working" during the turn, 4.6 s to 5.7 s; then "Ready". No
  bare bell outside an escape sequence. "Thinking", "Waiting" and
  "[ ! ] Action Required" were not drawn in that run: they are the
  source's (`tui/src/chatwidget/status_surfaces.rs`, `run_state_status_text`
  and the action-required prefix).
- **Without the settings.** The same launch with neither setting wrote no
  title, but it never reached a turn: a launch with no `-c` override goes
  through Codex's shared background server (`tui/src/startup_orchestration.rs`,
  `tui/src/daemon_startup.rs`), and that one stopped at "Background server
  has incompatible feature settings". So the run shows only that Codex
  writes no title on that screen. The source says more: with
  `tui.terminal_title` unset, Codex's items are `activity`, `thread-name`
  and `project-name` (`DEFAULT_TERMINAL_TITLE_ITEMS`): a spinner frame
  while busy and no word for the state, and a person's own setting may list
  any items or none. A1's override is what makes "Working" certain in
  Conductor's sessions.
- **"⚠ 2 warnings · f2 to view".** Codex's header showed two warnings with
  both settings; neither comes from them (found from Codex's source and
  `codex doctor`, which makes no model call and draws no screen):
  1. "Codex is ignoring 1 unrecognized configuration setting … `features.notify`
     is ignored": that home's own `config.toml`. `codex doctor --json`
     reports exactly this startup warning with and without the two `-c`
     overrides.
  2. "Running without the shared background server: command-line
     configuration overrides (-c, --enable, --disable, or --search)
     requires embedded mode": any `-c` override but a few feature flags
     turns the shared server off (`config_exclusion`). Conductor's launch
     already passes `-c` overrides (`notify`, the bell, its MCP server), so
     every Codex session Conductor launches shows it today.

  `codex doctor` reports both of A1's items as known (`terminal.title`:
  "activity, run-state", no invalid item), and Codex checks none of the
  names in `tui.notifications`: neither setting raises a warning.

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

   "Working", "Starting" and "Ready" were seen on 2026-10-14 (above); the
   rest are the source's. The workbench shows no program's title, so the
   override costs nothing visible. A person's own `tui.terminal_title` is
   overridden in Conductor's sessions only, and their `config.toml` is
   never written.
2. **Codex rings no bell for a question that does not wait.** Done in its
   own pull request, ahead of this plan's code (branch `codex-bell-config`,
   `Part of #186`): the launch passes
   `-c tui.notifications=["approval-requested","plan-mode-prompt","agent-turn-complete"]`,
   every notification Codex 0.161 has (`Notification::type_name`,
   `tui/src/chatwidget/notifications.rs`) except `async-question`.
   Approvals, MCP forms (`approval-requested`), `request_user_input`
   (`plan-mode-prompt`) and the turn's end still ring (#121 keeps the
   last from undoing the `done`). This part's tests take it as given.
3. **Codex's title is its state: while it says Working, Codex works.** The
   catalog's signal gains `workingTitle` (RE2, at most 200 bytes, must not
   match an empty title). Codex's built-in is
   `^(?:\S+\s+)?(?:Working|Thinking|Waiting)$` ("[ ! ] Action Required"
   followed by the run state has more than one word before it and does not
   match). `session.Options` gains `WorkingTitle`, set from the catalog in
   `internal/api/sessions.go`. The `Scanner` reports a window title (OSC 0
   or 2) as an `Event` with source `title` and the title as its text:
   control characters dropped, bounded by the 256 bytes it captures, a
   title cut by that bound matching nothing. `scanOutput` weighs a title
   only when `WorkingTitle` is set.

   The title is a state, not a heartbeat. Codex writes it only when its
   text changes (`refresh_terminal_title_from_selections` skips a title
   equal to the last one): with animations on, the spinner's frame changes
   it every 100 ms; with them off (`tui.animations`, `tui.effects.progress`,
   or the system's reduced-motion preference, which a launch override
   cannot undo) Codex writes "Working" once and nothing more until the
   state changes. So the session keeps the last title it read
   (`titleWorking`: whether it matched) and acts on it twice:
   - **A matching title** is applied as `setAttentionRead(AttentionWorking,
     "", SourceTitle, "", nil, unlessTurnEnded|unlessHeld|unlessOnScreen|unlessFresh,
     read)`.
   - **A wait raised while the last title says Working** (a bell, an OSC
     notification, the agent's report: not a question on the screen, not a
     startup question) arms a timer, `titleSettle` (1 s). So does **a
     matching title read while such a wait shows that is younger than
     `titleSettle`** (the wait came while the last title said Ready or
     Action Required, and one "Working" follows), for the rest of the
     second. When the timer fires, under the lock: if that wait (its id)
     still shows and no title has been read since that does not match, it
     gives way to `working`, with the same rules. A title that does not
     match ("[ ! ] Action Required", "Ready", "Starting") disarms it: Codex
     shows something for a person, or is at rest, and the wait stands. An approval rings the bell as Codex draws its overlay,
     and its title becomes "[ ! ] Action Required" with that overlay (O1;
     to be checked by hand to come within the second, below); a wait Codex
     goes on from (its own question it does not wait on, a form it
     declines by itself) gives way after a second whether frames come or
     not.

   One case the title cannot tell: while one of Codex's async questions
   (`request_user_input_async`) is pending, its title says "[ ! ] Action
   Required" (`BottomPane::terminal_title_requires_action` counts the
   question editor's unanswered questions, a blocking view or not) though
   Codex works on. A wait raised then stands until the title says Working
   again: when the question is answered or resolves by itself, 30 s after
   it was posted while the editor is collapsed
   (`bottom_pane/async_questions/state.rs`, `expires_at`; to be checked by
   hand). The stale Needs you is bounded by that, not ended by it; telling
   an async question's Action Required from a blocking one would take a
   look at the screen, which this plan does not add.

   The rules:
   - **unlessTurnEnded** (as for the bell): it does not undo a `done` until
     the next Enter or another state. A frame drawn before the turn ended
     and read after its `done` is not a new turn.
   - **unlessHeld** (new): it changes nothing while the session's turn is
     held for a typed text (`s.held`). The held branch of `setAttention`
     records every change it hides as an entry, and the title comes ten
     times a second.
   - **unlessOnScreen** (new): it changes nothing while the session shows a
     question it holds (`heldSource`: a startup question, a dialog on the
     screen). The screen decides those; this change leaves them alone.
   - **unlessFresh** (new): a matching title read within `titleSettle` of
     the wait now showing changes nothing; the timer decides that wait. A
     frame drawn between an approval's bell and its overlay no longer
     clears the bell.

   Everything else gives way to it, as to Claude Code's `PreToolUse`: a
   bell, an OSC notification, the agent's own report with or without
   choices (Part B sets the agent's question aside instead, in its turn),
   a screen pattern, the empty state, and `working` itself (no change,
   nothing recorded). A title spends no token of the event bucket (it is
   the session's own observation, like the bell) and records an entry only
   when it changes the state (`recordOwn`). It does not stamp the bell's
   pace (`lastBell`): the bells of a burst after it stay paced by the
   first. `SourceTitle = "title"` is a new attention source:
   `docs/protocol.md` (Attention) says what it means;
   `web/app/utils/protocol.ts` types the source as a string and needs no
   change.

## The events that interact

- E1 a title that matches `workingTitle`, about every 100 ms while Codex
  works.
- E2 a title that does not match (Action Required, Ready, Starting, another
  program's title): `titleWorking` false, the settle timer disarmed.
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
- E11 the settle timer firing, `titleSettle` after a wait raised while the
  last title said Working.
- E12 one title "Working" and then none (animations off): Codex works on
  with no title written.

## The orders they come in, and what must happen

- O1 E1 then E3 (approval): Codex's draw rings the bell before it draws the
  overlay, then its title becomes Action Required (E2), which clears
  nothing. The bell's `needs_input` stands until E5 replaces it with the
  dialog's question.
- O2 E3 then a stray E1 before the overlay's title (a spinner frame written
  between the bell and the overlay's draw; Codex's code writes none, but
  nothing promises it): nothing (unlessFresh); the overlay's title (E2)
  disarms the timer and the bell stands until E5 asks the dialog. A form
  the catalog does not know (an MCP form, `request_user_input`) keeps the
  bell's Needs you, as today.
- O3 E4 `needs_input` (ask) then E1: `working`. The question stays in the
  chat and the activity log, where `askInChat` and `recordOwn` put it.
  (Part B, once it merges: the question is set aside and shows again at
  its turn's `done`.)
- O4 E3 (an async question's bell, on a Codex launched without change 2)
  then E1: `working`. The bell was recorded once; the burst after it is
  paced.
- O5 E5 (question shown) then E1: nothing changes (unlessOnScreen). The
  person answers (E7) and `answer` clears it; the next E1 shows `working`.
  If the dialog leaves without a key, `missAsked`'s rules stand, unchanged
  here.
- O6 E6 then E1 (a title written while Codex starts behind its trust
  question): nothing changes (unlessOnScreen).
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
- O16 E12, then E4 `needs_input` (ask) or E3 (a bell Codex goes on from),
  and no title after: E11 shows `working` a second later.
- O17 E1, E3, then E2 within the second (an approval drawn): the timer is
  disarmed; the bell stands; E5 asks the dialog once the screen is quiet.
- O18 E11 against E4 `done` (the turn ends inside the second): `done`
  wins in either order; a timer that fires after the `done` changes
  nothing (unlessTurnEnded).
- O19 E11 after the wait it was armed for has gone (answered, replaced by
  another wait, which arms its own timer): nothing; it acts only on the
  wait whose id it carries.
- O20 E11 against E9 or E8: an ended session shows nothing and the timer
  changes nothing (the end clears the state and stops it); in a held turn
  it changes nothing (unlessHeld).
- O21 E4 `needs_input` while the last title says Ready, then one E1 within
  the second and no title after: the title arms the timer for the rest of
  the second (unlessFresh keeps it from applying at once); E11 shows
  `working`.
- O22 E1, then "[ ! ] Action Required" for an async question while Codex
  works, then E4 `needs_input` (ask): it stands (the title is not
  Working, no timer); the title back to Working (the async question
  resolved) applies `working` at once if the wait is older than the
  second, else arms the timer for the rest.

## Invariants

- A Codex session whose last title says Working never shows `needs_input`
  from a bell or a report for longer than `titleSettle` (plus one look at
  the timer) while no question it holds is on its screen, whether Codex
  animates its title or writes it once.
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
- Codex's own hooks (`features.hooks`): they stay what `conductor hooks
  install` offers.
- The two warnings in Codex's header: neither is Conductor's setting's
  (above). Embedded mode, which every `-c` launch has today, is not
  changed here.

## Tests that pin the orderings

Go (`go test -race`):

- `internal/session`: the scanner reports OSC 0 and 2 titles with BEL and ST
  ends, split between reads, cut at the bound (no match), with control
  characters dropped. Each of O1 to O22 with a `fakeProc`, Codex's questions
  and `WorkingTitle`, using the titles seen on 2026-10-14 (and the
  source's for the forms not seen), with `titleSettle` shortened by an
  option for the tests. A title-only stream does not break the quiet that
  the dialogs' watcher and the trust watcher wait for (E5). The two
  reproductions of #186 (the async question's bell, the agent's own ask)
  end in `working` twice: once followed by 5 s of spinner frames, once
  after a single "Working" title and 5 s of other output with no title
  (animations off). Every one of them fails on `origin/main`, which has no
  `WorkingTitle`.
- `internal/catalog`: `workingTitle` is validated (length, RE2, no empty
  match), and Codex's built-in matches every working form above and none
  of the others.
- `internal/agents`: Codex's `Inject` carries the title override, as TOML
  Codex reads, beside change 2's (pinned in its own pull request); the
  person's `config.toml` is untouched.
- `internal/crew`: a Codex member that asks and goes on is no longer
  counted in the run's `needsInput` once its title says it works, and a
  handoff waiting for it is typed.
- `internal/api`: a session launched from the catalog gets `WorkingTitle`.

Stub and Playwright:

- `web/e2e/stub-agent.sh`, as Codex (`STUB_IDENTITY=codex`), writes Codex's
  titles: "⠋ Working" frames while it answers or spins, "[ ! ] Action
  Required" during `stub approve`, "Ready" at rest; with
  `STUB_TITLE_STILL=1`, "Working" once per turn and no frames, as Codex
  with animations off. Two new lines:
  `stub async <question>` draws "? 1 question" and spins without a bell
  (rung only when its argv's `tui.notifications` lists `async-question`),
  its title "[ ! ] Action Required" while the question is pending (a few
  seconds in the stub) and Working frames after,
  and `stub ask <question>` runs `conductor notify --state needs_input` and
  then spins.
- A Playwright spec (`codexworking.spec.ts`) with a Codex stub session and
  with the todo example crew: after `stub ask …`, the member's row and the
  Yard show Needs you, then Working while it spins, then done (Part B
  changes the last to Needs you, and its own spec says so). After
  `stub async …`, Needs you never shows. During `stub approve`, the
  approval still shows with its choices, and Working follows the answer.
  The `stub ask` case runs again with `STUB_TITLE_STILL=1`, and once
  during a `stub async` question: Needs you until the stub's question
  resolves, then Working.
  The spec reads state from the rows and from the stub's files, never from
  `.xterm-rows`. `approval.spec.ts` and `trust.spec.ts` pass unchanged.
- vitest: nothing in the browser's logic changes. If the row's words for a
  `working` state from `title` turn out to differ, `useAttention`'s test
  covers it.

By hand (the real Codex is not run by an agent with a prompt): a crew of the
todo example in the next release candidate's app shows the members' Needs
you only at approvals; with a person's `tui.animations = false`, the same;
at an approval, Codex's title becomes "[ ! ] Action Required" within
`titleSettle` of its bell; and an async question's title returns to Working
when it resolves, about 30 s after it was posted (the capture script
records titles and bells with their times). Recorded in `docs/features.md` when done. The titles at rest and at
work were checked on 2026-10-14 (above).

## Alternatives weighed

- **Only change 2** (no bell for async questions) is one place and adds no
  state. It mends the first cause alone: the agent's own `ask`, and a bell
  Codex rings for an MCP form it declines by itself
  (`handle_elicitation_request_now` rings, then auto-declines OpenAI forms
  and URLs), still stay until the turn's end. It ships first, on its own.
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
- **The title as a heartbeat, with animations forced on at launch**
  (`-c tui.animations=true`, `tui.effects.progress=true`): the system's
  reduced-motion preference still turns the spinner off
  (`LocalSettings::apply_host_preferences`, `system_motion.rs`), and a
  person's motion preference is not Conductor's to override. The title as
  a state works either way.

## Found while building Part A (2026-10-14)

Details the plan left open, settled in the code; none changes the events,
orders or invariants above.

- **The working title survives the Agents page.** The page saves a signal
  with only what its form controls (`signalOut`, which keeps `toolEvents`):
  a person adding a variable to the Codex agent would have saved its signal
  without `workingTitle`, and an overlay saved before this change has none.
  `signalOut` keeps it as it keeps `toolEvents`, and a saved signal without
  one inherits the replaced agent's (`inherit`), as a saved agent without a
  signal inherits the signal.
- **A title cut at the capture** is reported with no text: `workingTitle`
  never matches an empty title, so it is a title that does not match (E2).
- **The rules the plan describes in words** are flags too: `unlessWorking`
  ("`working` itself: no change, nothing recorded"), `unlessEnded` (O13,
  O20) and `onlySettling` (O19, the timer acts only on its wait). The
  settle window is the wait's own, on the monotonic clock, from when it
  showed: a question a hold took the place of starts a new one when it
  shows again (O7).
- **Codex's Action Required title** is "[ ! ] Action Required" alone (its
  blink "[ . ] Action Required"): Codex leaves the spinner and the run state
  out of it (`build_action_required_title_text`). The pattern does not
  match it either way.
- **From Codex's review of the code (round 1).** A matching title read in
  the ended turn changes nothing at all, not even the last title the session
  keeps (O9, O12): kept, it armed the next turn's wait's timer. The timer
  weighs its own state (still armed, its generation), its wait and the last
  title under the lock that applies its change: an Action Required read as
  it fired was overtaken.
- **Round 2.** The last title the session keeps is its turn's: the turn
  that begins after a `done` (`beginTurn`) begins with none, so that a
  Working title kept from the ended turn (read after its `done`) settles
  no wait of the next.
- **The stub** reads still titles from `-c tui.animations=false` as Codex
  does, as well as `STUB_TITLE_STILL=1`; `stub async-ask <question>` is the
  ask during a pending async question; an async question pends
  `STUB_ASYNC_S` seconds (5 by default) and notes when it resolves in
  `<id>.async`.

# Part B: an agent's question shows until it is answered (#193)

## What happens, and why

The Conductor skill tells an agent: "ask, then wait for the answer in the
terminal. The session shows as needing input until someone replies"
(`internal/agents/skill.go`); the MCP tool `ask` says the same. An agent
waits by ending its turn, and the turn's `done` replaces the question:
`setAttention` keeps a waiting question only from another `needs_input`
(`unlessWaiting`, `keepAsked`), never from `done`. So the row shows done,
and the question leaves Needs you, the sidebar and the Yard, exactly when
the agent waits for its answer. Traced in the code; the API test below
reproduces it on `origin/main`.

Which `done` that is: Claude Code's `Stop` (and its `idle_prompt`
notification a minute later, mapped to `done`), Codex's notify
(`agent-turn-complete`), Copilot's `agentStop`, Cursor's `stop`,
Antigravity's and Goose's `Stop`: every agent with a hook reports its
turn's end as `done`, some with `turn` set and some without.

## The change

1. **The report says it is the agent's own question.** The attention
   route's body (`POST /api/sessions/{id}/attention`) gains `ask` (a
   boolean, absent meaning false), and so do the attention words of the
   events route (they share `reportAttention`). It is taken with
   `needs_input` only (with another state it changes nothing). It is said
   on purpose, never inferred from the command: the MCP tool `ask` sends
   it, and `conductor notify` sends it only with its new flag `--ask`
   (`conductor notify --ask --message "…" [--choices "…"]`; `--ask` with
   another state, an event or a hook flag is a usage error). A plain
   `--state needs_input` carries no mark, because Conductor's own assets
   report a program's wait that way: OpenCode's plugin for
   `permission.asked`, DeepSeek Harness's for `permission-requested` and
   `question-asked`, aider's notification command; those, and copies
   already installed in a person's config, keep today's behaviour. The
   skill's two `needs_input` lines become `--ask`. A server that refuses
   `ask` as an unknown field (an older one) gets the report again without
   it, as `agentSession` and `turn` are today: the question then shows as
   before.
2. **The session keeps the agent's question through its turn's end.**
   `Attention` keeps the mark (`ask`, not sent to browsers) and the turn it
   was asked in (`Local.turn`, the count of turns begun after a `done`, as
   the session counts them now). The session keeps at most one agent's
   question, shown or set aside (`aside`), with these rules, all decided in
   the critical section that changes the state:
   - **In its turn** (the turn count it was asked in, no `done` since): a
     `working` report or
     title, and any other state but those below, takes its place and sets
     it aside. A `done` (any, with `turn` or without) leaves it shown, or
     shows it again from aside, in place of whatever shows. The `done`
     still ends the turn
     (`turnEnded`) and is recorded as the report it is: its attention
     entry, with the state `done`, reaches the Events page, webhooks and a
     crew's start-after rule as today. Shown again, the question is the
     same `Attention` it was, its `Since` included, as a held-over question
     is when its hold ends (`endHoldTyped`): a reply under way compares
     that stamp before its Enter (`pressEnter`) and after its write
     (`answer`), so it is never re-stamped. It records its own
     `needs_input` entry (a state that shows has its entry) and is not put
     in the chat a second time.
   - **A question a reply has reached is never set aside.** One whose id a
     reply has claimed (`claimQuestion`, `s.answered`), or that a person's
     keys or a reply's text or Enter reached, is removed, not set aside,
     by whatever takes its place; and one set aside is removed by a
     person's keys or a reply's write made while it is aside. Such a
     question never shows again.
   - **After its turn's `done`**: it shows until a person's keys or reply
     answer it (`answer`, as today), the agent clears it (`clear`) or asks
     another, the agent works again (a `working` report: a new turn, which
     takes its place for good; so does `working` once an Enter has begun
     the next turn), another state takes its place, or the session ends. A `done` (Claude Code's `idle_prompt` a minute later)
     changes nothing shown and is recorded. Codex's working title read
     after the `done` changes nothing until the next Enter: unlessTurnEnded
     covers a question its turn's `done` kept as it covers `done`.
   - **Always**: the bell, an OSC notification and the screen pattern
     never replace it, shown or aside (`unlessChoices` counts the agent's
     question as one to keep, as a question with choices; the bare bell
     and the pattern keep any wait already). The agent's next question
     replaces it; `clear` and the session's end remove it. A person's
     keys while it is aside (the agent at work, the person typing into
     the terminal) remove it too: the person has taken part, and it does
     not show again at the `done`. Conductor's own submission (a crew's
     prompt, a handoff) typed while it is aside does not: that is no
     person's answer.
   - **A submission under way when it shows again.** Conductor's own
     submission (a handoff, a broadcast) may be typed while the question is
     aside, `working` showing; the turn's `done` can then come between its
     text and its Enter. The submission notes, as it begins, the question
     set aside (its id); the checks that leave an Enter out when a prompt
     came since the submission began (`pressEnter`, its guard as the
     writer takes the Enter) do not count that question shown again. The
     Enter is written, so the text is never left in the program's input
     without it; it answers nothing (it is no person's answer, and its
     `answer` compares the stamp the submission began with); and it begins
     the next turn, in which the agent's work (a `working` report or
     title) removes the question. A reply a person makes then answers it.
   - **A held turn** (`holdTyped`): the hold takes its place and keeps it
     as `heldOver`, as today; a `done`, and `working` in its turn, hidden
     by the hold no longer drop it (they drop another held-over question,
     as today); it shows again when the hold ends without answering it
     (`endHoldTyped`). A hold that ends after the turn's `done` shows it.
   - A needs_input without the mark (a hook's permission prompt, the bell,
     a dialog, a startup question, the pattern) is replaced by `done` as
     today.
3. **Hosted sessions.** The server sends the mark on to the host in its
   `attention` message (`HostAttentionMsg` gains `ask`, a boolean), and the
   host's session applies the same rules. The server's copy of a hosted
   session keeps a marked question it shows from a forwarded `done` too, so
   that it does not show `done` meanwhile; for a question set aside on the
   host, the server's copy shows the forwarded `done` until the host tells
   its attention back (the host tells it after every change it applies, as
   it does today). An older host ignores the field: the question shows as
   before there.
4. **Words.** The skill asks with `conductor notify --ask`, and it and the
   MCP tool's description gain one line: an agent that no longer needs its
   answer clears its question (`--state clear`, `set_state` clear).
   `docs/protocol.md` (Attention, the attention route's body, the host's
   `attention` message) says what `ask` does.

## The events that interact

- F1 the agent's question: a `needs_input` report with `ask` (the MCP tool
  `ask`, `conductor notify --ask`).
- F2 a `done`: the agent's turn's end (a hook, notify), Claude Code's
  `idle_prompt`, an explicit `done` (`set_state`, `--state done`).
- F3 `working`: a report (Claude Code's `PreToolUse`, `UserPromptSubmit`;
  Copilot's `userPromptSubmitted`; Codex's hooks), or Codex's title (A3).
- F4 the bell, an OSC notification, the screen pattern.
- F5 another wait: a `needs_input` report without `ask` (a hook's
  permission prompt), a dialog on the screen (`fireAsked`), a startup
  question (`fireTrust`).
- F6 `clear`, the agent's or an admin's.
- F7 a person's keys (INPUT that answers, `answer`), a reply (`submit`, a
  choice clicked: `AnswerInput`).
- F8 Conductor's submission (a crew's prompt, a handoff, a broadcast),
  written only while no `needs_input` shows (`UnlessWaiting`) or as a
  member's first prompt.
- F9 the turn held for a typed text, and its end by a person's keys.
- F10 the session's end.
- F11 a report the event bucket refuses (`429`).
- F12 a hosted session's report, forwarded to its host, and the host's
  report back.
- F13 a reply under way: a `submit` reply that has claimed the question
  and typed its text, its Enter 250 ms later; or a person's keys whose
  write has begun and not landed (the program not reading).
- F14 Part A's settle timer, for a question asked while Codex's title says
  Working.

## The orders they come in, and what must happen

- P1 F1, F2: the question shows, with its id, words and choices; the
  `done` is recorded (`done` on the Events page; a crew's members that
  start after this one start). A bell after it (the turn's own) asks
  nothing.
- P2 F1, F3, F2 (Codex: the title says Working while it writes its last
  message; Claude Code: a tool called after asking): `working` while the
  agent works, then the question shows again at the `done`, the same
  `Attention` (id and `Since`).
  Entries: `needs_input`, `working`, `done`, `needs_input`. One chat line.
- P3 F1, F3, F4, F2 (Codex rings its turn-end bell before its notify
  reports `done`): the bell shows over `working`; the `done` shows the
  question, not the bell's wait.
- P4 F1, F2, F7: answered, as today: the empty state, `lastAnswer`, the
  chat's answered line, an `input` entry.
- P5 F1, F2, F3 report: `working` (the agent went on in a new turn); the
  question is gone; the next F2 shows `done`.
- P6 F1, F2, then a Codex title "Working" read after the `done` (drawn
  before it): nothing. After the next Enter (which answers the question)
  titles show `working`.
- P7 F1, F2, F2 (`idle_prompt`): the question stays; the second `done` is
  recorded.
- P8 F1 then F4, shown or aside: nothing changes for the question.
- P9 F1 then F1 (another question): the second shows; the first is gone; a
  `done` keeps the second.
- P10 F1, F5 in its turn (a permission prompt, a dialog): the wait shows
  and the question is set aside; a person's keys that answer the wait (F7)
  remove the question too; if the wait goes without keys, the `done`
  shows the question.
- P11 F1, F2, F5 (after the `done`): the wait shows; the question is gone.
- P12 F1, F3, F7 (the person types while the agent works): the question is
  gone; the `done` shows `done`.
- P13 F1, F3, F8 (a crew's handoff typed while `working` shows): the
  question stays aside; the `done` shows it.
- P14 F1, F2, F8: the handoff waits while the question shows; once a
  person answers, it is typed. (A change for crews: today the `done` lets
  it be typed at once into an agent that waits on its question.)
- P15 F1, F6, F2: the empty state, then `done`.
- P16 F1, F9: the hold shows; the question is held over. F2 hidden by the
  hold: recorded, the question still held over. The hold ends by keys
  that do not answer it: the question shows. Keys that pick one of its
  choices: answered.
- P17 F10 in any of the above: the empty state, nothing set aside, nothing
  shown again.
- P18 F11 at any point: nothing changes; the question stays where it was.
- P19 a `needs_input` without `ask` (a hook's), then F2: `done`, as today.
- P20 races: F7 against F2, and F3 against F2, at once. Each order ends as
  the sequential rules say (answered then `done`: `done`; `done` then
  answered: the empty state; `working` then `done`: the question; `done`
  then `working`: `working`, the question gone). Never the question shown
  after its answer.
- P21 F12: the host shows what a server session would; the server's copy
  ends with what the host tells, and never keeps a question the host has
  dropped.
- P22 an older server refuses `ask`: `conductor notify` and the MCP tool
  report again without it; the question shows as before. An older host:
  as before.
- P23 F1, F13 (text typed), F2, then the Enter: the `done` changes nothing
  shown, so the Enter is written and answers the question (its `Since`
  unchanged).
- P24 F1, F13 (text typed), F3, F2, then the Enter: the question, claimed,
  is removed at F3, not set aside; the Enter is written (`working`
  shows); F2 shows `done`. The question never shows after its reply.
- P25 F1, F3 (set aside), a person's keys whose write lands late, F2: the
  question was removed as the keys were written; F2 shows `done`.
- P26 F1, F14 (Codex asked while it works on, its title written once):
  the timer shows `working` and sets the question aside; the turn's `done`
  shows it.
- P27 OpenCode's or DeepSeek Harness's permission report (no mark), then
  their `done` with no input between: `done`, as today.
- P28 F1, F3 (aside), F8 (a handoff: its text typed, its Enter not yet),
  F2, then the Enter: the question shows again; the Enter is written all
  the same (the handoff is entered, not left typed); it answers nothing;
  F3 after it (the agent at work on the handoff, a new turn) removes the
  question; without F3, a person's reply answers it, typed on its own.
- P29 F1, F3 (aside), F8 entered before F2 (Enter written while `working`
  shows, no turn begun by it): the question stays aside; F2 shows it. An
  agent that takes the handoff after its turn (Claude Code's queued
  message: `UserPromptSubmit`, `working` after the `done`) removes it; one
  that takes it into the same turn (Codex steering) shows it at its
  `done` until a person answers.

## Invariants

- A question an agent asked (`ask`) that nobody has answered shows as
  `needs_input` once the turn it was asked in has ended, until a person's
  keys or reply, the agent's `clear` or next question, the agent at work
  in a new turn, another state, or the session's end.
- It never shows after its answer, its `clear` or the session's end, nor
  after a reply or a person's keys reached it; shown again it is the same
  `Attention` (id and `Since`), so a reply under way is neither refused
  nor left without its Enter because of it.
- A submission whose text is typed while the question is aside is
  entered: the question's return is not a prompt that leaves its Enter
  out.
- Only a report that says so (`ask`) is kept; a program's wait reported
  without it (a permission prompt, aider's wait) is replaced by `done` as
  today.
- In its turn it never hides the agent at work: `working` shows (#186
  stands).
- Nothing changes for a `needs_input` without the mark: a hook's prompt,
  the bell, a dialog, a startup question and the pattern give way to
  `done` as today.
- A `done` it survives still ends the turn and is recorded as the report it
  is.
- Nothing is typed into the program while it shows (handoffs and
  broadcasts wait, as for any `needs_input`).
- It is put in the chat once.

## Out of scope

- Whether a crew member that waits on its question counts as done for the
  members that start after it. Unchanged: it does, its turn having ended
  (the skill: "begins when you next finish a turn"). An issue if that
  should change.
- A reply naming the question while it is set aside: refused, as a reply
  to a question no longer shown is today (`QuestionAnsweredWords`); taken
  once it shows again.
- Agents that report no `working` during a turn (Copilot after its prompt,
  Cursor, Antigravity, Goose, aider, agents without an adapter): one that
  asks and goes on shows the question during its work (as today) and, if
  nobody answers, after its turn's `done` too (new). A follow-up issue:
  map their hooks that run before a tool, where they have one, to
  `working`.
- Codex's own `request_user_input` and async questions: Part A and the
  bell pull request.

## Tests that pin the orderings

Go (`go test -race`):

- `internal/session`: P1 to P29 with a `fakeProc`, each through the
  setters the API and the pump use (`TrySetAttentionFull` with the mark,
  `scanOutput` for the bell and, once Part A is in, the title and its
  timer), and the activity, the chat and `OnActivity`'s state checked at
  each step. P20 runs each pair from two goroutines many times and checks
  the end state against the sequential rules. P23, P24 and P28 are
  deterministic: the submission is paused after its claim and text and
  before its Enter (a hook in the test, as the hold tests pause one), the
  reports are applied, then the Enter goes on; P25 holds the program's
  read so the keys' write lands after the reports. A `done` over a
  question without the mark still replaces it (P19, P27).
- `internal/api`: the attention route takes `ask` (a non-boolean is `400
  invalid_request`), and the events route's attention words too; `ask`
  then a `done` with `turn` leaves the session `needs_input` with the
  question's words (this test fails on `origin/main`); a webhook for
  `done` fires once; a hosted session's `ask` reaches the host's message,
  and the server's copy keeps the question over a forwarded `done`.
- `internal/cli` and `internal/notify`: the MCP tool `ask` sends `ask`,
  `set_state` does not; `conductor notify --ask` sends it, with or without
  `--choices`; `--state needs_input` without it, each `--<agent>-hook` and
  `--codex` never do; `--ask` with another state, an event or a hook flag
  is a usage error; a server that refuses the field gets the report again
  without it.
- `internal/agents`: the commands OpenCode's and DeepSeek Harness's
  plugins and aider's notification run, executed against a test server,
  report no mark; the skill's question lines use `--ask`.
- `internal/proto`: `HostAttentionMsg` carries `ask` and stays within the
  host message's bound; `internal/hostagent` applies it.
- `internal/crew`: a member that asks and ends its turn is counted in the
  run's `needsInput` after its `done`; a handoff to it waits until a person
  answers, then is typed; the members that start after it start at its
  `done`, as today; a handoff whose text is typed while the member's
  question is aside and whose Enter follows the `done` is noted entered
  (`noteHandoff`), never "typed without its Enter" (P28), and a reply after
  it is typed on its own.

Stub and Playwright:

- `web/e2e/stub-agent.sh`: `stub ask-wait <question>` asks with `conductor
  notify --ask` and ends its turn as its agent's hooks do (`STUB_REPORT`).
  The stubs that stand for a hook's prompt (`asker-*` in
  `web/e2e/conductor.e2e.json`, `STUB_CHOICES`) report with `--state
  needs_input` and keep today's behaviour; the whole suite runs.
- A Playwright spec (`askwait.spec.ts`): after `stub ask-wait …` the row,
  the Yard and the run's tile show Needs you after the stub's turn ends,
  with the question's words; a reply from the row answers it (the stub's
  transcript) and the row moves on; with Part A in, a Codex stub that asks
  and spins shows Working, then Needs you at its `done`.
- vitest: nothing in the browser's logic changes (the mark is not sent to
  it).

## Alternatives weighed

- **Only a `done` with `turn` keeps the question.** Claude Code's
  `idle_prompt` and Goose's `Stop` carry no `turn`: a minute later the
  question would go all the same.
- **Every `needs_input` survives `done`, no mark.** A hook's permission
  prompt that Codex's reviewer answers by itself, or one the agent
  withdraws, would show at the turn's end with nothing to answer.
- **`working` drops the question.** Codex's title says Working until its
  turn ends, and Claude Code may call a tool after asking: the question
  would go before the `done`, which is this issue again.
- **A tool event after the question means the agent went on.** The
  question's own call reports a tool event after it (the MCP tool, or the
  shell that ran `conductor notify`), which cannot be told from the next.
- **`ask` blocks until it is answered.** The agent's tool call would hold
  its turn open; MCP clients time tools out; it changes the tool's
  contract for every agent.

## Found while building Part B (2026-10-14)

Details the plan left open, settled in the code; none changes the events,
orders or invariants above.

- **The handoff's Enter claims nothing.** Conductor's own Enter puts the
  question showing behind (`noteAnswering`: an Enter picks any prompt), so
  the handoff of P28 would have claimed the question shown again, and a
  person's reply to it been refused. The question the submission noted
  aside is left out of that note, its text's write too.
- **A person's reply is one with `By`.** A reply box's line (`submit`, a
  chat sent to the agent) removes a question set aside as its text or
  Enter is written; a run's chat to a member, a broadcast and a handoff
  (`UnlessWaiting`, no `By`) are Conductor's and do not.
- **Round 1 of Codex's review of the code.** A person's write begun while
  the question was aside, and taken by the writer once the done had shown
  it again, did nothing to it (the removal was its own critical section,
  after the note of what the write answers), and a reply's Enter was left
  out for the question's return. Every write now notes the question aside
  as it begins (input, `submitState.aside`), and the note of what it
  answers (`noteAnswering`) weighs it in the same critical section as the
  done: a person's write removes it, still aside, or claims it, shown
  again, and answers it once written (`answerReached`), the reply's Enter
  written; Conductor's own leaves it. And an OSC notification under the
  hold dropped the question held over: the hold showing in its place
  counts as the question for `unlessChoices` and `unlessTurnEnded`.
- **Round 2.** Keys typed at the question showing and taken by the writer
  once the agent's work had set it aside left it to show again at the done:
  a person's write removes a question set aside whenever the write began.
  And keys begun with the question aside under the hold, taken once the
  done had held it over, claimed it as the hold's end showed it again:
  keys that end the hold without answering it claim nothing of the
  question they show again (`endHoldTyped` reports it).
- **Round 3** (fixed after the third review, unreviewed). Keys typed while
  the question showed, queued behind another write and written once the
  hold had taken its place, ended the hold, showed the question again, and
  then answered it as the prompt they were typed at. Such keys now answer
  nothing of the question they show again. The same held for any question
  the hold took the place of before this change (one the agent reported
  without the mark, no done needed); the one rule covers both.
- **Round 4** (an extra round the owner asked for). The claim a write
  answers once it is made was read from the session's `answered`, which
  every write shares: Ctrl+U that only showed the question again, its
  follow-up run after a reply had claimed the question, answered it for
  the reply, before the reply's Enter, and a reply whose link then went
  could not hold it over. `noteAnswering` now reports the claim it made,
  each write keeps its own (input's `claim`, `submitState.claim`), and
  only a write's own claim is answered by it.
- **Round 5** (fixed after it, unreviewed). A claim a hold's end let go and
  another write made again was the same `answered`: keys whose follow-up
  waited across both answered the question for the reply that claimed it
  again. Each change of `answered` is counted (`claims`,
  `setAnsweredLocked`); a write keeps the count its claim made, and
  `answerReached` answers only while it stands, weighed under the lock
  that clears the question (`answerWhen`).
- **The same words, now marked.** An `ask` with the words, source, kind and
  choices of the needs_input showing changes nothing shown, as any report
  of the same; it marks that question as the agent's from then.
- **Set aside when the hold begins.** The hold shows; the turn's done
  hidden by it holds the question over, to show once a person's keys end
  the hold without answering it, as a question the hold took the place of.
- **A switchyard** is told no mark, as it is told no words.

# Who may answer a question: first response wins (2026-10-15)

The owner's brief, 2026-10-15, replacing the first draft of this section
(one review round, superseded). #207 (Part B) stays a draft and is held out
of rc.11; its code changes to this section once it is reviewed.

## Why

Reviews 4, 5 and 6 of #207's code each found one more path where a write
clears a question it no longer owns. Two things combine to cause this:

- **Keys typed in the terminal "answer" a question.** Any key typed while
  a question shows clears it once written (`typedKeys` →
  `answer(promptSince)`), and keys that pick a choice or an Enter claim it
  as the writer takes them (`noteAnswering`).
- **A question can come back with the same id.** It returns after a hold
  (#172), and, under #193, when its turn's `done` shows it again. A write
  begun before it went away therefore still matches it after it returns.

Each fix closed one path, and the next review found another. This section
makes questions single-assignment: one serialized resolution, claimed only
by a structured answer under a fresh token. Raw keys never resolve a
question.

## The goal

The session on the agent's machine (a server session, or a `conductor
host`'s session) owns each question. It offers the question to the
connected participants over the existing transports. The question waits
indefinitely, until the first valid answer from an eligible participant is
accepted. That answer resolves it for everyone and is written to the
agent, once. Later answers cannot change the result. There is no consensus,
no vote, no quorum and no waiting for everyone.

## What a question is

A question is any `needs_input` the session shows with an id (`Attention.ID`):
- the agent's report, with the `--ask` mark or without;
- the bell, an OSC notification, the screen pattern;
- a startup question (`trust`) or a dialog on the screen (`question`).

The hold of the turn (`source:"submit"`) is no question: no answer names
it, as today. Each question instance is its id. A question shown again with
the same id (after a hold, or by the turn's `done` under #193) is the same
instance; its claim tokens tell one claim from the next.

## The state machine

The machine is per question instance, under the session's mutex (`s.mu`).

```
            claim (a structured answer's guard)          resolve(token)
   OPEN ──────────────────────────────────────▶ CLAIMED(token) ─────────────▶ RESOLVED
    ▲  ▲                                          │
    │  └────────── release(token) ────────────────┘
    │
    └── hidden ⇄ shown (set aside, held over / shown again, restored)

   OPEN, CLAIMED ── withdraw (the agent or the screen moves on, the session ends) ──▶ WITHDRAWN
```

- **OPEN.** Not answered and not claimed. It is either *shown*, in which
  case structured answers are taken, or *hidden*: set aside in its turn
  (#193), or held over by the hold (#172). A hidden question takes no
  answer; one naming it is refused, as today, and taken once it shows
  again.
- **CLAIMED(token).** A structured answer won the claim, and its bytes are
  being written. Every other answer is refused while the claim stands.
  The token is fresh for every claim (`s.claimSeq`, a counter under
  `s.mu`), so a later claim on the same id is never mistaken for an
  earlier one.
- **RESOLVED.** The claimer's last byte was written (a choice's keys, or a
  line's Enter) while its token was still current. The result is recorded
  under the same lock before anything is broadcast and before the
  claimer's caller is answered: who answered (from the connection), when,
  and what. The question then leaves the screen as the empty state with
  `source:"input"`, as an answer does today.
- **WITHDRAWN.** The question ended without an answer:
  - the agent moved on: its report, its clear, its next question, its work
    in a new turn, or the done rules as they stand;
  - the screen moved on: the startup question or dialog is gone;
  - the session ended.

  A claim on it ends with it, and the claimer's completion finds nothing to
  resolve.

**The transitions, and the only code that makes each:**
- **claim:** OPEN, shown → CLAIMED. A structured answer's guard
  (`claimQuestion`), the last check as the session's writer takes the
  write, in the same critical section that checks the question is shown
  and OPEN.
- **resolve(token):** CLAIMED(token) → RESOLVED. Exactly one function,
  `resolve`, called from the claimer's completion: in `input`, once a
  choice's keys are written; in `pressEnter`, once a line's Enter is
  written. The writer finishes on its own goroutine, so `resolve` checks
  under `s.mu`, in the critical section that clears the question, that the
  question's claim (`s.claim`: its id and token) is still this token.
  Shown, the question becomes the empty state; held over by a hold another
  write made, it is dropped and is not shown again. Nothing else may clear
  a question as answered: `answer(promptSince)` goes, and so does every
  clear by stamp or by id alone.
- **release(token):** CLAIMED(token) → OPEN. The claimer's write did not
  happen, or did not finish into an answer:
  - refused or withdrawn before it began: a link revoked or expired, the
    host not vouching, its context or `InputWait` run out, the input busy,
    the session ending;
  - a line's text typed and its Enter not written for good: its link let
    go before the Enter (#172, `ErrRevoked`), or the Enter left out
    because a prompt or a startup question came during its pause
    (`comesAfter`, `stillOnScreen` → `errPromptCame`, a nil error with
    `Typed && !Entered`). The text waits in the program's input. So the
    session holds the turn (`holdTyped`, for this case too), releases the
    claim in the same step, and the hold takes the question's place and
    holds it over, OPEN and hidden. No other line can join the text, and
    no answer is taken while the question is hidden. The hold ends by a
    person's keys:
    - keys that neither enter the held text nor pick the question's choice
      (Ctrl+U, the text killed) show the question again, OPEN (Ctrl+U
      bringing it back);
    - keys that enter the held text or pick its choice show it again
      *consumed* (below): those keys gave the program an answer, and no
      structured answer is taken after it.

  `release` is a no-op unless the token is still current. A claimer whose
  line is stranded so is answered with an error (`input_failed`, its words
  naming the held text), never `answer_ok`.
- **hide and show:**
  - set aside in its turn by the agent's work or another wait (#193), and
    shown again by the turn's done;
  - held over by the hold (#172), and restored as a person's keys end it.

  A question that is CLAIMED is not set aside: the agent's work withdraws
  it (P24). A hold made by another write leaves the claim standing, and the
  claimer resolves the question hidden (it is not shown again). A hold
  made by the claimer's own line releases the claim first.
- **withdraw:** any change of what shows that is not an answer:
  `setAttentionLooked` applying a report or an observation, the screen's
  watchers finding the question gone, `markEnded`. It drops any claim.

**A program not reading, and continuations.** A claim stands as long as its
write can still land:
- a write that has begun lands when the program reads: a key's write, or a
  line's text (`ErrSubmitPending`, finished by `finishSubmission`);
- a line whose text is typed and whose Enter had no turn in time
  (`errEnterLater`) is finished by `finishEnter` and `pressEnterHeld`,
  holding the session's turn.

Meanwhile other answers are refused as "being answered". The claim ends
by `resolve` when the last byte lands; by the stranded-text release
(above); or by withdrawal (the session ends). Only a write refused before
it began releases at once.

**Consumed.** A question instance can be *consumed*: it takes no answer
(refused as answered, with the result when one is recorded), holds no
claim, never resolves, and ends only by withdrawal. This replaces
`answered`'s use as a protection.

Only an OPEN instance is consumed. Raw keys, or the hold's end, reaching a
CLAIMED question change nothing of it: the writer goes on to the next
write once one is made, before the claimer's completion runs, so raw keys
can come between a structured answer's last byte and its `resolve`. The
claimer then resolves it, or releases it, after which it is OPEN and what
comes next applies. A question becomes consumed when:
- raw keys reach a question read off the screen (decision 2);
- the hold's end enters held text or picks its choice (above);
- a screen question is asked again with the words of the one last resolved
  or consumed before the program redrew: today's `sameDialog` and
  `startupStill`, `dialogAnswered` and `startupAnsweredWords`.

  A redraw of the same instance (the same words at another width) keeps
  its id, its claim or its result: no new instance and no new claim. A
  redraw while a structured answer's write is under way keeps that claim.

## Raw keys never resolve a question

Only a structured answer claims and resolves: a choice (`answer` with
`input`, a row's or the chat's or the Yard's button) or a reply naming the
question (`answer` with `text`, or `submit` with `question`). These come
from the server's WebSocket, from WebRTC, or from the relay through the
switchyard, and all of them end at the same `session.Local`
(`AnswerInput`, `Submit`). The rule lives there, the same for every
transport.

A person's keystrokes in the terminal (INPUT, a host's own terminal), and a
line that names no question (`submit` without `question`, a chat sent to
the agent, Conductor's own lines), are written as they are and resolve
nothing. The question clears when the agent moves on:
- its hook (Claude Code's `UserPromptSubmit` or `PreToolUse`, Copilot's
  `userPromptSubmitted`), Codex's working title, its next report, its done
  rules;
- its screen: the screen's watchers find a startup question or a dialog
  gone.

Three owner decisions where this changes what a person sees (below,
"Decisions") keep the protections that are not answers: withdrawal (an
end with no result) and consumption (no further answers), each decided in
one critical section as the writer takes the keys.

## The code that goes

All of it in `internal/session` unless named:
- **Raw keys answering:**
  - `typedKeys`'s `s.answer(promptSince, …)`. `typedKeys` keeps marking
    the viewer as typing and poking the dialogs' watcher.
  - `input`'s `promptSince` as an answer's key, and `answersQuestion`'s
    use for answering.
- **Raw writes claiming:** `noteAnswering`'s claims (`typedAt`, `picksChoice` and a
  dialog's `picks` → `markAnswered`), the agent's own question claimed when
  shown again (the aside case), its `aside`/`person` parameters and its
  return value; `Submit`'s and `pressEnter`'s calls to it.
- **The patches of rounds 1–6:**
  - `answerReached`, `answerWhen`, `claims` and `setAnsweredLocked`;
  - input's `aside`, `claim`, `restored` and `noteAside`, and its
    `promptSince = nil` on a restore;
  - `endHoldTyped`'s return value;
  - `submitState.claim`.
- **Lines answering by stamp:** `pressEnter`'s `s.answer(promptSince, …)`.
  A line answers only through its `Question`'s claim, and a line naming no
  question answers nothing.
- **Startup questions answered by keys:** `input`'s `startupSince` path and its `s.answer`;
  `escGone`'s `s.answer` (an Escape that may have answered becomes a
  withdrawal once the screen shows the question gone).
- **Dialogs answered by keys:** `fireAsked`'s and `missAsked`'s
  `s.answer(…, typed.by)` (a dialog gone is withdrawn); `typedAtAsked` and
  `noteTyped`, which only credit raw keys.
- **The hold's end answering by keys:** `endHoldTyped`'s `picksChoice`
  drop of the question it held over, and its `dropOwnLocked`. The hold's
  end always shows the question again: OPEN, or consumed when the keys
  entered the held text or picked its choice.
- **`answered` (one string for claims and protections) splits in two:**
  - `claim` (id and token: CLAIMED);
  - `consumed` (the instance takes no answer; decisions 1 and 2, the
    hold's end, a screen question redrawn after its answer).

  `markAnswered` becomes `claimLocked` for a structured answer's guard and
  `consumeLocked` for the rest. `setAttentionLooked`'s `stillAnswered` and
  `holdAskedAgain` mark the re-asked instance consumed, never claimed.
- **Startup questions withdrawn by the screen:** `heldAnswer` and `escGone`,
  today armed only by an Escape that may answer, are armed by every
  consumption of a startup question (raw keys picking its answer, a
  resolved structured answer). The feed-count and screen-size checks
  (`fed`, `cols`, `rows`, `screenShowsNoStartup`) stay. A digit or Enter
  that the program takes, drawing its normal prompt without the question
  again, then withdraws it; the turn is no longer refused with
  `ErrTrustQuestion`.
- **What stays as it is:** the hold (#172), the screen's watchers, `stillOnScreen` (a reply's
  check that the screen still shows its question), `comesAfter` and its
  aside exception (P28: a handoff's Enter after the done is written and
  answers nothing), and #193's set aside and shown again.

## Decisions for the owner

These are behaviours the brief changes, each with a recommendation.

1. **Waits that have no "moving on" signal.** Typing in the terminal
   cleared these, and will not:
   - the bell (Antigravity, Goose), the screen pattern (Cursor), an OSC
     notification;
   - a report from an agent that never reports working: aider's "aider is
     waiting" (its notifications command).

   Without keys clearing them, they stay Needs you until the agent's next
   state. For aider that may be never.

   **Recommended:** keys with an Enter *withdraw* (not answer) a wait whose
   source is the bell, OSC or the pattern, as the writer takes them. This
   is atomic, has no deferred step and records no answer. aider's
   notifications command reports `--state done --message "aider is
   waiting"` instead of `needs_input`: it fires each time aider waits for
   its next line, which is a turn's end. A report from any other agent is
   never cleared by keys.
2. **Keys at a question read off the screen** (a startup question, a
   dialog). Raw keys that pick its answer (an Enter, its choice's keys, a
   dialog's digit or Ctrl+C) reach the program's dialog. A structured
   answer after them could land on what the program shows next: the
   owner's update-question incident, and #192.

   **Recommended:** such keys *consume* the question as the writer takes
   them. Structured answers to it are refused ("answered in the terminal")
   until the screen shows it gone (withdrawn) or shows the next question.
   Consumption never clears, has no token and no back edge. A startup
   question so consumed arms the screen's withdrawal (`heldAnswer`,
   `escGone`). This is today's protection (`startupAnswered`,
   `heldAnswer`, `dialogAnswered`, `sameDialog`) minus its answering. Arrow
   keys consume nothing, as today.
3. **The agent's own question set aside while a person types** (#193's
   P12 and P25: the person answers in the terminal while the agent works).

   **Recommended:** a person's keys written while it is set aside
   *withdraw* it, in the critical section where the writer takes them. It
   does not show again at the done. Nothing is recorded as an answer, and
   there is no deferred step. Round 1's race (the done showing it again
   before the writer takes the keys) is then harmless: the keys find it
   shown, and shown questions are not cleared by keys. It stays until the
   agent moves on.
4. **"Answered by" for terminal answers.** The Events page and `LastAnswer`
   will name only structured answers. A question the agent moved on from
   shows as gone, not as answered by a person at the terminal.

   **Recommended:** accept it. The terminal's typing still shows on the
   roster ("typing"), and the agent's next state follows.

## The owner's decisions (2026-10-15)

- No third review round of this plan: it stands as revised after round 2.
- **Decision 1, with an addition.** As recommended: keys with an Enter
  withdraw a wait whose source is the bell, OSC or the pattern, as the
  writer takes them, and aider's notifications command reports
  `--state done --message "aider is waiting"` (each time aider waits for
  its next line, which is a turn's end; its adapter's events say `done`).
  Added: **a screen snapshot withdraws a bell wait.** A bell or OSC wait
  has no rows of its own, so the session reads the agent moving on from
  its screen:
  - Every session keeps a model of its screen for this (`Screen`, as the
    dialogs' watcher has one; a second, with no pattern, for every
    session, server and host alike), fed by the pump.
  - The baseline is taken at the first quiet of the screen (`patternQuiet`,
    500 ms without output) after the wait showed, not at the bell: the
    program draws what it rang for in the same read or the next (Goose's
    permission prompt, Codex's approval overlay), and that drawing is the
    wait's own screen, not the agent moving on.
  - At each later quiet, while that wait still shows and is OPEN (a claim
    stands while its write can land, and its resolve ends it; a hidden
    wait is not compared), the rows are compared with the baseline, each
    row's text with its trailing spaces trimmed. The wait is withdrawn
    (the empty state, `source:"screen"`, no answer recorded, no entry) when
    **at least 3 rows have changed, a row counting as changed when more
    than 8 of its cells differ** (a blank row filled, or a filled row
    blanked, counts by the same measure).

    Why 3 rows and 8 cells: a spinner frame changes 1 cell; a countdown
    ("0:29" to "0:28"), a clock or Codex's "(12s • esc to interrupt)"
    change 1 to 3 cells of one row; a status line whose words change
    wholesale ("Thinking" to "Running the tests") changes many cells of
    one row; a cursor is not drawn. The agent moving on prints output
    (every row shifts up: nearly all change), or redraws its prompt area
    (Claude Code's and Codex's input box is 3 rows or more). Two rows is
    where a status line and a spinner row can both change in a frame;
    three is beyond what any one-row redraw does. The measure is cells
    that differ, not rows that are rewritten, so a frame that redraws the
    same text counts nothing.
  - A resize drops the baseline; it is taken again at the next quiet
    (the program redraws everything after a resize).
  - The snapshot is only for a wait with no rows: the bell and an OSC
    notification, including a plain shell's bell. A question read off the
    screen (`trust`, `question`, #121) has rows and is withdrawn by its
    watcher as today (consumed, then gone from the screen). The pattern's
    prompt has a line, and Enter withdraws it (above). A bell rung with a
    dialog (O1): the dialogs' watcher and the snapshot both run at the
    first quiet, where the snapshot only takes its baseline, so the dialog
    replaces the bell's wait and nothing is withdrawn before it.
  - So a bell wait closes on any of: the agent's hooks (`working`,
    `done`), Codex's title, the screen moving on past the threshold, or an
    Enter typed in the terminal. Reading the prompt's text per agent, to
    turn a bell into a screen question with rows and choices, is out of
    scope: #195.
- **Decisions 2, 3 and 4:** as recommended above.

## No timeouts end a question (audit)

Nothing in the code expires a question. Each timer on an answer's path is
classified by where the write stands when it runs out:

| Timer | Write never began | Write begun or text typed |
|---|---|---|
| `session.InputWait` (10 s), a write's wait for the program | release | the claim stands; the write lands when the program reads |
| `submitTimeout` (10 s; `internal/api/ws_viewer.go`, `internal/hostagent/queue.go`), the host's `inputDeadline` | release | the claim stands: the submission goes on (`ErrSubmitPending` → `finishSubmission`, `errEnterLater` → `finishEnter`), and the claimer's word waits for its outcome (below) |
| the page's `ANSWER_WAIT_SECONDS` (15 s) | presentation only: the page says it was not confirmed; nothing on the owner changes | |

A retry after any of these deadlines, while the claim stands, is refused as
being answered.

These are observations, not expiries: Part A's `TitleSettle` (the agent's
title says it works, so the agent moved on), `patternQuiet`, `lookBudget`,
`ConfirmWait`. The events bucket refuses a report, which changes nothing.
A viewer detaching, or every viewer detaching, changes no attention. The
plan adds no timer.

## Notify, recover, refuse late answers

- **Recorded before broadcast.** `resolve` sets the result (`LastAnswer`,
  the chat's answered line naming the question id, and a bounded map of
  recent results by question id: the last 64, in memory) under `s.mu`. It
  then broadcasts the attention change and the chat line, and only then
  answers the claimer's caller.
- **`answer_ok` is the final word, sent once the answer's outcome is
  known.** It is sent when its last byte is written with its token current
  (RESOLVED). It is also sent when the question was withdrawn first (P24:
  the agent moved on as the answer was typed), because the answer was
  written; the attention says the question went.

  It is never sent while the outcome is pending. A line the program takes
  slowly (`ErrSubmitPending`, `errEnterLater`) can still be stranded by an
  Enter left out or its link going. So the transports
  (`internal/api/ws_viewer.go`'s `answer`, `internal/hostagent/peer.go`'s
  `answerIn`) register the submission's `Finished` and answer from it.
  For a choice's keys whose write began and landed late, they answer from
  `input`'s landed callback. Today they answer `answer_ok` at once for a
  pending line; that goes.

  A stranded or released answer gets an error naming why. The page's
  `repliedQuestions` releases the question on any error, so its sender can
  answer again once the question shows OPEN. Until the word arrives the
  page shows the answer as submitted; `ANSWER_WAIT_SECONDS` only says it
  is not confirmed yet. Broadcasting is not delivery.
- **A late or duplicate answer gets the recorded result.**
  `error{question_answered}` names who answered and when, from the map,
  and says "no longer asked" for a withdrawn question. No new frame and no
  new field: the error's message words.
- **Reconnection.** It uses the existing sync: an attaching viewer gets the
  current attention (OPEN shown, with its id) and the kept chat (the
  question and its answered line). A retry naming the question gets the
  recorded result. A hosted session's server listing learns from the
  host's `attention` and `chat` messages, as today.
- **Limits.**
  - The question's state lives in memory, in the process that runs the
    session. A server restart or a host's restart ends the session and its
    question.
  - A host that reconnects keeps the server's last told attention until
    its next change (the pre-existing reconnect issue, reported apart).
  - There is no host election, consensus or durable store.

## Events

- **G1.** A question shows: the agent's report (with or without the mark),
  the bell, OSC, the pattern, a startup question, a dialog.
- **G2.** A structured answer arrives: an `answer` with `input` or `text`,
  or a `submit` with `question`, from the server's WebSocket, WebRTC, the
  relay, or the owner's own page.
- **G3.** The claimer's write: begun, written, landed late, or refused
  before it began.
- **G4.** The claimer's link revoked or expired, before its text or between
  its text and its Enter (#172).
- **G5.** The hold ends by a person's keys (Ctrl+U), showing the question
  again.
- **G6.** Raw keys: INPUT, a host's terminal, and a line naming no
  question.
- **G7.** The agent moves on: its report, clear, next question, working in
  a new turn, done.
- **G8.** #193's set aside and shown again, the turn's done, Codex's title.
- **G9.** The screen moves on: a startup question or dialog gone, or the
  next one drawn.
- **G10.** The session ends; viewers detach and attach.
- **G11.** A late or duplicate answer naming a resolved or withdrawn
  question.

## The orderings, and what must happen

- **S1. G1, G2, G3 written:** RESOLVED, exactly once. The result is
  recorded, then broadcast, then `answer_ok` is sent.
- **S2. G1, then two G2 at once** (two pages, or the owner and a guest):
  one claim wins under `s.mu`. The other is refused with the result (or
  "being answered" while the claim stands). There is one write, one
  resolution and one continuation.
- **S3. A duplicate** (the same page twice, a retry after a lost
  `answer_ok`): refused with the recorded result, nothing written.
- **S4. An invalid G2** (a view role, a revoked link, a malformed answer,
  an id that is not the shown question, a choice not offered): refused,
  and the question stays OPEN.
- **S5. G1, then time passes** past every timeout above: OPEN.
- **S6. G1, then every viewer detaches:** OPEN. A viewer that attaches
  sees it with its id and answers it.
- **S7. G1, G2, G3 refused before it began** (a stall, an expired context,
  a revoked link): release, OPEN. The next answer claims with a new token.
- **S8. Revoke before Enter.** G1, G2 (its text typed), G4: release; the
  hold holds the question over, OPEN and hidden. Then G5 (Ctrl+U): shown
  again, OPEN. A new answer claims (a new token) and resolves. The first
  claimer's completion, when it runs, finds its token stale and resolves
  nothing.
- **S9. A program not reading.** G1, G2, G3 begun and not landed:
  CLAIMED. Another answer is refused as being answered. The write lands
  and resolves; or the session ends and the question is withdrawn.
- **S10. Round 3.** Raw keys typed at Q, queued; the hold takes Q's place;
  the done; the keys end the hold: Q is shown again, OPEN. The keys
  resolve nothing.
- **S11. Round 4.** Ctrl+U begins with Q set aside; the done holds Q over;
  Ctrl+U shows it again, OPEN. A reply claims (token t1) and is revoked
  before its Enter: release(t1), and the hold holds Q over. Keys end the
  hold: Q is OPEN, and nothing is recorded.
- **S12. Round 5.** Raw keys A, then a guest's line let go, Ctrl+U, and
  reply C's claim (t2). A's completion resolves nothing (raw keys never
  do). C resolves with t2, or, revoked, releases.
- **S13. Round 6.** As S12, with Q showing as A began: the same. There is
  no stamp path left.
- **S14. A done during a hold** (P16): the done is recorded; Q is held
  over, OPEN and hidden; the hold's end shows it, OPEN.
- **S15. The agent moves on during a claim** (P24): withdrawn. The
  claimer's completion finds nothing; `answer_ok` is sent; the attention
  says gone.
- **S16. A reply under way across the turn's done** (P23): the done leaves
  Q showing and CLAIMED, and the Enter resolves.
- **S17. A handoff's Enter after the done shows Q again** (P28): written;
  Conductor's line claims nothing; Q stays OPEN.
- **S18. Cancellation against submission:** G7 and G2 at once. Either the
  claim wins and the answer resolves (or is withdrawn by the later G7), or
  the withdrawal wins and the answer is refused as "no longer asked".
  Never both a resolution and a withdrawal.
- **S19. A late answer after RESOLVED or WITHDRAWN:** refused with the
  recorded result or "no longer asked"; nothing written.
- **S20. Two sessions, and a new question in one session:** an answer
  naming another id is refused. Questions are isolated by id.
- **S21. A dialog answered by raw keys, then a structured answer**
  (decision 2): refused as answered in the terminal. The dialog is
  withdrawn once the screen shows it gone.
- **S22. A bell's wait, then keys with an Enter** (decision 1): withdrawn
  as the writer takes them. An agent's report is not withdrawn by keys.
- **S23. The agent's question set aside, then a person types** (decision
  3): withdrawn as the writer takes the keys. Typed after the done showed
  it again: it stays OPEN until the agent moves on.
- **S24. A startup question answered by a raw digit or Enter**, after which
  the program draws its normal prompt with no redraw of the question:
  consumed as the writer takes the keys, and withdrawn once the screen
  shows no startup question (the feed and size checks). The next
  submission is not refused.
- **S25. A screen question redrawn**:
  - while a structured answer's write is under way: the same instance,
    still CLAIMED, and the write resolves it;
  - after it resolved, before the program drew what follows: a new
    instance, consumed, with no claim and no answer taken, withdrawn when
    the screen shows it gone.
- **S26. A reply's text typed, and its Enter left out** because a startup
  question or a prompt came in its pause: the line is stranded. The hold,
  the release and the question held over follow in one step; the claimer
  gets an error, not `answer_ok`. A second reply is refused while the
  question is hidden. The hold's end:
  - with Ctrl+U: the question is OPEN, and a new answer claims it;
  - with an Enter (the text entered): consumed.
- **S27. The hold's end by keys**: Ctrl+U shows the held-over question
  OPEN, with the same id. An Enter, or one of its choice's keys, shows it
  consumed. A marked question stays kept, through all three (no
  `dropOwnLocked`).
- **S28. Deadlines with a live continuation:**
  - a line whose text's write began when `submitTimeout` ran out
    (`ErrSubmitPending`);
  - a line whose Enter had no turn in time (`errEnterLater`).

  In both, the claim stands, a retry is refused as being answered, and
  the line resolves when it lands. No `answer_ok` is sent before then.
- **S29. A pending line stranded later.** Its text's write began as
  `submitTimeout` ran out; then, as it finishes, a prompt comes in its
  pause, or its link goes, and its Enter is left out. No `answer_ok` was
  sent. The stranded step follows: the hold, the release, the question
  held over. The claimer gets the error and its page releases the id. A
  person's keys end the hold (Ctrl+U), the question shows OPEN, and a new
  answer (the same page's retry, or another's) claims and resolves it.
  This runs through the server's WebSocket and through a host's peer.
- **S30. Raw keys between a structured answer's last byte and its
  `resolve`:** a choice's keys written, then another viewer's raw Enter
  taken by the writer before the choice's completion runs. The question
  is CLAIMED, so the raw keys change nothing of it, and the choice's token
  resolves it, once. The same holds for the hold's end over a question
  that another write's claim holds.

## Invariants

1. A question is RESOLVED only by `resolve`, for the token of the claim
   that stands, inside the critical section that clears it. Exactly once,
   and recorded before it is broadcast.
2. Only a structured answer's guard claims, and only a shown, OPEN
   question. At most one claim stands. A claim's token is never reused.
3. A released, withdrawn or replaced claim can never resolve anything.
4. Raw keys and lines naming no question resolve nothing. Their only
   effects are decisions 1–3, each made in one critical section as the
   writer takes them, with no deferred step.
5. Nothing expires a question, and no viewer's departure changes it.
6. Every transport reaches the same `Local` and the same rule.
7. A late answer never changes a result, and gets it.
8. #193 stands: the mark, set aside and shown again, the clearing events.
   #172's hold, #192's ids and answers, #170's turn rules and Part A's
   title stand, all expressed through the transitions above.

## Tests

Go (`go test -race`):

- **`internal/session`, the brief's list:**
  - one answer resolves;
  - two near-simultaneous answers from two goroutines, run many times:
    exactly one winner, one write and one resolution;
  - duplicates and retries are idempotent and get the recorded result;
  - invalid and unauthorized answers leave the question OPEN;
  - time past every timeout, with short `InputWait` and context deadlines,
    never expires it;
  - every viewer detaching leaves it OPEN;
  - attaching after the result gives the empty attention and the chat's
    answered line, and a retry gets the recorded result;
  - late answers cannot overwrite;
  - questions are isolated by id;
  - cancellation (the agent's clear, its next state, the session's end)
    races safely with a claim.
- **`internal/session`, Conductor's:**
  - S8, a revoke before Enter, with the test hooks pausing the claimer at
    its Enter;
  - S8 to S11, Ctrl+U bringing the question back;
  - S9, a program not reading (the fake process holding its read);
  - S14, a done during a hold;
  - S10 to S13: rounds 3 to 6, the existing regressions rewritten to the
    rule, and round 6's variant (Q showing as A began);
  - S21 to S23, the decisions;
  - S24, a startup question answered by a raw digit, then the program's
    normal prompt;
  - S25, a screen question redrawn during a pending write and after its
    resolution (`TestADialogAskedAgainBeforeTheProgramDrewIsStillAnswered`
    kept, rewritten to "consumed");
  - S26, a stranded line (`answer_test.go`'s startup screen drawn in a
    reply's pause, kept, now asserting the hold, the release, the error
    and no `answer_ok`);
  - S27, the hold's end by Ctrl+U, by an Enter and by a choice's keys,
    asserting the question's id, its claim and its consumed state (the
    `hold_test.go` case "the text entered" rewritten);
  - S28, deadlines with a live continuation;
  - S29, a pending line stranded later, with no early `answer_ok`;
  - S30, raw keys or a hold's end between a claim's last byte and its
    `resolve`, which `beforeWritten` pauses.
- **One table test: every clearing path through release and reclaim.**
  Each row is one path that can end a claim or a question:
  - `resolve` from a choice's keys, from a line's Enter, and from an
    Enter-only reply;
  - `release` from a refusal before the write, a revoke before the text, a
    revoke before the Enter, an Enter left out by a prompt in the pause,
    an expired context before the write began, and a stall before it
    began;
  - consumption by raw keys at a screen question, by the hold's end with
    an Enter, and by a redraw after a resolution;
  - withdrawal by the agent's clear, its next question, working in a new
    turn, a dialog gone, a startup question gone, and the session's end.

  The test pauses the path at its decision point (`beforeWritten`,
  `beforeEnter`, and one hook added for the watchers). Meanwhile it
  releases and reclaims the question: a line let go, the hold, Ctrl+U,
  then another answer's claim. It then resumes the path. The paused path
  must end with no effect on the new claim, and the new claim resolves
  once. A second run of each row without the release shows that the path
  still does what it does.
- **No other clear.** `resolve` is the only function that sets `LastAnswer`
  or the answered chat line. Every transition goes through its one
  function (`claimLocked`, `resolve`, `release`, `consumeLocked`,
  withdrawal in `setAttentionLooked` and `markEnded`). The table test
  asserts each question's state (id, claim token, consumed, shown or
  hidden) after every step, not only `LastAnswer`, so a path that drops or
  hides a question by another route fails it.
- **`internal/api` and `internal/hostagent`:** the same answer through the
  server's WebSocket, and through a host's peer (WebRTC and the relay share
  its frame loop), claims, refuses a second answer with the result, and
  resolves once. A pending line gets its word only from `Finished`: S29
  through each transport.

Web: vitest for `reply.ts`: an error after a reply releases its question,
so the page can answer again once the question shows OPEN (S29), and the
refusal's words. Playwright, `answered.spec.ts` extended: two pages
answer one question, one wins, and the other page shows the result. A page
that reconnects after the result shows it. A person typing in the terminal
leaves the row's question until the agent moves on. The existing
`askwait.spec.ts` changes its reply to come from the row, as it already
does.

## What stays as #207 has it

- The `--ask` mark through `notify`, the MCP tool, both routes and the
  host's message (none for a switchyard), and a plain `--state
  needs_input` without it.
- #193's events: set aside in its turn, shown again at its done as the same
  instance, kept through a done, held over by the hold, and every clearing
  event of P1–P29 that is not a raw key. P4's "a person's keys" becomes "a
  structured answer". P12 and P25 follow decision 3.
- Conductor's lines (handoffs, broadcasts) and P28's Enter. The words, the
  docs and the e2e spec.

## Found while building (2026-10-15)

Details the section left open, settled in the code; none changes the
events, orders or invariants above, but for the two marked.

- **A dialog gone from the screen under a claim is left to the claim**
  (an amendment to "withdraw: … it drops any claim"). A reply to a dialog
  is its choice's keys, claimed as the writer takes them and resolved as
  their write returns; the program takes them in between and removes the
  dialog. The screen's watchers (`missAsked`, `escGone`, `screenQuiet`)
  therefore withdraw only a question nobody answers (`withdraw`): one the
  screen shows gone while a claim stands is resolved by that claim, with
  its result, rather than withdrawn under it with the result lost. The
  agent's own clear, its next question, its work in a new turn and the
  session's end withdraw a claimed question as the section says.
- **The screen withdraws any dialog gone**, not only one keys answered:
  a dialog the program took away itself (an interrupt, a key Conductor
  does not read as an answer) left the question standing before; now a
  settled screen without it withdraws it (`missAsked`).
- **The startup guard reads the screen.** Typed text is refused while a
  whole, settled frame still shows a startup question during the startup
  watch, answered or not (`startupOnScreen`), in place of the stamp that
  said a key had answered it until the screen moved on. Conductor's own
  line waits for the screen.
- **A reply's stranded line holds the turn.** S26 as written: a claimed
  line whose Enter is left out releases its claim, and the hold follows
  as for a revoked link's (`strand`), so no other line joins the text. A
  line naming no question stranded the same way is left as before, no
  hold: it answers nothing and nobody waits on it.
- **A startup question is never claimed past its write**, so the table's
  "a startup question gone" row has no claim to withdraw from: a reply to
  it is its choice's keys, resolved as they are written, and the screen
  gone is the consumed path (S24). The row is a startup question answered
  in the terminal under the hold instead (the "Enter left out" release
  row).
- **Keys that land late tell their word late.** A reply's choice whose
  write began and lands when the program reads again (`ErrSubmitPending`)
  is told through `finished` (`AnswerInputFinished`), as a line's is, over
  both transports: `answer_ok` only once the keys landed, never before.
- **A page's Escape is a structured answer.** Sent as a reply naming the
  question, it resolves the question as it is written; the held Escape
  (one that may be the start of a sequence) is the raw keys' rule only.
- **A done drops an unmarked held-over question**, as before: a question
  the agent reported without the mark, held over by the hold, is withdrawn
  by its done; the agent's own question survives the done held over (S14
  is tested with the agent's own).
- **The snapshot compares a screen never erased.** A program that
  never erases its screen (a shell, a line reader; the e2e stub) resized
  with something on it is "clipped" for the dialogs' watcher (what shows
  was drawn at another size) and never a whole frame again; the snapshot
  compares the screen with itself, so it needs only a finished frame
  (`Screen.Drawn`: no synchronized update under way), and a resize, the
  baseline dropped, pokes a quiet look so that a program drawing nothing
  for it has its baseline before its next output.
- **The hold is no question.** A reply naming the hold's own id
  (`source:"submit"`) is refused, and raw keys do nothing to it but end it.
- **The refusal's words** say what became of the question when the
  session knows: `being answered` (CLAIMED), `answered in the terminal`
  (consumed), `already answered by <name>` (a result kept), else the
  generic `already answered`; the page shows each.
- **Round 7 of Codex's review of the code** (fixed after it, reviewed in
  round 8). A stranded line's claim was let go as the line ended and the
  hold installed on another goroutine after: a reply between the two
  claimed the question and wrote its keys after the text. The claim now
  stands until the hold's words take the question's place, and is let go
  in that step (`strandedClaim`, `releaseStrandedLocked`), as S26 says.
  An Enter whose write began and whose time ran out resolved the question
  and gave `answer_ok` at once: it resolves and gives its word once the
  Enter lands (`landEnter`, `ErrSubmitPending` meanwhile, over both
  transports), and a session that ends under it records nothing. The
  agent's own question consumed by the Enter that entered the held text
  was set aside by the agent's work and shown again OPEN at its done: a
  consumed question is reached (`reached`), and the work withdraws it.
  And the snapshot's rows were read before the session's lock, where a
  resize could drop the baseline between: they are read under it.
- **Round 8** (fixed after it, reviewed in round 9). An Enter typed in the
  terminal after the hold was installed and before its words showed
  entered the stranded text, ended the hold with nothing held over, and
  the stranded claim, let go after, left the question OPEN for a second
  reply. Keys that end a hold not shown yet (`holdPending`) let the
  stranded claim go themselves and consume the question shown when they
  entered the text or picked its choice, as the hold's end does with the
  question held over.
- **Round 9** (fixed after it, unreviewed). The same Enter typed before
  the hold was installed at all (between the line's end and the hold's
  goroutine) reached nothing: the claim stood, the hold followed, and
  its end showed the question OPEN again. The stranded claim is now
  marked as the line ends (`strand`), and keys that enter the text or
  pick the question's choice before the hold is installed let it go and
  consume the question (`rawKeys`); no hold follows then. And the chat's
  answered line referred to the question asked last in the chat, not the
  one resolved (a dialog drawn over the hold and gone, the held-over
  question restored by Ctrl+U): it refers to the resolved question's own
  line (`answeredInChat(id, …)`).

# The pull requests, in order

1. The host-connection change merges first (its own plan): it changes
   `internal/session`'s input path and the host's `attention` message,
   which both parts touch.
2. Part A from `main`: changes 1 and 3 (change 2 is in its own pull
   request, which merges before or beside it).
3. Part B from `main` once Part A has merged: it changes Part A's O3, and
   relies on Part A's title for Codex (without it a Codex agent that asks
   and goes on would keep its question past its turn).

# Reviews

Round 1 (6d1c3e7, `codex_review.py "review this plan: …"`): needs
attention, three findings, each traced in the code and taken in.

- [high] Showing a set-aside question again with a new `Since` strands a
  reply under way: `pressEnter` leaves its Enter out when the shown
  question's `Since` is not the one it began with (`submit.go`), and the
  claimed id then refuses another reply (`waitsLocked`); `answer` compares
  the same stamp. Held. Taken in: the question shows again as the same
  `Attention`, its `Since` included (as `endHoldTyped` restores a
  held-over one); a question a reply has claimed or reached is removed,
  never set aside or shown again (P23 to P25, with deterministic tests).
- [medium] `conductor notify --state needs_input` is not only the agents'
  question: OpenCode's plugin (`internal/agents/opencode.go`, its
  `permission.asked`) and DeepSeek Harness's (`dsh.go`, its
  `permission-requested`) report a permission wait that way, and so does
  aider's command; marking every such report would keep their stale
  permission prompts past `done`. Held. Taken in: the mark is said on
  purpose only, by the MCP tool and by a new `--ask` flag; a plain
  `--state needs_input` keeps today's behaviour, in installed copies too
  (P27).
- [medium] With animations off Codex writes "Working" once
  (`refresh_terminal_title_from_selections` skips an unchanged title; the
  spinner needs `tui.animations` and `tui.effects.progress`), so a title
  read as a heartbeat leaves a later wait standing through the work.
  Held, and the system's reduced-motion preference turns the spinner off
  whatever the launch says. Taken in: Part A reads the title as Codex's
  state, with a settle timer for a wait raised while it says Working
  (O16 to O20, P26), and the stub draws a still title too.

Round 2 (41ecad8): needs attention, three findings, each traced in the
code and taken in once; this revision is unreviewed.

- [high] A handoff typed while the question is aside, whose Enter follows
  the turn's `done`, has its Enter left out once the question shows
  again (`pressEnter` sees a prompt that came since it began), and the
  crew notes it "typed without its Enter" and never types it again
  (`internal/crew/handoff.go`); a reply would then join its text. Held:
  `done` shows nothing today, so the Enter goes through; Part B made the
  `done` a prompt. Taken in: the submission notes the question set aside
  as it begins, and its Enter is not left out for that question's return;
  it answers nothing and begins the next turn, in which the agent's work
  removes the question (P28, P29, a deterministic test, and the crew's
  handoff test).
- [high] Codex's title says "[ ! ] Action Required" while one of its async
  questions is pending, though it works on
  (`BottomPane::terminal_title_requires_action` counts the question
  editor's unanswered questions), so a wait raised then cannot settle.
  Held. Taken in as a bound, not a fix: the wait stands until the title
  says Working again, when the async question is answered or resolves by
  itself (30 s while the editor is collapsed, from the source; to be
  checked by hand); the stub draws that title and a spec covers an ask
  during it (O22). Telling the two Action Required apart would take a
  look at the screen, which this plan does not add.
- [medium] A wait raised while the last title is not Working, followed by
  a single "Working" within the second (animations off), arms no timer:
  unlessFresh refuses the title and nothing comes after. Held. Taken in: a
  matching title that meets a wait younger than the second arms the timer
  for the rest of it (O21).
