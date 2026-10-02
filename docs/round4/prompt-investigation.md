# Round 4: crew prompts that wait for Enter

Investigation, 2026-10-01, on `round2-crews-events` at 2c9c220 (`bin/conductor` built
from it). Claude Code 2.1.287, Codex CLI 0.159.0, the user's real `HOME` (logins,
`~/.claude/settings.json` with `defaultMode: auto`, plugins and MCP servers;
`~/.codex/config.toml` with MCP servers and `terminal_title = ["activity", ...]`).
Nothing in the checkout was edited. The experimental engine lived in a throwaway
worktree (now removed); its diff (`experiment.patch`) was evidence captured during the
investigation, not kept.

Evidence root, below `$E`: the investigation's own directory of captures, screenshots,
harness runs and scratch repositories (`$E/root/c2`, `$E/root/c3`, `$E/root/x3`). It was
evidence captured during the investigation, not kept; the `$E/...` names below identify
which capture a statement rests on. Its layout:

- `$E/caps/<run>/`: Conductor runs. `timeline.jsonl` (every WebSocket frame,
  control message, run-log entry and member status, ms since launch), `raw.bin`
  (the PTY output bytes, scrollback included), `run.json`.
- `$E/shots/<run>-<N>s.png`: headless Chromium screenshots of `/sessions/<id>`.
- `$E/h/<exp>/`: PTY-harness runs (`$E/ptyh.py`, the agent in a PTY with
  Conductor's clean environment, 100x30, answering DA1/XTVERSION like xterm.js):
  `timeline.jsonl`, `raw.bin`, `screen-*.txt` (the screen rendered with pyte).
- `$E/render.py <capdir> <ms,...>` renders a Conductor capture's screen at any time.

## Root causes

Typing the prompt is not the problem in itself: `Local.Type` writes the bytes as given,
the PTY is in raw mode (the agents set it), and the agents receive the `\r`. They
read it as part of a paste, not as Enter.

### Codex: the Enter is part of a paste burst (every prompt, whatever its length)

The prompt and its `\r` arrive in one read. Codex's composer takes a fast run of
characters as a paste (its paste-burst detection, for terminals that do not send
bracketed paste) and turns an Enter inside the run into a newline. The composer then
holds the text and an empty second line, and nothing is submitted. It does not depend
on length (49 and 1080 characters both fail), on how long Conductor waited (a 33 s
wait still fails), or on worktree vs none.

- Conductor, baseline: `x2-wt` (worktree of a trusted repo). Prompt typed at 33.3 s;
  the screen at 35 s and 70 s shows `› Reply with the single word READY and nothing else`
  above an empty composer line, with no "Working". Screenshot `$E/shots/x2-wt-55s.png`
  and `-70s.png`. Raw output after the write: the cursor goes to the new line under the
  text (`\x1b[?2026h\x1b[38;3H\x1b[?25h\x1b[?2026l`), with no task started.
- Harness, the same one-write at 3 s after the composer shows: `$E/h/x-onewrite`
  (stuck, footer `tab to queue message`). Typed before the composer is up:
  `$E/h/x-early-one` (stuck as well).
- Codex itself, asked (`$E/codexq/answer.txt`), says the same: "Once PasteBurst
  recognizes rapid character input, an immediate Enter is treated as a newline within
  the paste".

### Claude Code: two ways the Enter is lost

A. **Text over 800 characters becomes a paste.** One read of more than 800 characters
is collapsed to `[Pasted text #N]`, and the trailing `\r` becomes a line of that
paste (`[Pasted text #1 +1 lines]`). Threshold measured with nothing submitted
(`$E/h/c-thresh`): 100, 400, 700, 799 and 800 characters stay plain text, 801 and 900
become `[Pasted text]`.
- Conductor, baseline: `c2-wt-long` (1080-character prompt, worktree of a trusted
  repo). Typed at 5.06 s; the raw output right after is
  `...[Pasted text #1 +1 lines]\x1b[K ... paste again to expand`, and it stays until the
  stop at 32 s. Screenshot `$E/shots/c2-wt-long-30s.png`.
- The same short prompt (49 characters) runs: `c2-none`, typed at 3.76 s, READY at 5.6 s.

B. **Text typed before the REPL is mounted keeps no Enter.** Claude Code turns
bracketed paste on, then off (`\x1b[?2004l`, about 0.25 s after its first output),
loads, and turns it on again as the REPL mounts and draws (`$E/h/c-start-ans`: on
1.17 s, off 1.41 s, on 2.14 s, first frame 2.38 s). Text with its `\r` typed in that gap
is put in the input box and the `\r` is dropped, which is exactly the report:
`$E/h/c-gap-old` (typed 0.1 s after the `2004l`; at 11 s the screen shows
`❯ Reply with the single word READY and nothing else`, not submitted). Typed even
earlier, before any output: `$E/h/c-early`, the same. Typed in the earliest window
(between the first `2004h` and the `2004l`) the text is lost entirely:
`$E/caps/early-noretry` and `$E/caps/early-retry` (Conductor with readiness forced
to 200 ms after the first output; the input is empty afterwards).
Conductor's readiness (1 s of quiet after the first output, at least 2 s after the
start) lands in the gap only when Claude is quiet for 1 s there. Measured gaps before
the REPL: 0.18 to 0.76 s in seven harness starts (four of them concurrent,
`$E/h/gap-1..4`), and 0.95 s in the Conductor worktree run `fix-c2-wt-long`, where
the browser's resize output interrupted it. So this is a race that a slower start (a
heavier config, several members starting together, a cold cache) loses.

