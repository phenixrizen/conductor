# Issue 288: Claude Code's choices from its dialog, not a fixed list

## The defect

Every Claude Code `PermissionRequest` hook and `permission_prompt`
notification is reported with one fixed list, `permissionOptions()` in
`internal/notify/mappers.go`: Yes `1`, Always for this session `2`, No,
explain… `3`. Claude Code's dialogs differ by tool, mode and release, so the
buttons send keys that pick other answers.

## What Claude Code 2.1.304 shows (captured 2026-10-19)

Captured in a PTY at 120×40, rendered with `session.Screen`, hooks logged
beside. The `PermissionRequest` hook fires within 0.1 s of the dialog's first
drawing, so the screen can be read only after the report arrives.

- Bash: `Do you want to proceed?`, `❯ 1. Yes`, `2. Yes, and always allow
  access to <dir> from this project`, `3. Yes, and switch to auto mode · …`,
  `4. No`, footer `Esc to cancel · Tab to amend`. The fixed list's "No,
  explain…" (`3`) switches to auto mode here.
- Write: `Do you want to create c288.txt?`, `1. Yes`, `2. Yes, and switch to
  accept edits (…) for this session (shift+tab)`, `3. No`, the same footer.
  The fixed "Always for this session" (`2`) switches to accept edits.
- Plan approval (ExitPlanMode, also a `PermissionRequest`): `Claude has
  written up a plan and is ready to execute. Would you like to proceed?`,
  `1. Yes, and use auto mode` (or `Yes, and switch to BYPASS PERMISSIONS (no
  further prompts) for this session` when bypass is allowed), `2. Yes,
  manually approve edits`, `3. Tell Claude what to change` with the hint
  `shift+tab to approve with this feedback`, last row `ctrl+g to edit in
  <editor> · <plan path>`.
- AskUserQuestion (also a `PermissionRequest`): a header, the question,
  `1. Red` with its description on the next row, `2. Blue` …, `3. Type
  something.`, a rule, `4. Chat about this`, footer `Enter to select · ↑/↓ to
  navigate · Esc to cancel`.
- Keys, checked live with keys that approve nothing: a digit picks at once
  in the permission dialogs (`4` denied the Bash command, `3` rejected the
  write) and in AskUserQuestion (`2` answered Blue and the turn went on); in
  the plan dialog `3`, the input choice, only moves the highlight into its
  feedback field and the dialog stays. No hook fires after a denial.

## Design

Claude Code's numbered dialogs become a question asked during the work
(catalog `anytime`), as Codex's approvals are, so the whole screen-question
path applies: the dialog is read off the session's screen model at 500 ms of
quiet, it takes the place of the hook's report, a report after it does not
hide it, submissions are held while it shows, a reply names the question and
is refused once the dialog moved on, keys typed in the terminal mark it
answered, and it is withdrawn when the screen no longer shows it.

What is new is that its choices are read from the dialog each time it is
asked, instead of written in the catalog.

1. **Catalog.** `Question` gains `answersFromScreen` (bool): the answers are
   the numbered list the dialog shows, read at each look; it requires
   `anytime` and no `answers`. And `modeAnswers` (an RE2 of at most 200
   bytes, as a prompt): the labels of choices that change the session's
   permission mode. Validated in `catalog.Validate`, carried by the agent
   form as the other question fields are.