The example crews' Claude prompts are 318 to 713 bytes with their goal, under the 800
threshold (`example-todo-app` lead 713, core 636; codex members up to 972). So with the
examples, Claude fails through B, through a goal that pushes a prompt over 800, or
through handoffs (any length; they go through the same one write). Codex fails always.

### Not causes

- `Local.Type` and the PTY: the bytes arrive unchanged; `\r` is not converted.
- Readiness typing too early is a cause only for Claude (B). For Codex a long wait does
  not help (`x2-wt` typed after 33 s of startup still fails).
- Trust dialogs: they make the member exit (below), not wait for Enter.

### A separate delay for Codex

Codex animates a spinner in the terminal title (OSC 0, every 100 ms, from the user's
`terminal_title = ["activity", ...]`) for 30 to 46 s at startup here (`$E/h/idle-x2`;
the composer accepts prompts all along). The output never goes quiet, so Conductor's
readiness waits: `x2-none` got no prompt in 30 s, `x2-wt` got it at 33.3 s. Not the
bug, but every Codex member starts half a minute late.

## Trust dialogs (a second, different failure)

In a repository the agent has never trusted, the prompt is typed into the trust
question:

- Claude: "Do you trust the files in this folder?", default `No, exit`. The `\r`
  answers No and Claude exits 1 (`c1-none`, exit at 4.1 s;
  `$E/shots/c1-none-2s.png`). Same with the fix (`fix-c3-untrusted`).
- Codex: "Trust this folder?", default `1. Trust and continue`. With the current
  one-write typing Codex quit with exit 0 (`x1-none`). **With the paste-then-Enter fix
  the Enter accepts "Trust and continue": Codex saved
  `[projects."<repo>"] trust_level = "trusted"` in `~/.codex/config.toml` and the prompt
  was lost** (`fix-x3-untrusted`). So the fix must come with trust handling, or
  Conductor starts trusting folders on the user's behalf.

What decides trust (verified unless marked):

- Claude: `~/.claude.json` `projects[<path>].hasTrustDialogAccepted`, looked up from the
  cwd upwards but not past the repository's root, and for a worktree in the main
  repository (strings of the 2.1.287 binary; verified: a subdirectory and a
  `.conductor/worktrees/...` worktree of a trusted repo show no dialog, `$E/h/look-c2sub`,
  `look-c2wt`; a trusted parent directory does not cover a repository inside it,
  `look-c2`). `--dangerously-skip-permissions` still shows the dialog (`c3-skip`).
  `CLAUDE_CODE_SANDBOXED=1` skips it for every directory (`c3-sandboxed`), which is a
  global bypass meant for containers: not a fix. There is no per-session flag.
  Accepting the dialog is Down then Enter (`\x1b[B`, `\r`; `trust-c2`).
- Codex: `~/.codex/config.toml` `[projects."<repo root>"] trust_level = "trusted"`; a
  linked worktree inherits its main repository's trust (`look-x2wt`).
  `--dangerously-bypass-approvals-and-sandbox` still shows the screen (`x3-bypass`).
  A per-session override works without writing anything:
  `-c 'projects={"<repo root>"={trust_level="trusted"}}'` (`look-x2-override2`); the
  dotted form `-c 'projects."<path>".trust_level="trusted"'` (which Codex itself
  suggested) does not (`look-x2-override`).
- The user's own case: `test_crew_todo` is trusted in both files now, so its worktrees
  get no dialog; that fits the report (waiting for Enter, not exiting).

## Experiments

Harness unless "Conductor". "Runs" means the agent answered READY.

| Agent | What was written | Result |
|---|---|---|
| Codex | text + `\r`, one write (49 chars) | stuck, newline in composer (`x-onewrite`; Conductor `x2-wt`) |
| Codex | text, then `\r` 300 ms later (a) | runs (`x-sep300`) |
| Codex | text, then `\r` 50 ms later | runs (`x-sep50`) |
| Codex | one write, then a second `\r` 3 s later (b) | runs (`x-one-then-cr3`) |
| Codex | `ESC[200~` text `ESC[201~` + `\r`, one write (c) | runs, 49 and 1080 chars (`x-bp-same`, `x-long-bp`) |
| Codex | bracketed text, then `\r` 300 ms later | runs (`x-bp-sep`) |
| Codex | bracketed text + `\r` 300 ms later, typed 0.6 s after start | runs: Codex keeps early input (`x-early-bp-sep`) |
| Codex | one write, after a 33 s wait (d) | stuck (Conductor `x2-wt`) |
| Claude | 49 chars + `\r`, one write, REPL up | runs (Conductor `c2-none`) |
| Claude | 1080 chars + `\r`, one write | stuck as `[Pasted text #1 +1 lines]` (`c-long-one`; Conductor `c2-wt-long`) |
| Claude | 1080 chars, then `\r` 300 ms later (a) | runs (`c-long-sep`) |
| Claude | 1080 chars + `\r`, then a second `\r` 3 s later (b) | runs (`c-long-one-then-cr3`) |
| Claude | bracketed 1080 chars + `\r`, one write (c) | runs (`c-long-bp-same`) |
| Claude | bracketed 49 chars, then `\r` 300 ms later | runs (`c-short-bp-sep`) |
| Claude | one write in the bracketed-paste-off gap before the REPL | stuck in the input (`c-gap-old`) |
| Claude | bracketed + `\r` 300 ms later, 0.6 s after start; then `\r` again at +4 s | stuck in the input, then runs on the second `\r` (`c-early-bp-sep-retry`) |
| Claude | Conductor, typed 200 ms after first output, with and without a second `\r` | text lost; the second `\r` has nothing to submit (`early-noretry`, `early-retry`) |
| both | permissive flags (e) | trust dialogs still shown (`c3-skip`, `x3-bypass`) |

End to end with the experimental engine (paste-then-Enter, the readiness gate, Enter
retry for `claude`; server env `CONDUCTOR_EXP_CONFIRM=claude`):

- `fix-c2-wt-long` (Claude, 1080 chars, worktree): typed 6.3 s, `working` 0.4 s later
  ("m took its prompt (working)"), done READY at 8.7 s. `$E/shots/fix-c2-wt-long-10s.png`.