2. **Claude Code's entry** gets one such question: a prompt matching a
   highlighted numbered row (`❯ N.`) followed, within the dialog, by one of
   Claude Code's dialog footers (`Esc to cancel`, `Enter to select`, `ctrl+g
   to edit`) on one of the last rows (`\z`), so a numbered list in the
   transcript, or one the person typed, is not taken for a dialog; and
   `modeAnswers` `(?i)auto mode|accept edits|auto-accept|bypass permissions`.
3. **Reading the list** (`session`, new `readList`): bottom-up from the
   matched text's end, past the footer, hints, blank and rule rows, to the
   last row numbered `N.`, then up through rows numbered `N-1.` … `1.` at the
   same column (`❯` allowed before the number), rows between them being a
   choice's description or a rule. Each choice is its first row's label and
   its number as the key, no Enter. No `1.` reached, numbers out of order,
   or more than 9 choices: no list, so the question does not fire (the hook's
   report stays, without choices). The words are the rows above the list up
   to the dialog's top rule (a row of `─` alone), rows of rule characters
   left out (the plan's question line; the tool, its command and
   `Do you want to proceed?`; the question header and its text): they tell
   one dialog from the next (two Bash prompts differ by command) and the
   message is cut from them as for any screen question. A question's own
   `words` pattern, when set, still decides.
4. **Mode choices.** `session.Option` and `proto.AttentionOption` gain
   `mode` (bool, omitted when false): a choice that changes the session's
   permission mode. Pages give it no number key and never show it as the
   first, highlighted choice; its button is outlined with a warning colour
   and a title saying it changes the permission mode. It travels in
   `attention` frames, `session` listings, the host's attention, and chat
   `question` messages.
5. **Hook reports.** `permissionOptions()` goes. A Claude Code
   `PermissionRequest` or `permission_prompt` reports `needs_input`,
   `kind:"permission"`, no options: the dialog's own choices replace it once
   read. Words: ExitPlanMode → "Claude Code has a plan for you to approve";
   AskUserQuestion → its first question's text; other tools as today.
6. **Pages.** A `needs_input` of kind `permission` with no options offers no
   typed reply: the reply bar, the sidebar row, the phone sheet and the Yard
   say to answer in the terminal (its Enter would pick what the dialog
   highlights). This covers a dialog the screen cannot read (a variant the
   pattern misses, a hosted session, Codex's and Copilot's reports before
   or without their screen question).
7. **Docs.** `docs/protocol.md` (the `mode` field, the hook mapping, the
   catalog fields), README (the reply bar, the held dialogs), features.md
   (the open verification is done), `internal/proto` and `protocol.ts`.

## Plan review (Codex, 2026-10-19) and what changed

- Replies bound to the dialog instance: a reply is refused when the
  dialog on the screen shows other choices than the question's
  (`stillOnScreen` compares the choices read now as well as the words).
  Two dialogs with the same words (two plan approvals, the same command
  twice) are told apart by the hook Claude Code sends before each, its
  `PreToolUse`, a working report, which ends the memory of the dialog last
  answered (#283's rule).
- Wrapped labels: a choice is marked `mode` when the pattern matches its
  label with the rows that follow it up to the next choice, so "Yes, and
  switch to" over "BYPASS PERMISSIONS …" on a narrow screen is marked.
- AskUserQuestion's last page ("Review your answers", "Ready to submit your
  answers?", "1. Submit answers", "2. Cancel") has no footer (captured with
  2.1.304): the prompt matches it too, so it holds submissions as the
  pages before it do. Multi-select choices are read as any list; a digit
  there toggles, and the dialog asked again after it is held answered
  until Claude Code moves on, as any dialog a key left in place.
- False matches: the footer must be the last row the screen shows. Claude
  Code's prompt box and status line, gone while a dialog shows, stand below
  a dialog quoted in the transcript or typed into the prompt box.
- The plan's "Tell Claude what to change" is offered as the dialog lists
  it; its key takes the highlight into the feedback field, and the dialog,
  still on the screen, is held answered until Claude Code moves on: the
  feedback is typed in the terminal.
- A hook report answered in the terminal before the first look stays until
  the next hook, as it did before (now without choices); hosted sessions
  are #293.

## Code review (Codex, 2026-10-19) and what changed

Captured again with 2.1.304 for these: the plan's feedback field with
"abc" typed, and the plan, a Bash permission and AskUserQuestion 44 columns
wide.

- Choices compared whole: a question asked from a list keeps the list
  whole (each choice's label with its following rows, uncut), and a reply
  is refused when the screen's list differs, past the label's 60
  characters too. It is kept apart from the words, which still tell one
  question from the next.
- Wrapped footers: at 44 columns the plan's footer breaks inside its path
  onto the next row, and AskUserQuestion's puts "cancel" on a row of its
  own. The prompt allows up to three rows after the footer row that are
  neither a rule nor the prompt's `❯`, which still keeps a quoted or
  pasted dialog out. The bounds of the list grew (8 rows between choices,
  8 after the last) for a long folder's path wrapped on a phone.
- No PreToolUse: Conductor installs no PreToolUse hook for Claude Code, so
  nothing reported between two dialogs, and Claude Code's spinner (a frame
  every 100 ms, each a synchronized update) never lets the screen go
  quiet. The dialog answered is now forgotten at the first whole frame
  after the answer that no longer shows it (`answeredLeft`, on output), so
  the same command asked twice in a turn is asked twice. The session test
  draws the spinner between the dialogs and fails without the change.
- The last page of AskUserQuestion is a question of its own, its two
  choices the last rows of the screen, so it is not found above Claude
  Code's prompt box.
- The feedback field (found while checking the above): once the highlight
  is in it, every key is typed there ("❯ 3. abc"). A choice the catalog's
  `typedAnswers` matches (the plan's, by its hint "shift+tab to approve
  with this feedback") with the highlight before it makes the dialog offer
  no choices, so no click types a digit into the feedback and no new
  question is asked at each pause while it is typed.

## Not changed

- Hosted sessions (`conductor host`) watch no startup or screen questions
  (Codex's included); their Claude Code questions will show without choices
  and are answered in the terminal: #293.
- The server refuses no typed submission to a hook's permission report
  without choices; only the pages stop offering one, as the issue asks.

## Tests

- Go race: `readList` against the captured dialogs (Bash, Write, plan,
  plan with bypass, AskUserQuestion, highlight moved to 3, a wrapped label,
  a transcript list above the dialog, a typed `1.` prompt, out-of-order and
  missing numbers); the Claude Code prompt matching each dialog and not the
  idle screen; a session fed a captured dialog after a hook report shows
  the screen's choices and words with `mode` set where it should be, takes
  the place of the report, and is withdrawn when the screen moves on; two
  Bash dialogs in a row are two questions; catalog validation of the two
  fields; the notify mapper's reports carry no options.
- vitest: mode choices get no key and are never first-highlighted; the
  permission-without-choices reply state.
- Playwright: a stub session draws Claude Code's plan dialog and reports its
  hook; the sidebar row and the reply bar offer its three choices, the
  mode choice without a number; each button writes its digit (the stub
  prints what it read); a permission report without choices shows "answer
  in the terminal" and no reply field.