- `fix-x2-wt` (Codex, 49 chars, worktree): typed 34.1 s, READY at 37.4 s.
  `$E/shots/fix-x2-wt-60s.png`.
- `fix-x2-wt-long` (Codex, 1080 chars, worktree): typed 33.8 s, READY at 38.5 s.
- `fix-x2-wt-titlequiet` (Codex, with title-only output not counted as output): typed
  at 5.8 s instead of about 33 s, READY reported at 10.0 s. `$E/shots/fix-x2-wt-titlequiet-10s.png`.
- `fix-c3-untrusted`, `fix-x3-untrusted`: see Trust dialogs.

## Recommended fix

1. **Paste, then press Enter** (fixes Codex, and Claude case A). A
   `Local.TypeLine(text, byName, skipWhileWaiting, enterDelay)` that writes the text
   wrapped in `ESC[200~` / `ESC[201~` when the program has bracketed paste on (escape
   characters dropped from the text, so it cannot end the paste early), raw otherwise,
   then `\r` as its own write about 250 ms later. `Local` follows the mode from the
   output (`ESC[?2004h` / `ESC[?2004l`, carrying a few bytes across reads), as a
   terminal does; this is what xterm.js does for a paste. It records one input entry
   with the plain text. Use it for the role prompt (`run.go` `prompt()`), handoffs
   (`handoff.go` ~170, with skipWhileWaiting: the Enter is left out if needs_input
   shows by then) and broadcasts (`api/runs.go` ~328, the same one-write `line+"\r"`).
   Either half alone made both agents run here; together, the paste markers keep it
   right when the two writes are read together, and the separate Enter covers programs
   without bracketed paste.
2. **Do not type while the program is between screens** (Claude case B): not ready
   while bracketed paste has been turned on and then off again, unless the agent
   reported needs_input or done. Claude Code turns it off during its startup gap and on
   at the REPL.
3. **Confirm, then press Enter once more**, only for agents that report taking a prompt
   (Claude's `UserPromptSubmit` hook gives `working`): with no attention change from a
   source other than input within 3 s of the Enter, and needs_input not showing, write
   one more `\r`. It recovers text left in the input (verified), not text that was lost.
   This wants a flag on the adapter or catalog entry (Codex reports nothing on submit;
   its `notify` fires at the end of a turn).
4. **Never type into a trust question.** Before launch, read the agent's trust for the
   member's repository (Codex: `config.toml` `projects`, the main repository for a
   worktree; Claude: `~/.claude.json` `projects`, bounded walk-up). When it is not
   trusted, say so at launch and in the run log ("claude will ask whether to trust
   <repo>: answer it in m's terminal"), and type the prompt only after a person's input
   to that session has restarted the readiness wait. Or detect the questions with
   per-adapter patterns on the last screen line (`Enter to confirm · Esc to cancel`,
   `enter continue · esc quit`), which is cheaper and more brittle. Do not answer them
   for the user. A per-crew opt-in could pass Codex's per-session
   `-c 'projects={"<root>"={trust_level="trusted"}}'`; Claude has no per-session
   equivalent, and writing `~/.claude.json` from Conductor is unsafe: a trust entry I
   accepted for the scratch root was gone from the file later, overwritten by another
   running Claude Code (lost update). The examples' advice to use "a fresh repository"
   guarantees the dialog for both agents, so the examples text should say to open
   `claude` and `codex` in it once first.
5. **Optional: title-only output is not output for readiness** (`ESC]0;…BEL` /
   `ESC]2;…` chunks do not move `lastOutput`). Codex members then start about 30 s
   sooner (verified). Points 2 and 3 cover a program that is busy while it only
   updates its title.
6. The web reply boxes send `text + '\r'` in one INPUT frame
   (`pages/sessions/[id].vue` 189, `join/[token].vue` 135, `wall.vue` 128): the same bug
   for a Codex reply. xterm.js's `terminal.paste(text)` adds the paste markers by the
   mode; send the `'\r'` as a separate frame after it.

Risks:

- The bytes typed change, so tests that expect one write of `"text\r"` fail. With the
  experiment: 14 tests in `internal/crew` (`TestHandoff*`, `TestLaunchTypesPromptsWhenReady`,
  `TestAMultiLinePromptIsTypedAsOneLine`, `TestAfterCondition*`, `TestEarlyExitDropsPrompt`,
  `TestStartMemberStartsAPendingMember`, `TestAddMemberJoinsARun`,
  `TestNotReadyAfterTheCapTypesAnyway`); `internal/session` and `internal/api` pass.
  The fake processes need a bracketed-paste mode and must accept two writes.
- Shipping 1 without 4 makes Codex trust any untrusted folder it is launched in
  (verified above). They must land together.
- The 250 ms between text and Enter: a prompt raised in that window is not answered
  (handoffs and broadcasts leave the Enter out and the text waits), and `Type`'s
  "answers the needs_input that was showing" rule now spans two writes. A program
  without bracketed paste that reads slowly can still get both writes in one read.
- The extra Enter (3) could answer a question that appears within 3 s without
  needs_input. Claude reports permission questions through its hook, and the check is
  limited to agents that confirm.
- Untested: the other catalog agents (agy, copilot, cursor, opencode, pi, omp, aider,
  goose, amp, dsh, shell). A plain shell with readline turns bracketed paste on, so it
  would get the markers; that is what a terminal paste does.

## Side findings

- A viewer's terminal answers count as a person answering needs_input. After each turn
  Codex asks for the cursor position (`ESC[6n`); the browser's xterm answers through
  INPUT, and `Local.write` records it as that viewer answering and clears the attention:
  `fix-x2-wt` shows needs_input "READY" at 37.4 s and "input READY by nater" at 38.2 s.
  Same in `fix-x2-wt-titlequiet` at 9.6 and 9.9 s.
- Codex's needs_input message is sometimes raw JSON, `{"title":"Reply READY"}`
  (`fix-x2-wt`, `fix-x2-wt-titlequiet`): the codex notify mapper passes a payload field through.
- Codex warns at startup that `features.notify` in the user's `config.toml` is ignored
  (the user's file, not Conductor's).
- Codex 0.160.0 is out; paste-burst behaviour may differ there.

## What a person should check

- The user's run: were the Claude prompts over 800 bytes (the goal included), was it a
  handoff, or was it B? The example prompts are under 800, so B or a handoff is likely.
  The run log does not say which; a note when a confirming agent shows no `working`
  would.
- The fix in the real workbench with the user's crews (four-member todo app), handoffs
  and broadcasts included, and against Codex 0.160.0.
- The trust handling's wording and flow (4) in the UI.
- The other catalog agents with paste-then-Enter.

## Changes outside the evidence directory, and cost

- `~/.claude.json`: entries for the scratch repos `…/pinv/root/c2` (trusted) and
  `…/pinv/root/c3` (untrusted, which Claude adds on any start), left in place.
  Removing them risks clobbering other Claude Code processes' writes; they are harmless.
- `~/.codex/config.toml`: the `x3` trust entry the fix wrote was removed, and the
  `[tui.model_availability_nux]` counter Codex increments on each start was put back.
  The file is identical to the copy taken before (`$E/codex-config.toml.before`).
- A Codex start here launched Codex's per-user app-server daemon (0.160.0, pids 2202597
  and 2214242, cwd `…/pinv/root/x3`). It is Codex's own background service, left running.
- Quota: Claude 7 short turns, 6 of them on `--model haiku` (the user's weekly
  limit showed 96%, then 97%). Codex about 10 short turns and one `codex exec`.
- The test server (pid 2213730 and earlier) and the worktree are gone. The checkout was
  not modified by this work. `docs/features.md` shows as modified, by someone else.
